package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var tickerTestUpgrader = websocket.Upgrader{}

func TestTickerHub_SubscribeAndLatestPrice_KeyedByExchangeAndSymbol(t *testing.T) {
	h := NewTickerHub(context.Background())

	// Same symbol, two different exchanges, must not collide.
	h.SetPrice("bybit", "BTCUSDT", 60000)
	h.SetPrice("binance", "BTCUSDT", 60050)

	if got := h.LatestPrice("bybit", "BTCUSDT"); got != 60000 {
		t.Errorf("LatestPrice(bybit, BTCUSDT) = %v, want 60000", got)
	}
	if got := h.LatestPrice("binance", "BTCUSDT"); got != 60050 {
		t.Errorf("LatestPrice(binance, BTCUSDT) = %v, want 60050", got)
	}
	if got := h.LatestPrice("bybit", "ETHUSDT"); got != 0 {
		t.Errorf("LatestPrice(bybit, ETHUSDT) = %v, want 0 (never set)", got)
	}
}

func TestTickerHub_Subscribe_CallbackFiresOnMatchingExchangeOnly(t *testing.T) {
	h := NewTickerHub(context.Background())

	var bybitCalls, binanceCalls int
	unsubBybit := h.Subscribe("bybit", "BTCUSDT", func(p float64) { bybitCalls++ })
	defer unsubBybit()
	unsubBinance := h.Subscribe("binance", "BTCUSDT", func(p float64) { binanceCalls++ })
	defer unsubBinance()

	h.setPriceAndDispatch(tickerKey{Exchange: "bybit", Symbol: "BTCUSDT"}, 61000)

	if bybitCalls != 1 {
		t.Errorf("bybitCalls = %d, want 1", bybitCalls)
	}
	if binanceCalls != 0 {
		t.Errorf("binanceCalls = %d, want 0 (must not fire for a different exchange's subscription)", binanceCalls)
	}
}

func TestTickerHub_Unsubscribe_StopsCallback(t *testing.T) {
	h := NewTickerHub(context.Background())
	calls := 0
	unsub := h.Subscribe("bybit", "BTCUSDT", func(p float64) { calls++ })
	h.setPriceAndDispatch(tickerKey{Exchange: "bybit", Symbol: "BTCUSDT"}, 100)
	unsub()
	h.setPriceAndDispatch(tickerKey{Exchange: "bybit", Symbol: "BTCUSDT"}, 200)
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (unsubscribe must stop further callbacks)", calls)
	}
}

func TestTickerHub_HandleBybitMessage_ParsesTickerTopic(t *testing.T) {
	h := NewTickerHub(context.Background())
	h.Subscribe("bybit", "BTCUSDT", nil)

	data, _ := json.Marshal(map[string]any{
		"topic": "tickers.BTCUSDT",
		"data":  map[string]any{"markPrice": "62000.5"},
	})
	h.handleMessage("bybit", data)

	if got := h.LatestPrice("bybit", "BTCUSDT"); got != 62000.5 {
		t.Errorf("LatestPrice after Bybit message = %v, want 62000.5", got)
	}
}

func TestTickerHub_ReadDeadline_ReturnsWhenConnectionGoesSilent(t *testing.T) {
	origTimeout := tickerReadTimeout
	tickerReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { tickerReadTimeout = origTimeout })

	mux := http.NewServeMux()
	mux.HandleFunc("/v5/public/linear", func(w http.ResponseWriter, r *http.Request) {
		conn, err := tickerTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the subscribe message, then go silent forever — the same
		// silent-network-black-hole scenario pkg/trader/ws_test.go exercises.
		_, _, _ = conn.ReadMessage()
		_, _, _ = conn.ReadMessage()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origWS := bybitPublicWS
	bybitPublicWS = "ws" + strings.TrimPrefix(srv.URL, "http") + "/v5/public/linear"
	defer func() { bybitPublicWS = origWS }()

	h := NewTickerHub(context.Background())
	h.Subscribe("bybit", "BTCUSDT", nil)

	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if h.ConnCount() == 0 {
				return // success: the dead connection was torn down
			}
		case <-deadline:
			t.Fatal("ticker hub connection was never torn down after going silent — read deadline is not being enforced")
		}
	}
}
