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

// TestMatrixRepairCandidates_FindsOneSidedSymbols: a symbol with exactly one direction
// active/finishing and the other direction missing entirely (no row at all) or explicitly
// 'stopped' is a repair candidate, paired with the missing direction. A symbol whose other
// direction is 'paused' (user-initiated stop) must NOT be a candidate — directionHasLiveStrategy
// treats 'paused' as live, and repair must respect that same "don't touch it" boundary.
// A symbol with BOTH directions active (a complete pair) or NEITHER direction present is
// also not a candidate (nothing to repair either way).
func TestMatrixRepairCandidates_FindsOneSidedSymbols(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repaircand")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'repairbot',$2,'active','{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	insertStrat := func(symbol, dir, status string) {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
			 VALUES ($1,$2,$3,$4,$5,'matrix',$6)`,
			userID, accID, botID, symbol, dir, status); err != nil {
			t.Fatalf("insert strategy %s/%s/%s: %v", symbol, dir, status, err)
		}
	}

	// STOPUSDT: long active, short stopped -> repair candidate, missing "short".
	insertStrat("STOPUSDT", "long", "active")
	insertStrat("STOPUSDT", "short", "stopped")

	// NOROWUSDT: short active, long has no row at all -> repair candidate, missing "long".
	insertStrat("NOROWUSDT", "short", "active")

	// PAUSEDUSDT: long active, short paused -> NOT a candidate (must not touch).
	insertStrat("PAUSEDUSDT", "long", "active")
	insertStrat("PAUSEDUSDT", "short", "paused")

	// FULLUSDT: both active -> NOT a candidate (already a complete pair).
	insertStrat("FULLUSDT", "long", "active")
	insertStrat("FULLUSDT", "short", "active")

	// FINISHUSDT: long finishing (not active), short missing entirely -> repair candidate,
	// missing "short". Confirms 'finishing' counts as live on the present side, same as 'active'.
	insertStrat("FINISHUSDT", "long", "finishing")

	got := s.matrixRepairCandidates(ctx, botID)

	want := map[string]string{"STOPUSDT": "short", "NOROWUSDT": "long", "FINISHUSDT": "short"}
	if len(got) != len(want) {
		t.Fatalf("matrixRepairCandidates() = %v, want %v", got, want)
	}
	for sym, wantDir := range want {
		if got[sym] != wantDir {
			t.Errorf("matrixRepairCandidates()[%s] = %q, want %q", sym, got[sym], wantDir)
		}
	}
	if _, ok := got["PAUSEDUSDT"]; ok {
		t.Errorf("matrixRepairCandidates() included PAUSEDUSDT — its other leg is paused, must not be touched")
	}
	if _, ok := got["FULLUSDT"]; ok {
		t.Errorf("matrixRepairCandidates() included FULLUSDT — both legs already active, nothing to repair")
	}
}

// TestEnsureMatrixStrategies_RepairsOneSidedPairBeforeNewOnes: a symbol already missing
// one leg (repair candidate) must get that leg reopened even with zero whitelist symbols
// to scan for brand-new pairs, and even though it has no confirming activation signal —
// repair bypasses the activation gate entirely (mirrors the existing philosophy for
// adopting an orphan exchange position: leaving a half-open pair unbalanced is worse than
// opening the missing leg without waiting for a fresh signal).
func TestEnsureMatrixStrategies_RepairsOneSidedPairBeforeNewOnes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repairwire")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, max_strategies, max_long_strategies, max_short_strategies, strategy_config)
		 VALUES ($1,'repairwirebot',$2,'active',10,10,10,'{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	// ensureMatrixStrategies takes cfg directly as a parameter below (same pattern as the
	// existing TestEnsureMatrixStrategies_RespectsStrategyLimits) — it does not re-read
	// strategy_config from this row, so the bot's own JSON only needs bot_kind for the
	// dispatch-by-kind logic elsewhere in the engine; activation_signals lives in the Go
	// cfg value constructed below instead.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'REPAIRWIREUSDT','long','matrix','active')`,
		userID, accID, botID); err != nil {
		t.Fatalf("insert existing leg: %v", err)
	}

	cfg := botCfgJSON{
		StrategyType: "matrix",
		ActivationSignals: []struct {
			Name   string                 `json:"name"`
			Params map[string]interface{} `json:"params"`
		}{
			{Name: "price-change", Params: map[string]interface{}{"tf": "1D", "mode": "counter", "periodHours": 24.0, "thresholdPct": 20.0}},
		},
	}

	// Empty whitelist (no new-pair candidates to scan) — if the missing leg opens, it can
	// only be the repair pass that did it.
	s.ensureMatrixStrategies(ctx, botID, userID, accID, []string{}, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})

	var shortCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='REPAIRWIREUSDT' AND direction='short' AND status IN ('active','finishing')`,
		botID,
	).Scan(&shortCount); err != nil {
		t.Fatalf("count short leg: %v", err)
	}
	if shortCount != 1 {
		t.Errorf("REPAIRWIREUSDT short leg count = %d, want 1 (repair pass must open the missing leg, bypassing the activation gate, even with an empty whitelist)", shortCount)
	}
}

// TestEnsureMatrixStrategies_RepairTakesPrioritySlotOverNewPair: with capacity for only
// ONE more long strategy, a repair candidate missing "long" must win that slot over a
// brand-new candidate symbol also wanting to open long — repair runs first.
func TestEnsureMatrixStrategies_RepairTakesPrioritySlotOverNewPair(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "repairprio")
	accID := createTestAccount(t, s, userID)

	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, max_strategies, max_long_strategies, max_short_strategies, strategy_config)
		 VALUES ($1,'repairpriobot',$2,'active',3,1,2,'{"bot_kind":"matrix"}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	// PRIOUSDT already has a short leg -> repair candidate, wants "long".
	// max_long_strategies=1, so exactly one symbol's long leg can open this tick.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'PRIOUSDT','short','matrix','active')`,
		userID, accID, botID); err != nil {
		t.Fatalf("insert existing leg: %v", err)
	}

	// FRESHUSDT is a brand-new candidate (no existing rows) competing for the same long slot.
	whitelist := []string{"FRESHUSDT"}
	cfg := botCfgJSON{StrategyType: "matrix"} // no ActivationSignals -> new-pair path always passes activation

	s.ensureMatrixStrategies(ctx, botID, userID, accID, whitelist, nil, cfg, trader.Credentials{}, map[string]map[string]hedgePosInfo{}, map[string]bool{})

	var prioLong, freshLong int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='PRIOUSDT' AND direction='long' AND status IN ('active','finishing')`,
		botID,
	).Scan(&prioLong); err != nil {
		t.Fatalf("count PRIOUSDT long: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='FRESHUSDT' AND direction='long' AND status IN ('active','finishing')`,
		botID,
	).Scan(&freshLong); err != nil {
		t.Fatalf("count FRESHUSDT long: %v", err)
	}
	if prioLong != 1 {
		t.Errorf("PRIOUSDT long = %d, want 1 (repair candidate must win the only available long slot)", prioLong)
	}
	if freshLong != 0 {
		t.Errorf("FRESHUSDT long = %d, want 0 (new-pair candidate must NOT take the slot repair needed)", freshLong)
	}
}
