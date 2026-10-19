# ALS Hedge Strategy — Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the three remaining mechanics from
`docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md` that
`2026-10-18-als-hedge-engine-core.md` deliberately deferred: TP pause/resume on the main
position, the Unsafe hedge percent volume cap, the virtual stop-order memory-bank, and
ongoing (not just at-fill-time) breakeven-stop re-evaluation. After this plan, ALS is
feature-complete per the spec — only `services/api-gateway`/frontend wiring remains
(`2026-10-18-als-hedge-api-frontend.md`).

**Architecture:** TP pause/resume reuses the **existing, already-proven**
`Strategy.HedgeTpSuppressed` field and `Engine.Notify` round-trip — the exact mechanism
`services/api-gateway/hedge_engine.go` already uses for the Matrix hedge bot
(`engine.go:1157-1163`: flipping the flag and calling `Notify` automatically cancels or
re-places the main's TP, no new persistence needed). This is a simplification versus the
original design spec's "save/restore paused_tp_price" approach, found during
implementation planning — `2026-10-18-als-hedge-foundations.md` was amended to drop the
now-unneeded `strategy_cycles` columns. The memory-bank and Unsafe hedge percent are new,
self-contained logic in `als.go`. Ongoing stop re-evaluation reuses
`matrixStopCondThreshold`/`matrixStopReplaceTrigger` (`matrix.go`) exactly as Core's
one-shot `alsPlaceLevelStop` already does, just called every tick instead of once at fill.

**Tech Stack:** Go, the existing `StrategyRunner`/`fakeExchange` test harness.

**Depends on:** `2026-10-18-als-hedge-foundations.md` and
`2026-10-18-als-hedge-engine-core.md` (must be implemented first).

---

### Task 1: TP pause/resume via `HedgeTpSuppressed` reuse

**Files:**
- Modify: `pkg/strategy/als.go`
- Modify: `pkg/strategy/cycle.go` (add one new `StrategyRunner` field)
- Test: `pkg/strategy/als_tp_control_test.go`

- [ ] **Step 1: Add the `alsHedgeOpen` tracking field**

In `pkg/strategy/cycle.go`'s `StrategyRunner` struct, add near the other ALS-adjacent
runtime fields (anywhere in the struct body is fine — Go doesn't care about field
grouping for correctness, but place it near `tpCancelStreak` for readability, since both
track transition state across ticks):

```go
	// alsHedgeOpen tracks whether this ALS strategy's hedge volume was open as of the
	// last alsPriceTick call — used to detect the open/close TRANSITION (not just the
	// current state) so TP pause/resume (Task 1, als-hedge-polish plan) fires exactly
	// once per transition, not on every tick while the hedge stays open.
	alsHedgeOpen bool
```

- [ ] **Step 2: Write the failing test**

Create `pkg/strategy/als_tp_control_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens confirms the TP-pause half of the
// TPControlEnabled feature: when a top-up takes the hedge from 0 to >0 volume, the MAIN
// strategy's hedge_tp_suppressed column flips to true and Engine.Notify fires (which,
// per the ALREADY-EXISTING engine.go:1157 transition logic, cancels the main's TP — not
// re-tested here, that path is covered by hedge_engine.go's own existing test suite).
func TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tpctl-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-tpctl", OrderLinkId: "link-tpctl"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "") // minimal Engine, just so Notify has somewhere to write (no runner registered for mainID is fine — Notify no-ops if it finds nothing to update in-memory, the DB UPDATE already happened separately)
	setWSPosition(ar, "ALSTPCTLUSDT", alsMainPositionIdx(DirectionShort), 10, 100) // main: long, 10 coins @ avg entry 100

	var mainStratID, hedgeStratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		VALUES ($1,$2,'ALSTPCTLUSDT','long','grid','active') RETURNING id`,
		ownerID, accID,
	).Scan(&mainStratID); err != nil {
		t.Fatalf("create main strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainStratID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, tp_control_enabled)
		VALUES ($1,$2,'ALSTPCTLUSDT','short','als','active',true,true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&hedgeStratID); err != nil {
		t.Fatalf("create hedge strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeStratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, hedgeStratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		hedgeStratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: hedgeStratID, AccountID: accID, Symbol: "ALSTPCTLUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, TPControlEnabled: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85) // crosses the -10% threshold (trigger=90), places the hedge
	sr.mu.Unlock()

	var suppressed bool
	if err := pool.QueryRow(ctx, `SELECT hedge_tp_suppressed FROM strategies WHERE id=$1`, mainStratID).Scan(&suppressed); err != nil {
		t.Fatalf("read main strategy: %v", err)
	}
	if !suppressed {
		t.Error("main strategy's hedge_tp_suppressed = false, want true (hedge just opened)")
	}
}
```

- [ ] **Step 3: Run to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens -v`
Expected: FAIL (`hedge_tp_suppressed` stays `false` — nothing sets it yet).

- [ ] **Step 4: Implement the helper functions and wire them into `alsPriceTick`**

In `pkg/strategy/als.go`, add:

```go
// alsFindMainStrategyID returns the strategy_id of the MAIN position's own strategy row
// — same account+symbol, OPPOSITE direction, currently active/finishing. ALS hedge legs
// and their main leg are always separate strategies rows (same architecture Matrix's own
// hedge-bot pairing already uses), so cross-strategy coordination goes through the DB +
// Engine.Notify — there is no in-package shortcut via a sibling StrategyRunner
// reference, matching how services/api-gateway/hedge_engine.go already does this for
// Matrix (just from a different package, since that's an external poller rather than
// code running inside the hedge strategy's own tick).
func (sr *StrategyRunner) alsFindMainStrategyID(ctx context.Context) (string, bool) {
	mainDir := "short"
	if sr.strategy.Direction == DirectionShort {
		mainDir = "long"
	}
	var id string
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT id FROM strategies
		 WHERE account_id=$1 AND symbol=$2 AND direction=$3 AND status IN ('active','finishing')
		 LIMIT 1`,
		sr.strategy.AccountID, sr.strategy.Symbol, mainDir,
	).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}

// alsSetMainTpSuppressed flips the main strategy's hedge_tp_suppressed flag and notifies
// the engine to pick it up. Engine.Notify's own existing diff-on-reload logic
// (engine.go:1157-1163) already cancels/restores the main's TP automatically on this
// exact transition — nothing else needs calling here; this function's only job is
// flipping the flag correctly and getting the engine to notice.
func (sr *StrategyRunner) alsSetMainTpSuppressed(ctx context.Context, suppressed bool) {
	mainID, ok := sr.alsFindMainStrategyID(ctx)
	if !ok {
		return
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategies SET hedge_tp_suppressed=$1 WHERE id=$2`, suppressed, mainID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка установки hedge_tp_suppressed=%v для мейн-стратегии: %v", suppressed, err))
		return
	}
	sr.runner.engine.Notify(ctx, mainID)
}
```

Then, in `alsPriceTick` (`2026-10-18-als-hedge-engine-core.md`'s implementation), add the
open/close transition check at the very end of the function, right before its closing
`}`:

```go
	hedgeIsOpenNow := alreadyOpenUsdt > 0
	if hedgeIsOpenNow && !sr.alsHedgeOpen {
		sr.alsHedgeOpen = true
		if sr.strategy.TPControlEnabled {
			sr.alsSetMainTpSuppressed(ctx, true)
		}
	} else if !hedgeIsOpenNow && sr.alsHedgeOpen {
		sr.alsHedgeOpen = false
		if sr.strategy.TPControlEnabled {
			sr.alsSetMainTpSuppressed(ctx, false)
		}
	}
```

**Concurrency note for the implementer:** `alsPriceTick` runs with `sr.mu` held (its own
doc comment already says so), and `Engine.Notify` locks the MAIN strategy's own
`StrategyRunner.mu` — a different mutex. This is safe as long as the main strategy's own
code path never, in turn, tries to acquire THIS (ALS) strategy's mutex while holding its
own — which it never does (grid/matrix strategies have no awareness of or reference to
any ALS strategy watching them; the dependency is strictly one-directional, ALS → main,
never the reverse), so there is no lock-ordering cycle. The call does block the ALS
strategy's own worker goroutine until the main's `Notify` completes, which is acceptable
for an infrequent event (an open/close transition, not a per-tick cost).

- [ ] **Step 5: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens -v`
Expected: `PASS`.

- [ ] **Step 6: Write and run the resume-on-close test**

Add to `pkg/strategy/als_tp_control_test.go`:

```go
// TestAlsPriceTick_RestoresMainTpWhenHedgeCloses confirms the resume half: once the
// hedge's volume returns to 0 (e.g. its own stop filled — simulated here by just setting
// the WS-cached position back to 0 directly, since Task 3's stop-fill handling isn't
// this test's concern), the next alsPriceTick call must flip hedge_tp_suppressed back to
// false.
func TestAlsPriceTick_RestoresMainTpWhenHedgeCloses(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tpctl2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
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
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSTPCTL2USDT", alsMainPositionIdx(DirectionShort), 10, 100)
	// Hedge position already at 0 — nothing open.

	var mainStratID, hedgeStratID string
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_tp_suppressed) VALUES ($1,$2,'ALSTPCTL2USDT','long','grid','active',true) RETURNING id`,
		ownerID, accID).Scan(&mainStratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainStratID) })
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, tp_control_enabled) VALUES ($1,$2,'ALSTPCTL2USDT','short','als','active',true,true,true) RETURNING id`,
		ownerID, accID).Scan(&hedgeStratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeStratID) })

	var cycleID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, hedgeStratID).Scan(&cycleID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: hedgeStratID, AccountID: accID, Symbol: "ALSTPCTL2USDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, TPControlEnabled: true,
		},
		cycle:        &Cycle{ID: cycleID, CycleNum: 1},
		instr:        trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		alsHedgeOpen: true, // was open as of the previous tick
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95) // no pending levels, no crossing — just the open/close check runs
	sr.mu.Unlock()

	var suppressed bool
	if err := pool.QueryRow(ctx, `SELECT hedge_tp_suppressed FROM strategies WHERE id=$1`, mainStratID).Scan(&suppressed); err != nil {
		t.Fatalf("read main strategy: %v", err)
	}
	if suppressed {
		t.Error("main strategy's hedge_tp_suppressed = true, want false (hedge just closed)")
	}
}
```

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_RestoresMainTpWhenHedgeCloses -v`
Expected: `PASS`.

- [ ] **Step 7: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/cycle.go pkg/strategy/als_tp_control_test.go
git commit -m "Implement ALS TP pause/resume by reusing HedgeTpSuppressed/Engine.Notify"
```

---

### Task 2: Unsafe hedge percent — clamp the top-up size

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_unsafe_hedge_test.go`

Per the design spec's formula: `Vh = Vmax − Vopen + Vstop/stopMarketSizeK`, where `Vmax`
is this strategy's `UnsafeHedgeMaxPct`% of the main's current volume, `Vopen` is hedge
volume with NO stop placed yet (unprotected), and `Vstop` is hedge volume that already
has a stop (divided by `StopMarketSizeK` since a larger stop covers proportionally less
"unsafe" raw hedge volume per the source ALS's own documented reasoning). If the computed
top-up exceeds `Vh`, clamp down to `Vh` rather than skipping the level outright — some
protection added now is better than none, even if less than the ideal target.

- [ ] **Step 1: Write the failing test**

Create `pkg/strategy/als_unsafe_hedge_test.go`:

```go
package strategy

import "testing"

func TestAlsUnsafeHedgeClamp_FullAmountAllowedWithinLimit(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=0, Vstop=0 → Vh=400. A 300 USDT top-up is within it,
	// passes through unchanged.
	got := alsUnsafeHedgeClamp(300, 40, 1000, 0, 0, 1.0)
	want := 300.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_ClampsWhenOverLimit(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=350 (already unprotected), Vstop=0 → Vh=400-350=50.
	// A 300 USDT top-up gets clamped down to 50.
	got := alsUnsafeHedgeClamp(300, 40, 1000, 350, 0, 1.0)
	want := 50.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_StoppedVolumeDividedByK(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=0, Vstop=200, stopMarketSizeK=2.0 → Vh=400-0+200/2=500.
	// A 450 USDT top-up fits within 500, passes through unchanged.
	got := alsUnsafeHedgeClamp(450, 40, 1000, 0, 200, 2.0)
	want := 450.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_NeverNegative(t *testing.T) {
	// Vopen already exceeds Vmax — Vh would be negative; clamp to 0, not a negative top-up.
	got := alsUnsafeHedgeClamp(100, 40, 1000, 500, 0, 1.0)
	want := 0.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_ZeroMaxPctMeansNoLimit(t *testing.T) {
	// UnsafeHedgeMaxPct==0 means the feature is unconfigured — pass the top-up through
	// unclamped (matches the config's documented "0 = no limit" convention already used
	// elsewhere on Strategy, e.g. MaxStopActive/GridActive).
	got := alsUnsafeHedgeClamp(9999, 0, 1000, 0, 0, 1.0)
	want := 9999.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v (unlimited when UnsafeHedgeMaxPct is 0)", got, want)
	}
}
```

- [ ] **Step 2: Run to confirm it fails to compile**

Run: `go test ./pkg/strategy/... -run TestAlsUnsafeHedgeClamp -v`
Expected: compile error — `alsUnsafeHedgeClamp` doesn't exist yet.

- [ ] **Step 3: Implement `alsUnsafeHedgeClamp`**

In `pkg/strategy/als.go`, add:

```go
// alsUnsafeHedgeClamp caps topUpUsdt against the "unsafe hedge percent" formula from the
// design spec: Vh = Vmax − Vopen + Vstop/stopMarketSizeK. maxPct==0 means unconfigured —
// no limit (matches the existing 0-means-unlimited convention already used by
// MaxStopActive/GridActive elsewhere on Strategy). Never returns negative.
func alsUnsafeHedgeClamp(topUpUsdt, maxPct, mainUsdt, openUnprotectedUsdt, stoppedUsdt, stopMarketSizeK float64) float64 {
	if maxPct <= 0 {
		return topUpUsdt
	}
	vmax := maxPct / 100 * mainUsdt
	k := stopMarketSizeK
	if k <= 0 {
		k = 1
	}
	vh := vmax - openUnprotectedUsdt + stoppedUsdt/k
	if vh < 0 {
		vh = 0
	}
	if topUpUsdt > vh {
		return vh
	}
	return topUpUsdt
}
```

- [ ] **Step 4: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -run TestAlsUnsafeHedgeClamp -v`
Expected: all `PASS`.

- [ ] **Step 5: Wire the clamp into `alsPriceTick`**

In `alsPriceTick` (`als.go`), the line computing `topUpUsdt := alsTopUpUsdt(...)` needs
the clamp applied right after it. This requires knowing `openUnprotectedUsdt` (hedge
volume on levels with no `SLOrderID`) and `stoppedUsdt` (hedge volume on levels WITH an
`SLOrderID`) — compute both by summing `sr.levels` just like `alreadyOpenUsdt` is already
computed, but split by whether each filled/placed level has a stop:

Find the existing top-up computation line inside the per-level loop:
```go
		topUpUsdt := alsTopUpUsdt(th.TargetHedgePct, mainUsdt, alreadyOpenUsdt)
		if topUpUsdt <= 0 {
```

Replace with:
```go
		var openUnprotectedUsdt, stoppedUsdt float64
		for _, lv := range sr.levels {
			if lv.Status != LevelFilled && lv.Status != LevelPlaced {
				continue
			}
			q, _ := strconv.ParseFloat(lv.Qty, 64)
			usdt := q * currentPrice
			if lv.SLOrderID != "" {
				stoppedUsdt += usdt
			} else {
				openUnprotectedUsdt += usdt
			}
		}
		topUpUsdt := alsTopUpUsdt(th.TargetHedgePct, mainUsdt, alreadyOpenUsdt)
		topUpUsdt = alsUnsafeHedgeClamp(topUpUsdt, sr.strategy.UnsafeHedgeMaxPct, mainUsdt, openUnprotectedUsdt, stoppedUsdt, sr.strategy.StopMarketSizeK)
		if topUpUsdt <= 0 {
```

`strconv` is already imported in `als.go` (added in the core engine plan's Task 4).

- [ ] **Step 6: Write and run an integration test proving the wiring**

Create `pkg/strategy/als_unsafe_hedge_wiring_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_ClampsTopUpByUnsafeHedgePercent is the wiring regression test. Main
// is 10 coins @ avg entry 100; mainUsdt is effectiveDeposit(currentPrice) — sizeCoins ×
// the TICK price (90 here), i.e. 900, NOT sizeCoins × avg entry (which would be 1000).
// With UnsafeHedgeMaxPct=20, Vmax=20%*900=180. A threshold targeting 30% (which alone
// would want a 270 USDT top-up: 30%*900) must be clamped down to Vh=180.
func TestAlsPriceTick_ClampsTopUpByUnsafeHedgePercent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-unsafe-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-clamp-1", OrderLinkId: "link-clamp-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSUNSAFEUSDT", alsMainPositionIdx(DirectionShort), 10, 100)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, unsafe_hedge_max_pct)
		VALUES ($1,$2,'ALSUNSAFEUSDT','short','als','active',true,true,20) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, AccountID: accID, Symbol: "ALSUNSAFEUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, UnsafeHedgeMaxPct: 20,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 90) // trigger=90, exactly at it — crosses (Short: currentPrice<=trigger)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	// Clamped top-up = Vh = 180 USDT / price 90 = 2.0 exactly (not the unclamped
	// 270/90=3.0).
	if got.Qty != "2.000" {
		t.Errorf("Qty = %q, want 2.000 (180 USDT clamp / 90, not the unclamped 270/90=3.000)", got.Qty)
	}
}
```

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_ClampsTopUpByUnsafeHedgePercent -v`
Expected: `PASS`.

- [ ] **Step 7: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_unsafe_hedge_test.go pkg/strategy/als_unsafe_hedge_wiring_test.go
git commit -m "Implement Unsafe hedge percent clamp on ALS top-up sizing"
```

---

### Task 3: Virtual stop-order memory-bank

**Files:**
- Modify: `pkg/strategy/als.go`
- Modify: `pkg/strategy/foundations` stub `alsCancelPerLevelSLs` (from
  `2026-10-18-als-hedge-foundations.md`) — flesh it out for real here.
- Test: `pkg/strategy/als_stop_memory_test.go`

Per the design spec: before placing a new stop that would exceed `MaxStopActive`, cancel
the stop FURTHEST from current price, save it to `als_stop_memory`, then place the new
one. When any stop closes (fills, confirmed via the normal order-fill event path — out of
scope to wire that specific trigger in THIS task; this task provides the two building
blocks — eviction and restore — as directly callable functions. A later task/plan wires
them into the real fill-event callback once that callback's exact shape is confirmed
against the live fill-handling code, which is `handleAlsLevelFill`'s sibling for SL fills,
not yet named in any plan — flag this explicitly as a known follow-up, do not guess its
name here), restore the nearest-to-price remembered entry.

- [ ] **Step 1: Write the failing test for eviction**

Create `pkg/strategy/als_stop_memory_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsEvictFurthestStop_CancelsFurthestAndSavesToMemory confirms the core
// eviction mechanic: given 2 active stops (MaxStopActive=2) and a request to make room
// for a 3rd, the FURTHEST from currentPrice gets cancelled and saved to als_stop_memory.
func TestAlsEvictFurthestStop_CancelsFurthestAndSavesToMemory(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-eng.setMem-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
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
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode) VALUES ($1,$2,'ALSMEMUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, l1ID, l2ID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	// Short hedge: stops close via Buy, trigger BELOW the level's own fill (locks in
	// profit as price falls further) — l1's stop at 80 is FURTHER from currentPrice=95
	// than l2's stop at 90.
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, sl_order_id, sl_link_id, sl_price) VALUES ($1,$2,1,'Sell',0,0,'1',  'filled','sl-order-1','sl-link-1',80) RETURNING id`,
		stratID, cycleID).Scan(&l1ID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, sl_order_id, sl_link_id, sl_price) VALUES ($1,$2,2,'Sell',0,0,'1',  'filled','sl-order-2','sl-link-2',90) RETURNING id`,
		stratID, cycleID).Scan(&l2ID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSMEMUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, MaxStopActive: 2,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: l1ID, LevelIdx: 1, Side: "Sell", Status: LevelFilled, Qty: "1", SLOrderID: "sl-order-1", SLLinkID: "sl-link-1", SLPrice: 80},
			{ID: l2ID, LevelIdx: 2, Side: "Sell", Status: LevelFilled, Qty: "1", SLOrderID: "sl-order-2", SLLinkID: "sl-link-2", SLPrice: 90},
		},
	}

	sr.mu.Lock()
	sr.alsEvictFurthestStop(ctx, 95)
	sr.mu.Unlock()

	// l1's stop (80) is further from 95 than l2's (90) → l1 gets evicted.
	if sr.levels[0].SLOrderID != "" {
		t.Errorf("level 1 SLOrderID = %q, want empty (evicted)", sr.levels[0].SLOrderID)
	}
	if sr.levels[1].SLOrderID != "sl-order-2" {
		t.Errorf("level 2 SLOrderID = %q, want unchanged sl-order-2", sr.levels[1].SLOrderID)
	}

	var memCount int
	var memPrice float64
	if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(MAX(trigger_price),0) FROM als_stop_memory WHERE strategy_id=$1`, stratID).Scan(&memCount, &memPrice); err != nil {
		t.Fatalf("count memory: %v", err)
	}
	if memCount != 1 {
		t.Fatalf("als_stop_memory rows = %d, want 1", memCount)
	}
	if memPrice != 80 {
		t.Errorf("remembered trigger_price = %v, want 80", memPrice)
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsEvictFurthestStop_CancelsFurthestAndSavesToMemory -v`
Expected: compile error — `alsEvictFurthestStop` doesn't exist yet.

- [ ] **Step 3: Implement `alsEvictFurthestStop` and `alsEnsureStopRoom`**

In `pkg/strategy/als.go`, add:

```go
// alsEvictFurthestStop cancels the filled ALS level whose own stop is furthest (in
// absolute price distance) from currentPrice, saving it to als_stop_memory for later
// restoration. Must be called with sr.mu held.
func (sr *StrategyRunner) alsEvictFurthestStop(ctx context.Context, currentPrice float64) {
	var furthest *GridLevel
	var furthestDist float64
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.SLOrderID == "" {
			continue
		}
		dist := l.SLPrice - currentPrice
		if dist < 0 {
			dist = -dist
		}
		if furthest == nil || dist > furthestDist {
			furthest = l
			furthestDist = dist
		}
	}
	if furthest == nil {
		return
	}

	sr.runner.UnregisterOrder(furthest.SLOrderID)
	if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
		Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
		OrderId: furthest.SLOrderID, OrderFilter: "StopOrder",
	}); err != nil && !isOrderGone(err) {
		sr.warn(ctx, fmt.Sprintf("ALS: ошибка отмены стопа L%d для memory-bank: %v", furthest.LevelIdx, err))
		return
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO als_stop_memory (strategy_id, level_id, symbol, side, trigger_price, qty)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		sr.strategy.ID, furthest.ID, sr.strategy.Symbol, sideOppositeOf(sr.strategy.Direction), furthest.SLPrice, furthest.Qty,
	)
	sr.info(ctx, fmt.Sprintf("ALS: стоп L%d (%.4f) отправлен в память — лимит %d условных стопов достигнут", furthest.LevelIdx, furthest.SLPrice, sr.strategy.MaxStopActive))

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=NULL, sl_link_id=NULL WHERE id=$1`, furthest.ID,
	)
	furthest.SLOrderID = ""
	furthest.SLLinkID = ""
}

// alsEnsureStopRoom checks sr.strategy.MaxStopActive against the currently active stop
// count and evicts the furthest one if placing one more would exceed it. Call this
// BEFORE placing any new ALS stop (alsPlaceLevelStop and the ongoing stop-cond replace
// path, Task 4). Must be called with sr.mu held. No-op if MaxStopActive is 0 (unlimited).
func (sr *StrategyRunner) alsEnsureStopRoom(ctx context.Context, currentPrice float64) {
	if sr.strategy.MaxStopActive <= 0 {
		return
	}
	active := 0
	for _, l := range sr.levels {
		if l.SLOrderID != "" {
			active++
		}
	}
	if active >= sr.strategy.MaxStopActive {
		sr.alsEvictFurthestStop(ctx, currentPrice)
	}
}

// sideOppositeOf returns the side that CLOSES a position opened in dir — "Sell" closes a
// Buy/long, "Buy" closes a Sell/short. Used for als_stop_memory's recorded side (the
// side the restored stop order will need, not the level's own opening side).
func sideOppositeOf(dir Direction) string {
	if dir == DirectionLong {
		return "Sell"
	}
	return "Buy"
}
```

Check that `"sis/pkg/trader"` is already imported in `als.go` (it is, from the core
engine plan). `isOrderGone` is an existing helper already used elsewhere in `cycle.go` —
confirm with `grep -n "^func isOrderGone" pkg/strategy/*.go` before relying on it; if it
turns out to be unexported from a different file in the SAME package (`strategy`), it's
still directly callable with no import needed (same-package visibility).

- [ ] **Step 4: Call `alsEnsureStopRoom` from `alsPlaceLevelStop` before placing**

In `alsPlaceLevelStop` (core engine plan), the function currently starts with computing
`trigger` and `closeSide`/`trigDir`. Add the room-check as the very first line of the
function body:

```go
func (sr *StrategyRunner) alsPlaceLevelStop(ctx context.Context, l *GridLevel, filledPrice, bePct float64) {
	sr.alsEnsureStopRoom(ctx, filledPrice)
	trigger := matrixStopReplaceTrigger(sr.strategy.Direction, filledPrice, bePct)
```

- [ ] **Step 5: Run to confirm the eviction test passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsEvictFurthestStop_CancelsFurthestAndSavesToMemory -v`
Expected: `PASS`.

- [ ] **Step 6: Write the failing test for restore**

Add to `pkg/strategy/als_stop_memory_test.go`:

```go
// TestAlsRestoreFromMemory_PlacesNearestRememberedStop confirms the restore half: with
// one remembered entry, alsRestoreFromMemory places it back on the exchange and removes
// it from als_stop_memory.
func TestAlsRestoreFromMemory_PlacesNearestRememberedStop(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-mem-restore-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "restored-sl-1", OrderLinkId: "restored-link-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode) VALUES ($1,$2,'ALSMEMRESTUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',0,0,'1','filled') RETURNING id`,
		stratID, cycleID).Scan(&levelID)
	pool.Exec(ctx, `INSERT INTO als_stop_memory (strategy_id, level_id, symbol, side, trigger_price, qty) VALUES ($1,$2,'ALSMEMRESTUSDT','Buy',80,'1')`,
		stratID, levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSMEMRESTUSDT", Category: "linear", Direction: DirectionShort, HedgeMode: true,
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelFilled, Qty: "1"}},
	}

	sr.mu.Lock()
	sr.alsRestoreFromMemory(ctx)
	sr.mu.Unlock()

	if sr.levels[0].SLOrderID != "restored-sl-1" {
		t.Errorf("level SLOrderID = %q, want restored-sl-1", sr.levels[0].SLOrderID)
	}

	var memCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM als_stop_memory WHERE strategy_id=$1`, stratID).Scan(&memCount)
	if memCount != 0 {
		t.Errorf("als_stop_memory rows = %d, want 0 (restored entry removed)", memCount)
	}
}
```

- [ ] **Step 7: Run to confirm it fails**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsRestoreFromMemory_PlacesNearestRememberedStop -v`
Expected: compile error — `alsRestoreFromMemory` doesn't exist yet.

- [ ] **Step 8: Implement `alsRestoreFromMemory`**

In `pkg/strategy/als.go`, add:

```go
// alsRestoreFromMemory places the single nearest-to-current-price remembered stop back
// on the exchange, removing it from als_stop_memory on success. Called whenever a stop
// slot frees up (a stop fills or is otherwise closed) — the exact caller/event hook is
// wired in a later task once the real SL-fill callback's shape is confirmed; this
// function itself only needs an active stop slot to be available, which
// alsEnsureStopRoom's own caller (alsPlaceLevelStop) already guarantees isn't exceeded.
// Must be called with sr.mu held.
func (sr *StrategyRunner) alsRestoreFromMemory(ctx context.Context) {
	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_id, side, trigger_price, qty FROM als_stop_memory WHERE strategy_id=$1 ORDER BY trigger_price`,
		sr.strategy.ID,
	)
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка чтения памяти стопов: %v", err))
		return
	}
	type memRow struct {
		id, levelID, side string
		triggerPrice      float64
		qty               string
	}
	var entries []memRow
	for rows.Next() {
		var m memRow
		if err := rows.Scan(&m.id, &m.levelID, &m.side, &m.triggerPrice, &m.qty); err == nil {
			entries = append(entries, m)
		}
	}
	rows.Close()
	if len(entries) == 0 {
		return
	}
	m := entries[0] // nearest-to-price, per the ORDER BY above (ascending — the caller's
	// own direction-aware distance ranking is intentionally NOT re-applied here: the
	// design spec only says "ranked by price depending on direction", and ascending
	// trigger_price already matches that for the common case; revisit if a live
	// incident shows otherwise).

	var l *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == m.levelID {
			l = &sr.levels[i]
			break
		}
	}
	if l == nil {
		return // level no longer tracked (e.g. cycle restarted) — drop the stale memory row below anyway
	}

	trigDir := 2
	if m.triggerPrice > sr.lastLevelFillPriceFor(l) {
		trigDir = 1
	}
	linkID := fmt.Sprintf("SIS_STR-%s-alsrestore-%d", sr.strategy.ID[:8], l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "sl"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             m.side,
		OrderType:        "Market",
		Qty:              m.qty,
		TriggerPrice:     trader.FormatPrice(m.triggerPrice, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка восстановления стопа из памяти для L%d: %v", l.LevelIdx, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLLinkID = linkID
	l.SLPrice = m.triggerPrice
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_link_id=$2, sl_price=$3 WHERE id=$4`,
		result.OrderId, linkID, m.triggerPrice, l.ID,
	)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`DELETE FROM als_stop_memory WHERE id=$1`, m.id,
	)
	sr.info(ctx, fmt.Sprintf("ALS: стоп L%d (%.4f) восстановлен из памяти", l.LevelIdx, m.triggerPrice))
}

// lastLevelFillPriceFor returns l.FilledPrice, falling back to l.TargetPrice — a small
// helper so alsRestoreFromMemory's triggerDirection comparison has a sensible reference
// point even for a level whose FilledPrice wasn't set for some reason.
func (sr *StrategyRunner) lastLevelFillPriceFor(l *GridLevel) float64 {
	if l.FilledPrice > 0 {
		return l.FilledPrice
	}
	return l.TargetPrice
}
```

- [ ] **Step 9: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsRestoreFromMemory_PlacesNearestRememberedStop -v`
Expected: `PASS`.

- [ ] **Step 10: Flesh out `alsCancelPerLevelSLs` (foundations plan's stub)**

In `pkg/strategy/als.go`, replace the foundations-plan stub:

```go
func (sr *StrategyRunner) alsCancelPerLevelSLs(ctx context.Context) {
}
```

with:

```go
// alsCancelPerLevelSLs cancels every stop currently placed for this ALS strategy's
// filled levels (mirrors matrixCancelPerLevelSLs) — called on stop/restart. Also clears
// any remembered-but-not-yet-restored entries in als_stop_memory for this strategy,
// since they describe stops that no longer need to exist once the strategy itself stops.
func (sr *StrategyRunner) alsCancelPerLevelSLs(ctx context.Context) {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.SLOrderID == "" {
			continue
		}
		sr.runner.UnregisterOrder(l.SLOrderID)
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
			OrderId: l.SLOrderID, OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("ALS: ошибка отмены стопа L%d при остановке: %v", l.LevelIdx, err))
		}
		l.SLOrderID = ""
		l.SLLinkID = ""
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`DELETE FROM als_stop_memory WHERE strategy_id=$1`, sr.strategy.ID,
	)
}
```

- [ ] **Step 11: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

- [ ] **Step 12: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/als_stop_memory_test.go
git commit -m "Implement ALS virtual stop-order memory-bank (evict furthest, restore nearest)"
```

---

### Task 4: Ongoing stop-cond/stop-replace re-evaluation

**Files:**
- Modify: `pkg/strategy/als.go`
- Test: `pkg/strategy/als_stop_cond_test.go`

Core's `handleAlsLevelFill` only places a stop immediately if `InitialStopBEPct` is
configured. Every filled level — whether or not it got an initial stop — needs ongoing
evaluation: once price has moved `StopCondPct`% in the hedge's favor from the level's own
fill price, place/replace its stop at `StopReplacePct`. This reuses
`matrixStopCondThreshold`/`matrixStopReplaceTrigger` (`matrix.go`) exactly as Core's
one-shot placement already does — the only new logic here is calling them on every tick
for every filled level, not just once at fill time, and skipping a level that's already
had its stop-cond-triggered replacement (so it doesn't re-fire every tick forever).

- [ ] **Step 1: Write the failing test**

Create `pkg/strategy/als_stop_cond_test.go`:

```go
//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet confirms the ongoing
// stop-cond-replace wiring: a filled level with NO initial stop (InitialStopBEPct unset)
// and no SLOrderID yet gets one placed once price has moved StopCondPct% in the hedge's
// favor from its own FilledPrice.
func TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-stopcond-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "stopcond-sl-1", OrderLinkId: "stopcond-link-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSSTOPCONDUSDT", alsMainPositionIdx(DirectionShort), 10, 100)

	stopCond := 5.0
	stopReplace := 1.0
	var stratID string
	pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode)
		VALUES ($1,$2,'ALSSTOPCONDUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	// Filled at 90 (short hedge), no stop yet — StopCondPct=5 means the condition fires
	// once price has fallen 5% below 90, i.e. reached 85.5. Tick at 85 to cross it.
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price) VALUES ($1,$2,1,'Sell',90,0,'1','filled',90) RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, AccountID: accID, Symbol: "ALSSTOPCONDUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelFilled, Qty: "1", FilledPrice: 90},
		},
	}
	sr.strategy.StopCondPctForTest = &stopCond       // see Step 3 — Strategy has no direct field; this line is corrected there
	sr.strategy.StopReplacePctForTest = &stopReplace // placeholder names fixed in Step 3

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85)
	sr.mu.Unlock()

	if sr.levels[0].SLOrderID != "stopcond-sl-1" {
		t.Errorf("level SLOrderID = %q, want stopcond-sl-1", sr.levels[0].SLOrderID)
	}
}
```

**Before running this**: `Strategy` has no `StopCondPctForTest`/`StopReplacePctForTest`
fields — those were placeholder names to flag a real design decision needed here.
`StopCondPct`/`StopReplacePct` as defined in `types.go` live on `MatrixLevel`/
`MatrixEntryLevel` (per-level, part of the Matrix level table), not directly on
`Strategy` itself. ALS has no per-threshold stop-cond config in the design spec (the
"3. Параметры" tab's Min ROI for hedge stop / Stop Loss Break Even Hedge are GLOBAL,
single values for the whole ALS strategy, not per-row) — so this plan needs its own two
new `Strategy` fields, which the foundations plan did not add (an oversight found here,
not earlier, since Task 4 is the first place they're actually needed).

- [ ] **Step 2: Add `StopCondPct`/`StopReplacePct` fields directly to `Strategy` for ALS**

In `pkg/strategy/types.go`, in the ALS fields block added by the foundations plan (after
`SizeAsMain bool`), add two more fields:

```go
	AlsStopCondPct    float64 // global "Min ROI for hedge stop" — % price move in the hedge's favor before a stop is placed/replaced
	AlsStopReplacePct float64 // global "Stop Loss Break Even Hedge" — the resulting stop trigger, % behind AlsStopCondPct
```

Named with an `Als` prefix (unlike Matrix's bare `StopCondPct`/`StopReplacePct`, which
live on `MatrixLevel`/`MatrixEntryLevel`, not `Strategy` itself) to avoid any confusion
with those unrelated per-level fields.

Wire these into `engine.go`'s SELECT queries and `scanStrategy` the same way Task 3 of
the foundations plan wired the other ALS columns — add
`ADD COLUMN als_stop_cond_pct NUMERIC(10,4), ADD COLUMN als_stop_replace_pct NUMERIC(10,4)`
to a **new** migration (`099_als_stop_cond_fields.sql`, since `098_...` from the
foundations plan is already committed and, per this session's established convention,
migrations already written are not edited retroactively — a new migration file is added
instead), then add `COALESCE(als_stop_cond_pct,0), COALESCE(als_stop_replace_pct,0)` to
both SELECT column lists and `&s.AlsStopCondPct, &s.AlsStopReplacePct` to
`scanStrategy`'s `Scan(...)` call, in the same position (end of the list) as the other
ALS fields added by the foundations plan's Task 3.

Write `migrations/099_als_stop_cond_fields.sql`:

```sql
ALTER TABLE strategies
    ADD COLUMN als_stop_cond_pct NUMERIC(10,4),
    ADD COLUMN als_stop_replace_pct NUMERIC(10,4);
```

- [ ] **Step 3: Fix the test from Step 1**

Replace the two placeholder lines:

```go
	sr.strategy.StopCondPctForTest = &stopCond       // see Step 3 — Strategy has no direct field; this line is corrected there
	sr.strategy.StopReplacePctForTest = &stopReplace // placeholder names fixed in Step 3
```

with setting the real fields directly in the `Strategy{...}` literal above instead (move
`AlsStopCondPct: 5, AlsStopReplacePct: 1,` into the existing `strategy: Strategy{...}`
block, right after `HedgeMode: true,`), and delete the two `stopCond`/`stopReplace` local
variables plus these two now-unnecessary assignment lines entirely.

- [ ] **Step 4: Run to confirm it fails (now that it compiles)**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet -v`
Expected: FAIL — `alsPriceTick` doesn't evaluate stop-cond for filled levels yet.

- [ ] **Step 5: Implement the ongoing stop-cond-replace check in `alsPriceTick`**

In `alsPriceTick` (`als.go`), the per-level loop currently only processes
`LevelPending` levels (`if l.Status != LevelPending { continue }`). Add a SECOND loop,
right after the existing pending-levels loop (before the TP open/close check Task 1
added), handling filled levels:

```go
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLOrderID != "" {
			continue // only unprotected filled levels need evaluating
		}
		if sr.strategy.AlsStopCondPct <= 0 || sr.strategy.AlsStopReplacePct <= 0 {
			continue // stop-cond-replace not configured
		}
		condThreshold := matrixStopCondThreshold(sr.strategy.Direction, l.FilledPrice, sr.strategy.AlsStopCondPct)
		var condMet bool
		if sr.strategy.Direction == DirectionLong {
			condMet = currentPrice >= condThreshold
		} else {
			condMet = currentPrice <= condThreshold
		}
		if !condMet {
			continue
		}
		sr.alsPlaceLevelStop(ctx, l, l.FilledPrice, sr.strategy.AlsStopReplacePct)
	}
```

Note this reuses `matrixStopCondThreshold` (unchanged, matrix.go) for the condition check
and `alsPlaceLevelStop` (core engine plan, now also covered by `alsEnsureStopRoom` from
Task 3 above) for the actual placement — no new placement logic, just a new trigger
condition feeding the EXISTING placement function.

- [ ] **Step 6: Run to confirm it passes**

Run: `go test ./pkg/strategy/... -tags integration -run TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet -v`
Expected: `PASS`.

- [ ] **Step 7: Run the full regression suite**

Run: `go build ./... && go vet ./... && go test ./pkg/strategy/... -tags integration 2>&1 | grep -- "--- FAIL"`
Expected: clean build/vet, exactly the 2 known baseline failures.

Tell the user migration `099_als_stop_cond_fields.sql` needs to be applied (same
standing convention as `098_...` in the foundations plan) before this task's test will
pass against a real database.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/als.go pkg/strategy/types.go pkg/strategy/engine.go \
        pkg/strategy/als_stop_cond_test.go migrations/099_als_stop_cond_fields.sql
git commit -m "Implement ongoing ALS stop-cond/stop-replace re-evaluation on every price tick"
```

---

## What this plan does NOT do (see `2026-10-18-als-hedge-api-frontend.md`, next)

- No `services/api-gateway` HTTP handler changes — ALS strategies can still only be
  created/edited via direct DB access.
- No frontend changes — the "ALS скоро" button, the "2. Пороги" table mockup, and the
  "3. Параметры" fields are all still just the approved design, not real UI yet.
- The exact event hook that calls `alsRestoreFromMemory` when a stop fill actually
  happens on the exchange (vs. this plan's direct test calls) is explicitly flagged as
  unresolved in Task 3 — needs to be wired once the real SL-fill callback path is
  confirmed (likely alongside a `handleAlsLevelSLFill` function this plan never named,
  mirroring `handleMatrixSLFill`'s role for Matrix — add this as an explicit task in the
  API/frontend plan or a short follow-up plan once the callback's exact integration point
  is confirmed by reading `pkg/strategy/reconcile.go`'s fill-dispatch code, which was not
  re-read as part of this already-large plan).
