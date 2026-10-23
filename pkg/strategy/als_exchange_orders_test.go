//go:build integration

package strategy

import (
	"context"
	"errors"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_ExchangeMode_PlacesConditionalOrderBeforeThresholdCrossed is the core
// regression for the 2026-10-23 feature: with AlsExchangeOrders=true, alsPriceTick must
// place a real conditional (stop-market) order at the computed threshold price EVEN WHEN
// the live tick price has NOT yet reached it — the whole point is to let the exchange
// trigger it exactly at that price (avoiding the reactive path's "fill far from, and out
// of sequence relative to, the intended targets during a fast move" failure mode — see the
// BIRBUSDT incident this feature fixes).
func TestAlsPriceTick_ExchangeMode_PlacesConditionalOrderBeforeThresholdCrossed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xorder-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-1", OrderLinkId: "link-x-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	// Main is LONG @ avg entry 100 (short hedge ⟹ main long). mainUsdt depends on the tick
	// price (effectiveDeposit), not the entry — 10 coins × 95 = 950.
	setWSPosition(ar, "ALSXORDUSDT", 1, 10, 100)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSXORDUSDT','short','als','active',true,true) RETURNING id`,
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
			ID: stratID, Symbol: "ALSXORDUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	// Trigger = 100*(1-10/100) = 90 (short hedge, base=avgEntry). Tick at 95 — price has
	// NOT reached 90 yet. Reactive mode (AlsExchangeOrders=false) would skip entirely here
	// (see TestAlsPriceTick_PlacesTopUpWhenThresholdCrossed's tick=85, which DOES cross).
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 (conditional order placed ahead of the threshold)", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	if got.OrderFilter != "StopOrder" {
		t.Errorf("OrderFilter = %q, want StopOrder", got.OrderFilter)
	}
	if got.TriggerPrice != "90.00" {
		t.Errorf("TriggerPrice = %q, want 90.00", got.TriggerPrice)
	}
	if got.TriggerDirection != 2 {
		t.Errorf("TriggerDirection = %d, want 2 (Falling — short hedge, adverse-for-main is price falling)", got.TriggerDirection)
	}
	if got.Side != "Sell" {
		t.Errorf("Side = %q, want Sell", got.Side)
	}
	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID != "als-x-1" {
		t.Errorf("level = %+v, want Status=placed ExchangeOrderID=als-x-1", sr.levels[0])
	}
}

// TestAlsPriceTick_ExchangeMode_RepricesWhenMainAvgEntryMoves: trigger_type=1 bases the
// threshold on the main position's LIVE avg entry, which moves as main DCAs. A
// conditional order placed against the old avg entry becomes wrong once that happens and
// must be cancelled and replaced at the new price — the reactive path already handles
// this implicitly (it recomputes triggerPrice fresh every tick before even checking
// crossed); exchange mode must actively re-sync the resting order to match.
func TestAlsPriceTick_ExchangeMode_RepricesWhenMainAvgEntryMoves(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xreprice-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-1", OrderLinkId: "link-x-1"}, nil)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-2", OrderLinkId: "link-x-2"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSXREPUSDT", 1, 10, 100) // main avg entry 100

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSXREPUSDT','short','als','active',true,true) RETURNING id`,
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
			ID: stratID, Symbol: "ALSXREPUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95) // trigger = 100*0.9 = 90
	sr.mu.Unlock()
	if len(fake.placeOrderCalls) != 1 || fake.placeOrderCalls[0].TriggerPrice != "90.00" {
		t.Fatalf("after 1st tick: placeOrderCalls = %+v, want one call @ 90.00", fake.placeOrderCalls)
	}

	// Main averages down: avg entry moves from 100 to 80 — trigger must move to 80*0.9=72.
	setWSPosition(ar, "ALSXREPUSDT", 1, 10, 80)
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()

	if len(fake.cancelOrderCalls) != 1 || fake.cancelOrderCalls[0].OrderId != "als-x-1" {
		t.Fatalf("cancelOrderCalls = %+v, want one call cancelling als-x-1", fake.cancelOrderCalls)
	}
	if len(fake.placeOrderCalls) != 2 {
		t.Fatalf("after 2nd tick: placeOrderCalls = %d, want 2 (repriced)", len(fake.placeOrderCalls))
	}
	if got := fake.placeOrderCalls[1].TriggerPrice; got != "72.00" {
		t.Errorf("repriced TriggerPrice = %q, want 72.00", got)
	}
	// Bybit rejects a reused orderLinkId even after cancel (110072) — the reprice must not reuse it.
	if a, b := fake.placeOrderCalls[0].OrderLinkId, fake.placeOrderCalls[1].OrderLinkId; a == b || len(b) > 36 {
		t.Errorf("repriced OrderLinkId = %q (first %q): must differ and be <=36 chars", b, a)
	}
	if sr.levels[0].ExchangeOrderID != "als-x-2" {
		t.Errorf("level ExchangeOrderID = %q, want als-x-2 (the repriced order)", sr.levels[0].ExchangeOrderID)
	}
}

// TestAlsPriceTick_ExchangeMode_SkipsReplacementWhenOldOrderAlreadyFilled: if the cancel
// of a stale order fails because it had ALREADY filled on the exchange moments earlier,
// a replacement must NOT be placed — that fill's own WS event is already in flight and
// will carry the level through handleAlsLevelFill; placing a second order here would
// double this level's real position.
func TestAlsPriceTick_ExchangeMode_SkipsReplacementWhenOldOrderAlreadyFilled(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xfilled-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-1", OrderLinkId: "link-x-1"}, nil)
	fake.cancelOrderQ.push(struct{}{}, errors.New("order not exists or too late to cancel")) // cancel fails: already filled
	fake.fetchOrderByIdQ.push(orderLookupResp{order: trader.Order{OrderStatus: "Filled"}}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSXFILUSDT", 1, 10, 100)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSXFILUSDT','short','als','active',true,true) RETURNING id`,
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
			ID: stratID, Symbol: "ALSXFILUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("after 1st tick: placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}

	// Main moves, forcing a reprice attempt — but the resting order already filled.
	setWSPosition(ar, "ALSXFILUSDT", 1, 10, 80)
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Errorf("placeOrderCalls = %d, want still 1 — no replacement once the old order was found already filled", len(fake.placeOrderCalls))
	}
	if sr.levels[0].ExchangeOrderID != "" {
		t.Errorf("level.ExchangeOrderID = %q, want cleared (the fill's own WS event owns this level now)", sr.levels[0].ExchangeOrderID)
	}
}

func TestAlsPriceTick_ExchangeMode_DoesNotCountOwnRestingOrderAgainstUnsafeHedgeCap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-xselfcap-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-1", OrderLinkId: "link-x-1"}, nil)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-x-2", OrderLinkId: "link-x-2"}, nil)

	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	setWSPosition(ar, "ALSXSELFUSDT", 1, 10, 100) // main avg entry 100

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSXSELFUSDT','short','als','active',true,true) RETURNING id`,
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
			ID: stratID, Symbol: "ALSXSELFUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, AlsExchangeOrders: true, UnsafeHedgeMaxPct: 20,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95) // trigger = 100*0.9 = 90
	sr.mu.Unlock()
	if len(fake.placeOrderCalls) != 1 || fake.placeOrderCalls[0].TriggerPrice != "90.00" {
		t.Fatalf("after 1st tick: placeOrderCalls = %+v, want one call @ 90.00", fake.placeOrderCalls)
	}

	first := fake.placeOrderCalls[0].Qty

	// Nothing about the market, main position or settings changed — the resting order must be
	// left alone. Regression: the level's own unfilled resting order used to be counted as
	// "open unprotected" volume against UnsafeHedgeMaxPct, so every tick shrank/grew the
	// computed size and cancel+replaced the order forever (qty flapping 12 <-> 228 live).
	for i := 0; i < 3; i++ {
		sr.mu.Lock()
		sr.alsPriceTick(ctx, 95)
		sr.mu.Unlock()
	}
	if len(fake.cancelOrderCalls) != 0 || len(fake.placeOrderCalls) != 1 {
		t.Fatalf("stable inputs: cancels=%d places=%d, want 0 and 1 (first qty %s)",
			len(fake.cancelOrderCalls), len(fake.placeOrderCalls), first)
	}
}

