//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestClosedPnlSyncer_MatrixTPLinkID_RecordsProfitNotZombie: a ClosedPnl entry whose
// orderLinkId identifies a matrix-TP re-arm must be recorded into matrix_tp_profits and
// must NOT touch the strategy's open cycle (no ghost_close/zombie force-end) — this is
// the exact bug being fixed: a healthy, still-trading matrix cycle looks like a "zombie"
// to the old time-window heuristics.
func TestClosedPnlSyncer_MatrixTPLinkID_RecordsProfitNotZombie(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsync")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'CPLUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM matrix_tp_profits WHERE strategy_id=$1", stratID) })

	// An OPEN cycle — same state a healthy, currently-trading matrix cycle is always in.
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	linkID := "SIS_STR-" + stratID[:8] + "-tpl2-1-99001"
	p := trader.ClosedPnl{
		Symbol: "CPLUSDT", OrderId: "bybit-order-xyz", OrderLinkId: linkID,
		Side: "Sell", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "1.05",
		ClosedPnl: "5.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	// 1. Recorded into matrix_tp_profits, keyed by the real Bybit order id.
	var count int
	var netPnl float64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(net_pnl),0) FROM matrix_tp_profits WHERE strategy_id=$1 AND bybit_order_id=$2`,
		stratID, p.OrderId).Scan(&count, &netPnl); err != nil {
		t.Fatalf("query matrix_tp_profits: %v", err)
	}
	if count != 1 {
		t.Fatalf("matrix_tp_profits rows = %d, want 1", count)
	}
	if netPnl != 5.0 {
		t.Errorf("net_pnl = %v, want 5.0", netPnl)
	}

	// 2. The cycle must remain OPEN — no false zombie-close.
	var endedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT ended_at FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt); err != nil {
		t.Fatalf("query cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("cycle ended_at = %v, want NULL (must stay open — this is a re-arm, not a real close)", *endedAt)
	}

	// 3. No unattributed manual row was created for this order.
	var manualCount int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&manualCount)
	if manualCount != 0 {
		t.Errorf("trade_history rows for this order = %d, want 0 (matrix-TP goes to matrix_tp_profits, not trade_history)", manualCount)
	}
}

// TestClosedPnlSyncer_MatrixLevelSLLinkID_NoTradeHistoryWrite: a per-level matrix SL
// linkId is already tracked via strategy_levels.realized_pnl — the syncer must not also
// write a trade_history row for it (would be a duplicate/orphan entry).
func TestClosedPnlSyncer_MatrixLevelSLLinkID_NoTradeHistoryWrite(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncsl")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'CPLSLUSDT','short','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	linkID := "SIS_STR-" + stratID[:8] + "-msl-2-5"
	p := trader.ClosedPnl{
		Symbol: "CPLSLUSDT", OrderId: "bybit-order-msl-1", OrderLinkId: linkID,
		Side: "Buy", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "0.95",
		ClosedPnl: "-0.5", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	var count int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&count)
	if count != 0 {
		t.Errorf("trade_history rows = %d, want 0 (per-level SL already tracked elsewhere)", count)
	}
}

// TestClosedPnlSyncer_UnrecognizedLinkID_FallsBackToLegacyManual: an order with no
// SIS_STR- prefix (a genuine manual exchange trade) must still land as an unattributed
// manual row — the existing fallback path must be unaffected by this change.
func TestClosedPnlSyncer_UnrecognizedLinkID_FallsBackToLegacyManual(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncmanual")
	accID := createTestAccount(t, s, userID)

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	p := trader.ClosedPnl{
		Symbol: "MANUALUSDT", OrderId: "bybit-order-manual-1", OrderLinkId: "",
		Side: "Sell", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "1.1",
		ClosedPnl: "1.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE bybit_close_order_id=$1", p.OrderId) })

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	var count int
	var result string
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("trade_history rows = %d (err=%v), want 1 (legacy manual fallback)", count, err)
	}
	s.pool.QueryRow(ctx, `SELECT result FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&result)
	if result != "manual" {
		t.Errorf("result = %q, want %q (unchanged legacy behavior)", result, "manual")
	}
}
