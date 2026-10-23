//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured confirms a filled level gets its
// InitialStopBEPct stop placed immediately (the "condé trailing" variant) rather than
// staying unprotected until StopCondPct fires later.
func TestHandleAlsLevelFill_PlacesInitialStopWhenConfigured(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-fill-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-stop-1", OrderLinkId: "link-stop-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	initialStopBE := 2.0
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode, initial_stop_be_pct)
		VALUES ($1,$2,'ALSFILLUSDT','short','als','active',true,$3) RETURNING id`,
		ownerID, accID, initialStopBE,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',110,0,'2.679','placed') RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	bePct := 2.0
	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSFILLUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, InitialStopBEPct: &bePct,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPlaced, Qty: "2.679"},
		},
	}

	sr.mu.Lock()
	sr.handleAlsLevelFill(ctx, levelID, 112.0)
	sr.mu.Unlock()

	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %q, want filled", sr.levels[0].Status)
	}
	if sr.levels[0].FilledPrice != 112.0 {
		t.Errorf("FilledPrice = %v, want 112.0", sr.levels[0].FilledPrice)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 (initial BE stop)", len(fake.placeOrderCalls))
	}
	stopCall := fake.placeOrderCalls[0]
	if stopCall.Side != "Buy" {
		t.Errorf("stop Side = %q, want Buy (closes a Sell/short hedge)", stopCall.Side)
	}
	if stopCall.OrderFilter != "StopOrder" {
		t.Errorf("stop OrderFilter = %q, want StopOrder", stopCall.OrderFilter)
	}
	// Short hedge filled at 112, InitialStopBEPct=2.0 → matrixStopReplaceTrigger(short, 112, 2.0) = 112*(1-2/100) = 109.76.
	if stopCall.TriggerPrice != "109.76" {
		t.Errorf("stop TriggerPrice = %q, want 109.76", stopCall.TriggerPrice)
	}
	// 109.76 < filledPrice 112 → the stop must fire on a FALL to/below it (triggerDirection=2),
	// not a rise (=1) — a hardcoded by-direction trigDir gets exactly this backwards for a
	// short hedge's breakeven stop (same bug class fixed live for Binance, 2026-10-18,
	// NEARUSDT grid DCA — see binanceConditionalOrderType's doc comment).
	if stopCall.TriggerDirection != 2 {
		t.Errorf("stop TriggerDirection = %d, want 2 (falls to/below — price moved favorably for the short hedge)", stopCall.TriggerDirection)
	}
}

// TestAlsPlaceTopUp_SetsQtyInMemory is a regression for the 2026-10-22 live incident:
// alsPlaceTopUp wrote the computed qty to strategy_levels.qty in the DB but never set it on
// the in-memory GridLevel (l.Qty stayed ""). avgEntry() parses l.Qty with
// strconv.ParseFloat(l.Qty, 64) and ignores the error, so an empty string silently became 0
// — meaning every ALS hedge fill looked like qty=0 to handlePartialPositionChange, which
// then treated the just-opened real position as an orphan "ghost" and market-closed it
// immediately (BTRUSDT short, opened and force-closed within the same WS event, net
// -0.0667 USDT). This test drives the real sequence — place, then fill — and asserts
// avgEntry() sees the real qty, not 0.
func TestAlsPlaceTopUp_SetsQtyInMemory(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-topup-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-topup-1", OrderLinkId: "link-topup-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode)
		VALUES ($1,$2,'ALSTOPUPUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSTOPUPUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending},
		},
	}

	sr.mu.Lock()
	sr.alsPlaceTopUp(ctx, &sr.levels[0], 110.0, 20.0, 100.0) // 20 USDT @ 100 → qty = 0.2
	sr.mu.Unlock()

	if sr.levels[0].Qty == "" || sr.levels[0].Qty == "0" {
		t.Fatalf("after alsPlaceTopUp, l.Qty = %q, want the placed qty (non-empty, non-zero)", sr.levels[0].Qty)
	}

	sr.mu.Lock()
	sr.handleAlsLevelFill(ctx, levelID, 100.0)
	_, qty := sr.avgEntry()
	sr.mu.Unlock()

	if qty <= 0 {
		t.Errorf("avgEntry() qty = %v after a real fill, want > 0 — a filled ALS level must not look like an empty/ghost position", qty)
	}
}
