//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed is the core engine's end-to-end
// regression test: a long main position with a live avg entry of 100 USDT/coin and 10
// coins. One threshold at -10%/target 30%. Note mainUsdt is effectiveDeposit(currentPrice)
// — sizeCoins × the TICK price, not the avg entry — so at the tick price used below (85)
// mainUsdt is 10×85=850, not 10×100=1000.
func TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tick-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-1", OrderLinkId: "link-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	// Main is LONG (this hedge is SHORT, direction=short, so the opposite/main positionIdx is 1).
	setWSPosition(ar, "ALSTICKUSDT", 1, 10, 100) // main: 10 coins @ avg entry 100 (mainUsdt depends on the TICK price below, not this entry price)
	// No hedge position open yet.

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSTICKUSDT','short','als','active',true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID,
	).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSTICKUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	// Main is LONG at avg entry 100 — adverse for a long main is price FALLING. This
	// hedge's own direction is Short, so per alsThresholdPrice's convention (hedge Short
	// ⟹ main Long ⟹ trigger lands BELOW base): 100*(1+(-10)/100)=90. Tick at 85, below
	// it, to cross.
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.Side != "Sell" {
		t.Errorf("Side = %q, want Sell (short hedge)", got.Side)
	}
	// mainUsdt = effectiveDeposit(85) = 10*85 = 850. target 30% of 850 = 255 USDT,
	// at price 85 → 255/85 = 3.0 exactly.
	if got.Qty != "3.000" {
		t.Errorf("Qty = %q, want 3.000 (255 USDT / 85)", got.Qty)
	}

	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %q, want placed", sr.levels[0].Status)
	}
}

// TestAlsPriceTick_SecondLevelOnlyToppedUpByDelta confirms the sizing is cumulative, not
// flat-add: level 1 already has 3 coins open, level 2's target is 50% of main — must top
// up only the remaining delta, not another flat 50%-of-main add.
//
// Main LONG, avgEntry=100, 10 coins live. Hedge direction Short (main Long ⟹ trigger
// lands BELOW base, per alsThresholdPrice's convention). Level 2: trigger_pct=-30 ⟹
// trigger = 100*(1+(-30)/100) = 70. Tick at 65 (below it) to cross. At that tick price,
// mainUsdt = effectiveDeposit(65) = 10*65 = 650; the hedge's own 3 already-open coins =
// 3*65 = 195 USDT; target = 50% of 650 = 325; top-up = 325-195 = 130 USDT; qty =
// 130/65 = 2.0 exactly — chosen deliberately so the result isn't sensitive to
// FormatQty's floor-rounding (confirmed via pkg/trader/instruments.go: FormatQty floors
// to the nearest QtyStep multiple, not round-to-nearest).
func TestAlsPriceTick_SecondLevelOnlyToppedUpByDelta(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tick2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-2", OrderLinkId: "link-2"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSTICK2USDT", alsMainPositionIdx(DirectionShort), 10, 100) // main: long, 10 coins @ avg entry 100
	setWSPosition(ar, "ALSTICK2USDT", alsOwnPositionIdx(DirectionShort, true), 3, 90) // hedge: 3 coins already open (filled at level 1's own trigger, 90)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSTICK2USDT','short','als','active',true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, l1ID, l2ID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',90,0,'3','placed') RETURNING id`,
		stratID, cycleID).Scan(&l1ID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,2,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID).Scan(&l2ID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSTICK2USDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true,
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30},
				{TriggerType: 1, TriggerPct: -30, TargetHedgePct: 50},
			},
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: l1ID, LevelIdx: 1, Side: "Sell", Status: LevelPlaced, TargetPrice: 90, Qty: "3"},
			{ID: l2ID, LevelIdx: 2, Side: "Sell", Status: LevelPending},
		},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 65)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.Qty != "2.000" {
		t.Errorf("Qty = %q, want 2.000 (130 USDT top-up / 65)", got.Qty)
	}
}
