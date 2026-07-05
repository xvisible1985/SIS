# Matrix Relative Slots (Novabot renumbering) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `relative_slots` mode to matrix strategies where slots are numbered by relative depth from entry and renumber toward entry after a per-slot SL fires, so the next new slot uses the freed relative depth's config.

**Architecture:** Emergent relative numbering — the relative slot number is the rank of an open slot by depth among currently-open slots; no DB slot mutation. A per-side helper computes rank, next-config-index, and next-slot price. The matrix expansion path and SL-close path branch on `strategy.RelativeSlots`. Frontend gets a toggle and renders relative labels.

**Tech Stack:** Go (pkg/strategy, services/api-gateway), PostgreSQL/TimescaleDB migrations, React/TypeScript frontend.

---

## Reference: spec

Design: `docs/superpowers/specs/2026-07-05-matrix-relative-slots-design.md`. Read it first.

## File Structure

- **Migration** `migrations/079_matrix_relative_slots.sql` — add `strategies.relative_slots`.
- **Config plumbing** (mirror the existing `matrix_rebuild_on_sl` flag exactly):
  - `pkg/strategy/types.go` — `Strategy.RelativeSlots bool`.
  - `pkg/strategy/engine.go` — SELECT column + Scan (two query sites).
  - `services/api-gateway/bot_engine.go` — `botCfgJSON.RelativeSlots` + INSERT/SELECT of `relative_slots`.
  - `services/api-gateway/bots_handler.go` — PatchBot config UPDATE.
  - `services/api-gateway/strategy_handler.go` — ListStrategies output field.
- **Core logic** `pkg/strategy/matrix_relative.go` (new) — pure helpers + relative-mode expansion/SL entry points.
- **Core logic wiring** `pkg/strategy/matrix.go` — branch `handleMatrixSLFill` and the expansion/price-tick path on `RelativeSlots`.
- **Level payload** `services/api-gateway/strategy_handler.go` (GetStrategyState / level serialization) — add `relative_slot`.
- **Frontend** `frontend/src/features/bots/components/MatrixBotForm.tsx` — toggle; `frontend/src/components/terminal/Chart.tsx` — relative labels.
- **Tests** `pkg/strategy/matrix_relative_test.go` (unit, no build tag), `services/api-gateway/matrix_relative_integration_test.go` (`//go:build integration`).

---

## Task 1: DB migration + config flag plumbing

**Files:**
- Create: `migrations/079_matrix_relative_slots.sql`
- Modify: `pkg/strategy/types.go` (near line 118), `pkg/strategy/engine.go` (SELECT sites ~66/116, Scan ~593), `services/api-gateway/bot_engine.go` (~833 struct, ~1017/1039 SQL), `services/api-gateway/bots_handler.go` (~707/717), `services/api-gateway/strategy_handler.go` (~120/179/272)

- [ ] **Step 1: Write the migration**

Create `migrations/079_matrix_relative_slots.sql`:

```sql
-- Novabot-style relative matrix slots: opt-in mode where slots renumber toward
-- entry after a per-slot SL fires. Mutually exclusive with matrix_rebuild_on_sl /
-- matrix_rebuild_from_entry (ignored when relative_slots is true).
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS relative_slots BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 2: Apply migration locally and verify**

Run: `docker exec -i sis-timescaledb-1 psql -U sis -d sis < migrations/079_matrix_relative_slots.sql`
Then: `docker exec sis-timescaledb-1 psql -U sis -d sis -c "SELECT column_name FROM information_schema.columns WHERE table_name='strategies' AND column_name='relative_slots';"`
Expected: one row `relative_slots`.

- [ ] **Step 3: Add the struct field**

In `pkg/strategy/types.go`, after `RebuildFromEntry bool` (line ~118) add:

```go
	RelativeSlots         bool // Novabot: slots renumber toward entry after a per-slot SL; next new slot uses the freed relative depth's config
```

- [ ] **Step 4: Thread through Engine.Start**

In `pkg/strategy/engine.go`, both SELECT lists that contain `COALESCE(matrix_rebuild_on_sl,false)` (~lines 66-67 and 116-117): add `, COALESCE(relative_slots,false)` immediately after the `matrix_rebuild_from_entry` column in each list. In the corresponding `rows.Scan(...)` (the block containing `&s.RebuildOnSL,` ~line 593), add `&s.RelativeSlots,` immediately after `&s.RebuildFromEntry,`. Keep column order identical between SELECT and Scan in BOTH query sites.

- [ ] **Step 5: Thread through bot config JSON**

In `services/api-gateway/bot_engine.go`: add to `botCfgJSON` (near `MatrixRebuildOnSL`, ~833):

```go
	RelativeSlots         bool            `json:"relative_slots"`
```

In the INSERT column list (~1017) add `relative_slots` and a new `$N` placeholder in the VALUES, and in the args (~1039) add `cfg.RelativeSlots` in the same position. (Match the existing `matrix_rebuild_from_entry` placement.)

- [ ] **Step 6: Thread through PatchBot + ListStrategies**

In `services/api-gateway/bots_handler.go` PatchBot config UPDATE (~707-717): add `relative_slots = $N` and pass `cfg.RelativeSlots`.
In `services/api-gateway/strategy_handler.go`: add `RelativeSlots bool \`json:"relative_slots"\`` to both the response struct (~120 and ~272) and `COALESCE(s.relative_slots,false)` to the SELECT (~179), plus the matching Scan target.

- [ ] **Step 7: Build**

Run: `go build ./...`
Expected: no output (success).

- [ ] **Step 8: Commit**

```bash
git add migrations/079_matrix_relative_slots.sql pkg/strategy/types.go pkg/strategy/engine.go services/api-gateway/bot_engine.go services/api-gateway/bots_handler.go services/api-gateway/strategy_handler.go
git commit -m "feat(matrix): add relative_slots config flag plumbing"
```

---

## Task 2: Pure relative-slot helpers (TDD)

**Files:**
- Create: `pkg/strategy/matrix_relative.go`, `pkg/strategy/matrix_relative_test.go`

Concepts: an "open" accumulation slot is a `GridLevel` with `Status == LevelFilled` whose
`Slot != nil` and (for the below/hedge side) `*Slot < 0` — or `> 0` for the above side.
Depth = `abs(*Slot's price distance)`; we rank by target/fill price distance from entry.

- [ ] **Step 1: Write failing tests**

Create `pkg/strategy/matrix_relative_test.go`:

```go
package strategy

import (
	"math"
	"testing"
)

func ptrI(v int) *int { return &v }

// openSlot is the minimal shape the helpers need.
func lvl(slot int, price float64, status LevelStatus) GridLevel {
	s := slot
	return GridLevel{Slot: &s, TargetPrice: price, FilledPrice: price, Status: status}
}

func TestRelativeRanks_BelowSide(t *testing.T) {
	entry := 100.0
	// filled slots at descending prices (below side, e.g. long-hedge accumulation)
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled),
		lvl(-2, 95, LevelFilled),
		lvl(-3, 90, LevelPending), // not filled — ignored
	}
	got := relativeRanks(levels, entry, "below")
	// closest to entry (98) → rank 1; next (95) → rank 2
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("ranks = %v, want [1 2 ...]", got)
	}
}

func TestNextConfigIndex_RecycleAfterClose(t *testing.T) {
	entry := 100.0
	// -1 was SL-closed, -2 still filled → 1 open → next index = 2
	levels := []GridLevel{
		lvl(-1, 98, LevelSLClosed),
		lvl(-2, 95, LevelFilled),
	}
	if idx := nextConfigIndex(levels, "below", 5); idx != 2 {
		t.Fatalf("nextConfigIndex = %d, want 2", idx)
	}
}

func TestNextConfigIndex_CapAtN(t *testing.T) {
	entry := 100.0
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled), lvl(-2, 95, LevelFilled),
		lvl(-3, 90, LevelFilled),
	}
	if idx := nextConfigIndex(levels, "below", 3); idx != 0 {
		t.Fatalf("nextConfigIndex at cap = %d, want 0 (no expansion)", idx)
	}
	_ = entry
}

func TestNextSlotPrice_FromDeepestOpen(t *testing.T) {
	entry := 100.0
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled),
		lvl(-2, 95, LevelFilled),
	}
	// next below step -3% → deepest open (95) * (1 - 0.03) = 92.15
	got := nextSlotPrice(levels, entry, "below", -3.0)
	if math.Abs(got-92.15) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 92.15", got)
	}
}

func TestNextSlotPrice_NoOpen_FromEntry(t *testing.T) {
	got := nextSlotPrice(nil, 100.0, "below", -3.0)
	if math.Abs(got-97.0) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 97.0", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/strategy/ -run "TestRelativeRanks|TestNextConfigIndex|TestNextSlotPrice" 2>&1 | tail`
Expected: build failure — `undefined: relativeRanks`, etc.

- [ ] **Step 3: Implement the helpers**

Create `pkg/strategy/matrix_relative.go`:

```go
package strategy

// side is "below" (accumulation deeper from entry, price moves away in hedge
// direction) or "above" (counter side). We rank by price distance from entry.

// openAccumLevels returns filled accumulation levels for the given side, sorted by
// distance from entry (closest first). L(0) (Slot==0) is the anchor, excluded.
func openAccumLevels(levels []GridLevel, entry float64, side string) []*GridLevel {
	var out []*GridLevel
	for i := range levels {
		l := &levels[i]
		if l.Slot == nil || *l.Slot == 0 || l.Status != LevelFilled {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		out = append(out, l)
	}
	// sort by |price - entry| ascending (closest to entry first)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			di := absf(out[j].FilledPrice - entry)
			dj := absf(out[j-1].FilledPrice - entry)
			if di < dj {
				out[j], out[j-1] = out[j-1], out[j]
			} else {
				break
			}
		}
	}
	return out
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// relativeRanks returns the relative rank (1-based, closest to entry = 1) for each
// FILLED accumulation level of the side, in the same order as the input slice.
// Non-open or wrong-side entries get rank 0.
func relativeRanks(levels []GridLevel, entry float64, side string) []int {
	open := openAccumLevels(levels, entry, side)
	rankOf := make(map[*GridLevel]int, len(open))
	for i, l := range open {
		rankOf[l] = i + 1
	}
	out := make([]int, len(levels))
	for i := range levels {
		out[i] = rankOf[&levels[i]]
	}
	return out
}

// nextConfigIndex returns the 1-based config index for the next new slot on the side
// = (count of currently-open slots on that side) + 1, or 0 if the concurrency cap n
// is reached (no further expansion).
func nextConfigIndex(levels []GridLevel, side string, n int) int {
	// entry does not affect the count; pass 0.
	count := len(openAccumLevels(levels, 0, side))
	next := count + 1
	if next > n {
		return 0
	}
	return next
}

// nextSlotPrice returns the price for the next new slot: the deepest currently-open
// slot's fill price stepped by stepPct (%), or the entry price stepped by stepPct if
// no slots are open. stepPct is signed (e.g. -3.0 for 3% below).
func nextSlotPrice(levels []GridLevel, entry float64, side string, stepPct float64) float64 {
	open := openAccumLevels(levels, entry, side)
	base := entry
	if len(open) > 0 {
		base = open[len(open)-1].FilledPrice // deepest = last after sort
	}
	return base * (1 + stepPct/100)
}
```

Note: `LevelSLClosed`, `LevelFilled`, `LevelPending`, `GridLevel` already exist in `pkg/strategy`. If `openAccumLevels`'s `entry` param is unused in `nextConfigIndex`, that's fine — it passes 0.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/strategy/ -run "TestRelativeRanks|TestNextConfigIndex|TestNextSlotPrice" -v 2>&1 | tail -20`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/matrix_relative.go pkg/strategy/matrix_relative_test.go
git commit -m "feat(matrix): pure relative-slot helpers (rank, next index, next price)"
```

---

## Task 3: Relative-mode SL-close handling

**Files:**
- Modify: `pkg/strategy/matrix.go` (`handleMatrixSLFill`, ~1544-1653)

Goal: when `sr.strategy.RelativeSlots` is true, on a per-slot SL fill, mark `sl_closed`
+ `realized_pnl` (unchanged) but SKIP the absolute re-entry paths (`matrixWaitingSlots`,
`matrixRebuildFromSZLow`, `RebuildFromEntry`). The reduced open count makes the next
relative slot recompute naturally on the next price tick.

- [ ] **Step 1: Add the branch**

In `pkg/strategy/matrix.go`, inside `handleMatrixSLFill`, immediately after the block
that sets `closed.Status = LevelSLClosed`, `closed.SLOrderID = ""` and logs
`"Matrix SL сработал ..."` (right before the `if closed.Slot != nil {` block ~line 1588),
insert:

```go
	// Relative-slots mode: the slot is closed for good. No absolute re-entry / rebuild —
	// the drop in open-slot count makes the next relative slot recompute on the next
	// price tick (see matrixPriceTick relative branch). Other slots' SLs are untouched.
	if sr.strategy.RelativeSlots {
		sr.matrixUpdateTP(ctx) // recompute global TP from the new average entry
		return
	}
```

- [ ] **Step 2: Build**

Run: `go build ./pkg/strategy/...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add pkg/strategy/matrix.go
git commit -m "feat(matrix): relative-slots SL close skips absolute re-entry"
```

---

## Task 4: Relative-mode expansion (place next slot by relative depth)

**Files:**
- Modify: `pkg/strategy/matrix.go` (`matrixPriceTick`, ~868), `pkg/strategy/matrix_relative.go`

Goal: in relative mode, on each price tick, if there is no pending/placed next slot and
price has reached the next relative slot's target, place it using `config[next_rank]`.
Reuse existing placement (`placeMatrixLevel`) and config lookup.

- [ ] **Step 1: Add a relative-expansion method**

Append to `pkg/strategy/matrix_relative.go`:

```go
import (
	"context"
	"fmt"
)

// matrixRelativeExpand places the next relative accumulation slot when price reaches its
// target. Must be called with sr.mu held. Side is the accumulation side for this
// strategy's direction ("below" for long-hedge accumulation, mirrored for short).
func (sr *StrategyRunner) matrixRelativeExpand(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil {
		return
	}
	side := matrixAccumSide(sr.strategy.Direction)
	entry := sr.matrixEntryPrice()
	below := filterMatrixLevels(sr.strategy.MatrixLevels, side)
	n := len(below)
	if n == 0 {
		return
	}
	// Skip if a next slot is already pending/placed (only expand one at a time).
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot != 0 && (l.Status == LevelPending || l.Status == LevelPlaced) {
			return
		}
	}
	idx := nextConfigIndex(sr.levels, side, n)
	if idx == 0 {
		return // cap reached
	}
	cfg := below[idx-1]
	target := nextSlotPrice(sr.levels, entry, side, cfg.PriceStepPct)
	// Trigger when price has moved to/through the target in the accumulation direction.
	reached := (side == "below" && currentPrice <= target) || (side == "above" && currentPrice >= target)
	if !reached {
		return
	}
	sr.info(ctx, fmt.Sprintf("[REL] расширение: слот L(-%d) @ %.4f (шаг %.2f%% от глубины)", idx, target, cfg.PriceStepPct))
	sr.matrixPlaceRelativeSlot(ctx, side, idx, target, cfg, currentPrice)
}
```

Note: `matrixAccumSide`, `matrixEntryPrice`, and `matrixPlaceRelativeSlot` are defined in
Step 2. `filterMatrixLevels` and `MatrixLevel.PriceStepPct` already exist (see `matrix.go`
`matrixReplaceSlots` and the `MatrixLevel` struct).

- [ ] **Step 2: Add the placement + small accessors**

Append to `pkg/strategy/matrix_relative.go`:

```go
// matrixAccumSide returns the config side used for accumulation for the direction.
// Long hedge accumulates as price falls ("below"); short as price rises ("above").
func matrixAccumSide(dir Direction) string {
	if dir == DirectionShort {
		return "above"
	}
	return "below"
}

// matrixEntryPrice returns the L(0) fill price, or cycle start price as a fallback.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixEntryPrice() float64 {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot == 0 && l.FilledPrice > 0 {
			return l.FilledPrice
		}
	}
	if sr.cycle != nil {
		return sr.cycle.StartPrice
	}
	return 0
}

// matrixPlaceRelativeSlot inserts a new accumulation level for relative index idx and
// places its order using the existing matrix placement path. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlaceRelativeSlot(ctx context.Context, side string, idx int, target float64, cfg MatrixLevel, currentPrice float64) {
	slot := idx
	if side == "below" {
		slot = -idx
	}
	sizeUSDT := cfg.SizePct / 100 * sr.effectiveDeposit(currentPrice)
	qty := ""
	if target > 0 {
		qty = trader.FormatQty(sizeUSDT/target, sr.instr.QtyStep, sr.instr.MinQty)
	}
	side2 := matrixLevelSide(sr.strategy.Direction)
	levelIdx := sr.matrixNextLevelIdx()
	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, levelIdx, side2, target, sizeUSDT, qty, slot,
	).Scan(&levelID); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL] insert slot L(%d): %v", slot, err))
		return
	}
	s := slot
	sr.levels = append(sr.levels, GridLevel{
		ID: levelID, LevelIdx: levelIdx, Side: side2,
		TargetPrice: target, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &s,
	})
	placed := &sr.levels[len(sr.levels)-1]
	if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REL] place slot L(%d): %v", slot, err))
	}
}

// matrixNextLevelIdx returns max(existing level_idx)+1 to avoid linkId collisions.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixNextLevelIdx() int {
	next := 0
	for i := range sr.levels {
		if sr.levels[i].LevelIdx > next {
			next = sr.levels[i].LevelIdx
		}
	}
	return next + 1
}
```

Add the required imports to the file's import block: `"sis/pkg/trader"` (for `trader.FormatQty`). Verify `effectiveDeposit`, `matrixLevelSide`, `placeMatrixLevel`, `sr.instr.QtyStep/MinQty`, and `MatrixLevel.SizePct/PriceStepPct` exist by grepping `matrix.go` before relying on them.

- [ ] **Step 3: Call the relative expander from matrixPriceTick**

In `pkg/strategy/matrix.go`, at the very top of `matrixPriceTick` (after acquiring state,
before the existing absolute logic ~line 875), add:

```go
	if sr.strategy.RelativeSlots {
		sr.matrixRelativeExpand(ctx, currentPrice)
		return
	}
```

Confirm `matrixPriceTick` holds `sr.mu` at that point (it is called from the price
monitor with the lock — verify by reading the function header and its callers).

- [ ] **Step 4: Build**

Run: `go build ./pkg/strategy/...`
Expected: no output. Fix any missing-identifier errors by grepping the real signatures in `matrix.go` and adjusting.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/matrix_relative.go
git commit -m "feat(matrix): relative-mode progressive slot expansion"
```

---

## Task 5: Expose relative_slot in level state + Chart labels

**Files:**
- Modify: `services/api-gateway/strategy_handler.go` (GetStrategyState level serialization), `frontend/src/components/terminal/Chart.tsx`

- [ ] **Step 1: Find the level-state serializer**

Run: `grep -n "func (s \*Server) GetStrategyState" services/api-gateway/strategy_handler.go`
Read the level loop; identify where each level's `slot` is emitted to JSON.

- [ ] **Step 2: Add relative_slot to the payload**

In the level serialization, compute relative ranks once per strategy using the same
depth ordering as `relativeRanks` (closest filled accumulation slot to entry = 1) and
emit `"relative_slot": -rank` for filled accumulation levels (0/omitted otherwise). Since
the handler is in package `main`, replicate the tiny ordering inline (sort filled non-zero
slots by |price-entry|, assign 1..k) rather than importing the strategy helper.

Concretely, after loading the levels for the active cycle, add:

```go
	// Relative slot labels (Novabot mode): rank filled accumulation slots by distance
	// from entry (closest = 1). Only meaningful when the strategy has relative_slots on,
	// but harmless to always compute; the frontend uses it only in that mode.
	type rl struct{ idx int; dist float64 }
	entryPrice := 0.0
	for _, l := range levels {
		if l.Slot != nil && *l.Slot == 0 && l.FilledPrice > 0 { entryPrice = l.FilledPrice }
	}
	var acc []rl
	for i, l := range levels {
		if l.Slot != nil && *l.Slot != 0 && l.Status == "filled" && l.FilledPrice > 0 {
			acc = append(acc, rl{i, mathAbs(l.FilledPrice - entryPrice)})
		}
	}
	sort.Slice(acc, func(a, b int) bool { return acc[a].dist < acc[b].dist })
	relBySlice := make(map[int]int, len(acc))
	for r, a := range acc { relBySlice[a.idx] = r + 1 }
```

Then include `relBySlice[i]` (negated, or 0) as `relative_slot` in each level's JSON.
Add a small `mathAbs` helper if not present, and `"sort"` to imports.

- [ ] **Step 3: Render relative labels in Chart**

Run: `grep -n "slot\|L(\|Slot" frontend/src/components/terminal/Chart.tsx | head`
In the label-building code for matrix slot lines, when the strategy is in relative mode
(pass a `relativeSlots?: boolean` prop from the parent that has the strategy) and a level
has `relative_slot`, render `L(${relative_slot})`; otherwise keep the current
absolute-slot label. Thread `relativeSlots` from the strategy object down to `Chart`.

- [ ] **Step 4: Typecheck + build**

Run: `cd frontend && npx tsc --noEmit` (expect exit 0), then `cd .. && go build ./...` (expect no output).

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/strategy_handler.go frontend/src/components/terminal/Chart.tsx
git commit -m "feat(matrix): expose relative_slot and render relative chart labels"
```

---

## Task 6: Frontend toggle in matrix strategy form

**Files:**
- Modify: `frontend/src/features/bots/components/MatrixBotForm.tsx` (and the strategy matrix config form if separate)

- [ ] **Step 1: Find the matrix flag toggles**

Run: `grep -n "rebuild_on_sl\|rebuildOnSl\|matrix_rebuild\|protected_build\|Toggle\|checkbox" frontend/src/features/bots/components/MatrixBotForm.tsx | head`
Locate where `matrix_rebuild_on_sl` / `matrix_rebuild_from_entry` toggles are rendered and stored in the config object.

- [ ] **Step 2: Add the relative_slots toggle**

Add a toggle labelled "Относительные слоты (Novabot)" bound to `strategy_config.relative_slots`, mirroring the existing rebuild toggles. When it is on, disable/grey the `matrix_rebuild_on_sl` and `matrix_rebuild_from_entry` toggles (mutually exclusive) with a hint: "Взаимоисключимо с перестройкой сетки".

- [ ] **Step 3: Typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: exit 0.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/bots/components/MatrixBotForm.tsx
git commit -m "feat(matrix): relative_slots toggle in matrix form"
```

---

## Task 7: Integration test (matrix engine, real DB)

**Files:**
- Create: `services/api-gateway/matrix_relative_integration_test.go`

This is a DB-backed test (no exchange). It seeds a strategy + cycle + levels and asserts
the relative-slot serialization and that an SL close does not schedule an absolute re-entry.
It does NOT place exchange orders.

- [ ] **Step 1: Write the failing test**

Create `services/api-gateway/matrix_relative_integration_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Seeds a matrix strategy with L(0), L(-1), L(-2) filled and asserts GetStrategyState
// reports relative_slot -1 for the closest and -2 for the next.
func TestRelativeSlotLabels(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "relslots")
	accID := createTestAccount(t, s, userID)

	var stratID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, relative_slots)
		 VALUES ($1,$2,'RSUSDT','long','matrix',true) RETURNING id`, userID, accID).Scan(&stratID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)

	ins := func(slot int, price float64, status string) {
		s.pool.Exec(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot, filled_price)
			 VALUES ($1,$2,$3,'Buy',$4,10,'1',$5,$6,$4)`,
			stratID, cycleID, slot+10, price, status, slot)
	}
	ins(0, 100, "filled")
	ins(-1, 98, "filled")
	ins(-2, 95, "filled")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/state", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	s.GetStrategyState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetStrategyState: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Levels []struct {
			Slot         *int `json:"slot"`
			RelativeSlot int  `json:"relative_slot"`
		} `json:"levels"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	got := map[int]int{}
	for _, l := range resp.Levels {
		if l.Slot != nil {
			got[*l.Slot] = l.RelativeSlot
		}
	}
	if got[-1] != -1 || got[-2] != -2 {
		t.Fatalf("relative slots = %v, want -1:-1 -2:-2", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test -tags=integration -run TestRelativeSlotLabels ./services/api-gateway/... -v 2>&1 | tail -20`
Expected: FAIL (relative_slot not yet emitted / field 0) until Task 5 is implemented; if Task 5 is done, PASS. If the state payload field name differs, align the test to the real JSON.

- [ ] **Step 3: Make it pass**

Ensure Task 5 emits `relative_slot`. Adjust field names to match the real `GetStrategyState` JSON (inspect its response struct). Re-run until PASS.

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/matrix_relative_integration_test.go
git commit -m "test(matrix): relative-slot label integration test"
```

---

## Task 8: Full verification

- [ ] **Step 1: Build + vet + unit tests**

Run:
```bash
go build ./... && go vet ./pkg/strategy/... ./services/api-gateway/... && go test ./pkg/strategy/ ./pkg/auth/ ./pkg/crypto/
```
Expected: builds clean, vet clean, unit tests pass.

- [ ] **Step 2: Integration tests**

Run: `go test -tags=integration -run "TestRelative" ./services/api-gateway/... 2>&1 | tail`
Expected: PASS.

- [ ] **Step 3: Frontend typecheck**

Run: `cd frontend && npx tsc --noEmit`
Expected: exit 0.

- [ ] **Step 4: Report**

State that api-gateway needs rebuild + restart, migration 079 runs on prod via `deploy.sh`, and `relative_slots` defaults to false (existing strategies unaffected).

---

## Self-review notes

- **Spec coverage:** config flag (Task 1), helpers/rank/next-index/price (Task 2), SL-close renumbering (Task 3), progressive expansion priced from deepest open / entry (Task 4), display relative labels (Task 5), toggle + hedge-bot applicability via config (Task 6, and hedge bot needs no engine change), tests (Tasks 2/7/8). Edge cases (cap, no-open→entry base, both sides via `matrixAccumSide`) covered in Task 2/4.
- **Open verification points for the implementer** (grep before relying): exact `matrixPriceTick` lock/caller contract; `GetStrategyState` JSON field names; `MatrixLevel` field names (`SizePct`, `PriceStepPct`); `effectiveDeposit`, `placeMatrixLevel`, `matrixLevelSide`, `sr.instr` fields. These are existing symbols in `matrix.go`; adjust call sites to their real signatures.
- **Above-side (short hedge):** handled via `matrixAccumSide`; the price-reached comparison flips (`>=`). If short-hedge accumulation is out of first-pass scope, ship long-side first and mirror after.
