# Binance WS Order Placement — Design

## Problem

Binance trading currently goes through REST for every single order operation.
`pkg/trader/binance/exchange.go`'s `PlaceOrder` and `PlaceOrderREST` are the literal same
function:

```go
func (e *BinanceExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}
func (e *BinanceExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}
```

`e.placeOrder` always ends in `doSignedPOST(ctx, e.creds, "/fapi/v1/order", params)` — a
fresh signed HTTP request through the proxy pool on every call. Bybit, by contrast, places
orders over `*TradeStream` (`pkg/trader/trade_ws.go`): a persistent WebSocket connection
with per-request signing and response correlation by `reqId`, so an order placement is one
write + one matched read on an already-open socket, no TCP/TLS/HTTP handshake per call.

This REST-only path is why Binance order placement/cancellation is noticeably slower than
Bybit's in production (reported live, 2026-10-17) — every grid/matrix entry, TP/SL, and
batch order pays full REST round-trip latency through the proxy, every time, instead of
reusing one warm connection.

This was a known, deliberate scope cut, not an oversight: `ResolveExchange`'s own doc
comment (`pkg/strategy/engine.go`) says "Binance has no separate order-placement WS yet
(see Plan #2's design — PlaceOrder == PlaceOrderREST for Binance)".

## Goal

Binance order placement/cancellation (both regular LIMIT/MARKET orders and conditional/algo
orders — STOP_MARKET, TAKE_PROFIT_MARKET, TRAILING_STOP_MARKET) goes over a persistent,
reconnecting WebSocket connection, mirroring Bybit's `TradeStream` architecture, closing the
latency gap end to end in one pass (both order kinds, not phased).

Non-goals:
- `PlaceOrderREST`/`CancelOrderREST` (used by admin/rescue-engine callers that explicitly
  want a one-off REST call, no persistent connection) are unchanged.
- `FetchPositions`, `FetchOpenOrders`, `GetAccountMarginInfo`, closed-PnL/execution/order-
  history sync, and every other read-mostly Exchange method stay REST — only the hot
  write path (place/cancel, single + batch) moves to WS.
- No change to the account-data WebSocket (`pkg/trader/binance/ws.go`, position/order
  push updates) — Binance requires that to be a separate connection from the order-
  placement WS, so this is an architectural constraint, not a choice.

## Researched facts (Binance WS API, confirmed 2026-10-17)

- Endpoint: `wss://ws-fapi.binance.com/ws-fapi/v1`.
- Auth: per-request HMAC-SHA256 signing is supported (not just Ed25519/`session.logon`) —
  confirmed via a signed `order.place` example in Binance's own docs showing a plain
  `apiKey`+`signature` pair inside `params`, computed exactly like REST (sort params
  alphabetically, URL-encode, HMAC-SHA256 with the secret). **No key migration needed** —
  the existing stored API key/secret works as-is.
- Request envelope: `{"id": "<client-chosen string>", "method": "<name>", "params": {...}}`.
  Response echoes the same `id`.
- Response envelope: `{"id", "status", "result": {...}}` on success,
  `{"id", "status", "error": {"code": -1102, "msg": "..."}}` on failure — same
  `{code, msg}` shape as REST's error body, just nested under `error` instead of being the
  whole payload.
- `order.place` (regular orders) accepts LIMIT/MARKET only — explicitly documented as NOT
  supporting conditional types. Conditional orders (STOP_MARKET, TAKE_PROFIT_MARKET,
  TRAILING_STOP_MARKET) require the separate `algoOrder.place` method (and `algoOrder.cancel`
  for cancellation) — the same regular/algo split the 2025-12-09 REST Algo Order migration
  already forced onto `pkg/trader/binance/exchange.go` (`isAlgoOrderType`), now showing up
  again at the WS layer.
- Both methods are reachable over the **same** connection — unlike the order-WS vs.
  user-data-WS split, there is no stated requirement to use separate sockets for
  `order.place` vs. `algoOrder.place`.
- No native multi-order batch method was found for either `order.place` or
  `algoOrder.place` (each call takes one order's params, not an array) — unlike REST's
  `/fapi/v1/batchOrders`. Batch therefore means firing N individual WS calls concurrently
  over the shared connection, not a single wire message.
- Server pings every ~3 minutes; client must respond within ~10 minutes. This is the exact
  same protocol-level ping behavior `pkg/trader/binance/ws.go` already handles correctly
  (fixed 2026-10-16 for the account-data stream) via `SetPingHandler` refreshing the read
  deadline — reused verbatim for the new connection.
- Connections are valid up to 24 hours — `Run`'s reconnect loop (below) handles this the
  same way it handles any other disconnect.

Sources: [WebSocket API General Info](https://developers.binance.com/docs/derivatives/usds-margined-futures/websocket-api-general-info), [New Order (WS)](https://developers.binance.com/docs/derivatives/usds-margined-futures/trade/websocket-api/New-Order), [Request security](https://developers.binance.com/docs/binance-spot-api-docs/websocket-api/request-security).

## Architecture

### New component: `pkg/trader/binance/trade_ws.go`

`BinanceTradeStream` — a pure WS order client, with **no REST knowledge at all** (a
deliberate cleaner split than Bybit's `TradeStream`, which embeds its own REST-fallback
calls). Structure mirrors `pkg/trader/trade_ws.go`'s `TradeStream` closely:

```go
type binanceWSResp struct {
	Id     string          `json:"id"`
	Status int             `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	} `json:"error"`
}

type BinanceTradeStream struct {
	creds      trader.Credentials
	mu         sync.Mutex
	conn       *websocket.Conn
	seq        int64
	pending    map[string]chan binanceWSResp
	wsDisabled bool // set after a permanent auth/permission error; mirrors Bybit's wsDisabled
}

func NewBinanceTradeStream(creds trader.Credentials) *BinanceTradeStream
func (ts *BinanceTradeStream) Run(ctx context.Context)      // connect + reconnect loop, 5s retry
func (ts *BinanceTradeStream) runOnce(ctx context.Context) error
func (ts *BinanceTradeStream) sendReq(ctx context.Context, method string, params url.Values) (binanceWSResp, error)

func (ts *BinanceTradeStream) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error)
func (ts *BinanceTradeStream) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error)
func (ts *BinanceTradeStream) CancelOrder(ctx context.Context, req trader.CancelRequest) error
func (ts *BinanceTradeStream) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error
```

- `Run`/`runOnce`: same shape as Bybit's — dial via `proxy.WSDialerFor(ts.creds.WhitelistedIPs)`,
  register the connection, read loop in a goroutine feeding a channel, `select` over
  ctx/ping-ticker/error/message. Ping handling reuses `binance/ws.go`'s proven
  `SetReadDeadline` + `SetPingHandler` pattern (same `binanceReadTimeout` constant) instead
  of re-deriving it.
- `sendReq`: builds `params` as a `url.Values`, adds `apiKey` (`params.Set("apiKey",
  ts.creds.APIKey)`), signs via the **existing** `signParams(creds, params)` helper from
  `client.go` (identical signing math to REST — sort+encode+HMAC — so no new crypto code),
  converts the signed `url.Values` into a `map[string]any` for the JSON `params` object,
  assigns a `fmt.Sprintf("%d", ts.seq)`-style id (mirrors Bybit's `reqId` counter), writes
  `{"id","method","params"}`, and blocks on `pending[id]` with a 10s timeout — same
  structure as Bybit's `sendReq`.
- `PlaceOrder`/`CancelOrder`: pick `order.place`/`order.cancel` vs. `algoOrder.place`/
  `algoOrder.cancel` via the existing `isAlgoOrderType` check (reused from `exchange.go`,
  not duplicated), translate `trader.OrderRequest`/`CancelRequest` into WS params the same
  way `placeOrder`/`cancelOrder`/`placeAlgoOrder`/`cancelAlgoOrder` already translate them
  for REST (same field names/values — only the transport differs), and distinguish two
  failure modes:
  - **Not connected / `wsDisabled` / an auth-or-permission-shaped error** (new
    `binanceWSPermDenied(code int) bool` helper, Binance-coded equivalent of Bybit's
    `wsPermDenied`, covering e.g. `-2015` invalid key/IP/permissions, `-1022` bad
    signature) → return the sentinel `ErrWSUnavailable`, and (for a permission-denied
    *response*, not a transient connection failure) set `wsDisabled = true` so subsequent
    calls skip straight to the caller's REST fallback without retrying a doomed WS call
    each time.
  - **Any other non-zero `error.code`** (a genuine order rejection — bad qty, insufficient
    margin, precision, etc.) → return a normal `fmt.Errorf("binance: code=%d: %s", ...)`,
    exactly as REST already does. This must NOT trigger a fallback retry: the order was
    validly rejected (or, worse, validly accepted but slow to acknowledge), and blindly
    retrying over REST risks a duplicate submission — the same safety reasoning Bybit's
    `wsPermDenied` split already encodes, just phrased explicitly here since
    `BinanceTradeStream` has no fallback logic of its own to make that call implicitly.
- `PlaceOrderBatch`/`CancelOrderBatch`: split regular vs. algo items (same split already
  written in today's REST `BinanceExchange.PlaceOrderBatch`/`CancelOrderBatch`), then fire
  one `PlaceOrder`/`CancelOrder`-equivalent WS call per item **concurrently** (goroutines +
  `sync.WaitGroup`) over the shared connection — pipelined, not sequential — writing each
  result into the output slice at its original index, mirroring the existing REST version's
  indexing exactly.

### `BinanceExchange` changes (`pkg/trader/binance/exchange.go`)

- New field: `ws *BinanceTradeStream` (nil by default).
- New method: `func (e *BinanceExchange) SetTradeStream(ws *BinanceTradeStream)` — the only
  way `ws` ever becomes non-nil. `NewBinanceExchange(creds)`'s signature is **unchanged** —
  every existing call site (including ~40 `NewBinanceExchange(testCreds())` in
  `exchange_test.go`) keeps compiling and behaving exactly as today (pure REST, since
  `e.ws == nil`).
- `PlaceOrder`/`CancelOrder` become:
  ```go
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
  ```
  `CancelOrder`/`PlaceOrderBatch`/`CancelOrderBatch` follow the identical shape.
  `PlaceOrderREST`/`CancelOrderREST` are untouched (still call `e.placeOrder`/`e.cancelOrder`
  directly, never look at `e.ws`).

### `pkg/strategy/engine.go` changes — the only place that starts the connection

`ResolveExchange` itself stays side-effect-free (no new goroutines, no new fields on its
signature) — it's called from several places that must NOT each spin up a persistent WS
connection: `closed_pnl_syncer.go`'s per-tick resolve, `bot_engine.go`, `trader_handler.go`.
Spinning one up per call there would leak a connection per sync tick.

Only `newAccountRunnerWithExchange`/`AccountRunner.run()` — the one place that already owns
a long-lived, ctx-scoped goroutine lifecycle per account, and already starts Bybit's
`tradeStream.Run`/`privateStream.Run` under `safeLoop` for `exchangeName=="bybit"` — gains
the Binance equivalent:

```go
func (ar *AccountRunner) run(ctx context.Context) {
	go safeLoop(ctx, "reconcile/"+ar.accountID, ar.startReconcileLoop)
	go safeLoop(ctx, "riskMonitor/"+ar.accountID, ar.runRiskMonitorLoop)
	if ar.exchangeName == "binance" {
		if be, ok := ar.exchange.(*binance.BinanceExchange); ok {
			stream := binance.NewBinanceTradeStream(ar.creds)
			be.SetTradeStream(stream)
			go safeLoop(ctx, "binanceTradeStream/"+ar.accountID, stream.Run)
		}
		<-ctx.Done()
		return
	}
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

(`pkg/strategy` already imports `sis/pkg/trader/binance` for `ResolveExchange`'s own
`binance.NewBinanceExchange` call — no new import.) Every other engine (`matrix.go`,
`cycle.go`, reconcile, risk) is untouched: they already call through the `trader.Exchange`
interface (`ar.exchange.PlaceOrder(...)` etc.), which looks identical from the outside
whether `BinanceExchange.ws` is wired or not.

## Error Handling

- **Connection drop mid-flight**: `Run`'s reconnect loop (same as Bybit's) clears `conn` and
  flushes every pending request with a synthetic "disconnected" response, so no caller
  blocks forever waiting on a response that will never arrive — `sendReq` turns that
  synthetic response into `ErrWSUnavailable`, so in-flight `PlaceOrder`/`CancelOrder` calls
  get it promptly and fall back to REST instead of hanging.
- **Permission/auth error** (`binanceWSPermDenied`): one-time `wsDisabled = true`, same as
  Bybit — every account that hits this (e.g. a key genuinely without the right permission
  flag) degrades gracefully to REST-only behavior (today's current, working behavior)
  rather than retrying a doomed WS call on every single order forever.
- **Genuine order rejection**: returned as-is, no fallback, no duplicate-submission risk.
- **Timeout (10s, no response)**: same as Bybit's `sendReq` — cleans up the pending entry
  and returns a timeout error, which `PlaceOrder`/`CancelOrder` treat as `ErrWSUnavailable`
  (connection-health signal, not an order-rejection signal) and fall back to REST.

## Testing

- `BinanceTradeStream`: a local `httptest`/`gorilla/websocket` test server (same pattern as
  the existing account-data-stream tests in `pkg/trader/binance/ws_test.go`) driving
  `runOnce` directly — covers request/response correlation by id, the regular-vs-algo
  method choice, ping-deadline survival (reusing the exact scenario already proven for
  `ws.go`), and reconnect-after-drop.
- `BinanceExchange.PlaceOrder`/`CancelOrder`/batch WS→REST fallback: a new
  `binanceWsOrderClient` interface (mirroring Bybit's `wsOrderClient`) + hand-written fake,
  so the fallback decision (`ErrWSUnavailable` → REST; anything else → return as-is) is
  tested without a real network dial — exactly Bybit's `fakeWSOrderClient` pattern in
  `pkg/trader/exchange_test.go`.
- Every existing `NewBinanceExchange(testCreds())`-based test (the ~40 already in
  `exchange_test.go`/`instruments_test.go`) needs **zero changes** — `ws` stays nil unless a
  test explicitly calls `SetTradeStream`, so they keep exercising pure REST exactly as
  today.
- `pkg/strategy/engine.go`'s new gate: a focused test asserting that for a Binance account,
  `run()` ends up calling `SetTradeStream` on the resolved exchange and starts exactly one
  extra `safeLoop` goroutine, without touching the existing Bybit-path tests.
