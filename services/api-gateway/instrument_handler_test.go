//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/trader"
)

// TestGetInstrumentConstraints_DispatchesByExchange locks in Plan #4b/#4c's dispatch
// behavior: an explicit exchange=binance query param must route to the Binance public
// instrument-info lookup, not Bybit's — the exact regression this migration fixes (a
// Binance account's qty/price was previously always rounded against Bybit's precision
// rules, or an unrounded value was sent straight through, producing a live Binance
// "-1111 Precision is over the maximum defined for this asset" order rejection).
func TestGetInstrumentConstraints_DispatchesByExchange(t *testing.T) {
	s := newTestServer(t)

	origBybit, origBinance := bybitGetPublicInstrumentInfo, binanceGetPublicInstrumentInfo
	t.Cleanup(func() {
		bybitGetPublicInstrumentInfo, binanceGetPublicInstrumentInfo = origBybit, origBinance
	})

	var gotBybitCall, gotBinanceCall bool
	bybitGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		gotBybitCall = true
		return trader.PubInstrumentInfo{TickSize: 0.1, QtyStep: 0.001, MinQty: 0.001, MinNotionalValue: 5, MaxLeverage: 100}, nil
	}
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		gotBinanceCall = true
		return trader.PubInstrumentInfo{TickSize: 0.01, QtyStep: 0.0001, MinQty: 0.0001, MinNotionalValue: 20}, nil
	}

	// No exchange param: defaults to bybit (backward compatibility).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/instrument-info?symbol=BTCUSDT&category=linear", nil)
	s.GetInstrumentConstraints(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default exchange: got %d: %s", rec.Code, rec.Body.String())
	}
	if !gotBybitCall || gotBinanceCall {
		t.Fatalf("default exchange: bybit called=%v binance called=%v, want bybit only", gotBybitCall, gotBinanceCall)
	}
	var defaultBody trader.PubInstrumentInfo
	if err := json.NewDecoder(rec.Body).Decode(&defaultBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if defaultBody.TickSize != 0.1 {
		t.Errorf("default exchange: tick_size = %v, want bybit's 0.1", defaultBody.TickSize)
	}

	// exchange=binance: must route to the Binance lookup, and must NOT merge in the
	// Bybit-only DB-cached max_leverage (Binance's leverage is bracket-based, not a
	// single scalar — see refreshLeverage's doc comment in leverage_cache.go).
	gotBybitCall, gotBinanceCall = false, false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/instrument-info?symbol=BTCUSDT&category=linear&exchange=binance", nil)
	s.GetInstrumentConstraints(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("binance exchange: got %d: %s", rec.Code, rec.Body.String())
	}
	if gotBybitCall || !gotBinanceCall {
		t.Fatalf("binance exchange: bybit called=%v binance called=%v, want binance only", gotBybitCall, gotBinanceCall)
	}
	var binanceBody trader.PubInstrumentInfo
	if err := json.NewDecoder(rec.Body).Decode(&binanceBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if binanceBody.TickSize != 0.01 {
		t.Errorf("binance exchange: tick_size = %v, want binance's 0.01", binanceBody.TickSize)
	}
	if binanceBody.MaxLeverage != 0 {
		t.Errorf("binance exchange: max_leverage = %v, want 0 (DB leverage cache is bybit-only, must not be merged in)", binanceBody.MaxLeverage)
	}
}

// TestGetInstrumentConstraints_BinanceError_PropagatesAsBadGateway ensures a Binance
// lookup failure surfaces the same way a Bybit one always has (502, not a panic or a
// silently empty 200) — the dispatcher must not swallow errors from either branch.
func TestGetInstrumentConstraints_BinanceError_PropagatesAsBadGateway(t *testing.T) {
	s := newTestServer(t)

	origBinance := binanceGetPublicInstrumentInfo
	t.Cleanup(func() { binanceGetPublicInstrumentInfo = origBinance })
	binanceGetPublicInstrumentInfo = func(ctx context.Context, category, symbol string) (trader.PubInstrumentInfo, error) {
		return trader.PubInstrumentInfo{}, errors.New("binance: no exchangeInfo entry for symbol NOPEUSDT")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/instrument-info?symbol=NOPEUSDT&category=linear&exchange=binance", nil)
	s.GetInstrumentConstraints(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d: %s, want 502", rec.Code, rec.Body.String())
	}
}
