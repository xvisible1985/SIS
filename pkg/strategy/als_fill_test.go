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
