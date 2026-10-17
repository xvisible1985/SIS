package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/trader"
)

// Regression (2026-10-17): confirmed root cause of "orders/positions table never
// updates live without a page refresh" for every Binance account, for roughly the last
// six months. binanceWsBase used to be the pre-migration legacy URL
// ("wss://fstream.binance.com/ws/" + listenKey as a path suffix). Binance's own
// "Important WebSocket Change Notice" split the combined gateway into /public, /market,
// /private, with legacy URLs decommissioned 2026-04-23 — NOT rejected outright but
// silently degraded: connections not migrated keep connecting fine but /private
// channels (ACCOUNT_UPDATE, ORDER_TRADE_UPDATE — exactly what feeds the live orders/
// positions table) simply stop pushing data. Confirmed via a live reproduction before
// this fix: placed a real order on a real account while capturing raw WS frames — got
// the initial REST-driven snapshot, zero delta for the new order, for the connection's
// entire lifetime. This test pins the new URL's exact shape so a future refactor can't
// silently regress back to the legacy, connects-but-dead format.
func TestBinanceUserDataWsURL_UsesPrivateEndpointWithListenKeyAndEventsAsQueryParams(t *testing.T) {
	got := binanceUserDataWsURL("abc123listenkey")
	want := "wss://fstream.binance.com/private/ws?listenKey=abc123listenkey&events=ORDER_TRADE_UPDATE/ACCOUNT_UPDATE"
	if got != want {
		t.Errorf("binanceUserDataWsURL(...) = %q, want %q", got, want)
	}
	if strings.Contains(got, "/ws/abc123listenkey") {
		t.Error("listenKey must be a query parameter, not a path suffix (the legacy, now-decommissioned format)")
	}
}

func TestTranslateAccountUpdate_LongPosition_OneWayMode(t *testing.T) {
	var ev binanceAccountUpdateEvent
	raw := `{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"BTCUSDT","pa":"0.500","ep":"60000.0","up":"125.5","ps":"BOTH"}]}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	got := translateAccountUpdate(ev)
	if len(got) != 1 {
		t.Fatalf("got %d deltas, want 1", len(got))
	}
	p := got[0]
	if p["symbol"] != "BTCUSDT" || p["side"] != "Buy" || p["size"] != "0.500" {
		t.Errorf("got %+v, want symbol=BTCUSDT side=Buy size=0.500", p)
	}
	if p["positionIdx"] != 0 {
		t.Errorf("positionIdx = %v, want 0 (one-way mode)", p["positionIdx"])
	}
	// markPrice must be absent entirely — usePositionsWs.ts merges a delta onto the
	// last known full object, so a present-but-zero value would corrupt it.
	if _, has := p["markPrice"]; has {
		t.Error("delta must not include markPrice — see translateAccountUpdate's doc comment")
	}
}

// Regression: a one-way-mode short still reports positionSide:"BOTH" with a negative
// positionAmt — the sign check must not run as an `else if` chained after the BOTH
// check (that would wrongly route it to the hedge-mode short slot 2 instead of 0).
// Mirrors TestBinanceExchange_FetchPositions_OneWayModeShortGetsPositionIdxZero in
// exchange_test.go, pinned again here since this is a separate code path.
func TestTranslateAccountUpdate_ShortPosition_OneWayModeStillGetsSlotZero(t *testing.T) {
	var ev binanceAccountUpdateEvent
	raw := `{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"ETHUSDT","pa":"-2.000","ep":"3000.0","up":"-50","ps":"BOTH"}]}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	got := translateAccountUpdate(ev)
	if len(got) != 1 {
		t.Fatalf("got %d deltas, want 1", len(got))
	}
	p := got[0]
	if p["side"] != "Sell" || p["size"] != "2.000" {
		t.Errorf("got side=%v size=%v, want Sell/2.000 (sign stripped)", p["side"], p["size"])
	}
	if p["positionIdx"] != 0 {
		t.Errorf("positionIdx = %v, want 0 (one-way mode, not 2)", p["positionIdx"])
	}
}

func TestTranslateAccountUpdate_HedgeModeShort_GetsSlotTwo(t *testing.T) {
	var ev binanceAccountUpdateEvent
	raw := `{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"ETHUSDT","pa":"-2.000","ep":"3000.0","up":"-50","ps":"SHORT"}]}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	got := translateAccountUpdate(ev)
	if got[0]["positionIdx"] != 2 {
		t.Errorf("positionIdx = %v, want 2 (hedge-mode short)", got[0]["positionIdx"])
	}
}

// Regression: ACCOUNT_UPDATE lists every symbol touched, including ones that just
// closed to zero (pa=="0") — those must still be forwarded as a delta (zero size) so
// usePositionsWs.ts's merge logic removes them from the open-positions list, same as
// Bybit's own zero-size close convention. Silently dropping zero-size rows here would
// leave a closed position stuck showing as open until the next full snapshot.
func TestTranslateAccountUpdate_ZeroSizeRow_StillForwarded(t *testing.T) {
	var ev binanceAccountUpdateEvent
	raw := `{"e":"ACCOUNT_UPDATE","a":{"P":[{"s":"BTCUSDT","pa":"0","ep":"0.0","up":"0","ps":"BOTH"}]}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	got := translateAccountUpdate(ev)
	if len(got) != 1 {
		t.Fatalf("got %d deltas, want 1 (zero-size close must still be forwarded)", len(got))
	}
	if got[0]["size"] != "0" {
		t.Errorf("size = %v, want \"0\"", got[0]["size"])
	}
}

func TestTranslateOrderUpdate_StatusChangeOnly_NoExecution(t *testing.T) {
	var ev binanceOrderUpdateEvent
	raw := `{"e":"ORDER_TRADE_UPDATE","o":{"s":"BTCUSDT","c":"myOrder1","S":"BUY","o":"LIMIT","q":"0.5","p":"60000","x":"NEW","X":"NEW","i":123456}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	order, execution, isFill := translateOrderUpdate(ev)
	if isFill || execution != nil {
		t.Errorf("isFill=%v execution=%+v, want no execution for a plain status change", isFill, execution)
	}
	if order["orderId"] != "123456" || order["symbol"] != "BTCUSDT" || order["side"] != "Buy" {
		t.Errorf("order = %+v, want orderId=123456 symbol=BTCUSDT side=Buy", order)
	}
	if order["orderStatus"] != "New" {
		t.Errorf("orderStatus = %v, want New (binanceOrderStatus translation)", order["orderStatus"])
	}
}

func TestTranslateOrderUpdate_Trade_ReturnsExecution(t *testing.T) {
	var ev binanceOrderUpdateEvent
	raw := `{"e":"ORDER_TRADE_UPDATE","o":{"s":"BTCUSDT","c":"myOrder1","S":"SELL","o":"MARKET","q":"0.5","x":"TRADE","X":"FILLED","i":123456,"l":"0.5","L":"61000","T":1700000000000,"t":987}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	order, execution, isFill := translateOrderUpdate(ev)
	if !isFill || execution == nil {
		t.Fatalf("isFill=%v execution=%+v, want a fill", isFill, execution)
	}
	if order["orderStatus"] != "Filled" {
		t.Errorf("orderStatus = %v, want Filled", order["orderStatus"])
	}
	if execution["execId"] != "987" || execution["execPrice"] != "61000" || execution["execQty"] != "0.5" {
		t.Errorf("execution = %+v, want execId=987 execPrice=61000 execQty=0.5", execution)
	}
	if execution["side"] != "Sell" {
		t.Errorf("execution side = %v, want Sell", execution["side"])
	}
}

// Regression (2026-10-17): every fixture above omits the top-level "E" (event time)
// field real Binance payloads always include — which is exactly why this bug (found via
// a live reproduction, not these tests) went undetected: json.Unmarshal into
// binanceOrderUpdateEvent/binanceAccountUpdateEvent/ws.go's local `head` struct used to
// have no field with an EXACT match for "E", so it fell back to a case-insensitive match
// against EventType's "e" tag and failed with "cannot unmarshal number into Go struct
// field .e of type string" — discarding the WHOLE event (head parsing is the very first
// step in ws.go's read loop) before orderStatus/translateOrderUpdate ever ran. This is
// the exact raw payload captured from a real Binance account during the live
// reproduction that found it.
func TestBinanceOrderUpdateEvent_RealPayloadWithTopLevelEventTime_ParsesWithoutError(t *testing.T) {
	raw := `{"e":"ORDER_TRADE_UPDATE","T":1792264475090,"E":1792264475090,"o":{"s":"NEARUSDT","c":"SIS_TRM-478","S":"BUY","o":"LIMIT","f":"GTC","q":"2","p":"4.55","ap":"0","sp":"0","x":"NEW","X":"NEW","i":36776381703,"l":"0","z":"0","L":"0","n":"0","N":"USDT","T":1792264475090,"t":0,"b":"36.7","a":"0","m":false,"R":false,"wt":"CONTRACT_PRICE","ot":"LIMIT","ps":"LONG","cp":false,"rp":"0","pP":false,"si":0,"ss":0,"V":"EXPIRE_MAKER","pm":"NONE","gtd":0,"er":"0"}}`

	var head struct {
		EventType string `json:"e"`
		EventTime int64  `json:"E"`
	}
	if err := json.Unmarshal([]byte(raw), &head); err != nil {
		t.Fatalf("unmarshal head: %v (this exact error — \"cannot unmarshal number into Go struct field .e of type string\" — is the live bug this test pins)", err)
	}
	if head.EventType != "ORDER_TRADE_UPDATE" {
		t.Errorf("head.EventType = %q, want ORDER_TRADE_UPDATE", head.EventType)
	}

	var ev binanceOrderUpdateEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal binanceOrderUpdateEvent: %v", err)
	}
	if ev.EventType != "ORDER_TRADE_UPDATE" {
		t.Errorf("ev.EventType = %q, want ORDER_TRADE_UPDATE", ev.EventType)
	}
	if ev.O.Symbol != "NEARUSDT" || ev.O.OrderStatus != "NEW" {
		t.Errorf("ev.O = %+v, want Symbol=NEARUSDT OrderStatus=NEW", ev.O)
	}
}

func TestBinanceAccountUpdateEvent_RealShapeWithTopLevelEventTime_ParsesWithoutError(t *testing.T) {
	raw := `{"e":"ACCOUNT_UPDATE","T":1792264475090,"E":1792264475090,"a":{"P":[{"s":"BTCUSDT","pa":"0.500","ep":"60000.0","up":"125.5","ps":"BOTH"}]}}`
	var ev binanceAccountUpdateEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal binanceAccountUpdateEvent: %v", err)
	}
	if ev.EventType != "ACCOUNT_UPDATE" {
		t.Errorf("ev.EventType = %q, want ACCOUNT_UPDATE", ev.EventType)
	}
	if len(ev.A.Positions) != 1 || ev.A.Positions[0].Symbol != "BTCUSDT" {
		t.Errorf("ev.A.Positions = %+v, want one BTCUSDT position", ev.A.Positions)
	}
}

var wsTestUpgrader = websocket.Upgrader{}

// TestRunPositionStream_ReturnsWhenBinanceConnectionGoesSilent is the Binance
// counterpart of pkg/trader's TestRunPositionStream_ReturnsWhenBybitConnectionGoesSilent
// — same regression class: no read deadline on the upstream WS would leave the reader
// goroutine blocked in ReadMessage() forever on a silently-dead network path, so the
// frontend-facing conn never closes and the browser never reconnects for a fresh snapshot.
func TestRunPositionStream_ReturnsWhenBinanceConnectionGoesSilent(t *testing.T) {
	origTimeout := binanceReadTimeout
	binanceReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { binanceReadTimeout = origTimeout })

	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/listenKey", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"listenKey":"test-listen-key"}`))
	})
	mux.HandleFunc("/fapi/v3/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/fapi/v1/openOrders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/fapi/v2/account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"0","assets":[]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("listenKey") != "test-listen-key" {
			http.NotFound(w, r)
			return
		}
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Go silent forever — simulating a network path that black-holes without ever
		// sending a close/error. This blocking read only returns once the client
		// (production code) detects its own read-deadline timeout and closes the conn.
		_, _, _ = conn.ReadMessage()
	})
	mux.HandleFunc("/frontend", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origBase := binanceBase
	binanceBase = srv.URL
	defer func() { binanceBase = origBase }()

	origWS := binanceWsBase
	binanceWsBase = "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	defer func() { binanceWsBase = origWS }()

	frontendURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/frontend"
	frontendConn, _, err := websocket.DefaultDialer.Dial(frontendURL, nil)
	if err != nil {
		t.Fatalf("dial frontend sink: %v", err)
	}
	defer frontendConn.Close()

	done := make(chan struct{})
	go func() {
		RunPositionStream(context.Background(), frontendConn, trader.Credentials{APIKey: "k", SecretKey: "s"}, "test", nil)
		close(done)
	}()

	select {
	case <-done:
		// success: RunPositionStream returned instead of hanging forever
	case <-time.After(3 * time.Second):
		t.Fatal("RunPositionStream did not return after the upstream Binance connection went silent — read deadline is not being enforced")
	}
}

// TestRunPositionStream_SurvivesIdlePeriodAsLongAsPingsArrive is the regression test for
// the live bug (2026-10-16): a Binance account with no ACCOUNT_UPDATE/ORDER_TRADE_UPDATE
// activity for binanceReadTimeout used to have its connection torn down and rebuilt even
// though Binance's own protocol-level pings (gorilla auto-replies, but never surfaces
// them to ReadMessage()'s caller) prove the connection is perfectly healthy. Each
// unnecessary reconnect opens a real gap — events firing during the dial+listenKey
// window are never replayed by Binance — during which a just-filled order's position
// update could be silently dropped, visible to the user only after a page refresh
// (fresh REST snapshot). Sending ONLY protocol pings (no data messages at all) for
// longer than binanceReadTimeout must NOT cause RunPositionStream to give up.
func TestRunPositionStream_SurvivesIdlePeriodAsLongAsPingsArrive(t *testing.T) {
	origTimeout := binanceReadTimeout
	binanceReadTimeout = 150 * time.Millisecond
	t.Cleanup(func() { binanceReadTimeout = origTimeout })

	mux := http.NewServeMux()
	mux.HandleFunc("/fapi/v1/listenKey", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"listenKey":"test-listen-key"}`))
	})
	mux.HandleFunc("/fapi/v3/positionRisk", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/fapi/v1/openOrders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/fapi/v2/account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"0","assets":[]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("listenKey") != "test-listen-key" {
			http.NotFound(w, r)
			return
		}
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Send ONLY protocol-level pings, well past binanceReadTimeout in total, and no
		// real data message at all — gorilla answers each with a pong automatically on
		// the client side, which production code must treat as proof of life.
		deadline := time.Now().Add(6 * binanceReadTimeout)
		for time.Now().Before(deadline) {
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				return
			}
			time.Sleep(binanceReadTimeout / 3)
		}
	})
	mux.HandleFunc("/frontend", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origBase := binanceBase
	binanceBase = srv.URL
	defer func() { binanceBase = origBase }()

	origWS := binanceWsBase
	binanceWsBase = "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	defer func() { binanceWsBase = origWS }()

	frontendURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/frontend"
	frontendConn, _, err := websocket.DefaultDialer.Dial(frontendURL, nil)
	if err != nil {
		t.Fatalf("dial frontend sink: %v", err)
	}
	defer frontendConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 6*binanceReadTimeout+500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunPositionStream(ctx, frontendConn, trader.Credentials{APIKey: "k", SecretKey: "s"}, "test", nil)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("RunPositionStream returned early — protocol pings alone should have kept the read deadline refreshed, not torn down a healthy connection")
	case <-time.After(5 * binanceReadTimeout):
		// Still running after 5 timeout-periods' worth of ping-only traffic: the ping
		// handler is correctly refreshing the read deadline instead of giving up.
	}
}
