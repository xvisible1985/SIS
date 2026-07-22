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

// TestClosedPnlSyncer_RecognizedLinkIDNoMatchingStrategy_FallsBackToLegacyManual: an
// orderLinkId that matches our SIS_STR-{8hex}- format (so ParseStrategyLinkID succeeds)
// but whose 8 hex chars don't resolve to any strategy row on this account (e.g. a
// deleted strategy) must fall through to the legacy manual-attribution path — the
// documented safety net in processLinkIDAttributed's "no matching strategy" branch.
// This is a materially different code path from the empty-linkId case above:
// ParseStrategyLinkID returns ok=true here, and processLinkIDAttributed itself returns
// false after failing to find the strategy.
func TestClosedPnlSyncer_RecognizedLinkIDNoMatchingStrategy_FallsBackToLegacyManual(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncnostrat")
	accID := createTestAccount(t, s, userID)

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	// "deadbeef" is a well-formed 8-hex prefix but does not correspond to any strategy
	// row created for this (or any) account.
	linkID := "SIS_STR-deadbeef-tp-1-1"
	p := trader.ClosedPnl{
		Symbol: "NOSTRATUSDT", OrderId: "bybit-order-nostrat-1", OrderLinkId: linkID,
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

// TestClosedPnlSyncer_MatrixTPLinkID_AlreadyInTradeHistory_SkipsDoubleCount: a matrix-TP
// linkId (old pre-2026-07-21 format, still resting on the exchange for any cycle that was
// already open and re-armed before the matrix-cycle-lifecycle redesign deployed) must NOT
// write into matrix_tp_profits if trade_history already has a row for this exact closing
// order — that means the in-process closeCycle/RecordStrategyTrade path already attributed
// this exact close (the new architecture: matrix TP now always ends the cycle and writes
// trade_history, same as hedge/grid). Writing into matrix_tp_profits too would double-count
// this same close in every "Накоплено" figure that unions both tables. Found in code review
// (2026-07-21) during the matrix-cycle-lifecycle-redesign plan's Task 4: a real,
// money-affecting race between this poller and the in-process recorder for the transient
// population of cycles whose live TP order predates the Task 3 linkId fix.
func TestClosedPnlSyncer_MatrixTPLinkID_AlreadyInTradeHistory_SkipsDoubleCount(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cplsyncdupe")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'CPLDUPUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM matrix_tp_profits WHERE strategy_id=$1", stratID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,NOW(),NOW(),'tp') RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	orderID := "bybit-order-dupe-1"
	// Simulate RecordStrategyTrade already having won the race: trade_history already has
	// a row for this exact closing order.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		 cycle_num, result, source, avg_entry, exit_price, qty, volume_usdt, pnl, pnl_pct,
		 opened_at, closed_at, fees, funding, net_pnl, bybit_close_order_id)
		 VALUES ($1,$2,$3,'CPLDUPUSDT','linear','long',1,'tp','strategy',1.0,1.05,100,100,5.0,5.0,NOW(),NOW(),0,0,5.0,$4)`,
		stratID, accID, userID, orderID); err != nil {
		t.Fatalf("seed trade_history: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE bybit_close_order_id=$1", orderID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	linkID := "SIS_STR-" + stratID[:8] + "-tpl2-1-99002" // old format — still recognized by the parser
	p := trader.ClosedPnl{
		Symbol: "CPLDUPUSDT", OrderId: orderID, OrderLinkId: linkID,
		Side: "Sell", Qty: "100", AvgEntryPrice: "1.0", AvgExitPrice: "1.05",
		ClosedPnl: "5.0", CreatedTime: "0", Category: "linear",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	syncer.processClosedPnl(ctx, acc, trader.Credentials{}, p, time.Now())

	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM matrix_tp_profits WHERE strategy_id=$1 AND bybit_order_id=$2`,
		stratID, orderID).Scan(&count); err != nil {
		t.Fatalf("query matrix_tp_profits: %v", err)
	}
	if count != 0 {
		t.Errorf("matrix_tp_profits rows = %d, want 0 (already attributed via trade_history — must not double-count)", count)
	}
}
