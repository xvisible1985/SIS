//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestRepriceRemainingFromFills_UseSignalStep_ForcesVirtual is the regression for a
// UseSignal=true grid step that would otherwise be created as a blind resting order
// (OrderType != "virtual", PriceMovePct <= 0 — neither of which alone forces virtual) — a
// signal-gated level must always be software-monitored so the price-tick loop
// (gridVirtualPriceTick) gets a chance to check the signal before the level ever reaches the
// exchange. This exercises the "reprice from fills" dynamic level-creation site in
// repriceRemainingFromFills (pkg/strategy/cycle.go, forceVirtual around line 4393).
func TestRepriceRemainingFromFills_UseSignalStep_ForcesVirtual(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, level1ID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"rrffusfv-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'RRFFUSFVUSDT','long','grid','active') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price) VALUES ($1,1,100) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_levels WHERE cycle_id=$1", cycleID) })

	// L1: already filled, establishes basePrice for repricing/creating the next level.
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		 VALUES ($1,$2,1,'Buy',0,10,'0.1','filled',100) RETURNING id`,
		stratID, cycleID).Scan(&level1ID); err != nil {
		t.Fatalf("create level 1: %v", err)
	}

	sr := &StrategyRunner{
		runner: &AccountRunner{pool: pool},
		strategy: Strategy{
			ID:           stratID,
			Direction:    DirectionLong,
			GridSizeUSDT: 20,
			Steps: []GridStep{
				{PriceMovePct: 0, SizePct: 50}, // L1 — market entry, already filled
				{
					// Neither OrderType=="virtual" nor PriceMovePct>0 — without the
					// UseSignal fix this step would be a blind resting order.
					PriceMovePct: -5,
					SizePct:      50,
					OrderType:    "limit",
					UseSignal:    true,
				},
			},
		},
		cycle: &Cycle{ID: cycleID},
		instr: trader.InstrumentInfo{QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{
			{ID: level1ID, LevelIdx: 1, Side: "Buy", Status: LevelFilled, FilledPrice: 100},
		},
	}

	sr.repriceRemainingFromFills(ctx)

	if len(sr.levels) != 2 {
		t.Fatalf("len(sr.levels) = %d, want 2 (L1 filled + newly created L2)", len(sr.levels))
	}
	l2 := sr.levels[1]
	if l2.LevelIdx != 2 {
		t.Fatalf("sr.levels[1].LevelIdx = %d, want 2", l2.LevelIdx)
	}
	if !l2.ForceVirtual {
		t.Error("in-memory GridLevel.ForceVirtual = false, want true — UseSignal=true step must always be forced virtual")
	}
	if !l2.UseSignal {
		t.Error("in-memory GridLevel.UseSignal = false, want true — must carry the step's use_signal flag through")
	}

	var dbForceVirtual, dbUseSignal bool
	if err := pool.QueryRow(ctx,
		`SELECT force_virtual, use_signal FROM strategy_levels WHERE id=$1`, l2.ID,
	).Scan(&dbForceVirtual, &dbUseSignal); err != nil {
		t.Fatalf("query level 2: %v", err)
	}
	if !dbForceVirtual {
		t.Error("DB strategy_levels.force_virtual = false, want true")
	}
	if !dbUseSignal {
		t.Error("DB strategy_levels.use_signal = false, want true — must be persisted, not just kept in memory")
	}
}
