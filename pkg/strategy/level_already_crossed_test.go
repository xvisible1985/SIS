//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestPlaceLevel_FallsBackToMarketWhenTriggerAlreadyCrossed is the regression test for
// the live bug (2026-10-18, strategy NEARUSDT, acc N2 Binance): a grid DCA entry level
// with EntryOrderType="stop_market" was placed as a conditional (trigger) order even
// when the current mark price had already moved past its target — every exchange
// rejects a conditional order whose trigger condition is already satisfied at placement
// time (Binance: -2021 "Order would immediately trigger"), so the level got stuck
// "pending" forever, repeating the same rejection on every subsequent restart-grid
// recalculation without ever actually buying the dip it was meant to.
//
// Mirrors the existing TP circuit breaker's philosophy (checkPositionAfterTPCircuitBreaker,
// cycle.go) — when the price has already passed a target, place at market immediately
// instead of a doomed conditional trigger.
func TestPlaceLevel_FallsBackToMarketWhenTriggerAlreadyCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"lac-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, entry_order_type)
		 VALUES ($1,$2,'LACUSDT','long','grid','active','stop_market') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,3,'Buy',4.8149,10,'2.08','pending') RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	fake := &fakeExchange{}
	// Mark price has already fallen BELOW the level's 4.8149 target — the exact live
	// scenario: a long grid's Buy level triggers on a price FALL, and the fall already
	// happened before placement was attempted.
	fake.getMarkPriceQ.push(4.7080, nil)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "mkt-1", OrderLinkId: "link-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "LACUSDT", Category: "linear",
			Direction: DirectionLong, EntryOrderType: "stop_market",
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.0001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 3, Side: "Buy", TargetPrice: 4.8149, Qty: "2.08", Status: LevelPending}},
	}

	if err := sr.placeLevel(ctx, 0); err != nil {
		t.Fatalf("placeLevel: %v", err)
	}

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.OrderType != "Market" {
		t.Errorf("OrderType = %q, want Market (a conditional trigger whose condition is already satisfied must fall back to an immediate market order)", got.OrderType)
	}
	if got.TriggerPrice != "" {
		t.Errorf("TriggerPrice = %q, want empty — must not send a conditional order once the trigger is already crossed", got.TriggerPrice)
	}
	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %q, want %q", sr.levels[0].Status, LevelPlaced)
	}
}

// TestPlaceLevel_UsesStopMarketWhenTriggerNotYetCrossed is the positive counterpart —
// when the mark price has NOT yet reached the level's target, the normal conditional
// stop_market order must still be placed (no change to the existing, working case).
func TestPlaceLevel_UsesStopMarketWhenTriggerNotYetCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"lac2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, entry_order_type)
		 VALUES ($1,$2,'LAC2USDT','long','grid','active','stop_market') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,2,'Buy',4.913,10,'2.08','pending') RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	fake := &fakeExchange{}
	// Mark price is still ABOVE the level's 4.913 target — not crossed yet.
	fake.getMarkPriceQ.push(4.988, nil)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "stop-1", OrderLinkId: "link-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "LAC2USDT", Category: "linear",
			Direction: DirectionLong, EntryOrderType: "stop_market",
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.0001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 2, Side: "Buy", TargetPrice: 4.913, Qty: "2.08", Status: LevelPending}},
	}

	if err := sr.placeLevel(ctx, 0); err != nil {
		t.Fatalf("placeLevel: %v", err)
	}

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.TriggerPrice == "" {
		t.Errorf("TriggerPrice is empty, want the conditional stop_market order (trigger not yet crossed)")
	}
	if got.OrderFilter != "StopOrder" {
		t.Errorf("OrderFilter = %q, want StopOrder", got.OrderFilter)
	}
}

// TestPlaceLevelsBatch_FallsBackToMarketWhenTriggerAlreadyCrossed is
// TestPlaceLevel_FallsBackToMarketWhenTriggerAlreadyCrossed's counterpart for the batch
// placement path (placeLevelsBatch, used when 2+ pending levels are placed in one call) —
// the live NEARUSDT incident actually hit this path first (L2 and L3 placed together in
// one batch after a restart-grid reprice), not the single-level path.
func TestPlaceLevelsBatch_FallsBackToMarketWhenTriggerAlreadyCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, l2ID, l3ID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"lacb-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, entry_order_type)
		 VALUES ($1,$2,'LACBUSDT','long','grid','active','stop_market') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,2,'Buy',4.913,10,'2.08','pending') RETURNING id`,
		stratID, cycleID).Scan(&l2ID); err != nil {
		t.Fatalf("create level 2: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,3,'Buy',4.8149,10,'2.08','pending') RETURNING id`,
		stratID, cycleID).Scan(&l3ID); err != nil {
		t.Fatalf("create level 3: %v", err)
	}

	fake := &fakeExchange{}
	// Mark price is below BOTH targets — the exact live scenario (price fell past both
	// L2 at 4.913 and L3 at 4.8149 before either got placed).
	fake.getMarkPriceQ.push(4.7080, nil)
	fake.placeOrderBatchQ.push([]trader.BatchPlaceResult{
		{OrderId: "mkt-2", OrderLinkId: "link-2"},
		{OrderId: "mkt-3", OrderLinkId: "link-3"},
	}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "LACBUSDT", Category: "linear",
			Direction: DirectionLong, EntryOrderType: "stop_market",
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.0001},
		levels: []GridLevel{
			{ID: l2ID, LevelIdx: 2, Side: "Buy", TargetPrice: 4.913, Qty: "2.08", Status: LevelPending},
			{ID: l3ID, LevelIdx: 3, Side: "Buy", TargetPrice: 4.8149, Qty: "2.08", Status: LevelPending},
		},
	}

	sr.placeLevelsBatch(ctx, []int{0, 1})

	if len(fake.placeOrderBatchCalls) != 1 {
		t.Fatalf("placeOrderBatchCalls = %d, want 1", len(fake.placeOrderBatchCalls))
	}
	items := fake.placeOrderBatchCalls[0].Request
	if len(items) != 2 {
		t.Fatalf("batch items = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.OrderType != "Market" {
			t.Errorf("item %s: OrderType = %q, want Market (trigger already crossed)", it.OrderLinkId, it.OrderType)
		}
		if it.TriggerPrice != "" {
			t.Errorf("item %s: TriggerPrice = %q, want empty", it.OrderLinkId, it.TriggerPrice)
		}
	}
}
