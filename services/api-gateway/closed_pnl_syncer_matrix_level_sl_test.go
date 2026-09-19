//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestClosedPnlSyncer_MatrixLevelSL_OrderLinkIdMissing_StillNotGhostClosed reproduces the
// incident found live 2026-09-19 on CROSSUSDT: a matrix hedge cycle has several levels.
// Levels 1-2 close via their own per-level SL (handleMatrixSLFill marks them 'sl_closed'
// directly on strategy_levels — this never touches strategy_cycles or trade_history).
// Levels 3-4 remain genuinely open ('filled') on the exchange, unprotected.
//
// processClosedPnl is designed to recognize a per-level matrix SL close via the closing
// order's OrderLinkId (ParseStrategyLinkID → LinkIDMatrixLevelSL, see processLinkIDAttributed)
// and skip it entirely — the in-process engine already accounted for it. But Bybit does not
// reliably echo back a conditional/stop order's OrderLinkId once it triggers and converts to
// a market fill: the ClosedPnl record for order "9c17f82a-..." (CROSSUSDT's real L(0) SL,
// confirmed by matching strategy_levels.sl_order_id) came back with an EMPTY OrderLinkId, so
// the linkID path never matched. With no other check, processClosedPnl fell all the way to
// the zombie-cycle heuristic and force-closed the ENTIRE cycle (ghost_close) — even though
// levels 3-4 were still open — orphaning a live, unprotected ~524-qty short position.
//
// This is the fix: fall back to matching the closing order id directly against
// strategy_levels.sl_order_id before ever reaching the zombie-cycle branch, independent of
// whether OrderLinkId survived the round trip.
func TestClosedPnlSyncer_MatrixLevelSL_OrderLinkIdMissing_StillNotGhostClosed(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "mslghost")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'MSLUSDT','short','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	cycleStartedAt := time.Now().Add(-1 * time.Hour)
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,$2) RETURNING id`,
		stratID, cycleStartedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_levels WHERE strategy_id=$1", stratID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	// Level 1: already closed via its own per-level SL — this is the order whose ClosedPnl
	// event we are about to process.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, sl_order_id)
		 VALUES ($1,$2,1,'Sell',1.0,50,'100','sl_closed','order-msl-level1')`,
		stratID, cycleID); err != nil {
		t.Fatalf("seed closed level: %v", err)
	}
	// Level 2: still genuinely open/filled — this is the position that must stay protected.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,2,'Sell',1.0,50,'100','filled')`,
		stratID, cycleID); err != nil {
		t.Fatalf("seed open level: %v", err)
	}

	closeTime := cycleStartedAt.Add(10 * time.Minute)
	p := trader.ClosedPnl{
		Symbol: "MSLUSDT", OrderId: "order-msl-level1", OrderLinkId: "", // empty: Bybit didn't echo it back
		Side: "Buy", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "0.99",
		ClosedPnl: "1.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, closeTime)

	var endedAt *time.Time
	var result *string
	if err := s.pool.QueryRow(ctx, `SELECT ended_at, result FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt, &result); err != nil {
		t.Fatalf("query cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("cycle ended_at = %v, want NULL — a per-level matrix SL close must never ghost-close the whole cycle while other levels are still open", *endedAt)
	}
	if result != nil {
		t.Errorf("cycle result = %q, want NULL", *result)
	}

	var ghostCount int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE strategy_id=$1 AND result='ghost_close'`, stratID).Scan(&ghostCount)
	if ghostCount != 0 {
		t.Errorf("ghost_close trade_history rows = %d, want 0 — handleMatrixSLFill already accounted for this level's PnL, trade_history must stay untouched", ghostCount)
	}
}
