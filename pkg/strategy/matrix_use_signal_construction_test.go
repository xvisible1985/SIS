//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestMatrixReplaceSlots_ThreadsUseSignal is the matrix equivalent of
// TestRepriceRemainingFromFills_UseSignalStep_ForcesVirtual (grid): proves that a
// MatrixLevel.UseSignal=true config actually reaches the constructed GridLevel — both the
// in-memory struct AND the persisted strategy_levels row — for one of the matrix
// level-construction sites (matrixReplaceSlots, the Safe-Zone re-entry / "rebuild all
// slots" path used after a Safe Zone clears). Before Task 7, matrixLevelConfig's 5th
// return value (useSignal, added in Task 6) was resolved but never read at any
// construction site, so every matrix GridLevel silently kept UseSignal's zero value
// (false) regardless of its config.
//
// L(1) ("above", slot 1) is used because for a LONG strategy a positive slot is ALWAYS
// virtual (matrixIsVirtual) — placeMatrixLevel returns immediately once it sees that,
// before ever reaching the risk gate or the exchange, so this test needs no fakeExchange
// wiring and has no order-placement side effects to stub out.
func TestMatrixReplaceSlots_ThreadsUseSignal(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, level0ID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"mrsusig-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'MRSUSIGUSDT','long','matrix','active') RETURNING id`,
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

	// L(0): already filled — establishes the position and keeps matrixReplaceSlots from
	// touching slot 0 (activeSlots[0]=true), so the test only has to reason about slot 1.
	slotZero := 0
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price, slot)
		 VALUES ($1,$2,1,'Buy',0,500,'5','filled',100,$3) RETURNING id`,
		stratID, cycleID, slotZero,
	).Scan(&level0ID); err != nil {
		t.Fatalf("create level 0: %v", err)
	}

	sr := &StrategyRunner{
		runner: &AccountRunner{pool: pool},
		strategy: Strategy{
			ID:           stratID,
			Direction:    DirectionLong,
			GridSizeUSDT: 1000,
			MatrixLevels: []MatrixLevel{
				// slot 1 ("above" idx 0) — signal-gated accumulation level.
				{Direction: "above", PriceStepPct: 5, SizePct: 50, UseSignal: true},
			},
		},
		cycle:  &Cycle{ID: cycleID, StrategyID: stratID, CycleNum: 1, StartPrice: 100},
		instr:  trader.InstrumentInfo{QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: level0ID, LevelIdx: 1, Side: "Buy", Status: LevelFilled, FilledPrice: 100, Slot: &slotZero}},
	}

	sr.matrixReplaceSlots(ctx, 100.0)

	if len(sr.levels) != 2 {
		t.Fatalf("len(sr.levels) = %d, want 2 (L0 filled + newly created L1)", len(sr.levels))
	}
	l1 := sr.levels[1]
	if l1.Slot == nil || *l1.Slot != 1 {
		t.Fatalf("sr.levels[1].Slot = %v, want pointer to 1", l1.Slot)
	}
	if !l1.UseSignal {
		t.Error("in-memory GridLevel.UseSignal = false, want true — matrixReplaceSlots must thread the slot's config through")
	}

	var dbUseSignal bool
	if err := pool.QueryRow(ctx,
		`SELECT use_signal FROM strategy_levels WHERE id=$1`, l1.ID,
	).Scan(&dbUseSignal); err != nil {
		t.Fatalf("query level 1: %v", err)
	}
	if !dbUseSignal {
		t.Error("DB strategy_levels.use_signal = false, want true — must be persisted, not just kept in memory")
	}
}
