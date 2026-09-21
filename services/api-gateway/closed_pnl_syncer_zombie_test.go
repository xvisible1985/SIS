//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestClosedPnlSyncer_ZombieMatch_NeverMatchesCycleThatStartedAfterClose is the regression
// for the 2026-09-16 incident: fullReconcile's wider lookback let a stale, unrelated
// 2026-09-11 closed-pnl entry (an order this syncer had never managed to attribute) match
// against an unrelated OPEN cycle that started five days later, on 2026-09-16 — the old
// "zombie cycle" heuristic matched purely on symbol+direction+"still open", with no check
// that the close actually happened after the cycle began. It ghost-closed a live,
// currently-trading matrix position (ended_at set BEFORE started_at), silently halting all
// further order placement on a real open exchange position.
func TestClosedPnlSyncer_ZombieMatch_NeverMatchesCycleThatStartedAfterClose(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "zombietimeorder")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'ZTOUSDT','short','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// Cycle starts NOW — well after the stale order's close time below.
	cycleStartedAt := time.Now()
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,$2) RETURNING id`,
		stratID, cycleStartedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })
	// The manual-fallback row this test expects to land carries no strategy_id (it matched
	// no strategy), so the cleanup above alone wouldn't remove it — clean it explicitly too.
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE bybit_close_order_id=$1", "stale-order-predates-cycle") })

	// A stale order that closed 5 days BEFORE the cycle even started — e.g. an old,
	// unrelated position this syncer is only now reaching via a wide reconcile sweep.
	staleCloseTime := cycleStartedAt.Add(-5 * 24 * time.Hour)
	p := trader.ClosedPnl{
		Symbol: "ZTOUSDT", OrderId: "stale-order-predates-cycle", OrderLinkId: "",
		Side: "Buy", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "0.9",
		ClosedPnl: "10.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, staleCloseTime)

	// 1. The cycle must remain OPEN — this is the actual bug: it must never be
	// ghost-closed by an order that predates its own start.
	var endedAt *time.Time
	var result *string
	if err := s.pool.QueryRow(ctx, `SELECT ended_at, result FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt, &result); err != nil {
		t.Fatalf("query cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("cycle ended_at = %v, want NULL — a cycle must never be closed by an order that predates its own start_at (%v)", *endedAt, cycleStartedAt)
	}
	if result != nil {
		t.Errorf("cycle result = %q, want NULL", *result)
	}

	// 2. No ghost_close trade_history row must have been fabricated for this cycle.
	var ghostCount int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE strategy_id=$1 AND result='ghost_close'`, stratID).Scan(&ghostCount)
	if ghostCount != 0 {
		t.Errorf("ghost_close trade_history rows = %d, want 0", ghostCount)
	}

	// 3. The stale order, having matched no strategy, must fall through to the legacy
	// manual-attribution path instead — same as any other genuinely unattributed order.
	var manualCount int
	var source string
	s.pool.QueryRow(ctx, `SELECT count(*), COALESCE(MAX(source),'') FROM trade_history WHERE account_id=$1 AND bybit_close_order_id=$2`, accID, p.OrderId).Scan(&manualCount, &source)
	if manualCount != 1 || source != "manual" {
		t.Errorf("trade_history for stale order: count=%d source=%q, want count=1 source=manual", manualCount, source)
	}
}

// TestClosedPnlSyncer_ZombieMatch_StillClosesGenuineZombie: unchanged existing behavior —
// a cycle that genuinely started BEFORE the close (the real "gateway was down while this
// cycle finished" case) must still be ghost-closed and recorded, same as before this fix.
func TestClosedPnlSyncer_ZombieMatch_StillClosesGenuineZombie(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "zombiegenuine")
	accID := createTestAccount(t, s, userID)

	startedAt := time.Now().Add(-1 * time.Hour)
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'ZGENUSDT','long','grid','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,$2) RETURNING id`,
		stratID, startedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	// Closed 10 minutes AFTER the cycle started — the genuine "we missed this while the
	// gateway was down" scenario.
	closeTime := startedAt.Add(10 * time.Minute)
	p := trader.ClosedPnl{
		Symbol: "ZGENUSDT", OrderId: "genuine-zombie-close", OrderLinkId: "",
		Side: "Sell", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "1.1",
		ClosedPnl: "10.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, closeTime)

	var endedAt *time.Time
	var result *string
	if err := s.pool.QueryRow(ctx, `SELECT ended_at, result FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt, &result); err != nil {
		t.Fatalf("query cycle: %v", err)
	}
	if endedAt == nil {
		t.Fatal("cycle ended_at = NULL, want it set — a genuine zombie (started before the close) must still be ghost-closed")
	}
	if result == nil || *result != "ghost_close" {
		t.Errorf("cycle result = %v, want \"ghost_close\"", result)
	}
}
