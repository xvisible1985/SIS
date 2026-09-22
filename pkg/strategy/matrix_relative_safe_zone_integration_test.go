//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestMatrixRelativeExpand_SafeZoneBlocksImmediateReopen is the end-to-end regression for
// the incident found live 2026-09-21/22 (Gonchar 2.0 hedge, poligonorigin33 account,
// COTIUSDT): with safe_zone_pct configured but relative_slots=true, matrixAfterSLClose
// skipped the absolute-mode safe zone entirely, letting a just-closed slot's config index
// reopen (a brand new strategy_levels row) the instant nextConfigIndex recounted open
// levels — L(4) reopened and stopped out 8 times in under 5 hours. Proves the wiring in
// matrixRelativeExpand actually blocks the DB insert while price is still inside the safe
// zone, and allows it once price has recovered past the threshold.
func TestMatrixRelativeExpand_SafeZoneBlocksImmediateReopen(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"relsz-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	matrixLevelsJSON := `[
		{"direction":"below","price_step_pct":-2,"size_pct":10},
		{"direction":"above","price_step_pct":2,"size_pct":10},
		{"direction":"above","price_step_pct":2,"size_pct":10}
	]`
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode, relative_slots, safe_zone_pct, matrix_levels)
		 VALUES ($1,$2,'RELSZUSDT','short','matrix','active',true,true,1.5,$3::jsonb) RETURNING id`,
		ownerID, accID, matrixLevelsJSON).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	one := 1
	two := 2
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: stratID, AccountID: accID, OwnerID: ownerID, Symbol: "RELSZUSDT",
			Category: "linear", Direction: DirectionShort, HedgeMode: true,
			RelativeSlots: true, SafeZonePct: 1.5,
			MatrixLevels: []MatrixLevel{
				{Direction: "below", PriceStepPct: -2, SizePct: 10},
				{Direction: "above", PriceStepPct: 2, SizePct: 10},
				{Direction: "above", PriceStepPct: 2, SizePct: 10},
			},
		},
		runner: ar,
		cycle:  &Cycle{ID: cycleID, StartPrice: 100.0},
		levels: []GridLevel{
			{Slot: &one, Status: LevelFilled, FilledPrice: 98.0},               // above-side idx1, still open
			{Slot: &two, Status: LevelSLClosed, SLPrice: 96.04, FilledPrice: 96.04}, // above-side idx2, just stopped out
		},
	}

	countLevels := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategy_levels WHERE cycle_id=$1`, cycleID).Scan(&n); err != nil {
			t.Fatalf("count levels: %v", err)
		}
		return n
	}

	// nextSlotPrice bases idx2's target on idx1's fill (98.0), stepped -2% for short → 96.04.
	// matrixSlotReached is satisfied at currentPrice<=96.04, e.g. 95.0. But the safe zone
	// (threshold ~94.60, from the SL trigger 96.04) has NOT recovered yet at 95.0 — must block.
	before := countLevels()
	sr.matrixRelativeExpand(ctx, "above", 95.0)
	if got := countLevels(); got != before {
		t.Errorf("strategy_levels count = %d, want unchanged %d — safe zone must block the reopen while price hasn't recovered", got, before)
	}

	// Price now at 94.0, past the ~94.60 threshold — the reopen must be allowed.
	sr.matrixRelativeExpand(ctx, "above", 94.0)
	if got := countLevels(); got != before+1 {
		t.Errorf("strategy_levels count = %d, want %d — a new level must be inserted once price clears the safe zone", got, before+1)
	}
}
