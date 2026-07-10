//go:build integration

package main

import (
	"context"
	"testing"
)

// TestDirectionHasLiveStrategy_PausedBlocks: нога в статусе paused должна
// блокировать пересоздание этого направления (ensureMatrixStrategies его пропустит),
// а направление без стратегии — не блокировать.
func TestDirectionHasLiveStrategy_PausedBlocks(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "pauseblk")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "pb")

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'PBUSDT','long','matrix','paused',$3) RETURNING id`,
		userID, accID, botID,
	).Scan(&id); err != nil {
		t.Fatalf("insert paused strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	if !s.directionHasLiveStrategy(ctx, accID, "PBUSDT", "long", botID) {
		t.Errorf("paused-нога должна блокировать пересоздание long")
	}
	if s.directionHasLiveStrategy(ctx, accID, "PBUSDT", "short", botID) {
		t.Errorf("short без стратегии не должен блокироваться")
	}
}
