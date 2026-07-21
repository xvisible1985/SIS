# Matrix Cycle Lifecycle Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Matrix strategies close their cycle when the position fully flattens (position → 0), routing through the same `closeCycle`/`handleTPFill` hedge/grid already use, instead of re-arming the same cycle in place forever. Adds a `hedge_sessions.accumulated_pnl` counter incremented at the source (not reconstructed from `trade_history`) that drives the "breakeven" paired-close trigger and a new UI progress indicator.

**Architecture:** `handleTPFill` (`pkg/strategy/cycle.go`) loses its `if StrategyType == "matrix"` delegation and closes matrix cycles the same way it closes hedge/grid cycles — `closeCycle`/`cancelPlacedLevels` already contain matrix-specific bookkeeping (per-level SL cancellation, waiting-slot reset), so removing the branch is safe. `handleMatrixSLFill` (per-level SL, a genuinely different event shape from hedge's single combined SL) gains a "did this bring the position to zero?" check and calls `closeCycle`+`maybeRestart` when it does. A new `AccumulateHedgeSessionPnl` helper, called from both paths, keeps `hedge_sessions.accumulated_pnl` current using an insert-vs-update guard so WS event replays can't double-count. `meetsPairedCloseCriteria`'s breakeven branch and `GetHedgeSession`'s API response both read this counter directly.

**Tech Stack:** Go (`pkg/strategy`, `services/api-gateway`), Postgres migration, React/TypeScript frontend (`HedgePairCard.tsx`).

---

## Spec coverage note

This plan implements every section of [2026-07-21-matrix-cycle-lifecycle-redesign-design.md](../specs/2026-07-21-matrix-cycle-lifecycle-redesign-design.md) except the two items the spec explicitly defers (zombie-detection removal, strategy-limit-overshoot re-investigation — Section 5, "not part of this change"). Task 3 was not named in the spec by number but is required by it: Section 1 says matrix routes through the shared `closeCycle` and Section 5 flags orderLinkId verification as an implementation-time task — investigation during planning found this is not optional (see Task 3's rationale).

---

### Task 1: `hedge_sessions.accumulated_pnl` migration

**Files:**
- Create: `migrations/083_hedge_sessions_accumulated_pnl.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/083_hedge_sessions_accumulated_pnl.sql
-- Bot-level accumulated realized PnL for a hedge/matrix pairing session, incremented
-- directly by the code that realizes each PnL event (closeCycle's async trade recorder,
-- handleMatrixSLFill's per-level SL close) rather than reconstructed later from
-- trade_history/strategy_levels — see docs/superpowers/specs/2026-07-21-matrix-cycle-
-- lifecycle-redesign-design.md Section 2 for why: trade_history attribution is not
-- reliable enough to drive a real-money paired-close trigger.
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS accumulated_pnl NUMERIC(18, 8) NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Apply the migration to the local dev DB**

Run: `docker exec sis-timescaledb-1 psql -U sis -d sis -f /dev/stdin < migrations/083_hedge_sessions_accumulated_pnl.sql`

(If the project uses a migration runner instead of applying `.sql` files directly, use that instead — check `services/api-gateway/main.go` or a `migrate` invocation in `deploy.sh` for the actual mechanism before running this manually.)

Expected: no error. Verify with:
```
docker exec sis-timescaledb-1 psql -U sis -d sis -c "\d hedge_sessions" | grep accumulated_pnl
```
Expected output includes: `accumulated_pnl | numeric(18,8) | not null default 0`

- [ ] **Step 3: Commit**

```bash
git add migrations/083_hedge_sessions_accumulated_pnl.sql
git commit -m "feat: add hedge_sessions.accumulated_pnl column"
```

---

### Task 2: `AccumulateHedgeSessionPnl` helper + wire into `RecordStrategyTrade`

**Files:**
- Modify: `pkg/strategy/trade_recorder.go`
- Test: `services/api-gateway/accumulated_pnl_test.go` (new — integration test, needs the full DB test harness that lives in `services/api-gateway`, not `pkg/strategy`)

- [ ] **Step 1: Write the failing test**

```go
// services/api-gateway/accumulated_pnl_test.go
//go:build integration

package main

import (
	"context"
	"testing"

	"sis/pkg/strategy"
)

// TestAccumulateHedgeSessionPnl_AddsToOpenSession: the helper adds netPnl to whichever
// currently-open (ended_at IS NULL) hedge_sessions row matches the strategy, whether it's
// the main leg or the hedge/matrix leg.
func TestAccumulateHedgeSessionPnl_AddsToOpenSession(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "accpnl1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "accpnl-bot")

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','long','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("insert main strategy: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("insert hedge_sessions: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, mainID, 12.5)
	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, hedgeID, -3.25)

	var got float64
	if err := s.pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&got); err != nil {
		t.Fatalf("read accumulated_pnl: %v", err)
	}
	if got != 9.25 {
		t.Errorf("accumulated_pnl = %v, want 9.25 (12.5 + -3.25, both legs' events land on the same session)", got)
	}
}

// TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp: a strategy with no open hedge_sessions
// row (a standalone strategy, or one whose session already ended) must not error or touch
// any other bot's session.
func TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "accpnl2")
	accID := createTestAccount(t, s, userID)

	var soloID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'SOLOUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&soloID); err != nil {
		t.Fatalf("insert solo strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", soloID) })

	// Must not panic or error — there is no hedge_sessions row for this strategy at all.
	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, soloID, 5.0)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./services/api-gateway/ -run TestAccumulateHedgeSessionPnl -v`
Expected: FAIL with `undefined: strategy.AccumulateHedgeSessionPnl`

- [ ] **Step 3: Write the helper**

Add to `pkg/strategy/trade_recorder.go`, after the `RecordMatrixTPProfit`/`InsertMatrixTPProfit` block (after line 132, before `RecordStrategyTrade`):

```go
// AccumulateHedgeSessionPnl adds netPnl to the accumulated_pnl of whichever currently-open
// (ended_at IS NULL) hedge_sessions row matches stratID — as either the main or the
// hedge/matrix leg. No-op if stratID isn't part of any open session (a standalone
// strategy, or one between sessions). Called directly from the code path that just
// realized this PnL (RecordStrategyTrade on cycle close, handleMatrixSLFill on a
// per-level SL fill) — never reconstructed later from trade_history, so it isn't exposed
// to that table's close-attribution fragility. See design doc Section 2.
func AccumulateHedgeSessionPnl(ctx context.Context, pool *pgxpool.Pool, stratID string, netPnl float64) {
	if _, err := pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl = accumulated_pnl + $1
		 WHERE (main_strategy_id = $2 OR hedge_strategy_id = $2) AND ended_at IS NULL`,
		netPnl, stratID,
	); err != nil {
		log.Printf("AccumulateHedgeSessionPnl: strategy %s: %v", stratID, err)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags=integration ./services/api-gateway/ -run TestAccumulateHedgeSessionPnl -v`
Expected: `--- PASS: TestAccumulateHedgeSessionPnl_AddsToOpenSession` and `--- PASS: TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp`

- [ ] **Step 5: Wire the helper into `RecordStrategyTrade`, guarded against WS-replay double-counting**

`RecordStrategyTrade`'s final write (`pkg/strategy/trade_recorder.go`, currently ends around line 374-377) uses `INSERT ... ON CONFLICT (strategy_id, cycle_num) DO UPDATE` — a WS replay of the same close re-runs this as an UPDATE of the same row, not a fresh row. Only a genuine first-time insert should accumulate; detect it with Postgres's `xmax = 0` trick (true only when the row was just inserted, not updated by the ON CONFLICT branch).

Change the final `_, err = pool.Exec(ctx, ...)` block (the one starting `// Normal path: INSERT, or re-upsert...`) to capture whether this was a fresh insert:

```go
	// Normal path: INSERT, or re-upsert if we already wrote this cycle earlier.
	var freshInsert bool
	err = pool.QueryRow(ctx, `
		INSERT INTO trade_history (
			strategy_id, bot_id, account_id, owner_id,
			symbol, category, direction, cycle_num, result, source,
			avg_entry, exit_price, qty, volume_usdt,
			pnl, pnl_pct, opened_at, closed_at,
			fees, funding, net_pnl, bybit_close_order_id
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, 'strategy',
			$10, $11, $12, $13,
			$14, $15, $16, NOW(),
			$17, $18, $19, $20
		)
		ON CONFLICT (strategy_id, cycle_num) WHERE strategy_id IS NOT NULL
		DO UPDATE SET
			result               = EXCLUDED.result,
			exit_price           = EXCLUDED.exit_price,
			qty                  = EXCLUDED.qty,
			pnl                  = EXCLUDED.pnl,
			pnl_pct              = EXCLUDED.pnl_pct,
			fees                 = EXCLUDED.fees,
			funding              = EXCLUDED.funding,
			net_pnl              = EXCLUDED.net_pnl,
			bybit_close_order_id = EXCLUDED.bybit_close_order_id,
			closed_at            = NOW()
		RETURNING (xmax = 0)`,
		in.Strategy.ID, in.Strategy.BotID, in.Strategy.AccountID, in.Strategy.OwnerID,
		in.Strategy.Symbol, in.Strategy.Category, string(in.Strategy.Direction),
		in.CycleNum, finalResult,
		avgEntry, exitPrice, closedQty, totalUSDT,
		grossPnl, pnlPct, in.StartedAt,
		fees, funding, netPnl, bybitCloseOrderID,
	).Scan(&freshInsert)
	if err != nil {
		log.Printf("trade recorder [%s cy%d]: upsert: %v", in.Strategy.Symbol, in.CycleNum, err)
		return
	}
	log.Printf("trade recorder [%s cy%d]: записано — result=%s gross=%.4f fees=%.4f funding=%.4f net=%.4f",
		in.Strategy.Symbol, in.CycleNum, finalResult, grossPnl, fees, funding, netPnl)
	if freshInsert {
		AccumulateHedgeSessionPnl(ctx, pool, in.Strategy.ID, netPnl)
	}
```

The other write path in this function (the "upgrade a manual row" `UPDATE ... WHERE strategy_id IS NULL` block, lines ~301-335) is already naturally idempotent — after it succeeds once, `strategy_id` is no longer NULL, so a replay can never match that `WHERE` clause again. Add the same accumulation call there too, inside the `if tag.RowsAffected() > 0 {` block, right before its `return`:

```go
		if tag.RowsAffected() > 0 {
			log.Printf("trade recorder [%s cy%d]: ручная → стратегия result=%s gross=%.4f net=%.4f",
				in.Strategy.Symbol, in.CycleNum, finalResult, grossPnl, netPnl)
			AccumulateHedgeSessionPnl(ctx, pool, in.Strategy.ID, netPnl)
			return
		}
```

- [ ] **Step 6: Run the full pkg/strategy and services/api-gateway test suites**

Run: `go build ./pkg/strategy/... ./services/api-gateway/... && go test ./pkg/strategy/... ./services/api-gateway/... -count=1 && go test -tags=integration ./services/api-gateway/ -run TestAccumulateHedgeSessionPnl -v`
Expected: all PASS, build clean.

- [ ] **Step 7: Commit**

```bash
git add pkg/strategy/trade_recorder.go services/api-gateway/accumulated_pnl_test.go
git commit -m "feat: accumulate hedge_sessions.accumulated_pnl on cycle close, replay-safe"
```

---

### Task 3: Stop tagging the matrix global TP order as "cycle doesn't end"

**Why this is required, not optional:** `pkg/strategy/linkid.go`'s `reMatrixTP` pattern (`^SIS_STR-([0-9a-f]{8})-tpl`) classifies the matrix global TP order's close as `LinkIDMatrixTP`, and `services/api-gateway/closed_pnl_syncer.go`'s handler for that kind explicitly does "Never touch ended_at: this cycle is healthy and still trading" (line ~380). After Task 4 makes matrix TP fills genuinely end the cycle, that classification becomes wrong — `ClosedPnlSyncer` would keep treating a correctly-closed cycle as one that must never be touched. The fix is at the source: stop generating the `-tpl` linkId tag for matrix TP orders, so they're classified as `LinkIDGridTP` instead (a genuine cycle-ending close) — the exact same classification hedge/grid's TP already gets, and `ClosedPnlSyncer`'s existing `LinkIDGridTP` handling (log-and-wait-for-recorder, no strategy-type-specific logic) is already correct for this case with zero changes needed there.

**Files:**
- Modify: `pkg/strategy/matrix.go:1557-1572` (inside `matrixUpdateTP`)
- Test: `pkg/strategy/linkid_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pkg/strategy/linkid_test.go`:

```go
// TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP: the matrix global TP order's
// linkId must classify as LinkIDGridTP (a genuine cycle-ending close), not LinkIDMatrixTP
// (ClosedPnlSyncer's "never touch ended_at" case) — matrix TP now ends the cycle the same
// way hedge/grid's does (see matrix-cycle-lifecycle-redesign design doc). Pins the format
// matrixUpdateTP must produce: SIS_STR-{id8}-tp-{cycleNum}-{seq}, with no slot suffix
// embedded between "tp" and the first "-", which is what previously triggered the
// (now-incorrect) LinkIDMatrixTP classification.
func TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP(t *testing.T) {
	parsed, ok := ParseStrategyLinkID("SIS_STR-abc12345-tp-3-2")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if parsed.Kind != LinkIDGridTP {
		t.Errorf("Kind = %v, want LinkIDGridTP", parsed.Kind)
	}
	if parsed.StrategyID8 != "abc12345" {
		t.Errorf("StrategyID8 = %q, want abc12345", parsed.StrategyID8)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/strategy/ -run TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP -v`
Expected: FAIL — `SIS_STR-abc12345-tp-3-2` already matches `reGridTP` today (this specific string was never actually produced by the current code, which always embeds a slot suffix when `governing.Slot != nil` and current matrix cycles always have a governing slot). Confirm the CURRENT `matrixUpdateTP` output format still hits `LinkIDMatrixTP` with a second assertion first — write and run this one before Step 3:

```go
func TestParseStrategyLinkID_MatrixGlobalTP_OldFormatStillMatrixTP(t *testing.T) {
	parsed, ok := ParseStrategyLinkID("SIS_STR-abc12345-tpl2-3-2")
	if !ok || parsed.Kind != LinkIDMatrixTP {
		t.Fatalf("expected old -tpl2- format to still classify as LinkIDMatrixTP (unchanged), got kind=%v ok=%v", parsed.Kind, ok)
	}
}
```
Run: `go test ./pkg/strategy/ -run TestParseStrategyLinkID_MatrixGlobalTP -v`
Expected: `TestParseStrategyLinkID_MatrixGlobalTP_OldFormatStillMatrixTP` PASSES (the parser itself is unchanged, still correctly classifies -tpl2- as before — old historical linkIds must keep parsing this way). `TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP` also already PASSES on its own (the parser already handles plain `-tp-`) — this task's actual RED/GREEN cycle is about the *generator* (`matrixUpdateTP`), covered by Step 6's test, not the parser. Keep both parser tests as permanent regression pins; proceed to Step 3.

- [ ] **Step 3: Change `matrixUpdateTP` to stop embedding the slot suffix in the linkId**

In `pkg/strategy/matrix.go`, find (around line 1557-1572):

```go
	sr.tpPlaceSeq++
	// Embed the initiating slot into the linkId so the frontend can display
	// "TP L(N)" in the execution marker. Uses same encoding as matrixSlotLinkStr:
	// positive slots → plain digits, negative slots → "n{abs}".
	slotSuffix := ""
	if governing.Slot != nil {
		s := *governing.Slot
		var enc string
		if s < 0 {
			enc = fmt.Sprintf("n%d", -s)
		} else {
			enc = fmt.Sprintf("%d", s)
		}
		slotSuffix = "l" + enc // e.g. "l2" or "ln1" → link looks like "…-tpl2-…"
	}
	linkID := fmt.Sprintf("SIS_STR-%s-tp%s-%d-%d", sr.strategy.ID[:8], slotSuffix, sr.cycle.CycleNum, sr.tpPlaceSeq)
```

Replace with:

```go
	sr.tpPlaceSeq++
	// Plain "-tp-" linkId, matching hedge/grid's format exactly — deliberately NOT
	// embedding the governing slot anymore (that used to produce "-tpl{N}-", which
	// ClosedPnlSyncer's linkid.go classifies as LinkIDMatrixTP: "cycle never ends".
	// Since the matrix-cycle-lifecycle redesign, a matrix TP fill DOES end the cycle
	// exactly like hedge/grid's does, so it must classify as LinkIDGridTP instead — the
	// frontend's "TP L(N)" execution-marker label is lost for matrix TP orders, a minor
	// display detail traded for correct close attribution.
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, sr.tpPlaceSeq)
```

- [ ] **Step 4: Check whether `governing.Slot` is used elsewhere in this function**

Run: `grep -n "governing" pkg/strategy/matrix.go | sed -n '1,20p'`

If `governing.Slot` (or `governing`) is only referenced in the block just replaced, no further change is needed — Go will not complain about an unused struct field access removed from one branch as long as `governing` itself is still used elsewhere in the function (it almost certainly is, for computing the TP price). If `governing` is now entirely unused, the compiler will flag it in Step 5 — fix by whatever the compiler reports.

- [ ] **Step 5: Build**

Run: `go build ./pkg/strategy/...`
Expected: no errors.

- [ ] **Step 6: Add a test pinning the new generator output shape**

Since `matrixUpdateTP` requires a live `sr.runner`/`tradeStream` to actually place an order (not reachable in a no-DB unit test without panicking, per this package's established test pattern — see `pkg/strategy/matrix_relative_test.go`'s stuck-slot tests), pin the linkId *format* directly instead of exercising the full function. Add to `pkg/strategy/linkid_test.go`:

```go
// TestMatrixTPLinkIDFormat_NoSlotSuffix documents and pins the exact linkId shape
// matrixUpdateTP must now produce (see matrix.go's linkID construction inside
// matrixUpdateTP) — plain "SIS_STR-{id8}-tp-{cycleNum}-{seq}", classified as
// LinkIDGridTP by ParseStrategyLinkID. If matrixUpdateTP's format ever drifts from this,
// this test's sibling (TestParseStrategyLinkID_MatrixGlobalTP_ClassifiedAsGridTP) is the
// one that will actually catch it end-to-end; this test exists to make the intended
// generated shape explicit in one place.
func TestMatrixTPLinkIDFormat_NoSlotSuffix(t *testing.T) {
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", "abc12345", 3, 2)
	if linkID != "SIS_STR-abc12345-tp-3-2" {
		t.Fatalf("linkID = %q, want SIS_STR-abc12345-tp-3-2", linkID)
	}
	parsed, ok := ParseStrategyLinkID(linkID)
	if !ok || parsed.Kind != LinkIDGridTP {
		t.Fatalf("expected LinkIDGridTP, got kind=%v ok=%v", parsed.Kind, ok)
	}
}
```

Add `"fmt"` to `pkg/strategy/linkid_test.go`'s imports if not already present.

- [ ] **Step 7: Run all linkid tests**

Run: `go test ./pkg/strategy/ -run TestParseStrategyLinkID -v && go test ./pkg/strategy/ -run TestMatrixTPLinkIDFormat -v`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/linkid_test.go
git commit -m "fix(matrix): stop tagging global TP linkId as never-ending-cycle"
```

---

### Task 4: Unify `handleTPFill` — matrix TP now closes the cycle

**Files:**
- Modify: `pkg/strategy/cycle.go:2750-2794` (`handleTPFill`)
- Modify: `pkg/strategy/matrix.go:1879-1985` (delete `handleMatrixTPFill`)
- Test: `pkg/strategy/matrix_relative_test.go` or a new `pkg/strategy/matrix_cycle_test.go`

- [ ] **Step 1: Write the failing test**

This package's established pattern for testing `StrategyRunner` methods that reach `sr.runner.tradeStream`/`sr.runner.pool` without a real DB/exchange is to assert the code *reaches* the expected call by triggering a nil-pointer panic at that exact point (see `TestMatrixRetryStuckRelativeSlot_RetriesPendingVirtualSlot` in `matrix_relative_test.go` for the precedent). Use the same technique to prove `handleTPFill` no longer special-cases matrix and falls through to the shared body (which calls `sr.closeCycle`, which — for `StrategyType == "matrix"` — calls `sr.runner.tradeStream.CancelOrder` if there's a per-level SL registered, or `sr.runner.pool.Exec` for the `ended_at` update; either panics on a nil runner).

Create `pkg/strategy/matrix_cycle_test.go`:

```go
package strategy

import (
	"context"
	"testing"
)

// TestHandleTPFill_MatrixFallsThroughToSharedClose: handleTPFill must no longer delegate
// matrix strategies to a separate handleMatrixTPFill that re-arms in place — it must reach
// the same closeCycle path hedge/grid uses. Proven by reaching a nil sr.runner panic inside
// closeCycle's DB write (`UPDATE strategy_cycles SET ended_at=NOW()...`), which only
// happens if handleTPFill fell through past any matrix-specific branch instead of
// returning early from a re-arm-in-place implementation that never calls closeCycle.
// Regression for the matrix-cycle-lifecycle redesign (2026-07-21): before this change,
// a matrix TP fill never set ended_at at all, so this panic would never be reached.
func TestHandleTPFill_MatrixFallsThroughToSharedClose(t *testing.T) {
	slot := 0
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			StrategyType:  "matrix",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot, Status: LevelFilled, FilledPrice: 100.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching closeCycle's nil-runner DB write — handleTPFill did not fall through to the shared close path for a matrix strategy")
		}
	}()
	sr.handleTPFill(context.Background(), "tp-order-1", 105.0, 1.0)
}
```

- [ ] **Step 2: Run test to verify it fails (wrong way)**

Run: `go test ./pkg/strategy/ -run TestHandleTPFill_MatrixFallsThroughToSharedClose -v`
Expected: FAIL — `handleMatrixTPFill` currently handles matrix and returns without ever reaching a nil-runner call in the exact same way (it also touches `sr.runner.pool.Exec` early, e.g. clearing `tp_order_id` at matrix.go's step 3) — check the actual failure message. If it panics for a *different* reason (also inside `handleMatrixTPFill`, since that function also touches `sr.runner`), this test doesn't distinguish the two paths yet. In that case, skip to Step 3 (implement first) and revisit this test's assertion — the important RED signal here is confirming the CURRENT code reaches `handleMatrixTPFill`, not `closeCycle`, which you can additionally confirm by temporarily adding `t.Log` or checking test output/stack trace for `handleMatrixTPFill` in the panic's call stack vs `closeCycle`.

Run with verbose stack trace: `go test ./pkg/strategy/ -run TestHandleTPFill_MatrixFallsThroughToSharedClose -v 2>&1 | grep -A5 "handleMatrixTPFill\|closeCycle"`
Expected before the fix: stack trace shows `handleMatrixTPFill`, not `closeCycle`.

- [ ] **Step 3: Remove the delegation branch in `handleTPFill`**

In `pkg/strategy/cycle.go`, find:

```go
// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context, orderID string, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.StrategyType == "matrix" {
		sr.handleMatrixTPFill(ctx, orderID, fillPrice, fillQty)
		return
	}
	// Guard against duplicate fill events (e.g. WS reconnect replay): if the
```

Replace with:

```go
// handleTPFill is called when the TP order is filled. Matrix strategies close through
// this same path as hedge/grid — closeCycle and cancelPlacedLevels already contain the
// matrix-specific bookkeeping (per-level SL cancellation, waiting-slot reset) needed
// before a matrix cycle actually ends. See matrix-cycle-lifecycle-redesign design doc.
func (sr *StrategyRunner) handleTPFill(ctx context.Context, orderID string, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	// Guard against duplicate fill events (e.g. WS reconnect replay): if the
```

(The rest of the function body is unchanged — `avgEntry()`, `cancelPlacedLevels`, `closeCycle`, `maybeRestart` are all already generic across strategy types, as verified during planning.)

- [ ] **Step 4: Delete `handleMatrixTPFill`**

In `pkg/strategy/matrix.go`, delete the entire function from its doc comment through its closing brace:

```go
// handleMatrixTPFill is called when the global matrix TP order fills.
// It cancels all per-level SL orders, resets filled/sl_closed levels back to
// pending, re-anchors the grid at the TP fill price, and re-places all non-virtual
// levels so the matrix can immediately start re-entering.
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixTPFill(ctx context.Context, orderID string, fillPrice, fillQty float64) {
```
... through its closing `}` and the blank line after it (this is `matrix.go:1879-1986` as read during planning — re-locate by searching `func (sr \*StrategyRunner) handleMatrixTPFill` since line numbers may have shifted from Task 3's edit).

- [ ] **Step 5: Build and check for now-unused imports/symbols**

Run: `go build ./pkg/strategy/...`

If `RecordMatrixTPProfit`, `MatrixTPRecordInput`, or the `"go RecordMatrixTPProfit(...)"` goroutine spawn (which lived inside the just-deleted function) leaves any now-unreachable-but-still-compiling dead code elsewhere, that's fine — `RecordMatrixTPProfit` itself is a still-valid, still-compiling function in `trade_recorder.go` (Task 6 addresses whether to keep it). This step should build clean with no changes needed beyond the deletion itself, since Go doesn't error on unused *package-level* functions (only unused imports/locals).

Expected: clean build.

- [ ] **Step 6: Update the test's assertion for the real failure point**

Re-run: `go test ./pkg/strategy/ -run TestHandleTPFill_MatrixFallsThroughToSharedClose -v`
Expected: PASS — the panic now originates inside `closeCycle` (nil `sr.runner.pool`), proving `handleTPFill` fell through to the shared path.

- [ ] **Step 7: Run the full pkg/strategy test suite**

Run: `go test ./pkg/strategy/... -count=1 -v 2>&1 | tail -80`
Expected: all PASS. Pay particular attention to any test with "matrix" in the name — this is the highest-risk change in the whole plan (removing the branch that isolated matrix's TP handling for the entire life of the feature).

- [ ] **Step 8: Commit**

```bash
git add pkg/strategy/cycle.go pkg/strategy/matrix.go pkg/strategy/matrix_cycle_test.go
git commit -m "fix(matrix): TP fill closes the cycle via shared closeCycle, not in-place re-arm"
```

---

### Task 5: `handleMatrixSLFill` closes the cycle when the position fully flattens

**Files:**
- Modify: `pkg/strategy/matrix.go:1707-1827` (`handleMatrixSLFill`)
- Test: `pkg/strategy/matrix_cycle_test.go` (from Task 4)

- [ ] **Step 1: Write the failing test**

Add to `pkg/strategy/matrix_cycle_test.go`:

```go
// TestHandleMatrixSLFill_LastLevelClosesCycle: when the level whose SL just fired was the
// ONLY filled level (matrixActiveQty() == 0 after marking it sl_closed), the position has
// fully flattened and the cycle must close via the shared closeCycle/maybeRestart path —
// proven by reaching a nil-runner panic inside closeCycle, same technique as Task 4's test.
func TestHandleMatrixSLFill_LastLevelClosesCycle(t *testing.T) {
	slot := 0
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			StrategyType: "matrix",
			Direction:    DirectionLong,
			Symbol:       "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot, Status: LevelFilled, FilledPrice: 100.0, Qty: "1.0", SizeUSDT: 100.0, SLPrice: 95.0},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching closeCycle — the only filled level's SL firing must flatten the position and close the cycle")
		}
	}()
	sr.handleMatrixSLFill(context.Background(), "level-0", 95.0)
}

// TestHandleMatrixSLFill_OtherLevelsStillFilled_CycleStaysOpen: when other levels remain
// filled after this one's SL fires, the position has NOT flattened — the cycle must stay
// open, and the code must reach matrixUpdateTP's TP recompute (proving the cycle-stays-
// open branch was taken) rather than closeCycle (which would mean it incorrectly treated
// a still-open position as flat). Verified during planning: matrixUpdateTP unconditionally
// calls sr.resolveExchangeAvgEntry (matrix.go, right after computing avgEntry() from
// remaining filled levels) whenever any level is still filled — which touches
// sr.runner.tradeStream before any strategy-type branch, so it panics on this test's nil
// runner. This is a DIFFERENT panic site than TestHandleMatrixSLFill_LastLevelClosesCycle
// (which panics inside closeCycle) — the two tests distinguish the two branches by WHICH
// function is on the panic stack, not by whether a panic happens at all.
//
// Fixture: level-0's SL fires; level-1 stays LevelFilled throughout, so
// matrixActiveQty() == 1.0 (not zero) after level-0 is marked sl_closed — the position has
// NOT flattened.
func TestHandleMatrixSLFill_OtherLevelsStillFilled_CycleStaysOpen(t *testing.T) {
	slot0, slot1 := 0, 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			StrategyType: "matrix",
			Direction:    DirectionLong,
			Symbol:       "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot0, Status: LevelFilled, FilledPrice: 100.0, Qty: "1.0", SizeUSDT: 100.0, SLPrice: 95.0},
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 98.0, Qty: "1.0", SizeUSDT: 98.0},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching matrixUpdateTP (via resolveExchangeAvgEntry) — cycle must stay open and recompute TP, not close, while level-1 is still filled")
		}
	}()
	sr.handleMatrixSLFill(context.Background(), "level-0", 95.0)
}
```

- [ ] **Step 2: Verify both tests fail correctly before implementing**

Run: `go test ./pkg/strategy/ -run TestHandleMatrixSLFill -v`
Expected: `TestHandleMatrixSLFill_LastLevelClosesCycle` FAILs (no panic — current code never closes the cycle from this path at all, matches the audit's finding). `TestHandleMatrixSLFill_OtherLevelsStillFilled_CycleStaysOpen` should already PASS today (current code never closes the cycle from any SL path) — confirm this with `go test -run TestHandleMatrixSLFill_OtherLevelsStillFilled -v`; if it panics for an unrelated reason (e.g. nil map access), fix the test fixture (not the production code) until it reaches the expected point cleanly on today's unmodified code.

- [ ] **Step 3: Implement the full-flatten check**

In `pkg/strategy/matrix.go`, find the end of `handleMatrixSLFill` (the final comment block):

```go
	// Recalculate global TP (will cancel it if no filled levels remain)
	sr.matrixUpdateTP(ctx)
	// Per-level SL closes only part of the position — the cycle continues.
	// When the full position goes to zero on the exchange, handlePositionClose
	// will detect it (closedBySelf=false, hasPosition=false) and return early,
	// leaving the cycle alive so matrixReplaceSlots can re-enter after SafeZone.
}
```

Replace with:

```go
	// If the position has fully flattened (no other level still filled), this was the
	// last leg standing — the cycle ends here via the same shared path hedge/grid TP/SL
	// fills use, instead of leaving it open for handlePositionClose to (previously) do
	// nothing useful with. See matrix-cycle-lifecycle-redesign design doc Section 1.
	if sr.matrixActiveQty() == 0 {
		AccumulateHedgeSessionPnl(ctx, sr.runner.pool, sr.strategy.ID, levelPnl)
		sr.closedBySelf = true
		sr.closedByReason = "SL"
		sr.cancelPlacedLevels(ctx)
		sr.closeCycle(ctx, "sl")
		sr.maybeRestart(ctx)
		return
	}
	// Recalculate global TP (will cancel it if no filled levels remain)
	sr.matrixUpdateTP(ctx)
	// Per-level SL closes only part of the position — the cycle continues, and this
	// level enters the waiting-reentry queue via the code above.
}
```

Note: `levelPnl` is the gross, not fee-adjusted, per-level PnL already computed earlier in this function (see the existing `levelPnl := ...` block above). This is a deliberate, smaller scope than `RecordStrategyTrade`'s fee-aware accumulation (Task 2) — fee-adjusting a single per-level SL close would require its own async Bybit-fee lookup (mirroring `InsertMatrixTPProfit`'s `trader_executions` query), which is real additional scope. Flag this explicitly to the user as a known simplification before merging this task: **накопление from per-level SL closes is NOT fee-adjusted, only cycle-ending closes routed through `RecordStrategyTrade` are.** If exact fee accounting on every partial SL matters before shipping, that's a follow-up task, not silently done here.

- [ ] **Step 4: Run both tests**

Run: `go test ./pkg/strategy/ -run TestHandleMatrixSLFill -v`
Expected: both PASS.

- [ ] **Step 5: Run the full pkg/strategy test suite**

Run: `go test ./pkg/strategy/... -count=1 -v 2>&1 | tail -80`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/strategy/matrix.go pkg/strategy/matrix_cycle_test.go
git commit -m "fix(matrix): per-level SL closes the cycle when it flattens the position"
```

**Flag to user before proceeding:** this task's fee-adjustment simplification (Step 4's note) should be surfaced explicitly in the task-review report, not buried — the user cares specifically about fee-accurate накопление for breakeven mode (their own words: "с учетом комиссий биржи").

---

### Task 6: `meetsPairedCloseCriteria` breakeven mode uses `accumulated_pnl`

**Files:**
- Modify: `services/api-gateway/hedge_engine.go:328-345` (`meetsPairedCloseCriteria`), `:1435` (call site)
- Modify: `services/api-gateway/matrix_engine.go:251` (call site)
- Test: `services/api-gateway/hedge_engine_test.go` (new, or add to an existing hedge test file if one covers pure functions — check for `hedge_engine_test.go` first with `ls services/api-gateway/hedge_engine_test.go`; if absent, create it)

- [ ] **Step 1: Write the failing test**

```go
// services/api-gateway/hedge_engine_test.go
package main

import "testing"

// TestMeetsPairedCloseCriteria_Breakeven_UsesAccumulatedPlusLive: mode 2 (breakeven) must
// compare accumulated_pnl (this session's realized history) PLUS the live combined
// unrealized PnL against the threshold — not live-only, which ignored all historical
// realized PnL and fees. Modes 0/1 are unaffected (live-only, unchanged).
func TestMeetsPairedCloseCriteria_Breakeven_UsesAccumulatedPlusLive(t *testing.T) {
	cfg := botCfgJSON{HedgeDeactCloseType: 2, HedgeBreakevenProfit: 10.0}
	main := hedgePosInfo{UnrealisedPnl: 3.0}
	hedge := hedgePosInfo{UnrealisedPnl: 2.0}
	// live combined = 5.0; accumulated = 4.0 → total = 9.0, below threshold 10.0
	if meetsPairedCloseCriteria(main, hedge, cfg, 4.0) {
		t.Error("9.0 total < 10.0 threshold: expected false")
	}
	// accumulated = 5.5 → total = 10.5, above threshold
	if !meetsPairedCloseCriteria(main, hedge, cfg, 5.5) {
		t.Error("10.5 total >= 10.0 threshold: expected true")
	}
}

// TestMeetsPairedCloseCriteria_PnlDollar_IgnoresAccumulated: mode 0 stays live-only —
// accumulated_pnl must not affect it even when large.
func TestMeetsPairedCloseCriteria_PnlDollar_IgnoresAccumulated(t *testing.T) {
	cfg := botCfgJSON{HedgeDeactCloseType: 0, HedgeDeactCloseValue: 10.0}
	main := hedgePosInfo{UnrealisedPnl: 3.0}
	hedge := hedgePosInfo{UnrealisedPnl: 2.0}
	// live combined = 5.0, below threshold — a huge accumulated value must not flip this.
	if meetsPairedCloseCriteria(main, hedge, cfg, 1000.0) {
		t.Error("mode 0 must ignore accumulated_pnl entirely")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./services/api-gateway/ -run TestMeetsPairedCloseCriteria -v`
Expected: FAIL — `too many arguments in call to meetsPairedCloseCriteria`

- [ ] **Step 3: Add the `accumulatedPnl` parameter**

In `services/api-gateway/hedge_engine.go`, find:

```go
func meetsPairedCloseCriteria(mainPos, hPos hedgePosInfo, cfg botCfgJSON) bool {
	combined := mainPos.UnrealisedPnl + hPos.UnrealisedPnl
	switch cfg.HedgeDeactCloseType {
	case 0: // combined pnl$ ≥ threshold
		return combined >= cfg.HedgeDeactCloseValue
	case 1: // combined roi%
		mainMargin := mainPos.EntryPrice * mainPos.Size / mainPos.Leverage
		hMargin := hPos.EntryPrice * hPos.Size / hPos.Leverage
		totalMargin := mainMargin + hMargin
		if totalMargin == 0 {
			return false
		}
		return combined/totalMargin*100 >= cfg.HedgeDeactCloseValue
	case 2: // breakeven + optional profit target
		return combined >= cfg.HedgeBreakevenProfit
	}
	return false
}
```

Replace with:

```go
// meetsPairedCloseCriteria reports whether the pair should be closed now. accumulatedPnl
// is this session's hedge_sessions.accumulated_pnl (both legs' realized PnL since the last
// paired_close, fee-adjusted where the source event was fee-aware — see
// AccumulateHedgeSessionPnl) — used only by mode 2 (breakeven), which must account for
// realized history, not just the live open position. Modes 0/1 stay live-only.
func meetsPairedCloseCriteria(mainPos, hPos hedgePosInfo, cfg botCfgJSON, accumulatedPnl float64) bool {
	combined := mainPos.UnrealisedPnl + hPos.UnrealisedPnl
	switch cfg.HedgeDeactCloseType {
	case 0: // combined pnl$ ≥ threshold
		return combined >= cfg.HedgeDeactCloseValue
	case 1: // combined roi%
		mainMargin := mainPos.EntryPrice * mainPos.Size / mainPos.Leverage
		hMargin := hPos.EntryPrice * hPos.Size / hPos.Leverage
		totalMargin := mainMargin + hMargin
		if totalMargin == 0 {
			return false
		}
		return combined/totalMargin*100 >= cfg.HedgeDeactCloseValue
	case 2: // breakeven: accumulated realized history + current live position vs threshold
		return accumulatedPnl+combined >= cfg.HedgeBreakevenProfit
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./services/api-gateway/ -run TestMeetsPairedCloseCriteria -v`
Expected: both PASS.

- [ ] **Step 5: Update the hedge bot call site**

In `services/api-gateway/hedge_engine.go`, find (around line 1432-1435):

```go
		// Paired close: combined P&L condition (requires both positions).
		// This is the ONLY genuine paired-close trigger — the cumulative PnL
		// counter (GetHedgeSession) resets here and nowhere else.
		if hasMain && hasHedge && meetsPairedCloseCriteria(mainPos, hedgePos, cfg) {
```

Replace with:

```go
		// Paired close: combined P&L condition (requires both positions).
		// This is the ONLY genuine paired-close trigger — the cumulative PnL
		// counter (GetHedgeSession) resets here and nowhere else.
		var accumulatedPnl float64
		if hasMain && hasHedge {
			s.pool.QueryRow(ctx, //nolint:errcheck
				`SELECT accumulated_pnl FROM hedge_sessions
				 WHERE hedge_strategy_id=$1 AND ended_at IS NULL`, h.id).Scan(&accumulatedPnl)
		}
		if hasMain && hasHedge && meetsPairedCloseCriteria(mainPos, hedgePos, cfg, accumulatedPnl) {
```

- [ ] **Step 6: Update the matrix bot call site**

In `services/api-gateway/matrix_engine.go`, find (around line 251, inside `checkMatrixPairedClose`, after the `hedge_sessions` bootstrap insert and before the criteria check):

```go
		if meetsPairedCloseCriteria(longPos, shortPos, cfg) {
```

Replace with (add the fetch right after the existing bootstrap `INSERT INTO hedge_sessions ... DO NOTHING` block, before this line):

```go
		var accumulatedPnl float64
		s.pool.QueryRow(ctx, //nolint:errcheck
			`SELECT accumulated_pnl FROM hedge_sessions
			 WHERE hedge_strategy_id=$1 AND ended_at IS NULL`, p.shortID).Scan(&accumulatedPnl)
		if meetsPairedCloseCriteria(longPos, shortPos, cfg, accumulatedPnl) {
```

(`p.shortID` matches this function's existing convention — re-read the surrounding `checkMatrixPairedClose` body during implementation to confirm the exact loop variable name in the current source, since Task 4/5's edits don't touch this file and its line numbers are unaffected, but double-check before pasting.)

- [ ] **Step 7: Build and run full test suite**

Run: `go build ./services/api-gateway/... && go test ./services/api-gateway/... -count=1 && go test -tags=integration ./services/api-gateway/ -run 'TestMatrixPairedClose|TestGetHedgeSession|TestMeetsPairedCloseCriteria' -v`
Expected: all PASS. `matrix_paired_close_test.go`'s existing `TestMatrixLegCloseRequest` is unaffected (different function) but run it anyway as a regression check.

- [ ] **Step 8: Commit**

```bash
git add services/api-gateway/hedge_engine.go services/api-gateway/matrix_engine.go services/api-gateway/hedge_engine_test.go
git commit -m "feat: breakeven paired-close mode accounts for accumulated realized PnL"
```

---

### Task 7: `GetHedgeSession` reads `accumulated_pnl` directly

**Files:**
- Modify: `services/api-gateway/strategy_handler.go:1509-1596` (`GetHedgeSession`)
- Test: `services/api-gateway/hedge_session_test.go` (existing — update, don't duplicate)

- [ ] **Step 1: Read the existing tests to understand what must keep passing**

Run: `go test -tags=integration ./services/api-gateway/ -run TestGetHedgeSession -v` (before any change) to confirm current green baseline. All three (`TestGetHedgeSession_IncludesRealizedPnl`, `TestGetHedgeSession_IncludesMatrixTPProfits`, `TestGetHedgeSession_ResetsOnlyOnPairedClose`) must still pass after this task — but `TestGetHedgeSession_IncludesMatrixTPProfits` specifically tests the OLD 3-way-UNION behavior (summing `matrix_tp_profits` rows into the response) via `matrix_tp_profits` INSERTs directly, not via `accumulated_pnl`. Since Task 5/6 no longer write new `matrix_tp_profits` rows going forward, and this task changes `GetHedgeSession` to stop reading that table, this specific test's *assertions* are now testing retired behavior — it needs to be rewritten to insert/assert against `accumulated_pnl` instead of `matrix_tp_profits`, not left as-is expecting `matrix_tp_profits` rows to still surface in the response.

- [ ] **Step 2: Read the current test file and rewrite the matrix-tp-profits test**

Run: `grep -n "func TestGetHedgeSession_IncludesMatrixTPProfits" -A 55 services/api-gateway/hedge_session_test.go`

Rewrite that test to seed `hedge_sessions.accumulated_pnl` directly via `UPDATE hedge_sessions SET accumulated_pnl=$1 WHERE id=$2` instead of inserting `matrix_tp_profits` rows, and assert `resp.CumulativeHedgePnl` (or whatever the response field is renamed to in Step 4) equals that seeded value. Keep the test name (`TestGetHedgeSession_IncludesMatrixTPProfits` → rename to `TestGetHedgeSession_ReadsAccumulatedPnl` since it no longer tests `matrix_tp_profits` specifically) and its position reset assertions (`TestGetHedgeSession_ResetsOnlyOnPairedClose` already tests reset semantics against whatever mechanism is live — since `accumulated_pnl` defaults to 0 on a new session row per Task 1's migration, this test should continue passing once the query switches to reading the column, but re-verify its exact assertions against the new query in Step 5 before assuming it needs no changes).

- [ ] **Step 3: Run the rewritten test to verify it fails against the OLD query**

Run: `go test -tags=integration ./services/api-gateway/ -run TestGetHedgeSession -v`
Expected: the renamed test FAILs (old query doesn't read `accumulated_pnl` at all, so seeding it directly has no effect on the response yet).

- [ ] **Step 4: Simplify the `GetHedgeSession` query**

In `services/api-gateway/strategy_handler.go`, find the `GetHedgeSession` function's query (the big `SELECT` starting `hs.id::text, ...`). Replace the `CumulativeHedgePnl` computation subquery:

```go
			(
				WITH floor_time AS (
					SELECT COALESCE(MAX(ended_at), '-infinity'::timestamptz) AS t
					FROM hedge_sessions
					WHERE hedge_strategy_id = hs.hedge_strategy_id
					  AND end_reason = 'paired_close'
				),
				leg AS (
					SELECT CASE WHEN hs.main_strategy_id = $1 THEN hs.main_strategy_id ELSE hs.hedge_strategy_id END AS id
				)
				SELECT
					COALESCE((
						SELECT SUM(th.net_pnl)
						FROM trade_history th, floor_time, leg
						WHERE th.strategy_id = leg.id
						  AND th.closed_at >= floor_time.t
					), 0)
					+
					COALESCE((
						SELECT SUM(sl.realized_pnl)
						FROM strategy_levels sl, floor_time, leg
						WHERE sl.strategy_id = leg.id
						  AND sl.realized_pnl IS NOT NULL
						  AND sl.sl_closed_at >= floor_time.t
					), 0)
					+
					COALESCE((
						SELECT SUM(mtp.net_pnl)
						FROM matrix_tp_profits mtp, floor_time, leg
						WHERE mtp.strategy_id = leg.id
						  AND mtp.closed_at >= floor_time.t
					), 0)
			)::float8
```

with:

```go
			hs.accumulated_pnl::float8
```

Update the surrounding comment (currently starts `// The cumulative counter must include every closed trade...`) to:

```go
		// accumulated_pnl is incremented directly by the code that realizes each PnL
		// event (RecordStrategyTrade on cycle close, handleMatrixSLFill on a per-level
		// SL) — see AccumulateHedgeSessionPnl. It already covers both legs together and
		// already resets correctly on a new session (each row starts at 0), so no
		// separate floor_time/reset computation is needed here anymore.
```

- [ ] **Step 5: Build and run tests**

Run: `go build ./services/api-gateway/... && go test -tags=integration ./services/api-gateway/ -run TestGetHedgeSession -v`
Expected: all PASS, including the two pre-existing tests (`TestGetHedgeSession_IncludesRealizedPnl`, `TestGetHedgeSession_ResetsOnlyOnPairedClose`) — if either fails, read its assertions carefully: `TestGetHedgeSession_IncludesRealizedPnl` likely seeds `trade_history` directly and expects the OLD query's `trade_history`-summing behavior; since `GetHedgeSession` no longer reads `trade_history` at all, this test's premise needs the same treatment as Step 2 — rewrite it to seed `accumulated_pnl` instead, or delete it if `TestGetHedgeSession_ReadsAccumulatedPnl` (Step 2's rename) already covers the same ground redundantly. Do not leave a test asserting behavior the code no longer has.

- [ ] **Step 6: Add mode/threshold fields for the frontend progress bar**

Still in `GetHedgeSession`, add three fields to `sessionResp` and populate them from the bot's config (already loaded via the `hs.bot_id` join — need the bot's `strategy_config` too). Update the struct:

```go
	type sessionResp struct {
		ID                 string     `json:"id"`
		BotID              string     `json:"bot_id"`
		MainStrategyID     *string    `json:"main_strategy_id"`
		HedgeStrategyID    string     `json:"hedge_strategy_id"`
		MainEntryAtStart   *float64   `json:"main_entry_at_start"`
		HedgeEntryAtStart  *float64   `json:"hedge_entry_at_start"`
		GapAtStart         *float64   `json:"gap_at_start"`
		StartedAt          time.Time  `json:"started_at"`
		EndedAt            *time.Time `json:"ended_at"`
		CumulativeHedgePnl float64    `json:"cumulative_hedge_pnl"`
		CloseType          int        `json:"close_type"`           // 0=pnl$, 1=roi%, 2=breakeven
		CloseThreshold     float64    `json:"close_threshold"`      // cfg value for the active close_type
	}
```

After the existing `QueryRow(...).Scan(...)` call succeeds, fetch the bot's config and populate the two new fields:

```go
	var stratCfg []byte
	if err := s.pool.QueryRow(r.Context(),
		`SELECT strategy_config FROM bots WHERE id=$1`, resp.BotID,
	).Scan(&stratCfg); err == nil {
		var cfg botCfgJSON
		if json.Unmarshal(stratCfg, &cfg) == nil {
			resp.CloseType = cfg.HedgeDeactCloseType
			switch cfg.HedgeDeactCloseType {
			case 1:
				resp.CloseThreshold = cfg.HedgeDeactCloseValue
			case 2:
				resp.CloseThreshold = cfg.HedgeBreakevenProfit
			default:
				resp.CloseThreshold = cfg.HedgeDeactCloseValue
			}
		}
	}
```

Add `"encoding/json"` to `strategy_handler.go`'s imports if not already present (check first — this file almost certainly already imports it given its size; verify with `grep -n '"encoding/json"' services/api-gateway/strategy_handler.go` before adding a duplicate).

- [ ] **Step 7: Write a test for the new fields**

Add to `services/api-gateway/hedge_session_test.go`:

```go
// TestGetHedgeSession_IncludesCloseTypeAndThreshold: the response must surface the bot's
// active paired-close mode and threshold so the frontend can render a progress indicator
// without a second API call.
func TestGetHedgeSession_IncludesCloseTypeAndThreshold(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "sessct1")
	accID := createTestAccount(t, s, userID)

	cfgJSON := `{"bot_kind":"matrix","hedge_deact_close_type":2,"hedge_breakeven_profit":10.5}`
	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, account_id, name, status, strategy_config)
		 VALUES ($1,$2,'ct-bot','active',$3::jsonb) RETURNING id`,
		userID, accID, cfgJSON).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}

	var mainID, hedgeID string
	s.pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		VALUES ($1,$2,'CTUSDT','long','matrix','active',$3) RETURNING id`, userID, accID, botID).Scan(&mainID)
	s.pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		VALUES ($1,$2,'CTUSDT','short','matrix','active',$3) RETURNING id`, userID, accID, botID).Scan(&hedgeID)
	s.pool.Exec(ctx, `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3)`,
		botID, mainID, hedgeID)

	req := httptest.NewRequest("GET", "/strategies/"+hedgeID+"/hedge-session", nil)
	req = req.WithContext(context.WithValue(req.Context(), userIDCtxKey, userID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", hedgeID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	s.GetHedgeSession(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		CloseType      int     `json:"close_type"`
		CloseThreshold float64 `json:"close_threshold"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CloseType != 2 {
		t.Errorf("CloseType = %d, want 2", resp.CloseType)
	}
	if resp.CloseThreshold != 10.5 {
		t.Errorf("CloseThreshold = %v, want 10.5", resp.CloseThreshold)
	}
}
```

Check `userIDCtxKey`'s actual name by running `grep -n "UserIDFromCtx\|userIDCtxKey" services/api-gateway/strategy_handler.go | head -3` before pasting — other test files in this package (e.g. `bind_strategies_test.go`) already exercise handler functions directly with a constructed `httptest.NewRequest`/`chi.RouteContext`; copy the exact context-key pattern from one of those rather than guessing.

- [ ] **Step 8: Run tests**

Run: `go build ./services/api-gateway/... && go test -tags=integration ./services/api-gateway/ -run TestGetHedgeSession -v`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add services/api-gateway/strategy_handler.go services/api-gateway/hedge_session_test.go
git commit -m "feat: GetHedgeSession reads accumulated_pnl directly, exposes close mode/threshold"
```

---

### Task 8: Frontend paired-close progress bar

**Files:**
- Modify: `frontend/src/types.ts:362-373` (`HedgeSession` interface)
- Modify: `frontend/src/components/strategies/HedgePairCard.tsx:805-808` (empty "Правая колонка")
- Test: manual verification (frontend changes in this codebase are verified via `tsc --noEmit` + visual check per project convention, not unit tests — see CLAUDE.md)

- [ ] **Step 1: Update the `HedgeSession` type**

In `frontend/src/types.ts`, find:

```typescript
export interface HedgeSession {
  id:                   string
  bot_id:               string
  main_strategy_id:     string | null
  hedge_strategy_id:    string
  main_entry_at_start:  number | null
  hedge_entry_at_start: number | null
  gap_at_start:         number | null
  started_at:           string
  ended_at:             string | null
  cumulative_hedge_pnl: number
}
```

Replace with:

```typescript
export interface HedgeSession {
  id:                   string
  bot_id:               string
  main_strategy_id:     string | null
  hedge_strategy_id:    string
  main_entry_at_start:  number | null
  hedge_entry_at_start: number | null
  gap_at_start:         number | null
  started_at:           string
  ended_at:             string | null
  cumulative_hedge_pnl: number
  close_type:           number  // 0=pnl$, 1=roi%, 2=breakeven
  close_threshold:      number  // cfg value for the active close_type
}
```

- [ ] **Step 2: Add a `PairedCloseProgress` component**

In `frontend/src/components/strategies/HedgePairCard.tsx`, add this component near `StatRow` (after its definition, around line 148):

```tsx
// ── PairedCloseProgress ────────────────────────────────────────────────────

const CLOSE_MODE_LABEL: Record<number, string> = { 0: 'PnL$', 1: 'ROI%', 2: 'Безубыток' }

function fmtCloseValue(v: number, closeType: number): string {
  if (closeType === 1) return `${v >= 0 ? '+' : ''}${v.toFixed(2)}%`
  return `${v >= 0 ? '+' : ''}${v.toFixed(2)}$`
}

function PairedCloseProgress({
  closeType, current, threshold,
}: { closeType: number; current: number; threshold: number }) {
  const pct = threshold !== 0 ? Math.max(0, Math.min(100, (current / threshold) * 100)) : 0
  const label = CLOSE_MODE_LABEL[closeType] ?? 'PnL$'
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between">
        <span className="text-[11px] text-slate-500">{label}</span>
        <span className="text-[11px] text-slate-500 tabular-nums">{pct.toFixed(0)}% до порога</span>
      </div>
      <div className="h-1.5 rounded-full overflow-hidden bg-white/[.06]">
        <div
          className="h-full rounded-full transition-all"
          style={{ width: `${pct}%`, background: pct >= 100 ? '#6ee7b7' : '#a78bfa' }}
        />
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-[12px] font-semibold tabular-nums" style={{ color: current >= threshold ? '#6ee7b7' : '#94a3b8' }}>
          {fmtCloseValue(current, closeType)}
        </span>
        <span className="text-[12px] text-slate-600 tabular-nums">{fmtCloseValue(threshold, closeType)}</span>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Compute the "current" value per mode and render the component**

The card already computes live position data (`mainPos`, `hedgePos` — read their exact variable names from the component's existing scope, visible earlier in the file around the `pairedCloseTarget` `useMemo`) and already fetches `hedgeSession`/`matrixPnl`. Add a `pairedCloseCurrent` computation near `pairedCloseTarget` (after its `useMemo` block, around line 512):

```tsx
  const pairedCloseCurrent = useMemo(() => {
    if (!mainPos || !hedgePos) return null
    const liveCombined = parseFloat(mainPos.unrealisedPnl) + parseFloat(hedgePos.unrealisedPnl)
    const closeType = hedgeBot?.strategyConfig?.hedge_deact_close_type ?? 0
    if (closeType === 2) {
      const accumulated = isMatrixPair ? (matrixPnl ?? 0) : (hedgeSession?.cumulative_hedge_pnl ?? 0)
      return accumulated + liveCombined
    }
    if (closeType === 1) {
      const Em = parseFloat(mainPos.entryPrice), Eh = parseFloat(hedgePos.entryPrice)
      const Sm = parseFloat(mainPos.size), Sh = parseFloat(hedgePos.size)
      const totalMargin = (Em * Sm) + (Eh * Sh)
      return totalMargin !== 0 ? (liveCombined / totalMargin) * 100 : null
    }
    return liveCombined
  }, [mainPos, hedgePos, hedgeBot, isMatrixPair, matrixPnl, hedgeSession])
```

Check the exact field names on `mainPos`/`hedgePos` (`unrealisedPnl` vs `unrealised_pnl` etc.) by reading their type definition — grep `interface.*Position` in `frontend/src/types.ts` — before pasting; the snippet above assumes camelCase parsed-string fields matching the pattern already used in the existing `pairedCloseTarget` block (`parseFloat(mainPos.entryPrice)`), so mirror that exactly.

Then replace the empty right column (around line 805-808):

```tsx
              {/* ── Правая колонка ── */}
              <div className="space-y-1.5">
                {/* будет заполнена позже */}
              </div>
```

with:

```tsx
              {/* ── Правая колонка ── */}
              <div className="space-y-1.5">
                {pairedCloseCurrent !== null && hedgeBot?.strategyConfig && (
                  <PairedCloseProgress
                    closeType={hedgeBot.strategyConfig.hedge_deact_close_type ?? 0}
                    current={pairedCloseCurrent}
                    threshold={
                      (hedgeBot.strategyConfig.hedge_deact_close_type ?? 0) === 2
                        ? (hedgeBot.strategyConfig.hedge_breakeven_profit ?? 0)
                        : (hedgeBot.strategyConfig.hedge_deact_close_value ?? 0)
                    }
                  />
                )}
              </div>
```

- [ ] **Step 4: Type-check**

Run: `cd frontend && npx tsc --noEmit`
Expected: no errors. Fix any field-name mismatches found (e.g. `unrealisedPnl` vs actual position type field) using the real type definitions, not guesses.

- [ ] **Step 5: Visual check**

Start the frontend dev server if not already running, open a matrix or hedge pair card in the UI, expand it, and confirm the progress bar renders in the previously-empty right column with plausible values (compare against the "Накоплено" stat row already shown in the left column — for breakeven mode they should be closely related). Test at least one matrix pair and one hedge pair if both exist in the current dev data.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/types.ts frontend/src/components/strategies/HedgePairCard.tsx
git commit -m "feat: paired-close progress bar (mode/current/threshold) in pair card"
```

---

## Final full regression pass

- [ ] Run: `go build ./pkg/strategy/... ./services/api-gateway/...`
- [ ] Run: `go test ./pkg/strategy/... ./services/api-gateway/... -count=1`
- [ ] Run: `go test -tags=integration ./services/api-gateway/ -count=1 -v 2>&1 | tail -150` — full integration suite, not just the tests touched by this plan; matrix/hedge changes are exactly the kind of change likely to have non-obvious ripple effects on zombie-detection, strategy limits, and cross-bot conflict tests written earlier this session.
- [ ] Run: `cd frontend && npx tsc --noEmit`
- [ ] Report to the user, per CLAUDE.md: which existing mechanics were checked (list every test suite run), what passed, what (if anything) changed behavior and why that's expected — explicitly call out Task 5's fee-adjustment simplification and confirm whether the user wants it addressed now or tracked as follow-up debt.
- [ ] Remind the user: this needs a full rebuild + api-gateway restart before it's live, same as every backend change this session.
