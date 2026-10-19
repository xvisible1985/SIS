//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestStartAlsCycle_SeedsOneLevelRowPerThreshold is the regression test for the core
// scaffolding this whole engine plan builds on: starting an ALS cycle must create one
// strategy_levels row per AlsThresholds entry, all pending, with no exchange calls yet
// (nothing fires until alsPriceTick sees a crossed threshold — Task 3).
func TestStartAlsCycle_SeedsOneLevelRowPerThreshold(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-start-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool

	var stratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, als_thresholds)
		VALUES ($1,$2,'ALSCOREUSDT','long','als','active',$3::jsonb) RETURNING id`,
		ownerID, accID,
		`[{"trigger_type":1,"trigger_pct":-10,"target_hedge_pct":30},{"trigger_type":1,"trigger_pct":-30,"target_hedge_pct":50}]`,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: stratID, Symbol: "ALSCOREUSDT", Category: "linear", Direction: DirectionLong,
			HedgeMode: true,
			AlsThresholds: []AlsThreshold{
				{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30},
				{TriggerType: 1, TriggerPct: -30, TargetHedgePct: 50},
			},
		},
	}

	if err := sr.startAlsCycle(ctx); err != nil {
		t.Fatalf("startAlsCycle: %v", err)
	}

	if sr.cycle == nil {
		t.Fatal("sr.cycle is nil after startAlsCycle")
	}
	if len(sr.levels) != 2 {
		t.Fatalf("sr.levels = %d, want 2", len(sr.levels))
	}
	for i, l := range sr.levels {
		if l.Status != LevelPending {
			t.Errorf("level %d status = %q, want pending", i, l.Status)
		}
		if l.LevelIdx != i+1 {
			t.Errorf("level %d LevelIdx = %d, want %d", i, l.LevelIdx, i+1)
		}
	}

	var dbCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM strategy_levels WHERE cycle_id=$1`, sr.cycle.ID).Scan(&dbCount); err != nil {
		t.Fatalf("count levels: %v", err)
	}
	if dbCount != 2 {
		t.Errorf("DB strategy_levels rows = %d, want 2", dbCount)
	}
}
