# Multi-Exchange TickerHub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pkg/signal.TickerHub` (the mark-price WS dispatcher `pkg/strategy` subscribes to for live pricing) serve **both** Bybit and Binance, keyed by `(exchange, symbol)` instead of bare `symbol` — Plan #3 of the multi-exchange rollout (see `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`).

**Architecture:** `TickerHub`'s internal maps and its WS connection pool already support "add a topic to whichever pooled connection has room, else open a new one" (`assignTopic`/`runConn`) — this plan extends that pool to be exchange-aware (`tickerConn.exchange` field), adds a second dial target + message parser for Binance's `markPriceUpdate` stream, and changes the map keys from `string` (symbol) to a `tickerKey{Exchange, Symbol}` struct. All 3 existing call sites (`pkg/strategy/cycle.go`, `pkg/strategy/matrix.go`, `services/api-gateway/hedge_engine.go`) pass a literal `"bybit"` for now — they don't yet know which exchange their account actually uses; threading the real value through is explicitly **Plan #4**'s job (call-site migration into `AccountRunner`). This plan proves the pipe works end-to-end for both exchanges without changing what any strategy actually does today.

While touching `runConn`/`readLoop`, this plan also retrofits the same read-deadline fix already applied to `pkg/trader/ws.go` (commit `143ffb5`, this repo): the existing Bybit ticker connections have no read deadline either, so the exact same silent-network-hang failure mode is possible here too — worth closing now since the code is already open for the multi-exchange split.

**Tech Stack:** Go, `github.com/gorilla/websocket`, `httptest`-free unit tests using a local WS test server (matching `pkg/trader/ws_test.go`'s pattern) for the read-deadline test; direct unit tests (no network) for the pure parsing/keying logic.

**Spec:** `docs/superpowers/specs/2026-09-03-binance-live-trading-design.md`
**Depends on:** Plan #2 (`pkg/trader/binance`, merged) — not actually called from this plan's code, but `GetMarkPrice` on `trader.Exchange`/`BinanceExchange` exists for whichever later plan wires a REST warm-up path if one ever proves necessary (none of today's 3 call sites need one — see "What this plan deliberately does not do").

---

## Before you start

Read these first — every step below assumes you already know their exact current shape:
- `pkg/signal/ticker_hub.go` — the entire file (276 lines). This plan rewrites large parts of it in place.
- `pkg/signal/hub.go:15-30` — where `bybitPublicWS`, `maxTopicsPerConn`, `wsPingInterval`, `wsReconnectDelay` are declared (package-level, shared with the kline hub — do not duplicate these, only add what's new).
- `pkg/trader/ws.go` (as it stands after commit `143ffb5`) — the exact read-deadline pattern (`SetReadDeadline` before the loop, reset after every successful read) this plan repeats for `ticker_hub.go`'s two WS pools.
- The 3 call sites, so you know exactly what to change and nothing more:
  - `pkg/strategy/cycle.go:5610` — `se.PriceHub().Subscribe(symbol, func(markPrice float64) {...})`
  - `pkg/strategy/matrix.go:792` — same pattern
  - `services/api-gateway/hedge_engine.go:601` — `s.signalEngine.PriceHub().LatestPrice(entry.symbol)`

**Real Binance USDⓈ-M Futures facts this plan relies on** (verified via web search during planning, not assumed from memory):
- Base WS URL for dynamic single-stream subscriptions: `wss://fstream.binance.com/ws` (the `/ws` raw endpoint, not `/stream` — `/stream` wraps every payload as `{"stream":"...","data":{...}}`, which we don't want here; `/ws` delivers the raw event object directly, matching how this plan's parser is written).
- Dynamic subscribe/unsubscribe on an already-open `/ws` connection: `{"method":"SUBSCRIBE","params":["btcusdt@markPrice@1s"],"id":<int>}` / `{"method":"UNSUBSCRIBE",...}` — structurally parallel to Bybit's `{"op":"subscribe","args":[...]}`, so the existing "one pooled connection, keep adding topics until full" design carries over directly.
- Stream name casing: the subscription topic uses a **lowercase** symbol (`btcusdt@markPrice@1s`), but the event payload's `"s"` field is **uppercase** (`"BTCUSDT"`) — matching how this codebase represents symbols everywhere else, so no case-translation is needed on the receiving side, only when building the subscribe topic.
- `@markPrice@1s` is the 1-second-cadence variant (default `@markPrice` updates every 3s) — used here for update frequency closer to Bybit's ticker stream.
- `markPriceUpdate` event shape: `{"e":"markPriceUpdate","E":<ms>,"s":"BTCUSDT","p":"<mark price string>","ap":"...","i":"...","P":"...","r":"...","T":<ms>}` — this plan only reads `e`, `s`, `p`.
- Liveness: Binance's WS server sends a native WebSocket **protocol-level** ping frame every ~3 minutes; the client must pong within 10 minutes or gets dropped. `gorilla/websocket` answers control-frame pings automatically as long as `ReadMessage` is being called in a loop — **no app-level ping message needs to be sent to Binance** (unlike Bybit, which needs an explicit `{"op":"ping"}` JSON message every `wsPingInterval` since Bybit's liveness is an application-level protocol, not native WS control frames). This plan gates the existing `pingTicker` send on `tc.exchange != "binance"`.

---

### Task 1: Composite-key TickerHub + read-deadline retrofit (Bybit-only, behavior-preserving)

This task changes `TickerHub`'s internals and public API to key everything by `(exchange, symbol)` and adds the read-deadline fix — but touches **zero** Binance-specific code yet. Every existing (Bybit) code path must behave identically once the 3 call sites (updated in Task 3) pass `"bybit"` as the exchange.

**Files:**
- Modify: `pkg/signal/ticker_hub.go`
- Create: `pkg/signal/ticker_hub_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/signal/ticker_hub_test.go`:

```go
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
```

Note: `bybitPublicWS` must become a `var` (currently a `const` in `hub.go`) for the last test to override it — that's part of Step 3 below. This task's 5 tests deliberately cover Bybit-path behavior and the exchange-keying/read-deadline mechanics only — the Binance-specific parsing tests (`handleBinanceMessage`, `topicFor`, `subscribeMsg`) are added in Task 2, even though Task 1 already implements that code (see Task 2's intro for why).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/signal/... -run TestTickerHub -v`
Expected: FAIL to compile — `tickerKey`, `setPriceAndDispatch`, `tickerReadTimeout`, and the new two-argument `Subscribe`/`LatestPrice`/`SetPrice` signatures don't exist yet; `bybitPublicWS` is still a `const` so the last test's assignment won't compile either.

- [ ] **Step 3: Make `bybitPublicWS` a `var`**

In `pkg/signal/hub.go`, find:
```go
	bybitPublicWS     = "wss://stream.bybit.com/v5/public/linear"
```
This is almost certainly already inside a `const (...)` block alongside `maxTopicsPerConn`, `wsPingInterval`, `wsReconnectDelay`. Pull `bybitPublicWS` out into its own `var` declaration right after that const block (the other three stay `const` — only the URL needs to be test-overridable):

```go
var bybitPublicWS = "wss://stream.bybit.com/v5/public/linear"
```

Remove it from inside the `const (...)` block where it currently lives.

- [ ] **Step 4: Rewrite `ticker_hub.go`**

Replace the full contents of `pkg/signal/ticker_hub.go` with:

```go
package signal

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"sis/pkg/proxy"
)

// tickerReadTimeout bounds how long a pooled WS connection (Bybit or Binance) waits for
// ANY message before being treated as dead. Without this, a silently-broken network
// path (packets black-holed, no TCP RST/FIN) leaves the reader goroutine blocked in
// ReadMessage() forever — see pkg/trader/ws.go's identical fix (commit 143ffb5) for the
// real incident this class of bug caused on the private position-stream side. Set well
// above both exchanges' normal update cadence (Bybit tickers and Binance's @1s mark
// price stream both update at least once per second when healthy).
var tickerReadTimeout = 45 * time.Second

// binancePublicWS is Binance USDⓈ-M Futures' dynamic-subscribe WS endpoint. Unlike the
// combined-stream /stream endpoint (which wraps every payload as {"stream":...,
// "data":...}), /ws delivers the raw event object directly — matching how
// handleBinanceMessage below parses it.
var binancePublicWS = "wss://fstream.binance.com/ws"

// tickerKey identifies one (exchange, symbol) price feed. Two exchanges can report
// wildly different prices for the identically-spelled symbol string, so the exchange is
// part of the key everywhere — TickerHub never merges prices across exchanges.
type tickerKey struct {
	Exchange string
	Symbol   string
}

// TickerHub maintains WS connections (one pool per exchange) and dispatches markPrice
// updates to registered per-(exchange,symbol) callbacks.
type TickerHub struct {
	ctx context.Context
	mu  sync.RWMutex

	prices map[tickerKey]float64
	cbs    map[tickerKey][]*tickerCb

	pool   []*tickerConn
	poolMu sync.Mutex
}

type tickerConn struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	conn     *websocket.Conn
	exchange string
	topics   []string
}

type tickerCb struct {
	fn func(float64)
}

// TickerHubMetrics is a live snapshot of TickerHub state.
type TickerHubMetrics struct {
	Symbols     int `json:"symbols"`
	WarmSymbols int `json:"warm_symbols"` // symbols that have received at least one price
	WsConns     int `json:"ws_connections"`
}

// NewTickerHub creates a TickerHub. Subscribe symbols directly or warm via GlobalWarmer.
func NewTickerHub(ctx context.Context) *TickerHub {
	return &TickerHub{
		ctx:    ctx,
		prices: make(map[tickerKey]float64),
		cbs:    make(map[tickerKey][]*tickerCb),
	}
}

// Subscribe ensures a WS subscription for (exchange, symbol) and, if cb is non-nil,
// registers it to be called on every markPrice update. Returns an unsubscribe func.
// exchange is a lowercase exchange id ("bybit" or "binance").
func (h *TickerHub) Subscribe(exchange, symbol string, cb func(markPrice float64)) func() {
	key := tickerKey{Exchange: exchange, Symbol: symbol}
	h.mu.Lock()
	_, exists := h.prices[key]
	if !exists {
		h.prices[key] = 0
	}
	var entry *tickerCb
	if cb != nil {
		entry = &tickerCb{fn: cb}
		h.cbs[key] = append(h.cbs[key], entry)
	}
	h.mu.Unlock()

	if !exists {
		h.assignTopic(exchange, topicFor(exchange, symbol))
	}

	return func() {
		if entry == nil {
			return
		}
		h.mu.Lock()
		cbs := h.cbs[key]
		for i, c := range cbs {
			if c == entry {
				h.cbs[key] = append(cbs[:i], cbs[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
	}
}

// topicFor builds the exchange-specific subscribe topic string for symbol. Binance's
// stream name uses a lowercase symbol; the event payload it later sends back uses
// uppercase (matching this codebase's convention), so no case translation is needed on
// the receiving side — only here, when constructing the subscribe request.
func topicFor(exchange, symbol string) string {
	if exchange == "binance" {
		return strings.ToLower(symbol) + "@markPrice@1s"
	}
	return "tickers." + symbol
}

// LatestPrice returns the most recently received markPrice for (exchange, symbol), or 0.
func (h *TickerHub) LatestPrice(exchange, symbol string) float64 {
	h.mu.RLock()
	p := h.prices[tickerKey{Exchange: exchange, Symbol: symbol}]
	h.mu.RUnlock()
	return p
}

// SetPrice manually sets the cached latest price for (exchange, symbol) — used by tests
// to seed a price without a live WS connection; production code should rely on
// handleMessage's real ticker updates instead.
func (h *TickerHub) SetPrice(exchange, symbol string, price float64) {
	h.mu.Lock()
	h.prices[tickerKey{Exchange: exchange, Symbol: symbol}] = price
	h.mu.Unlock()
}

// ConnCount returns the current number of WS connections in the pool (both exchanges).
func (h *TickerHub) ConnCount() int {
	h.poolMu.Lock()
	n := len(h.pool)
	h.poolMu.Unlock()
	return n
}

// Metrics returns a live snapshot of TickerHub state, aggregated across both exchanges.
func (h *TickerHub) Metrics() TickerHubMetrics {
	h.mu.RLock()
	total := len(h.prices)
	warm := 0
	for _, p := range h.prices {
		if p > 0 {
			warm++
		}
	}
	h.mu.RUnlock()
	return TickerHubMetrics{
		Symbols:     total,
		WarmSymbols: warm,
		WsConns:     h.ConnCount(),
	}
}

// ── WS pool management ─────────────────────────────────────────────────────

func (h *TickerHub) assignTopic(exchange, topic string) {
	h.poolMu.Lock()
	for _, c := range h.pool {
		if c.exchange != exchange {
			continue
		}
		c.mu.Lock()
		if len(c.topics) < maxTopicsPerConn {
			c.topics = append(c.topics, topic)
			c.mu.Unlock()
			h.poolMu.Unlock()
			h.wsSend(c, subscribeMsg(exchange, []string{topic}))
			return
		}
		c.mu.Unlock()
	}
	tc := &tickerConn{exchange: exchange, topics: []string{topic}}
	h.pool = append(h.pool, tc)
	h.poolMu.Unlock()
	go h.runConn(tc)
}

// subscribeMsg builds the exchange-specific subscribe request body.
func subscribeMsg(exchange string, topics []string) map[string]any {
	if exchange == "binance" {
		return map[string]any{"method": "SUBSCRIBE", "params": topics, "id": time.Now().UnixNano()}
	}
	return map[string]any{"op": "subscribe", "args": topics}
}

func wsURLFor(exchange string) string {
	if exchange == "binance" {
		return binancePublicWS
	}
	return bybitPublicWS
}

func (h *TickerHub) runConn(tc *tickerConn) {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, _, err := proxy.WSDialer().DialContext(h.ctx, wsURLFor(tc.exchange), nil)
		if err != nil {
			log.Printf("ticker hub: dial %s: %v; retry in %s", tc.exchange, err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		tc.mu.Lock()
		tc.conn = conn
		topics := make([]string, len(tc.topics))
		copy(topics, tc.topics)
		tc.mu.Unlock()

		h.wsSend(tc, subscribeMsg(tc.exchange, topics))

		h.readLoop(conn, tc)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		default:
			log.Printf("ticker hub: %s reconnecting in %s", tc.exchange, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (h *TickerHub) readLoop(conn *websocket.Conn, tc *tickerConn) {
	// Bybit's liveness is an application-level ping/pong (a JSON {"op":"ping"} message
	// we must send, per wsPingInterval below). Binance's is native WS protocol-level
	// ping frames sent BY THE SERVER, which gorilla/websocket answers automatically as
	// long as ReadMessage is running — no app-level ping needs sending to Binance.
	var ping *time.Ticker
	if tc.exchange != "binance" {
		ping = time.NewTicker(wsPingInterval)
		defer ping.Stop()
	}

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	conn.SetReadDeadline(time.Now().Add(tickerReadTimeout)) //nolint:errcheck
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(tickerReadTimeout)) //nolint:errcheck
			msgCh <- data
		}
	}()

	var pingCh <-chan time.Time
	if ping != nil {
		pingCh = ping.C
	}

	for {
		select {
		case <-h.ctx.Done():
			return
		case err := <-errCh:
			log.Printf("ticker hub: %s read: %v", tc.exchange, err)
			return
		case <-pingCh:
			h.wsSend(tc, map[string]string{"op": "ping"})
		case data := <-msgCh:
			h.handleMessage(tc.exchange, data)
		}
	}
}

func (h *TickerHub) wsSend(tc *tickerConn, v interface{}) {
	data, _ := json.Marshal(v)
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	tc.mu.Lock()
	conn := tc.conn
	tc.mu.Unlock()
	if conn == nil {
		return
	}
	conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// ── Message parsing ────────────────────────────────────────────────────────

func (h *TickerHub) handleMessage(exchange string, data []byte) {
	if exchange == "binance" {
		h.handleBinanceMessage(data)
		return
	}
	h.handleBybitMessage(data)
}

type wsTickerMsg struct {
	Topic string `json:"topic"`
	Data  struct {
		MarkPrice string `json:"markPrice"`
	} `json:"data"`
}

func (h *TickerHub) handleBybitMessage(data []byte) {
	var msg wsTickerMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Topic == "" {
		return
	}
	if !strings.HasPrefix(msg.Topic, "tickers.") {
		return
	}
	symbol := strings.TrimPrefix(msg.Topic, "tickers.")
	markPrice, err := strconv.ParseFloat(msg.Data.MarkPrice, 64)
	if err != nil || markPrice == 0 {
		return
	}
	h.setPriceAndDispatch(tickerKey{Exchange: "bybit", Symbol: symbol}, markPrice)
}

// binanceMarkPriceMsg is one markPriceUpdate event from Binance's /ws endpoint — the
// raw event object, not wrapped in {"stream":...,"data":...} (that wrapping only
// applies to the /stream combined endpoint, which this package does not use).
type binanceMarkPriceMsg struct {
	Event  string `json:"e"`
	Symbol string `json:"s"`
	Price  string `json:"p"`
}

func (h *TickerHub) handleBinanceMessage(data []byte) {
	var msg binanceMarkPriceMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Event != "markPriceUpdate" {
		return
	}
	markPrice, err := strconv.ParseFloat(msg.Price, 64)
	if err != nil || markPrice == 0 {
		return
	}
	h.setPriceAndDispatch(tickerKey{Exchange: "binance", Symbol: msg.Symbol}, markPrice)
}

// setPriceAndDispatch updates the cached price for key and calls every registered
// callback with the new price. Shared by both exchanges' message handlers.
func (h *TickerHub) setPriceAndDispatch(key tickerKey, price float64) {
	h.mu.Lock()
	h.prices[key] = price
	cbs := make([]*tickerCb, len(h.cbs[key]))
	copy(cbs, h.cbs[key])
	h.mu.Unlock()

	for _, c := range cbs {
		c.fn(price)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./pkg/signal/... -run TestTickerHub -v`
Expected: `PASS` for all 5 tests (`TestTickerHub_SubscribeAndLatestPrice_KeyedByExchangeAndSymbol`, `TestTickerHub_Subscribe_CallbackFiresOnMatchingExchangeOnly`, `TestTickerHub_Unsubscribe_StopsCallback`, `TestTickerHub_HandleBybitMessage_ParsesTickerTopic`, `TestTickerHub_ReadDeadline_ReturnsWhenConnectionGoesSilent`).

- [ ] **Step 6: Run the full `pkg/signal` and `pkg/strategy` test suites**

Run: `go test ./pkg/signal/... ./pkg/strategy/... -v 2>&1 | tail -60`
Expected: `pkg/signal` passes. **`pkg/strategy` will currently FAIL to compile** — `pkg/strategy/cycle.go:5610` and `matrix.go:792` still call the old one-argument `Subscribe(symbol, cb)`/`LatestPrice(symbol)`, which no longer exist. This is expected and resolved in Task 3; do not attempt to fix those call sites in this task — keep this task's diff scoped to `pkg/signal` only, and note the expected compile failure in your task report so the reviewer isn't surprised by it.

- [ ] **Step 7: Commit**

```bash
git add pkg/signal/ticker_hub.go pkg/signal/ticker_hub_test.go pkg/signal/hub.go
git commit -m "$(cat <<'EOF'
feat(signal): key TickerHub by (exchange, symbol), add WS read-deadline

Prep for Binance market-data support (Plan #3 of the multi-exchange
rollout): TickerHub's price cache and callback registry now key on exchange
identity, not just symbol, since the same symbol string exists independently
on multiple exchanges. The WS connection pool is now exchange-aware
(tickerConn.exchange), though only Bybit connections are actually opened by
this commit — Binance dialing lands in the next commit.

Also retrofits the read-deadline fix already applied to pkg/trader/ws.go
(143ffb5): the same silent-network-hang failure mode was possible here too
(no SetReadDeadline anywhere in the WS reader loop), worth closing while
this code is already open for the exchange-pool split.

NOTE: pkg/strategy's 2 call sites (cycle.go, matrix.go) do not compile
against this commit alone — they're updated in the next commit in this
plan, which is expected and by design (keeps the pkg/signal-only diff
reviewable on its own).

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Binance market-data parsing tests

Task 1 already implemented `handleBinanceMessage`/`topicFor`/`subscribeMsg`'s Binance branches (there's no separate "write the Binance code" step left — it was included in Task 1's full-file rewrite, since splitting the single-file rewrite across two commits would have meant shipping a temporarily-broken intermediate state). This task adds the tests specifically proving the Binance parsing path, which Task 1 deferred.

**Files:**
- Modify: `pkg/signal/ticker_hub_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `pkg/signal/ticker_hub_test.go`:

```go
func TestTickerHub_HandleBinanceMessage_ParsesMarkPriceUpdate(t *testing.T) {
	h := NewTickerHub(context.Background())
	h.Subscribe("binance", "BTCUSDT", nil)

	data, _ := json.Marshal(map[string]any{
		"e": "markPriceUpdate",
		"E": 1562305380000,
		"s": "BTCUSDT",
		"p": "63000.25000000",
		"ap": "63000.10000000",
		"i": "62999.00000000",
		"r": "0.00038167",
		"T": 1562306400000,
	})
	h.handleMessage("binance", data)

	if got := h.LatestPrice("binance", "BTCUSDT"); got != 63000.25 {
		t.Errorf("LatestPrice after Binance message = %v, want 63000.25", got)
	}
}

func TestTickerHub_HandleBinanceMessage_IgnoresNonMarkPriceEvents(t *testing.T) {
	h := NewTickerHub(context.Background())
	h.Subscribe("binance", "BTCUSDT", nil)

	// Some other Binance event type must not be misparsed into a price update.
	data, _ := json.Marshal(map[string]any{"e": "aggTrade", "s": "BTCUSDT", "p": "1.0"})
	h.handleMessage("binance", data)

	if got := h.LatestPrice("binance", "BTCUSDT"); got != 0 {
		t.Errorf("LatestPrice after a non-markPriceUpdate event = %v, want 0 (must be ignored)", got)
	}
}

func TestTickerHub_HandleBinanceMessage_DoesNotLeakIntoBybitKey(t *testing.T) {
	h := NewTickerHub(context.Background())
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
```

- [ ] **Step 2: Run the tests to verify they pass**

Run: `go test ./pkg/signal/... -v`
Expected: `PASS` for all tests in the package — the production code these tests exercise was already written in Task 1, so this step should go straight to green. If anything fails, that means Task 1's Binance-branch code has a bug that its own tests didn't catch — fix `ticker_hub.go` (not the test) to match this task's spec, since these tests encode the actual Binance wire-format facts researched for this plan.

- [ ] **Step 3: Commit**

```bash
git add pkg/signal/ticker_hub_test.go
git commit -m "$(cat <<'EOF'
test(signal): cover TickerHub's Binance markPriceUpdate parsing

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Update the 3 existing call sites to compile against the new signature

**Files:**
- Modify: `pkg/strategy/cycle.go:5610`
- Modify: `pkg/strategy/matrix.go:792`
- Modify: `services/api-gateway/hedge_engine.go:601`

Every call site below gets a literal `"bybit"` — **not** a symbolic constant, and **not** a lookup of the strategy's real account exchange. Threading the real exchange value through `AccountRunner`/`StrategyRunner`/hedge sessions is Plan #4's explicit job (call-site migration); this task only needs the codebase to compile and behave identically to before, since nothing here has ever run against a non-Bybit account.

- [ ] **Step 1: Update `pkg/strategy/cycle.go`**

Read the surrounding function first (`go run` won't help you here — read the actual file) to confirm the exact current line, then change:
```go
		unsub := se.PriceHub().Subscribe(symbol, func(markPrice float64) {
```
to:
```go
		// TODO(plan #4): "bybit" is hardcoded until AccountRunner threads the
		// account's real exchange through to StrategyRunner — every account is a
		// Bybit account today, so this is behavior-preserving, not a shortcut that
		// silently breaks anything yet.
		unsub := se.PriceHub().Subscribe("bybit", symbol, func(markPrice float64) {
```

- [ ] **Step 2: Update `pkg/strategy/matrix.go`**

Same change at the equivalent line:
```go
		unsub := se.PriceHub().Subscribe(symbol, func(markPrice float64) {
```
becomes:
```go
		// TODO(plan #4): "bybit" is hardcoded until AccountRunner threads the
		// account's real exchange through — see the identical note in cycle.go.
		unsub := se.PriceHub().Subscribe("bybit", symbol, func(markPrice float64) {
```

- [ ] **Step 3: Update `services/api-gateway/hedge_engine.go`**

```go
	mp := s.signalEngine.PriceHub().LatestPrice(entry.symbol)
```
becomes:
```go
	// TODO(plan #4): "bybit" is hardcoded until this reads the account's real
	// exchange — see the identical note in pkg/strategy/cycle.go.
	mp := s.signalEngine.PriceHub().LatestPrice("bybit", entry.symbol)
```

- [ ] **Step 4: Build and run the full repository test suite**

Run: `go build ./...`
Expected: clean — this is what resolves Task 1's expected compile failure.

Run: `go test ./...`
Expected: every package passes, identical to the pre-Task-1 baseline (`pkg/strategy`, `services/api-gateway`, `pkg/signal`, and everything else). This proves the refactor is behavior-preserving for the only exchange actually in use today.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/matrix.go services/api-gateway/hedge_engine.go
git commit -m "$(cat <<'EOF'
fix(strategy): update PriceHub call sites for TickerHub's new (exchange, symbol) key

Passes a hardcoded "bybit" at all 3 call sites — every account is a Bybit
account today, so this is behavior-preserving. Threading the account's real
exchange through is Plan #4's job (AccountRunner/call-site migration), not
this plan's.

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
EOF
)"
```

---

## What this plan deliberately does not do

- Does not make any strategy actually price against Binance — every call site still hardcodes `"bybit"`. That requires Plan #4 to know which exchange an account belongs to, which requires `AccountRunner` to hold a resolved `trader.Exchange` per account (today it hardcodes `trader.NewTradeStream`/Bybit-only free functions).
- Does not add a REST warm-up fallback (`Exchange.GetMarkPrice`) for a symbol whose WS hasn't delivered its first price yet. No current call site needs one — Bybit's own `Subscribe` has never had this either, and nothing today times out waiting. If a real need surfaces once Plan #4 wires a live Binance account, add it then, grounded in the actual failure observed, rather than speculatively here.
- Does not touch `pkg/signal/hub.go`'s kline/candle WS pool (a structurally similar but separate hub) — out of scope for price dispatch; a future plan can decide whether klines need the same multi-exchange/read-deadline treatment.
- Does not change `GlobalWarmer` (mentioned in `NewTickerHub`'s doc comment) — if it exists and calls `Subscribe`, it's Bybit-only warming today and stays that way until Plan #4.
