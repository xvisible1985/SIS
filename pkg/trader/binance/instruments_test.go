package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/trader"
)

// resetExchangeInfoCache clears loadExchangeInfo's package-level cache so each test
// starts cold — exchangeInfoCache/exchangeInfoAt are shared package state, and without
// this a test running after one that already warmed the cache would silently reuse
// stale data instead of hitting the mock server it just set up.
func resetExchangeInfoCache(t *testing.T) {
	t.Helper()
	exchangeInfoMu.Lock()
	exchangeInfoCache = nil
	exchangeInfoAt = time.Time{}
	exchangeInfoMu.Unlock()
}

const sampleExchangeInfoBody = `{"symbols":[
	{"symbol":"BTCUSDT","filters":[
		{"filterType":"PRICE_FILTER","tickSize":"0.10"},
		{"filterType":"LOT_SIZE","stepSize":"0.001","minQty":"0.001"},
		{"filterType":"MIN_NOTIONAL","notional":"100"}
	]},
	{"symbol":"DOGEUSDT","filters":[
		{"filterType":"PRICE_FILTER","tickSize":"0.00001"},
		{"filterType":"LOT_SIZE","stepSize":"1","minQty":"1"},
		{"filterType":"MIN_NOTIONAL","notional":"5"}
	]}
]}`

func TestLoadExchangeInfo_ParsesFiltersAndCachesForFiveMinutes(t *testing.T) {
	resetExchangeInfoCache(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleExchangeInfoBody))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	all, err := loadExchangeInfo(context.Background(), trader.Credentials{})
	if err != nil {
		t.Fatalf("loadExchangeInfo: %v", err)
	}
	btc, ok := all["BTCUSDT"]
	if !ok {
		t.Fatal("expected BTCUSDT entry")
	}
	if btc.TickSize != 0.10 || btc.QtyStep != 0.001 || btc.MinQty != 0.001 || btc.MinNotionalValue != 100 {
		t.Errorf("BTCUSDT info = %+v, want {TickSize:0.10 QtyStep:0.001 MinQty:0.001 MinNotionalValue:100}", btc)
	}

	// Second call within the 5-minute TTL must be served from cache, not hit the server
	// again — the whole point of caching one exchangeInfo fetch for every symbol lookup.
	if _, err := loadExchangeInfo(context.Background(), trader.Credentials{}); err != nil {
		t.Fatalf("loadExchangeInfo (2nd call): %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1 (second call should be served from cache)", hits)
	}
}

func TestGetInstrumentInfo_UnknownSymbol_ReturnsError(t *testing.T) {
	resetExchangeInfoCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleExchangeInfoBody))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	e := NewBinanceExchange(trader.Credentials{})
	if _, err := e.GetInstrumentInfo(context.Background(), "linear", "NOSUCHUSDT"); err == nil {
		t.Fatal("expected an error for a symbol absent from exchangeInfo, got nil")
	}
}

func TestGetPublicInstrumentInfo_MinOrderUSDT_PicksLargerOfMinQtyValueAndMinNotional(t *testing.T) {
	resetExchangeInfoCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			_, _ = w.Write([]byte(sampleExchangeInfoBody))
		case "/fapi/v1/premiumIndex":
			_, _ = w.Write([]byte(`{"markPrice":"50000.00"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	info, err := GetPublicInstrumentInfo(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("GetPublicInstrumentInfo: %v", err)
	}
	if info.TickSize != 0.10 || info.QtyStep != 0.001 || info.MinQty != 0.001 || info.MinNotionalValue != 100 {
		t.Errorf("info = %+v, want tick/step/minQty/minNotional from exchangeInfo", info)
	}
	// minQty(0.001) * markPrice(50000) = 50, which is below MinNotionalValue (100) —
	// MinOrderUSDT must take the larger of the two, so the auto-raise logic that
	// consumes it (bot_engine.go's computeBotStrategyCols) never under-raises grid_size.
	if info.MinOrderUSDT != 100 {
		t.Errorf("MinOrderUSDT = %v, want 100 (minNotional, which exceeds minQty*markPrice=50)", info.MinOrderUSDT)
	}
	// Binance leverage is a per-notional-bracket schedule, not a single scalar — must be
	// left at 0 so callers don't mistake it for a real cap (see the doc comment on
	// GetPublicInstrumentInfo and on leverage_cache.go's refreshLeverage).
	if info.MaxLeverage != 0 {
		t.Errorf("MaxLeverage = %v, want 0 (Binance has no single scalar max leverage)", info.MaxLeverage)
	}
}

func TestGetPublicInstrumentInfo_MinOrderUSDT_UsesMinQtyValueWhenLarger(t *testing.T) {
	resetExchangeInfoCache(t)
	// DOGEUSDT: minQty=1, minNotional=5. At a high mark price, minQty*markPrice clearly
	// exceeds minNotional, so MinOrderUSDT must track minQty*markPrice instead.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			_, _ = w.Write([]byte(sampleExchangeInfoBody))
		case "/fapi/v1/premiumIndex":
			_, _ = w.Write([]byte(`{"markPrice":"1000"}`)) // minQty(1) * 1000 = 1000 > minNotional(5)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	info, err := GetPublicInstrumentInfo(context.Background(), "linear", "DOGEUSDT")
	if err != nil {
		t.Fatalf("GetPublicInstrumentInfo: %v", err)
	}
	if info.MinOrderUSDT != 1000 {
		t.Errorf("MinOrderUSDT = %v, want 1000 (minQty*markPrice, which exceeds minNotional=5)", info.MinOrderUSDT)
	}
}
