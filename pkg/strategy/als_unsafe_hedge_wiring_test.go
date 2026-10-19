//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_ClampsTopUpByUnsafeHedgePercent is the wiring regression test. Main
// is 10 coins @ avg entry 100; mainUsdt is effectiveDeposit(currentPrice) — sizeCoins ×
// the TICK price (90 here), i.e. 900, NOT sizeCoins × avg entry (which would be 1000).
// With UnsafeHedgeMaxPct=20, Vmax=20%*900=180. A threshold targeting 30% (which alone
// would want a 270 USDT top-up: 30%*900) must be clamped down to Vh=180.
func TestAlsPriceTick_ClampsTopUpByUnsafeHedgePercent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-unsafe-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-clamp-1", OrderLinkId: "link-clamp-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSUNSAFEUSDT", alsMainPositionIdx(DirectionShort), 10, 100)

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, unsafe_hedge_max_pct)
		VALUES ($1,$2,'ALSUNSAFEUSDT','short','als','active',true,true,20) RETURNING id`,
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
			ID: stratID, AccountID: accID, Symbol: "ALSUNSAFEUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, UnsafeHedgeMaxPct: 20,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 90) // trigger=90, exactly at it — crosses (Short: currentPrice<=trigger)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("placeOrderCalls = %d, want 1", len(fake.placeOrderCalls))
	}
	got := fake.placeOrderCalls[0]
	// Clamped top-up = Vh = 180 USDT / price 90 = 2.0 exactly (not the unclamped
	// 270/90=3.0).
	if got.Qty != "2.000" {
		t.Errorf("Qty = %q, want 2.000 (180 USDT clamp / 90, not the unclamped 270/90=3.000)", got.Qty)
	}
}
