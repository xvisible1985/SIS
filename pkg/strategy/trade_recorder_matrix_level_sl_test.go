//go:build integration

package strategy

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mlslReadTradeHistory(t *testing.T, pool *pgxpool.Pool, ctx context.Context, accountID, orderID string) (
	exists bool, strategyID, botID *string, cycleNum int, result, source string, isPartial bool, netPnl float64,
) {
	t.Helper()
	err := pool.QueryRow(ctx, `
		SELECT strategy_id, bot_id, cycle_num, result, source, is_partial, net_pnl
		FROM trade_history WHERE account_id=$1 AND bybit_close_order_id=$2`,
		accountID, orderID,
	).Scan(&strategyID, &botID, &cycleNum, &result, &source, &isPartial, &netPnl)
	if err == nil {
		exists = true
	}
	return
}

// TestRecordMatrixLevelSLTradeNow_WritesAttributedPartialRow: the core fix verified end to
// end — a per-level matrix SL close must land in trade_history correctly attributed to its
// owning strategy and bot from the moment it's recorded, not reconstructed later by
// ClosedPnlSyncer's unreliable orderLinkId/sl_order_id fallbacks (see the full-account audit,
// 2026-10-18: dozens of these fell through to source='manual' with no strategy or bot).
// is_partial=true so it doesn't collide with idx_trade_history_strategy_cycle's
// one-row-per-full-cycle-close scope.
func TestRecordMatrixLevelSLTradeNow_WritesAttributedPartialRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSRECUSDT")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", f.accID) })

	var botID string
	if err := pool.QueryRow(ctx, `SELECT bot_id FROM strategies WHERE id=$1`, f.mainID).Scan(&botID); err != nil {
		t.Fatalf("read bot_id: %v", err)
	}

	recordMatrixLevelSLTradeNow(ctx, pool, MatrixLevelSLRecordInput{
		Strategy: Strategy{
			ID: f.mainID, OwnerID: f.ownerID, AccountID: f.accID,
			BotID: &botID, Symbol: "MLSRECUSDT", Category: "linear", Direction: DirectionLong,
		},
		CycleNum: 3, OrderID: "rec-sl-order-1",
		EntryPrice: 100.0, ExitPrice: 98.0, Qty: 5.0, VolumeUSDT: 500.0,
		GrossPnl: 10.0,
	})

	exists, stratID, botIDGot, cycleNum, result, source, isPartial, netPnl :=
		mlslReadTradeHistory(t, pool, ctx, f.accID, "rec-sl-order-1")
	if !exists {
		t.Fatalf("trade_history row was not written")
	}
	if stratID == nil || *stratID != f.mainID {
		t.Errorf("strategy_id = %v, want %s — must be attributed to the owning strategy", stratID, f.mainID)
	}
	if botIDGot == nil || *botIDGot != botID {
		t.Errorf("bot_id = %v, want %s — must be attributed to the owning bot", botIDGot, botID)
	}
	if cycleNum != 3 {
		t.Errorf("cycle_num = %d, want 3", cycleNum)
	}
	if result != "sl" {
		t.Errorf("result = %q, want \"sl\"", result)
	}
	if source != "strategy" {
		t.Errorf("source = %q, want \"strategy\" — must never fall into the unattributed 'manual' bucket", source)
	}
	if !isPartial {
		t.Errorf("is_partial = false, want true — a level close must not collide with the cycle's eventual terminal close")
	}
	if netPnl != 10.0 {
		t.Errorf("net_pnl = %v, want 10.0 (no trader_executions fee rows seeded -> fees=0)", netPnl)
	}
}

// TestRecordMatrixLevelSLTradeNow_SameCycleMultiplePartials_BothRowsCoexist: a matrix cycle
// can have several per-level SL closes before it finally ends via its global TP/SL — this is
// exactly why is_partial exists (idx_trade_history_strategy_cycle only enforces uniqueness
// among non-partial rows). Two partial closes in the same cycle must both persist, not
// collide or silently overwrite each other.
func TestRecordMatrixLevelSLTradeNow_SameCycleMultiplePartials_BothRowsCoexist(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSMULTIUSDT")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", f.accID) })

	strat := Strategy{ID: f.mainID, OwnerID: f.ownerID, AccountID: f.accID, Symbol: "MLSMULTIUSDT", Category: "linear", Direction: DirectionLong}

	recordMatrixLevelSLTradeNow(ctx, pool, MatrixLevelSLRecordInput{
		Strategy: strat, CycleNum: 1, OrderID: "multi-order-1",
		EntryPrice: 100, ExitPrice: 98, Qty: 1, VolumeUSDT: 100, GrossPnl: 2.0,
	})
	recordMatrixLevelSLTradeNow(ctx, pool, MatrixLevelSLRecordInput{
		Strategy: strat, CycleNum: 1, OrderID: "multi-order-2",
		EntryPrice: 101, ExitPrice: 99, Qty: 1, VolumeUSDT: 101, GrossPnl: 2.0,
	})

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trade_history WHERE strategy_id=$1 AND cycle_num=1`, f.mainID).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Errorf("trade_history rows for cycle 1 = %d, want 2 — two distinct partial closes must both persist", count)
	}
}

// TestRecordMatrixLevelSLTradeNow_Idempotent_NoDuplicateOnConflict: a replayed call for the
// same closing order (e.g. a retry, or ClosedPnlSyncer independently rediscovering the same
// close) must be a safe no-op, not a duplicate row or an error — relies on the existing
// (account_id, bybit_close_order_id) unique index, same dedup key InsertMatrixTPProfit uses.
func TestRecordMatrixLevelSLTradeNow_Idempotent_NoDuplicateOnConflict(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSIDEMUSDT")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", f.accID) })

	strat := Strategy{ID: f.mainID, OwnerID: f.ownerID, AccountID: f.accID, Symbol: "MLSIDEMUSDT", Category: "linear", Direction: DirectionLong}
	in := MatrixLevelSLRecordInput{
		Strategy: strat, CycleNum: 1, OrderID: "idem-order-1",
		EntryPrice: 100, ExitPrice: 98, Qty: 1, VolumeUSDT: 100, GrossPnl: 2.0,
	}
	recordMatrixLevelSLTradeNow(ctx, pool, in)
	recordMatrixLevelSLTradeNow(ctx, pool, in) // replay

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM trade_history WHERE account_id=$1 AND bybit_close_order_id='idem-order-1'`, f.accID).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("trade_history rows = %d, want 1 — a replayed call must not create a duplicate", count)
	}
}

// TestRecordMatrixLevelSLTradeNow_NetsFeesFromTraderExecutions: mirrors
// TestAccumulateMatrixLevelSLPnlNow_NetsFeesFromTraderExecutions — the recorded net_pnl must
// be fee-adjusted the same way, from the same trader_executions source, scoped to this
// order's own fee rows only.
func TestRecordMatrixLevelSLTradeNow_NetsFeesFromTraderExecutions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	f := newMLSLFixture(t, pool, ctx, "MLSFEEUSDT")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", f.accID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, order_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES
		   ($1,$2,'fee-exec-1','fee-sl-order','bybit','MLSFEEUSDT','linear','Trade',0.75,NOW()),
		   ($1,$2,'fee-exec-2','fee-sl-order','bybit','MLSFEEUSDT','linear','Trade',-0.30,NOW()),
		   ($1,$2,'fee-exec-3','fee-sl-order-OTHER','bybit','MLSFEEUSDT','linear','Trade',999.0,NOW())`,
		f.ownerID, f.accID); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", f.accID) })

	recordMatrixLevelSLTradeNow(ctx, pool, MatrixLevelSLRecordInput{
		Strategy:   Strategy{ID: f.mainID, OwnerID: f.ownerID, AccountID: f.accID, Symbol: "MLSFEEUSDT", Category: "linear", Direction: DirectionLong},
		CycleNum:   1,
		OrderID:    "fee-sl-order",
		EntryPrice: 100, ExitPrice: 98, Qty: 1, VolumeUSDT: 100,
		GrossPnl: 10.0,
	})

	_, _, _, _, _, _, _, netPnl := mlslReadTradeHistory(t, pool, ctx, f.accID, "fee-sl-order")
	if netPnl != 8.95 {
		t.Errorf("net_pnl = %v, want 8.95 (10.0 gross - (0.75+0.30) fees, unrelated order excluded)", netPnl)
	}
}
