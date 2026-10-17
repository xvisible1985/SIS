package binance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/trader"
)

// Regression target: Binance's WS API rejects (or silently misbehaves on) INT-typed
// params sent as quoted JSON strings — timestamp/recvWindow must be raw JSON numbers;
// everything else (including numeric-looking decimal fields like quantity/price) must
// stay a JSON string, matching Binance's own documented request examples exactly.
func TestWsParamsToJSON_IntFieldsBecomeNumbers_OthersStayStrings(t *testing.T) {
	params := url.Values{
		"symbol":     {"BTCUSDT"},
		"quantity":   {"0.01000000"},
		"price":      {"43187.00"},
		"timestamp":  {"1645423376532"},
		"recvWindow": {"5000"},
		"apiKey":     {"abc123"},
		"signature":  {"deadbeef"},
	}
	got := wsParamsToJSON(params)

	if v, ok := got["timestamp"].(int64); !ok || v != 1645423376532 {
		t.Errorf("timestamp = %#v (%T), want int64(1645423376532)", got["timestamp"], got["timestamp"])
	}
	if v, ok := got["recvWindow"].(int64); !ok || v != 5000 {
		t.Errorf("recvWindow = %#v (%T), want int64(5000)", got["recvWindow"], got["recvWindow"])
	}
	for _, key := range []string{"symbol", "quantity", "price", "apiKey", "signature"} {
		v, ok := got[key].(string)
		if !ok {
			t.Errorf("%s = %#v (%T), want a string", key, got[key], got[key])
			continue
		}
		if v != params.Get(key) {
			t.Errorf("%s = %q, want %q", key, v, params.Get(key))
		}
	}
}

func TestWsParamsToJSON_ReduceOnlyBecomesBool(t *testing.T) {
	params := url.Values{"reduceOnly": {"true"}}
	got := wsParamsToJSON(params)
	v, ok := got["reduceOnly"].(bool)
	if !ok || v != true {
		t.Errorf("reduceOnly = %#v (%T), want bool(true)", got["reduceOnly"], got["reduceOnly"])
	}
}

// wsEchoServer starts a local WS server that, for every request, decodes
// {"id","method","params"} and replies with whatever handler returns for that method —
// giving each test full control over the response without touching the real exchange.
func wsEchoServer(t *testing.T, handler func(method string, params map[string]any) (result any, errCode int, errMsg string)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Id     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			result, errCode, errMsg := handler(req.Method, req.Params)
			resp := map[string]any{"id": req.Id, "status": 200}
			if errCode != 0 {
				resp["status"] = 400
				resp["error"] = map[string]any{"code": errCode, "msg": errMsg}
			} else {
				resp["result"] = result
			}
			out, _ := json.Marshal(resp)
			conn.WriteMessage(websocket.TextMessage, out) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitConnected polls ts.conn until non-nil or 2s elapse — shared by every WS test.
func waitConnected(t *testing.T, ts *BinanceTradeStream) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ts.mu.Lock()
		connected := ts.conn != nil
		ts.mu.Unlock()
		if connected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("BinanceTradeStream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBinanceTradeStream_SendReq_CorrelatesResponseById(t *testing.T) {
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		return map[string]any{"echoedMethod": method, "echoedSymbol": params["symbol"]}, 0, ""
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = wsURL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	resp, err := ts.sendReq(ctx, "test.echo", url.Values{"symbol": {"BTCUSDT"}})
	if err != nil {
		t.Fatalf("sendReq: %v", err)
	}
	var result struct {
		EchoedMethod string `json:"echoedMethod"`
		EchoedSymbol string `json:"echoedSymbol"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.EchoedMethod != "test.echo" || result.EchoedSymbol != "BTCUSDT" {
		t.Errorf("got %+v, want method=test.echo symbol=BTCUSDT", result)
	}
}

func TestBinanceTradeStream_SendReq_SurfacesErrorResponse(t *testing.T) {
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		return nil, -1102, "Mandatory parameter 'quantity' was not sent"
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = wsURL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	resp, err := ts.sendReq(ctx, "test.fail", nil)
	if err != nil {
		t.Fatalf("sendReq transport error: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -1102 {
		t.Errorf("resp.Error = %+v, want code=-1102", resp.Error)
	}
}

func TestBinanceTradeStream_PlaceOrder_RegularOrderUsesOrderPlaceMethod(t *testing.T) {
	var gotMethod string
	var gotSymbol, gotQuantity any
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		gotMethod = method
		gotSymbol = params["symbol"]
		gotQuantity = params["quantity"]
		return map[string]any{"orderId": 500, "clientOrderId": "SIS_STR-abc"}, 0, ""
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	got, err := ts.PlaceOrder(ctx, trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "0.01",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if gotMethod != "order.place" {
		t.Errorf("method = %q, want order.place", gotMethod)
	}
	if gotSymbol != "BTCUSDT" || gotQuantity != "0.01" {
		t.Errorf("symbol/quantity = %v/%v, want BTCUSDT/0.01", gotSymbol, gotQuantity)
	}
	if got.OrderId != "500" || got.OrderLinkId != "SIS_STR-abc" {
		t.Errorf("got %+v, want OrderId=500 OrderLinkId=SIS_STR-abc", got)
	}
}

func TestBinanceTradeStream_PlaceOrder_ConditionalOrderUsesAlgoOrderPlaceMethod(t *testing.T) {
	var gotMethod string
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		gotMethod = method
		return map[string]any{"algoId": 900, "clientAlgoId": "SIS_STR-tp"}, 0, ""
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	got, err := ts.PlaceOrder(ctx, trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000", Qty: "0.01",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if gotMethod != "algoOrder.place" {
		t.Errorf("method = %q, want algoOrder.place", gotMethod)
	}
	if got.OrderId != "900" || got.OrderLinkId != "SIS_STR-tp" {
		t.Errorf("got %+v, want OrderId=900 OrderLinkId=SIS_STR-tp", got)
	}
}

func TestBinanceTradeStream_PlaceOrder_NotConnectedReturnsErrWSUnavailable(t *testing.T) {
	ts := NewBinanceTradeStream(testCreds()) // Run() never called — ts.conn stays nil
	_, err := ts.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if !errors.Is(err, ErrWSUnavailable) {
		t.Errorf("err = %v, want ErrWSUnavailable", err)
	}
}

func TestBinanceTradeStream_PlaceOrder_GenuineRejectionIsNotErrWSUnavailable(t *testing.T) {
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		return nil, -2019, "Margin is insufficient."
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	_, err := ts.PlaceOrder(ctx, trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrWSUnavailable) {
		t.Error("a genuine order rejection must NOT be ErrWSUnavailable — the caller must not retry it on REST (duplicate-submission risk)")
	}
}

func TestBinanceTradeStream_CancelOrder_UsesAlgoOrderCancelWhenOrderFilterIsStopOrder(t *testing.T) {
	var gotMethod string
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		gotMethod = method
		return map[string]any{}, 0, ""
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	if err := ts.CancelOrder(ctx, trader.CancelRequest{Symbol: "BTCUSDT", OrderId: "900", OrderFilter: "StopOrder"}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if gotMethod != "algoOrder.cancel" {
		t.Errorf("method = %q, want algoOrder.cancel", gotMethod)
	}
}

func TestBinanceTradeStream_PlaceOrderBatch_PreservesOriginalIndexOrder(t *testing.T) {
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		switch params["symbol"] {
		case "ETHUSDT":
			return map[string]any{"orderId": 10, "clientOrderId": "eth"}, 0, ""
		case "BTCUSDT":
			return map[string]any{"algoId": 99, "clientAlgoId": "btc-tp"}, 0, ""
		}
		return nil, -1102, "unexpected symbol"
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	got, err := ts.PlaceOrderBatch(ctx, trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000", Qty: "1"}, // index 0: conditional
		{Symbol: "ETHUSDT", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "3000"},            // index 1: regular
	}})
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].OrderId != "99" || got[0].OrderLinkId != "btc-tp" {
		t.Errorf("result[0] (conditional BTCUSDT) = %+v, want OrderId=99 OrderLinkId=btc-tp", got[0])
	}
	if got[1].OrderId != "10" || got[1].OrderLinkId != "eth" {
		t.Errorf("result[1] (regular ETHUSDT) = %+v, want OrderId=10 OrderLinkId=eth", got[1])
	}
}

func TestBinanceTradeStream_CancelOrderBatch_SplitsRegularAndConditional(t *testing.T) {
	var gotMethods []string
	var mu sync.Mutex
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		mu.Lock()
		gotMethods = append(gotMethods, method)
		mu.Unlock()
		return map[string]any{}, 0, ""
	})
	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)
	waitConnected(t, ts)

	err := ts.CancelOrderBatch(ctx, trader.BatchCancelRequest{Request: []trader.BatchCancelItem{
		{Symbol: "BTCUSDT", OrderId: "10"},
		{Symbol: "BTCUSDT", OrderId: "99", OrderFilter: "StopOrder"},
	}})
	if err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotMethods) != 2 {
		t.Fatalf("got %d WS calls, want 2", len(gotMethods))
	}
	wantSet := map[string]bool{"order.cancel": false, "algoOrder.cancel": false}
	for _, m := range gotMethods {
		wantSet[m] = true
	}
	if !wantSet["order.cancel"] || !wantSet["algoOrder.cancel"] {
		t.Errorf("methods = %v, want both order.cancel and algoOrder.cancel", gotMethods)
	}
}
