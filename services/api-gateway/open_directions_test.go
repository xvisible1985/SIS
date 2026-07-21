//go:build integration

package main

import (
	"context"
	"testing"
)

// TestLoadOpenDirections_BlocksOtherBotsSymbol: a symbol+direction already traded by a
// DIFFERENT bot on the same account must show up as occupied — found live (2026-07-21)
// alongside the matching matrix-bot fix (TestDirectionHasLiveStrategy_BlocksOtherBotsSymbol):
// MatrixNova and ST-Fast both opened ARKMUSDT short within minutes of each other because
// the signal-bot duplicate-check was scoped to bot_id=$1 OR bot_id IS NULL only, never
// seeing another bot's own active strategies.
func TestLoadOpenDirections_BlocksOtherBotsSymbol(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "xsigblk")
	accID := createTestAccount(t, s, userID)
	botA := createZombieBot(t, s, userID, "xsig-a")

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ARKMUSDT','short','grid','active',$3) RETURNING id`,
		userID, accID, botA,
	).Scan(&id); err != nil {
		t.Fatalf("insert strategy for bot A: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	opened, err := s.loadOpenDirections(ctx, accID)
	if err != nil {
		t.Fatalf("loadOpenDirections: %v", err)
	}
	if !opened[openDirectionKey{"ARKMUSDT", "short"}] {
		t.Errorf("another bot's active ARKMUSDT short must show as occupied")
	}
	if opened[openDirectionKey{"ARKMUSDT", "long"}] {
		t.Errorf("ARKMUSDT long has no strategy from any bot and must not show as occupied")
	}
}
