# Matrix Retroactive Stop-Cond SL Placement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Place stop-loss orders retroactively at startup for filled matrix levels whose `stop_cond_pct` threshold was already crossed while the server was down.

**Architecture:** Extract the stop-condition SL placement loop (currently inline in `matrixPriceTick` section 3) into a standalone method `matrixApplyStopCondSLs`. Call it from both `matrixPriceTick` (replacing the inline code) and from `loadMatrixCycle` (new startup call, after fetching the current mark price).

**Tech Stack:** Go, `pkg/strategy/matrix.go`, Bybit REST via `trader.FetchMarkPrice` / `tradeStream.PlaceOrder`

---

## File Map

| File | Change |
|------|--------|
| `pkg/strategy/matrix.go` | Extract section 3 → `matrixApplyStopCondSLs`; call from `matrixPriceTick` and `loadMatrixCycle` |
| `pkg/strategy/engine_test.go` | Add guard-condition tests for `matrixApplyStopCondSLs` |

---

### Task 1: Write guard-condition tests for `matrixApplyStopCondSLs`

Tests verify that the function returns early without touching `sr.runner` (which is nil in these test constructions) when the level should be skipped.

**Files:**
- Modify: `pkg/strategy/engine_test.go`

- [ ] **Step 1: Add the two guard tests**

Append to `pkg/strategy/engine_test.go`:

```go
func TestMatrixApplyStopCondSLsSkipsAlreadyReplaced(t *testing.T) {
	// SLReplaced=true → must exit before touching sr.runner (nil here).
	slot := 1
	condPct := 1.5
	replacePct := 0.5
	sr := &StrategyRunner{
		strategy: Strategy{
			Direction: DirectionLong,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		levels: []GridLevel{
			{
				ID:          "level-1",
				Slot:        &slot,
				Status:      LevelFilled,
				FilledPrice: 100.0,
				SLReplaced:  true,
			},
		},
	}
	// price (105) > threshold (101.5) — would place SL if guard is missing → panic on nil runner
	sr.matrixApplyStopCondSLs(context.Background(), 105.0)
}

func TestMatrixApplyStopCondSLsSkipsBelowThreshold(t *testing.T) {
	// price < stop_cond threshold → condition not met, no SL placed.
	slot := 1
	condPct := 1.5
	replacePct := 0.5
	sr := &StrategyRunner{
		strategy: Strategy{
			Direction: DirectionLong,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		levels: []GridLevel{
			{
				ID:          "level-1",
				Slot:        &slot,
				Status:      LevelFilled,
				FilledPrice: 100.0,
				SLReplaced:  false,
				SLOrderID:   "",
			},
		},
	}
	// price 101.0 < threshold 101.5 → condMet=false → no SL → no panic
	sr.matrixApplyStopCondSLs(context.Background(), 101.0)
}
```

- [ ] **Step 2: Run tests — expect compile error (function doesn't exist yet)**

```
cd c:\Users\123\Projects\sis
go test ./pkg/strategy/... 2>&1
```

Expected: `sr.matrixApplyStopCondSLs undefined`

---

### Task 2: Extract section 3 into `matrixApplyStopCondSLs`

**Files:**
- Modify: `pkg/strategy/matrix.go` (lines ~768–944)

- [ ] **Step 1: Add the new method just before `matrixPriceTick`**

In `pkg/strategy/matrix.go`, find the comment line:

```go
// matrixPriceTick is called on each mark price update from TickerHub or the REST fallback poll.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPriceTick(ctx context.Context, currentPrice float64) {
```

Insert this new function IMMEDIATELY before that comment block:

```go
// matrixApplyStopCondSLs checks all filled levels and places a replacement SL for any
// level whose stop_cond threshold has been crossed at currentPrice.
// Mirrors section 3 of matrixPriceTick and is also called once at startup in loadMatrixCycle
// to handle levels whose threshold was crossed while the server was down.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixApplyStopCondSLs(ctx context.Context, currentPrice float64) {
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
		var condMet bool
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
			sr.strategy.ID[:8], matrixSlotLinkStr(l), sr.matrixSLSeq)
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		fmtQty := trader.FormatQty(qty, sr.instr.QtyStep, sr.instr.MinQty)
		if fmtQty == "0" || fmtQty == "" {
			sr.warn(ctx, fmt.Sprintf("matrixApplyStopCondSLs: stop-cond SL replace L%d: qty rounds to zero, skipping", l.LevelIdx))
			continue
		}
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
		sr.runner.RegisterOrder(linkID, ref)

		result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:           sr.strategy.Symbol,
			Category:         sr.strategy.Category,
			Side:             slSide,
			OrderType:        "Market",
			Qty:              fmtQty,
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
			sr.errlog(ctx, fmt.Sprintf("Matrix stop-cond SL replace %s: %v", slotLabel(l.Slot), err))
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
		sr.info(ctx, fmt.Sprintf("Matrix SL %s переставлен → %.4f (stop cond сработал)", slotLabel(l.Slot), newTrigger))
	}
}
```

- [ ] **Step 2: Replace section 3 body in `matrixPriceTick` with a single call**

In `pkg/strategy/matrix.go`, find section 3 inside `matrixPriceTick`:

```go
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
		var condMet bool
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
			sr.strategy.ID[:8], matrixSlotLinkStr(l), sr.matrixSLSeq)
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		fmtQty := trader.FormatQty(qty, sr.instr.QtyStep, sr.instr.MinQty)
		if fmtQty == "0" || fmtQty == "" {
			sr.warn(ctx, fmt.Sprintf("matrixPriceTick: stop-cond SL replace L%d: qty rounds to zero, skipping", l.LevelIdx))
			continue
		}
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
		sr.runner.RegisterOrder(linkID, ref)

		result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:           sr.strategy.Symbol,
			Category:         sr.strategy.Category,
			Side:             slSide,
			OrderType:        "Market",
			Qty:              fmtQty,
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
			sr.errlog(ctx, fmt.Sprintf("Matrix stop-cond SL replace %s: %v", slotLabel(l.Slot), err))
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
		sr.info(ctx, fmt.Sprintf("Matrix SL %s переставлен → %.4f (stop cond сработал)", slotLabel(l.Slot), newTrigger))
	}
```

Replace with:

```go
	// 3. Check stop conditions for filled levels
	sr.matrixApplyStopCondSLs(ctx, currentPrice)
```

- [ ] **Step 3: Run tests — expect all pass**

```
cd c:\Users\123\Projects\sis
go test ./pkg/strategy/... 2>&1
```

Expected: `ok  sis/pkg/strategy`

- [ ] **Step 4: Commit**

```
cd c:\Users\123\Projects\sis
git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
git commit -m "refactor(matrix): extract stop-cond SL loop into matrixApplyStopCondSLs"
```

---

### Task 3: Call `matrixApplyStopCondSLs` from `loadMatrixCycle` at startup

**Files:**
- Modify: `pkg/strategy/matrix.go` (function `loadMatrixCycle`, lines ~630–651)

- [ ] **Step 1: Replace `loadMatrixCycle` body**

Find the current function:

```go
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
	if err := sr.loadActiveCycle(ctx); err != nil {
		return err
	}
	sr.mu.Lock()
	sr.restoreMatrixWaitingSlots()
	sr.mu.Unlock()
	// Trigger virtual L0 immediately if it's still pending — the price-cross condition
	// is already satisfied for the entry slot, and the price monitor won't fire it
	// because currentPrice >= TargetPrice only holds at the moment of cycle start.
	sr.mu.Lock()
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot == 0 && l.Status == LevelPending && sr.matrixIsVirtual(l) {
			sr.matrixTriggerVirtualLevel(ctx, l)
			break
		}
	}
	sr.mu.Unlock()
	sr.launchMatrixPriceMonitor()
	return nil
}
```

Replace with:

```go
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
	if err := sr.loadActiveCycle(ctx); err != nil {
		return err
	}
	// Fetch mark price before taking lock — used for retroactive stop-cond check below.
	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: loadMatrixCycle: fetch price for stop-cond check: %v", sr.strategy.ID, err)
	}
	sr.mu.Lock()
	sr.restoreMatrixWaitingSlots()
	if price > 0 {
		sr.matrixApplyStopCondSLs(ctx, price)
	}
	sr.mu.Unlock()
	// Trigger virtual L0 immediately if it's still pending — the price-cross condition
	// is already satisfied for the entry slot, and the price monitor won't fire it
	// because currentPrice >= TargetPrice only holds at the moment of cycle start.
	sr.mu.Lock()
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot == 0 && l.Status == LevelPending && sr.matrixIsVirtual(l) {
			sr.matrixTriggerVirtualLevel(ctx, l)
			break
		}
	}
	sr.mu.Unlock()
	sr.launchMatrixPriceMonitor()
	return nil
}
```

- [ ] **Step 2: Build and test**

```
cd c:\Users\123\Projects\sis
go build ./... 2>&1 && go test ./pkg/strategy/... 2>&1
```

Expected:
```
ok  sis/pkg/strategy
```

- [ ] **Step 3: Commit**

```
cd c:\Users\123\Projects\sis
git add pkg/strategy/matrix.go
git commit -m "feat(matrix): place retroactive stop-cond SL on startup if threshold already crossed"
```

---

### Task 4: Manual verification

- [ ] **Step 1: Build and restart the server**

```
cd c:\Users\123\Projects\sis
go build -o api-gateway.exe ./cmd/api-gateway && .\api-gateway.exe
```

- [ ] **Step 2: Check startup log for the matrix strategy**

In `server.err.log`, look for lines like:

```
Matrix SL L(N) переставлен → 0.XXXX (stop cond сработал)
```

If current price is below all `stop_cond` thresholds → no such lines → correct (conservative behaviour).

- [ ] **Step 3: Verify `matrixPriceTick` still triggers stop-cond in real-time**

Watch for the same log line during live price movement. If the threshold is crossed, the log should appear within one price tick (same as before the refactor).
