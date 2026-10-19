# ALS Hedge Strategy — Core Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `als.go`'s no-op stubs (from `2026-10-18-als-hedge-foundations.md`) with
the real engine: evaluate threshold rows against the live main position's price, compute
the target-based top-up volume, place it, and place each filled level's own breakeven
stop. After this plan, an ALS strategy actually hedges — end to end — for the simple case
(no stop-order limit overflow, no TP pause/resume, no Unsafe hedge percent clamp yet —
those three are `2026-10-18-als-hedge-polish.md`, written after this one).

**Architecture:** A new price-tick monitor (`launchAlsPriceMonitor`/`alsPriceTick`,
structurally mirroring `matrix.go`'s `launchMatrixPriceMonitor`/`matrixPriceTick`) walks
every pending threshold level each tick, checks whether it's crossed, and if so computes
and places the delta top-up. Breakeven-stop-with-delay reuses the **existing, unmodified**
free functions `matrixStopCondThreshold`/`matrixStopReplaceTrigger` (`pkg/strategy/matrix.go`)
directly — they take no matrix-specific state, just `(Direction, fillPrice, pct float64)`,
so `als.go` calls them exactly as `matrix.go` does, with zero coupling.

**Tech Stack:** Go, the existing `StrategyRunner`/`fakeExchange` test harness.

**Depends on:** `docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md` and
`docs/superpowers/plans/2026-10-18-als-hedge-foundations.md` (must be applied first —
this plan edits the stub functions that one created).

---

### Task 1: Pure calculation functions — threshold price and target/delta sizing

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_calc_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/strategy/als_calc_test.go`:

**Direction convention — read this before writing the tests below.** Every function in
`als.go` that takes a `Direction` parameter (here and throughout this plan) takes the ALS
strategy row's OWN direction — i.e. exactly `sr.strategy.Direction`, the HEDGE's own
direction, never the main position's. The main position is always the OPPOSITE side
(confirmed by the already-existing `effectiveDeposit`'s identical mapping in `cycle.go`:
hedge long → main short, hedge short → main long). So "adverse for main" inverts
relative to what you'd expect from the hedge's own direction alone:

- Hedge direction **Short** ⟹ main is **Long** ⟹ adverse-for-main = price **falling** ⟹
  the threshold trigger price lands **below** the base price.
- Hedge direction **Long** ⟹ main is **Short** ⟹ adverse-for-main = price **rising** ⟹
  the threshold trigger price lands **above** the base price.

This is the opposite of what a naive "Long levels go up, Short levels go down" reading of
`Direction` would suggest — because `Direction` here is the hedge's, not the main's. Get
this backwards and every ALS hedge fires at the wrong price, in production, with real
money — this is the exact class of direction bug fixed live for Binance earlier this
session (`binanceConditionalOrderType`, 2026-10-18) and must not be reintroduced here.

```go
package strategy

import "testing"

func TestAlsThresholdPrice_ShortHedgeProtectsLongMain_TriggersBelowBase(t *testing.T) {
	// Hedge direction Short (protects a Long main) — adverse for main is price FALLING,
	// so trigger_pct=-10 against a base of 100 must land BELOW 100.
	got := alsThresholdPrice(DirectionShort, 1, -10, 100, 0)
	want := 90.0
	if got != want {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdPrice_LongHedgeProtectsShortMain_TriggersAboveBase(t *testing.T) {
	// Hedge direction Long (protects a Short main) — adverse for main is price RISING,
	// so trigger_pct=-10 against a base of 100 must land ABOVE 100.
	got := alsThresholdPrice(DirectionLong, 1, -10, 100, 0)
	want := 110.0
	if got != want {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdPrice_FromLastOrder(t *testing.T) {
	// trigger_type=0 (from last order) uses lastOrderPrice, not avgEntry. Hedge Short
	// (long main): -5% from a last-order base of 95 lands below it.
	got := alsThresholdPrice(DirectionShort, 0, -5, 100, 95)
	want := 90.25 // 95 * (1 - 5/100)
	if got != want {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdCrossed_ShortHedgeProtectsLongMain_CrossesOnFall(t *testing.T) {
	cases := []struct {
		name    string
		price   float64
		trigger float64
		want    bool
	}{
		{"not yet crossed", 95, 90, false},
		{"exactly at trigger", 90, 90, true},
		{"crossed", 85, 90, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := alsThresholdCrossed(DirectionShort, c.price, c.trigger); got != c.want {
				t.Errorf("alsThresholdCrossed(short, %v, %v) = %v, want %v", c.price, c.trigger, got, c.want)
			}
		})
	}
}

func TestAlsThresholdCrossed_LongHedgeProtectsShortMain_CrossesOnRise(t *testing.T) {
	if !alsThresholdCrossed(DirectionLong, 110, 105) {
		t.Error("alsThresholdCrossed(long, 110, 105) = false, want true (price rose past trigger)")
	}
	if alsThresholdCrossed(DirectionLong, 100, 105) {
		t.Error("alsThresholdCrossed(long, 100, 105) = true, want false (price hasn't reached trigger)")
	}
}

func TestAlsTopUpUsdt_FirstLevel_NoExistingHedge(t *testing.T) {
	// target=30% of a 1000 USDT main, nothing open yet → full 300 USDT top-up.
	got := alsTopUpUsdt(30, 1000, 0)
	want := 300.0
	if got != want {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_SecondLevel_OnlyDelta(t *testing.T) {
	// target=50% of a 1000 USDT main, 300 already open → top up only 200 more.
	got := alsTopUpUsdt(50, 1000, 300)
	want := 200.0
	if got != want {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_MainGrewBetweenLevels(t *testing.T) {
	// target=50% of a main that's now 1200 USDT (grew via its own DCA since level 1
	// fired), 300 already open → target is 600, top up 300 more, not a flat re-add.
	got := alsTopUpUsdt(50, 1200, 300)
	want := 300.0
	if got != want {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_NeverNegative(t *testing.T) {
	// Already-open hedge exceeds this level's target (e.g. main shrank) — must not
	// return a negative top-up (that would mean "close some hedge", out of scope here).
	got := alsTopUpUsdt(30, 1000, 400)
	want := 0.0
	if got != want {
		t.Errorf("alsTopUpUsdt = %v, want %v (clamped to zero, never negative)", got, want)
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `go test ./pkg/strategy/... -run TestAlsThresholdPrice -run TestAlsThresholdCrossed -run TestAlsTopUpUsdt -v`
Expected: compile error — `alsThresholdPrice`/`alsThresholdCrossed`/`alsTopUpUsdt` don't exist yet.

- [ ] **Step 3: Implement the functions**

In `pkg/strategy/als.go`, add (keep the existing stub functions from the foundations
plan — these are new additions, not replacements, except where Task 2+ below says
otherwise):

```go
// alsThresholdPrice computes the absolute price at which one AlsThreshold row fires.
// dir is the ALS strategy's OWN (hedge) direction — sr.strategy.Direction, never the
// main position's — see the direction-convention note above this file's test block for
// the full reasoning. triggerType 0 = % from the last filled order (lastOrderPrice); 1 =
// % from the main position's average entry (avgEntryPrice) — mirrors the existing
// hedge-activation act_type convention (0/1), not the source ALS's ROI-based units; see
// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md. triggerPct is signed:
// negative means "adverse move" (price against the main position).
func alsThresholdPrice(dir Direction, triggerType int, triggerPct, avgEntryPrice, lastOrderPrice float64) float64 {
	base := avgEntryPrice
	if triggerType == 0 {
		base = lastOrderPrice
	}
	if dir == DirectionShort {
		// Hedge Short ⟹ main is Long ⟹ adverse-for-main = price FALLING ⟹ trigger
		// lands BELOW base (a negative triggerPct correctly lowers it).
		return base * (1 + triggerPct/100)
	}
	// Hedge Long (or Both, treated as Long here — ALS's per-level direction should
	// never actually be "Both"; the UI constrains thresholds to a single hedge
	// direction) ⟹ main is Short ⟹ adverse-for-main = price RISING ⟹ trigger lands
	// ABOVE base (a negative triggerPct correctly raises it).
	return base * (1 - triggerPct/100)
}

// alsThresholdCrossed reports whether currentPrice has reached triggerPrice in the
// adverse-for-main direction. dir is the hedge's own direction, same convention as
// alsThresholdPrice: hedge Short (long main) crosses on a FALL to/below triggerPrice;
// hedge Long (short main) crosses on a RISE to/above triggerPrice.
func alsThresholdCrossed(dir Direction, currentPrice, triggerPrice float64) bool {
	if dir == DirectionShort {
		return currentPrice <= triggerPrice
	}
	return currentPrice >= triggerPrice
}

// alsTopUpUsdt computes how much MORE hedge volume (in USDT) to place for one threshold
// row: the row's target is a CUMULATIVE percentage of the main position's current live
// volume (mainUsdt — pass effectiveDeposit(currentPrice), which already resolves to the
// opposite-direction position's live USDT value when SizeAsMain is set), not a flat
// per-level add. Only the gap between that target and what's already open gets placed —
// see the design spec's worked example. Never negative: if the hedge is already at or
// above this level's target (e.g. main shrank since an earlier level fired), there is
// nothing to top up.
func alsTopUpUsdt(targetHedgePct, mainUsdt, alreadyOpenUsdt float64) float64 {
	target := targetHedgePct / 100 * mainUsdt
	topUp := target - alreadyOpenUsdt
	if topUp < 0 {
		return 0
	}
	return topUp
}
```

- [ ] **Step 4: Run to confirm pass**

Run: `go test ./pkg/strategy/... -run TestAlsThresholdPrice -run TestAlsThresholdCrossed -run TestAlsTopUpUsdt -v`
Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_calc_test.go
git commit -m "Add ALS threshold price/crossing and target-based sizing calculations"
```

---

### Task 2: `startAlsCycle` — seed `strategy_levels` from `AlsThresholds`

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_cycle_start_test.go`

- [ ] **Step 1: Write the failing test**

`pkg/strategy`'s own integration tests create users/accounts via raw SQL against `pool`
directly (see `pkg/strategy/mark_level_placed_test.go` for the established pattern in
this package) — `createWHUser`/`createTestAccountExchange` live in `services/api-gateway`,
a different package, and are not callable here.

Create `pkg/strategy/als_cycle_start_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestStartAlsCycle_SeedsOneLevelRowPerThreshold is the regression test for the core
// scaffolding this whole engine plan builds on: starting an ALS cycle must create one
// strategy_levels row per AlsThresholds entry, all pending, with no exchange calls yet
// (nothing fires until alsPriceTick sees a crossed threshold — Task 3).
func TestStartAlsCycle_SeedsOneLevelRowPerThreshold(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-start-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, als_thresholds)
		VALUES ($1,$2,'ALSCOREUSDT','long','als','active',$3::jsonb) RETURNING id`,
		ownerID, accID,
		`[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30},{"trigger_type":1,"trigger_pct":-30,"target_hedge_pct":50}]`,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSCOREUSDT", Category: "linear", Direction: DirectionLong,
			HedgeMode: true,
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30},
				{TriggerType: 1, TriggerPct: -30, TargetHedgePct: 50},
			},
		},
	}

	if err := sr.startAlsCycle(ctx); err != nil {
		t.Fatalf("startAlsCycle: %v", err)
	}

	if sr.cycle == nil {
		t.Fatal("sr.cycle is nil after startAlsCycle")
	}
	if len(sr.levels) != 2 {
		t.Fatalf("sr.levels = %d, want 2", len(sr.levels))
	}
	for i, l := range sr.levels {
		if l.Status != LevelPending {
			t.Errorf("level %d status = %q, want pending", i, l.Status)
		}
		if l.LevelIdx != i+1 {
			t.Errorf("level %d LevelIdx = %d, want %d", i, l.LevelIdx, i+1)
		}
	}

	var dbCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategy_levels WHERE cycle_id=$1`, sr.cycle.ID).Scan(&dbCount); err != nil {
		t.Fatalf("count levels: %v", err)
	}
	if dbCount != 2 {
		t.Errorf("DB strategy_levels rows = %d, want 2", dbCount)
	}
}
```

Note: this test calls `sr.startAlsCycle(ctx)` directly, without holding `startMu` — safe
here because the test is single-goroutine and nothing else touches this `sr` instance
concurrently, but production callers (the dispatchers wired in the foundations plan) must
still hold `startMu` per `startAlsCycle`'s own doc comment.

- [ ] **Step 2: Run to confirm it fails to compile**

Run: `go test ./pkg/strategy/... -tags integration -run TestStartAlsCycle_SeedsOneLevelRowPerThreshold -v`
Expected: compile error (`startAlsCycle` still returns `nil` unconditionally and does
nothing — the test's `sr.cycle == nil` assertion will fail once it compiles, not before;
fix the test file issues from Step 1 first if there are compile errors there).

- [ ] **Step 3: Implement `startAlsCycle`**

In `pkg/strategy/als.go`, replace the Task 4-foundations stub:

```go
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	return nil
}
```

with:

```go
// startAlsCycle creates a new strategy_cycles row and seeds one pending strategy_levels
// row per sr.strategy.AlsThresholds entry — target_price is left at 0 here and computed
// fresh on every alsPriceTick call (Task 3) from the main position's LIVE avg entry, not
// fixed at cycle-start time, since the design requires each threshold's trigger point to
// track the main position's entry as it moves (e.g. via its own DCA).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var cycleID string
	var cycleNum int
	err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at)
		 VALUES ($1, COALESCE((SELECT MAX(cycle_num)+1 FROM strategy_cycles WHERE strategy_id=$1), 1), NOW())
		 RETURNING id, cycle_num`,
		sr.strategy.ID,
	).Scan(&cycleID, &cycleNum)
	if err != nil {
		return fmt.Errorf("startAlsCycle: insert cycle: %w", err)
	}
	sr.cycle = &Cycle{ID: cycleID, StrategyID: sr.strategy.ID, CycleNum: cycleNum}
	sr.levels = nil

	for i, th := range sr.strategy.AlsThresholds {
		levelIdx := i + 1
		side := "Buy"
		if sr.strategy.Direction == DirectionShort {
			side = "Sell"
		}
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
			 VALUES ($1,$2,$3,$4,0,0,'0','pending') RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side,
		).Scan(&levelID); err != nil {
			return fmt.Errorf("startAlsCycle: insert level %d: %w", levelIdx, err)
		}
		sr.levels = append(sr.levels, GridLevel{
			ID: levelID, LevelIdx: levelIdx, Side: side, Status: LevelPending,
		})
	}

	sr.info(ctx, fmt.Sprintf("ALS: цикл %d запущен, уровней: %d", cycleNum, len(sr.strategy.AlsThresholds)))
	return nil
}
```

Check that `fmt` is already imported in `pkg/strategy/als.go` (it will be, from Task 1's
functions using `%` operators only — Task 1 doesn't actually use `fmt`, so add the import
now): add `"fmt"` to the file's `import` block if not already present (check with
`head -10 pkg/strategy/als.go` before editing).

- [ ] **Step 4: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestStartAlsCycle_SeedsOneLevelRowPerThreshold -v`
Expected: `PASS`.

- [ ] **Step 5: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_cycle_start_test.go
git commit -m "Implement startAlsCycle: seed strategy_levels from AlsThresholds"
```

---

### Task 3: Price monitor and top-up placement

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_price_tick_test.go`

This is the core trading loop. On every price tick: for each pending level whose
threshold has crossed, compute the top-up (Task 1's pure functions) using the main
position's live avg entry (`GetPositionAvgEntry`) and live volume
(`sr.effectiveDeposit(currentPrice)` — already resolves to the opposite-direction
position's USDT value when `SizeAsMain` is set, same helper Matrix already uses) and the
hedge's own already-open volume (`GetPositionSizeCoins` for THIS strategy's own
direction/positionIdx, times currentPrice), then place a market order for the delta and
mark the level `placed`.

- [ ] **Step 1: Write the failing test**

Create `pkg/strategy/als_price_tick_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed is the core engine's end-to-end
// regression test: a long main position with a live avg entry of 100 USDT/coin and 10
// coins. One threshold at -10%/target 30%. Note mainUsdt is effectiveDeposit(currentPrice)
// — sizeCoins × the TICK price, not the avg entry — so at the tick price used below (85)
// mainUsdt is 10×85=850, not 10×100=1000.
func TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tick-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-1", OrderLinkId: "link-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	// Main is LONG (this hedge is SHORT, direction=short, so the opposite/main positionIdx is 1).
	setWSPosition(ar, "ALSTICKUSDT", 1, 10, 100) // main: 10 coins @ avg entry 100 (mainUsdt depends on the TICK price below, not this entry price)
	// No hedge position open yet.

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSTICKUSDT','short','als','active',true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID,
	).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSTICKUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	// Main is LONG at avg entry 100 — adverse for a long main is price FALLING. This
	// hedge's own direction is Short, so per alsThresholdPrice's convention (hedge Short
	// ⟹ main Long ⟹ trigger lands BELOW base): 100*(1+(-10)/100)=90. Tick at 85, below
	// it, to cross.
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.Side != "Sell" {
		t.Errorf("Side = %q, want Sell (short hedge)", got.Side)
	}
	// mainUsdt = effectiveDeposit(85) = 10*85 = 850. target 30% of 850 = 255 USDT,
	// at price 85 → 255/85 = 3.0 exactly.
	if got.Qty != "3.000" {
		t.Errorf("Qty = %q, want 3.000 (255 USDT / 85)", got.Qty)
	}

	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %q, want placed", sr.levels[0].Status)
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed -v`
Expected: compile error — `alsPriceTick` doesn't exist yet.

- [ ] **Step 3: Implement `alsPriceTick` and its order-placement helper**

In `pkg/strategy/als.go`, add:

```go
// alsMainPositionIdx returns the positionIdx of the MAIN position opposite this ALS
// hedge strategy's own direction — mirrors effectiveDeposit's identical mapping
// (cycle.go): hedge long → main is short (2), hedge short → main is long (1).
func alsMainPositionIdx(hedgeDir Direction) int {
	if hedgeDir == DirectionLong {
		return 2
	}
	return 1
}

// alsOwnPositionIdx returns this ALS strategy's OWN positionIdx (the hedge side),
// mirroring positionIdxForOpen's convention used throughout cycle.go/matrix.go.
func alsOwnPositionIdx(hedgeDir Direction, hedgeMode bool) int {
	return positionIdxForOpen(hedgeMode, alsSideForDirection(hedgeDir))
}

func alsSideForDirection(dir Direction) string {
	if dir == DirectionShort {
		return "Sell"
	}
	return "Buy"
}

// alsPriceTick evaluates every pending threshold level against currentPrice, placing a
// top-up order for any that have crossed. Must be called with sr.mu held.
func (sr *StrategyRunner) alsPriceTick(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil || currentPrice <= 0 {
		return
	}
	avgEntry := sr.runner.GetPositionAvgEntry(sr.strategy.Symbol, alsMainPositionIdx(sr.strategy.Direction))
	if avgEntry <= 0 {
		return // main position not yet known — nothing to measure thresholds against
	}
	mainUsdt := sr.effectiveDeposit(currentPrice)
	if mainUsdt <= 0 {
		return
	}
	ownIdx := alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode)
	alreadyOpenCoins := sr.runner.GetPositionSizeCoins(sr.strategy.Symbol, ownIdx)
	alreadyOpenUsdt := alreadyOpenCoins * currentPrice

	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending {
			continue
		}
		if l.LevelIdx-1 >= len(sr.strategy.AlsThresholds) {
			continue
		}
		th := sr.strategy.AlsThresholds[l.LevelIdx-1]
		var lastOrderPrice float64
		for j := i - 1; j >= 0; j-- {
			if sr.levels[j].Status == LevelFilled || sr.levels[j].Status == LevelPlaced {
				lastOrderPrice = sr.levels[j].FilledPrice
				if lastOrderPrice == 0 {
					lastOrderPrice = sr.levels[j].TargetPrice
				}
				break
			}
		}
		if lastOrderPrice == 0 {
			lastOrderPrice = avgEntry
		}
		triggerPrice := alsThresholdPrice(sr.strategy.Direction, th.TriggerType, th.TriggerPct, avgEntry, lastOrderPrice)
		if !alsThresholdCrossed(sr.strategy.Direction, currentPrice, triggerPrice) {
			continue
		}
		topUpUsdt := alsTopUpUsdt(th.TargetHedgePct, mainUsdt, alreadyOpenUsdt)
		if topUpUsdt <= 0 {
			// Target already met by existing volume — mark this level placed with no
			// order so it doesn't keep re-evaluating every tick.
			l.Status = LevelPlaced
			l.TargetPrice = triggerPrice
			continue
		}
		sr.alsPlaceTopUp(ctx, l, triggerPrice, topUpUsdt, currentPrice)
		alreadyOpenUsdt += topUpUsdt // so a later level in the SAME tick sees this one's contribution
	}
}

// alsPlaceTopUp places one threshold level's top-up order at market and marks it placed
// on success. Must be called with sr.mu held.
func (sr *StrategyRunner) alsPlaceTopUp(ctx context.Context, l *GridLevel, triggerPrice, topUpUsdt, currentPrice float64) {
	qty := trader.FormatQty(topUpUsdt/currentPrice, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "0" || qty == "" {
		sr.warn(ctx, fmt.Sprintf("ALS L%d: объём округляется до нуля (%.2f USDT @ %.4f) — пропущено", l.LevelIdx, topUpUsdt, currentPrice))
		return
	}
	side := alsSideForDirection(sr.strategy.Direction)
	linkID := fmt.Sprintf("SIS_STR-%s-als-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        side,
		OrderType:   "Market",
		Qty:         qty,
		PositionIdx: alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId: linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS L%d: ошибка довыставления хеджа: %v", l.LevelIdx, err))
		return
	}

	l.Status = LevelPlaced
	l.TargetPrice = triggerPrice
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, target_price=$3, placed_at=NOW(), qty=$4 WHERE id=$5`,
		result.OrderId, linkID, triggerPrice, qty, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d: довыставлен хедж %.2f USDT (%s qty=%s) @ рынок", l.LevelIdx, topUpUsdt, side, qty))
}
```

Add `"sis/pkg/trader"` to `pkg/strategy/als.go`'s imports if not already present (check
with `head -10 pkg/strategy/als.go`).

- [ ] **Step 4: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed -v`
Expected: `PASS`.

- [ ] **Step 5: Write and run the delta-only second-level test**

Add to `pkg/strategy/als_price_tick_test.go`:

```go
// TestAlsPriceTick_SecondLevelOnlyToppedUpByDelta confirms the sizing is cumulative, not
// flat-add: level 1 already has 3 coins open, level 2's target is 50% of main — must top
// up only the remaining delta, not another flat 50%-of-main add.
//
// Main LONG, avgEntry=100, 10 coins live. Hedge direction Short (main Long ⟹ trigger
// lands BELOW base, per alsThresholdPrice's convention). Level 2: trigger_pct=-30 ⟹
// trigger = 100*(1+(-30)/100) = 70. Tick at 65 (below it) to cross. At that tick price,
// mainUsdt = effectiveDeposit(65) = 10*65 = 650; the hedge's own 3 already-open coins =
// 3*65 = 195 USDT; target = 50% of 650 = 325; top-up = 325-195 = 130 USDT; qty =
// 130/65 = 2.0 exactly — chosen deliberately so the result isn't sensitive to
// FormatQty's floor-rounding (confirmed via pkg/trader/instruments.go: FormatQty floors
// to the nearest QtyStep multiple, not round-to-nearest).
func TestAlsPriceTick_SecondLevelOnlyToppedUpByDelta(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tick2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-2", OrderLinkId: "link-2"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSTICK2USDT", alsMainPositionIdx(DirectionShort), 10, 100) // main: long, 10 coins @ avg entry 100
	setWSPosition(ar, "ALSTICK2USDT", alsOwnPositionIdx(DirectionShort, true), 3, 90) // hedge: 3 coins already open (filled at level 1's own trigger, 90)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSTICK2USDT','short','als','active',true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, l1ID, l2ID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',90,0,'3','placed') RETURNING id`,
		stratID, cycleID).Scan(&l1ID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,2,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID).Scan(&l2ID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSTICK2USDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true,
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30},
				{TriggerType: 1, TriggerPct: -30, TargetHedgePct: 50},
			},
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: l1ID, LevelIdx: 1, Side: "Sell", Status: LevelPlaced, TargetPrice: 90, Qty: "3"},
			{ID: l2ID, LevelIdx: 2, Side: "Sell", Status: LevelPending},
		},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 65)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.Qty != "2.000" {
		t.Errorf("Qty = %q, want 2.000 (130 USDT top-up / 65)", got.Qty)
	}
}
```

- [ ] **Step 6: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_SecondLevelOnlyToppedUpByDelta -v`
Expected: `PASS`. If `got.Qty` is formatted with a different number of decimal places than
`"2.000"` (e.g. `"2"` or `"2.0"`), that means `stepDecimals(0.001)` doesn't return 3 in
practice — read `pkg/trader/instruments.go`'s `stepDecimals` function, compute what it
actually returns for `0.001`, and correct this assertion to match reality (do not change
production code to match a wrong assertion).

- [ ] **Step 7: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_price_tick_test.go
git commit -m "Implement alsPriceTick: threshold crossing detection and target-based top-up placement"
```

---

### Task 4: Per-level breakeven stop on fill, reusing `matrixStopCondThreshold`/`matrixStopReplaceTrigger`

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_fill_test.go`

When a top-up order fills, place its own stop immediately (reusing `matrixSLTrigger`-style
math is NOT needed here — `StopCondPct`/`StopReplacePct` ARE the "Min ROI for hedge stop" /
"Stop Loss Break Even Hedge" pair; the stop only gets placed/replaced once price has moved
`StopCondPct`% in the hedge's favor, per `matrixStopCondThreshold`). Until that condition
fires, the level is simply unprotected (counted in `Vopen` for the Unsafe hedge percent
check — built in the next plan). If `InitialStopBEPct` is set, place that stop
immediately at fill time instead of leaving the level fully unprotected until
`StopCondPct` fires.

- [ ] **Step 1: Write the failing test**

Create `pkg/strategy/als_fill_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured confirms a filled level gets its
// InitialStopBEPct stop placed immediately (the "condé trailing" variant) rather than
// staying unprotected until StopCondPct fires later.
func TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-fill-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-stop-1", OrderLinkId: "link-stop-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	initialStopBE := 2.0
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode, initial_stop_be_pct)
		VALUES ($1,$2,'ALSFILLUSDT','short','als','active',true,$3) RETURNING id`,
		ownerID, accID, initialStopBE,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',110,0,'2.679','placed') RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	bePct := 2.0
	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSFILLUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, InitialStopBEPct: &bePct,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPlaced, Qty: "2.679"},
		},
	}

	sr.mu.Lock()
	sr.handleAlsLevelFill(ctx, levelID, 112.0)
	sr.mu.Unlock()

	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %q, want filled", sr.levels[0].Status)
	}
	if sr.levels[0].FilledPrice != 112.0 {
		t.Errorf("FilledPrice = %v, want 112.0", sr.levels[0].FilledPrice)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 (initial BE stop)", len(fake.placeOrderCalls))
	}
	stopCall := fake.placeOrderCalls[0]
	if stopCall.Side != "Buy" {
		t.Errorf("stop Side = %q, want Buy (closes a Sell/short hedge)", stopCall.Side)
	}
	if stopCall.OrderFilter != "StopOrder" {
		t.Errorf("stop OrderFilter = %q, want StopOrder", stopCall.OrderFilter)
	}
	// Short hedge filled at 112, InitialStopBEPct=2.0 → matrixStopReplaceTrigger(short, 112, 2.0) = 112*(1-2/100) = 109.76.
	if stopCall.TriggerPrice != "109.76" {
		t.Errorf("stop TriggerPrice = %q, want 109.76", stopCall.TriggerPrice)
	}
	// 109.76 < filledPrice 112 → the stop must fire on a FALL to/below it (triggerDirection=2),
	// not a rise (=1) — a hardcoded by-direction trigDir gets exactly this backwards for a
	// short hedge's breakeven stop (same bug class fixed live for Binance, 2026-10-18,
	// NEARUSDT grid DCA — see binanceConditionalOrderType's doc comment).
	if stopCall.TriggerDirection != 2 {
		t.Errorf("stop TriggerDirection = %d, want 2 (falls to/below — price moved favorably for the short hedge)", stopCall.TriggerDirection)
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured -v`
Expected: FAIL — `handleAlsLevelFill` is still the foundations plan's no-op stub.

- [ ] **Step 3: Implement `handleAlsLevelFill` and its stop-placement helper**

In `pkg/strategy/als.go`, replace:

```go
func (sr *StrategyRunner) handleAlsLevelFill(ctx context.Context, levelID string, filledPrice float64) {
}
```

with:

```go
// handleAlsLevelFill marks one ALS threshold level filled and, if InitialStopBEPct is
// configured, places its breakeven stop immediately (the "condé trailing" variant — see
// the design spec's Initial Stop Loss Break Even Hedge section). Without
// InitialStopBEPct, the level stays unprotected until matrixStopCondThreshold's
// condition fires on a later price tick (built in the polish plan's stop-cond-replace
// wiring, which reuses matrix.go's existing free functions directly).
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleAlsLevelFill(ctx context.Context, levelID string, filledPrice float64) {
	var l *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			l = &sr.levels[i]
			break
		}
	}
	if l == nil {
		return
	}
	l.Status = LevelFilled
	l.FilledPrice = filledPrice
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d исполнен @ %.4f", l.LevelIdx, filledPrice))

	if sr.strategy.InitialStopBEPct != nil {
		sr.alsPlaceLevelStop(ctx, l, filledPrice, *sr.strategy.InitialStopBEPct)
	}
}

// alsPlaceLevelStop places a breakeven-style stop for one filled ALS level at
// trigger = matrixStopReplaceTrigger(hedgeDirection, filledPrice, bePct) — reusing
// matrix.go's existing free function unchanged, since the math (trail behind price on
// the favorable side by bePct%) is identical regardless of strategy type. Side/
// triggerDirection are the OPPOSITE of the level's own open side (a stop CLOSES the
// hedge). Must be called with sr.mu held.
func (sr *StrategyRunner) alsPlaceLevelStop(ctx context.Context, l *GridLevel, filledPrice, bePct float64) {
	trigger := matrixStopReplaceTrigger(sr.strategy.Direction, filledPrice, bePct)
	closeSide := "Buy"
	if sr.strategy.Direction == DirectionLong {
		closeSide = "Sell"
	}
	// Determine triggerDirection by comparing trigger against filledPrice directly,
	// rather than hardcoding it from sr.strategy.Direction — matrixApplyStopCondSLs
	// (matrix.go) uses this exact same comparison-based approach, for the identical
	// reason documented there: a trigger fixed by direction alone gets this backwards
	// and the exchange rejects it outright. Found live for matrix (2026-09-16, ICXUSDT):
	// retCode=110092 "expect Rising, but trigger_price <= current" when trigDir was
	// hardcoded by direction instead of compared against where the trigger actually
	// landed relative to price.
	trigDir := 2 // Falling: price must drop to reach trigger
	if trigger > filledPrice {
		trigDir = 1 // Rising: price must climb to reach trigger
	}

	qty := l.Qty
	fmtQty := trader.FormatQty(mustParseFloat(qty), sr.instr.QtyStep, sr.instr.MinQty)
	if fmtQty == "0" || fmtQty == "" {
		sr.warn(ctx, fmt.Sprintf("ALS L%d: стоп qty округляется до нуля", l.LevelIdx))
		return
	}

	linkID := fmt.Sprintf("SIS_STR-%s-alssl-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "sl"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             closeSide,
		OrderType:        "Market",
		Qty:              fmtQty,
		TriggerPrice:     trader.FormatPrice(trigger, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS L%d: ошибка выставления стопа: %v", l.LevelIdx, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLLinkID = linkID
	l.SLPrice = trigger
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_link_id=$2, sl_price=$3 WHERE id=$4`,
		result.OrderId, linkID, trigger, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d: стоп @ %.4f выставлен", l.LevelIdx, trigger))
}

// mustParseFloat parses a qty string already validated upstream (DB-stored, always a
// valid float) — panics are acceptable here only because every caller controls the
// input format; mirrors the same trust level trader.FormatQty's own callers already
// place in their qty strings throughout cycle.go/matrix.go.
func mustParseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
```

Check `strconv` is imported in `pkg/strategy/als.go` (add `"strconv"` to the imports if
not already present).

- [ ] **Step 4: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured -v`
Expected: `PASS`.

- [ ] **Step 5: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_fill_test.go
git commit -m "Implement handleAlsLevelFill: mark filled, place initial breakeven stop when configured"
```

---

### Task 5: Launch the price monitor from `startAlsCycle`/`resumeAlsCycle`

**Files:**
- Modify: `pkg/strategy/als.go`

- [ ] **Step 1: Add `launchAlsPriceMonitor`, mirroring `launchMatrixPriceMonitor`**

In `pkg/strategy/als.go`, add:

```go
// launchAlsPriceMonitor starts a goroutine feeding real-time mark prices into
// alsPriceTick — structurally identical to launchMatrixPriceMonitor (matrix.go), reused
// by copy rather than by call since the two have no shared state to coordinate through.
// Falls back to 5-second REST polling if the signal engine is unavailable.
// Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) launchAlsPriceMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel // reuses the same field — als and matrix are mutually exclusive per strategy
	stratID := sr.strategy.ID
	symbol := sr.strategy.Symbol
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	if se == nil {
		go sr.runAlsPriceMonitorPolling(ctx, cancel, stratID, symbol)
		return
	}

	priceCh := make(chan float64, 1)
	go func() {
		unsub := se.PriceHub().Subscribe("bybit", symbol, func(markPrice float64) {
			select {
			case <-priceCh:
			default:
			}
			select {
			case priceCh <- markPrice:
			default:
			}
		})
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
					sr.alsPriceTick(taskCtx, price)
				})
			}
		}
	}()
}

// runAlsPriceMonitorPolling is the REST fallback when signalEngine is nil — mirrors
// runMatrixPriceMonitorPolling exactly.
func (sr *StrategyRunner) runAlsPriceMonitorPolling(ctx context.Context, cancel context.CancelFunc, stratID, symbol string) {
	defer cancel()
	sr.mu.Lock()
	ex := sr.runner.Exchange()
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
			price, err := ex.GetMarkPrice(ctx, category, symbol)
			if err != nil {
				log.Printf("strategy %s: als price monitor: %v", stratID, err)
				continue
			}
			sr.submit(func(taskCtx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				sr.alsPriceTick(taskCtx, price)
			})
		}
	}
}
```

Add `"log"`, `"time"` to `pkg/strategy/als.go`'s imports if not already present.

Note the comment on `sr.matrixMonitorStop` — ALS reuses this `StrategyRunner` field
rather than adding a new one, since a given strategy is never both matrix and als at
once (mutually exclusive `StrategyType`), so there's no risk of the two monitors
colliding on the same field.

- [ ] **Step 2: Call it from `startAlsCycle`, `resumeAlsCycle`, `restartAlsCycle`**

In `startAlsCycle` (Task 2's implementation), right before the final `return nil`, add:

```go
	go sr.launchAlsPriceMonitor()
	return nil
```

In `pkg/strategy/als.go`, replace the foundations-plan stubs for `resumeAlsCycle` and
`restartAlsCycle`:

```go
func (sr *StrategyRunner) resumeAlsCycle(ctx context.Context) {
}

func (sr *StrategyRunner) restartAlsCycle(ctx context.Context) {
}
```

with:

```go
// resumeAlsCycle restarts the price monitor for an ALS cycle that was already running
// (e.g. after a process restart) — the cycle/levels themselves were already loaded by
// the generic loadActiveCycle path (same one grid/matrix use), so there's nothing to
// reconstruct here beyond relaunching the goroutine.
func (sr *StrategyRunner) resumeAlsCycle(ctx context.Context) {
	go sr.launchAlsPriceMonitor()
}

// restartAlsCycle re-applies strategy config to an already-running ALS cycle (settings
// changed mid-flight) — thresholds are re-read from sr.strategy on every alsPriceTick
// call already (no cached copy), so nothing needs rebuilding; just confirm the monitor
// is still running.
func (sr *StrategyRunner) restartAlsCycle(ctx context.Context) {
	sr.mu.Lock()
	monitorRunning := sr.matrixMonitorStop != nil
	sr.mu.Unlock()
	if !monitorRunning {
		go sr.launchAlsPriceMonitor()
	}
}
```

- [ ] **Step 3: Build and run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 4: Commit**

```bash
git add pkg/strategy/als.go
git commit -m "Launch ALS price monitor from cycle start/resume/restart"
```

---

## What this plan does NOT do (see `2026-10-18-als-hedge-polish.md`, next)

- Unsafe hedge percent clamping — top-ups are placed at full computed size with no cap
  yet.
- Virtual stop-order memory-bank — `MaxStopActive` is read from config but nothing checks
  it for ALS yet (every level's stop is placed unconditionally).
- TP pause/resume on the main position.
- `stop_cond_pct`/`stop_replace_pct`-driven stop REPLACEMENT after the initial stop (only
  the one-time `InitialStopBEPct` placement at fill exists so far — the ongoing
  `matrixApplyStopCondSLs`-equivalent re-evaluation on every tick is not yet wired for
  ALS levels).
- Safe Zone re-entry blocking for ALS (levels never re-open once placed in this plan —
  that's correct for a forward-only threshold ladder, so this may turn out to be a
  non-issue; confirmed/revisited in the polish plan once memory-bank introduces a case
  where a level's stop fires and the level needs to become eligible again).
