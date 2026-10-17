# Binance WS Order Placement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **This session's execution note:** per the user's explicit instruction, this plan is executed INLINE in the current session's main working directory (no worktree, no subagent handoff) — a continuation of an ongoing live-debugging session, not a fresh feature-branch workflow. Follow `superpowers:executing-plans`'s batch-with-checkpoints shape, but skip its worktree-setup step.

**Goal:** Binance order placement/cancellation (regular LIMIT/MARKET and conditional/algo
orders, single + batch) goes over a persistent, reconnecting WebSocket connection
(`wss://ws-fapi.binance.com/ws-fapi/v1`), mirroring Bybit's `TradeStream`, instead of a
fresh REST call every time.

**Architecture:** A new `BinanceTradeStream` (pure WS client, no REST knowledge) lives
alongside `BinanceExchange`. `BinanceExchange` gets an optional `ws` field, wired up via
`SetTradeStream` only for live trading accounts (not for the many short-lived/read-only
`ResolveExchange` call sites) — `PlaceOrder`/`CancelOrder`/batch variants try WS first,
fall back to the existing REST implementation on `ErrWSUnavailable`, and never retry a
genuine order-rejection error via a different transport (duplicate-submission risk).

**Tech Stack:** Go, `gorilla/websocket`, existing `pkg/proxy` dialer/HTTP-client
infrastructure, existing `pkg/trader/binance` REST signing helpers (`signParams`/`sign`).

See design spec: `docs/superpowers/specs/2026-10-17-binance-ws-trading-design.md`.

---

## File Structure

- **Create:** `pkg/trader/binance/trade_ws.go` — `BinanceTradeStream` (connection
  lifecycle, signing, request/response correlation, `PlaceOrder`/`PlaceOrderBatch`/
  `CancelOrder`/`CancelOrderBatch`), plus the small `wsParamsToJSON` type-conversion
  helper and `binanceWSConnDenied`/`binanceWSPermanentlyDenied`.
- **Create:** `pkg/trader/binance/trade_ws_test.go` — all new tests for the above.
- **Modify:** `pkg/trader/binance/exchange.go` — add `ws` field, `SetTradeStream`,
  `HasTradeStream` to `BinanceExchange`; change `PlaceOrder`/`CancelOrder`/
  `PlaceOrderBatch`/`CancelOrderBatch` to try WS-then-REST.
- **Modify:** `pkg/trader/binance/exchange_test.go` — new tests for the WS-then-REST
  fallback logic, using a fake `binanceWsOrderClient`.
- **Modify:** `pkg/strategy/engine.go` — extract `wireBinanceTradeStream`, call it from
  `AccountRunner.run()` for Binance accounts.
- **Modify:** `pkg/strategy/engine_test.go` — test for `wireBinanceTradeStream`.

---

### Task 1: `wsParamsToJSON` — string params → correctly-typed WS JSON

Binance's WS API expects INT-typed fields (`timestamp`, `recvWindow`) as unquoted JSON
numbers and everything else (symbol, side, quantity, price, etc.) as JSON strings — but
signing must still happen over the REST-style encoded query string (all-strings), via the
*existing* `signParams` helper. This task builds the pure conversion function that bridges
the two, with zero network/WS code yet.

**Files:**
- Create: `pkg/trader/binance/trade_ws.go`
- Create: `pkg/trader/binance/trade_ws_test.go`

- [ ] **Step 1: Write the failing test**

```go
// pkg/trader/binance/trade_ws_test.go
package binance

import (
	"net/url"
	"testing"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run TestWsParamsToJSON -v`
Expected: FAIL — `wsParamsToJSON` undefined (build error).

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/trader/binance/trade_ws.go
package binance

import (
	"net/url"
	"strconv"
)

// binanceWSIntParams is the fixed set of WS request fields Binance expects as JSON
// integers rather than strings — confirmed via Binance's own documented example
// payloads (INT-typed params like timestamp/recvWindow are unquoted; DECIMAL/STRING
// params like quantity/price/symbol stay quoted, exactly as REST's form-encoded body
// already treats them as strings for signing purposes).
var binanceWSIntParams = map[string]bool{
	"timestamp":  true,
	"recvWindow": true,
}

// binanceWSBoolParams is the (currently singleton) set of WS request fields Binance
// expects as a JSON boolean rather than a string — REST sends "true" as a literal
// string in the form-encoded body; the WS JSON envelope expects a real `true`.
var binanceWSBoolParams = map[string]bool{
	"reduceOnly": true,
}

// wsParamsToJSON converts a signed url.Values (built and signed exactly like a REST
// request — see signParams in client.go) into the map[string]any the WS JSON envelope's
// "params" object needs, converting only the fields Binance expects as non-string JSON
// types. The signature itself is unaffected: it's computed over params.Encode() (the
// all-strings query form) BEFORE this conversion ever runs, so signing math never needs
// to know about WS-specific JSON typing at all.
func wsParamsToJSON(params url.Values) map[string]any {
	out := make(map[string]any, len(params))
	for k := range params {
		v := params.Get(k)
		switch {
		case binanceWSIntParams[k]:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[k] = n
				continue
			}
		case binanceWSBoolParams[k]:
			if b, err := strconv.ParseBool(v); err == nil {
				out[k] = b
				continue
			}
		}
		out[k] = v
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/trader/binance/... -run TestWsParamsToJSON -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/trade_ws.go pkg/trader/binance/trade_ws_test.go
git commit -m "feat(binance): add WS params JSON-type conversion helper

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: `BinanceTradeStream` connection lifecycle + request/response correlation

Builds the persistent connection, signing, and id-correlated request/response plumbing —
no order-specific logic yet (that's Task 3+). Tested against a local
`httptest`/`gorilla/websocket` server, same pattern as `pkg/trader/binance/ws_test.go`.

**Files:**
- Modify: `pkg/trader/binance/trade_ws.go`
- Modify: `pkg/trader/binance/trade_ws_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Replace trade_ws_test.go's import block (Task 1 only needed net/url+testing; this
// task's tests need the full set below):
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)
```

```go
// Append the rest of this task's code to trade_ws_test.go:

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

func TestBinanceTradeStream_SendReq_CorrelatesResponseById(t *testing.T) {
	srv := wsEchoServer(t, func(method string, params map[string]any) (any, int, string) {
		return map[string]any{"echoedMethod": method, "echoedSymbol": params["symbol"]}, 0, ""
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ts := NewBinanceTradeStream(testCreds())
	ts.dialURL = wsURL // test-only override, added below
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ts.Run(ctx)

	// Wait for the connection to come up (runOnce sets ts.conn under ts.mu).
	deadline := time.Now().Add(2 * time.Second)
	for {
		ts.mu.Lock()
		connected := ts.conn != nil
		ts.mu.Unlock()
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("BinanceTradeStream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

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

	deadline := time.Now().Add(2 * time.Second)
	for {
		ts.mu.Lock()
		connected := ts.conn != nil
		ts.mu.Unlock()
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("BinanceTradeStream never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := ts.sendReq(ctx, "test.fail", nil)
	if err != nil {
		t.Fatalf("sendReq transport error: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -1102 {
		t.Errorf("resp.Error = %+v, want code=-1102", resp.Error)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run TestBinanceTradeStream_SendReq -v`
Expected: FAIL — `NewBinanceTradeStream`/`dialURL`/`sendReq`/`binanceWSResp` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Replace trade_ws.go's import block (Task 1 only needed net/url+strconv; this task
// needs the full set below — `fmt` is deliberately NOT included yet, since nothing in
// THIS task uses it; Task 3 adds it when it introduces fmt.Errorf):
import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/proxy"
	"sis/pkg/trader"
)
```

```go
// Append the rest of this task's code to trade_ws.go, below wsParamsToJSON:

// binanceTradeWS is the Binance USDⓈ-M Futures WebSocket API endpoint for order
// placement/cancellation — a separate service from both the market-data streams
// (pkg/signal) and the account/user-data stream (ws.go's binanceWsBase); Binance
// requires these on independent connections.
const binanceTradeWS = "wss://ws-fapi.binance.com/ws-fapi/v1"

// ErrWSUnavailable is returned by BinanceTradeStream's order methods whenever the
// failure is about the CONNECTION (not connected, a permission/auth-shaped rejection, or
// a timeout) rather than a genuine order outcome — callers (BinanceExchange) treat this
// as "retry on REST", and must NEVER retry any other error (a real order
// acceptance/rejection) via a different transport, to avoid a duplicate submission.
var ErrWSUnavailable = errors.New("binance trade ws: unavailable")

// binanceWSResp is one WS API response envelope — see
// developers.binance.com/docs/derivatives/usds-margined-futures/websocket-api-general-info
// for the documented shape: {id, status, result} on success, {id, status, error:{code,msg}}
// on failure.
type binanceWSResp struct {
	Id     string          `json:"id"`
	Status int             `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	} `json:"error"`
}

// BinanceTradeStream maintains a persistent WebSocket to Binance's order-placement WS
// API. Each request is individually signed (HMAC-SHA256, same math as REST — see
// sendReq) and correlated to its response via a client-chosen "id". Deliberately has NO
// REST fallback logic of its own (unlike Bybit's *TradeStream) — see
// ErrWSUnavailable's doc comment; BinanceExchange owns that decision instead.
type BinanceTradeStream struct {
	creds trader.Credentials
	mu    sync.Mutex
	conn  *websocket.Conn // nil when not connected
	seq   int64
	pending    map[string]chan binanceWSResp
	wsDisabled bool // set after a permanent auth/permission error — see binanceWSPermanentlyDenied

	// dialURL overrides binanceTradeWS — test-only (set directly by trade_ws_test.go);
	// the zero value (empty string) means "use the real endpoint".
	dialURL string
}

// NewBinanceTradeStream builds a BinanceTradeStream for one account's credentials. The
// connection isn't established until Run is called.
func NewBinanceTradeStream(creds trader.Credentials) *BinanceTradeStream {
	return &BinanceTradeStream{
		creds:   creds,
		pending: make(map[string]chan binanceWSResp),
	}
}

// Run connects and maintains the trade WS, reconnecting every 5s on failure. Blocks
// until ctx is cancelled — call as `go safeLoop(ctx, "...", ts.Run)`, mirroring
// pkg/trader/trade_ws.go's TradeStream.Run exactly.
func (ts *BinanceTradeStream) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := ts.runOnce(ctx); err != nil {
			log.Printf("binance trade ws: %v, retry in 5s", err)
		}
		ts.mu.Lock()
		ts.conn = nil
		for id, ch := range ts.pending {
			select {
			case ch <- binanceWSResp{Id: id, Status: 0}: // synthetic "disconnected" — sendReq's caller sees a zero-value response and ts.conn==nil decides ErrWSUnavailable
			default:
			}
			delete(ts.pending, id)
		}
		ts.mu.Unlock()
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (ts *BinanceTradeStream) runOnce(ctx context.Context) error {
	dialer, err := proxy.WSDialerFor(ts.creds.WhitelistedIPs)
	if err != nil {
		return err
	}
	target := binanceTradeWS
	if ts.dialURL != "" {
		target = ts.dialURL
	}
	conn, _, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	ts.mu.Lock()
	ts.conn = conn
	ts.mu.Unlock()
	log.Printf("binance trade ws: connected")

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
	// Same ping-deadline handling already proven for the account-data stream (ws.go) —
	// Binance's server-side ping every ~3 minutes must refresh our read deadline, or a
	// perfectly healthy but quiet connection gets torn down for no reason.
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
			select {
			case msgCh <- data:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return nil
		case err := <-errCh:
			return err
		case data := <-msgCh:
			var resp binanceWSResp
			if json.Unmarshal(data, &resp) != nil || resp.Id == "" {
				continue
			}
			ts.mu.Lock()
			ch, ok := ts.pending[resp.Id]
			if ok {
				delete(ts.pending, resp.Id)
			}
			ts.mu.Unlock()
			if ok {
				select {
				case ch <- resp:
				default:
				}
			}
		}
	}
}

// sendReq signs params (identical math to REST's signParams — see client.go), converts
// them to the WS envelope's JSON types (wsParamsToJSON), and sends {id, method, params}
// over the connection, then blocks for the matching response (by id) or a 10s timeout.
// Caller must not hold ts.mu. Returns ErrWSUnavailable (never a transport error value)
// whenever the problem is connection-level rather than a genuine response.
func (ts *BinanceTradeStream) sendReq(ctx context.Context, method string, params url.Values) (binanceWSResp, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("apiKey", ts.creds.APIKey)
	signed := signParams(ts.creds, params)

	ts.mu.Lock()
	conn := ts.conn
	if conn == nil {
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	}
	ts.seq++
	id := strconv.FormatInt(ts.seq, 10)
	ch := make(chan binanceWSResp, 1)
	ts.pending[id] = ch

	msg, _ := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": wsParamsToJSON(signed),
	})
	err := conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	}
	ts.mu.Unlock()

	select {
	case resp := <-ch:
		if resp.Status == 0 {
			// Synthetic disconnect response from Run's cleanup (see Run's comment).
			return binanceWSResp{}, ErrWSUnavailable
		}
		return resp, nil
	case <-time.After(10 * time.Second):
		ts.mu.Lock()
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	case <-ctx.Done():
		ts.mu.Lock()
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ctx.Err()
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceTradeStream_SendReq|TestWsParamsToJSON' -v`
Expected: PASS (all 4 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/trade_ws.go pkg/trader/binance/trade_ws_test.go
git commit -m "feat(binance): add BinanceTradeStream connection + request correlation

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: `PlaceOrder`/`CancelOrder` over WS (regular + algo orders)

**Files:**
- Modify: `pkg/trader/binance/trade_ws.go`
- Modify: `pkg/trader/binance/trade_ws_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Add "errors" to trade_ws_test.go's import block (used below by errors.Is) — the
// file's imports should now read: context, encoding/json, errors, net/http,
// net/http/httptest, net/url, strings, testing, time, github.com/gorilla/websocket.
```

```go
// Append to pkg/trader/binance/trade_ws_test.go:

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

// waitConnected polls ts.conn until non-nil or 2s elapse — shared by every test above.
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
```

Also replace the two earlier tests' inline connect-wait loops with calls to `waitConnected(t, ts)` (same helper, now shared — DRY) and remove the now-duplicated inline loops from Task 2's two tests.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run TestBinanceTradeStream_PlaceOrder -v`
Expected: FAIL — `PlaceOrder`/`CancelOrder` undefined on `*BinanceTradeStream`.

- [ ] **Step 3: Write minimal implementation**

```go
// Add "fmt" to trade_ws.go's import block (used below by fmt.Errorf) — the file's
// imports should now read: context, encoding/json, errors, fmt, log, net/url, strconv,
// sync, time, gorilla/websocket, sis/pkg/proxy, sis/pkg/trader.
```

```go
// Append to pkg/trader/binance/trade_ws.go

// binanceWSConnDenied reports whether code means the WS request never reached real
// order validation at all — connection/auth/signature/clock-shaped, not a genuine order
// outcome — so falling back to REST for THIS call is safe: nothing was placed or
// rejected on Binance's side yet, so there is no duplicate-submission risk. -2015 is the
// exact code from the 2026-10-16 incident ("Invalid API-key, IP, or permissions");
// -1002 is "not authorized for this endpoint"; -1022/-1021 are a bad signature or a
// timestamp outside recvWindow — Binance's equivalent of pkg/trader/trade_ws.go's
// wsPermDenied (Bybit), just split further below for the permanent-disable decision.
func binanceWSConnDenied(code int) bool {
	switch code {
	case -2015, -1002, -1022, -1021:
		return true
	}
	return false
}

// binanceWSPermanentlyDenied is the narrower subset of binanceWSConnDenied that
// indicates a real, persistent account/key misconfiguration (wrong IP, wrong key, a
// permission the key genuinely lacks) rather than something that could be a one-off
// blip — only -2015/-1002 set wsDisabled permanently. -1022 (bad signature) and -1021
// (timestamp/recvWindow) do NOT: both fall back to REST for this one call via
// binanceWSConnDenied, but get a fresh, un-disabled chance at WS on the very next
// call — a transient clock-sync hiccup must not permanently strand the account on REST
// for the rest of its runtime.
func binanceWSPermanentlyDenied(code int) bool {
	switch code {
	case -2015, -1002:
		return true
	}
	return false
}

func (ts *BinanceTradeStream) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if disabled {
		return trader.OrderResult{}, ErrWSUnavailable
	}

	orderType := binanceOrderType(req)
	method := "order.place"
	params := url.Values{
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {orderType},
		"positionSide": {binancePositionSide(req.PositionIdx)},
	}
	if isAlgoOrderType(orderType) {
		method = "algoOrder.place"
		params.Set("algoType", "CONDITIONAL")
		if req.TriggerPrice != "" {
			params.Set("triggerPrice", req.TriggerPrice)
		}
		if req.TriggerPrice != "" && req.TriggerBy == "MarkPrice" {
			params.Set("workingType", "MARK_PRICE")
		}
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	if req.Price != "" {
		params.Set("price", req.Price)
	}
	if req.ReduceOnly && req.PositionIdx == 0 {
		params.Set("reduceOnly", "true")
	}
	if req.TimeInForce != "" && orderType == "LIMIT" {
		params.Set("timeInForce", req.TimeInForce)
	}
	if req.OrderLinkId != "" {
		idField := "newClientOrderId"
		if method == "algoOrder.place" {
			idField = "clientAlgoId"
		}
		params.Set(idField, truncateClientOrderID(req.OrderLinkId))
	}

	resp, err := ts.sendReq(ctx, method, params)
	if err != nil {
		return trader.OrderResult{}, err
	}
	if resp.Error != nil {
		if binanceWSConnDenied(resp.Error.Code) {
			if binanceWSPermanentlyDenied(resp.Error.Code) {
				ts.mu.Lock()
				ts.wsDisabled = true
				ts.mu.Unlock()
			}
			return trader.OrderResult{}, ErrWSUnavailable
		}
		return trader.OrderResult{}, fmt.Errorf("binance: code=%d: %s", resp.Error.Code, resp.Error.Msg)
	}
	if method == "algoOrder.place" {
		var r struct {
			AlgoId       int64  `json:"algoId"`
			ClientAlgoId string `json:"clientAlgoId"`
		}
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			return trader.OrderResult{}, fmt.Errorf("parse algoOrder.place response: %w", err)
		}
		return trader.OrderResult{OrderId: strconv.FormatInt(r.AlgoId, 10), OrderLinkId: r.ClientAlgoId}, nil
	}
	var r struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		return trader.OrderResult{}, fmt.Errorf("parse order.place response: %w", err)
	}
	return trader.OrderResult{OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId}, nil
}

func (ts *BinanceTradeStream) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if disabled {
		return ErrWSUnavailable
	}

	method := "order.cancel"
	idField, origIdField := "orderId", "origClientOrderId"
	if req.OrderFilter == "StopOrder" {
		method = "algoOrder.cancel"
		idField, origIdField = "algoId", "clientAlgoId"
	}
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set(idField, req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set(origIdField, truncateClientOrderID(req.OrderLinkId))
	}

	resp, err := ts.sendReq(ctx, method, params)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		if binanceWSConnDenied(resp.Error.Code) {
			if binanceWSPermanentlyDenied(resp.Error.Code) {
				ts.mu.Lock()
				ts.wsDisabled = true
				ts.mu.Unlock()
			}
			return ErrWSUnavailable
		}
		return fmt.Errorf("binance: code=%d: %s", resp.Error.Code, resp.Error.Msg)
	}
	return nil
}
```

Remove the `var _ = fmt.Sprintf` placeholder line added in Task 2 — `fmt` is now genuinely
used by `fmt.Errorf` above.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/trader/binance/... -run 'TestBinanceTradeStream' -v`
Expected: PASS (all tests from Tasks 2 and 3).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/trade_ws.go pkg/trader/binance/trade_ws_test.go
git commit -m "feat(binance): PlaceOrder/CancelOrder over WS (regular + algo orders)

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: `PlaceOrderBatch`/`CancelOrderBatch` — concurrent WS fan-out

Mirrors the exact regular/algo split already written in today's REST
`BinanceExchange.PlaceOrderBatch`/`CancelOrderBatch` (`pkg/trader/binance/exchange.go`),
firing N `PlaceOrder`/`CancelOrder` WS calls concurrently over the shared connection
instead of sequentially — there is no native multi-order WS method.

**Files:**
- Modify: `pkg/trader/binance/trade_ws.go`
- Modify: `pkg/trader/binance/trade_ws_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Add "sync" to trade_ws_test.go's import block (used below by sync.Mutex in
// TestBinanceTradeStream_CancelOrderBatch_SplitsRegularAndConditional).
```

```go
// Append to pkg/trader/binance/trade_ws_test.go:

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run 'PlaceOrderBatch|CancelOrderBatch' -v`
Expected: FAIL — `PlaceOrderBatch`/`CancelOrderBatch` undefined on `*BinanceTradeStream`.

- [ ] **Step 3: Write minimal implementation**

```go
// Append to pkg/trader/binance/trade_ws.go ("sync" is already imported — added back in
// Task 2 for sync.Mutex — so no import-block change is needed for this task).

func (ts *BinanceTradeStream) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	out := make([]trader.BatchPlaceResult, len(req.Request))
	var wg sync.WaitGroup
	for i, it := range req.Request {
		wg.Add(1)
		go func(i int, it trader.BatchOrderItem) {
			defer wg.Done()
			res, err := ts.PlaceOrder(ctx, trader.OrderRequest{
				Symbol: it.Symbol, Side: it.Side, OrderType: it.OrderType, Qty: it.Qty,
				Price: it.Price, TriggerPrice: it.TriggerPrice, TimeInForce: it.TimeInForce,
				OrderFilter: it.OrderFilter, ReduceOnly: it.ReduceOnly,
				PositionIdx: it.PositionIdx, OrderLinkId: it.OrderLinkId,
			})
			if err != nil {
				out[i] = trader.BatchPlaceResult{Code: -1, Msg: err.Error()}
				return
			}
			out[i] = trader.BatchPlaceResult{OrderId: res.OrderId, OrderLinkId: res.OrderLinkId}
		}(i, it)
	}
	wg.Wait()
	return out, nil
}

func (ts *BinanceTradeStream) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	var wg sync.WaitGroup
	errs := make([]error, len(req.Request))
	for i, it := range req.Request {
		wg.Add(1)
		go func(i int, it trader.BatchCancelItem) {
			defer wg.Done()
			errs[i] = ts.CancelOrder(ctx, trader.CancelRequest{
				Symbol: it.Symbol, OrderId: it.OrderId, OrderFilter: it.OrderFilter,
			})
		}(i, it)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err // first error wins, matching the existing REST CancelOrderBatch's reporting granularity
		}
	}
	return nil
}
```

Note: if `PlaceOrderBatch` returns one item with `Code: -1` (a WS-level failure for just
that item, e.g. `ErrWSUnavailable`), `BinanceExchange.PlaceOrderBatch` (Task 6) decides
per-item REST fallback — `BinanceTradeStream.PlaceOrderBatch` itself does not retry.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/trader/binance/... -v`
Expected: PASS — every test in the package, including Tasks 1-3's.

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/trade_ws.go pkg/trader/binance/trade_ws_test.go
git commit -m "feat(binance): PlaceOrderBatch/CancelOrderBatch as concurrent WS fan-out

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: Wire `BinanceTradeStream` into `BinanceExchange` with REST fallback

**Files:**
- Modify: `pkg/trader/binance/exchange.go`
- Modify: `pkg/trader/binance/exchange_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Append to pkg/trader/binance/exchange_test.go

// fakeBinanceWsOrderClient is a hand-written stand-in for binanceWsOrderClient — lets
// exchange_test.go assert BinanceExchange's WS-then-REST fallback decision without a
// real network dial, mirroring pkg/trader/exchange_test.go's fakeWSOrderClient for Bybit.
type fakeBinanceWsOrderClient struct {
	placeOrderResp trader.OrderResult
	placeOrderErr  error
	cancelOrderErr error
}

func (f *fakeBinanceWsOrderClient) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return f.placeOrderResp, f.placeOrderErr
}
func (f *fakeBinanceWsOrderClient) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	return nil, f.placeOrderErr
}
func (f *fakeBinanceWsOrderClient) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	return f.cancelOrderErr
}
func (f *fakeBinanceWsOrderClient) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	return f.cancelOrderErr
}

func TestBinanceExchange_PlaceOrder_UsesWSResultWhenAvailable(t *testing.T) {
	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderResp: trader.OrderResult{OrderId: "1", OrderLinkId: "ws-order"}})

	got, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderLinkId != "ws-order" {
		t.Errorf("got %+v, want the WS result, not a REST call", got)
	}
}

func TestBinanceExchange_PlaceOrder_FallsBackToRESTOnErrWSUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":2,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderErr: ErrWSUnavailable})

	got, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderLinkId != "rest-order" {
		t.Errorf("got %+v, want the REST fallback's result", got)
	}
}

func TestBinanceExchange_PlaceOrder_DoesNotFallBackOnGenuineRejection(t *testing.T) {
	var hitREST bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitREST = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":3,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	wantErr := errors.New("binance: code=-2019: Margin is insufficient.")
	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderErr: wantErr})

	_, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err == nil || err.Error() != wantErr.Error() {
		t.Errorf("err = %v, want %v (no REST retry)", err, wantErr)
	}
	if hitREST {
		t.Error("a genuine order rejection must not fall back to REST — duplicate-submission risk")
	}
}

func TestBinanceExchange_HasTradeStream_FalseUntilSetTradeStream(t *testing.T) {
	ex := NewBinanceExchange(testCreds())
	if ex.HasTradeStream() {
		t.Error("HasTradeStream() = true before SetTradeStream was ever called")
	}
	ex.SetTradeStream(NewBinanceTradeStream(testCreds()))
	if !ex.HasTradeStream() {
		t.Error("HasTradeStream() = false after SetTradeStream")
	}
}

func TestBinanceExchange_PlaceOrderREST_NeverTouchesWS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":4,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderResp: trader.OrderResult{OrderLinkId: "WOULD-BE-WRONG-IF-USED"}})

	got, err := ex.PlaceOrderREST(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrderREST: %v", err)
	}
	if got.OrderLinkId != "rest-order" {
		t.Errorf("got %+v, want PlaceOrderREST to always use REST, ignoring any wired ws client", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/trader/binance/... -run TestBinanceExchange_PlaceOrder -v`
Expected: FAIL — `setWsOrderClientForTest`/`HasTradeStream`/`SetTradeStream` undefined on
`*BinanceExchange`.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/trader/binance/exchange.go — modify the BinanceExchange struct and constructor,
// and PlaceOrder/CancelOrder/PlaceOrderBatch/CancelOrderBatch.

// binanceWsOrderClient is the subset of *BinanceTradeStream's methods BinanceExchange
// needs for the WS-backed order operations — exists only so exchange_test.go can
// substitute a fake, mirroring pkg/trader/exchange.go's wsOrderClient for Bybit.
type binanceWsOrderClient interface {
	PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req trader.CancelRequest) error
	CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error
}

type BinanceExchange struct {
	creds trader.Credentials
	ws    binanceWsOrderClient // nil until SetTradeStream is called — PlaceOrder/CancelOrder/batch fall straight through to REST
}

// NewBinanceExchange builds a BinanceExchange for one account's credentials. ws stays
// nil (pure REST, today's exact behavior) until SetTradeStream is called — most
// ResolveExchange call sites (closed_pnl_syncer.go, bot_engine.go, trader_handler.go)
// deliberately never call it, since those are short-lived/read-mostly resolutions that
// must not each open a persistent WS connection. Only AccountRunner's own long-lived
// per-account lifecycle (pkg/strategy/engine.go) calls SetTradeStream.
func NewBinanceExchange(creds trader.Credentials) *BinanceExchange {
	return &BinanceExchange{creds: creds}
}

// SetTradeStream wires a live *BinanceTradeStream into this exchange — see
// NewBinanceExchange's doc comment for who should (and shouldn't) call this.
func (e *BinanceExchange) SetTradeStream(ws *BinanceTradeStream) {
	e.ws = ws
}

// setWsOrderClientForTest is test-only — lets exchange_test.go substitute a
// fakeBinanceWsOrderClient without needing a real *BinanceTradeStream (which would
// require a real or test WS connection). SetTradeStream (above) is the real, exported
// entry point every non-test caller uses.
func (e *BinanceExchange) setWsOrderClientForTest(ws binanceWsOrderClient) {
	e.ws = ws
}

// HasTradeStream reports whether a trade WS has been wired in — used by
// pkg/strategy/engine.go's wireBinanceTradeStream test to confirm SetTradeStream was
// actually reached, without exposing the (unexported) ws field itself.
func (e *BinanceExchange) HasTradeStream() bool {
	return e.ws != nil
}

func (e *BinanceExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	if e.ws != nil {
		result, err := e.ws.PlaceOrder(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return result, err
		}
		log.Printf("binance trade ws: PlaceOrder fallback to REST (%v)", err)
	}
	return e.placeOrder(ctx, req)
}

func (e *BinanceExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	if e.ws != nil {
		err := e.ws.CancelOrder(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return err
		}
		log.Printf("binance trade ws: CancelOrder fallback to REST (%v)", err)
	}
	return e.cancelOrder(ctx, req)
}

func (e *BinanceExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	if e.ws != nil {
		results, err := e.ws.PlaceOrderBatch(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return results, err
		}
		log.Printf("binance trade ws: PlaceOrderBatch fallback to REST (%v)", err)
	}
	return e.placeOrderBatchREST(ctx, req)
}

func (e *BinanceExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	if e.ws != nil {
		err := e.ws.CancelOrderBatch(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return err
		}
		log.Printf("binance trade ws: CancelOrderBatch fallback to REST (%v)", err)
	}
	return e.cancelOrderBatchREST(ctx, req)
}
```

Also:
- Rename the existing `(e *BinanceExchange) PlaceOrderBatch` body (the REST
  implementation) to `placeOrderBatchREST` (keep every line of its current logic
  unchanged — only the method name changes), and existing `CancelOrderBatch`'s body to
  `cancelOrderBatchREST`, the same way `placeOrder`/`cancelOrder` are already private
  helpers `PlaceOrder`/`PlaceOrderREST` both delegate to.
- `exchange.go`'s current import block is `context, encoding/json, fmt, math, net/url,
  sort, strconv, strings, time, sis/pkg/trader` — neither `"errors"` (used by
  `errors.Is` above) nor `"log"` (used by `log.Printf` above) is in it yet. Add both.
- `PlaceOrderREST`/`CancelOrderREST` stay exactly as they are today (`return
  e.placeOrder(ctx, req)` / `return e.cancelOrder(ctx, req)`) — never look at `e.ws`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/trader/binance/... -v`
Expected: PASS — every test in the package, old and new (the ~40 pre-existing
`NewBinanceExchange(testCreds())`-based tests never call `SetTradeStream`, so `e.ws`
stays nil for them and they keep exercising pure REST exactly as before).

- [ ] **Step 5: Commit**

```bash
git add pkg/trader/binance/exchange.go pkg/trader/binance/exchange_test.go
git commit -m "feat(binance): wire BinanceTradeStream into BinanceExchange with REST fallback

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 6: Start the trade stream per Binance account (`pkg/strategy/engine.go`)

**Files:**
- Modify: `pkg/strategy/engine.go`
- Modify: `pkg/strategy/engine_test.go`

- [ ] **Step 1: Write the failing test**

```go
// Add "sis/pkg/trader/binance" to engine_test.go's import block (used below by
// binance.NewBinanceExchange) — the file's imports should now read: context, math,
// testing, time, sis/pkg/trader, sis/pkg/trader/binance.
```

```go
// Append to pkg/strategy/engine_test.go:

func TestWireBinanceTradeStream_BinanceExchange_WiresAndReturnsStream(t *testing.T) {
	ex := binance.NewBinanceExchange(trader.Credentials{})
	stream := wireBinanceTradeStream(ex, trader.Credentials{})
	if stream == nil {
		t.Fatal("expected a non-nil *binance.BinanceTradeStream")
	}
	if !ex.HasTradeStream() {
		t.Error("wireBinanceTradeStream did not actually call SetTradeStream on the exchange")
	}
}

func TestWireBinanceTradeStream_BybitExchange_ReturnsNil(t *testing.T) {
	ex := trader.NewBybitExchange(trader.Credentials{}, nil)
	stream := wireBinanceTradeStream(ex, trader.Credentials{})
	if stream != nil {
		t.Error("expected nil for a non-Binance exchange")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/strategy/... -run TestWireBinanceTradeStream -v`
Expected: FAIL — `wireBinanceTradeStream` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// pkg/strategy/engine.go — add near ResolveExchange, and modify run().

// wireBinanceTradeStream, if ex is a *binance.BinanceExchange, builds a
// *binance.BinanceTradeStream, wires it in via SetTradeStream, and returns it (nil for
// any other Exchange implementation). Split out of run() so this decision is unit-
// testable without starting a goroutine or blocking on ctx.Done() — run() itself only
// decides whether to start the returned stream's Run loop.
func wireBinanceTradeStream(ex trader.Exchange, creds trader.Credentials) *binance.BinanceTradeStream {
	be, ok := ex.(*binance.BinanceExchange)
	if !ok {
		return nil
	}
	stream := binance.NewBinanceTradeStream(creds)
	be.SetTradeStream(stream)
	return stream
}

func (ar *AccountRunner) run(ctx context.Context) {
	go safeLoop(ctx, "reconcile/"+ar.accountID, ar.startReconcileLoop)
	go safeLoop(ctx, "riskMonitor/"+ar.accountID, ar.runRiskMonitorLoop)

	if ar.exchangeName == "binance" {
		if stream := wireBinanceTradeStream(ar.exchange, ar.creds); stream != nil {
			go safeLoop(ctx, "binanceTradeStream/"+ar.accountID, stream.Run)
		}
		<-ctx.Done()
		return
	}

	// tradeStream (Bybit's low-latency order-placement WS) and RunPrivateStream
	// (Bybit's private fill/position push WS) are both Bybit-specific with no Binance
	// equivalent (Binance's now has its own, started above) — starting them
	// unconditionally for every account, as before, meant a Binance account's two loops
	// spent forever retrying a Bybit auth failure every 5s for nothing.
	if ar.exchangeName != "bybit" {
		<-ctx.Done()
		return
	}

	go safeLoop(ctx, "tradeStream/"+ar.accountID, ar.tradeStream.Run)
	safeLoop(ctx, "privateStream/"+ar.accountID, func(ctx context.Context) {
		trader.RunPrivateStream(ctx, ar.creds, ar)
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/strategy/... -run TestWireBinanceTradeStream -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/engine_test.go
git commit -m "feat(strategy): start BinanceTradeStream per Binance account in AccountRunner.run

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 7: Full regression sweep + manual smoke test + deploy

- [ ] **Step 1: Full build + vet**

Run: `go build ./...`
Expected: no output (clean build).

Run: `go vet ./... && go vet -tags integration ./...`
Expected: no output (clean vet), both tag variants.

- [ ] **Step 2: Full unit test sweep for every touched package**

Run: `go test ./pkg/trader/... ./pkg/trader/binance/... ./pkg/strategy/... ./pkg/signal/... -v`
Expected: PASS, zero failures — every existing test (REST-only Binance behavior,
`AccountRunner`, Bybit's own `TradeStream`/exchange tests) keeps passing unchanged,
alongside every new test from Tasks 1-6.

- [ ] **Step 3: Integration-tagged sweep against the night's established baselines**

Run: `go test ./pkg/strategy/... -tags integration`
Expected: exactly the known 2 pre-existing, unrelated failures (`TestResolveSignalConfigs_ExpandsCustomSignalIntoComponents`,
`TestResolveSignalConfigs_MixedCatalogAndCustom` — a `custom_signals_badge_len` DB
constraint issue, confirmed via `git stash` earlier in this session to predate tonight's
work entirely). Any OTHER failure here is a real regression from this plan — stop and
investigate before proceeding.

Run: `go test ./services/api-gateway/... -tags integration`
Expected: exactly the known 10 pre-existing, unrelated failures (listed in this session's
own prior test-review reports — `TestCreateAccount_NoEncKey`, `TestPatchBot_LinkedSubscriptionBlocked`,
`TestDeployBot`, `TestForkBot`, `TestPatchBot_StrategyLockWhileActive`,
`TestCreateCustomSignal_SavesAndListsComponents`, `TestCreateWebhook_RejectsBothOrNeitherSignalSource`,
`TestCreateWebhook_WithCustomSignalID_SubscribesAndResolvesName`,
`TestDeleteCustomSignal_CascadesOwnedWebhooks`, `TestMatrixRepairCandidates_FindsOneSidedSymbols`).
Any OTHER failure is a real regression — stop and investigate.

- [ ] **Step 4: Manual smoke test against the real Binance account (required before trusting this on live strategies)**

This step exists because the WS JSON param-typing (Task 1) and the exact `algoOrder.place`
response shape are inferred from documentation, not yet exercised against the real
exchange. Before relying on this for live strategy trading:

1. Deploy (`pm2 restart sis-api-gateway`) with the Binance account's strategies PAUSED
   (so nothing live places an order automatically yet).
2. Use the terminal's manual order form to place ONE tiny LIMIT order on the Binance
   account, priced far enough from the current market that it rests unfilled (e.g. 20%
   below market for a buy) — confirms `order.place` over WS round-trips correctly end to
   end against the real exchange, and that the order actually appears on Binance (check
   the exchange's own UI or `GET /fapi/v1/openOrders`).
3. Cancel that same order through the terminal — confirms `order.cancel` over WS.
4. Check `sis-api-gateway`'s logs for `binance trade ws: connected` (confirms the
   connection came up) and absence of `binance trade ws: PlaceOrder fallback to REST`
   for these two calls (confirms WS actually handled them, not a silent REST fallback
   masking a WS problem).
5. Only after this manual check passes, unpause the account's live strategies.

- [ ] **Step 5: Deploy and report**

```bash
pm2 restart sis-api-gateway
pm2 logs sis-api-gateway --lines 60 --nostream
```

Confirm no crash-on-startup, no unexpected `binance trade ws:` errors, and (once Step 4's
manual check has passed) that a real strategy's next Binance order placement logs
noticeably faster than before — report the before/after timing difference observed in
production logs (the `trader trade ws: %s took %v (slow)` — style latency, adapted for
Binance if added — or simply comparing wall-clock gaps between "order intent" and
"exchange ack" log lines) back to the user in Russian, per this session's established
reporting convention.
