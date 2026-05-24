# Matrix Strategy Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Go backend execution engine for `strategy_type = "dca"` (Matrix) — per-level independent SL orders, global TP from latest fill, Safe Zone re-entry, and a 5-second price monitor goroutine.

**Architecture:** New file `pkg/strategy/matrix.go` holds all matrix-specific logic. Minimal branches are added to `cycle.go` (`loadOrStart`, `loadActiveCycle`, `closeCycle`, `handleLevelFill`, `handleStopRequest`) and `engine.go` (`OnOrderEvent`). The existing `tpOrderID` field doubles as the global matrix TP; per-level SL orders are tracked in the extended `GridLevel` struct and registered in `orderIndex` under `refType = "matrix_sl"`.

**Tech Stack:** Go, pgx/v5, Bybit REST (`trader.FetchMarkPrice`, `trader.PlaceOrder`, `trader.CancelOrder`), existing `orderRef` / `RegisterOrder` / `UnregisterOrder` pattern.

---

## File Map

| Action | Path | Purpose |
|--------|------|---------|
| Create | `migrations/040_matrix_sl.sql` | Add `sl_order_id`, `sl_price`, `sl_replaced`, `slot` to `strategy_levels` |
| Modify | `pkg/strategy/types.go` | `LevelSLClosed` constant, `MatrixSafeZone` type, extend `GridLevel` |
| Modify | `pkg/strategy/cycle.go` | `StrategyRunner` fields, `loadActiveCycle`, `loadOrStart`, `handleLevelFill`, `closeCycle`, `handleStopRequest` |
| Modify | `pkg/strategy/engine.go` | `OnOrderEvent` — route `"matrix_sl"` ref type |
| Create | `pkg/strategy/matrix.go` | All matrix functions |
| Modify | `pkg/strategy/engine_test.go` | Pure-logic unit tests for matrix helpers |

---

## Task 1: DB Migration

**Files:**
- Create: `migrations/040_matrix_sl.sql`

- [ ] **Step 1: Write migration file**

```sql
-- migrations/040_matrix_sl.sql
ALTER TABLE strategy_levels
  ADD COLUMN IF NOT EXISTS sl_order_id  TEXT,
  ADD COLUMN IF NOT EXISTS sl_price     NUMERIC(18,8),
  ADD COLUMN IF NOT EXISTS sl_replaced  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS slot         SMALLINT;
-- 'sl_closed' is a valid value for the status column (no constraint — it is TEXT).
```

- [ ] **Step 2: Apply migration**

```
psql $DATABASE_URL -f migrations/040_matrix_sl.sql
```

Expected: `ALTER TABLE` (no errors).

- [ ] **Step 3: Commit**

```bash
git add migrations/040_matrix_sl.sql
git commit -m "feat: add matrix SL columns to strategy_levels (migration 040)"
```

---

## Task 2: Extend Types

**Files:**
- Modify: `pkg/strategy/types.go`

- [ ] **Step 1: Write failing test for new constants/types**

Add to `pkg/strategy/engine_test.go`:

```go
func TestLevelSLClosedConstant(t *testing.T) {
    if LevelSLClosed != "sl_closed" {
        t.Errorf("want sl_closed, got %s", LevelSLClosed)
    }
}

func TestMatrixSafeZoneContains(t *testing.T) {
    z := &MatrixSafeZone{Low: 90.0, High: 110.0}
    if !z.Contains(100.0) {
        t.Error("100 should be inside [90,110]")
    }
    if z.Contains(80.0) {
        t.Error("80 should be outside [90,110]")
    }
    if z.Contains(120.0) {
        t.Error("120 should be outside [90,110]")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestLevelSLClosedConstant -v
```

Expected: compile error `LevelSLClosed undefined`.

- [ ] **Step 3: Add `LevelSLClosed` constant, `MatrixSafeZone` type, and extend `GridLevel`**

In `pkg/strategy/types.go`, add after the existing `LevelCancelled` constant:

```go
LevelSLClosed LevelStatus = "sl_closed"
```

Add after the `Cycle` struct:

```go
type MatrixSafeZone struct {
    Low, High float64
    CreatedAt time.Time
}

// Contains reports whether price falls strictly inside the Safe Zone.
func (z *MatrixSafeZone) Contains(price float64) bool {
    return price >= z.Low && price <= z.High
}
```

Extend `GridLevel` (add after `FilledPrice float64`):

```go
// Matrix-only fields (nil/zero for grid strategies)
SLOrderID  string
SLPrice    float64
SLReplaced bool
Slot       *int   // nil = grid; matrix slot index: -N…0…+N
```

- [ ] **Step 4: Run test to verify it passes**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run "TestLevelSLClosedConstant|TestMatrixSafeZoneContains" -v
```

Expected: PASS.

- [ ] **Step 5: Extend `StrategyRunner` struct in `cycle.go`**

In `pkg/strategy/cycle.go`, add after the `signalMonitorID` / `currentSignalState` block in `StrategyRunner`:

```go
// Matrix strategy state
matrixSafeZone    *MatrixSafeZone
matrixMonitorStop context.CancelFunc
matrixSLSeq       int // increments on each per-level SL placement for unique linkIds
```

- [ ] **Step 6: Verify the project still compiles**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/types.go pkg/strategy/cycle.go pkg/strategy/engine_test.go
git commit -m "feat: add matrix types — LevelSLClosed, MatrixSafeZone, GridLevel SL fields"
```

---

## Task 3: Engine Routing for matrix_sl + loadActiveCycle Extension

**Files:**
- Modify: `pkg/strategy/engine.go` (OnOrderEvent)
- Modify: `pkg/strategy/cycle.go` (loadActiveCycle)

- [ ] **Step 1: Add `matrix_sl` case to `OnOrderEvent` in `engine.go`**

In `pkg/strategy/engine.go`, inside `OnOrderEvent`, the `Filled` switch block (around line 733):

```go
// existing cases:
case "level":
    price, _ := strconv.ParseFloat(ev.AvgPrice, 64)
    qty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
    levelID := ref.levelID
    sr.submit(func(ctx context.Context) { sr.handleLevelFill(ctx, levelID, price, qty) })
case "tp":
    fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
    fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
    sr.submit(func(ctx context.Context) { sr.handleTPFill(ctx, fillPrice, fillQty) })
case "sl":
    fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
    fillQty, _ := strconv.ParseFloat(ev.CumExecQty, 64)
    sr.submit(func(ctx context.Context) { sr.handleSLFill(ctx, fillPrice, fillQty) })
// ADD:
case "matrix_sl":
    fillPrice, _ := strconv.ParseFloat(ev.AvgPrice, 64)
    levelID := ref.levelID
    sr.submit(func(ctx context.Context) { sr.handleMatrixSLFill(ctx, levelID, fillPrice) })
```

Also in the `Cancelled` switch block (around line 699), add re-placement for cancelled matrix_sl:

```go
// existing:
case "tp", "sl":
    refType := ref.refType
    sr.submit(func(ctx context.Context) { sr.handleTPSLCancelled(ctx, refType) })
case "level":
    levelID, orderID := ref.levelID, ev.OrderID
    sr.submit(func(ctx context.Context) { sr.handleLevelCancelled(ctx, levelID, orderID) })
// ADD:
case "matrix_sl":
    levelID := ref.levelID
    sr.submit(func(ctx context.Context) { sr.handleMatrixSLCancelled(ctx, levelID) })
```

- [ ] **Step 2: Extend `loadActiveCycle` in `cycle.go` to read new columns**

Replace the `rows.Scan` call (around line 1016) with one that also reads the new fields:

Current query (around line 1003):
```go
rows, err := sr.runner.pool.Query(ctx,
    `SELECT id, level_idx, side, target_price, size_usdt, qty, status,
            COALESCE(exchange_order_id,''), COALESCE(filled_price,0), COALESCE(exchange_link_id,'')
     FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx ASC`,
    c.ID,
)
```

Replace with:
```go
rows, err := sr.runner.pool.Query(ctx,
    `SELECT id, level_idx, side, target_price, size_usdt, qty, status,
            COALESCE(exchange_order_id,''), COALESCE(filled_price,0), COALESCE(exchange_link_id,''),
            COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), slot
     FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx ASC`,
    c.ID,
)
```

Replace the `rows.Scan` call:
```go
// was:
if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
    &l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice, &l.ExchangeLinkID); err != nil {

// becomes:
var slotVal *int16
if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
    &l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice, &l.ExchangeLinkID,
    &l.SLOrderID, &l.SLPrice, &l.SLReplaced, &slotVal); err != nil {
    continue
}
if slotVal != nil {
    s := int(*slotVal)
    l.Slot = &s
}
```

Then, after the `sr.levels = append(sr.levels, l)` block, add matrix_sl registration after the existing `LevelPlaced` registrations:

```go
// After the existing placed-level registration block:
if l.SLOrderID != "" {
    sr.runner.RegisterOrder(l.SLOrderID, orderRef{
        strategyID: sr.strategy.ID,
        levelID:    l.ID,
        refType:    "matrix_sl",
    })
}
```

- [ ] **Step 3: Verify compile**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

Expected: compile error about `handleMatrixSLFill` and `handleMatrixSLCancelled` undefined — that is expected at this stage (they live in matrix.go which doesn't exist yet). Verify the error mentions only those two symbols.

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/engine.go pkg/strategy/cycle.go
git commit -m "feat: route matrix_sl fill/cancel events; load matrix SL columns in loadActiveCycle"
```

---

## Task 4: matrix.go — Helpers + startMatrixCycle

**Files:**
- Create: `pkg/strategy/matrix.go`

- [ ] **Step 1: Write failing test for matrix price calculations**

Add to `pkg/strategy/engine_test.go`:

```go
func TestMatrixLevelPrices_Long(t *testing.T) {
    // L(0) = 100, below[0] step=2%, below[1] step=3%, above[0] step=2%, above[1] step=3%
    aboveLevels := []MatrixLevel{
        {Direction: "above", PriceStepPct: 2.0, SizePct: 25},
        {Direction: "above", PriceStepPct: 3.0, SizePct: 25},
    }
    belowLevels := []MatrixLevel{
        {Direction: "below", PriceStepPct: 2.0, SizePct: 25},
        {Direction: "below", PriceStepPct: 3.0, SizePct: 25},
    }
    startPrice := 100.0
    prices := calculateMatrixPrices(startPrice, aboveLevels, belowLevels)
    // slot 0 = startPrice
    if math.Abs(prices[0]-100.0) > 0.0001 {
        t.Errorf("slot 0: want 100, got %.4f", prices[0])
    }
    // slot -1 = 100 * (1 - 0.02) = 98
    if math.Abs(prices[-1+3]-98.0) > 0.0001 {
        t.Errorf("slot -1: want 98, got %.4f", prices[-1+3])
    }
    // slot -2 = 98 * (1 - 0.03) = 95.06
    if math.Abs(prices[-2+3]-95.06) > 0.001 {
        t.Errorf("slot -2: want 95.06, got %.4f", prices[-2+3])
    }
    // slot +1 = 100 * (1 + 0.02) = 102
    if math.Abs(prices[1+3]-102.0) > 0.0001 {
        t.Errorf("slot +1: want 102, got %.4f", prices[1+3])
    }
    // slot +2 = 102 * (1 + 0.03) = 105.06
    if math.Abs(prices[2+3]-105.06) > 0.001 {
        t.Errorf("slot +2: want 105.06, got %.4f", prices[2+3])
    }
}
```

Note: the test uses a map[int]float64 for prices indexed by slot.

Rewrite using a cleaner helper signature:

```go
func TestCalculateMatrixPrices(t *testing.T) {
    above := []MatrixLevel{
        {Direction: "above", PriceStepPct: 2.0},
        {Direction: "above", PriceStepPct: 3.0},
    }
    below := []MatrixLevel{
        {Direction: "below", PriceStepPct: 2.0},
        {Direction: "below", PriceStepPct: 3.0},
    }
    prices := calculateMatrixPrices(100.0, above, below)
    cases := []struct {
        slot int
        want float64
    }{
        {0, 100.0},
        {-1, 98.0},
        {-2, 95.06},
        {1, 102.0},
        {2, 105.06},
    }
    for _, c := range cases {
        got, ok := prices[c.slot]
        if !ok {
            t.Errorf("slot %d not in result", c.slot)
            continue
        }
        if math.Abs(got-c.want) > 0.001 {
            t.Errorf("slot %d: want %.4f, got %.4f", c.slot, c.want, got)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestCalculateMatrixPrices -v
```

Expected: `undefined: calculateMatrixPrices`.

- [ ] **Step 3: Create `pkg/strategy/matrix.go` with helpers and `startMatrixCycle`**

```go
package strategy

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"sis/pkg/trader"
)

// calculateMatrixPrices returns a map from slot index to target price.
// Slot 0 = startPrice (entry). Positive slots = above, negative = below.
// Each step compounds from the previous level's price.
func calculateMatrixPrices(startPrice float64, above, below []MatrixLevel) map[int]float64 {
	prices := map[int]float64{0: startPrice}
	prev := startPrice
	for i, lvl := range below {
		prev = prev * (1 - lvl.PriceStepPct/100)
		prices[-(i + 1)] = prev
	}
	prev = startPrice
	for i, lvl := range above {
		prev = prev * (1 + lvl.PriceStepPct/100)
		prices[i+1] = prev
	}
	return prices
}

// matrixLevelConfig returns the per-level config values for the given slot.
// slot=0 → MatrixEntryLevel; slot>0 → above[slot-1]; slot<0 → below[-slot-1].
// Returns zero values if the slot is out of range.
func (sr *StrategyRunner) matrixLevelConfig(slot int) (tpPct, stopPct, stopCondPct, stopReplacePct *float64) {
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		if e == nil {
			return
		}
		return e.TPPct, e.StopPct, e.StopCondPct, e.StopReplacePct
	}
	if slot > 0 {
		above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
		if idx := slot - 1; idx < len(above) {
			l := above[idx]
			return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct
		}
		return
	}
	// slot < 0
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	if idx := -slot - 1; idx < len(below) {
		l := below[idx]
		return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct
	}
	return
}

func filterMatrixLevels(levels []MatrixLevel, dir string) []MatrixLevel {
	var out []MatrixLevel
	for _, l := range levels {
		if l.Direction == dir {
			out = append(out, l)
		}
	}
	return out
}

// matrixActiveQty returns the total qty of filled (non-sl_closed) levels.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixActiveQty() float64 {
	total := 0.0
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			qty, _ := strconv.ParseFloat(l.Qty, 64)
			total += qty
		}
	}
	return total
}

// matrixLatestActiveFill returns the most recently inserted filled non-sl_closed level.
// "Most recently inserted" = highest level_idx among filled levels (last in the slice).
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixLatestActiveFill() (*GridLevel, bool) {
	for i := len(sr.levels) - 1; i >= 0; i-- {
		if sr.levels[i].Status == LevelFilled && sr.levels[i].FilledPrice > 0 {
			return &sr.levels[i], true
		}
	}
	return nil, false
}

// matrixLevelSide returns "Buy" for Long strategies, "Sell" for Short.
func matrixLevelSide(dir Direction) string {
	if dir == DirectionShort {
		return "Sell"
	}
	return "Buy"
}

// startMatrixCycle creates a new matrix cycle: fetches price, builds levels from
// MatrixLevels config, inserts into DB, places L(0) as market, places remaining levels.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) startMatrixCycle(ctx context.Context) error {
	// Guard: existing active levels → load instead.
	var existingLevels int
	_ = sr.runner.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategy_levels sl
		 JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		 WHERE sc.strategy_id = $1 AND sc.ended_at IS NULL
		   AND sl.status IN ('pending','placed')`,
		sr.strategy.ID,
	).Scan(&existingLevels)
	if existingLevels > 0 {
		log.Printf("strategy %s: startMatrixCycle skipped — %d active levels in DB", sr.strategy.ID, existingLevels)
		return sr.loadMatrixCycle(ctx)
	}

	sr.closeDustPosition(ctx)
	sr.cancelAllStrategyOrders(ctx)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET ended_at=NOW(), result='orphan'
		 WHERE strategy_id=$1 AND ended_at IS NULL`, sr.strategy.ID)

	lev := sr.strategy.Leverage
	if lev <= 0 {
		lev = 1
	}
	levStr := strconv.Itoa(lev)
	if lerr := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
		Symbol:       sr.strategy.Symbol,
		Category:     sr.strategy.Category,
		BuyLeverage:  levStr,
		SellLeverage: levStr,
	}); lerr != nil && !strings.Contains(lerr.Error(), "110043") {
		log.Printf("strategy %s: matrix set leverage: %v", sr.strategy.ID, lerr)
	}

	if !sr.ensurePositionMode(ctx) {
		sr.mu.Lock()
		sr.stopWithMessage(ctx, "Не удалось переключить position mode — стратегия остановлена")
		sr.mu.Unlock()
		return fmt.Errorf("position mode mismatch")
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status == StatusStopped {
		return nil
	}

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

	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(price, above, below)
	side := matrixLevelSide(sr.strategy.Direction)
	entryLevel := sr.strategy.MatrixEntryLevel

	// Insert L(0) — entry level
	if entryLevel != nil {
		sizeUSDT := entryLevel.SizePct / 100 * sr.strategy.GridSizeUSDT
		qty := trader.FormatQty(sizeUSDT/price, sr.instr.QtyStep, sr.instr.MinQty)
		slot := 0
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, cycleID, 1, side, 0, sizeUSDT, qty, slot,
		).Scan(&levelID); err == nil {
			sr.levels = append(sr.levels, GridLevel{
				ID: levelID, LevelIdx: 1, Side: side,
				TargetPrice: 0, SizeUSDT: sizeUSDT, Qty: qty,
				Status: LevelPending, Slot: &slot,
			})
		}
	}

	levelIdx := 2
	// Insert below levels (slots -1, -2, ...)
	for i, lvl := range below {
		slot := -(i + 1)
		targetPrice := prices[slot]
		sizeUSDT := lvl.SizePct / 100 * sr.strategy.GridSizeUSDT
		qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
		s := slot
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side, targetPrice, sizeUSDT, qty, slot,
		).Scan(&levelID); err == nil {
			sr.levels = append(sr.levels, GridLevel{
				ID: levelID, LevelIdx: levelIdx, Side: side,
				TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
				Status: LevelPending, Slot: &s,
			})
		}
		levelIdx++
	}
	// Insert above levels (slots +1, +2, ...)
	for i, lvl := range above {
		slot := i + 1
		targetPrice := prices[slot]
		sizeUSDT := lvl.SizePct / 100 * sr.strategy.GridSizeUSDT
		qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
		s := slot
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side, targetPrice, sizeUSDT, qty, slot,
		).Scan(&levelID); err == nil {
			sr.levels = append(sr.levels, GridLevel{
				ID: levelID, LevelIdx: levelIdx, Side: side,
				TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
				Status: LevelPending, Slot: &s,
			})
		}
		levelIdx++
	}

	sr.info(ctx, fmt.Sprintf("Matrix цикл %d запущен @ %.4f, уровней: %d",
		sr.cycle.CycleNum, price, len(sr.levels)))
	sr.clearManualAlert(ctx)

	// Place L(0) as market first; remaining levels placed after
	for i := range sr.levels {
		if sr.levels[i].TargetPrice == 0 {
			if err := sr.placeMatrixLevel(ctx, &sr.levels[i], price); err != nil {
				sr.errlog(ctx, fmt.Sprintf("Matrix L(0) place: %v", err))
			}
			break
		}
	}
	// Place non-virtual non-entry levels
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || l.TargetPrice == 0 {
			continue
		}
		if sr.matrixIsVirtual(l) {
			continue // virtual levels are fired by price monitor
		}
		if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Matrix L(%v) place: %v", l.Slot, err))
		}
	}

	// Launch price monitor (outside mu — but we are under mu here, so schedule via goroutine)
	go sr.launchMatrixPriceMonitor()
	return nil
}

// matrixIsVirtual reports whether the given level should be treated as virtual
// (not placed on exchange — fired by price monitor instead).
func (sr *StrategyRunner) matrixIsVirtual(l *GridLevel) bool {
	if l.Slot == nil {
		return false
	}
	slot := *l.Slot
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		return e != nil && e.OrderType == "virtual"
	}
	if slot > 0 {
		above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
		idx := slot - 1
		return idx < len(above) && above[idx].OrderType == "virtual"
	}
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	idx := -slot - 1
	return idx < len(below) && below[idx].OrderType == "virtual"
}
```

Note: `strings.Contains` is already imported in `cycle.go`; in `matrix.go` we need to add `"strings"` to the import.

- [ ] **Step 4: Add `OrderType` field to Go structs**

In `pkg/strategy/types.go`, extend `MatrixLevel` and `MatrixEntryLevel`:

```go
type MatrixLevel struct {
    Direction      string   `json:"direction"`
    PriceStepPct   float64  `json:"price_step_pct"`
    SizePct        float64  `json:"size_pct"`
    StopPct        *float64 `json:"stop_pct,omitempty"`
    StopCondPct    *float64 `json:"stop_cond_pct,omitempty"`
    StopReplacePct *float64 `json:"stop_replace_pct,omitempty"`
    TPPct          *float64 `json:"tp_pct,omitempty"`
    OrderType      string   `json:"order_type,omitempty"` // "exchange" | "virtual"
}

type MatrixEntryLevel struct {
    SizePct        float64  `json:"size_pct"`
    StopPct        *float64 `json:"stop_pct,omitempty"`
    StopCondPct    *float64 `json:"stop_cond_pct,omitempty"`
    StopReplacePct *float64 `json:"stop_replace_pct,omitempty"`
    TPPct          *float64 `json:"tp_pct,omitempty"`
    OrderType      string   `json:"order_type,omitempty"` // "exchange" | "virtual"
}
```

- [ ] **Step 5: Run failing test and fix**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestCalculateMatrixPrices -v
```

Expected: PASS.

- [ ] **Step 6: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

Expected: errors about `handleMatrixSLFill`, `handleMatrixSLCancelled` (not yet written) — that is fine.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/types.go pkg/strategy/engine_test.go
git commit -m "feat: add startMatrixCycle, calculateMatrixPrices, matrixLevelConfig helpers"
```

---

## Task 5: placeMatrixLevel

**Files:**
- Modify: `pkg/strategy/matrix.go`

- [ ] **Step 1: Write failing test for placement decision**

Add to `pkg/strategy/engine_test.go`:

```go
func TestMatrixPlacementDecision(t *testing.T) {
    // Long: target below current → LIMIT
    orderType, trigDir := matrixEntryOrderType("long", 95.0, 100.0)
    if orderType != "Limit" {
        t.Errorf("below-current Long: want Limit, got %s", orderType)
    }
    _ = trigDir

    // Long: target above current → Stop Market
    orderType, trigDir = matrixEntryOrderType("long", 105.0, 100.0)
    if orderType != "StopMarket" {
        t.Errorf("above-current Long: want StopMarket, got %s", orderType)
    }
    if trigDir != 1 {
        t.Errorf("above-current Long: want trigDir=1, got %d", trigDir)
    }

    // Short: target above current → LIMIT
    orderType, trigDir = matrixEntryOrderType("short", 105.0, 100.0)
    if orderType != "Limit" {
        t.Errorf("above-current Short: want Limit, got %s", orderType)
    }

    // Short: target below current → Stop Market
    orderType, trigDir = matrixEntryOrderType("short", 95.0, 100.0)
    if orderType != "StopMarket" {
        t.Errorf("below-current Short: want StopMarket, got %s", orderType)
    }
    if trigDir != 2 {
        t.Errorf("below-current Short: want trigDir=2, got %d", trigDir)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixPlacementDecision -v
```

Expected: `undefined: matrixEntryOrderType`.

- [ ] **Step 3: Add `matrixEntryOrderType` and `placeMatrixLevel` to `matrix.go`**

```go
// matrixEntryOrderType decides how to place a matrix entry level.
// Returns "Limit", "StopMarket", or "Market" (for L(0) with targetPrice=0),
// and the TriggerDirection (1 = rises, 2 = falls; 0 for Limit/Market).
func matrixEntryOrderType(direction string, targetPrice, currentPrice float64) (orderType string, triggerDirection int) {
    if targetPrice == 0 {
        return "Market", 0
    }
    isLong := direction == "long" || direction == string(DirectionLong)
    if isLong {
        if targetPrice < currentPrice {
            return "Limit", 0 // price hasn't dropped yet
        }
        return "StopMarket", 1 // price needs to rise to target
    }
    // Short
    if targetPrice > currentPrice {
        return "Limit", 0 // price hasn't risen yet
    }
    return "StopMarket", 2 // price needs to fall to target
}

// placeMatrixLevel places a single pending matrix level on the exchange.
// Virtual levels are silently skipped (they are fired by the price monitor).
// Safe Zone suppression: levels whose target falls inside the current safe zone are skipped.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeMatrixLevel(ctx context.Context, l *GridLevel, currentPrice float64) error {
    if sr.strategy.Status == StatusStopped {
        return nil
    }
    // Safe Zone suppression
    if sr.matrixSafeZone != nil && sr.matrixSafeZone.Contains(l.TargetPrice) {
        return nil
    }
    // Virtual levels: leave as pending; price monitor fires them.
    if sr.matrixIsVirtual(l) {
        return nil
    }

    linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d",
        sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)

    var req trader.OrderRequest
    if l.TargetPrice == 0 {
        req = trader.OrderRequest{
            Symbol:      sr.strategy.Symbol,
            Category:    sr.strategy.Category,
            Side:        l.Side,
            OrderType:   "Market",
            Qty:         l.Qty,
            PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
            OrderLinkId: linkID,
        }
    } else {
        formattedPrice := trader.FormatPrice(l.TargetPrice, sr.instr.TickSize)
        oType, trigDir := matrixEntryOrderType(string(sr.strategy.Direction), l.TargetPrice, currentPrice)
        if oType == "Limit" {
            req = trader.OrderRequest{
                Symbol:      sr.strategy.Symbol,
                Category:    sr.strategy.Category,
                Side:        l.Side,
                OrderType:   "Limit",
                Qty:         l.Qty,
                Price:       formattedPrice,
                TimeInForce: "GTC",
                PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
                OrderLinkId: linkID,
            }
        } else {
            req = trader.OrderRequest{
                Symbol:           sr.strategy.Symbol,
                Category:         sr.strategy.Category,
                Side:             l.Side,
                OrderType:        "Market",
                Qty:              l.Qty,
                TriggerPrice:     formattedPrice,
                TriggerDirection: trigDir,
                OrderFilter:      "StopOrder",
                PositionIdx:      positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
                OrderLinkId:      linkID,
            }
        }
    }

    ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
    sr.runner.RegisterOrder(linkID, ref)

    result, err := sr.runner.tradeStream.PlaceOrder(ctx, req)
    if err != nil {
        sr.runner.UnregisterOrder(linkID)
        if isDuplicateLinkId(err) {
            // Find level index and recover
            for i := range sr.levels {
                if sr.levels[i].ID == l.ID {
                    sr.recoverDuplicateLevel(ctx, i, linkID)
                    return nil
                }
            }
        }
        if isInsufficientBalance(err) {
            sr.stopWithMessage(ctx, "Недостаточно баланса для выставления ордера — стратегия остановлена")
            return err
        }
        sr.errlog(ctx, fmt.Sprintf("Matrix placeLevel L%d: %v", l.LevelIdx, err))
        return err
    }

    roundedPrice := l.TargetPrice
    if l.TargetPrice > 0 {
        roundedPrice, _ = strconv.ParseFloat(trader.FormatPrice(l.TargetPrice, sr.instr.TickSize), 64)
        l.TargetPrice = roundedPrice
    }
    if _, err := sr.runner.pool.Exec(ctx,
        `UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW(),
         target_price=CASE WHEN $4::numeric > 0 THEN $4::numeric ELSE target_price END WHERE id=$3`,
        result.OrderId, linkID, l.ID, roundedPrice,
    ); err != nil {
        log.Printf("strategy %s: matrix level DB update: %v", sr.strategy.ID, err)
    }
    l.Status = LevelPlaced
    l.ExchangeOrderID = result.OrderId
    l.ExchangeLinkID = linkID
    sr.runner.RegisterOrder(result.OrderId, ref)

    return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixPlacementDecision -v
```

Expected: PASS.

- [ ] **Step 5: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
git commit -m "feat: add placeMatrixLevel with limit/stop-market/virtual routing"
```

---

## Task 6: handleMatrixLevelFill + matrixUpdateTP

**Files:**
- Modify: `pkg/strategy/matrix.go`
- Modify: `pkg/strategy/cycle.go` (`handleLevelFill` branch)

- [ ] **Step 1: Write failing test for per-level SL trigger price**

Add to `pkg/strategy/engine_test.go`:

```go
func TestMatrixPerLevelSLTrigger(t *testing.T) {
    // Long: stop_pct = -2.0, fill_price = 100 → trigger = 100 * (1 - 0.02) = 98
    stopPct := -2.0
    trigger := matrixSLTrigger(DirectionLong, 100.0, stopPct)
    if math.Abs(trigger-98.0) > 0.0001 {
        t.Errorf("Long SL trigger: want 98, got %.4f", trigger)
    }
    // Short: stop_pct = -2.0, fill_price = 100 → trigger = 100 * (1 + 0.02) = 102
    trigger = matrixSLTrigger(DirectionShort, 100.0, stopPct)
    if math.Abs(trigger-102.0) > 0.0001 {
        t.Errorf("Short SL trigger: want 102, got %.4f", trigger)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixPerLevelSLTrigger -v
```

Expected: `undefined: matrixSLTrigger`.

- [ ] **Step 3: Add `matrixSLTrigger`, `handleMatrixLevelFill`, and `matrixUpdateTP` to `matrix.go`**

```go
// matrixSLTrigger computes the per-level SL trigger price.
// stopPct is expected negative (e.g. -2.0 means 2% adverse from fill).
func matrixSLTrigger(dir Direction, fillPrice, stopPct float64) float64 {
    if dir == DirectionLong {
        return fillPrice * (1 + stopPct/100) // stopPct < 0 → trigger below fill
    }
    return fillPrice * (1 - stopPct/100) // stopPct < 0 → trigger above fill
}

// handleMatrixLevelFill is the matrix-specific handler called by handleLevelFill
// when strategy_type == "dca".
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixLevelFill(ctx context.Context, levelID string, filledPrice float64) {
    // Idempotency
    for _, l := range sr.levels {
        if l.ID == levelID && l.Status == LevelFilled {
            return
        }
    }

    sr.runner.pool.Exec(ctx, //nolint:errcheck
        `UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
        filledPrice, levelID,
    )
    var filled *GridLevel
    for i := range sr.levels {
        if sr.levels[i].ID == levelID {
            sr.runner.UnregisterOrder(sr.levels[i].ExchangeOrderID)
            sr.levels[i].Status = LevelFilled
            sr.levels[i].FilledPrice = filledPrice
            filled = &sr.levels[i]
            sr.info(ctx, fmt.Sprintf("Matrix L%d (slot %v) %s @ %.4f исполнен",
                sr.levels[i].LevelIdx, sr.levels[i].Slot, sr.levels[i].Side, filledPrice))
            break
        }
    }
    if filled == nil {
        return
    }

    // Place per-level SL if configured
    if filled.Slot != nil {
        _, stopPct, _, _ := sr.matrixLevelConfig(*filled.Slot)
        if stopPct != nil {
            sr.matrixPlacePerLevelSL(ctx, filled, filledPrice, *stopPct)
        }
    }

    // Update global TP
    sr.matrixUpdateTP(ctx)
}

// matrixPlacePerLevelSL places a reduce-only stop-market SL for one filled level.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlacePerLevelSL(ctx context.Context, l *GridLevel, fillPrice, stopPct float64) {
    trigger := matrixSLTrigger(sr.strategy.Direction, fillPrice, stopPct)

    var slSide string
    var trigDir int
    if sr.strategy.Direction == DirectionLong {
        slSide, trigDir = "Sell", 2 // fires when price falls to trigger
    } else {
        slSide, trigDir = "Buy", 1 // fires when price rises to trigger
    }

    sr.matrixSLSeq++
    linkID := fmt.Sprintf("SIS_STR-%s-msl-%s-%d",
        sr.strategy.ID[:8], l.ID[:8], sr.matrixSLSeq)
    qty := trader.FormatQty(func() float64 { q, _ := strconv.ParseFloat(l.Qty, 64); return q }(),
        sr.instr.QtyStep, sr.instr.MinQty)

    ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
    sr.runner.RegisterOrder(linkID, ref)

    result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
        Symbol:           sr.strategy.Symbol,
        Category:         sr.strategy.Category,
        Side:             slSide,
        OrderType:        "Market",
        Qty:              qty,
        TriggerPrice:     trader.FormatPrice(trigger, sr.instr.TickSize),
        TriggerBy:        "LastPrice",
        TriggerDirection: trigDir,
        OrderFilter:      "StopOrder",
        ReduceOnly:       !sr.strategy.HedgeMode,
        PositionIdx:      positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
        OrderLinkId:      linkID,
    })
    if err != nil {
        sr.runner.UnregisterOrder(linkID)
        sr.errlog(ctx, fmt.Sprintf("Matrix per-level SL для L%d: %v", l.LevelIdx, err))
        return
    }

    l.SLOrderID = result.OrderId
    l.SLPrice = trigger
    sr.runner.RegisterOrder(result.OrderId, ref)
    sr.runner.pool.Exec(ctx, //nolint:errcheck
        `UPDATE strategy_levels SET sl_order_id=$1, sl_price=$2 WHERE id=$3`,
        result.OrderId, trigger, l.ID,
    )
    sr.info(ctx, fmt.Sprintf("Matrix SL для L%d @ %.4f выставлен", l.LevelIdx, trigger))
}

// matrixUpdateTP cancels the existing global TP and places a new one based on the
// latest active (non-sl_closed) filled level's fill_price and tp_pct.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixUpdateTP(ctx context.Context) {
    if sr.instr.QtyStep == 0 {
        return
    }
    latest, ok := sr.matrixLatestActiveFill()
    if !ok {
        // No active fills → cancel TP
        if sr.tpOrderID != "" {
            sr.runner.UnregisterOrder(sr.tpOrderID)
            sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
                Symbol:   sr.strategy.Symbol,
                Category: sr.strategy.Category,
                OrderId:  sr.tpOrderID,
            })
            sr.tpOrderID = ""
            sr.runner.pool.Exec(ctx, //nolint:errcheck
                `UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)
        }
        return
    }

    var tpPctVal *float64
    if latest.Slot != nil {
        tpPctVal, _, _, _ = sr.matrixLevelConfig(*latest.Slot)
    }
    if tpPctVal == nil {
        return // this level has no TP configured
    }

    var tpPrice float64
    var tpSide string
    if sr.strategy.Direction == DirectionLong {
        tpSide = "Sell"
        tpPrice = latest.FilledPrice * (1 + *tpPctVal/100)
    } else {
        tpSide = "Buy"
        tpPrice = latest.FilledPrice * (1 - *tpPctVal/100)
    }

    activeQty := sr.matrixActiveQty()
    if activeQty == 0 {
        return
    }
    tpQty := trader.FormatQty(activeQty, sr.instr.QtyStep, sr.instr.MinQty)

    // Cancel existing TP
    if sr.tpOrderID != "" {
        old := sr.tpOrderID
        sr.runner.UnregisterOrder(old)
        sr.tpOrderID = ""
        if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
            Symbol:   sr.strategy.Symbol,
            Category: sr.strategy.Category,
            OrderId:  old,
        }); err != nil && !isOrderGone(err) {
            sr.warn(ctx, fmt.Sprintf("matrixUpdateTP: cancel old TP: %v", err))
        }
    }

    sr.tpPlaceSeq++
    linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, sr.tpPlaceSeq)
    result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
        Symbol:      sr.strategy.Symbol,
        Category:    sr.strategy.Category,
        Side:        tpSide,
        OrderType:   "Limit",
        Qty:         tpQty,
        Price:       trader.FormatPrice(tpPrice, sr.instr.TickSize),
        TimeInForce: "GTC",
        ReduceOnly:  !sr.strategy.HedgeMode,
        PositionIdx: positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
        OrderLinkId: linkID,
    })
    if err != nil {
        sr.errlog(ctx, fmt.Sprintf("matrixUpdateTP: place TP: %v", err))
        return
    }
    sr.tpOrderID = result.OrderId
    sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
    sr.runner.pool.Exec(ctx, //nolint:errcheck
        `UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
    sr.info(ctx, fmt.Sprintf("Matrix TP @ %.4f qty=%s выставлен", tpPrice, tpQty))
}
```

- [ ] **Step 4: Branch `handleLevelFill` in `cycle.go`**

In `pkg/strategy/cycle.go`, inside `handleLevelFill` (around line 1604), at the top of the function after the idempotency check, add:

```go
// Dispatch to matrix handler for DCA strategies.
if sr.strategy.StrategyType == "dca" {
    sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
    return
}
```

This goes right after the idempotency loop:
```go
// Idempotency: market orders may trigger this via both linkID and orderId.
for _, l := range sr.levels {
    if l.ID == levelID && l.Status == LevelFilled {
        return
    }
}
// ADD HERE:
if sr.strategy.StrategyType == "dca" {
    sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
    return
}
```

- [ ] **Step 5: Run test to verify SL trigger math**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixPerLevelSLTrigger -v
```

Expected: PASS.

- [ ] **Step 6: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/cycle.go pkg/strategy/engine_test.go
git commit -m "feat: handleMatrixLevelFill — per-level SL placement and global TP update"
```

---

## Task 7: handleMatrixSLFill + handleMatrixSLCancelled + closeCycle/handleStopRequest cleanup

**Files:**
- Modify: `pkg/strategy/matrix.go`
- Modify: `pkg/strategy/cycle.go`

- [ ] **Step 1: Write failing test for safe zone creation**

Add to `pkg/strategy/engine_test.go`:

```go
func TestMatrixSafeZoneCreation(t *testing.T) {
    // safe_zone_pct=5, sl_trigger=100 → Low=95, High=105
    zone := createMatrixSafeZone(100.0, 5.0)
    if math.Abs(zone.Low-95.0) > 0.0001 {
        t.Errorf("Low: want 95, got %.4f", zone.Low)
    }
    if math.Abs(zone.High-105.0) > 0.0001 {
        t.Errorf("High: want 105, got %.4f", zone.High)
    }
    if !zone.Contains(100.0) {
        t.Error("100 should be inside zone")
    }
    if zone.Contains(90.0) {
        t.Error("90 should be outside zone")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixSafeZoneCreation -v
```

Expected: `undefined: createMatrixSafeZone`.

- [ ] **Step 3: Add `createMatrixSafeZone`, `handleMatrixSLFill`, `handleMatrixSLCancelled`, and `matrixCancelPerLevelSLs` to `matrix.go`**

```go
// createMatrixSafeZone builds a safe zone centered on slTrigger with ±safeZonePct.
func createMatrixSafeZone(slTrigger, safeZonePct float64) *MatrixSafeZone {
    return &MatrixSafeZone{
        Low:       slTrigger * (1 - safeZonePct/100),
        High:      slTrigger * (1 + safeZonePct/100),
        CreatedAt: time.Now(),
    }
}

// handleMatrixSLFill is called when a per-level SL fires (refType="matrix_sl").
// Must NOT be called with sr.mu held (submitted to worker via sr.submit).
func (sr *StrategyRunner) handleMatrixSLFill(ctx context.Context, levelID string, filledPrice float64) {
    sr.mu.Lock()
    defer sr.mu.Unlock()

    if sr.cycle == nil {
        return
    }

    // Find and mark the level sl_closed
    var closed *GridLevel
    for i := range sr.levels {
        if sr.levels[i].ID == levelID {
            closed = &sr.levels[i]
            break
        }
    }
    if closed == nil {
        return
    }

    slTrigger := closed.SLPrice
    if slTrigger == 0 {
        slTrigger = filledPrice
    }

    sr.runner.pool.Exec(ctx, //nolint:errcheck
        `UPDATE strategy_levels SET status='sl_closed' WHERE id=$1`, levelID,
    )
    closed.Status = LevelSLClosed
    closed.SLOrderID = ""
    sr.warn(ctx, fmt.Sprintf("Matrix SL сработал L%d (slot %v) @ %.4f",
        closed.LevelIdx, closed.Slot, slTrigger))

    // Create Safe Zone
    if sr.strategy.SafeZonePct > 0 {
        sr.matrixSafeZone = createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct)
        sr.info(ctx, fmt.Sprintf("Safe Zone создана: [%.4f, %.4f]",
            sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
    }

    // Recalculate global TP
    sr.matrixUpdateTP(ctx)

    // If no active filled levels remain → close cycle
    if sr.matrixActiveQty() == 0 {
        sr.info(ctx, "Все уровни закрыты по SL — цикл завершается")
        sr.closedBySelf = true
        sr.closeCycle(ctx, "sl")
        sr.maybeRestart(ctx)
    }
}

// handleMatrixSLCancelled re-places the per-level SL when it is externally cancelled.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixSLCancelled(ctx context.Context, levelID string) {
    sr.mu.Lock()
    defer sr.mu.Unlock()

    for i := range sr.levels {
        l := &sr.levels[i]
        if l.ID == levelID && l.Status == LevelFilled && l.FilledPrice > 0 {
            if l.Slot == nil {
                return
            }
            _, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
            if stopPct == nil {
                return
            }
            l.SLOrderID = ""
            sr.warn(ctx, fmt.Sprintf("Matrix SL для L%d отменён биржей — переставляю", l.LevelIdx))
            sr.matrixPlacePerLevelSL(ctx, l, l.FilledPrice, *stopPct)
            return
        }
    }
}

// matrixCancelPerLevelSLs cancels all active per-level SL orders.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixCancelPerLevelSLs(ctx context.Context) {
    for i := range sr.levels {
        l := &sr.levels[i]
        if l.SLOrderID == "" {
            continue
        }
        orderID := l.SLOrderID
        sr.runner.UnregisterOrder(orderID)
        l.SLOrderID = ""
        if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
            Symbol:      sr.strategy.Symbol,
            Category:    sr.strategy.Category,
            OrderId:     orderID,
            OrderFilter: "StopOrder",
        }); err != nil && !isOrderGone(err) {
            sr.warn(ctx, fmt.Sprintf("matrixCancelPerLevelSLs: %s: %v", orderID, err))
        }
    }
}
```

- [ ] **Step 4: Run test to verify safe zone math**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixSafeZoneCreation -v
```

Expected: PASS.

- [ ] **Step 5: Extend `closeCycle` in `cycle.go` to cancel matrix per-level SLs**

In `pkg/strategy/cycle.go`, inside `closeCycle` (around line 2010), after cancelling the global SL and before clearing `sr.cycle = nil`, add:

```go
// Cancel per-level matrix SL orders (noop for grid strategies)
if sr.strategy.StrategyType == "dca" {
    sr.matrixCancelPerLevelSLs(ctx)
    if sr.matrixMonitorStop != nil {
        sr.matrixMonitorStop()
        sr.matrixMonitorStop = nil
    }
    sr.matrixSafeZone = nil
}
```

- [ ] **Step 6: Extend `handleStopRequest` in `cycle.go` to cancel matrix per-level SLs**

In `pkg/strategy/cycle.go`, inside `handleStopRequest` (around line 2713), after the signal monitor cancellation block and before the existing SL cancel, add:

```go
if sr.strategy.StrategyType == "dca" {
    sr.matrixCancelPerLevelSLs(ctx)
    if sr.matrixMonitorStop != nil {
        sr.matrixMonitorStop()
        sr.matrixMonitorStop = nil
    }
    sr.matrixSafeZone = nil
}
```

- [ ] **Step 7: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

Expected: no errors (all matrix functions are now defined).

- [ ] **Step 8: Run all strategy tests**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -v
```

Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/cycle.go pkg/strategy/engine_test.go
git commit -m "feat: handleMatrixSLFill — sl_closed status, safe zone, cycle end; extend closeCycle/handleStopRequest"
```

---

## Task 8: launchMatrixPriceMonitor + matrixPriceTick

**Files:**
- Modify: `pkg/strategy/matrix.go`

- [ ] **Step 1: Write failing test for stop condition threshold**

Add to `pkg/strategy/engine_test.go`:

```go
func TestMatrixStopCondThreshold(t *testing.T) {
    // Long: stop_cond_pct=3.0 (move up 3% from fill is the trigger)
    threshold := matrixStopCondThreshold(DirectionLong, 100.0, 3.0)
    if math.Abs(threshold-103.0) > 0.0001 {
        t.Errorf("Long threshold: want 103, got %.4f", threshold)
    }
    // Short: stop_cond_pct=3.0 (move down 3% from fill is the trigger)
    threshold = matrixStopCondThreshold(DirectionShort, 100.0, 3.0)
    if math.Abs(threshold-97.0) > 0.0001 {
        t.Errorf("Short threshold: want 97, got %.4f", threshold)
    }
}

func TestMatrixStopReplaceNewTrigger(t *testing.T) {
    // Long: stop_replace_pct=0.5 → new SL above fill (breakeven+)
    trigger := matrixStopReplaceTrigger(DirectionLong, 100.0, 0.5)
    if math.Abs(trigger-100.5) > 0.0001 {
        t.Errorf("Long replace trigger: want 100.5, got %.4f", trigger)
    }
    // Short: stop_replace_pct=0.5 → new SL below fill
    trigger = matrixStopReplaceTrigger(DirectionShort, 100.0, 0.5)
    if math.Abs(trigger-99.5) > 0.0001 {
        t.Errorf("Short replace trigger: want 99.5, got %.4f", trigger)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run "TestMatrixStopCond|TestMatrixStopReplace" -v
```

Expected: undefined errors.

- [ ] **Step 3: Add price monitor + helpers to `matrix.go`**

```go
// matrixStopCondThreshold computes the price level that must be crossed to
// trigger a stop-condition SL replacement. condPct is always positive (absolute value).
func matrixStopCondThreshold(dir Direction, fillPrice, condPct float64) float64 {
    if dir == DirectionLong {
        return fillPrice * (1 + condPct/100)
    }
    return fillPrice * (1 - condPct/100)
}

// matrixStopReplaceTrigger computes the new SL trigger price after stop-condition fires.
func matrixStopReplaceTrigger(dir Direction, fillPrice, replacePct float64) float64 {
    if dir == DirectionLong {
        return fillPrice * (1 + replacePct/100)
    }
    return fillPrice * (1 - replacePct/100)
}

// launchMatrixPriceMonitor starts a goroutine that polls mark price every 5 seconds.
// It replaces any existing monitor. Must be called WITHOUT sr.mu held.
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

// matrixPriceTick is called on each 5-second price poll.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPriceTick(ctx context.Context, currentPrice float64) {
    if sr.cycle == nil {
        return
    }

    // 1. Check Safe Zone exit
    if sr.matrixSafeZone != nil && !sr.matrixSafeZone.Contains(currentPrice) {
        sr.info(ctx, fmt.Sprintf("Safe Zone очищена: цена %.4f вышла из [%.4f, %.4f]",
            currentPrice, sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
        sr.matrixSafeZone = nil
        // Trigger re-placement for sl_closed slots
        sr.matrixReplaceSlots(ctx, currentPrice)
    }

    // 2. Trigger virtual levels whose target price has been crossed
    for i := range sr.levels {
        l := &sr.levels[i]
        if l.Status != LevelPending || l.ExchangeOrderID != "" {
            continue
        }
        if !sr.matrixIsVirtual(l) {
            continue
        }
        crossed := false
        if sr.strategy.Direction == DirectionLong {
            // Long virtual above: fires when price rises to target
            // Long virtual below: fires when price falls to target (but below are LIMIT, not virtual in V1)
            crossed = currentPrice >= l.TargetPrice
        } else {
            crossed = currentPrice <= l.TargetPrice
        }
        if crossed {
            sr.matrixTriggerVirtualLevel(ctx, l)
        }
    }

    // 3. Check stop conditions for filled levels
    for i := range sr.levels {
        l := &sr.levels[i]
        if l.Status != LevelFilled || l.SLReplaced || l.Slot == nil {
            continue
        }
        _, _, stopCondPct, stopReplacePct := sr.matrixLevelConfig(*l.Slot)
        if stopCondPct == nil || stopReplacePct == nil {
            continue
        }
        condPct := math.Abs(*stopCondPct)
        threshold := matrixStopCondThreshold(sr.strategy.Direction, l.FilledPrice, condPct)
        condMet := false
        if sr.strategy.Direction == DirectionLong {
            condMet = currentPrice >= threshold
        } else {
            condMet = currentPrice <= threshold
        }
        if !condMet {
            continue
        }
        // Replace SL
        newTrigger := matrixStopReplaceTrigger(sr.strategy.Direction, l.FilledPrice, *stopReplacePct)
        if l.SLOrderID != "" {
            old := l.SLOrderID
            sr.runner.UnregisterOrder(old)
            l.SLOrderID = ""
            sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
                Symbol:      sr.strategy.Symbol,
                Category:    sr.strategy.Category,
                OrderId:     old,
                OrderFilter: "StopOrder",
            })
        }

        var slSide string
        var trigDir int
        if sr.strategy.Direction == DirectionLong {
            slSide, trigDir = "Sell", 2
        } else {
            slSide, trigDir = "Buy", 1
        }
        sr.matrixSLSeq++
        linkID := fmt.Sprintf("SIS_STR-%s-msl-%s-%d",
            sr.strategy.ID[:8], l.ID[:8], sr.matrixSLSeq)
        qty := trader.FormatQty(func() float64 { q, _ := strconv.ParseFloat(l.Qty, 64); return q }(),
            sr.instr.QtyStep, sr.instr.MinQty)
        ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
        sr.runner.RegisterOrder(linkID, ref)

        result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
            Symbol:           sr.strategy.Symbol,
            Category:         sr.strategy.Category,
            Side:             slSide,
            OrderType:        "Market",
            Qty:              qty,
            TriggerPrice:     trader.FormatPrice(newTrigger, sr.instr.TickSize),
            TriggerBy:        "LastPrice",
            TriggerDirection: trigDir,
            OrderFilter:      "StopOrder",
            ReduceOnly:       !sr.strategy.HedgeMode,
            PositionIdx:      positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
            OrderLinkId:      linkID,
        })
        if err != nil {
            sr.runner.UnregisterOrder(linkID)
            sr.errlog(ctx, fmt.Sprintf("Matrix stop-cond SL replace L%d: %v", l.LevelIdx, err))
            continue
        }
        l.SLOrderID = result.OrderId
        l.SLPrice = newTrigger
        l.SLReplaced = true
        sr.runner.RegisterOrder(result.OrderId, ref)
        sr.runner.pool.Exec(ctx, //nolint:errcheck
            `UPDATE strategy_levels SET sl_order_id=$1, sl_price=$2, sl_replaced=true WHERE id=$3`,
            result.OrderId, newTrigger, l.ID,
        )
        sr.info(ctx, fmt.Sprintf("Matrix SL L%d переставлен → %.4f (stop cond сработал)", l.LevelIdx, newTrigger))
    }
}

// matrixTriggerVirtualLevel fires a virtual level by placing a market order.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixTriggerVirtualLevel(ctx context.Context, l *GridLevel) {
    linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-v%d",
        sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
    ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
    sr.runner.RegisterOrder(linkID, ref)

    result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
        Symbol:      sr.strategy.Symbol,
        Category:    sr.strategy.Category,
        Side:        l.Side,
        OrderType:   "Market",
        Qty:         l.Qty,
        PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
        OrderLinkId: linkID,
    })
    if err != nil {
        sr.runner.UnregisterOrder(linkID)
        sr.errlog(ctx, fmt.Sprintf("Matrix virtual L%d: %v", l.LevelIdx, err))
        return
    }
    l.Status = LevelPlaced
    l.ExchangeOrderID = result.OrderId
    l.ExchangeLinkID = linkID
    sr.runner.RegisterOrder(result.OrderId, ref)
    sr.info(ctx, fmt.Sprintf("Matrix virtual L%d (slot %v) запущен @ market", l.LevelIdx, l.Slot))
}

// matrixReplaceSlots re-places levels for any slot that has no active (pending/placed/filled) row.
// Called after safe zone clears. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReplaceSlots(ctx context.Context, currentPrice float64) {
    above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
    below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
    prices := calculateMatrixPrices(sr.cycle.StartPrice, above, below)

    // Collect all slots in the config
    slots := []int{0}
    for i := range above {
        slots = append(slots, i+1)
    }
    for i := range below {
        slots = append(slots, -(i + 1))
    }

    // Find which slots have no active level
    activeSlots := map[int]bool{}
    for _, l := range sr.levels {
        if l.Slot == nil {
            continue
        }
        switch l.Status {
        case LevelPending, LevelPlaced, LevelFilled:
            activeSlots[*l.Slot] = true
        }
    }

    side := matrixLevelSide(sr.strategy.Direction)
    levelIdx := len(sr.levels) + 1

    for _, slot := range slots {
        if activeSlots[slot] {
            continue
        }
        targetPrice, ok := prices[slot]
        if !ok {
            continue
        }
        var sizeUSDT float64
        if slot == 0 {
            if sr.strategy.MatrixEntryLevel == nil {
                continue
            }
            sizeUSDT = sr.strategy.MatrixEntryLevel.SizePct / 100 * sr.strategy.GridSizeUSDT
        } else if slot > 0 {
            idx := slot - 1
            if idx >= len(above) {
                continue
            }
            sizeUSDT = above[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
        } else {
            idx := -slot - 1
            if idx >= len(below) {
                continue
            }
            sizeUSDT = below[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
        }
        priceForQty := targetPrice
        if priceForQty == 0 {
            priceForQty = currentPrice
        }
        qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
        s := slot
        var levelID string
        if err := sr.runner.pool.QueryRow(ctx,
            `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
            sr.strategy.ID, sr.cycle.ID, levelIdx, side, targetPrice, sizeUSDT, qty, slot,
        ).Scan(&levelID); err != nil {
            log.Printf("strategy %s: matrix re-entry slot %d insert: %v", sr.strategy.ID, slot, err)
            continue
        }
        newLevel := GridLevel{
            ID: levelID, LevelIdx: levelIdx, Side: side,
            TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
            Status: LevelPending, Slot: &s,
        }
        sr.levels = append(sr.levels, newLevel)
        if err := sr.placeMatrixLevel(ctx, &sr.levels[len(sr.levels)-1], currentPrice); err != nil {
            sr.errlog(ctx, fmt.Sprintf("Matrix re-entry slot %d: %v", slot, err))
        } else {
            sr.info(ctx, fmt.Sprintf("Matrix re-entry slot %d @ %.4f (после Safe Zone)", slot, targetPrice))
        }
        levelIdx++
    }
}
```

Note: `math.Abs` requires `"math"` import in `matrix.go`.

- [ ] **Step 4: Run stop condition tests**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run "TestMatrixStopCond|TestMatrixStopReplace" -v
```

Expected: PASS.

- [ ] **Step 5: Run all strategy tests**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -v
```

Expected: all PASS.

- [ ] **Step 6: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
git commit -m "feat: launchMatrixPriceMonitor — safe zone exit, virtual triggers, stop conditions, slot re-entry"
```

---

## Task 9: loadMatrixCycle + loadOrStart Branch + restoreMatrixSafeZone

**Files:**
- Modify: `pkg/strategy/matrix.go`
- Modify: `pkg/strategy/cycle.go` (`loadOrStart`)

- [ ] **Step 1: Add `loadMatrixCycle` and `restoreMatrixSafeZone` to `matrix.go`**

```go
// loadMatrixCycle resumes an active matrix cycle after service restart.
// Calls loadActiveCycle to read levels (which now includes SL columns),
// registers per-level SL orders, restores safe zone, and launches the monitor.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
    if err := sr.loadActiveCycle(ctx); err != nil {
        return err
    }
    // Register per-level SL IDs (loadActiveCycle already reads them into GridLevel.SLOrderID)
    // and record lastTPSL dedup values for matrix (activeQty-based, not avgEntry-based)
    sr.mu.Lock()
    sr.mu.Unlock()

    sr.restoreMatrixSafeZone(ctx)
    sr.launchMatrixPriceMonitor()
    return nil
}

// restoreMatrixSafeZone checks the last sl_closed level in the current cycle
// and rebuilds the safe zone if it is recent (< 1 hour) and price is still inside.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreMatrixSafeZone(ctx context.Context) {
    sr.mu.Lock()
    if sr.cycle == nil || sr.strategy.SafeZonePct <= 0 {
        sr.mu.Unlock()
        return
    }
    cycleID := sr.cycle.ID
    sr.mu.Unlock()

    var slPrice float64
    var closedAt time.Time
    err := sr.runner.pool.QueryRow(ctx,
        `SELECT COALESCE(sl_price,0), COALESCE(filled_at, placed_at, NOW())
         FROM strategy_levels
         WHERE cycle_id=$1 AND status='sl_closed' AND sl_price IS NOT NULL
         ORDER BY filled_at DESC NULLS LAST LIMIT 1`,
        cycleID,
    ).Scan(&slPrice, &closedAt)
    if err != nil || slPrice == 0 {
        return
    }
    if time.Since(closedAt) > time.Hour {
        return
    }

    price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
    if err != nil {
        return
    }

    sr.mu.Lock()
    defer sr.mu.Unlock()
    zone := createMatrixSafeZone(slPrice, sr.strategy.SafeZonePct)
    if zone.Contains(price) {
        sr.matrixSafeZone = zone
        log.Printf("strategy %s: restored Safe Zone [%.4f, %.4f]", sr.strategy.ID, zone.Low, zone.High)
    }
}
```

- [ ] **Step 2: Add `strategy_type == "dca"` branch to `loadOrStart` in `cycle.go`**

In `pkg/strategy/cycle.go`, inside `loadOrStart` (around line 158), after `loadErr := sr.loadActiveCycle(ctx)` and its handling, locate the successful load path. Replace the section that falls through to `placeNextLevels` with a strategy-type branch.

Find the block after the `loadActiveCycle` error handling (around line 186, after `return`):
```go
// Settings were changed while the strategy was stopped — apply them now.
sr.resetCancelledLevels(ctx)
```

Before this block, add:
```go
// Matrix strategies use their own load path for extra registration + monitor.
if sr.strategy.StrategyType == "dca" {
    // loadActiveCycle already ran and succeeded; loadMatrixCycle wraps it
    // but we can't call it again without duplicating. Just do the extra steps.
    sr.restoreMatrixSafeZone(ctx)
    sr.launchMatrixPriceMonitor()
    // Skip the grid-specific path below (no placeNextLevels / gridChangedWhileStopped check)
    return
}
```

Also in `loadOrStart`, replace the `startCycle` call (around line 179) with a strategy-type branch:

```go
// was:
sr.startMu.Lock()
err2 := sr.startCycle(ctx)
sr.startMu.Unlock()

// becomes:
sr.startMu.Lock()
var err2 error
if sr.strategy.StrategyType == "dca" {
    err2 = sr.startMatrixCycle(ctx)
} else {
    err2 = sr.startCycle(ctx)
}
sr.startMu.Unlock()
```

- [ ] **Step 3: Compile check**

```
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```

Expected: no errors.

- [ ] **Step 4: Run all strategy tests**

```
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/cycle.go
git commit -m "feat: loadMatrixCycle + loadOrStart branch — matrix engine fully wired"
```

---

## Task 10: Final Integration Verification

**Files:** none changed — this task is smoke-test only.

- [ ] **Step 1: Build the full service**

```
cd C:\Users\123\Projects\sis && go build ./...
```

Expected: no errors.

- [ ] **Step 2: Run all tests**

```
cd C:\Users\123\Projects\sis && go test ./... 2>&1
```

Expected: all PASS (or only pre-existing failures unrelated to matrix).

- [ ] **Step 3: Smoke test — start a matrix strategy via API**

Manual test steps:
1. Create a strategy with `strategy_type = "dca"`, valid `matrix_levels`, `safe_zone_pct`, `matrix_entry_level` via the UI or REST.
2. Set it to `status = 'active'`.
3. Check service logs: should see `Matrix цикл N запущен @ PRICE, уровней: K`.
4. Confirm `strategy_levels` rows exist with correct `slot` values (-2…+2).
5. Confirm L(0) (slot=0) is placed as market and fills immediately.
6. Confirm below-entry levels show `status='placed'` as Limit orders on the exchange.
7. Confirm above-entry levels show `status='placed'` as Stop Market orders on the exchange.
8. Verify per-level SL is placed for each filled level (check `sl_order_id` in `strategy_levels`).
9. Verify global TP order appears after first fill.
10. Kill and restart the service; verify the strategy resumes without duplicate orders.

- [ ] **Step 4: Commit (if any last-minute fixes were needed)**

```bash
git add -A
git commit -m "fix: matrix engine integration fixes from smoke test"
```

---

## Self-Review Checklist

### Spec Coverage

| Spec section | Task covering it |
|---|---|
| §3 DB migration (040_matrix_sl.sql) | Task 1 |
| §3.1 New types (MatrixSafeZone, GridLevel extensions) | Task 2 |
| §3.2 StrategyRunner fields | Task 2 step 5 |
| §4.1 startMatrixCycle | Task 4 |
| §4.2 placeMatrixLevel (Limit / Stop Market / Virtual) | Task 5 |
| §4.3 loadMatrixCycle | Task 9 |
| §5.1 handleMatrixLevelFill + per-level SL | Task 6 |
| §5.1 matrixUpdateTP (from fill_price × tp_pct) | Task 6 |
| §5.2 handleMatrixSLFill + sl_closed + safe zone | Task 7 |
| §5.3 stop condition monitoring | Task 8 |
| §6 Safe Zone suppression in placeMatrixLevel | Task 5 |
| §6 Safe Zone exit in price monitor | Task 8 |
| §6 Slot re-entry (matrixReplaceSlots) | Task 8 |
| §7 launchMatrixPriceMonitor (5s poll) | Task 8 |
| §8 TP fires → cancel per-level SLs | Task 7 (closeCycle) |
| §8 All sl_closed, qty=0 → close cycle | Task 7 (handleMatrixSLFill) |
| §8 Manual stop → cancel per-level SLs | Task 7 (handleStopRequest) |
| §9 Reconcile on restart | Task 3 (loadActiveCycle + matrix_sl registration) |
| engine.go routing for matrix_sl | Task 3 |
