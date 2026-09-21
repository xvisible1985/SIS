# DCA/Matrix Level Signal Gating Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the already-existing `UseSignal` per-level flag (grid steps, matrix levels, matrix entry level) actually gate order placement: a flagged level's pending order only goes live on the exchange while the strategy's configured signal agrees with its direction, and gets cancelled/withdrawn if the signal later drops — instead of being a UI toggle with zero backend effect, as it is today.

**Architecture:** Both strategy types already have a continuous "virtual level" price-tick monitor (`gridVirtualPriceTick`, `matrixPriceTick`/`matrixTriggerVirtualLevel`/`matrixPlaceRelativeVirtualOrder`) that re-evaluates pending levels against live price every tick and calls `PlaceOrder` once a price trigger is crossed. We extend these existing monitors — no new ticker, no new subscription — with a synchronous `signal.Engine.QueryState(...)` check at the exact point each already decides to place an order, plus a new shared pass that cancels an already-placed-but-unfilled signal-gated order once the signal drops. A `UseSignal=true` level is also forced into "virtual" status (software-monitored, never a blind resting order placed unconditionally at cycle start).

**Tech Stack:** Go (`pkg/strategy`, `pkg/signal`), Postgres migration, React/TypeScript (`frontend/src/components/terminal/Chart.tsx`).

**Full design context:** `docs/superpowers/specs/2026-09-21-dca-level-signal-gating-design.md`

---

## Task 1: Migration — `use_signal` column on `strategy_levels`

**Files:**
- Create: `migrations/096_strategy_levels_use_signal.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/096_strategy_levels_use_signal.sql
-- Persists, per placed/pending level row, whether this level's order is signal-gated —
-- mirrors force_virtual (added earlier for the same "should this level be software-monitored
-- instead of a blind resting order" question). A level created before this feature existed
-- defaults to false (unchanged, unconditional placement), matching every level's actual
-- historical behavior.
ALTER TABLE strategy_levels ADD COLUMN IF NOT EXISTS use_signal BOOLEAN NOT NULL DEFAULT false;
```

- [ ] **Step 2: Apply it to the local dev Postgres**

Find how prior migrations in this repo get applied (check `pkg/db`'s migration runner, or
look for a `go run ./cmd/migrate` style command already used earlier in this project's
history — grep `migrations/` usage in `pkg/db/*.go` if unsure) and apply this migration the
same way. Verify with:

```bash
docker exec sis-timescaledb-1 psql -U sis -d sis -c "\d strategy_levels" 
```

Expected: a `use_signal | boolean | not null | false` row appears.

- [ ] **Step 3: Commit**

```bash
git add migrations/096_strategy_levels_use_signal.sql
git commit -m "feat(db): add use_signal column to strategy_levels"
```

---

## Task 2: `GridLevel.UseSignal` field + level loader

**Files:**
- Modify: `pkg/strategy/types.go`
- Modify: `pkg/strategy/cycle.go` (the level loader inside `loadActiveCycle`, currently at
  line ~1469-1494 — confirm the exact current line range by reading the file live, it may
  have shifted)

- [ ] **Step 1: Add the field**

In `pkg/strategy/types.go`, find the `GridLevel` struct (currently ~line 170):

```go
type GridLevel struct {
	ID              string
	LevelIdx        int
	Side            string
	TargetPrice     float64
	SizeUSDT        float64
	Qty             string
	Status          LevelStatus
	ExchangeOrderID string
	ExchangeLinkID  string
	FilledPrice     float64
	// Matrix-only fields (nil/zero for grid strategies)
	SLOrderID    string
	SLPrice      float64
	SLReplaced   bool
	Slot         *int      // nil = grid; matrix slot index: -N…0…+N
	ForceVirtual bool      // set at runtime when exchange rejected placement (e.g. 110007)
	PlacedAt     time.Time // in-memory: when order was last placed (for interference detection)
}
```

Add a new field, matching `ForceVirtual`'s placement/style:

```go
	ForceVirtual bool      // set at runtime when exchange rejected placement (e.g. 110007)
	UseSignal    bool      // level is signal-gated — snapshot of the config's use_signal at creation time
	PlacedAt     time.Time // in-memory: when order was last placed (for interference detection)
```

- [ ] **Step 2: Load it from DB**

In `pkg/strategy/cycle.go`, find the level-loading query inside `loadActiveCycle` (search for
`FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx ASC` — currently ~line 1469-1494):

```go
	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(exchange_order_id,''), COALESCE(filled_price,0), COALESCE(exchange_link_id,''),
		        COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), slot, COALESCE(force_virtual,false)
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
		var slotVal *int16
		if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
			&l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice, &l.ExchangeLinkID,
			&l.SLOrderID, &l.SLPrice, &l.SLReplaced, &slotVal, &l.ForceVirtual); err != nil {
			continue
		}
```

Change to select and scan `use_signal` too:

```go
	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(exchange_order_id,''), COALESCE(filled_price,0), COALESCE(exchange_link_id,''),
		        COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), slot, COALESCE(force_virtual,false),
		        use_signal
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
		var slotVal *int16
		if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
			&l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice, &l.ExchangeLinkID,
			&l.SLOrderID, &l.SLPrice, &l.SLReplaced, &slotVal, &l.ForceVirtual,
			&l.UseSignal); err != nil {
			continue
		}
```

(`use_signal` has a DB-level `NOT NULL DEFAULT false`, so no `COALESCE` is needed — a plain
`bool` scan target is fine, mirroring how `sl_replaced` is NOT NULL in that same table and
scans directly without COALESCE either... actually check: `sl_replaced` above IS wrapped in
COALESCE. If reading the live schema shows `use_signal` truly has no possibility of NULL
because of the `NOT NULL` constraint from Task 1, a bare `use_signal` column reference without
COALESCE is correct and simpler — this is a deliberate, minor style choice, not a bug; keep it
as shown here unless you find a concrete reason columns in this exact query are COALESCE'd
even when NOT NULL, in which case match that existing convention instead.)

- [ ] **Step 3: Build to verify it compiles**

Run: `go build ./...`
Expected: exits 0. (This task alone has no behavior to test yet — `UseSignal` is loaded but
not read anywhere. Task 4 onward add the actual gating logic and its tests.)

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/types.go pkg/strategy/cycle.go
git commit -m "feat(strategy): load use_signal into GridLevel"
```

---

## Task 3: Shared signal-gate helper

**Files:**
- Create: `pkg/strategy/signal_gate.go`
- Test: `pkg/strategy/signal_gate_test.go`

This is the one new piece of decision logic every other task builds on: "does the strategy's
own configured signal currently agree with its direction?" — reusing the exact same
`SignalConfigs`/timeframe-resolution convention `awaitSignal` already uses (see
`pkg/strategy/cycle.go:3215`, specifically how it resolves `tf` from
`configs[0].Params["tf"]` with a `"1h"` fallback, and calls
`sr.runner.resolveSignalConfigs(configs)` before touching `pkg/signal`).

- [ ] **Step 1: Write the failing tests**

```go
// pkg/strategy/signal_gate_test.go
package strategy

import (
	"testing"

	"sis/pkg/signal"
)

// TestSignalGateAllows_NoSignalEngine_AllowsByDefault mirrors awaitSignal's own fallback
// (cycle.go: "if signalEngine == nil || len(configs) == 0 { ...start unconditionally... }")
// — a UseSignal=true level must never block a bot's trading indefinitely just because the
// signal engine isn't wired up in this test/runtime context.
func TestSignalGateAllows_NoSignalEngine_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: []SignalConfig{{Name: "rsi-os"}},
		},
		runner: &AccountRunner{}, // signalEngine left nil
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — no signal engine must fall back to allow")
	}
}

// TestSignalGateAllows_NoConfigs_AllowsByDefault is the other half of the same fallback —
// SignalConfigs empty even though a signal engine exists.
func TestSignalGateAllows_NoConfigs_AllowsByDefault(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
			// SignalConfigs deliberately empty
		},
		runner: &AccountRunner{signalEngine: &signal.Engine{}},
	}
	if !sr.signalGateAllows() {
		t.Error("signalGateAllows() = false, want true — empty SignalConfigs must fall back to allow")
	}
}
```

(These two tests only exercise the fallback branch — they don't need a working signal engine
or a real subscription. A third case, "engine present + configs present + state actually
queried", is exercised indirectly by Task 5's/Task 8's gating tests once there's a
`PlaceOrder`/`CancelOrder` call whose presence or absence proves the gate's real decision —
don't try to unit-test `signal.Engine.QueryState`'s own correctness here, that belongs to
`pkg/signal`'s own test suite and is out of scope for this plan.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/strategy/ -run TestSignalGateAllows -v`
Expected: FAIL to compile — `undefined: (*StrategyRunner).signalGateAllows` (and check
`AccountRunner.signalEngine`'s exact field name/visibility by reading `pkg/strategy/engine.go`
live first — if it's unexported and this test file is in the same package `strategy`, direct
field access like `&AccountRunner{signalEngine: ...}` is fine; if the actual field is named
differently, adjust the test to match the real name).

- [ ] **Step 3: Write the implementation**

```go
// pkg/strategy/signal_gate.go
package strategy

import "sis/pkg/signal"

// signalGateAllows reports whether the strategy's own configured signal (SignalConfigs)
// currently agrees with its trading direction — the shared decision every UseSignal=true
// level's placement/cancellation logic consults. Mirrors awaitSignal's (cycle.go)
// no-engine/no-configs fallback exactly: a UseSignal level must never block trading
// indefinitely just because nothing is configured to evaluate.
//
// Unlike awaitSignal (a one-shot Subscribe that fires once and unsubscribes), this is a
// synchronous, repeatable query — signal.Engine.QueryState is a cheap cached lookup (falls
// back to a fresh compute only if no subscription unit exists yet for this exact
// symbol/interval/config hash), safe to call on every price tick.
//
// Must be called with sr.mu held (same requirement as its callers in the price-tick paths).
func (sr *StrategyRunner) signalGateAllows() bool {
	configs := sr.strategy.SignalConfigs
	signalEngine := sr.runner.signalEngine
	if signalEngine == nil || len(configs) == 0 {
		return true
	}

	sigConfigs := sr.runner.resolveSignalConfigs(configs)

	tf := "1h"
	if v, ok := configs[0].Params["tf"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			tf = s
		}
	}

	state, ok := signalEngine.QueryState(sr.strategy.Symbol, tf, sigConfigs)
	if !ok {
		return false
	}

	var want signal.State
	switch sr.strategy.Direction {
	case DirectionLong:
		want = signal.Buy
	case DirectionShort:
		want = signal.Sell
	default: // both — accept either non-neutral direction, matching awaitSignal's own "both" handling
		return state != signal.Neutral
	}
	return state == want
}
```

(Read `pkg/strategy/engine.go`'s `AccountRunner` struct and `awaitSignal`'s exact current body
live before writing this — the plan text above is verbatim-accurate as of when this plan was
written, but confirm the `signalEngine` field name and `resolveSignalConfigs` signature
haven't shifted.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/strategy/ -run TestSignalGateAllows -v`
Expected: `PASS` for both tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/signal_gate.go pkg/strategy/signal_gate_test.go
git commit -m "feat(strategy): add signalGateAllows shared signal-check helper"
```

---

## Task 4: Grid — force-virtual + persist `UseSignal` at level creation

**Files:**
- Modify: `pkg/strategy/cycle.go` (two grid level-creation sites)
- Test: extend an existing grid-cycle-start test file, or add a small new one — see Step 1.

Grid currently computes, at TWO separate call sites (initial cycle start, and "reprice from
fills" dynamic level creation), whether a step is software-monitored ("virtual") instead of a
blind immediately-placed resting order:

```go
forceVirtual := step.OrderType == "virtual" || step.PriceMovePct > 0
```

A `UseSignal=true` step must ALSO always be virtual — it can never be a blind resting order,
since nothing would ever re-check the signal on it otherwise.

- [ ] **Step 1: Write the failing test**

Find the existing grid cycle-start test(s) that assert on `ForceVirtual` for a constructed
level (grep `ForceVirtual` in `pkg/strategy/*_test.go` for an example of the established
assertion style before writing this). Add a new test in the same file/style:

```go
// TestStartGridCycle_UseSignalStep_ForcesVirtual is the regression for a UseSignal=true grid
// step that would otherwise place as a blind resting order (OrderType != "virtual",
// PriceMovePct <= 0) — a signal-gated level must always be software-monitored so the
// price-tick loop (gridVirtualPriceTick) gets a chance to check the signal before it ever
// reaches the exchange.
func TestStartGridCycle_UseSignalStep_ForcesVirtual(t *testing.T) {
	// Follow this file's existing pattern for constructing a StrategyRunner + Strategy with
	// Steps configured and calling whatever the existing "start a grid cycle"/"reprice"
	// entry point is (read the file live — do not guess the exact helper/setup shape here).
	// Assert that the resulting GridLevel for the UseSignal=true step has ForceVirtual==true
	// even though its own OrderType/PriceMovePct alone would not force that.
}
```

If no existing test conveniently exercises level construction in a way you can extend, it's
acceptable to test the narrower unit directly: extract nothing new — just confirm, by reading
the two call sites, that `forceVirtual := step.OrderType == "virtual" || step.PriceMovePct > 0`
appears verbatim (search `pkg/strategy/cycle.go` for this exact string; it currently appears
at two locations, roughly line 1799 and line 4391 — confirm live) and write a test at whichever
of the two is more directly testable in isolation, following this file's established fixture
conventions. Ask if genuinely stuck — this is judgment-call territory given the size of
`cycle.go`.

- [ ] **Step 2: Run test to verify it fails**

Expected: FAILs because `ForceVirtual` doesn't yet account for `UseSignal`.

- [ ] **Step 3: Write the implementation**

At BOTH locations (search `cycle.go` for `forceVirtual := step.OrderType == "virtual" ||
step.PriceMovePct > 0` — verify there are exactly two matches before editing; if the count
differs from two, stop and report rather than guessing which occurrence is which):

```go
			// Signal-gated levels are always virtual — a level nothing would ever re-check
			// the signal on must never be a blind resting order.
			forceVirtual := step.OrderType == "virtual" || step.PriceMovePct > 0 || step.UseSignal
```

And at both corresponding `GridLevel{...}` struct-literal construction sites right below each
(there are two grid `sr.levels = append(sr.levels, GridLevel{...})` calls, one per creation
site — read the surrounding ~15 lines at each `forceVirtual :=` occurrence to find its
matching literal), add the field:

```go
					sr.levels = append(sr.levels, GridLevel{
						ID:           levelID,
						LevelIdx:     levelIdx,
						Side:         side,
						TargetPrice:  targetPrice,
						SizeUSDT:     sizeUSDT,
						Qty:          qty,
						Status:       LevelPending,
						ForceVirtual: forceVirtual,
						UseSignal:    step.UseSignal,
					})
```

And each matching `INSERT INTO strategy_levels (...)` right above each struct literal needs
`use_signal` added to its column list and `step.UseSignal` added to its args (read the exact
current INSERT at each site — one includes `force_virtual` already per Task 1's sibling
column, follow that exact same column-list/args-list editing pattern for `use_signal`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/strategy/ -v -run TestStartGridCycle_UseSignalStep_ForcesVirtual` (or
whatever you actually named it)
Expected: `PASS`.

- [ ] **Step 5: Run the full pkg/strategy suite to check for regressions**

Run: `go test ./pkg/strategy/...`
Expected: all existing tests still pass — this task only ADDS a new OR-condition and a new
struct field default (`false` for every existing test fixture that doesn't set `UseSignal`),
so no existing behavior should change.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/*_test.go
git commit -m "feat(strategy): force UseSignal grid steps to be virtual, persist the flag"
```

---

## Task 5: Grid — gate placement in `gridVirtualPriceTick`

**Files:**
- Modify: `pkg/strategy/cycle.go` (`gridVirtualPriceTick`, currently ~line 5706)
- Test: `pkg/strategy/grid_signal_gate_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// pkg/strategy/grid_signal_gate_test.go
package strategy

import (
	"context"
	"testing"
)

// TestGridVirtualPriceTick_UseSignalLevel_SignalAbsent_DoesNotPlace is the regression for the
// live incident (2026-09-21, PTBUSDT/Semera): a UseSignal=true level whose price trigger has
// been crossed must NOT place an order while the strategy's signalGateAllows() is false —
// proven by the level staying LevelPending (not advancing to LevelPlaced), since reaching
// PlaceOrder on a nil sr.runner.Exchange() would otherwise panic and this test would need a
// different proof technique if that path were reached.
func TestGridVirtualPriceTick_UseSignalLevel_SignalAbsent_DoesNotPlace(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
			// No SignalConfigs/signalEngine configured — but see note below: this test needs
			// signalGateAllows() to return FALSE, which the "no engine/no configs" fallback
			// does NOT give you (that fallback returns true). You must construct a scenario
			// where signalGateAllows() genuinely evaluates to false — e.g. a real
			// sr.runner.signalEngine with a queryable state that resolves to Neutral/opposite
			// direction, OR (simpler, and preferred if it works) temporarily factor
			// signalGateAllows's result to something test-injectable. Read Task 3's
			// signal_gate.go once it exists and decide the cleanest way to force a "false"
			// result here without a real pkg/signal engine wired end-to-end — this may mean
			// constructing a minimal real signal.Engine with a subscribed/cached Neutral
			// state, or it may reveal that signalGateAllows should accept its inputs in a
			// more testable shape. Use your judgment; if genuinely blocked, report back
			// rather than weakening the test's actual assertion.
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, ForceVirtual: true,
				UseSignal: true, TargetPrice: 100.0, Qty: "1.0"},
		},
	}
	sr.gridVirtualPriceTick(context.Background(), 100.0)
	if sr.levels[0].Status != LevelPending {
		t.Errorf("level status = %v, want still LevelPending — signal absent must block placement", sr.levels[0].Status)
	}
	if sr.levels[0].ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must stay empty — signal-blocked level must never reach PlaceOrder")
	}
}

// TestGridVirtualPriceTick_NonSignalLevel_Unaffected is the regression-of-the-regression:
// confirms Task 5's new gate check doesn't accidentally block a level that was never
// UseSignal-gated in the first place. Reaching PlaceOrder on the nil sr.runner.Exchange()
// proves the existing (pre-this-plan) placement path still runs for a plain virtual level.
func TestGridVirtualPriceTick_NonSignalLevel_Unaffected(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, ForceVirtual: true,
				UseSignal: false, TargetPrice: 100.0, Qty: "1.0"},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching PlaceOrder on nil Exchange — a non-signal-gated level must place unconditionally, unchanged from before this plan")
		}
	}()
	sr.gridVirtualPriceTick(context.Background(), 100.0)
}
```

Resolve the placeholder/judgment-call note inside the first test before considering this step
done — do not leave a test that doesn't actually exercise a real `false` result from
`signalGateAllows()`. If after investigation you find the cleanest approach is different from
what's sketched above, that's fine — the requirement is a genuine, non-trivial proof that a
signal-absent UseSignal level does not place, not the exact mechanism sketched here.

- [ ] **Step 2: Run tests to verify they fail for the right reason**

Run: `go test ./pkg/strategy/ -run TestGridVirtualPriceTick -v`
Expected: the "signal absent" test fails (level currently places unconditionally — no gate
exists yet); the "unaffected" test currently already passes (nothing changed that path yet) —
that's fine, it's here to lock in behavior Task 5 must not break, not to prove a new failure.

- [ ] **Step 3: Write the implementation**

In `gridVirtualPriceTick` (`pkg/strategy/cycle.go`, ~line 5706), find:

```go
		if !crossed {
			continue
		}
		// Execute at market price
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
```

Insert the gate check between the crossed-check and the placement:

```go
		if !crossed {
			continue
		}
		if l.UseSignal && !sr.signalGateAllows() {
			continue // price reached, but the configured signal doesn't currently agree — stay pending
		}
		// Execute at market price
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/strategy/ -run TestGridVirtualPriceTick -v`
Expected: `PASS` for both.

- [ ] **Step 5: Full pkg/strategy regression check**

Run: `go test ./pkg/strategy/...`
Expected: all pass, no unrelated regressions.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/grid_signal_gate_test.go
git commit -m "feat(strategy): gate grid virtual-level placement on signal state"
```

---

## Task 6: Matrix — extend `matrixLevelConfig` to also return `useSignal`

**Files:**
- Modify: `pkg/strategy/matrix.go` (`matrixLevelConfig`, currently ~line 74)
- Modify: all 7 existing callers (list below)
- Test: extend or add to `pkg/strategy/matrix_test.go` (check what test file already covers
  `matrixLevelConfig`, if any, and extend it there for consistency)

`matrixLevelConfig(slot int) (tpPct, stopPct, stopCondPct, stopReplacePct *float64)` is the
one place matrix code already looks up a slot's full config (whether slot 0/entry, a positive
"above" slot, or a negative "below" slot). Adding a 5th return value here is less error-prone
than re-deriving `UseSignal` separately at every level-construction site in Task 7.

- [ ] **Step 1: Write the failing test**

```go
// Add to whichever existing pkg/strategy/*_test.go file already tests matrixLevelConfig
// (search for `matrixLevelConfig` in _test.go files first) — or create a small new one if
// none exists:
func TestMatrixLevelConfig_ReturnsUseSignal(t *testing.T) {
	useSig := true
	tpPct := 2.0
	sr := &StrategyRunner{
		strategy: Strategy{
			MatrixEntryLevel: &MatrixEntryLevel{SizePct: 20, UseSignal: true},
			MatrixLevels: []MatrixLevel{
				{Direction: "below", PriceStepPct: -5, TPPct: &tpPct, UseSignal: useSig},
				{Direction: "above", PriceStepPct: 2, UseSignal: false},
			},
		},
	}
	if _, _, _, _, us := sr.matrixLevelConfig(0); us != true {
		t.Errorf("slot 0 useSignal = %v, want true", us)
	}
	if _, _, _, _, us := sr.matrixLevelConfig(-1); us != true {
		t.Errorf("slot -1 useSignal = %v, want true", us)
	}
	if _, _, _, _, us := sr.matrixLevelConfig(1); us != false {
		t.Errorf("slot 1 useSignal = %v, want false", us)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAILS TO COMPILE — `matrixLevelConfig` only returns 4 values today.

- [ ] **Step 3: Write the implementation**

Change the signature and every `return` inside it:

```go
func (sr *StrategyRunner) matrixLevelConfig(slot int) (tpPct, stopPct, stopCondPct, stopReplacePct *float64, useSignal bool) {
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		if e == nil {
			return
		}
		return e.TPPct, e.StopPct, e.StopCondPct, e.StopReplacePct, e.UseSignal
	}
	if slot > 0 {
		above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
		if idx := slot - 1; idx < len(above) {
			l := above[idx]
			return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct, l.UseSignal
		}
		return
	}
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	if idx := -slot - 1; idx < len(below) {
		l := below[idx]
		return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct, l.UseSignal
	}
	return
}
```

Then update EVERY existing caller to accept the 5th return value (use `_` where the caller
doesn't need it yet — later tasks will use it at the two call sites that matter for placement).
Current callers (confirm this list is still accurate by re-grepping `matrixLevelConfig(` before
editing — this plan was written against a specific commit and line numbers may have shifted):

- `pkg/strategy/hedge_support.go:110` — `_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)` → add a 5th `_`
- `pkg/strategy/matrix.go:874` — same pattern, add 5th `_`
- `pkg/strategy/matrix.go:1060` — same
- `pkg/strategy/matrix.go:1393` — same
- `pkg/strategy/matrix.go:1582` — `tpPctVal, _, _, _ = sr.matrixLevelConfig(...)` (note: `=` not `:=` here — check live) → add 5th `_`
- `pkg/strategy/matrix.go:1987` — same as the first pattern
- `pkg/strategy/reconcile.go:599` — same

- [ ] **Step 4: Run test to verify it passes, and build the whole module**

Run: `go build ./...` (this will fail loudly at every caller you missed — fix each) then
`go test ./pkg/strategy/ -run TestMatrixLevelConfig_ReturnsUseSignal -v`
Expected: build exits 0, test `PASS`.

- [ ] **Step 5: Full pkg/strategy regression check**

Run: `go test ./pkg/strategy/...`
Expected: all pass — this task only widens a return tuple and updates callers to ignore the
new value; no existing behavior changes.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/hedge_support.go pkg/strategy/reconcile.go pkg/strategy/*_test.go
git commit -m "feat(strategy): matrixLevelConfig also returns useSignal"
```

---

## Task 7: Matrix — force-virtual + persist `UseSignal` at every level-construction site

**Files:**
- Modify: `pkg/strategy/matrix.go` (multiple sites)
- Modify: `pkg/strategy/matrix_relative_engine.go` (multiple sites)
- Test: extend existing matrix cycle-start / relative-expand tests

This is the matrix equivalent of Task 4, but with more call sites since matrix levels get
constructed in more places (initial cycle start, safe-zone rebuild, relative-slots expansion,
etc.) than grid's two. Every site that does `INSERT INTO strategy_levels (...)` for a matrix
level, followed by `sr.levels = append(sr.levels, GridLevel{...})` or `newLevel :=
GridLevel{...}`, needs `use_signal`/`UseSignal` threaded through, sourced from
`matrixLevelConfig(slot)`'s new 5th return value (Task 6).

Known sites as of this plan's writing (re-grep `GridLevel{` in both files before starting —
this list may be incomplete or have shifted):

- `pkg/strategy/matrix.go:449` (inside the main cycle-start loop — this is `startMatrixCycle`
  or equivalent; the surrounding code already computes `slot` for each level and could call
  `matrixLevelConfig(slot)` to get `useSignal` before the INSERT)
- `pkg/strategy/matrix.go:1247`
- `pkg/strategy/matrix.go:2208`
- `pkg/strategy/matrix.go:2279`
- `pkg/strategy/matrix.go:2357`
- `pkg/strategy/matrix.go:2433`
- `pkg/strategy/matrix.go:2664`
- `pkg/strategy/matrix_relative_engine.go:125`
- `pkg/strategy/matrix_relative_engine.go:182`

(`matrix_relative_engine.go:345`, `virtual = sr.matrixIsVirtual(&GridLevel{Slot: &s})`, is a
throwaway struct used only to probe `matrixIsVirtual`'s slot-based logic, not a real level
insert — skip it, but see Task 8, which touches `matrixIsVirtual` itself and this exact call
site's caller.)

- [ ] **Step 1: For each site, read it live and apply the same pattern**

For each location: find the nearby `slot` variable already in scope (every one of these sites
already knows which slot it's constructing, since that's needed for `TargetPrice`/`SizePct`
lookups), call `_, _, _, _, useSignal := sr.matrixLevelConfig(slot)` (or reuse an existing
`matrixLevelConfig` call already made at that site for TP/stop config — don't call it twice if
one call already exists nearby; extend that existing call's assignment instead), add
`use_signal` to the `INSERT INTO strategy_levels` column list and args, and add `UseSignal:
useSignal` to the `GridLevel{...}` literal.

Follow Task 6's `TestMatrixLevelConfig_ReturnsUseSignal`-adjacent style — after finishing all
sites, write at least ONE test proving a `MatrixLevel.UseSignal=true` config produces a
`GridLevel` with `UseSignal=true` after a full matrix cycle start (whatever the existing
"start a matrix cycle" test helper is called — grep for an existing test that already
constructs a matrix cycle and asserts on the resulting `sr.levels`, and extend it, following
Task 4's approach of extending established fixtures rather than inventing new scaffolding).

- [ ] **Step 2 & 3: TDD as usual** — write the assertion first (confirm it fails because
`UseSignal` isn't threaded through yet), then apply the fix at all 9 sites, then confirm it
passes.

- [ ] **Step 4: Verify no site was missed**

```bash
grep -c "GridLevel{" pkg/strategy/matrix.go pkg/strategy/matrix_relative_engine.go
```

Compare against the count from before you started (7 in matrix.go, 2 in
matrix_relative_engine.go, per this plan's own count — confirm your count matches, since a
missed site is a silent gap, not a compile error, given `UseSignal` defaults to `false` on an
unset struct field).

- [ ] **Step 5: Full pkg/strategy regression check**

Run: `go test ./pkg/strategy/...`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/matrix_relative_engine.go pkg/strategy/*_test.go
git commit -m "feat(strategy): thread UseSignal through every matrix level construction site"
```

---

## Task 8: Matrix — force-virtual via `matrixIsVirtual` + gate placement

**Files:**
- Modify: `pkg/strategy/matrix.go` (`matrixIsVirtual`, currently ~line 233; `matrixTriggerVirtualLevel`, ~line 1093)
- Modify: `pkg/strategy/matrix_relative_engine.go` (`matrixPlaceRelativeVirtualOrder`, ~line 199)
- Test: `pkg/strategy/matrix_signal_gate_test.go`

Two remaining pieces for matrix, mirroring Tasks 4 and 5's grid work:

1. `matrixIsVirtual(l *GridLevel) bool` must also return `true` when `l.UseSignal` is true
   (so a signal-gated matrix level is always software-monitored, same reasoning as grid's
   `forceVirtual`).
2. Both matrix placement call sites (`matrixTriggerVirtualLevel` for absolute-mode matrix,
   `matrixPlaceRelativeVirtualOrder` for `RelativeSlots` mode — matrix has two, unlike grid's
   one, because relative-slots strategies use an entirely separate expansion path) must gate
   on `signalGateAllows()` before calling `PlaceOrder`, exactly like Task 5's grid change.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/strategy/matrix_signal_gate_test.go
package strategy

import (
	"context"
	"testing"
)

// TestMatrixIsVirtual_UseSignalLevel_AlwaysVirtual is the matrix equivalent of Task 4's grid
// forceVirtual regression: a UseSignal=true level must be treated as virtual even when
// nothing else about it (Slot, OrderType) would normally make it so.
func TestMatrixIsVirtual_UseSignalLevel_AlwaysVirtual(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionLong}}
	slot := 1
	l := &GridLevel{Slot: &slot, UseSignal: true}
	if !sr.matrixIsVirtual(l) {
		t.Error("matrixIsVirtual = false, want true — UseSignal must force virtual regardless of slot/OrderType config")
	}
}

// TestMatrixTriggerVirtualLevel_SignalAbsent_DoesNotPlace is the matrix absolute-mode
// equivalent of Task 5's grid test.
func TestMatrixTriggerVirtualLevel_SignalAbsent_DoesNotPlace(t *testing.T) {
	// Mirror Task 5's approach for forcing signalGateAllows() to return false — read
	// signal_gate.go (Task 3) and Task 5's finished test for the established technique
	// before writing this.
	slot := 1
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
	}
	l := &GridLevel{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, Slot: &slot, UseSignal: true, Qty: "1.0"}
	sr.matrixTriggerVirtualLevel(context.Background(), l)
	if l.Status != LevelPending {
		t.Errorf("level status = %v, want still LevelPending", l.Status)
	}
}
```

(Leave the `matrixPlaceRelativeVirtualOrder` case to Step 3's implementation + Task 7/8's
existing relative-slots test file conventions — read `matrix_relative_test.go`'s existing
tests for `matrixPlaceRelativeVirtualOrder` first, e.g.
`TestMatrixPlaceRelativeVirtualOrder_AllowedByRiskGate_ReachesExchange`, and add a sibling
`_SignalAbsent_DoesNotReachExchange` test following that exact same file's established
fixture style, which already knows how to construct a runnable `sr.runner`/`AccountRunner`
without a full nil-runner panic — reuse that pattern rather than the panic-proof technique
used elsewhere in this plan, to stay consistent with that specific file.)

- [ ] **Step 2: Run tests to verify they fail**

Expected: `matrixIsVirtual` test fails (doesn't check UseSignal yet); `matrixTriggerVirtualLevel`
test fails (places unconditionally, level advances past Pending).

- [ ] **Step 3: Write the implementation**

In `matrixIsVirtual` (read the current full body live first — this plan's earlier summary of
it is from an earlier read and may not reflect every branch), add a check at the very top,
before any of the existing slot-based branching:

```go
func (sr *StrategyRunner) matrixIsVirtual(l *GridLevel) bool {
	if l.ForceVirtual {
		return true
	}
	if l.UseSignal {
		return true
	}
	if l.Slot == nil {
		return false
	}
	// ... existing slot-based logic unchanged below ...
```

In `matrixTriggerVirtualLevel` (~line 1093), add the same gate Task 5 added to grid, right
before the `PlaceOrder` call:

```go
func (sr *StrategyRunner) matrixTriggerVirtualLevel(ctx context.Context, l *GridLevel) {
	if l.UseSignal && !sr.signalGateAllows() {
		return // price reached, but the configured signal doesn't currently agree — stay pending
	}
	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-v%d",
		sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
	// ... rest unchanged ...
```

In `matrixPlaceRelativeVirtualOrder` (`matrix_relative_engine.go`, ~line 199), read its full
current body live and add the equivalent gate at the same point the existing risk-gate check
(`TestMatrixPlaceRelativeVirtualOrder_BlockedByRiskGate_DoesNotReachExchange`'s subject) runs
— i.e. before the `PlaceOrder` call, following that function's own existing early-return style
for a blocked placement (it already has at least one such early-return for the risk gate;
match that same shape for the signal gate).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/strategy/ -run "TestMatrixIsVirtual|TestMatrixTriggerVirtualLevel|TestMatrixPlaceRelativeVirtualOrder" -v`
Expected: `PASS` across the board, including pre-existing tests in this group (confirms the
new gate doesn't break the risk-gate test or the "allowed" positive-path test).

- [ ] **Step 5: Full pkg/strategy regression check**

Run: `go test ./pkg/strategy/...`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/matrix_relative_engine.go pkg/strategy/matrix_signal_gate_test.go
git commit -m "feat(strategy): gate matrix virtual-level placement on signal state (both absolute and relative-slots modes)"
```

---

## Task 9: Cancel-on-signal-loss for already-placed levels

**Files:**
- Create: `pkg/strategy/signal_gate_cancel.go`
- Modify: `pkg/strategy/cycle.go` (call the new function from `gridVirtualPriceTick`'s caller,
  or directly at the end of `gridVirtualPriceTick` — your judgment on the cleanest call site,
  see below)
- Modify: `pkg/strategy/matrix.go` (call the new function from `matrixPriceTick`)
- Test: `pkg/strategy/signal_gate_cancel_test.go`

This is the one genuinely new behavior (not just gating an existing placement path): a level
that's `UseSignal=true`, currently `LevelPlaced` (a real resting order, unfilled), must have
that order cancelled and the level reverted to `Pending` once the signal no longer agrees —
shared logic for both strategy types, since it only depends on fields already uniform across
`GridLevel` (`Status`, `UseSignal`, `ExchangeOrderID`), not on `Slot`/matrix-specific state.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/strategy/signal_gate_cancel_test.go
package strategy

import (
	"context"
	"testing"
)

// TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels is the core new
// behavior this task adds: a resting, unfilled, signal-gated order must be pulled once the
// signal no longer agrees. Proven by reaching CancelOrder on a nil sr.runner.Exchange()
// (panics), the same proof technique used throughout this plan and this codebase's existing
// matrix tests.
func TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: true, ExchangeOrderID: "order-abc"},
		},
		// signalGateAllows() must evaluate false here — reuse whatever technique Task 5/8
		// settled on for forcing that.
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching CancelOrder on nil Exchange — signal-lost placed level must be cancelled")
		}
	}()
	sr.cancelSignalLostLevels(context.Background())
}

// TestCancelSignalLostLevels_FilledLevel_NeverTouched is the critical safety regression:
// signal loss must NEVER touch an already-filled (real, open) position — only pending/placed
// unfilled orders. No panic expected; the filled level must be completely ignored.
func TestCancelSignalLostLevels_FilledLevel_NeverTouched(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelFilled, UseSignal: true, FilledPrice: 100.0},
		},
	}
	// No signal-forcing needed — even if signalGateAllows() were false, a LevelFilled level
	// must never reach CancelOrder. A nil sr.runner is deliberately left in place: if this
	// test panics, the fix is wrong (it touched a filled level).
	sr.cancelSignalLostLevels(context.Background())
	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %v, want unchanged LevelFilled", sr.levels[0].Status)
	}
}

// TestCancelSignalLostLevels_NonSignalLevel_NeverTouched confirms a plain (UseSignal=false)
// placed level is left alone regardless of what the signal is doing — this cancellation path
// only applies to levels that opted into signal gating.
func TestCancelSignalLostLevels_NonSignalLevel_NeverTouched(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: false, ExchangeOrderID: "order-abc"},
		},
	}
	sr.cancelSignalLostLevels(context.Background())
	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID == "" {
		t.Error("non-signal-gated placed level must be untouched")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail for the right reason**

Expected: `undefined: (*StrategyRunner).cancelSignalLostLevels` (compile failure) — write the
implementation next.

- [ ] **Step 3: Write the implementation**

```go
// pkg/strategy/signal_gate_cancel.go
package strategy

import (
	"context"

	"sis/pkg/trader"
)

// cancelSignalLostLevels cancels the resting exchange order for every UseSignal=true level
// currently LevelPlaced (unfilled) whose signal no longer agrees, reverting each to Pending
// so the existing virtual-level price-tick monitors (gridVirtualPriceTick, matrixPriceTick)
// resume silently watching it — it re-places automatically once the signal returns (and the
// price condition still holds). Shared by both strategy types: nothing here is grid- or
// matrix-specific, since GridLevel's Status/UseSignal/ExchangeOrderID fields are uniform
// across both.
//
// Deliberately does NOT touch LevelFilled levels — signal loss withholds/removes only
// not-yet-filled orders, never closes an already-open position. See the design doc's
// "Already-filled levels are never touched" invariant.
//
// Must be called with sr.mu held (same requirement as its callers in the price-tick paths).
func (sr *StrategyRunner) cancelSignalLostLevels(ctx context.Context) {
	if sr.cycle == nil {
		return
	}
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPlaced || !l.UseSignal || l.ExchangeOrderID == "" {
			continue
		}
		if sr.signalGateAllows() {
			continue // signal still agrees — leave the resting order in place
		}
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  l.ExchangeOrderID,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, "signal-gated level: отмена ордера: "+err.Error())
			continue
		}
		sr.runner.UnregisterOrder(l.ExchangeOrderID)
		l.Status = LevelPending
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
		sr.info(ctx, "Signal-gated уровень "+l.ID+": сигнал пропал — ордер снят с биржи")
	}
}
```

(Confirm `isOrderGone` and `sr.runner.UnregisterOrder` are the exact right helper names by
grepping `pkg/strategy/*.go` — both are used elsewhere in this codebase per earlier reads in
this plan's own design research; verify live rather than trusting this plan text blindly.)

Wire it in: at the end of `gridVirtualPriceTick` (after the existing per-level loop, right
before `sr.lastVirtualPrice = price`), add `sr.cancelSignalLostLevels(ctx)`. In
`matrixPriceTick`, add the same call — pick a location after the existing virtual-trigger
step (step 2 in that function's own numbered-comment structure) and before step 3
(`matrixApplyStopCondSLs`), matching that function's own established "numbered steps" style;
read the function's current full body live to place it correctly relative to the
`RelativeSlots` branch too (the call must run for BOTH relative and absolute matrix modes,
so it likely belongs after the `if sr.strategy.RelativeSlots { ... } else { ... }` block
closes, not inside either branch).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/strategy/ -run TestCancelSignalLostLevels -v`
Expected: `PASS` for all three.

- [ ] **Step 5: Full pkg/strategy regression check**

Run: `go test ./pkg/strategy/...`
Expected: all pass — pay particular attention to any existing `gridVirtualPriceTick`/
`matrixPriceTick` test whose fixture includes a `LevelPlaced` level with a nil/nonzero
`ExchangeOrderID` and no `UseSignal` set (should be entirely unaffected, since the new call
only acts on `UseSignal=true` levels) — if any such test's behavior changed, that's a real bug
to fix, not a test to weaken.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/signal_gate_cancel.go pkg/strategy/signal_gate_cancel_test.go pkg/strategy/cycle.go pkg/strategy/matrix.go
git commit -m "feat(strategy): cancel resting signal-gated orders when the signal drops"
```

---

## Task 10: Frontend — muted + badge rendering for signal-withheld levels

**Files:**
- Modify: `frontend/src/components/terminal/Chart.tsx`

**Files:**
- Test: extend an existing Chart.tsx test if one exists (search `frontend/src/**/*.test.tsx`
  for `Chart` first); if none exists for this component, a manual browser check (Step 4) is
  the verification method — do not invent a new test harness for a component that has none.

- [ ] **Step 1: Read the current virtual-level rendering block live**

Find the block currently around `Chart.tsx:586-609` (search for `// Virtual levels — pending
(matrix) or force_virtual (regular strategies)`):

```tsx
    if (!overlaySettings || overlaySettings.showPlacedOrders) {
      for (const lv of (strategyLevels ?? []).filter(l => (l.status === 'pending' || l.force_virtual) && l.status !== 'cancelled' && l.status !== 'filled' && l.status !== 'sl_closed' && l.target_price > 0)) {
        if (dirFilter) {
          const lvLong = lv.side === 'Buy'
          if (dirFilter === 'long' && !lvLong) continue
          if (dirFilter === 'short' && lvLong) continue
        }
        const p = lv.target_price
        const isLong = lv.side === 'Buy'
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice, isLong)}` : ''
        const usdt = lv.size_usdt > 0 ? ` ${lv.size_usdt.toFixed(0)}$` : ''
        const color = isLong ? '#6ee7b7' : '#fca5a5'
        const vText = `${lv.slot != null ? `L(${lv.slot})` : `L${lv.level_idx}`}${pct}${usdt} [V]`
        priceLineTitlesRef.current.push({ price: p, color, text: vText, filled: false })
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 3,
          axisLabelVisible: false,
          title: '',
        }))
      }
    }
```

- [ ] **Step 2: Add the `use_signal` field to the `strategyLevels` prop's TypeScript type**

Find the type definition for the `strategyLevels` prop (search this file, or a shared types
file it imports from, for the interface/type containing `target_price`, `slot`, `force_virtual`
— these are the sibling fields already present) and add `use_signal?: boolean` alongside them,
matching how `force_virtual` is typed there (optional vs required — match its exact style).

Also confirm the backend API response that feeds `strategyLevels` (likely
`GetStrategyState`, `services/api-gateway/strategy_handler.go` — grep for `force_virtual` in
that file's JSON-emitting code) already includes `use_signal` in its output. Since Task 2
added the column and it's now part of every level row, check whether that handler's SELECT/
struct already does a `SELECT *`-equivalent or an explicit column list — if explicit, add
`use_signal` there too (same file, likely near where `force_virtual` is selected/returned for
this same endpoint) so the frontend actually receives it.

- [ ] **Step 3: Apply the muted + badge styling for signal-withheld levels**

Replace the block from Step 1:

```tsx
    if (!overlaySettings || overlaySettings.showPlacedOrders) {
      for (const lv of (strategyLevels ?? []).filter(l => (l.status === 'pending' || l.force_virtual) && l.status !== 'cancelled' && l.status !== 'filled' && l.status !== 'sl_closed' && l.target_price > 0)) {
        if (dirFilter) {
          const lvLong = lv.side === 'Buy'
          if (dirFilter === 'long' && !lvLong) continue
          if (dirFilter === 'short' && lvLong) continue
        }
        const p = lv.target_price
        const isLong = lv.side === 'Buy'
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice, isLong)}` : ''
        const usdt = lv.size_usdt > 0 ? ` ${lv.size_usdt.toFixed(0)}$` : ''
        // Signal-gated levels render muted (reduced-opacity direction color) with a "⏸
        // signal" badge instead of the normal lighter "virtual, waiting on price" color —
        // chosen over a gray palette (browser-mockup comparison, 2026-09-21) so the line
        // still reads as this level's own buy/sell color, just visibly held back.
        const signalWithheld = lv.status === 'pending' && !!lv.use_signal
        const color = signalWithheld ? (isLong ? '#34d39955' : '#f8717155') : (isLong ? '#6ee7b7' : '#fca5a5')
        const badge = signalWithheld ? ' ⏸ signal' : ''
        const vText = `${lv.slot != null ? `L(${lv.slot})` : `L${lv.level_idx}`}${pct}${usdt} [V]${badge}`
        priceLineTitlesRef.current.push({ price: p, color, text: vText, filled: false })
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 3,
          axisLabelVisible: false,
          title: '',
        }))
      }
    }
```

- [ ] **Step 4: Verify it compiles and manually check in the browser**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json`
Expected: exits 0.

Manual check (per this project's CLAUDE.md — UI changes need a real browser check, not just a
type-check): start the dev server, open a strategy that has a `UseSignal=true` grid step or
matrix level whose price has been reached but signal doesn't currently match (or temporarily
flip an existing strategy's `use_signal` flag via the UI toggle to force this state for
testing), confirm the level's line renders with the muted color + `⏸ signal` badge instead of
the previous lighter "virtual" look, and confirm every OTHER existing level style (placed,
plain virtual, filled) is visually unchanged.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/terminal/Chart.tsx
git commit -m "feat(frontend): render signal-withheld levels muted with a signal badge"
```

(Also commit the `services/api-gateway/strategy_handler.go` change from Step 2 here if that
file needed editing:)

```bash
git add services/api-gateway/strategy_handler.go
```

---

## Final verification (run after all tasks)

- [ ] **Full Go build**

Run: `go build ./...`
Expected: exits 0.

- [ ] **Full Go unit test suite**

Run: `go test ./pkg/...`
Expected: `ok` for every package, especially `pkg/strategy` (grid/matrix/hedge tests
unaffected outside what this plan deliberately changed).

- [ ] **Frontend type-check and test suite**

Run: `cd frontend && npx tsc --noEmit -p tsconfig.json && npx vitest run`
Expected: clean type-check, all existing tests still passing.

- [ ] **Per CLAUDE.md: explicit test-review report**

Before considering this plan done, explicitly report to the user (not just "tests passed"):
which existing mechanics were checked for regression (grid cycle start/reprice, matrix
absolute-mode virtual triggering, matrix relative-slots expansion, the risk gate on relative
virtual orders, `matrixLevelConfig`'s 7 existing callers), and confirm each still behaves
identically for `UseSignal=false` levels (the default, and every pre-existing level in the
live DB before this migration).
