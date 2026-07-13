//go:build integration

package main

import (
	"context"
	"testing"
)

// TestCreateBotStrategy_SetsOriginBotId: createBotStrategy пишет origin_bot_id=bot_id,
// и detach (leave) его НЕ обнуляет.
func TestCreateBotStrategy_SetsOriginBotId(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "origin")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "origin")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1 OR origin_bot_id=$1", botID) })

	cfg := botCfgJSON{StrategyType: "matrix", GridSizeUSDT: 20, HedgeMode: true}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	id, err := s.createBotStrategy(ctx, b, cfg, "ORGUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy: %v", err)
	}

	var origin *string
	s.pool.QueryRow(ctx, `SELECT origin_bot_id::text FROM strategies WHERE id=$1`, id).Scan(&origin)
	if origin == nil || *origin != botID {
		t.Errorf("origin_bot_id=%v, want %s", origin, botID)
	}

	// Simulate detach (leave): bot_id -> NULL. origin must survive.
	s.pool.Exec(ctx, `UPDATE strategies SET bot_id=NULL WHERE id=$1`, id)
	s.pool.QueryRow(ctx, `SELECT origin_bot_id::text FROM strategies WHERE id=$1`, id).Scan(&origin)
	if origin == nil || *origin != botID {
		t.Errorf("after detach origin_bot_id=%v, want %s (must not be cleared)", origin, botID)
	}
}
