//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// TestWriteGapTradeHistory_ComputesAndInsertsCorrectly is the regression for the
// 2026-09-02 incident: a strategy_cycle ended with result='tp' but closeCycle's async
// RecordStrategyTrade goroutine never wrote a trade_history row (a long-lived process's
// REST connectivity to Bybit died silently after a VPN/proxy drop, while its WS execution
// stream — and so strategy_cycles.result itself — kept updating fine). This exercises
// reconcileMissingTradeHistory's per-gap writer directly with a synthetic Bybit
// closed-pnl entry, mirroring what RecordStrategyTrade itself would have computed.
func TestWriteGapTradeHistory_ComputesAndInsertsCorrectly(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "gapwrite")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id) VALUES ($1,'Gap Test Bot',$2) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, category, status)
		 VALUES ($1,$2,$3,'GAPUSDT','long','grid','linear','active') RETURNING id`,
		userID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	startedAt := time.Now().Add(-1 * time.Hour)
	endedAt := time.Now().Add(-10 * time.Minute)
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,7,$2,$3,'tp') RETURNING id`,
		stratID, startedAt, endedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	// Two filled levels — VWAP should be (1.0*100 + 2.0*50) / 150 = 1.333...
	for i, lv := range []struct{ price, usdt float64 }{{1.0, 100}, {2.0, 100}} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
			 VALUES ($1,$2,$3,'Buy',$4,$5,'1','filled',$4)`,
			stratID, cycleID, i, lv.price, lv.usdt); err != nil {
			t.Fatalf("create level %d: %v", i, err)
		}
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_levels WHERE cycle_id=$1", cycleID) })

	// A fee execution inside the cycle's window, tagged as belonging to this strategy.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time, order_link_id)
		 VALUES ($1,$2,'gap-exec-1','bybit','GAPUSDT','linear','Trade',0.5,$3,$4)`,
		userID, accID, endedAt.Add(-5*time.Minute), "SIS_STR-"+stratID[:8]+"-tp-7-1"); err != nil {
		t.Fatalf("create execution: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	gap := tradeHistoryGap{
		cycleID: cycleID, strategyID: stratID, cycleNum: 7,
		startedAt: startedAt, endedAt: endedAt, result: "tp",
		symbol: "GAPUSDT", category: "linear", direction: "long",
		botID: &botID, ownerID: userID,
	}
	p := &trader.ClosedPnl{
		OrderId: "gap-bybit-order-1", Symbol: "GAPUSDT", Side: "Sell",
		Qty: "200", AvgExitPrice: "2.2", ClosedPnl: "13.33",
	}
	acc := closedPnlAccount{id: accID, ownerID: userID}

	if err := syncer.writeGapTradeHistory(ctx, acc, gap, p); err != nil {
		t.Fatalf("writeGapTradeHistory: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	var (
		gotStratID, gotBotID, gotSource, gotResult string
		gotAvgEntry, gotPnl, gotFees, gotNetPnl    float64
		gotCycleNum                                int
	)
	if err := s.pool.QueryRow(ctx,
		`SELECT strategy_id, bot_id, source, result, avg_entry, pnl, fees, net_pnl, cycle_num
		 FROM trade_history WHERE bybit_close_order_id=$1`,
		p.OrderId,
	).Scan(&gotStratID, &gotBotID, &gotSource, &gotResult, &gotAvgEntry, &gotPnl, &gotFees, &gotNetPnl, &gotCycleNum); err != nil {
		t.Fatalf("query written row: %v", err)
	}

	if gotStratID != stratID {
		t.Errorf("strategy_id = %q, want %q", gotStratID, stratID)
	}
	if gotBotID != botID {
		t.Errorf("bot_id = %q, want %q — bot attribution must survive the backfill", gotBotID, botID)
	}
	if gotSource != "strategy" {
		t.Errorf("source = %q, want %q", gotSource, "strategy")
	}
	if gotResult != "tp" {
		t.Errorf("result = %q, want %q", gotResult, "tp")
	}
	if gotCycleNum != 7 {
		t.Errorf("cycle_num = %d, want 7", gotCycleNum)
	}
	const wantAvgEntry = 1.0*100.0/150.0 + 2.0*50.0/150.0 // VWAP by USDT weight = 1.333...
	if diff := gotAvgEntry - wantAvgEntry; diff > 0.01 || diff < -0.01 {
		t.Errorf("avg_entry = %v, want ~%v", gotAvgEntry, wantAvgEntry)
	}
	if gotPnl != 13.33 {
		t.Errorf("pnl = %v, want 13.33 (from the exchange's own ClosedPnl)", gotPnl)
	}
	if gotFees != 0.5 {
		t.Errorf("fees = %v, want 0.5 (only the in-window execution)", gotFees)
	}
	if diff := gotNetPnl - 12.83; diff > 0.01 || diff < -0.01 {
		t.Errorf("net_pnl = %v, want ~12.83 (pnl - fees)", gotNetPnl)
	}
}

// TestWriteGapTradeHistory_Idempotent_NoDuplicateOnConflict: reconcileMissingTradeHistory
// runs every ~90s and could see the same gap again before the DB write from a prior tick
// is reflected — writeGapTradeHistory must be safe to call twice for the same cycle.
func TestWriteGapTradeHistory_Idempotent_NoDuplicateOnConflict(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "gapidem")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, category, status)
		 VALUES ($1,$2,'GAPIDEMUSDT','long','grid','linear','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	startedAt := time.Now().Add(-1 * time.Hour)
	endedAt := time.Now().Add(-10 * time.Minute)
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,$2,$3,'tp') RETURNING id`,
		stratID, startedAt, endedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	gap := tradeHistoryGap{
		cycleID: cycleID, strategyID: stratID, cycleNum: 1,
		startedAt: startedAt, endedAt: endedAt, result: "tp",
		symbol: "GAPIDEMUSDT", category: "linear", direction: "long", ownerID: userID,
	}
	p := &trader.ClosedPnl{OrderId: "gap-idem-order-1", Symbol: "GAPIDEMUSDT", Side: "Sell", Qty: "1", ClosedPnl: "1.0"}
	acc := closedPnlAccount{id: accID, ownerID: userID}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	if err := syncer.writeGapTradeHistory(ctx, acc, gap, p); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second call — same (strategy_id, cycle_num) — must not error and must not duplicate.
	p2 := &trader.ClosedPnl{OrderId: "gap-idem-order-2-different", Symbol: "GAPIDEMUSDT", Side: "Sell", Qty: "1", ClosedPnl: "999"}
	if err := syncer.writeGapTradeHistory(ctx, acc, gap, p2); err != nil {
		t.Fatalf("second write: %v", err)
	}

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE strategy_id=$1 AND cycle_num=1`, stratID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("trade_history rows for (strategy,cycle) = %d, want 1 (ON CONFLICT DO NOTHING must hold)", count)
	}
}

// TestWriteGapTradeHistory_FeesScopedToCycleWindow: a backfill can run hours or days
// after the cycle actually closed, so — unlike a live RecordStrategyTrade call, which
// uses time.Now() as its fee-lookup upper bound because "now" and "close time" are
// nearly the same — this MUST use the cycle's own endedAt, not time.Now(), as the upper
// bound. Otherwise a much-later backfill would sweep in fees from every trade this
// symbol made since, wildly inflating (or randomly changing) this one cycle's net_pnl.
func TestWriteGapTradeHistory_FeesScopedToCycleWindow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "gapfeewin")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, category, status)
		 VALUES ($1,$2,'GAPFEEUSDT','long','grid','linear','active') RETURNING id`,
		userID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// Cycle closed 3 days ago — this backfill is a genuinely late catch-up.
	startedAt := time.Now().Add(-73 * time.Hour)
	endedAt := time.Now().Add(-72 * time.Hour)
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,2,$2,$3,'tp') RETURNING id`,
		stratID, startedAt, endedAt).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	// In-window fee (must be counted) + a fee from a much later, unrelated close on the
	// same symbol (must NOT be counted, even though it happened before "now").
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES ($1,$2,'gapfee-inwindow','bybit','GAPFEEUSDT','linear','Trade',0.2,$3)`,
		userID, accID, endedAt.Add(-30*time.Minute)); err != nil {
		t.Fatalf("create in-window execution: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES ($1,$2,'gapfee-later','bybit','GAPFEEUSDT','linear','Trade',99.0,$3)`,
		userID, accID, time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("create later execution: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	gap := tradeHistoryGap{
		cycleID: cycleID, strategyID: stratID, cycleNum: 2,
		startedAt: startedAt, endedAt: endedAt, result: "tp",
		symbol: "GAPFEEUSDT", category: "linear", direction: "long", ownerID: userID,
	}
	p := &trader.ClosedPnl{OrderId: "gap-feewin-order-1", Symbol: "GAPFEEUSDT", Side: "Sell", Qty: "1", ClosedPnl: "10.0"}
	acc := closedPnlAccount{id: accID, ownerID: userID}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	if err := syncer.writeGapTradeHistory(ctx, acc, gap, p); err != nil {
		t.Fatalf("writeGapTradeHistory: %v", err)
	}

	var fees float64
	if err := s.pool.QueryRow(ctx, `SELECT fees FROM trade_history WHERE bybit_close_order_id=$1`, p.OrderId).Scan(&fees); err != nil {
		t.Fatalf("query: %v", err)
	}
	if fees != 0.2 {
		t.Errorf("fees = %v, want 0.2 — the later, unrelated execution (fee=99) must not leak in just because the backfill ran long after endedAt", fees)
	}
}
