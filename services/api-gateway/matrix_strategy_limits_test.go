//go:build integration

package main

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestEnsureMatrixStrategies_RespectsStrategyLimits: with a whitelist larger than what
// the configured limits allow, ensureMatrixStrategies must stop creating new strategies
// once max_strategies/max_long_strategies/max_short_strategies are reached — not open
// one pair per whitelisted symbol unconditionally. Regression test for a live incident
// (2026-07-17): MatrixNova was configured for max 4 total (2 long, 2 short) but opened
// 10+ strategies before being stopped by hand, because these limits — stored on the
// bots table — were never read anywhere in the matrix engine.
func TestEnsureMatrixStrategies_RespectsStrategyLimits(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "matrixlimit")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, max_strategies, max_long_strategies, max_short_strategies, strategy_config)
		 VALUES ($1,'limitbot',$2,'active',4,2,2,'{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	whitelist := []string{"LIMA1USDT", "LIMA2USDT", "LIMA3USDT", "LIMA4USDT", "LIMA5USDT", "LIMA6USDT"}
	cfg := botCfgJSON{StrategyType: "matrix"}

	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})

	var total, long, short int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE direction='long'), count(*) FILTER (WHERE direction='short')
		 FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`, botID,
	).Scan(&total, &long, &short); err != nil {
		t.Fatalf("count strategies: %v", err)
	}
	if total != 4 {
		t.Errorf("total strategies = %d, want 4 (max_strategies=4 not enforced — whitelist of %d symbols could open up to %d)",
			total, len(whitelist), len(whitelist)*2)
	}
	if long != 2 {
		t.Errorf("long strategies = %d, want 2 (max_long_strategies=2)", long)
	}
	if short != 2 {
		t.Errorf("short strategies = %d, want 2 (max_short_strategies=2)", short)
	}
}
