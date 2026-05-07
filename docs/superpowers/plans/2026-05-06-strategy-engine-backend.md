# Strategy Engine Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Autonomous per-account strategy engine that manages grid orders, take-profit, stop-loss, cycle restart, and reconciliation against Bybit, running as goroutines inside `api-gateway`.

**Architecture:** `pkg/strategy` package with an `Engine` (goroutine coordinator) → per-account `AccountRunner` (one Bybit private WS per account) → per-strategy `StrategyRunner` (cycle state machine). Engine is started in `main.go` alongside the existing HTTP server. REST handlers for strategy CRUD call `engine.Notify()` to update running state without restart. Orders placed on exchange are tracked in DB (`strategy_levels`) by `exchange_order_id`; WS fill events are routed via an in-memory `orderIndex` map.

**Tech Stack:** Go, pgx/v5, gorilla/websocket (already in use), Bybit V5 private WS (`wss://stream.bybit.com/v5/private`)

---

## File Structure

### New Files
| File | Responsibility |
|------|----------------|
| `migrations/007_strategies.sql` | DB schema: strategies, strategy_cycles, strategy_levels |
| `pkg/trader/private_stream.go` | Reusable Bybit private WS subscription (callback-based, auto-reconnect) |
| `pkg/strategy/types.go` | Shared types: Strategy, Cycle, GridLevel, constants |
| `pkg/strategy/engine.go` | Engine + AccountRunner: lifecycle, WS routing, orderIndex |
| `pkg/strategy/cycle.go` | StrategyRunner: grid calculation, cycle start/stop, level fill, TP/SL |
| `pkg/strategy/reconcile.go` | Periodic reconciliation: DB vs exchange drift detection |
| `pkg/strategy/engine_test.go` | Unit tests for grid calculation and sliding window |
| `services/api-gateway/strategy_handler.go` | REST CRUD: GET/POST/PUT/DELETE /strategies, POST /strategies/{id}/status |

### Modified Files
| File | Change |
|------|--------|
| `pkg/trader/bybit.go` | Add `FetchMarkPrice()` |
| `services/api-gateway/main.go` | Add engine init, strategy routes |
| `services/api-gateway/server.go` (or wherever `Server` is defined) | Add `engine *strategy.Engine` field |

---

## Task 1: DB Migration

**Files:**
- Create: `migrations/007_strategies.sql`

- [ ] **Step 1: Write migration**

```sql
-- migrations/007_strategies.sql

CREATE TABLE strategies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    UUID NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    symbol        TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT 'linear',
    direction     TEXT NOT NULL DEFAULT 'long',   -- long | short | both
    status        TEXT NOT NULL DEFAULT 'stopped', -- active | finishing | stopped

    grid_levels   INT          NOT NULL DEFAULT 5,
    grid_active   INT          NOT NULL DEFAULT 3,   -- how many to keep on exchange
    grid_step_pct NUMERIC(10,4) NOT NULL DEFAULT 1.0,
    grid_size_usdt NUMERIC(18,2) NOT NULL DEFAULT 100,

    tp_mode       TEXT          NOT NULL DEFAULT 'total', -- per_level | total
    tp_pct        NUMERIC(10,4) NOT NULL DEFAULT 2.0,

    sl_type       TEXT          NOT NULL DEFAULT 'conditional', -- conditional | programmatic
    sl_pct        NUMERIC(10,4) NOT NULL DEFAULT 5.0,

    signal_filter BOOLEAN NOT NULL DEFAULT FALSE,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE strategy_cycles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    cycle_num   INT  NOT NULL,
    start_price NUMERIC(18,8),
    tp_order_id TEXT,
    sl_order_id TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    result      TEXT,            -- tp | sl | manual  (NULL = active)
    realized_pnl NUMERIC(18,8),
    UNIQUE(strategy_id, cycle_num)
);

CREATE TABLE strategy_levels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id       UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    cycle_id          UUID NOT NULL REFERENCES strategy_cycles(id) ON DELETE CASCADE,
    level_idx         INT  NOT NULL,
    side              TEXT NOT NULL,   -- Buy | Sell
    target_price      NUMERIC(18,8) NOT NULL,
    size_usdt         NUMERIC(18,2) NOT NULL,
    qty               TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending | placed | filled | cancelled
    exchange_order_id TEXT,
    exchange_link_id  TEXT,
    placed_at         TIMESTAMPTZ,
    filled_at         TIMESTAMPTZ,
    filled_price      NUMERIC(18,8)
);

CREATE INDEX idx_strategy_cycles_strategy ON strategy_cycles(strategy_id);
CREATE INDEX idx_strategy_levels_cycle    ON strategy_levels(cycle_id);
CREATE INDEX idx_strategy_levels_order_id ON strategy_levels(exchange_order_id)
    WHERE exchange_order_id IS NOT NULL;
```

- [ ] **Step 2: Apply migration**

```bash
# The existing db.Migrate() auto-runs all files in migrations/ on startup.
# Restart api-gateway to apply, or run manually:
psql $DATABASE_URL -f migrations/007_strategies.sql
```

Expected: tables `strategies`, `strategy_cycles`, `strategy_levels` created with no errors.

- [ ] **Step 3: Commit**

```bash
git add migrations/007_strategies.sql
git commit -m "feat: strategy engine DB schema"
```

---

## Task 2: FetchMarkPrice in pkg/trader/bybit.go

**Files:**
- Modify: `pkg/trader/bybit.go`

- [ ] **Step 1: Add FetchMarkPrice at end of bybit.go**

```go
// FetchMarkPrice returns the current mark price for a symbol from Bybit REST.
func FetchMarkPrice(ctx context.Context, creds Credentials, category, symbol string) (float64, error) {
	q := "category=" + category + "&symbol=" + symbol
	data, err := doSignedGET(ctx, creds, "/v5/market/tickers", q)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			List []struct {
				MarkPrice string `json:"markPrice"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if len(resp.Result.List) == 0 {
		return 0, fmt.Errorf("no ticker for %s/%s", category, symbol)
	}
	price, err := strconv.ParseFloat(resp.Result.List[0].MarkPrice, 64)
	if err != nil {
		return 0, fmt.Errorf("parse mark price: %w", err)
	}
	return price, nil
}
```

Add `"strconv"` to the import block in bybit.go (it may already be there — check first).

- [ ] **Step 2: Build to verify**

```bash
go build ./pkg/trader/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/trader/bybit.go
git commit -m "feat: add FetchMarkPrice to trader pkg"
```

---

## Task 3: Bybit Private Stream for Engine

**Files:**
- Create: `pkg/trader/private_stream.go`

This provides a callback-based Bybit private WS subscription reused by the engine. Different from `RunPositionStream` (which relays to a frontend WS), this calls a Go interface method on every order fill.

- [ ] **Step 1: Write the file**

```go
package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// OrderEvent carries order data from Bybit private WS "order" topic.
type OrderEvent struct {
	OrderID     string `json:"orderId"`
	OrderLinkID string `json:"orderLinkId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	OrderStatus string `json:"orderStatus"`
	AvgPrice    string `json:"avgPrice"`
	CumExecQty  string `json:"cumExecQty"`
	OrderType   string `json:"orderType"`
	Category    string `json:"category"`
	OrderFilter string `json:"orderFilter"`
}

// PrivateStreamHandler receives events from Bybit private WS.
type PrivateStreamHandler interface {
	OnOrderEvent(ev OrderEvent)
	OnConnected()
	OnDisconnected(err error)
}

// RunPrivateStream connects to Bybit private WS, subscribes to "order" topic,
// and dispatches events to handler. Blocks until ctx is cancelled. Auto-reconnects.
func RunPrivateStream(ctx context.Context, creds Credentials, handler PrivateStreamHandler) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := runPrivateOnce(ctx, creds, handler); err != nil {
			handler.OnDisconnected(err)
			log.Printf("trader private stream: disconnected (%v), retry in 5s", err)
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func runPrivateOnce(ctx context.Context, creds Credentials, handler PrivateStreamHandler) error {
	ts := serverTimestamp()
	var tsMs int64
	fmt.Sscanf(ts, "%d", &tsMs)
	expires := tsMs + 10000

	sigStr := fmt.Sprintf("GET/realtime%d", expires)
	wsSign := hmacSHA256(creds.SecretKey, sigStr)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	authMsg, _ := json.Marshal(map[string]any{
		"op":   "auth",
		"args": []any{creds.APIKey, expires, wsSign},
	})
	if err := conn.WriteMessage(websocket.TextMessage, authMsg); err != nil {
		return err
	}

	ping := time.NewTicker(20 * time.Second)
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

	subscribed := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			p, _ := json.Marshal(map[string]string{"op": "ping"})
			conn.WriteMessage(websocket.TextMessage, p) //nolint:errcheck
		case err := <-errCh:
			return err
		case data := <-msgCh:
			var raw map[string]any
			if json.Unmarshal(data, &raw) != nil {
				continue
			}
			op, _ := raw["op"].(string)
			switch op {
			case "auth":
				if ok, _ := raw["success"].(bool); ok && !subscribed {
					sub, _ := json.Marshal(map[string]any{
						"op":   "subscribe",
						"args": []string{"order"},
					})
					conn.WriteMessage(websocket.TextMessage, sub) //nolint:errcheck
					subscribed = true
					handler.OnConnected()
				} else if !ok {
					return fmt.Errorf("auth failed: %v", raw["ret_msg"])
				}
			case "pong":
				// ignore
			default:
				topic, _ := raw["topic"].(string)
				if topic == "order" {
					items, ok := raw["data"].([]any)
					if !ok {
						continue
					}
					for _, item := range items {
						b, _ := json.Marshal(item)
						var ev OrderEvent
						if json.Unmarshal(b, &ev) == nil {
							handler.OnOrderEvent(ev)
						}
					}
				}
			}
		}
	}
}

// hmacSHA256 returns hex-encoded HMAC-SHA256 of msg with key.
func hmacSHA256(key, msg string) string {
	import_mac := hmacNew([]byte(key))  // placeholder — see Step 2 for real code
	_ = import_mac
	return ""
}
```

**Note:** The `hmacSHA256` above is a placeholder. In Step 2 we replace it with the real implementation using the existing `sign()` helper in bybit.go.

- [ ] **Step 2: Replace hmacSHA256 placeholder with real crypto**

The file `pkg/trader/ws.go` already has the HMAC logic inline. Extract it into a shared helper in `private_stream.go`. Replace the entire `hmacSHA256` function with:

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func hmacHex(key, msg string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}
```

And in `runPrivateOnce`, replace `hmacSHA256(creds.SecretKey, sigStr)` with `hmacHex(creds.SecretKey, sigStr)`.

Also update `pkg/trader/ws.go` to use `hmacHex` instead of its inline HMAC block (search for `mac := hmac.New(sha256.New` in ws.go and replace):

In `ws.go`, the auth block currently has:
```go
mac := hmac.New(sha256.New, []byte(creds.SecretKey))
mac.Write([]byte(sigStr))
wsSign := hex.EncodeToString(mac.Sum(nil))
```
Replace with:
```go
wsSign := hmacHex(creds.SecretKey, sigStr)
```

Then remove the unused `crypto/hmac`, `crypto/sha256`, `encoding/hex` imports from ws.go (they are now only in private_stream.go).

- [ ] **Step 3: Build**

```bash
go build ./pkg/trader/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add pkg/trader/private_stream.go pkg/trader/ws.go
git commit -m "feat: add callback-based Bybit private WS stream for strategy engine"
```

---

## Task 4: pkg/strategy/types.go

**Files:**
- Create: `pkg/strategy/types.go`

- [ ] **Step 1: Write the file**

```go
package strategy

import "time"

type Status string

const (
	StatusActive    Status = "active"
	StatusFinishing Status = "finishing"
	StatusStopped   Status = "stopped"
)

type Direction string

const (
	DirectionLong  Direction = "long"
	DirectionShort Direction = "short"
	DirectionBoth  Direction = "both"
)

type TPMode string

const (
	TPModeTotal    TPMode = "total"
	TPModePerLevel TPMode = "per_level"
)

type SLType string

const (
	SLTypeConditional  SLType = "conditional"
	SLTypeProgrammatic SLType = "programmatic"
)

type LevelStatus string

const (
	LevelPending   LevelStatus = "pending"
	LevelPlaced    LevelStatus = "placed"
	LevelFilled    LevelStatus = "filled"
	LevelCancelled LevelStatus = "cancelled"
)

type Strategy struct {
	ID           string
	OwnerID      string
	AccountID    string
	Symbol       string
	Category     string
	Direction    Direction
	Status       Status
	GridLevels   int
	GridActive   int
	GridStepPct  float64
	GridSizeUSDT float64
	TPMode       TPMode
	TPPct        float64
	SLType       SLType
	SLPct        float64
	SignalFilter bool
}

type Cycle struct {
	ID         string
	StrategyID string
	CycleNum   int
	StartPrice float64
	TPOrderID  string
	SLOrderID  string
	StartedAt  time.Time
}

type GridLevel struct {
	ID              string
	LevelIdx        int
	Side            string
	TargetPrice     float64
	SizeUSDT        float64
	Qty             string
	Status          LevelStatus
	ExchangeOrderID string
	FilledPrice     float64 // 0 if not filled
}
```

- [ ] **Step 2: Build**

```bash
go build ./pkg/strategy/...
```

Expected: no errors (empty package is fine at this stage).

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/types.go
git commit -m "feat: strategy engine shared types"
```

---

## Task 5: pkg/strategy/engine.go + engine_test.go

**Files:**
- Create: `pkg/strategy/engine.go`
- Create: `pkg/strategy/engine_test.go`

The `Engine` manages per-account `AccountRunner`s. Each `AccountRunner` holds one Bybit private WS connection and routes order fills to the correct `StrategyRunner` via an in-memory `orderIndex`.

- [ ] **Step 1: Write the failing test first**

```go
// pkg/strategy/engine_test.go
package strategy

import (
	"math"
	"testing"
)

func TestCalculateGridLevels_Long(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 5, "Buy")
	// level[i] = 100 * (1-0.01)^(i+1)
	expected := []float64{99.0, 98.01, 97.0299, 96.0596, 95.0990}
	if len(prices) != 5 {
		t.Fatalf("want 5 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestCalculateGridLevels_Short(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 3, "Sell")
	// level[i] = 100 * (1+0.01)^(i+1)
	expected := []float64{101.0, 102.01, 103.0301}
	if len(prices) != 3 {
		t.Fatalf("want 3 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestPlacedCount(t *testing.T) {
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelPlaced},
			{Status: LevelPending},
			{Status: LevelFilled},
			{Status: LevelPlaced},
		},
	}
	if got := sr.placedCount(); got != 2 {
		t.Errorf("want 2 placed, got %d", got)
	}
}

func TestAvgEntry(t *testing.T) {
	p1, p2 := 99.0, 98.0
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelFilled, FilledPrice: p1, Qty: "1.0"},
			{Status: LevelFilled, FilledPrice: p2, Qty: "1.0"},
			{Status: LevelPending},
		},
	}
	avg, total := sr.avgEntry()
	if math.Abs(avg-98.5) > 0.001 {
		t.Errorf("want avg 98.5, got %.4f", avg)
	}
	if math.Abs(total-2.0) > 0.001 {
		t.Errorf("want total 2.0, got %.4f", total)
	}
}
```

- [ ] **Step 2: Run tests — expect compile failure (functions not defined yet)**

```bash
go test ./pkg/strategy/... 2>&1 | head -20
```

Expected: compile errors mentioning `calculateGridLevels`, `StrategyRunner`, etc. not defined.

- [ ] **Step 3: Write engine.go**

```go
// pkg/strategy/engine.go
package strategy

import (
	"context"
	"log"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
	"sis/pkg/trader"
)

// orderRef identifies what a placed exchange order belongs to.
type orderRef struct {
	strategyID string
	levelID    string // UUID of strategy_levels row; empty for tp/sl
	refType    string // "level" | "tp" | "sl"
}

// Engine coordinates all strategy runners, grouped by account.
type Engine struct {
	pool    *pgxpool.Pool
	encKey  []byte
	mu      sync.RWMutex
	runners map[string]*AccountRunner // accountID → runner
}

// New creates a new Engine. Call Start(ctx) to begin.
func New(pool *pgxpool.Pool, encKey []byte) *Engine {
	return &Engine{pool: pool, encKey: encKey, runners: make(map[string]*AccountRunner)}
}

// Start loads all active/finishing strategies from DB and launches account runners.
func (e *Engine) Start(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter
		 FROM strategies WHERE status IN ('active','finishing')`)
	if err != nil {
		log.Printf("strategy engine: load: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var s Strategy
		if err := scanStrategy(rows, &s); err == nil {
			e.loadStrategy(ctx, s)
		}
	}
}

// Notify reloads a strategy from DB after a REST update (status change or param edit).
func (e *Engine) Notify(ctx context.Context, strategyID string) {
	var s Strategy
	row := e.pool.QueryRow(ctx,
		`SELECT id, owner_id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter
		 FROM strategies WHERE id=$1`, strategyID)
	if err := scanStrategyRow(row, &s); err != nil {
		log.Printf("strategy engine: notify %s: %v", strategyID, err)
		return
	}
	if s.Status == StatusStopped {
		e.mu.RLock()
		runner := e.runners[s.AccountID]
		e.mu.RUnlock()
		if runner != nil {
			runner.removeStrategy(ctx, strategyID)
		}
		return
	}
	e.loadStrategy(ctx, s)
}

func (e *Engine) loadStrategy(ctx context.Context, s Strategy) {
	e.mu.Lock()
	runner, ok := e.runners[s.AccountID]
	if !ok {
		creds, err := e.loadCreds(ctx, s.AccountID)
		if err != nil {
			e.mu.Unlock()
			log.Printf("strategy engine: creds for account %s: %v", s.AccountID, err)
			return
		}
		runCtx, cancel := context.WithCancel(ctx)
		runner = newAccountRunner(s.AccountID, creds, e.pool, cancel)
		e.runners[s.AccountID] = runner
		e.mu.Unlock()
		go runner.run(runCtx)
	} else {
		e.mu.Unlock()
	}
	runner.addStrategy(ctx, s)
}

func (e *Engine) loadCreds(ctx context.Context, accountID string) (trader.Credentials, error) {
	var apiKeyEnc, secretEnc string
	if err := e.pool.QueryRow(ctx,
		`SELECT api_key_enc, secret_enc FROM exchange_accounts WHERE id=$1`, accountID,
	).Scan(&apiKeyEnc, &secretEnc); err != nil {
		return trader.Credentials{}, err
	}
	apiKey, err := crypto.Decrypt(apiKeyEnc, e.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	secret, err := crypto.Decrypt(secretEnc, e.encKey)
	if err != nil {
		return trader.Credentials{}, err
	}
	return trader.Credentials{APIKey: apiKey, SecretKey: secret}, nil
}

// scanStrategy scans a pgx row into a Strategy. Used with Query (rows.Scan).
func scanStrategy(rows interface{ Scan(...any) error }, s *Strategy) error {
	var dir, stat, tpm, slt string
	var sf bool
	err := rows.Scan(
		&s.ID, &s.OwnerID, &s.AccountID, &s.Symbol, &s.Category,
		&dir, &stat,
		&s.GridLevels, &s.GridActive, &s.GridStepPct, &s.GridSizeUSDT,
		&tpm, &s.TPPct, &slt, &s.SLPct, &sf,
	)
	if err != nil {
		return err
	}
	s.Direction = Direction(dir)
	s.Status = Status(stat)
	s.TPMode = TPMode(tpm)
	s.SLType = SLType(slt)
	s.SignalFilter = sf
	return nil
}

// scanStrategyRow scans a pgx.Row into a Strategy.
func scanStrategyRow(row interface{ Scan(...any) error }, s *Strategy) error {
	return scanStrategy(row, s)
}

// ─── AccountRunner ──────────────────────────────────────────────────────────

// AccountRunner owns one Bybit private WS connection and all strategies for one account.
type AccountRunner struct {
	accountID  string
	creds      trader.Credentials
	pool       *pgxpool.Pool
	mu         sync.RWMutex
	strategies map[string]*StrategyRunner
	orderIndex map[string]orderRef // exchangeOrderID → ref
	cancel     context.CancelFunc
}

func newAccountRunner(accountID string, creds trader.Credentials, pool *pgxpool.Pool, cancel context.CancelFunc) *AccountRunner {
	return &AccountRunner{
		accountID:  accountID,
		creds:      creds,
		pool:       pool,
		strategies: make(map[string]*StrategyRunner),
		orderIndex: make(map[string]orderRef),
		cancel:     cancel,
	}
}

func (ar *AccountRunner) run(ctx context.Context) {
	go ar.startReconcileLoop(ctx)
	trader.RunPrivateStream(ctx, ar.creds, ar)
}

func (ar *AccountRunner) addStrategy(ctx context.Context, s Strategy) {
	ar.mu.Lock()
	existing, ok := ar.strategies[s.ID]
	if ok {
		existing.mu.Lock()
		existing.strategy = s
		existing.mu.Unlock()
		ar.mu.Unlock()
		return
	}
	sr := &StrategyRunner{strategy: s, runner: ar}
	ar.strategies[s.ID] = sr
	ar.mu.Unlock()
	go sr.loadOrStart(ctx)
}

func (ar *AccountRunner) removeStrategy(ctx context.Context, strategyID string) {
	ar.mu.Lock()
	sr, ok := ar.strategies[strategyID]
	if !ok {
		ar.mu.Unlock()
		return
	}
	delete(ar.strategies, strategyID)
	for id, ref := range ar.orderIndex {
		if ref.strategyID == strategyID {
			delete(ar.orderIndex, id)
		}
	}
	ar.mu.Unlock()
	go sr.cancelAllPlaced(ctx)
}

// RegisterOrder adds an exchange order → strategy mapping so WS events can be routed.
func (ar *AccountRunner) RegisterOrder(exchangeOrderID string, ref orderRef) {
	ar.mu.Lock()
	ar.orderIndex[exchangeOrderID] = ref
	ar.mu.Unlock()
}

// UnregisterOrder removes a mapping (called on fill or cancel).
func (ar *AccountRunner) UnregisterOrder(exchangeOrderID string) {
	ar.mu.Lock()
	delete(ar.orderIndex, exchangeOrderID)
	ar.mu.Unlock()
}

// OnOrderEvent implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnOrderEvent(ev trader.OrderEvent) {
	if ev.OrderStatus != "Filled" {
		return
	}
	ar.mu.RLock()
	ref, ok := ar.orderIndex[ev.OrderID]
	if !ok {
		ar.mu.RUnlock()
		return
	}
	sr := ar.strategies[ref.strategyID]
	ar.mu.RUnlock()
	if sr == nil {
		return
	}
	ctx := context.Background()
	switch ref.refType {
	case "level":
		price, _ := strconv.ParseFloat(ev.AvgPrice, 64)
		qty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
		sr.handleLevelFill(ctx, ref.levelID, price, qty)
	case "tp":
		sr.handleTPFill(ctx)
	case "sl":
		sr.handleSLFill(ctx)
	}
}

// OnConnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnConnected() {
	log.Printf("strategy: bybit WS connected account=%s", ar.accountID)
}

// OnDisconnected implements trader.PrivateStreamHandler.
func (ar *AccountRunner) OnDisconnected(err error) {
	log.Printf("strategy: bybit WS disconnected account=%s err=%v", ar.accountID, err)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./pkg/strategy/... -v -run TestCalculateGridLevels
```

Expected: FAIL — `calculateGridLevels` not defined yet (it's in cycle.go, next task).

- [ ] **Step 5: Commit partial (engine.go only)**

```bash
git add pkg/strategy/engine.go pkg/strategy/engine_test.go
git commit -m "feat: strategy engine and account runner skeleton"
```

---

## Task 6: pkg/strategy/cycle.go

**Files:**
- Create: `pkg/strategy/cycle.go`

Contains: `StrategyRunner`, `calculateGridLevels`, cycle start/resume, level fill handling, TP/SL management.

- [ ] **Step 1: Write the file**

```go
// pkg/strategy/cycle.go
package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"sync"
	"time"

	"sis/pkg/trader"
)

// StrategyRunner holds the runtime state of one strategy.
type StrategyRunner struct {
	mu       sync.Mutex
	strategy Strategy
	runner   *AccountRunner

	cycle     *Cycle
	levels    []GridLevel // all levels for current cycle, sorted by level_idx asc
	tpOrderID string
	slOrderID string
}

// calculateGridLevels returns target prices for all N grid levels.
// For Buy: prices below base (compound step down each level).
// For Sell: prices above base (compound step up each level).
func calculateGridLevels(basePrice, stepPct float64, count int, side string) []float64 {
	prices := make([]float64, count)
	for i := 0; i < count; i++ {
		exp := float64(i + 1)
		if side == "Buy" {
			prices[i] = basePrice * math.Pow(1-stepPct/100, exp)
		} else {
			prices[i] = basePrice * math.Pow(1+stepPct/100, exp)
		}
	}
	return prices
}

// loadOrStart resumes an existing active cycle from DB or starts a fresh one.
func (sr *StrategyRunner) loadOrStart(ctx context.Context) {
	if err := sr.loadActiveCycle(ctx); err != nil {
		// No active cycle in DB — start fresh.
		if err2 := sr.startCycle(ctx); err2 != nil {
			log.Printf("strategy %s: start cycle: %v", sr.strategy.ID, err2)
		}
	}
}

// loadActiveCycle loads the current cycle and placed levels from DB.
// Returns an error if there is no active (unfinished) cycle.
func (sr *StrategyRunner) loadActiveCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var c Cycle
	var tpID, slID *string
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT id, strategy_id, cycle_num, start_price, started_at, tp_order_id, sl_order_id
		 FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL
		 ORDER BY cycle_num DESC LIMIT 1`,
		sr.strategy.ID,
	).Scan(&c.ID, &c.StrategyID, &c.CycleNum, &c.StartPrice, &c.StartedAt, &tpID, &slID)
	if err != nil {
		return err
	}
	if tpID != nil {
		c.TPOrderID = *tpID
	}
	if slID != nil {
		c.SLOrderID = *slID
	}
	sr.cycle = &c
	sr.tpOrderID = c.TPOrderID
	sr.slOrderID = c.SLOrderID

	// Load levels.
	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(exchange_order_id,''), COALESCE(filled_price,0)
		 FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx ASC`,
		c.ID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var l GridLevel
		var stat string
		if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
			&l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice); err != nil {
			continue
		}
		l.Status = LevelStatus(stat)
		sr.levels = append(sr.levels, l)
		if l.Status == LevelPlaced && l.ExchangeOrderID != "" {
			sr.runner.RegisterOrder(l.ExchangeOrderID, orderRef{
				strategyID: sr.strategy.ID,
				levelID:    l.ID,
				refType:    "level",
			})
		}
	}
	// Re-register TP/SL.
	if sr.tpOrderID != "" {
		sr.runner.RegisterOrder(sr.tpOrderID, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	}
	if sr.slOrderID != "" {
		sr.runner.RegisterOrder(sr.slOrderID, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	}
	log.Printf("strategy %s: resumed cycle %d with %d levels", sr.strategy.ID, c.CycleNum, len(sr.levels))
	return nil
}

// startCycle creates a new cycle: fetches price, calculates grid, places initial window.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) startCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		return fmt.Errorf("fetch price: %w", err)
	}

	var maxCycle int
	sr.runner.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(cycle_num),0) FROM strategy_cycles WHERE strategy_id=$1`,
		sr.strategy.ID,
	).Scan(&maxCycle) //nolint:errcheck

	var cycleID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price) VALUES ($1,$2,$3) RETURNING id`,
		sr.strategy.ID, maxCycle+1, price,
	).Scan(&cycleID); err != nil {
		return fmt.Errorf("insert cycle: %w", err)
	}
	sr.cycle = &Cycle{
		ID: cycleID, StrategyID: sr.strategy.ID,
		CycleNum: maxCycle + 1, StartPrice: price, StartedAt: time.Now(),
	}
	sr.levels = nil

	sides := sidesForDirection(sr.strategy.Direction)
	levelIdx := 1
	for _, side := range sides {
		prices := calculateGridLevels(price, sr.strategy.GridStepPct, sr.strategy.GridLevels, side)
		for _, p := range prices {
			qty := strconv.FormatFloat(sr.strategy.GridSizeUSDT/p, 'f', 6, 64)
			var levelID string
			if err := sr.runner.pool.QueryRow(ctx,
				`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty)
				 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
				sr.strategy.ID, cycleID, levelIdx, side, p, sr.strategy.GridSizeUSDT, qty,
			).Scan(&levelID); err != nil {
				log.Printf("strategy %s: insert level %d: %v", sr.strategy.ID, levelIdx, err)
				levelIdx++
				continue
			}
			sr.levels = append(sr.levels, GridLevel{
				ID: levelID, LevelIdx: levelIdx, Side: side,
				TargetPrice: p, SizeUSDT: sr.strategy.GridSizeUSDT, Qty: qty,
				Status: LevelPending,
			})
			levelIdx++
		}
	}

	log.Printf("strategy %s: started cycle %d at %.2f with %d levels",
		sr.strategy.ID, sr.cycle.CycleNum, price, len(sr.levels))
	return sr.placeNextLevels(ctx)
}

func sidesForDirection(d Direction) []string {
	switch d {
	case DirectionLong:
		return []string{"Buy"}
	case DirectionShort:
		return []string{"Sell"}
	default:
		return []string{"Buy", "Sell"}
	}
}

// placeNextLevels places pending levels until the sliding window (GridActive) is full.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeNextLevels(ctx context.Context) error {
	need := sr.strategy.GridActive - sr.placedCount()
	for i := range sr.levels {
		if need <= 0 {
			break
		}
		if sr.levels[i].Status != LevelPending {
			continue
		}
		if err := sr.placeLevel(ctx, i); err != nil {
			log.Printf("strategy %s: place level %d: %v", sr.strategy.ID, sr.levels[i].LevelIdx, err)
			continue
		}
		need--
	}
	return nil
}

// placeLevel places a single grid level on the exchange.
// Must be called with sr.mu held. Modifies sr.levels[idx] in place.
func (sr *StrategyRunner) placeLevel(ctx context.Context, idx int) error {
	l := &sr.levels[idx]
	linkID := fmt.Sprintf("STR-%s-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        l.Side,
		OrderType:   "Limit",
		Qty:         l.Qty,
		Price:       fmt.Sprintf("%.4f", l.TargetPrice),
		TimeInForce: "GoodTillCancel",
		OrderLinkId: linkID,
	})
	if err != nil {
		return err
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW() WHERE id=$3`,
		result.OrderId, linkID, l.ID,
	); err != nil {
		return err
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"})
	log.Printf("strategy %s cycle %d: placed level %d %s @ %.4f order=%s",
		sr.strategy.ID, sr.cycle.CycleNum, l.LevelIdx, l.Side, l.TargetPrice, result.OrderId)
	return nil
}

func (sr *StrategyRunner) placedCount() int {
	n := 0
	for _, l := range sr.levels {
		if l.Status == LevelPlaced {
			n++
		}
	}
	return n
}

// avgEntry returns the weighted average fill price and total quantity across filled levels.
func (sr *StrategyRunner) avgEntry() (avg, totalQty float64) {
	for _, l := range sr.levels {
		if l.Status != LevelFilled || l.FilledPrice == 0 {
			continue
		}
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		avg += l.FilledPrice * qty
		totalQty += qty
	}
	if totalQty > 0 {
		avg /= totalQty
	}
	return
}

// handleLevelFill is called by AccountRunner when a grid level order is filled.
func (sr *StrategyRunner) handleLevelFill(ctx context.Context, levelID string, filledPrice, _ float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			sr.runner.UnregisterOrder(sr.levels[i].ExchangeOrderID)
			sr.levels[i].Status = LevelFilled
			sr.levels[i].FilledPrice = filledPrice
			break
		}
	}
	if err := sr.updateTP(ctx); err != nil {
		log.Printf("strategy %s: updateTP: %v", sr.strategy.ID, err)
	}
	if err := sr.placeNextLevels(ctx); err != nil {
		log.Printf("strategy %s: placeNextLevels: %v", sr.strategy.ID, err)
	}
}

// updateTP cancels the existing TP order (if any) and places a new one for the full position.
// Must be called with sr.mu held.
func (sr *StrategyRunner) updateTP(ctx context.Context) error {
	avg, totalQty := sr.avgEntry()
	if totalQty == 0 || avg == 0 {
		return nil
	}
	// Cancel existing TP.
	if sr.tpOrderID != "" {
		trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{ //nolint:errcheck
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: sr.tpOrderID,
		})
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}

	tpSide, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
	if tpSide == "" {
		return nil // "both" direction: skip global TP for now
	}

	tpQty := strconv.FormatFloat(totalQty, 'f', 6, 64)
	linkID := fmt.Sprintf("STP-%s-tp-%d", sr.strategy.ID[:8], sr.cycle.CycleNum)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        tpSide,
		OrderType:   "Limit",
		Qty:         tpQty,
		Price:       fmt.Sprintf("%.4f", tpPrice),
		TimeInForce: "GoodTillCancel",
		ReduceOnly:  true,
		OrderLinkId: linkID,
	})
	if err != nil {
		return fmt.Errorf("place TP: %w", err)
	}
	sr.tpOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	log.Printf("strategy %s cycle %d: TP placed @ %.4f qty=%s order=%s",
		sr.strategy.ID, sr.cycle.CycleNum, tpPrice, tpQty, result.OrderId)
	return nil
}

func tpParams(dir Direction, avg, tpPct float64) (side string, price float64) {
	switch dir {
	case DirectionLong:
		return "Sell", avg * (1 + tpPct/100)
	case DirectionShort:
		return "Buy", avg * (1 - tpPct/100)
	default:
		return "", 0
	}
}

// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	log.Printf("strategy %s: TP filled, cycle %d done", sr.strategy.ID, sr.cycle.CycleNum)
	sr.closeCycle(ctx, "tp")
	sr.maybeRestart(ctx)
}

// handleSLFill is called when the SL order is filled.
func (sr *StrategyRunner) handleSLFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	log.Printf("strategy %s: SL filled, cycle %d done", sr.strategy.ID, sr.cycle.CycleNum)
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "sl")
	sr.maybeRestart(ctx)
}

// maybeRestart checks status and either starts a new cycle or transitions to stopped.
// Must be called with sr.mu held.
func (sr *StrategyRunner) maybeRestart(ctx context.Context) {
	switch sr.strategy.Status {
	case StatusActive:
		go func() {
			if err := sr.startCycle(ctx); err != nil {
				log.Printf("strategy %s: restart cycle: %v", sr.strategy.ID, err)
			}
		}()
	case StatusFinishing:
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID)
		sr.strategy.Status = StatusStopped
		log.Printf("strategy %s: finishing → stopped", sr.strategy.ID)
	}
}

// closeCycle marks the current cycle as ended in DB and clears local state.
// Must be called with sr.mu held.
func (sr *StrategyRunner) closeCycle(ctx context.Context, result string) {
	if sr.cycle == nil {
		return
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET ended_at=NOW(), result=$1 WHERE id=$2`, result, sr.cycle.ID)
	if sr.tpOrderID != "" {
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}
	if sr.slOrderID != "" {
		sr.runner.UnregisterOrder(sr.slOrderID)
		sr.slOrderID = ""
	}
	sr.cycle = nil
	sr.levels = nil
}

// cancelPlacedLevels cancels all placed (unexecuted) grid level orders on the exchange.
// Must be called with sr.mu held.
func (sr *StrategyRunner) cancelPlacedLevels(ctx context.Context) {
	for _, l := range sr.levels {
		if l.Status != LevelPlaced {
			continue
		}
		trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{ //nolint:errcheck
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: l.ExchangeOrderID,
		})
		sr.runner.UnregisterOrder(l.ExchangeOrderID)
	}
	if sr.cycle != nil {
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='placed'`, sr.cycle.ID)
	}
}

// cancelAllPlaced is called when a strategy is stopped (from removeStrategy goroutine).
func (sr *StrategyRunner) cancelAllPlaced(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.cancelPlacedLevels(ctx)
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./pkg/strategy/... -v
```

Expected: All 4 tests pass (TestCalculateGridLevels_Long, TestCalculateGridLevels_Short, TestPlacedCount, TestAvgEntry).

- [ ] **Step 3: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/cycle.go
git commit -m "feat: strategy cycle state machine with grid calculation and TP/SL"
```

---

## Task 7: pkg/strategy/reconcile.go

**Files:**
- Create: `pkg/strategy/reconcile.go`

Periodic reconciliation: every 60s, compare `strategy_levels WHERE status='placed'` against live exchange orders. Re-place any missing ones.

- [ ] **Step 1: Write the file**

```go
// pkg/strategy/reconcile.go
package strategy

import (
	"context"
	"log"
	"time"

	"sis/pkg/trader"
)

// startReconcileLoop runs reconcile() every 60 seconds until ctx is cancelled.
func (ar *AccountRunner) startReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ar.reconcile(ctx)
		}
	}
}

// reconcile checks that every strategy_level with status='placed' still exists
// on the exchange. Missing orders are reset to 'pending' so placeNextLevels
// re-places them on the next fill event (or next reconcile cycle).
func (ar *AccountRunner) reconcile(ctx context.Context) {
	// 1. Fetch all placed level order IDs from DB for this account.
	rows, err := ar.pool.Query(ctx,
		`SELECT sl.id, sl.exchange_order_id, sl.strategy_id
		 FROM strategy_levels sl
		 JOIN strategies s ON s.id = sl.strategy_id
		 WHERE s.account_id = $1 AND sl.status = 'placed'`,
		ar.accountID,
	)
	if err != nil {
		log.Printf("strategy reconcile %s: query: %v", ar.accountID, err)
		return
	}
	type placed struct{ levelID, orderID, strategyID string }
	var dbPlaced []placed
	for rows.Next() {
		var p placed
		if err := rows.Scan(&p.levelID, &p.orderID, &p.strategyID); err == nil {
			dbPlaced = append(dbPlaced, p)
		}
	}
	rows.Close()

	if len(dbPlaced) == 0 {
		return
	}

	// 2. Fetch open orders from exchange for this account.
	exchangeOrders, err := trader.FetchOpenOrders(ctx, ar.creds)
	if err != nil {
		log.Printf("strategy reconcile %s: fetch exchange orders: %v", ar.accountID, err)
		return
	}
	live := make(map[string]bool, len(exchangeOrders))
	for _, o := range exchangeOrders {
		live[o.OrderId] = true
	}

	// 3. Find placed levels missing from exchange.
	for _, p := range dbPlaced {
		if live[p.orderID] {
			continue
		}
		log.Printf("strategy reconcile: level %s order %s missing from exchange — resetting to pending", p.levelID, p.orderID)
		ar.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
			p.levelID,
		)
		ar.UnregisterOrder(p.orderID)

		// Update local StrategyRunner level state.
		ar.mu.RLock()
		sr := ar.strategies[p.strategyID]
		ar.mu.RUnlock()
		if sr == nil {
			continue
		}
		sr.mu.Lock()
		for i := range sr.levels {
			if sr.levels[i].ID == p.levelID {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				break
			}
		}
		// Attempt to re-place immediately.
		sr.placeNextLevels(ctx) //nolint:errcheck
		sr.mu.Unlock()
	}
}
```

- [ ] **Step 2: Build**

```bash
go build ./pkg/strategy/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/reconcile.go
git commit -m "feat: strategy periodic reconciliation against exchange"
```

---

## Task 8: services/api-gateway/strategy_handler.go

**Files:**
- Create: `services/api-gateway/strategy_handler.go`

REST endpoints for strategy CRUD. After any status or param change, calls `s.engine.Notify()`.

- [ ] **Step 1: Write the file**

```go
// services/api-gateway/strategy_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type strategyPayload struct {
	AccountID    string  `json:"account_id"`
	Symbol       string  `json:"symbol"`
	Category     string  `json:"category"`
	Direction    string  `json:"direction"`
	GridLevels   int     `json:"grid_levels"`
	GridActive   int     `json:"grid_active"`
	GridStepPct  float64 `json:"grid_step_pct"`
	GridSizeUSDT float64 `json:"grid_size_usdt"`
	TPMode       string  `json:"tp_mode"`
	TPPct        float64 `json:"tp_pct"`
	SLType       string  `json:"sl_type"`
	SLPct        float64 `json:"sl_pct"`
	SignalFilter bool    `json:"signal_filter"`
}

// ListStrategies lists all strategies for the current user.
// GET /strategies
func (s *Server) ListStrategies(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, created_at, updated_at
		 FROM strategies WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		ID           string    `json:"id"`
		AccountID    string    `json:"account_id"`
		Symbol       string    `json:"symbol"`
		Category     string    `json:"category"`
		Direction    string    `json:"direction"`
		Status       string    `json:"status"`
		GridLevels   int       `json:"grid_levels"`
		GridActive   int       `json:"grid_active"`
		GridStepPct  float64   `json:"grid_step_pct"`
		GridSizeUSDT float64   `json:"grid_size_usdt"`
		TPMode       string    `json:"tp_mode"`
		TPPct        float64   `json:"tp_pct"`
		SLType       string    `json:"sl_type"`
		SLPct        float64   `json:"sl_pct"`
		SignalFilter bool      `json:"signal_filter"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Symbol, &r.Category, &r.Direction, &r.Status,
			&r.GridLevels, &r.GridActive, &r.GridStepPct, &r.GridSizeUSDT,
			&r.TPMode, &r.TPPct, &r.SLType, &r.SLPct, &r.SignalFilter, &r.CreatedAt, &r.UpdatedAt,
		); err == nil {
			result = append(result, r)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateStrategy creates a new strategy (status=stopped by default).
// POST /strategies
func (s *Server) CreateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "account_id and symbol are required")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}
	if req.Direction == "" {
		req.Direction = "long"
	}
	if req.GridLevels == 0 {
		req.GridLevels = 5
	}
	if req.GridActive == 0 {
		req.GridActive = 3
	}
	var id string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO strategies
		 (owner_id, account_id, symbol, category, direction,
		  grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		  tp_mode, tp_pct, sl_type, sl_pct, signal_filter)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 RETURNING id`,
		userID, req.AccountID, req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UpdateStrategy updates strategy parameters (but not status).
// PUT /strategies/{id}
func (s *Server) UpdateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET
		  symbol=$1, category=$2, direction=$3,
		  grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		  tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		  updated_at=NOW()
		 WHERE id=$13 AND owner_id=$14`,
		req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetStrategyStatus changes strategy status (active | finishing | stopped).
// POST /strategies/{id}/status
func (s *Server) SetStrategyStatus(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Status != "active" && req.Status != "finishing" && req.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "status must be active | finishing | stopped")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET status=$1, updated_at=NOW() WHERE id=$2 AND owner_id=$3`,
		req.Status, id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteStrategy deletes a strategy (only if stopped).
// DELETE /strategies/{id}
func (s *Server) DeleteStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM strategies WHERE id=$1 AND owner_id=$2 AND status='stopped'`,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusBadRequest, "strategy not found or not stopped")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 2: Build**

```bash
go build ./services/api-gateway/...
```

Expected: compile error — `s.engine` not defined on `Server` yet. Fix in Task 9.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/strategy_handler.go
git commit -m "feat: strategy REST CRUD handlers"
```

---

## Task 9: Wire Engine into main.go

**Files:**
- Modify: `services/api-gateway/main.go`
- Modify: `services/api-gateway/server.go` (find where `Server` struct is defined — check `server.go` or `main.go`)

- [ ] **Step 1: Find Server struct definition**

```bash
grep -n "type Server struct" services/api-gateway/*.go
```

Note the file and line number.

- [ ] **Step 2: Add engine field to Server struct**

Add to the `Server` struct:

```go
engine *strategy.Engine
```

Add to the `NewServer` function body (after creating the struct):

```go
s.engine = strategy.New(pool, []byte(encKey))
```

Add import:

```go
"sis/pkg/strategy"
```

- [ ] **Step 3: Start engine in main.go**

In `main()`, after `s := NewServer(...)`, add:

```go
// Start strategy engine.
go s.engine.Start(ctx)
```

- [ ] **Step 4: Add strategy routes**

In the protected routes group in `main.go`, add:

```go
// Strategies
r.Get("/strategies", s.ListStrategies)
r.Post("/strategies", s.CreateStrategy)
r.Put("/strategies/{id}", s.UpdateStrategy)
r.Post("/strategies/{id}/status", s.SetStrategyStatus)
r.Delete("/strategies/{id}", s.DeleteStrategy)
```

- [ ] **Step 5: Build and verify**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Run tests**

```bash
go test ./...
```

Expected: all existing tests pass, strategy tests pass.

- [ ] **Step 7: Commit**

```bash
git add services/api-gateway/main.go services/api-gateway/server.go
git commit -m "feat: wire strategy engine into api-gateway"
```

---

## Self-Review

### Spec coverage

| Requirement | Task |
|-------------|------|
| Strategies stored in DB | Task 1 |
| Direction: long / short / both | Task 6 (`sidesForDirection`) |
| Grid: N levels, K active on exchange (sliding window) | Task 6 (`placeNextLevels`) |
| Compound step % | Task 6 (`calculateGridLevels`) |
| TP total mode (cancel old, place new on fill) | Task 6 (`updateTP`) |
| TP per_level mode | ⚠️ Not implemented — `updateTP` only handles `total`. Per-level TP requires placing an individual TP per fill. Add as follow-up. |
| SL conditional | Task 6 (`handleSLFill` detects fill via WS) — **however SL order is never placed on exchange in this plan.** Add `placeSL()` call in `startCycle` as follow-up. |
| Status: active / finishing / stopped | Task 6 (`maybeRestart`) |
| Cycle restart after TP/SL | Task 6 (`maybeRestart`) |
| Resume after restart | Task 6 (`loadActiveCycle`) |
| Reconciliation | Task 7 |
| REST CRUD | Task 8 |
| Engine wired in main | Task 9 |

### Gaps to address as immediate follow-ups (not blocking backend MVP)

1. **`placeSL()` not called at cycle start** — add call in `startCycle` after `placeNextLevels`, placing a conditional stop-market order at `avg_entry * (1 - sl_pct/100)`. Since avg entry is unknown at cycle start, SL placement should happen after first level fills (move to `handleLevelFill` → `updateSL()`).

2. **`tp_mode = per_level`** — in `handleLevelFill`, if `TPMode == TPModePerLevel`, place an individual TP for this fill only (not replace the global TP).

3. **Signal filter** — not wired. Add a `SetSignal(symbol string, positive bool)` method to `AccountRunner`. Call it from a webhook endpoint. When negative: call `cancelPlacedLevels`; when positive: call `placeNextLevels`.

These gaps are tracked here so the frontend plan can assume they exist but the backend MVP is functional without them for the initial demo.
