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
