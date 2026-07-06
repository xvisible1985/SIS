//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// seedZombieMatrixStrategy creates an active bot matrix strategy whose only cycle ended
// more than the grace period ago (a "zombie" with no active cycle).
func seedZombieMatrixStrategy(t *testing.T, s *Server, botID, accID, ownerID, symbol, dir string) string {
	t.Helper()
	ctx := context.Background()
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,$3,$4,'matrix','active',$5) RETURNING id`,
		ownerID, accID, symbol, dir, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("insert strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// A cycle that ended 5 minutes ago (past the 2-minute grace).
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,NOW()-INTERVAL '10 minutes',NOW()-INTERVAL '5 minutes','ghost_close')`,
		stratID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	return stratID
}

func statusOf(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.pool.QueryRow(context.Background(), `SELECT status FROM strategies WHERE id=$1`, id).Scan(&st); err != nil {
		t.Fatalf("status: %v", err)
	}
	return st
}

func createZombieBot(t *testing.T, s *Server, ownerID, suffix string) string {
	t.Helper()
	var botID string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO bots (owner_id, name) VALUES ($1,$2) RETURNING id`, ownerID, "zbot-"+suffix,
	).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })
	return botID
}

// TestMatrixZombie_StopsWhenPositionGone: a zombie leg with no exchange position is stopped.
func TestMatrixZombie_StopsWhenPositionGone(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "zomb1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "1")
	stratID := seedZombieMatrixStrategy(t, s, botID, accID, userID, "ZMBUSDT", "short")

	// posMap has no ZMBUSDT position → should be stopped.
	s.checkMatrixZombieStrategies(context.Background(), botID, map[string]map[string]hedgePosInfo{})

	if got := statusOf(t, s, stratID); got != "stopped" {
		t.Errorf("zombie status = %q, want stopped", got)
	}
}

// TestMatrixZombie_SkipsWhenPositionOpen: a zombie leg whose exchange position is still
// open must NOT be stopped (avoids double-opening).
func TestMatrixZombie_SkipsWhenPositionOpen(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "zomb2")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "2")
	stratID := seedZombieMatrixStrategy(t, s, botID, accID, userID, "ZMBUSDT", "short")

	// posMap shows an open short (Sell) position → must be skipped.
	posMap := map[string]map[string]hedgePosInfo{
		"ZMBUSDT": {"Sell": {Symbol: "ZMBUSDT", Side: "Sell", Size: 100}},
	}
	s.checkMatrixZombieStrategies(context.Background(), botID, posMap)

	if got := statusOf(t, s, stratID); got != "active" {
		t.Errorf("zombie with open position status = %q, want active (skipped)", got)
	}
}

// TestMatrixZombie_SkipsWithinGrace: a leg whose cycle ended recently (within grace) is
// not stopped — avoids racing a normal in-flight restart.
func TestMatrixZombie_SkipsWithinGrace(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "zomb3")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "3")

	var stratID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ZMBUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&stratID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// Cycle ended 30 seconds ago — within the 2-minute grace.
	s.pool.Exec(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,$2,$3,'tp')`, stratID, time.Now().Add(-5*time.Minute), time.Now().Add(-30*time.Second))

	s.checkMatrixZombieStrategies(ctx, botID, map[string]map[string]hedgePosInfo{})

	if got := statusOf(t, s, stratID); got != "active" {
		t.Errorf("recently-closed leg status = %q, want active (within grace)", got)
	}
}
