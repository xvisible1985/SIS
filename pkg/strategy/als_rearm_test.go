//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

func TestAlsPriceTick_RearmsStoppedLevelAfterSafeZone(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-rearm-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
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
	setWSPosition(ar, "ALSREARMUSDT", 1, 10, 100) // main avg entry 100

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode)
		VALUES ($1,$2,'ALSREARMUSDT','short','als','active',true,true) RETURNING id`,
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
			ID: stratID, Symbol: "ALSREARMUSDT", Category: "linear", Direction: DirectionShort,
			HedgeMode: true, SizeAsMain: true, SafeZonePct: 2,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelSLClosed, SLPrice: 90, FilledPrice: 90, Qty: "1"}},
		alsSafeAnchor: 90, // its stop fired at 90
	}

	countRows := func() int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM strategy_levels WHERE cycle_id=$1`, cycleID).Scan(&n) //nolint:errcheck
		return n
	}

	// Price 89: inside the Safe Zone band [88.2, 90] — hedge must stay out.
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 89)
	sr.mu.Unlock()
	if len(fake.placeOrderCalls) != 0 || len(sr.levels) != 1 || sr.alsSafeAnchor != 90 {
		t.Fatalf("inside Safe Zone: places=%d levels=%d anchor=%v, want 0/1/90", len(fake.placeOrderCalls), len(sr.levels), sr.alsSafeAnchor)
	}

	// Price 88: out of the band (below 88.2) and below the L1 trigger (100*0.9 = 90) — the stopped level
	// is re-armed (history row kept, fresh pending row added) and the hedge topped back up.
	sr.mu.Lock()
	sr.alsPriceTick(ctx, 88)
	sr.mu.Unlock()
	if len(sr.levels) != 2 || sr.levels[0].Status != LevelSLClosed {
		t.Fatalf("after Safe Zone: levels = %+v, want [sl_closed, re-armed]", sr.levels)
	}
	if sr.alsSafeAnchor != 0 {
		t.Errorf("alsSafeAnchor = %v, want cleared", sr.alsSafeAnchor)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1 top-up after Safe Zone", len(fake.placeOrderCalls))
	}
	if got := countRows(); got != 2 {
		t.Errorf("strategy_levels rows = %d, want 2 (stopped row kept + re-armed row)", got)
	}
	if sr.levels[1].Status == LevelSLClosed || sr.levels[1].LevelIdx != 1 {
		t.Errorf("re-armed row = %+v, want level 1 no longer sl_closed", sr.levels[1])
	}
}
