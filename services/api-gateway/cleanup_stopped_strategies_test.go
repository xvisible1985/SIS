//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// seedStoppedStrategy inserts a stopped strategy row for the given bot/account/symbol and
// returns its id.
func seedStoppedStrategy(t *testing.T, s *Server, ownerID, accID, botID, symbol, direction string) string {
	t.Helper()
	var stratID string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status)
		 VALUES ($1,$2,$3,$4,'linear',$5,'grid','stopped') RETURNING id`,
		ownerID, accID, botID, symbol, direction,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed stopped strategy: %v", err)
	}
	return stratID
}

// TestCleanupStoppedStrategies_PositionStillOpenPastGracePeriod_NeverDeletes reproduces the
// bug found live 2026-09-18: a stopped strategy whose exchange position the cleanup engine
// still sees as open must never be deleted, no matter how long the grace period has already
// elapsed. Previously, once cleanupStoppedStrategiesMaxWait passed, the row was force-deleted
// unconditionally — orphaning a real, still-open position with nothing left to track it.
func TestCleanupStoppedStrategies_PositionStillOpenPastGracePeriod_NeverDeletes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanstillopen")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanstillopen")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")

	// Simulate the grace period having already elapsed (well past cleanupStoppedStrategiesMaxWait).
	s.cleanupWaiters.Store(stratID, time.Now().Add(-cleanupStoppedStrategiesMaxWait-time.Hour))

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{"ICXUSDT:short": true}

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategies WHERE id=$1`, stratID).Scan(&count); err != nil {
		t.Fatalf("query strategy: %v", err)
	}
	if count != 1 {
		t.Errorf("strategy row count = %d, want 1 — must NOT be deleted while the exchange still reports an open position", count)
	}
}

// TestCleanupStoppedStrategies_PositionClosed_Deletes verifies the legitimate case still
// works: once the exchange confirms the position is closed, the stopped strategy row is
// deleted as before, regardless of how long cleanup had been waiting.
func TestCleanupStoppedStrategies_PositionClosed_Deletes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanclosed")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanclosed")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{} // exchange reports nothing open for this symbol+direction

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategies WHERE id=$1`, stratID).Scan(&count); err != nil {
		t.Fatalf("query strategy: %v", err)
	}
	if count != 0 {
		t.Errorf("strategy row count = %d, want 0 — a genuinely closed position must still be cleaned up", count)
	}
}

// TestCleanupStoppedStrategies_PositionOpenWithinGracePeriod_WaitsWithoutDeleting verifies
// the normal waiting path (position open, grace period not yet elapsed) is unaffected.
func TestCleanupStoppedStrategies_PositionOpenWithinGracePeriod_WaitsWithoutDeleting(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanwaiting")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanwaiting")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{"ICXUSDT:short": true}

	// No prior cleanupWaiters entry — this is the first tick seeing the position open.
	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategies WHERE id=$1`, stratID).Scan(&count); err != nil {
		t.Fatalf("query strategy: %v", err)
	}
	if count != 1 {
		t.Errorf("strategy row count = %d, want 1 — must wait, not delete, within the grace period", count)
	}
}
