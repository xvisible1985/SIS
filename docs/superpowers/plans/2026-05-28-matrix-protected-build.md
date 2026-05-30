# Matrix Protected Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current SafeZone re-entry mechanism with a depth-ordered re-entry system, and add a global "Защищённое построение" toggle that gates each level on the previous level's stop confirmation.

**Architecture:**
- Remove `matrixSafeZone` / `matrixSZPending*` runtime state from `StrategyRunner`; replace with `matrixWaitingSlots map[int]bool`
- On per-level SL fire: mark slot as waiting (not re-enter immediately)
- Price monitor checks: for each waiting slot, find deepest active reference slot → re-enter only when price passes below (SHORT) or above (LONG) that reference's fill price
- `ProtectedBuild bool` on `Strategy` gates both virtual level triggering and waiting-slot re-entry on stop confirmation of the reference level
- DB column `protected_build BOOLEAN DEFAULT FALSE NOT NULL` on `strategies` table

**Tech Stack:** Go (backend engine), PostgreSQL (DB migration), React/TypeScript (frontend toggle)

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `db/migrations/NNNN_add_protected_build.sql` | Create | Add `protected_build` column |
| `pkg/strategy/types.go` | Modify | Add `ProtectedBuild bool` to `Strategy` |
| `pkg/strategy/cycle.go` | Modify | Replace SZ fields with `matrixWaitingSlots` |
| `pkg/strategy/engine.go` | Modify | Add `protected_build` to SELECT/scan; update `GetMatrixSafeZone` |
| `pkg/strategy/matrix.go` | Major rework | New re-entry logic, protected build checks |
| `services/api-gateway/strategy_handler.go` | Modify | Add `protected_build` to read/write/response |
| `frontend/src/types.ts` | Modify | Add `protected_build` to `Strategy` and `StrategyFormData` |
| `frontend/src/components/strategies/StrategyModal.tsx` | Modify | Add toggle in matrix tab |

---

### Task 1: DB Migration — add `protected_build` column

**Files:**
- Create: `db/migrations/` — find the highest-numbered existing migration and create the next one

- [ ] **Step 1: Find existing migration directory and naming convention**

Run:
```powershell
Get-ChildItem C:\Users\123\Projects\sis\db\migrations | Sort-Object Name | Select-Object -Last 3
```
Note the naming convention (e.g., `0042_something.sql` or timestamp-based).

- [ ] **Step 2: Create the migration file**

Create `db/migrations/<NEXT>_add_protected_build.sql`:
```sql
ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS protected_build BOOLEAN NOT NULL DEFAULT FALSE;
```

- [ ] **Step 3: Apply the migration**

Run against the local DB (find connection details from existing code or env):
```powershell
# Find how migrations are applied in this project
Get-ChildItem C:\Users\123\Projects\sis -Filter "*.sh" -Recurse | Select-Object FullName
Get-ChildItem C:\Users\123\Projects\sis -Filter "Makefile" | Select-Object FullName
```

Then apply:
```bash
psql $DATABASE_URL -f db/migrations/<NEXT>_add_protected_build.sql
```

- [ ] **Step 4: Verify column exists**

```bash
psql $DATABASE_URL -c "\d strategies" | grep protected
```
Expected output: `protected_build | boolean | not null | false`

- [ ] **Step 5: Commit**

```bash
git add db/migrations/<NEXT>_add_protected_build.sql
git commit -m "feat(db): add protected_build column to strategies"
```

---

### Task 2: Backend types — add `ProtectedBuild` to Strategy struct

**Files:**
- Modify: `pkg/strategy/types.go`

- [ ] **Step 1: Write failing test**

In `pkg/strategy/engine_test.go`, add (or verify it would fail without the field):
```go
func TestStrategyHasProtectedBuild(t *testing.T) {
    s := Strategy{}
    s.ProtectedBuild = true
    if !s.ProtectedBuild {
        t.Fatal("ProtectedBuild field missing or not settable")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestStrategyHasProtectedBuild -v
```
Expected: compile error or FAIL

- [ ] **Step 3: Add field to Strategy struct**

In `pkg/strategy/types.go`, add after `MatrixEntryLevel *MatrixEntryLevel`:
```go
ProtectedBuild bool
```

Full context (lines 111-114 of types.go):
```go
	MatrixLevels          []MatrixLevel
	SafeZonePct           float64
	MatrixEntryLevel      *MatrixEntryLevel
	ProtectedBuild        bool
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestStrategyHasProtectedBuild -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/types.go pkg/strategy/engine_test.go
git commit -m "feat(strategy): add ProtectedBuild field to Strategy struct"
```

---

### Task 3: Backend engine — wire `protected_build` into DB queries and scan

**Files:**
- Modify: `pkg/strategy/engine.go`

The `scanStrategy` and `scanStrategyRow` functions are in `engine.go`. The SELECT queries in `Start()` and `Notify()` must include `protected_build`.

- [ ] **Step 1: Find scanStrategy/scanStrategyRow in engine.go**

Read lines 240–300 of `pkg/strategy/engine.go` to see the exact scan code.

- [ ] **Step 2: Add `protected_build` to SELECT in `Start()`**

Current query (lines 51-59 of engine.go):
```go
`SELECT id, owner_id, account_id, symbol, category, direction, status,
        grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
        leverage, COALESCE(margin_type,'isolated'),
        entry_order_type, COALESCE(steps::text,'[]'),
        COALESCE(signal_configs::text,'[]'),
        cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
        COALESCE(strategy_type,'grid')
 FROM strategies WHERE status IN ('active','finishing')`
```

Add `COALESCE(protected_build,false)` at the end of the SELECT (before `COALESCE(strategy_type,...)`):
```go
`SELECT id, owner_id, account_id, symbol, category, direction, status,
        grid_levels, grid_active, COALESCE(max_stop_active,0), grid_step_pct, grid_size_usdt,
        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, hedge_mode,
        leverage, COALESCE(margin_type,'isolated'),
        entry_order_type, COALESCE(steps::text,'[]'),
        COALESCE(signal_configs::text,'[]'),
        cycle_count, max_cycles, bot_id, COALESCE(matrix_levels::text,''), COALESCE(safe_zone_pct,0), COALESCE(matrix_entry_level::text,''),
        COALESCE(protected_build,false),
        COALESCE(strategy_type,'grid')
 FROM strategies WHERE status IN ('active','finishing')`
```

Same change in `Notify()` (lines 77-84).

- [ ] **Step 3: Add `&s.ProtectedBuild` to scanStrategy**

Find `scanStrategy` / `scanStrategyRow` in engine.go (around line 240+). Add `&s.ProtectedBuild` to the `rows.Scan(...)` / `row.Scan(...)` call, in the same position as added to SELECT.

- [ ] **Step 4: Build to verify**

```bash
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/...
```
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/engine.go
git commit -m "feat(strategy): load protected_build from DB in strategy engine"
```

---

### Task 4: Backend — replace SafeZone state with matrixWaitingSlots in StrategyRunner

**Files:**
- Modify: `pkg/strategy/cycle.go`

- [ ] **Step 1: Replace SZ fields with matrixWaitingSlots**

In `cycle.go`, the "Matrix strategy runtime state" section (lines 51-58) currently has:
```go
// Matrix strategy runtime state
matrixSafeZone       *MatrixSafeZone
matrixMonitorStop    context.CancelFunc
matrixSLSeq          int
matrixSZPendingSlot  *int
matrixSZPendingPrice float64
matrixSZLogTime      time.Time
matrixSZBoundaryLog  time.Time
lastMatrixPrice      float64
```

Replace with:
```go
// Matrix strategy runtime state
matrixMonitorStop    context.CancelFunc
matrixSLSeq          int
matrixWaitingSlots   map[int]bool // positive slot → true when SL'd and waiting to re-enter
lastMatrixPrice      float64
```

Note: `matrixSafeZone`, `matrixSZPendingSlot`, `matrixSZPendingPrice`, `matrixSZLogTime`, `matrixSZBoundaryLog` are REMOVED. `MatrixSafeZone` type in types.go is kept for now (used by `GetMatrixSafeZone` in engine.go; will be updated in Task 5).

- [ ] **Step 2: Build to see compilation errors**

```bash
cd C:\Users\123\Projects\sis && go build ./pkg/strategy/... 2>&1
```

Note all compilation errors — these are the references to removed fields that need updating in matrix.go and engine.go.

- [ ] **Step 3: Commit stub (compile will fail until matrix.go is updated)**

Skip commit — wait for Task 5 to fix compilation errors together.

---

### Task 5: Backend — rework matrix.go re-entry logic (main task)

**Files:**
- Modify: `pkg/strategy/matrix.go`
- Modify: `pkg/strategy/engine.go` (update `GetMatrixSafeZone`)

This is the core task. We:
1. Remove all SafeZone logic
2. Add `matrixWaitingSlots` management
3. Add price-based re-entry check in `matrixPriceTick`
4. Add protected-build check for virtual level triggering
5. Add restore logic for restart recovery

**Key helpers to implement:**

```go
// matrixSlotCovered returns true if the level for slot has at least one stop
// order confirmed (SLOrderID set, whether original or replaced).
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixSlotCovered(slot int) bool {
    for _, l := range sr.levels {
        if l.Slot != nil && *l.Slot == slot && l.Status == LevelFilled {
            return l.SLOrderID != ""
        }
    }
    return false
}

// matrixFindDeepestActiveRef finds the deepest active (filled/placed/pending) level
// with slot > waitingSlot. "Deeper" = higher slot number (further from L0).
// Returns nil if no such level exists.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixFindDeepestActiveRef(waitingSlot int) *GridLevel {
    var best *GridLevel
    for i := range sr.levels {
        l := &sr.levels[i]
        if l.Slot == nil || *l.Slot <= waitingSlot {
            continue
        }
        switch l.Status {
        case LevelFilled, LevelPlaced, LevelPending:
        default:
            continue
        }
        if best == nil || *l.Slot > *best.Slot {
            best = l
        }
    }
    return best
}

// matrixReentryConditionMet returns true if current price has crossed past ref's fill price
// in the favorable direction (price < ref.FilledPrice for SHORT, > for LONG).
func (sr *StrategyRunner) matrixReentryConditionMet(ref *GridLevel, currentPrice float64) bool {
    if ref.FilledPrice <= 0 {
        return false
    }
    if sr.strategy.Direction == DirectionShort {
        return currentPrice < ref.FilledPrice
    }
    return currentPrice > ref.FilledPrice
}
```

**Key logic in `matrixPriceTick` (new section, replaces SafeZone sections 1, 1b, 1c, 1d):**

```go
// 1. Check waiting slots for re-entry
if len(sr.matrixWaitingSlots) > 0 {
    sr.matrixCheckWaitingReentry(ctx, currentPrice)
}
```

**New function `matrixCheckWaitingReentry`:**

```go
// matrixCheckWaitingReentry checks each waiting slot to see if it can re-enter.
// Called from matrixPriceTick. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixCheckWaitingReentry(ctx context.Context, currentPrice float64) {
    // Collect and sort waiting slots descending (deepest = highest slot number first)
    var slots []int
    for s := range sr.matrixWaitingSlots {
        slots = append(slots, s)
    }
    sort.Sort(sort.Reverse(sort.IntSlice(slots)))

    for _, slot := range slots {
        ref := sr.matrixFindDeepestActiveRef(slot)

        if ref == nil {
            // No deeper active level — re-enter at configured price (rebuild from scratch)
            sr.matrixReenterAtConfigPrice(ctx, slot, currentPrice)
            continue
        }

        // Wait for price to pass below (SHORT) or above (LONG) the reference's fill price
        if !sr.matrixReentryConditionMet(ref, currentPrice) {
            continue
        }

        // ProtectedBuild: reference must have stop confirmed
        if sr.strategy.ProtectedBuild && !sr.matrixSlotCovered(*ref.Slot) {
            continue
        }

        sr.matrixReenterRelativeToRef(ctx, slot, ref, currentPrice)
    }
}
```

**New function `matrixReenterRelativeToRef`:**

```go
// matrixReenterRelativeToRef places a new level for waitingSlot at ref.FilledPrice ± one step.
// The step size is taken from the waiting slot's config (PriceStepPct).
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterRelativeToRef(ctx context.Context, waitingSlot int, ref *GridLevel, currentPrice float64) {
    // Look up step% for the waiting slot
    above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
    below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")

    var stepPct float64
    if waitingSlot > 0 {
        idx := waitingSlot - 1
        if idx < len(above) {
            stepPct = above[idx].PriceStepPct
        }
    } else {
        idx := -waitingSlot - 1
        if idx < len(below) {
            stepPct = below[idx].PriceStepPct
        }
    }

    if stepPct == 0 {
        return
    }

    // For SHORT: positive slots go down from entry (stepMul=-1, price_step_pct > 0)
    // New position = ref.FilledPrice * (1 + stepMul * abs(stepPct)/100)
    stepMul := 1.0
    if sr.strategy.Direction == DirectionShort {
        stepMul = -1.0
    }
    newTargetPrice := ref.FilledPrice * (1 + stepMul*math.Abs(stepPct)/100)

    // Compute size from slot config
    var sizeUSDT float64
    if waitingSlot > 0 {
        idx := waitingSlot - 1
        if idx < len(above) {
            sizeUSDT = above[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
        }
    } else {
        idx := -waitingSlot - 1
        if idx < len(below) {
            sizeUSDT = below[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
        }
    }

    if sizeUSDT == 0 {
        return
    }

    qty := trader.FormatQty(sizeUSDT/newTargetPrice, sr.instr.QtyStep, sr.instr.MinQty)
    side := matrixLevelSide(sr.strategy.Direction)

    nextLevelIdx := 0
    for _, l := range sr.levels {
        if l.LevelIdx > nextLevelIdx {
            nextLevelIdx = l.LevelIdx
        }
    }
    nextLevelIdx++

    slotCopy := waitingSlot
    var levelID string
    if err := sr.runner.pool.QueryRow(ctx,
        `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
        sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, newTargetPrice, sizeUSDT, qty, slotCopy,
    ).Scan(&levelID); err != nil {
        log.Printf("strategy %s: matrixReenterRelativeToRef slot=%d: %v", sr.strategy.ID, waitingSlot, err)
        return
    }

    newLevel := GridLevel{
        ID: levelID, LevelIdx: nextLevelIdx, Side: side,
        TargetPrice: newTargetPrice, SizeUSDT: sizeUSDT, Qty: qty,
        Status: LevelPending, Slot: &slotCopy,
    }
    sr.levels = append(sr.levels, newLevel)
    placed := &sr.levels[len(sr.levels)-1]

    delete(sr.matrixWaitingSlots, waitingSlot)

    if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
        sr.errlog(ctx, fmt.Sprintf("Matrix re-entry slot %d relative to ref L%d: %v", waitingSlot, *ref.Slot, err))
    } else {
        sr.info(ctx, fmt.Sprintf("[RE-ENTRY] L%d @ %.4f (ref L%d @ %.4f)", waitingSlot, newTargetPrice, *ref.Slot, ref.FilledPrice))
    }
}
```

**New function `matrixReenterAtConfigPrice`:**

```go
// matrixReenterAtConfigPrice re-enters a waiting slot at its original configured price
// (relative to cycle.StartPrice). Used when no deeper active reference exists.
// Delegates to existing matrixReplaceSlots logic for that specific slot.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterAtConfigPrice(ctx context.Context, waitingSlot int, currentPrice float64) {
    above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
    below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
    prices := calculateMatrixPrices(sr.cycle.StartPrice, above, below, sr.strategy.Direction)

    targetPrice, ok := prices[waitingSlot]
    if !ok {
        return
    }

    var sizeUSDT float64
    if waitingSlot > 0 {
        idx := waitingSlot - 1
        if idx >= len(above) {
            return
        }
        sizeUSDT = above[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
    } else {
        idx := -waitingSlot - 1
        if idx >= len(below) {
            return
        }
        sizeUSDT = below[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
    }

    if sizeUSDT == 0 {
        return
    }

    qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
    side := matrixLevelSide(sr.strategy.Direction)

    nextLevelIdx := 0
    for _, l := range sr.levels {
        if l.LevelIdx > nextLevelIdx {
            nextLevelIdx = l.LevelIdx
        }
    }
    nextLevelIdx++

    slotCopy := waitingSlot
    var levelID string
    if err := sr.runner.pool.QueryRow(ctx,
        `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
        sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, targetPrice, sizeUSDT, qty, slotCopy,
    ).Scan(&levelID); err != nil {
        log.Printf("strategy %s: matrixReenterAtConfigPrice slot=%d: %v", sr.strategy.ID, waitingSlot, err)
        return
    }

    newLevel := GridLevel{
        ID: levelID, LevelIdx: nextLevelIdx, Side: side,
        TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
        Status: LevelPending, Slot: &slotCopy,
    }
    sr.levels = append(sr.levels, newLevel)
    placed := &sr.levels[len(sr.levels)-1]

    delete(sr.matrixWaitingSlots, waitingSlot)

    if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
        sr.errlog(ctx, fmt.Sprintf("Matrix re-entry slot %d at config price: %v", waitingSlot, err))
    } else {
        sr.info(ctx, fmt.Sprintf("[RE-ENTRY] L%d @ %.4f (config price, no deeper ref)", waitingSlot, targetPrice))
    }
}
```

- [ ] **Step 1: Write a test for re-entry condition logic**

In `pkg/strategy/engine_test.go`:
```go
func TestMatrixReentryConditionMet(t *testing.T) {
    sr := &StrategyRunner{strategy: Strategy{Direction: DirectionShort}}
    ref := &GridLevel{FilledPrice: 102.0}
    // SHORT: condition met when price < ref.FilledPrice
    if !sr.matrixReentryConditionMet(ref, 101.0) {
        t.Error("expected condition met for price 101 < ref 102 (SHORT)")
    }
    if sr.matrixReentryConditionMet(ref, 103.0) {
        t.Error("expected condition NOT met for price 103 > ref 102 (SHORT)")
    }

    sr.strategy.Direction = DirectionLong
    if !sr.matrixReentryConditionMet(ref, 103.0) {
        t.Error("expected condition met for price 103 > ref 102 (LONG)")
    }
    if sr.matrixReentryConditionMet(ref, 101.0) {
        t.Error("expected condition NOT met for price 101 < ref 102 (LONG)")
    }
}
```

- [ ] **Step 2: Run test — expect compile error (functions don't exist yet)**

```bash
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -run TestMatrixReentryConditionMet -v 2>&1 | head -20
```

- [ ] **Step 3: Implement all changes in matrix.go**

**3a. In `startMatrixCycle`**: replace SZ reset with waiting slots initialization:
```go
// OLD (remove these 3 lines):
sr.matrixSafeZone = nil
sr.matrixSZPendingSlot = nil
sr.matrixSZPendingPrice = 0

// NEW:
sr.matrixWaitingSlots = make(map[int]bool)
```

**3b. In `handleMatrixSLFill`**: replace SafeZone creation with waiting slot marking:
```go
// REMOVE entire SafeZone creation block:
// if sr.strategy.SafeZonePct > 0 && closed.Slot != nil && *closed.Slot > 0 {
//     ...
//     sr.matrixSafeZone = createMatrixSafeZone(...)
//     ...
// }

// ADD:
if closed.Slot != nil && *closed.Slot > 0 {
    if sr.matrixWaitingSlots == nil {
        sr.matrixWaitingSlots = make(map[int]bool)
    }
    sr.matrixWaitingSlots[*closed.Slot] = true
    sr.info(ctx, fmt.Sprintf("Matrix L%d ожидает перезахода (SL @ %.4f)", *closed.Slot, slTrigger))
}
```

**3c. In `matrixPriceTick`**: replace sections 1, 1b, 1c, 1d with new waiting check:
```go
// Replace everything from "// 1. Check Safe Zone exit" through the heartbeat block
// with:

// 1. Check waiting slots for re-entry
if len(sr.matrixWaitingSlots) > 0 {
    sr.matrixCheckWaitingReentry(ctx, currentPrice)
}
```

**3d. In `matrixPriceTick` section 2** (trigger virtual levels): add ProtectedBuild check:
```go
if crossed {
    // ProtectedBuild: don't trigger next virtual level until previous slot has stop confirmed
    if sr.strategy.ProtectedBuild && l.Slot != nil && *l.Slot > 0 {
        prevSlot := *l.Slot - 1
        if prevSlot > 0 && !sr.matrixSlotCovered(prevSlot) {
            continue // previous slot not covered yet
        }
    }
    sr.matrixTriggerVirtualLevel(ctx, l)
}
```

**3e. Add new functions**: `matrixSlotCovered`, `matrixFindDeepestActiveRef`, `matrixReentryConditionMet`, `matrixCheckWaitingReentry`, `matrixReenterRelativeToRef`, `matrixReenterAtConfigPrice` (all as shown above).

**3f. Remove `restoreMatrixSafeZone`** and add `restoreMatrixWaitingSlots`:
```go
// restoreMatrixWaitingSlots derives the waiting state from sr.levels after a service restart.
// A slot is "waiting" if it has an sl_closed level but no active (pending/placed/filled) level.
// Must be called with sr.mu held.
func (sr *StrategyRunner) restoreMatrixWaitingSlots() {
    activeSlots := map[int]bool{}
    slClosedSlots := map[int]bool{}
    for _, l := range sr.levels {
        if l.Slot == nil || *l.Slot <= 0 {
            continue
        }
        switch l.Status {
        case LevelPending, LevelPlaced, LevelFilled:
            activeSlots[*l.Slot] = true
        case LevelSLClosed:
            slClosedSlots[*l.Slot] = true
        }
    }
    sr.matrixWaitingSlots = make(map[int]bool)
    for slot := range slClosedSlots {
        if !activeSlots[slot] {
            sr.matrixWaitingSlots[slot] = true
            log.Printf("strategy %s: restored waiting slot L%d", sr.strategy.ID, slot)
        }
    }
}
```

**3g. In `loadMatrixCycle`**: replace `sr.restoreMatrixSafeZone(ctx)` with:
```go
sr.mu.Lock()
sr.restoreMatrixWaitingSlots()
sr.mu.Unlock()
```

**3h. Remove `createMatrixSafeZone`** function (no longer needed).

**3i. Remove `handleMatrixSLCancelled`** references to SZ if any... Actually check if `handleMatrixSLCancelled` references SZ — it doesn't, it just re-places the SL. Keep it.

**3j. In `handleMatrixTPFill`**: remove SZ-related resets:
```go
// REMOVE:
sr.matrixSafeZone = nil
sr.matrixSZPendingSlot = nil
sr.matrixSZPendingPrice = 0

// ADD:
sr.matrixWaitingSlots = make(map[int]bool) // clear waiting state — TP closed everything fresh
```

**3k. Update `engine.go` `GetMatrixSafeZone`**: either remove it entirely or return nil always (the API handler may still call it; update the API handler in Task 6 to stop calling it).

Also add `"sort"` to matrix.go imports (needed for `sort.Sort`).

- [ ] **Step 4: Run tests**

```bash
cd C:\Users\123\Projects\sis && go test ./pkg/strategy/... -v 2>&1
```
Expected: TestMatrixReentryConditionMet PASS, others PASS or not affected

- [ ] **Step 5: Build full project**

```bash
cd C:\Users\123\Projects\sis && go build ./... 2>&1
```
Expected: no compilation errors

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/cycle.go pkg/strategy/engine.go pkg/strategy/engine_test.go
git commit -m "feat(matrix): replace SafeZone with depth-ordered re-entry + ProtectedBuild gate"
```

---

### Task 6: API Gateway — add `protected_build` to strategy CRUD

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`

The handler has:
- A struct for reading strategy from DB (lines ~100-118)
- A struct for the response (lines ~230-237)  
- INSERT query (lines ~321-325)
- UPDATE query (lines ~405-409)

- [ ] **Step 1: Add `protected_build` to DB read struct**

Around line 118, after `SafeZonePct float64`:
```go
ProtectedBuild bool `json:"protected_build"`
```

- [ ] **Step 2: Add `protected_build` to SELECT query**

Find the SELECT in the handler (around line 166-168). Add `COALESCE(s.protected_build,false)` to the column list and add `&row.ProtectedBuild` to the Scan call.

- [ ] **Step 3: Add `protected_build` to response struct**

Around line 235-236, after `SafeZonePct float64`:
```go
ProtectedBuild bool `json:"protected_build"`
```
And map `row.ProtectedBuild → response.ProtectedBuild` where the response is built.

- [ ] **Step 4: Add `protected_build` to the request body struct (for create/update)**

Find the request body struct (somewhere around `type createStrategyRequest struct`). Add:
```go
ProtectedBuild bool `json:"protected_build"`
```

- [ ] **Step 5: Add `protected_build` to INSERT query**

Current INSERT (line 323):
```sql
matrix_levels, safe_zone_pct, matrix_entry_level)
```
Change to:
```sql
matrix_levels, safe_zone_pct, matrix_entry_level, protected_build)
```
And add `body.ProtectedBuild` to the VALUES parameters.

- [ ] **Step 6: Add `protected_build` to UPDATE query**

Current UPDATE (line 407):
```sql
safe_zone_pct=$25, matrix_entry_level=($26::text)::jsonb,
  updated_at=NOW()
WHERE id=$27 AND owner_id=$28
```
Change to:
```sql
safe_zone_pct=$25, matrix_entry_level=($26::text)::jsonb,
  protected_build=$27,
  updated_at=NOW()
WHERE id=$28 AND owner_id=$29
```
Adjust parameter numbers accordingly throughout the UPDATE.

- [ ] **Step 7: Remove `GetMatrixSafeZone` usage**

Find where `GetMatrixSafeZone` is called in `strategy_handler.go` (in the strategy state endpoint). Remove or nullify the `safe_zone` field in the response (it was used to show SZ on the chart). The `GetMatrixSafeZone` function in engine.go can be left but return nil always or be removed. For now: just remove the call and the `safe_zone` field from the response, or keep returning nil.

- [ ] **Step 8: Build**

```bash
cd C:\Users\123\Projects\sis && go build ./services/api-gateway/... 2>&1
```
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add services/api-gateway/strategy_handler.go
git commit -m "feat(api): add protected_build to strategy create/update/read"
```

---

### Task 7: Frontend types — add `protected_build` to TypeScript interfaces

**Files:**
- Modify: `frontend/src/types.ts`

- [ ] **Step 1: Find Strategy and StrategyFormData interfaces in types.ts**

Read `frontend/src/types.ts` around the `safe_zone_pct` fields (around line 270-383).

- [ ] **Step 2: Add to Strategy interface**

After `safe_zone_pct?: number | null` (line ~274):
```typescript
protected_build?: boolean | null
```

- [ ] **Step 3: Add to StrategyFormData interface**

After `safe_zone_pct: number` (line ~381):
```typescript
protected_build: boolean
```

- [ ] **Step 4: Verify TypeScript compiles**

```bash
cd C:\Users\123\Projects\sis\frontend && npx tsc --noEmit 2>&1 | head -30
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts
git commit -m "feat(frontend): add protected_build to Strategy and StrategyFormData types"
```

---

### Task 8: Frontend UI — add "Защищённое построение" toggle in StrategyModal

**Files:**
- Modify: `frontend/src/components/strategies/StrategyModal.tsx`

- [ ] **Step 1: Add to `defaultForm()`**

In `defaultForm()` (around line 22-54), after `safe_zone_pct: 1.5`:
```typescript
protected_build: false,
```

- [ ] **Step 2: Add to `strategyToForm()`**

In `strategyToForm()` (around line 56-95), after `safe_zone_pct: s.safe_zone_pct ?? 1.5`:
```typescript
protected_build: s.protected_build ?? false,
```

- [ ] **Step 3: Add toggle to matrix tab UI**

In the matrix tab section (Tab 2, around line 736-741, after the Safe-Zone input):

```tsx
<div>
  <label className={labelCls}>
    🔒 Защищённое построение
    <Tip text="Следующий уровень матрицы выставляется только после подтверждения стопа на предыдущем. Также контролирует перезаход выбитых уровней: уровень восстанавливается только когда у более глубокого уровня подтверждён стоп." />
  </label>
  <Toggle
    options={[
      { label: 'Выкл', value: 'false' },
      { label: '🔒 Вкл', value: 'true' },
    ]}
    value={String(form.protected_build ?? false)}
    onChange={v => patch({ protected_build: v === 'true' })}
    optionColors={{ true: 'bg-amber-700 text-white' }}
  />
</div>
```

Place this block after:
```tsx
<div>
  <label className={labelCls}>Safe-Zone от SL %<Tip text="..." /></label>
  <input type="number" ...safe_zone_pct... />
</div>
```

- [ ] **Step 4: Verify TypeScript compiles**

```bash
cd C:\Users\123\Projects\sis\frontend && npx tsc --noEmit 2>&1 | head -30
```

- [ ] **Step 5: Start dev server and verify toggle appears**

The dev server should already be running (`npm run dev`). Open the browser, create/edit a matrix strategy, go to Tab 2 (Матрица) and verify:
- "🔒 Защищённое построение" toggle appears below Safe-Zone field
- Toggle is off by default
- Toggle can be enabled

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/strategies/StrategyModal.tsx
git commit -m "feat(frontend): add Защищённое построение toggle to matrix strategy settings"
```

---

## Post-Implementation

After all tasks complete:

1. **Notify user**: api-gateway needs restart for backend changes to take effect
2. **Notify user**: frontend dev server auto-reloads

## Список изменений

- **Удалена** логика SafeZone (matrixSafeZone, matrixSZPendingSlot, matrixSZPendingPrice, matrixSZLogTime, matrixSZBoundaryLog, createMatrixSafeZone, restoreMatrixSafeZone)
- **Добавлена** логика ожидания перезахода: matrixWaitingSlots, matrixCheckWaitingReentry, matrixFindDeepestActiveRef, matrixReentryConditionMet, matrixReenterRelativeToRef, matrixReenterAtConfigPrice, restoreMatrixWaitingSlots
- **Добавлен** параметр ProtectedBuild в Strategy и DB
- **Добавлен** переключатель "🔒 Защищённое построение" в настройках матрицы (frontend)
- **Добавлена** проверка protected_build при триггере виртуальных уровней
