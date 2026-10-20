//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestHandleAlsLevelSLFill_ClosesLevelAndRestoresFromMemory is the regression test for
// the real event-hook this session's Polish plan explicitly left unwired: when an ALS
// per-level stop actually fills (refType="als_sl", engine.go's OnOrderEvent dispatch),
// handleAlsLevelSLFill must mark the level sl_closed, clear its SLOrderID, and restore
// the nearest remembered stop from als_stop_memory into the now-freed slot.
func TestHandleAlsLevelSLFill_ClosesLevelAndRestoresFromMemory(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-slfill-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "restored-after-fill", OrderLinkId: "restored-link"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode) VALUES ($1,$2,'ALSSLFILLUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, l1ID, l2ID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	// l1: filled at 90, qty 2, stop live at 88 — about to fire.
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price, sl_order_id, sl_link_id, sl_price) VALUES ($1,$2,1,'Sell',90,0,'2','filled',90,'sl-order-1','sl-link-1',88) RETURNING id`,
		stratID, cycleID).Scan(&l1ID)
	// l2: a different filled level with no stop — not touched by this test, just confirms
	// handleAlsLevelSLFill doesn't disturb unrelated levels.
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,2,'Sell',70,0,'1','filled') RETURNING id`,
		stratID, cycleID).Scan(&l2ID)
	// A remembered stop waiting in memory, tied to l2 — should get restored once l1's
	// slot frees up.
	pool.Exec(ctx, `INSERT INTO als_stop_memory (strategy_id, level_id, symbol, side, trigger_price, qty) VALUES ($1,$2,'ALSSLFILLUSDT','Buy',75,'1')`,
		stratID, l2ID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, AccountID: accID, Symbol: "ALSSLFILLUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: l1ID, LevelIdx: 1, Side: "Sell", Status: LevelFilled, Qty: "2", FilledPrice: 90, SLOrderID: "sl-order-1", SLLinkID: "sl-link-1", SLPrice: 88},
			{ID: l2ID, LevelIdx: 2, Side: "Sell", Status: LevelFilled, Qty: "1"},
		},
	}

	sr.handleAlsLevelSLFill(ctx, l1ID, 88.0)

	if sr.levels[0].Status != LevelSLClosed {
		t.Errorf("level 1 status = %q, want sl_closed", sr.levels[0].Status)
	}
	if sr.levels[0].SLOrderID != "" {
		t.Errorf("level 1 SLOrderID = %q, want cleared", sr.levels[0].SLOrderID)
	}
	if sr.levels[1].SLOrderID != "restored-after-fill" {
		t.Errorf("level 2 SLOrderID = %q, want restored-after-fill (restored from memory)", sr.levels[1].SLOrderID)
	}

	var dbStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM strategy_levels WHERE id=$1`, l1ID).Scan(&dbStatus); err != nil {
		t.Fatalf("read back level 1: %v", err)
	}
	if dbStatus != "sl_closed" {
		t.Errorf("DB level 1 status = %q, want sl_closed", dbStatus)
	}

	var memCount int
	pool.QueryRow(ctx, `SELECT count(*) FROM als_stop_memory WHERE strategy_id=$1`, stratID).Scan(&memCount)
	if memCount != 0 {
		t.Errorf("als_stop_memory rows = %d, want 0 (restored entry removed)", memCount)
	}
}
