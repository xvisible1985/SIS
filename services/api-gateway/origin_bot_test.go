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

// TestCreateBotStrategy_ReuseKeepsExistingOrigin: реюз-ветка createBotStrategy
// (UPDATE ... status='stopped') использует COALESCE(origin_bot_id, $N), поэтому уже
// проставленный origin (иной источник / legacy) НЕ должен перезаписываться на b.id.
func TestCreateBotStrategy_ReuseKeepsExistingOrigin(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "coalesce")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "coalesce")
	otherBot := createZombieBot(t, s, userID, "coalesceother")
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1 OR origin_bot_id=$1 OR origin_bot_id=$2", botID, otherBot)
	})

	// Засидить stopped-строку этого бота, но с origin_bot_id = ДРУГОЙ бот (иной источник).
	// created_at в прошлом, чтобы reuse-SELECT (ORDER BY created_at DESC) её и взял.
	var stoppedID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO strategies
		  (owner_id, account_id, bot_id, symbol, direction, strategy_type, status, origin_bot_id, created_at)
		VALUES ($1,$2,$3,'COAUSDT','long','matrix','stopped',$4, NOW()-INTERVAL '1 hour')
		RETURNING id`,
		userID, accID, botID, otherBot,
	).Scan(&stoppedID); err != nil {
		t.Fatalf("seed stopped strategy: %v", err)
	}

	cfg := botCfgJSON{StrategyType: "matrix", GridSizeUSDT: 20, HedgeMode: true}
	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	id, err := s.createBotStrategy(ctx, b, cfg, "COAUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy: %v", err)
	}
	// Убедиться, что reuse реально сработал (вернулась та же stopped-строка), а не INSERT.
	if id != stoppedID {
		t.Fatalf("expected reuse of stopped row %s, got new id %s", stoppedID, id)
	}

	var origin *string
	s.pool.QueryRow(ctx, `SELECT origin_bot_id::text FROM strategies WHERE id=$1`, id).Scan(&origin)
	if origin == nil || *origin != otherBot {
		t.Errorf("after reuse origin_bot_id=%v, want %s (COALESCE must not overwrite existing origin)", origin, otherBot)
	}
}
