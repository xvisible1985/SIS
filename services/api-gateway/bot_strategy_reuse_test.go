//go:build integration

package main

import (
	"context"
	"testing"
)

// TestCreateBotStrategy_ReusesStoppedRow: при наличии остановленной строки бота по
// слоту createBotStrategy реактивирует ЕЁ (тот же id, status→active, конфиг перезаписан),
// не создавая новую. Без stopped-строки — создаётся новая.
func TestCreateBotStrategy_ReusesStoppedRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "reuse")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "reuse")
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID)
	})

	// Seed a stopped matrix leg for the slot (older row, low grid size).
	var stoppedID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, grid_size_usdt, created_at)
		 VALUES ($1,$2,$3,'REUUSDT','linear','long','matrix','stopped',5,NOW()-INTERVAL '1 hour') RETURNING id`,
		userID, accID, botID,
	).Scan(&stoppedID); err != nil {
		t.Fatalf("seed stopped: %v", err)
	}

	cfg := botCfgJSON{StrategyType: "matrix", GridSizeUSDT: 20, HedgeMode: true}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}

	// (1) Reuse path: an existing stopped row → reactivate it, no new row.
	id, err := s.createBotStrategy(ctx, b, cfg, "REUUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy: %v", err)
	}
	if id != stoppedID {
		t.Errorf("expected reuse of %s, got %s", stoppedID[:8], id)
	}
	var cnt int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='REUUSDT' AND direction='long'`, botID).Scan(&cnt)
	if cnt != 1 {
		t.Errorf("expected 1 row after reuse, got %d", cnt)
	}
	var status string
	var gridSize float64
	s.pool.QueryRow(ctx, `SELECT status, grid_size_usdt FROM strategies WHERE id=$1`, stoppedID).Scan(&status, &gridSize)
	if status != "active" {
		t.Errorf("reused row status=%q, want active", status)
	}
	if gridSize < 20 {
		t.Errorf("reused row grid_size_usdt=%v, want config value overwritten (>=20)", gridSize)
	}

	// (2) Insert path: no stopped row for a different slot → a brand-new row is created.
	newID, err := s.createBotStrategy(ctx, b, cfg, "REUUSDT", "short", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy insert: %v", err)
	}
	if newID == "" || newID == stoppedID {
		t.Errorf("expected a new row id for the short slot, got %q", newID)
	}
}
