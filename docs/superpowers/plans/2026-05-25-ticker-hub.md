# TickerHub — Real-Time Mark Price via Bybit Tickers WS

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace 5-second REST polling in the matrix price monitor with real-time mark prices from Bybit's `tickers.*` WebSocket stream, and display TickerHub stats on the admin monitoring page.

**Architecture:** A new `TickerHub` in `pkg/signal/` mirrors the existing `KlineHub` pattern — it subscribes to `tickers.{SYMBOL}` topics on Bybit's public linear WS, maintains a pool of connections (40 topics each), stores the latest `markPrice` per symbol, and fires registered per-symbol callbacks on every update. `GlobalWarmer` pre-subscribes all USDT symbols at startup. `signal.Engine` exposes `PriceHub() *TickerHub`. `launchMatrixPriceMonitor` in `pkg/strategy/matrix.go` replaces its 5-second ticker+REST loop with a TickerHub callback. Admin page gets a new "Ticker Hub" section.

**Tech Stack:** Go (gorilla/websocket, sync), React/TypeScript (AdminPage.tsx)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `pkg/signal/ticker_hub.go` | TickerHub struct, WS pool, callbacks, metrics |
| Modify | `pkg/signal/engine.go` | Add `tickerHub` field + `PriceHub()` method |
| Modify | `pkg/signal/global_warmer.go` | Accept and warm TickerHub |
| Modify | `services/api-gateway/server.go` | Pass `se.PriceHub()` to `NewGlobalWarmer` |
| Modify | `pkg/strategy/matrix.go` | Replace REST polling with TickerHub callback |
| Modify | `services/api-gateway/admin_handler.go` | Add `ticker_hub` to metrics response |
| Modify | `frontend/src/pages/AdminPage.tsx` | Add types + TickerHub monitoring section |

---

## Task 1: TickerHub core

**Files:**
- Create: `pkg/signal/ticker_hub.go`

- [ ] **Step 1: Create ticker_hub.go**

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
)

// TickerHub maintains Bybit tickers WS connections and dispatches markPrice
// updates to registered per-symbol callbacks.
type TickerHub struct {
	ctx context.Context
	mu  sync.RWMutex

	prices map[string]float64  // latest markPrice per symbol
	cbs    map[string][]*tickerCb

	pool   []*tickerConn
	poolMu sync.Mutex
}

type tickerConn struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
	topics  []string
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

// NewTickerHub creates a TickerHub. Call Subscribe or warm via GlobalWarmer.
func NewTickerHub(ctx context.Context) *TickerHub {
	return &TickerHub{
		ctx:    ctx,
		prices: make(map[string]float64),
		cbs:    make(map[string][]*tickerCb),
	}
}

// Subscribe ensures a tickers WS subscription for symbol and, if cb is non-nil,
// registers it to be called on every markPrice update. Returns an unsubscribe func.
func (h *TickerHub) Subscribe(symbol string, cb func(markPrice float64)) func() {
	h.mu.Lock()
	_, exists := h.prices[symbol]
	if !exists {
		h.prices[symbol] = 0
	}
	var entry *tickerCb
	if cb != nil {
		entry = &tickerCb{fn: cb}
		h.cbs[symbol] = append(h.cbs[symbol], entry)
	}
	h.mu.Unlock()

	if !exists {
		h.assignTopic("tickers." + symbol)
	}

	return func() {
		if entry == nil {
			return
		}
		h.mu.Lock()
		cbs := h.cbs[symbol]
		for i, c := range cbs {
			if c == entry {
				h.cbs[symbol] = append(cbs[:i], cbs[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
	}
}

// LatestPrice returns the most recently received markPrice for symbol, or 0.
func (h *TickerHub) LatestPrice(symbol string) float64 {
	h.mu.RLock()
	p := h.prices[symbol]
	h.mu.RUnlock()
	return p
}

// ConnCount returns the current number of WS connections in the pool.
func (h *TickerHub) ConnCount() int {
	h.poolMu.Lock()
	n := len(h.pool)
	h.poolMu.Unlock()
	return n
}

// Metrics returns a live snapshot of TickerHub state.
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

func (h *TickerHub) assignTopic(topic string) {
	h.poolMu.Lock()
	for _, c := range h.pool {
		c.mu.Lock()
		if len(c.topics) < maxTopicsPerConn {
			c.topics = append(c.topics, topic)
			c.mu.Unlock()
			h.poolMu.Unlock()
			h.wsSend(c, map[string]interface{}{
				"op":   "subscribe",
				"args": []string{topic},
			})
			return
		}
		c.mu.Unlock()
	}
	tc := &tickerConn{topics: []string{topic}}
	h.pool = append(h.pool, tc)
	h.poolMu.Unlock()
	go h.runConn(tc)
}

func (h *TickerHub) runConn(tc *tickerConn) {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		// bybitPublicWS is declared in hub.go (same package)
		conn, _, err := websocket.DefaultDialer.DialContext(h.ctx, bybitPublicWS, nil)
		if err != nil {
			log.Printf("ticker hub: dial: %v; retry in %s", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		tc.mu.Lock()
		tc.conn = conn
		topics := make([]string, len(tc.topics))
		copy(topics, tc.topics)
		tc.mu.Unlock()

		h.wsSend(tc, map[string]interface{}{
			"op":   "subscribe",
			"args": topics,
		})

		h.readLoop(conn, tc)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		default:
			log.Printf("ticker hub: reconnecting in %s", wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (h *TickerHub) readLoop(conn *websocket.Conn, tc *tickerConn) {
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- data
		}
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		case err := <-errCh:
			log.Printf("ticker hub: read: %v", err)
			return
		case <-ping.C:
			h.wsSend(tc, map[string]string{"op": "ping"})
		case data := <-msgCh:
			h.handleMessage(data)
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

type wsTickerMsg struct {
	Topic string `json:"topic"`
	Data  struct {
		MarkPrice string `json:"markPrice"`
	} `json:"data"`
}

func (h *TickerHub) handleMessage(data []byte) {
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

	h.mu.Lock()
	h.prices[symbol] = markPrice
	cbs := make([]*tickerCb, len(h.cbs[symbol]))
	copy(cbs, h.cbs[symbol])
	h.mu.Unlock()

	for _, c := range cbs {
		c.fn(markPrice)
	}
}

```

> **Note:** `bybitPublicWS`, `wsReconnectDelay`, `wsPingInterval`, `maxTopicsPerConn` are all declared in `pkg/signal/hub.go` — same package, accessible directly. Do not redeclare them.

- [ ] **Step 2: Build to verify no errors**

```
cd C:\Users\123\Projects\sis
go build ./pkg/signal/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```
git add pkg/signal/ticker_hub.go
git commit -m "feat: add TickerHub for real-time Bybit tickers WS"
```

---

## Task 2: Wire TickerHub into signal.Engine

**Files:**
- Modify: `pkg/signal/engine.go`

The `Engine` struct currently has `hub *KlineHub`. Add `tickerHub *TickerHub` and expose `PriceHub()`.

- [ ] **Step 1: Add field to Engine struct**

In `pkg/signal/engine.go` around line 121, the `Engine` struct is:

```go
type Engine struct {
	hub     *KlineHub
	metrics *Metrics
	// ...
}
```

Change to:

```go
type Engine struct {
	hub       *KlineHub
	tickerHub *TickerHub
	metrics   *Metrics
	// ...
}
```

- [ ] **Step 2: Create TickerHub in NewEngine**

`NewEngine` (around line 134) currently does:

```go
func NewEngine(ctx context.Context, exec ExecFn) *Engine {
	hub := NewKlineHub(ctx)
	m := newMetrics(ctx, exec)
	return &Engine{
		hub:     hub,
		metrics: m,
		units:   make(map[string]*computeUnit),
		subs:    make(map[string]string),
	}
}
```

Change to:

```go
func NewEngine(ctx context.Context, exec ExecFn) *Engine {
	hub := NewKlineHub(ctx)
	tickerHub := NewTickerHub(ctx)
	m := newMetrics(ctx, exec)
	return &Engine{
		hub:       hub,
		tickerHub: tickerHub,
		metrics:   m,
		units:     make(map[string]*computeUnit),
		subs:      make(map[string]string),
	}
}
```

- [ ] **Step 3: Add PriceHub() method**

After the existing `Hub()` method (line 340):

```go
// Hub returns the underlying KlineHub (for conn count metrics).
func (e *Engine) Hub() *KlineHub { return e.hub }
```

Add immediately after:

```go
// PriceHub returns the TickerHub for real-time mark price subscriptions.
func (e *Engine) PriceHub() *TickerHub { return e.tickerHub }
```

- [ ] **Step 4: Build**

```
go build ./pkg/signal/...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```
git add pkg/signal/engine.go
git commit -m "feat: expose PriceHub() from signal.Engine"
```

---

## Task 3: Warm TickerHub via GlobalWarmer

**Files:**
- Modify: `pkg/signal/global_warmer.go`
- Modify: `services/api-gateway/server.go`

GlobalWarmer must call `tickerHub.Subscribe(sym, nil)` for every symbol so that WS connections are established before any strategy needs them.

- [ ] **Step 1: Add tickerHub field to GlobalWarmer**

In `pkg/signal/global_warmer.go`, the struct (line 37):

```go
type GlobalWarmer struct {
	hub *KlineHub
	// ...
}
```

Change to:

```go
type GlobalWarmer struct {
	hub       *KlineHub
	tickerHub *TickerHub
	// ...
}
```

- [ ] **Step 2: Update NewGlobalWarmer signature**

Current (line 49):

```go
func NewGlobalWarmer(hub *KlineHub) *GlobalWarmer {
	return &GlobalWarmer{
		hub:       hub,
		intervals: make(map[string]bool),
		startedAt: time.Now(),
	}
}
```

Change to:

```go
func NewGlobalWarmer(hub *KlineHub, tickerHub *TickerHub) *GlobalWarmer {
	return &GlobalWarmer{
		hub:       hub,
		tickerHub: tickerHub,
		intervals: make(map[string]bool),
		startedAt: time.Now(),
	}
}
```

- [ ] **Step 3: Warm TickerHub in loadAndSubscribeAll**

In `loadAndSubscribeAll` (around line 141), after the kline subscription loop:

```go
	for _, sym := range syms {
		for _, iv := range ivs {
			w.hub.Subscribe(sym, iv, nil)
		}
	}
```

Add after the inner loop:

```go
	for _, sym := range syms {
		for _, iv := range ivs {
			w.hub.Subscribe(sym, iv, nil)
		}
		w.tickerHub.Subscribe(sym, nil)
	}
```

- [ ] **Step 4: Update server.go to pass PriceHub**

In `services/api-gateway/server.go`, `NewServer` (line 63):

```go
se := signal.NewEngine(ctx, exec)
gw := signal.NewGlobalWarmer(se.Hub())
```

Change to:

```go
se := signal.NewEngine(ctx, exec)
gw := signal.NewGlobalWarmer(se.Hub(), se.PriceHub())
```

- [ ] **Step 5: Build**

```
go build ./pkg/signal/... && go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```
git add pkg/signal/global_warmer.go services/api-gateway/server.go
git commit -m "feat: GlobalWarmer warms TickerHub for all USDT symbols"
```

---

## Task 4: Replace matrix price monitor with TickerHub

**Files:**
- Modify: `pkg/strategy/matrix.go`

Replace the 5-second REST polling loop in `launchMatrixPriceMonitor` with a TickerHub subscription. A buffered channel decouples the WS callback goroutine from the strategy runner.

- [ ] **Step 1: Replace launchMatrixPriceMonitor**

Current implementation (around line 595–639 of `pkg/strategy/matrix.go`):

```go
func (sr *StrategyRunner) launchMatrixPriceMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel
	stratID := sr.strategy.ID
	creds := sr.runner.creds
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	go func() {
		defer cancel()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sr.mu.Lock()
				hasCycle := sr.cycle != nil
				sr.mu.Unlock()
				if !hasCycle {
					return
				}
				price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
				if err != nil {
					log.Printf("strategy %s: matrix price monitor: %v", stratID, err)
					continue
				}
				sr.submit(func(taskCtx context.Context) {
					sr.mu.Lock()
					defer sr.mu.Unlock()
					sr.matrixPriceTick(taskCtx, price)
				})
			}
		}
	}()
}
```

Replace the entire function with:

```go
// launchMatrixPriceMonitor starts a goroutine that feeds real-time mark prices
// from TickerHub into matrixPriceTick. Falls back to 5-second REST polling if
// the signal engine is unavailable. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) launchMatrixPriceMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel
	stratID := sr.strategy.ID
	symbol := sr.strategy.Symbol
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	if se == nil {
		go sr.runMatrixPriceMonitorPolling(ctx, cancel, stratID, symbol)
		return
	}

	// Buffered channel holds the latest price; old values are overwritten.
	priceCh := make(chan float64, 1)

	unsub := se.PriceHub().Subscribe(symbol, func(markPrice float64) {
		// Non-blocking: drain stale price, insert latest.
		select {
		case <-priceCh:
		default:
		}
		select {
		case priceCh <- markPrice:
		default:
		}
	})

	go func() {
		defer cancel()
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case price := <-priceCh:
				sr.mu.Lock()
				hasCycle := sr.cycle != nil
				sr.mu.Unlock()
				if !hasCycle {
					return
				}
				sr.submit(func(taskCtx context.Context) {
					sr.mu.Lock()
					defer sr.mu.Unlock()
					sr.matrixPriceTick(taskCtx, price)
				})
			}
		}
	}()
}

// runMatrixPriceMonitorPolling is the REST fallback when signalEngine is nil.
func (sr *StrategyRunner) runMatrixPriceMonitorPolling(ctx context.Context, cancel context.CancelFunc, stratID, symbol string) {
	defer cancel()
	sr.mu.Lock()
	creds := sr.runner.creds
	category := sr.strategy.Category
	sr.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sr.mu.Lock()
			hasCycle := sr.cycle != nil
			sr.mu.Unlock()
			if !hasCycle {
				return
			}
			price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
			if err != nil {
				log.Printf("strategy %s: matrix price monitor: %v", stratID, err)
				continue
			}
			sr.submit(func(taskCtx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				sr.matrixPriceTick(taskCtx, price)
			})
		}
	}
}
```

- [ ] **Step 2: Check imports in matrix.go**

The `time` package is still needed (for `runMatrixPriceMonitorPolling`). The `trader` package is still needed (for `FetchMarkPrice` in fallback). No new imports needed.

Verify the file still compiles:

```
go build ./pkg/strategy/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```
git add pkg/strategy/matrix.go
git commit -m "feat: matrix price monitor uses TickerHub WS instead of REST polling"
```

---

## Task 5: Expose TickerHub metrics in admin API

**Files:**
- Modify: `services/api-gateway/admin_handler.go`

- [ ] **Step 1: Add ticker_hub to GetAdminMetrics response**

In `admin_handler.go`, `GetAdminMetrics` (line 59), the `writeJSON` call currently ends with:

```go
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"signal":           sigSnap,
		"strategy":         map[string]interface{}{...},
		"strategy_workers": s.engine.WorkerStats(),
		"global_warmer":    warmerMetrics,
		"bot_engine": map[string]interface{}{...},
	})
```

Add `"ticker_hub"` entry:

```go
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"signal":           sigSnap,
		"strategy": map[string]interface{}{
			"activeStrategies": active,
			"activeCycles":     cycles,
			"ordersToday":      0,
			"fillsToday":       0,
			"accounts":         []interface{}{},
		},
		"strategy_workers": s.engine.WorkerStats(),
		"global_warmer":    warmerMetrics,
		"ticker_hub":       s.signalEngine.PriceHub().Metrics(),
		"bot_engine": map[string]interface{}{
			"last_tick_at":    lastAtStr,
			"last_tick_ms":    ms,
			"bots_active":     bots,
			"groups_computed": groups,
			"opportunities":   opps,
		},
	})
```

- [ ] **Step 2: Build**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```
git add services/api-gateway/admin_handler.go
git commit -m "feat: expose TickerHub metrics in GET /admin/metrics"
```

---

## Task 6: Admin monitoring page — TickerHub section

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

Two changes: (1) add `TickerHubMetrics` type and field to `AdminMetrics`; (2) add "Ticker Hub" section between Global Warmer and Bot Engine in `MonitoringTab`.

- [ ] **Step 1: Add TickerHubMetrics type to AdminMetrics interface**

In `AdminPage.tsx`, the `AdminMetrics` interface (around line 50) currently ends with:

```typescript
  bot_engine?: {
    last_tick_at: string
    last_tick_ms: number
    bots_active: number
    groups_computed: number
    opportunities: number
  }
}
```

Add `ticker_hub` field before `bot_engine`:

```typescript
  ticker_hub?: {
    symbols: number
    warm_symbols: number
    ws_connections: number
  }
  bot_engine?: {
    last_tick_at: string
    last_tick_ms: number
    bots_active: number
    groups_computed: number
    opportunities: number
  }
}
```

- [ ] **Step 2: Add Ticker Hub section to MonitoringTab**

In `MonitoringTab` (around line 498), between the Global Warmer section and the Bot Engine section:

Current:

```tsx
      {/* ── Global Warmer ── */}
      <section>
        ...
      </section>

      {/* ── Bot Engine ── */}
      <section>
```

Add the new section between them:

```tsx
      {/* ── Global Warmer ── */}
      <section>
        <SectionHeader>Global Warmer</SectionHeader>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          <StatCard label="Символов"      value={dash(metrics?.global_warmer?.symbols)}                     color="blue"   />
          <StatCard label="Интервалов"    value={dash(metrics?.global_warmer?.intervals?.length)}            color="violet" />
          <StatCard label="Тёплых слотов" value={dash(metrics?.global_warmer?.warm_count)}                  color="green"  />
          <StatCard label="Всего слотов"  value={dash(metrics?.global_warmer?.total_slots)}                  color="blue"   />
          <StatCard label="Прогрев %"     value={dash(metrics?.global_warmer?.warm_pct, 1, '%')}             color="amber"  sub={`${dash(metrics?.global_warmer?.prefetch_ms)} ms prefetch`} />
          <StatCard label="Интервалы"     value={metrics?.global_warmer?.intervals?.join(', ') ?? '—'}       color="violet" />
        </div>
      </section>

      {/* ── Ticker Hub ── */}
      <section>
        <SectionHeader>Ticker Hub</SectionHeader>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <StatCard label="Символов"        value={dash(metrics?.ticker_hub?.symbols)}      color="blue"   />
          <StatCard
            label="С ценой"
            value={metrics?.ticker_hub != null
              ? `${dash(metrics.ticker_hub.warm_symbols)} / ${dash(metrics.ticker_hub.symbols)}`
              : '—'}
            color="green"
            sub={metrics?.ticker_hub != null && metrics.ticker_hub.symbols > 0
              ? `${((metrics.ticker_hub.warm_symbols / metrics.ticker_hub.symbols) * 100).toFixed(1)}%`
              : undefined}
          />
          <StatCard label="WS соединений"   value={dash(metrics?.ticker_hub?.ws_connections)} color="violet" />
        </div>
      </section>

      {/* ── Bot Engine ── */}
      <section>
```

- [ ] **Step 3: TypeScript check**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat: add Ticker Hub section to admin monitoring page"
```

---

## Verification

After all tasks, rebuild Go and check:

```
go build ./...
```

Start the backend. Check logs for:
```
ticker hub: reconnecting in 3s    ← normal on first connect attempt
```
After a few seconds, no more reconnect logs — WS connected.

Navigate to `/admin` → Мониторинг tab. Confirm:
- "Ticker Hub" section appears between Global Warmer and Bot Engine
- "Символов" shows ~500 (all linear USDT pairs)
- "С ценой" starts at 0/500 and climbs to 500/500 within 30 seconds as WS prices arrive
- "WS соединений" shows ~13

Start a DCA/matrix strategy. Confirm in Go logs:
- No more `matrix price monitor: FetchMarkPrice` REST log lines
- Matrix level crossings fire immediately when price moves

**Пересборка нужна:** `go build ./...` после изменений Go; фронтенд перезагружается автоматически через Vite.
