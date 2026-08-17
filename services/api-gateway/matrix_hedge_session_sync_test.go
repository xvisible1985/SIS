//go:build integration

package main

import (
	"context"
	"testing"
)

// createMatrixPairFixture creates a bot with three strategy rows: an old (stopped) long
// leg, a new (active) long leg, and a short leg — reproducing the scenario where
// createBotStrategy refused to reuse the old long row (still had an open cycle) and
// created a fresh strategy_id for it instead.
func createMatrixPairFixture(t *testing.T, s *Server, userID, accountID, symbol string) (botID, oldLongID, newLongID, shortID string) {
	t.Helper()
	ctx := context.Background()
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name) VALUES ($1,$2) RETURNING id`,
		userID, "mhs-bot-"+symbol,
	).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })

	insertStrat := func(direction, status string) string {
		var id string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, bot_id, status)
			 VALUES ($1,$2,$3,$4,'matrix',$5,$6) RETURNING id`,
			userID, accountID, symbol, direction, botID, status,
		).Scan(&id); err != nil {
			t.Fatalf("create %s strategy: %v", direction, err)
		}
		t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", id) })
		return id
	}
	oldLongID = insertStrat("long", "stopped")
	newLongID = insertStrat("long", "active")
	shortID = insertStrat("short", "active")
	return botID, oldLongID, newLongID, shortID
}

// TestEnsureMatrixHedgeSession_MigratesMainStrategyIdOnLongLegReplacement is the
// regression for the "Накоплено матрикс" showing "—" bug: when the long leg gets a new
// strategy_id (old row stopped with a still-open cycle, so createBotStrategy couldn't
// reuse it), the existing hedge_sessions row must follow to the new id, not stay pinned
// to the superseded one — otherwise GetHedgeSession (looked up by the CURRENT main.id)
// finds no row, even though accumulated_pnl kept accumulating correctly via
// hedge_strategy_id (the short leg, which didn't change).
func TestEnsureMatrixHedgeSession_MigratesMainStrategyIdOnLongLegReplacement(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "mhs1")
	accID := createTestAccount(t, s, userID)
	botID, oldLongID, newLongID, shortID := createMatrixPairFixture(t, s, userID, accID, "MHS1USDT")
	ctx := context.Background()

	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, accumulated_pnl)
		 VALUES ($1,$2,$3,2.54) RETURNING id`,
		botID, oldLongID, shortID).Scan(&sessionID); err != nil {
		t.Fatalf("seed stale session: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	if err := ensureMatrixHedgeSession(ctx, s.pool, botID, newLongID, shortID); err != nil {
		t.Fatalf("ensureMatrixHedgeSession: %v", err)
	}

	var mainID string
	var accumulated float64
	if err := s.pool.QueryRow(ctx,
		`SELECT main_strategy_id, accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID,
	).Scan(&mainID, &accumulated); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if mainID != newLongID {
		t.Errorf("main_strategy_id = %s, want %s (must migrate to the new live long leg)", mainID, newLongID)
	}
	if accumulated != 2.54 {
		t.Errorf("accumulated_pnl = %v, want unchanged 2.54 (migration must not reset/duplicate it)", accumulated)
	}

	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM hedge_sessions WHERE hedge_strategy_id=$1 AND ended_at IS NULL`, shortID,
	).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 1 {
		t.Errorf("open session count for this hedge leg = %d, want 1 (must update in place, not insert a duplicate)", count)
	}

	// End-to-end: GetHedgeSession looked up by the NEW main.id must now find the row and
	// return its accumulated_pnl — this is what the frontend's "Накоплено матрикс" reads.
	got := getHedgeSessionPnl(t, s, userID, newLongID)
	if got != 2.54 {
		t.Errorf("GetHedgeSession(newLongID).cumulative_hedge_pnl = %v, want 2.54", got)
	}
}

// TestEnsureMatrixHedgeSession_IdempotentWhenMainUnchanged verifies the common tick-over-
// tick case (long leg's strategy_id hasn't changed) is a true no-op: no duplicate row, no
// accumulated_pnl disturbance.
func TestEnsureMatrixHedgeSession_IdempotentWhenMainUnchanged(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "mhs2")
	accID := createTestAccount(t, s, userID)
	botID, _, newLongID, shortID := createMatrixPairFixture(t, s, userID, accID, "MHS2USDT")
	ctx := context.Background()

	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, accumulated_pnl)
		 VALUES ($1,$2,$3,7.0) RETURNING id`,
		botID, newLongID, shortID).Scan(&sessionID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	for i := 0; i < 3; i++ {
		if err := ensureMatrixHedgeSession(ctx, s.pool, botID, newLongID, shortID); err != nil {
			t.Fatalf("ensureMatrixHedgeSession call %d: %v", i, err)
		}
	}

	var count int
	var accumulated float64
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*), MAX(accumulated_pnl) FROM hedge_sessions WHERE hedge_strategy_id=$1`, shortID,
	).Scan(&count, &accumulated); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (repeated calls with unchanged main id must not duplicate)", count)
	}
	if accumulated != 7.0 {
		t.Errorf("accumulated_pnl = %v, want unchanged 7.0", accumulated)
	}
}
