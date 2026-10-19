//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// newNonBybitReconcileFixture seeds a user/account/strategy/cycle/filled-level for the
// given exchange and wires up an in-memory AccountRunner + StrategyRunner matching
// production shape closely enough to exercise reconcile()'s new section 0c — including a
// real (buffered, not started) taskCh, since that section dispatches via sr.submit rather
// than calling the handler directly.
func newNonBybitReconcileFixture(t *testing.T, exchange string) (ar *AccountRunner, sr *StrategyRunner, cycleID string) {
	t.Helper()
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"nbrc-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,$2,'x','','') RETURNING id`,
		ownerID, exchange).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'TERUSDT','long','grid','active') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,7,NOW()-INTERVAL '1 hour') RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		VALUES ($1,$2,1,'Buy',0,10,'2.08','filled',320.58) RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_levels WHERE id=$1", levelID) })

	fake := &fakeExchange{}
	ar = &AccountRunner{
		accountID:           accID,
		exchangeName:        exchange,
		pool:                pool,
		exchange:            fake,
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posAvgEntrySize:     make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
	sr = &StrategyRunner{
		strategy: Strategy{ID: stratID, AccountID: accID, Symbol: "TERUSDT", Category: "linear", Direction: DirectionLong, StrategyType: "grid"},
		runner:   ar,
		cycle:    &Cycle{ID: cycleID, StrategyID: stratID, CycleNum: 7},
		levels:   []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Buy", Status: LevelFilled, FilledPrice: 320.58}},
		taskCh:   make(chan func(context.Context), 4),
	}
	ar.strategies[stratID] = sr
	return ar, sr, cycleID
}

// drainOneTask takes exactly one queued task off sr.taskCh and runs it synchronously,
// standing in for the real worker goroutine (which no test in this package starts —
// handler methods are tested directly elsewhere; here it's reconcile()'s decision to
// submit at all that's under test, so the task must actually be pulled off the channel
// reconcile() pushed it onto).
func drainOneTask(t *testing.T, ch chan func(context.Context), ctx context.Context) {
	t.Helper()
	select {
	case task := <-ch:
		task(ctx)
	default:
		t.Fatal("expected a task to be queued on taskCh, found none")
	}
}

// TestReconcile_NonBybitAccount_PositionClosedOnExchange_ClosesCycle is the regression test
// for the live bug (2026-10-19, account N2 Binance, TERUSDT/NEARUSDT/1000TURBOUSDT): closing
// a position on any non-Bybit exchange was never detected at all. handlePositionClose (the
// only code that ends a cycle on an external close) is driven exclusively by Bybit's private
// WS "position" topic, which AccountRunner.run never starts for non-Bybit accounts — so a
// closed Binance position left the cycle open, the filled level stuck 'filled' forever, and
// everything derived from that (the strategy card, the chart's stale SL line) never updated
// until the process was restarted.
func TestReconcile_NonBybitAccount_PositionClosedOnExchange_ClosesCycle(t *testing.T) {
	ctx := context.Background()
	ar, sr, cycleID := newNonBybitReconcileFixture(t, "binance")

	fake := ar.exchange.(*fakeExchange)
	// The position is genuinely gone on the exchange. (FetchOpenOrders, hit by reconcile()'s
	// later order-reconciliation blocks, defaults to an empty no-error result when nothing
	// is queued — same as TestReconcile_RevivesSplitBrainCycle relies on.)
	fake.fetchPositionsQ.push([]trader.Position{}, nil)

	ar.reconcile(ctx)
	drainOneTask(t, sr.taskCh, ctx)

	var endedAt *time.Time
	var result *string
	if err := ar.pool.QueryRow(ctx, `SELECT ended_at, result FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt, &result); err != nil {
		t.Fatalf("read back cycle: %v", err)
	}
	if endedAt == nil {
		t.Error("ended_at = NULL, want set — reconcile must detect the externally-closed position and end the cycle on a non-Bybit account")
	}
	if result == nil || *result != "manual_close" {
		t.Errorf("result = %v, want \"manual_close\"", result)
	}
}

// TestReconcile_NonBybitAccount_PositionStillOpenOnExchange_DoesNotCloseCycle is the
// negative control: when the exchange confirms the position is still open, the queued
// retry must leave the cycle untouched — reconcile() polling this every 20s must never
// force-close a strategy that's still genuinely trading.
func TestReconcile_NonBybitAccount_PositionStillOpenOnExchange_DoesNotCloseCycle(t *testing.T) {
	ctx := context.Background()
	ar, sr, cycleID := newNonBybitReconcileFixture(t, "binance")

	fake := ar.exchange.(*fakeExchange)
	// The position is still open on the exchange.
	fake.fetchPositionsQ.push([]trader.Position{
		{Symbol: "TERUSDT", Side: "Buy", Size: "2.08"},
	}, nil)

	ar.reconcile(ctx)
	drainOneTask(t, sr.taskCh, ctx)

	var endedAt *time.Time
	if err := ar.pool.QueryRow(ctx, `SELECT ended_at FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt); err != nil {
		t.Fatalf("read back cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("ended_at = %v, want NULL — the position is still open on the exchange, the cycle must not be closed", *endedAt)
	}
}

// TestReconcile_BybitAccount_DoesNotSubmitPositionCloseRetry is the negative control for
// the exchange gate itself: a Bybit account already gets this detection push-based
// (AccountRunner.OnPositionEvent) — section 0c must not also poll it, which would mean an
// extra FetchPositions call every 20s per Bybit strategy for no benefit. taskCh isn't a
// dedicated position-close-retry channel (reconcile's other, unrelated sections may queue
// their own tasks regardless of this fix), so the precise signal that section 0c's exchange
// gate actually fired is whether it ever called FetchPositions at all — not taskCh's
// contents.
func TestReconcile_BybitAccount_DoesNotSubmitPositionCloseRetry(t *testing.T) {
	ctx := context.Background()
	ar, _, _ := newNonBybitReconcileFixture(t, "bybit")
	fake := ar.exchange.(*fakeExchange)

	ar.reconcile(ctx)

	if fake.fetchPositionsCalls != 0 {
		t.Errorf("FetchPositions called %d times, want 0 — section 0c must be skipped entirely for a Bybit account", fake.fetchPositionsCalls)
	}
}
