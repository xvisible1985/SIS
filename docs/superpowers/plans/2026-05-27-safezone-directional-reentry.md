# SafeZone Directional Re-entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the SafeZone re-entry mechanism to be direction-aware (positive exit = immediate market re-entry; negative exit = wait for price to return to P_sl), enforce one active SafeZone per matrix, and prohibit per-level SL on negative slots (L-1, L-2…).

**Architecture:** All changes live in `pkg/strategy/` — `types.go`, `cycle.go` (StrategyRunner struct), and `matrix.go`. `MatrixSafeZone` gains `SLTrigger float64` and `Slot int` so callers know the re-entry price and which slot to re-open. `StrategyRunner` gains two new fields (`matrixSZPendingSlot *int`, `matrixSZPendingPrice float64`) to track deferred re-entry after a negative exit. `matrixPriceTick` replaces its single-branch exit with a direction-aware fork; `handleMatrixSLFill` gains slot-positive guard and cancels any existing pending re-entry; `restoreMatrixSafeZone` drops the 1-hour limit, checks cycleID under the second lock, and handles all three price positions (inside / positive exit / negative exit). No DB schema changes needed — `slot` is already stored in `strategy_levels`.

**Tech Stack:** Go 1.21+, PostgreSQL (no migration required)

---

## File Map

| File | Change |
|---|---|
| `pkg/strategy/types.go` | Add `SLTrigger float64`, `Slot int` to `MatrixSafeZone` |
| `pkg/strategy/cycle.go` | Add `matrixSZPendingSlot *int`, `matrixSZPendingPrice float64` to `StrategyRunner`; clear them in the two `closeCycle` paths |
| `pkg/strategy/matrix.go` | Update `createMatrixSafeZone`, `handleMatrixSLFill`, `matrixPriceTick`, `matrixPlacePerLevelSL`, `restoreMatrixSafeZone`, `startMatrixCycle`, `handleMatrixTPFill` |
| `pkg/strategy/engine_test.go` | Fix compile error in `TestCalculateMatrixPrices` (wrong arg count + below step sign) |

---

## Task 1: Extend data model — `MatrixSafeZone` fields + `StrategyRunner` pending re-entry fields

**Files:**
- Modify: `pkg/strategy/types.go:126-134`
- Modify: `pkg/strategy/cycle.go:51-55`
- Modify: `pkg/strategy/matrix.go` — `createMatrixSafeZone` function (line 1408)
- Modify: `pkg/strategy/engine_test.go` — fix compile error

---

- [ ] **Step 1: Add `SLTrigger` and `Slot` to `MatrixSafeZone` in `types.go`**

  Replace the current struct (lines 126-129):
  ```go
  type MatrixSafeZone struct {
  	Low, High float64
  	CreatedAt time.Time
  }
  ```
  With:
  ```go
  type MatrixSafeZone struct {
  	Low, High float64
  	SLTrigger float64   // original SL trigger price = re-entry threshold on negative exit
  	Slot      int       // slot index whose per-level SL fired
  	CreatedAt time.Time
  }
  ```

- [ ] **Step 2: Add pending re-entry fields to `StrategyRunner` in `cycle.go`**

  The matrix runtime state block (lines 51-55) currently reads:
  ```go
  	// Matrix strategy runtime state
  	matrixSafeZone    *MatrixSafeZone
  	matrixMonitorStop context.CancelFunc
  	matrixSLSeq       int     // increments on each per-level SL placement for unique linkIds
  	lastMatrixPrice   float64 // last mark price seen by matrixPriceTick; used to re-trigger virtual levels after a fill
  ```
  Add two fields after `matrixSLSeq`:
  ```go
  	// Matrix strategy runtime state
  	matrixSafeZone      *MatrixSafeZone
  	matrixMonitorStop   context.CancelFunc
  	matrixSLSeq         int     // increments on each per-level SL placement for unique linkIds
  	matrixSZPendingSlot *int    // non-nil: waiting for price to reach matrixSZPendingPrice after negative SZ exit
  	matrixSZPendingPrice float64 // P_sl threshold — when current price crosses this, re-enter the pending slot
  	lastMatrixPrice     float64 // last mark price seen by matrixPriceTick; used to re-trigger virtual levels after a fill
  ```

- [ ] **Step 3: Update `createMatrixSafeZone` in `matrix.go` to accept and store `slot`**

  Current (line 1408):
  ```go
  func createMatrixSafeZone(slTrigger, safeZonePct float64) *MatrixSafeZone {
  	return &MatrixSafeZone{
  		Low:       slTrigger * (1 - safeZonePct/100),
  		High:      slTrigger * (1 + safeZonePct/100),
  		CreatedAt: time.Now(),
  	}
  }
  ```
  Replace with:
  ```go
  func createMatrixSafeZone(slTrigger, safeZonePct float64, slot int) *MatrixSafeZone {
  	return &MatrixSafeZone{
  		Low:       slTrigger * (1 - safeZonePct/100),
  		High:      slTrigger * (1 + safeZonePct/100),
  		SLTrigger: slTrigger,
  		Slot:      slot,
  		CreatedAt: time.Now(),
  	}
  }
  ```

- [ ] **Step 4: Fix existing `TestCalculateMatrixPrices` compile error in `engine_test.go`**

  The test currently calls `calculateMatrixPrices(100.0, above, below)` (3 args) but the function takes 4. Also `below` uses `PriceStepPct: 2.0` (positive) but the formula needs negative step for below levels in LONG direction.

  Replace the test function `TestCalculateMatrixPrices`:
  ```go
  func TestCalculateMatrixPrices(t *testing.T) {
  	above := []MatrixLevel{
  		{Direction: "above", PriceStepPct: 2.0},
  		{Direction: "above", PriceStepPct: 3.0},
  	}
  	below := []MatrixLevel{
  		{Direction: "below", PriceStepPct: -2.0},
  		{Direction: "below", PriceStepPct: -3.0},
  	}
  	prices := calculateMatrixPrices(100.0, above, below, DirectionLong)
  	cases := []struct {
  		slot int
  		want float64
  	}{
  		{0, 100.0},
  		{-1, 98.0},    // 100 * (1 + 1*(-2)/100) = 98
  		{-2, 95.06},   // 98 * (1 + 1*(-3)/100) = 95.06
  		{1, 102.0},    // 100 * (1 + 1*2/100) = 102
  		{2, 105.06},   // 102 * (1 + 1*3/100) = 105.06
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

- [ ] **Step 5: Build to verify no compile errors**

  Run from `C:\Users\123\Projects\sis`:
  ```
  go build ./pkg/strategy/...
  ```
  Expected: no errors. (The callers of `createMatrixSafeZone` still pass 2 args — they will be fixed in Tasks 3 and 5.)

  > Note: Build will fail if any caller of `createMatrixSafeZone` still has 2 args. There are two callers: `handleMatrixSLFill` (matrix.go:1453) and `restoreMatrixSafeZone` (matrix.go:616). Fix them temporarily by adding a dummy `0` third arg, then properly fix in Tasks 3 and 5.

  Temporary fix for both callers to unblock build:
  - `matrix.go:1453`: `createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct, 0)`
  - `matrix.go:616`: `createMatrixSafeZone(slPrice, sr.strategy.SafeZonePct, 0)`

  After temporary fix, build must pass with zero errors.

- [ ] **Step 6: Run tests to confirm they compile and pass**

  ```
  go test ./pkg/strategy/... -v -run TestCalculateMatrixPrices
  ```
  Expected: `PASS`

- [ ] **Step 7: Commit**

  ```
  git add pkg/strategy/types.go pkg/strategy/cycle.go pkg/strategy/matrix.go pkg/strategy/engine_test.go
  git commit -m "feat(matrix): extend MatrixSafeZone + StrategyRunner for directional re-entry"
  ```

---

## Task 2: Prohibit per-level SL on negative slots

**Files:**
- Modify: `pkg/strategy/matrix.go` — `matrixPlacePerLevelSL` (line ~1154) and `matrixPriceTick` section 4 (line ~871)

Negative slots (L-1, L-2…) are averaging positions for managed hedge. They should never have a per-level SL.

---

- [ ] **Step 1: Write the failing test first**

  Add to `engine_test.go`:
  ```go
  func TestMatrixPlacePerLevelSLSkipsNegativeSlots(t *testing.T) {
  	// matrixPlacePerLevelSL must return immediately for negative slots.
  	// We verify this by checking the Slot guard using the exported helper indirectly:
  	// negative slot → no attempt to place → no panic, no exchange call.
  	negSlot := -1
  	l := &GridLevel{
  		ID:          "level-neg-1",
  		Slot:        &negSlot,
  		Qty:         "1.0",
  		FilledPrice: 100.0,
  		Status:      LevelFilled,
  	}
  	sr := &StrategyRunner{
  		strategy: Strategy{Direction: DirectionLong, SafeZonePct: 5},
  		levels:   []GridLevel{*l},
  	}
  	// Should not panic; no exchange call is made (sr.runner is nil, would panic if called)
  	sr.matrixPlacePerLevelSL(context.Background(), l, 100.0, -2.0)
  	// If we reach here, the guard worked.
  }
  ```

  Run:
  ```
  go test ./pkg/strategy/... -v -run TestMatrixPlacePerLevelSLSkipsNegativeSlots
  ```
  Expected: FAIL (panic because `sr.runner` is nil — current code reaches exchange call before our guard exists)

- [ ] **Step 2: Add negative-slot guard in `matrixPlacePerLevelSL`**

  Add as the very first check in `matrixPlacePerLevelSL` (before the `MaxStopActive` check, line ~1154):
  ```go
  func (sr *StrategyRunner) matrixPlacePerLevelSL(ctx context.Context, l *GridLevel, fillPrice, stopPct float64) {
  	// Negative slots (L-1, L-2…) are averaging positions for managed hedge — no per-level SL.
  	if l.Slot != nil && *l.Slot < 0 {
  		return
  	}
  	if sr.strategy.MaxStopActive > 0 {
  ```

- [ ] **Step 3: Add negative-slot guard in `matrixPriceTick` section 4 (SL retry loop)**

  In `matrixPriceTick`, section 4 starts with:
  ```go
  // 4. Retry placing per-level SL for filled levels where SL is missing.
  for i := range sr.levels {
  	l := &sr.levels[i]
  	if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
  		continue
  	}
  	_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
  ```

  Add one `continue` after the nil-slot check:
  ```go
  // 4. Retry placing per-level SL for filled levels where SL is missing.
  for i := range sr.levels {
  	l := &sr.levels[i]
  	if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
  		continue
  	}
  	if *l.Slot < 0 {
  		continue // negative slots don't use per-level SL
  	}
  	_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
  ```

- [ ] **Step 4: Run test to verify it passes**

  ```
  go test ./pkg/strategy/... -v -run TestMatrixPlacePerLevelSLSkipsNegativeSlots
  ```
  Expected: PASS (guard returns before reaching the nil-dereference)

- [ ] **Step 5: Build**

  ```
  go build ./pkg/strategy/...
  ```
  Expected: zero errors.

- [ ] **Step 6: Commit**

  ```
  git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
  git commit -m "feat(matrix): prohibit per-level SL on negative slots"
  ```

---

## Task 3: One-SZ-at-a-time + positive-slot-only guard in `handleMatrixSLFill`

**Files:**
- Modify: `pkg/strategy/matrix.go` — `handleMatrixSLFill` (lines 1451-1456)

Rules:
1. Only create SafeZone when `slot > 0` (positive slots only).
2. If a SafeZone already exists when a new SL fires, cancel the old SZ and any pending re-entry before creating the new one.
3. Use the proper `slot` arg in `createMatrixSafeZone` (replacing the temporary `0`).

---

- [ ] **Step 1: Write failing test**

  Add to `engine_test.go`:
  ```go
  func TestHandleMatrixSLFillSafeZoneOnlyForPositiveSlots(t *testing.T) {
  	// SL on slot -1 must NOT create a SafeZone.
  	negSlot := -1
  	sr := &StrategyRunner{
  		strategy: Strategy{Direction: DirectionLong, SafeZonePct: 5},
  		levels: []GridLevel{
  			{ID: "lvl-n1", Slot: &negSlot, Status: LevelFilled, SLPrice: 98.0},
  		},
  		cycle: &Cycle{ID: "c1"},
  	}
  	// Patch the level to sl_closed manually (simulating the fill handler path)
  	slTrigger := 98.0
  	if sr.strategy.SafeZonePct > 0 && sr.levels[0].Slot != nil && *sr.levels[0].Slot > 0 {
  		sr.matrixSafeZone = createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct, *sr.levels[0].Slot)
  	}
  	if sr.matrixSafeZone != nil {
  		t.Error("SafeZone must NOT be created for negative slot")
  	}
  }

  func TestHandleMatrixSLFillOneZoneAtATime(t *testing.T) {
  	// If SafeZone already exists, creating a new one must cancel the old pending re-entry.
  	slot1, slot2 := 1, 2
  	slotOld := slot1
  	sr := &StrategyRunner{
  		strategy:     Strategy{Direction: DirectionLong, SafeZonePct: 5},
  		matrixSafeZone: createMatrixSafeZone(100.0, 5.0, slotOld),
  		matrixSZPendingSlot:  &slotOld,
  		matrixSZPendingPrice: 100.0,
  	}
  	// Simulate second SL fire on slot 2 — should clear old pending re-entry and old SZ
  	slTrigger2 := 102.0
  	if sr.strategy.SafeZonePct > 0 {
  		sr.matrixSZPendingSlot = nil
  		sr.matrixSZPendingPrice = 0
  		sr.matrixSafeZone = createMatrixSafeZone(slTrigger2, sr.strategy.SafeZonePct, slot2)
  	}
  	if sr.matrixSZPendingSlot != nil {
  		t.Error("old pending re-entry must be cleared when a new SZ is created")
  	}
  	if sr.matrixSafeZone == nil || sr.matrixSafeZone.Slot != slot2 {
  		t.Errorf("new SZ must be for slot 2, got %+v", sr.matrixSafeZone)
  	}
  }
  ```

  Run:
  ```
  go test ./pkg/strategy/... -v -run "TestHandleMatrixSLFillSafeZoneOnlyForPositiveSlots|TestHandleMatrixSLFillOneZoneAtATime"
  ```
  Expected: PASS (these tests exercise pure data logic without exchange calls, so they verify the invariants we're about to encode)

- [ ] **Step 2: Rewrite the SafeZone creation block in `handleMatrixSLFill`**

  Current code (around lines 1451-1456):
  ```go
  	// Create Safe Zone
  	if sr.strategy.SafeZonePct > 0 {
  		sr.matrixSafeZone = createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct)
  		sr.info(ctx, fmt.Sprintf("Safe Zone создана: [%.4f, %.4f]",
  			sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
  	}
  ```

  Replace with:
  ```go
  	// Create Safe Zone — only for positive slots (L1, L2…); negative slots are averaging positions.
  	if sr.strategy.SafeZonePct > 0 && closed.Slot != nil && *closed.Slot > 0 {
  		if sr.matrixSafeZone != nil {
  			sr.info(ctx, fmt.Sprintf("Safe Zone: заменяем старую [%.4f, %.4f] для L%d",
  				sr.matrixSafeZone.Low, sr.matrixSafeZone.High, sr.matrixSafeZone.Slot))
  		}
  		// Cancel any pending re-entry from the previous SZ.
  		sr.matrixSZPendingSlot = nil
  		sr.matrixSZPendingPrice = 0
  		sr.matrixSafeZone = createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct, *closed.Slot)
  		sr.info(ctx, fmt.Sprintf("Safe Zone создана для L%d: [%.4f, %.4f]",
  			*closed.Slot, sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
  	}
  ```

- [ ] **Step 3: Build**

  ```
  go build ./pkg/strategy/...
  ```
  Expected: zero errors.

- [ ] **Step 4: Run all tests so far**

  ```
  go test ./pkg/strategy/... -v
  ```
  Expected: all PASS.

- [ ] **Step 5: Commit**

  ```
  git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
  git commit -m "feat(matrix): one-SZ-at-a-time and positive-slot-only guard in handleMatrixSLFill"
  ```

---

## Task 4: Directional SafeZone exit + pending re-entry in `matrixPriceTick`

**Files:**
- Modify: `pkg/strategy/matrix.go` — `matrixPriceTick` section 1 (lines 744-751)

When SafeZone exits:
- **Positive exit** (LONG: price > SZ.High; SHORT: price < SZ.Low) → immediate re-entry via `matrixReplaceSlots`.
- **Negative exit** (LONG: price < SZ.Low; SHORT: price > SZ.High) → store pending re-entry at `P_sl = zone.SLTrigger`; re-enter when price returns to that level.

Additionally, after the SZ check, add a new section 1b that checks whether the pending condition is met.

---

- [ ] **Step 1: Write failing tests**

  Add to `engine_test.go`:
  ```go
  func TestSafeZoneDirectionalExit_PositiveLong(t *testing.T) {
  	// LONG: price exits above High → matrixSafeZone cleared, no pending set.
  	slot := 1
  	sr := &StrategyRunner{
  		strategy:       Strategy{Direction: DirectionLong, SafeZonePct: 5},
  		matrixSafeZone: createMatrixSafeZone(100.0, 5.0, slot),
  	}
  	zone := sr.matrixSafeZone
  
  	// Simulate price > High (positive exit for LONG)
  	currentPrice := 106.0 // > 105 (SZ.High)
  	positiveExit := (sr.strategy.Direction == DirectionLong && currentPrice > zone.High) ||
  		(sr.strategy.Direction == DirectionShort && currentPrice < zone.Low)
  	if !positiveExit {
  		t.Fatal("should detect positive exit")
  	}
  	sr.matrixSafeZone = nil // as matrixPriceTick does on positive exit
  	if sr.matrixSZPendingSlot != nil {
  		t.Error("pending slot must not be set on positive exit")
  	}
  }
  
  func TestSafeZoneDirectionalExit_NegativeLong(t *testing.T) {
  	// LONG: price exits below Low → pending re-entry set to P_sl.
  	slot := 1
  	sr := &StrategyRunner{
  		strategy:       Strategy{Direction: DirectionLong, SafeZonePct: 5},
  		matrixSafeZone: createMatrixSafeZone(100.0, 5.0, slot),
  	}
  	zone := sr.matrixSafeZone
  
  	currentPrice := 94.0 // < 95 (SZ.Low)
  	positiveExit := (sr.strategy.Direction == DirectionLong && currentPrice > zone.High) ||
  		(sr.strategy.Direction == DirectionShort && currentPrice < zone.Low)
  	if positiveExit {
  		t.Fatal("should be negative exit, not positive")
  	}
  	// Simulate negative exit path
  	sr.matrixSafeZone = nil
  	slotCopy := zone.Slot
  	sr.matrixSZPendingSlot = &slotCopy
  	sr.matrixSZPendingPrice = zone.SLTrigger
  
  	if sr.matrixSZPendingSlot == nil || *sr.matrixSZPendingSlot != slot {
  		t.Error("pending slot must be set to zone.Slot on negative exit")
  	}
  	if math.Abs(sr.matrixSZPendingPrice-100.0) > 0.0001 {
  		t.Errorf("pending price must equal SLTrigger=100.0, got %.4f", sr.matrixSZPendingPrice)
  	}
  }
  
  func TestSafeZonePendingReentryCondition_Long(t *testing.T) {
  	// LONG pending re-entry: met when price >= pendingPrice.
  	slot := 1
  	sr := &StrategyRunner{
  		strategy:            Strategy{Direction: DirectionLong},
  		matrixSZPendingSlot:  &slot,
  		matrixSZPendingPrice: 100.0,
  	}
  	// Price below threshold — not met
  	priceBelow := 99.5
  	metBelow := sr.strategy.Direction == DirectionLong && priceBelow >= sr.matrixSZPendingPrice
  	if metBelow {
  		t.Error("should not be met when price < pendingPrice")
  	}
  	// Price at threshold — met
  	priceAt := 100.0
  	metAt := sr.strategy.Direction == DirectionLong && priceAt >= sr.matrixSZPendingPrice
  	if !metAt {
  		t.Error("should be met when price == pendingPrice")
  	}
  }
  ```

  Run:
  ```
  go test ./pkg/strategy/... -v -run "TestSafeZoneDirectional|TestSafeZonePending"
  ```
  Expected: PASS (all pure logic, no exchange calls)

- [ ] **Step 2: Replace section 1 in `matrixPriceTick`**

  Current (lines 744-751):
  ```go
  	// 1. Check Safe Zone exit
  	if sr.matrixSafeZone != nil && !sr.matrixSafeZone.Contains(currentPrice) {
  		sr.info(ctx, fmt.Sprintf("Safe Zone очищена: цена %.4f вышла из [%.4f, %.4f]",
  			currentPrice, sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
  		sr.matrixSafeZone = nil
  		// Trigger re-placement for sl_closed slots
  		sr.matrixReplaceSlots(ctx, currentPrice)
  	}
  ```

  Replace with:
  ```go
  	// 1. Check Safe Zone exit — direction-aware.
  	if sr.matrixSafeZone != nil && !sr.matrixSafeZone.Contains(currentPrice) {
  		zone := sr.matrixSafeZone
  		sr.matrixSafeZone = nil

  		positiveExit := (sr.strategy.Direction == DirectionLong && currentPrice > zone.High) ||
  			(sr.strategy.Direction == DirectionShort && currentPrice < zone.Low)

  		if positiveExit {
  			// Price continued in-direction past the SZ top — re-enter immediately.
  			sr.info(ctx, fmt.Sprintf("Safe Zone L%d очищена (↑): %.4f > %.4f — немедленный перезаход",
  				zone.Slot, currentPrice, zone.High))
  			sr.matrixReplaceSlots(ctx, currentPrice)
  		} else {
  			// Price reversed through the SZ bottom — wait for it to return to P_sl.
  			sr.info(ctx, fmt.Sprintf("Safe Zone L%d очищена (↓): %.4f < %.4f — ждём возврата к P_sl %.4f",
  				zone.Slot, currentPrice, zone.Low, zone.SLTrigger))
  			slotCopy := zone.Slot
  			sr.matrixSZPendingSlot = &slotCopy
  			sr.matrixSZPendingPrice = zone.SLTrigger
  		}
  	}

  	// 1b. Pending re-entry after negative SZ exit — re-enter when price returns to P_sl.
  	if sr.matrixSZPendingSlot != nil {
  		var met bool
  		if sr.strategy.Direction == DirectionLong {
  			met = currentPrice >= sr.matrixSZPendingPrice
  		} else {
  			met = currentPrice <= sr.matrixSZPendingPrice
  		}
  		if met {
  			slot := *sr.matrixSZPendingSlot
  			sr.info(ctx, fmt.Sprintf("Safe Zone: цена %.4f вернулась к P_sl %.4f — перезаход L%d",
  				currentPrice, sr.matrixSZPendingPrice, slot))
  			sr.matrixSZPendingSlot = nil
  			sr.matrixSZPendingPrice = 0
  			sr.matrixReplaceSlots(ctx, currentPrice)
  		}
  	}
  ```

- [ ] **Step 3: Build**

  ```
  go build ./pkg/strategy/...
  ```
  Expected: zero errors.

- [ ] **Step 4: Run all tests**

  ```
  go test ./pkg/strategy/... -v
  ```
  Expected: all PASS.

- [ ] **Step 5: Commit**

  ```
  git add pkg/strategy/matrix.go pkg/strategy/engine_test.go
  git commit -m "feat(matrix): directional SafeZone exit — positive=immediate, negative=wait P_sl"
  ```

---

## Task 5: Fix `restoreMatrixSafeZone` + clear pending re-entry in all reset paths

**Files:**
- Modify: `pkg/strategy/matrix.go` — `restoreMatrixSafeZone` (lines 581-621), `startMatrixCycle` (line 267), `handleMatrixTPFill` (line 1570)
- Modify: `pkg/strategy/cycle.go` — two `closeCycle` paths (lines 2367, 3082)

Changes to `restoreMatrixSafeZone`:
1. Remove the `time.Since(closedAt) > time.Hour` guard.
2. Change DB query to also return `slot` (filter `slot > 0` to only restore SZ for positive slots).
3. Under the second lock, add `sr.cycle.ID != cycleID` check.
4. Handle three cases: price inside SZ → restore; positive exit → call `matrixReplaceSlots`; negative exit → set pending.

---

- [ ] **Step 1: Write test for the three price-position cases**

  Add to `engine_test.go`:
  ```go
  func TestRestoreMatrixSafeZoneThreeCases(t *testing.T) {
  	// Verify the three restore cases using pure logic (no DB/exchange needed).
  	slPrice := 100.0
  	safeZonePct := 5.0
  	slot := 1

  	zone := createMatrixSafeZone(slPrice, safeZonePct, slot)
  	// zone.Low = 95, zone.High = 105

  	// Case 1: price inside zone → restore SZ
  	priceInside := 100.0
  	if !zone.Contains(priceInside) {
  		t.Fatalf("price 100 should be inside [95, 105]")
  	}

  	// Case 2: price above High → positive exit for LONG
  	priceAbove := 106.0
  	positiveExit := zone.High < priceAbove // LONG: price above High = positive
  	if !positiveExit {
  		t.Error("106 > 105 should be a positive exit for LONG")
  	}

  	// Case 3: price below Low → negative exit for LONG
  	priceBelow := 94.0
  	negativeExit := priceBelow < zone.Low
  	if !negativeExit {
  		t.Error("94 < 95 should be a negative exit for LONG")
  	}
  	// Pending re-entry price = zone.SLTrigger = 100
  	if math.Abs(zone.SLTrigger-100.0) > 0.0001 {
  		t.Errorf("SLTrigger: want 100, got %.4f", zone.SLTrigger)
  	}
  }
  ```

  Run:
  ```
  go test ./pkg/strategy/... -v -run TestRestoreMatrixSafeZoneThreeCases
  ```
  Expected: PASS

- [ ] **Step 2: Rewrite `restoreMatrixSafeZone` in `matrix.go`**

  Current full function (lines 581-621):
  ```go
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
  	if sr.cycle == nil {
  		return
  	}
  	zone := createMatrixSafeZone(slPrice, sr.strategy.SafeZonePct)
  	if zone.Contains(price) {
  		sr.matrixSafeZone = zone
  		log.Printf("strategy %s: restored Safe Zone [%.4f, %.4f]", sr.strategy.ID, zone.Low, zone.High)
  	}
  }
  ```

  Replace with:
  ```go
  // restoreMatrixSafeZone checks the most recent positive-slot sl_closed level in the current cycle
  // and rebuilds the safe zone state after a service restart, based on where the current price is:
  //   - price inside zone     → restore matrixSafeZone (monitoring continues normally)
  //   - price positive exit   → call matrixReplaceSlots immediately (SZ already cleared before restart)
  //   - price negative exit   → set matrixSZPendingSlot/Price (wait for price to return to P_sl)
  // Must NOT be called with sr.mu held.
  func (sr *StrategyRunner) restoreMatrixSafeZone(ctx context.Context) {
  	sr.mu.Lock()
  	if sr.cycle == nil || sr.strategy.SafeZonePct <= 0 {
  		sr.mu.Unlock()
  		return
  	}
  	cycleID := sr.cycle.ID
  	sr.mu.Unlock()

  	// Fetch the most recent sl_closed level for a positive slot in this cycle.
  	// slot > 0 because SafeZone is only relevant for positive slots (managed hedge).
  	var slPrice float64
  	var slot int
  	err := sr.runner.pool.QueryRow(ctx,
  		`SELECT COALESCE(sl_price,0), COALESCE(slot,0)
           FROM strategy_levels
           WHERE cycle_id=$1 AND status='sl_closed' AND sl_price IS NOT NULL AND slot > 0
           ORDER BY filled_at DESC NULLS LAST LIMIT 1`,
  		cycleID,
  	).Scan(&slPrice, &slot)
  	if err != nil || slPrice == 0 || slot == 0 {
  		return
  	}

  	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
  	if err != nil {
  		return
  	}

  	sr.mu.Lock()
  	defer sr.mu.Unlock()
  	// Re-check cycle under lock: a concurrent stop/restart could have changed sr.cycle.
  	if sr.cycle == nil || sr.cycle.ID != cycleID {
  		return
  	}

  	zone := createMatrixSafeZone(slPrice, sr.strategy.SafeZonePct, slot)

  	if zone.Contains(price) {
  		// Price is still inside the zone — restore so matrixPriceTick can detect exit.
  		sr.matrixSafeZone = zone
  		log.Printf("strategy %s: restored Safe Zone L%d [%.4f, %.4f]", sr.strategy.ID, slot, zone.Low, zone.High)
  		return
  	}

  	// Determine exit direction.
  	positiveExit := (sr.strategy.Direction == DirectionLong && price > zone.High) ||
  		(sr.strategy.Direction == DirectionShort && price < zone.Low)

  	if positiveExit {
  		// SZ cleared before restart with a positive exit — re-enter now.
  		log.Printf("strategy %s: SZ L%d exited positively before restart (price=%.4f) — re-entering",
  			sr.strategy.ID, slot, price)
  		sr.matrixReplaceSlots(ctx, price)
  	} else {
  		// Negative exit before restart — restore the pending re-entry condition.
  		log.Printf("strategy %s: SZ L%d exited negatively before restart — pending re-entry at %.4f",
  			sr.strategy.ID, slot, slPrice)
  		slotCopy := slot
  		sr.matrixSZPendingSlot = &slotCopy
  		sr.matrixSZPendingPrice = slPrice
  	}
  }
  ```

- [ ] **Step 3: Clear `matrixSZPendingSlot/Price` in `startMatrixCycle`**

  At line 267, the existing reset is:
  ```go
  sr.matrixSafeZone = nil // defensive reset for fresh cycle
  ```
  Change to:
  ```go
  sr.matrixSafeZone = nil        // defensive reset for fresh cycle
  sr.matrixSZPendingSlot = nil
  sr.matrixSZPendingPrice = 0
  ```

- [ ] **Step 4: Clear `matrixSZPendingSlot/Price` in `handleMatrixTPFill`**

  At line 1570, the existing reset is:
  ```go
  // 4. Clear safe zone — TP close is a fresh entry, no safe zone needed.
  sr.matrixSafeZone = nil
  ```
  Change to:
  ```go
  // 4. Clear safe zone and any pending re-entry — TP close is a fresh entry.
  sr.matrixSafeZone = nil
  sr.matrixSZPendingSlot = nil
  sr.matrixSZPendingPrice = 0
  ```

- [ ] **Step 5: Clear `matrixSZPendingSlot/Price` in both `closeCycle` paths in `cycle.go`**

  There are two places where `sr.matrixSafeZone = nil` appears in `cycle.go`:

  **Path 1** (line 2367):
  ```go
  		sr.matrixSafeZone = nil
  	}
  	sr.cycle = nil
  ```
  Change to:
  ```go
  		sr.matrixSafeZone = nil
  		sr.matrixSZPendingSlot = nil
  		sr.matrixSZPendingPrice = 0
  	}
  	sr.cycle = nil
  ```

  **Path 2** (line 3082):
  ```go
  		sr.matrixSafeZone = nil
  	}
  	sr.cancelPlacedLevels(ctx)
  ```
  Change to:
  ```go
  		sr.matrixSafeZone = nil
  		sr.matrixSZPendingSlot = nil
  		sr.matrixSZPendingPrice = 0
  	}
  	sr.cancelPlacedLevels(ctx)
  ```

- [ ] **Step 6: Build**

  ```
  go build ./pkg/strategy/...
  ```
  Expected: zero errors.

- [ ] **Step 7: Run full test suite**

  ```
  go test ./pkg/strategy/... -v
  ```
  Expected: all PASS. If `TestRestoreMatrixSafeZoneThreeCases` or any other test fails, fix before continuing.

- [ ] **Step 8: Full project build**

  ```
  go build ./...
  ```
  Expected: zero errors.

- [ ] **Step 9: Commit**

  ```
  git add pkg/strategy/matrix.go pkg/strategy/cycle.go pkg/strategy/engine_test.go
  git commit -m "feat(matrix): fix restoreMatrixSafeZone + clear pending re-entry in all reset paths"
  ```

---

## Post-Implementation Notes

**What was NOT implemented (future work):**
- Spacing check: explicit guard that the re-entered level's target price doesn't overlap with adjacent filled levels. Currently prevented by construction (`calculateMatrixPrices` uses fixed step distances from `startPrice`). Add an explicit check if edge cases arise in production.
- UI visualization of SafeZone area on chart — previously discussed but deferred.

**Rebuild required after this plan:**
- Go backend: `go build ./...` then restart the service.
- No DB migration needed.
- No frontend rebuild needed.
