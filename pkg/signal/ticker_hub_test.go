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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)

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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)

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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
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

func TestTickerHub_HandleBinanceMessage_ParsesMarkPriceUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
	h.Subscribe("binance", "BTCUSDT", nil)

	// A raw string literal, not json.Marshal(map[string]any{...}), is deliberate here:
	// Go's json.Marshal always alphabetizes map keys, which puts every uppercase key
	// (ASCII < lowercase) before its lowercase counterpart — "P" would always land
	// before "p", so the exact-match "p" field would always be decoded last and always
	// win regardless of whether the P-collision bug is fixed, silently defeating this
	// test's purpose. Binance's actual wire order sends "p" before "P" (see the field
	// order in the real payload example in this package's binanceMarkPriceMsg doc
	// comment), which is the order that actually lets "P" overwrite "p" when the bug is
	// present — so this test reproduces that exact byte order.
	data := []byte(`{"e":"markPriceUpdate","E":1562305380000,"s":"BTCUSDT","p":"63000.25000000","P":"61111.11111111","i":"62999.00000000","r":"0.00038167","T":1562306400000}`)
	h.handleMessage("binance", data)

	if got := h.LatestPrice("binance", "BTCUSDT"); got != 63000.25 {
		t.Errorf("LatestPrice after Binance message = %v, want 63000.25 (must read \"p\", not \"P\" the estimated settle price)", got)
	}
}

func TestTickerHub_HandleBinanceMessage_IgnoresNonMarkPriceEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
	h.Subscribe("binance", "BTCUSDT", nil)

	// Some other Binance event type must not be misparsed into a price update.
	data, _ := json.Marshal(map[string]any{"e": "aggTrade", "s": "BTCUSDT", "p": "1.0"})
	h.handleMessage("binance", data)

	if got := h.LatestPrice("binance", "BTCUSDT"); got != 0 {
		t.Errorf("LatestPrice after a non-markPriceUpdate event = %v, want 0 (must be ignored)", got)
	}
}

func TestTickerHub_HandleBinanceMessage_DoesNotLeakIntoBybitKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := NewTickerHub(ctx)
	h.Subscribe("bybit", "BTCUSDT", nil)
	h.Subscribe("binance", "BTCUSDT", nil)

	data, _ := json.Marshal(map[string]any{"e": "markPriceUpdate", "s": "BTCUSDT", "p": "70000"})
	h.handleMessage("binance", data)

	if got := h.LatestPrice("bybit", "BTCUSDT"); got != 0 {
		t.Errorf("LatestPrice(bybit, BTCUSDT) after a Binance message = %v, want 0 (must stay isolated)", got)
	}
	if got := h.LatestPrice("binance", "BTCUSDT"); got != 70000 {
		t.Errorf("LatestPrice(binance, BTCUSDT) = %v, want 70000", got)
	}
}

func TestTopicFor_LowercasesSymbolForBinanceOnly(t *testing.T) {
	if got := topicFor("binance", "BTCUSDT"); got != "btcusdt@markPrice@1s" {
		t.Errorf("topicFor(binance, BTCUSDT) = %q, want btcusdt@markPrice@1s", got)
	}
	if got := topicFor("bybit", "BTCUSDT"); got != "tickers.BTCUSDT" {
		t.Errorf("topicFor(bybit, BTCUSDT) = %q, want tickers.BTCUSDT (unchanged, uppercase)", got)
	}
}

func TestSubscribeMsg_UsesMethodParamsForBinanceOpArgsForBybit(t *testing.T) {
	bm := subscribeMsg("binance", []string{"btcusdt@markPrice@1s"})
	if bm["method"] != "SUBSCRIBE" {
		t.Errorf("binance subscribeMsg method = %v, want SUBSCRIBE", bm["method"])
	}
	if _, ok := bm["id"]; !ok {
		t.Error("binance subscribeMsg missing id field")
	}

	bb := subscribeMsg("bybit", []string{"tickers.BTCUSDT"})
	if bb["op"] != "subscribe" {
		t.Errorf("bybit subscribeMsg op = %v, want subscribe", bb["op"])
	}
}
