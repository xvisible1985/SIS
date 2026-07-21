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

// TestDirectionHasLiveStrategy_BlocksOtherBotsSymbol: a symbol+direction already traded
// by a DIFFERENT bot on the same account must block a second bot from opening it too —
// found live (2026-07-21): MatrixNova and ST-Fast both opened ARKMUSDT short within
// minutes of each other because directionHasLiveStrategy only checked the calling bot's
// own strategies plus detached ones, never strategies owned by a different, still-active
// bot_id.
func TestDirectionHasLiveStrategy_BlocksOtherBotsSymbol(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "xbotblk")
	accID := createTestAccount(t, s, userID)
	botA := createZombieBot(t, s, userID, "xbot-a")
	botB := createZombieBot(t, s, userID, "xbot-b")

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ARKMUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botA,
	).Scan(&id); err != nil {
		t.Fatalf("insert strategy for bot A: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	if !s.directionHasLiveStrategy(ctx, accID, "ARKMUSDT", "short", botB) {
		t.Errorf("bot B must see bot A's active ARKMUSDT short as a conflict and refuse to open its own")
	}
	// Sanity: bot A itself still correctly recognizes its own strategy too (regression
	// guard against a fix that accidentally narrows this back to self-only or drops the
	// self case entirely).
	if !s.directionHasLiveStrategy(ctx, accID, "ARKMUSDT", "short", botA) {
		t.Errorf("bot A must still see its own ARKMUSDT short")
	}
	// A direction neither bot has must remain unblocked.
	if s.directionHasLiveStrategy(ctx, accID, "ARKMUSDT", "long", botB) {
		t.Errorf("ARKMUSDT long has no strategy from any bot and must not be blocked")
	}
}
