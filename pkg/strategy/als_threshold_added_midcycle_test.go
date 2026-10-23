//go:build integration

package strategy

import (
	"context"
	"strings"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_ExchangeModeToggledOff_CancelsOrphanedOrderAndRecoversVirtually is the
// regression for the 2026-10-23 gap: turning AlsExchangeOrders off while a real
// conditional order is still resting on the exchange used to leave it there forever —
// alsPriceTick's per-level loop simply stopped looking at that level once
// AlsExchangeOrders(false) && Status==LevelPlaced no longer matched the "exchangePending"
// exception. Now the loop must cancel the stale order and fall back to pending so the
// virtual/reactive path picks the threshold back up on the SAME tick.
func TestAlsPriceTick_ExchangeModeToggledOff_CancelsOrphanedOrderAndRecoversVirtually(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xoff-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.cancelOrderQ.push(struct{}{}, nil) // cancelling the stale resting order succeeds
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-1", OrderLinkId: "link-top-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSXOFFUSDT", 1, 10, 100) // main: long, 10 coins @ avg entry 100

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, als_exchange_orders)
		VALUES ($1,$2,'ALSXOFFUSDT','short','als','active',true,true,false) RETURNING id`,
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
	// Level already has a REAL resting conditional order from when exchange mode was on.
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, exchange_order_id, exchange_link_id)
		VALUES ($1,$2,1,'Sell',90,0,'3.166','placed','old-x-1','old-link-1') RETURNING id`,
		stratID, cycleID,
	).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSXOFFUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: false, // just toggled off
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPlaced, TargetPrice: 90, Qty: "3.166", ExchangeOrderID: "old-x-1", ExchangeLinkID: "old-link-1"},
		},
	}

	// Trigger = 100*0.9 = 90. Tick at 85 — already past it, so the virtual path must fire
	// a market top-up on this SAME tick right after the stale order is cancelled.
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85)
	sr.mu.Unlock()

	if len(fake.cancelOrderCalls) != 1 || fake.cancelOrderCalls[0].OrderId != "old-x-1" {
		t.Fatalf("cancelOrderCalls = %+v, want one call cancelling old-x-1", fake.cancelOrderCalls)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 (immediate virtual top-up after cancelling the stale order)", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.OrderType != "Market" || got.TriggerPrice != "" {
		t.Errorf("order = %+v, want a plain market order (no TriggerPrice) — not another conditional order", got)
	}
	if sr.levels[0].ExchangeOrderID != "als-top-1" {
		t.Errorf("level.ExchangeOrderID = %q, want als-top-1 (the new market order's id, old-x-1 must be gone)", sr.levels[0].ExchangeOrderID)
	}

	var dbExchangeOrderID, dbStatus *string
	if err := pool.QueryRow(ctx, `SELECT status, exchange_order_id FROM strategy_levels WHERE id=$1`, levelID).Scan(&dbStatus, &dbExchangeOrderID); err != nil {
		t.Fatalf("read back level: %v", err)
	}
	if dbStatus == nil || *dbStatus != "placed" {
		t.Errorf("DB status = %v, want placed", dbStatus)
	}
}

// TestAlsPriceTick_ThresholdAddedMidCycle_AppendsLevelAndToppedUpImmediately is the
// regression for the second half of the 2026-10-23 report: adding a new threshold row
// to an ALS bot's settings while a cycle is already running used to have NO effect until
// the cycle ended, because restartAlsCycle never rebuilds sr.levels. alsPriceTick must
// now grow sr.levels to match a longer AlsThresholds itself and, if the new threshold's
// trigger is already behind price, top it up immediately (virtual/reactive mode) rather
// than waiting for a tick that will never come for an already-crossed level with no
// level row.
func TestAlsPriceTick_ThresholdAddedMidCycle_AppendsLevelAndToppedUpImmediately(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-newthresh-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-l2", OrderLinkId: "link-l2"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSNEWTHUSDT", 1, 10, 100) // main: long, 10 coins @ avg entry 100

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSNEWTHUSDT','short','als','active',true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, level1ID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	// Only L1's level row exists — as if the cycle started with a single threshold.
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID,
	).Scan(&level1ID); err != nil {
		t.Fatalf("create level 1: %v", err)
	}

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSNEWTHUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true,
			// L1 (-10%, trigger=90) is unchanged and NOT yet crossed. L2 (-3%, trigger=97)
			// was just appended via settings and has no level row yet — tick price (95) has
			// already passed it.
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30},
				{TriggerType: 1, TriggerPct: -3, TargetHedgePct: 60},
			},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: level1ID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()

	if len(sr.levels) != 2 {
		t.Fatalf("len(sr.levels) = %d, want 2 (L2's row appended)", len(sr.levels))
	}
	if sr.levels[1].LevelIdx != 2 || sr.levels[1].ID == "" {
		t.Fatalf("sr.levels[1] = %+v, want a real L2 row", sr.levels[1])
	}

	var level2Status string
	if err := pool.QueryRow(ctx, `SELECT status FROM strategy_levels WHERE strategy_id=$1 AND level_idx=2`, stratID).Scan(&level2Status); err != nil {
		t.Fatalf("L2 row not found in DB: %v", err)
	}

	// L1 must stay untouched (not crossed) — only L2 fires.
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 (only the newly-appended, already-crossed L2)", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.OrderType != "Market" || got.TriggerPrice != "" {
		t.Errorf("order = %+v, want a plain market order, not a conditional one", got)
	}
	if got.Side != "Sell" {
		t.Errorf("Side = %q, want Sell", got.Side)
	}
	// mainUsdt = effectiveDeposit(95) = 10*95 = 950. target 60% of 950 = 570 USDT,
	// at price 95 → 570/95 = 6.0 exactly.
	if got.Qty != "6.000" {
		t.Errorf("Qty = %q, want 6.000 (570 USDT / 95)", got.Qty)
	}
	if sr.levels[0].Status != LevelPending {
		t.Errorf("L1 status = %v, want still Pending (its own threshold wasn't crossed)", sr.levels[0].Status)
	}
}

// TestAlsPriceTick_ExchangeMode_ThresholdAddedMidCycleAlreadyCrossed_UsesMarketNotConditional
// pins the exchange-mode counterpart: a resting conditional order can't be made to fire
// "now" by flipping its TriggerDirection — that only changes what it waits for (price
// coming BACK to that level), which would be a silently wrong order. So a threshold that
// is already crossed by the time its level is first created (same mid-cycle-add scenario)
// must take the immediate-market-order path even when AlsExchangeOrders is true, exactly
// like virtual mode — never a conditional order.
func TestAlsPriceTick_ExchangeMode_ThresholdAddedMidCycleAlreadyCrossed_UsesMarketNotConditional(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xnewthresh-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-l1", OrderLinkId: "link-l1"}, nil)   // L1: still-pending conditional order
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-l2x", OrderLinkId: "link-l2x"}, nil) // L2: already-crossed market order

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSXNEWTHUSDT", 1, 10, 100)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, als_exchange_orders)
		VALUES ($1,$2,'ALSXNEWTHUSDT','short','als','active',true,true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, level1ID string
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID,
	).Scan(&level1ID); err != nil {
		t.Fatalf("create level 1: %v", err)
	}

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSXNEWTHUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: true,
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}, // L1: trigger=90, not crossed
				{TriggerType: 1, TriggerPct: -3, TargetHedgePct: 60},  // L2: trigger=97, already crossed @95
			},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: level1ID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()

	// L1 (not yet crossed) correctly still gets a resting conditional order — exchange
	// mode is otherwise unaffected. L2 (just added, already crossed) must NOT.
	if len(fake.placeOrderCalls) != 2 {
		t.Fatalf("placeOrderCalls = %d, want 2 (L1's conditional order + L2's market order)", len(fake.placeOrderCalls))
	}
	l1Order := fake.placeOrderCalls[0]
	if l1Order.OrderFilter != "StopOrder" || l1Order.TriggerPrice == "" {
		t.Errorf("L1 order = %+v, want a resting conditional order (unaffected — its own threshold wasn't crossed)", l1Order)
	}
	l2Order := fake.placeOrderCalls[1]
	if l2Order.OrderFilter == "StopOrder" || l2Order.TriggerPrice != "" {
		t.Errorf("L2 order = %+v, want a plain MARKET order (no OrderFilter/TriggerPrice) — a conditional order here "+
			"would need to wait for price to come back UP to 97, not react to the fact it already fell past it", l2Order)
	}
	if l2Order.OrderType != "Market" {
		t.Errorf("L2 OrderType = %q, want Market", l2Order.OrderType)
	}
	// ExchangeLinkID's prefix tells which path placed it: "-als-N-" is alsPlaceTopUp's
	// plain market order, "-alsx-N-" is alsSyncExchangeLevel's resting conditional order.
	if len(sr.levels) != 2 || !strings.Contains(sr.levels[1].ExchangeLinkID, "-als-1-2") {
		t.Errorf("L2 = %+v, want ExchangeLinkID from the market-order path (-als-1-2-), not the conditional one (-alsx-)", sr.levels[1])
	}
	if !strings.Contains(sr.levels[0].ExchangeLinkID, "-alsx-1-1") {
		t.Errorf("L1 = %+v, want ExchangeLinkID from the conditional-order path (-alsx-1-1-), its own resting order unaffected", sr.levels[0])
	}
}
