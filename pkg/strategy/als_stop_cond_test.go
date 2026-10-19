//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet confirms the ongoing
// stop-cond-replace wiring: a filled level with NO initial stop (InitialStopBEPct unset)
// and no SLOrderID yet gets one placed once price has moved StopCondPct% in the hedge's
// favor from its own FilledPrice.
func TestAlsPriceTick_PlacesStopCondReplaceOnceConditionMet(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-stopcond-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "stopcond-sl-1", OrderLinkId: "stopcond-link-1"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSSTOPCONDUSDT", alsMainPositionIdx(DirectionShort), 10, 100)

	var stratID string
	pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode)
		VALUES ($1,$2,'ALSSTOPCONDUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID).Scan(&cycleID)
	// Filled at 90 (short hedge), no stop yet — StopCondPct=5 means the condition fires
	// once price has fallen 5% below 90, i.e. reached 85.5. Tick at 85 to cross it.
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price) VALUES ($1,$2,1,'Sell',90,0,'1','filled',90) RETURNING id`,
		stratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, AccountID: accID, Symbol: "ALSSTOPCONDUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true,
			AlsStopCondPct: 5, AlsStopReplacePct: 1,
		},
		cycle: &Cycle{ID: cycleID, CycleNum: 1},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelFilled, Qty: "1", FilledPrice: 90},
		},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85)
	sr.mu.Unlock()

	if sr.levels[0].SLOrderID != "stopcond-sl-1" {
		t.Errorf("level SLOrderID = %q, want stopcond-sl-1", sr.levels[0].SLOrderID)
	}
}
