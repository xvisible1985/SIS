//go:build integration

package main

import (
	"context"
	"encoding/json"
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

	// Non-empty matrix_levels/matrix_entry_level so the reuse-UPDATE actually binds a
	// *string into the jsonb columns — this exercises the ($::text)::jsonb cast; without
	// it pgx v5 (binary protocol) fails to bind *string to a jsonb column.
	cfg := botCfgJSON{
		StrategyType:     "matrix",
		GridSizeUSDT:     20,
		HedgeMode:        true,
		MatrixLevels:     json.RawMessage(`[{"pct":-1,"size":10},{"pct":-2,"size":20}]`),
		MatrixEntryLevel: json.RawMessage(`{"pct":0,"size":5}`),
	}
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
	var matrixLevels *string
	s.pool.QueryRow(ctx, `SELECT matrix_levels::text FROM strategies WHERE id=$1`, stoppedID).Scan(&matrixLevels)
	if matrixLevels == nil {
		t.Errorf("reused row matrix_levels is NULL, want the config jsonb written")
	}

	// (2) Insert path: no stopped row for a different slot → a brand-new row is created.
	newID, err := s.createBotStrategy(ctx, b, cfg, "REUUSDT", "short", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy insert: %v", err)
	}
	if newID == "" || newID == stoppedID {
		t.Errorf("expected a new row id for the short slot, got %q", newID)
	}

	// (3) Open-cycle guard: a stopped row that still has an OPEN cycle (ended_at IS NULL)
	// must NOT be reused — reactivating it would resurrect the stale cycle (loadActiveCycle
	// keys on ended_at IS NULL), making cycle_count=0 and the config/adopt overwrite
	// meaningless. Expect a brand-new row instead, and the open-cycle row left stopped.
	var openStopID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, created_at)
		 VALUES ($1,$2,$3,'OPENUSDT','linear','long','matrix','stopped',NOW()-INTERVAL '2 hours') RETURNING id`,
		userID, accID, botID,
	).Scan(&openStopID); err != nil {
		t.Fatalf("seed open-cycle stopped: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()-INTERVAL '2 hours')`,
		openStopID,
	); err != nil { // ended_at NULL ⇒ open cycle
		t.Fatalf("seed open cycle: %v", err)
	}

	openSlotID, err := s.createBotStrategy(ctx, b, cfg, "OPENUSDT", "long", 0, "", nil)
	if err != nil {
		t.Fatalf("createBotStrategy open-cycle slot: %v", err)
	}
	if openSlotID == "" || openSlotID == openStopID {
		t.Errorf("expected a NEW row (not reuse of open-cycle stopped %s), got %q", openStopID[:8], openSlotID)
	}
	var openStopStatus string
	s.pool.QueryRow(ctx, `SELECT status FROM strategies WHERE id=$1`, openStopID).Scan(&openStopStatus)
	if openStopStatus != "stopped" {
		t.Errorf("open-cycle stopped row status=%q, want it left stopped (not reactivated)", openStopStatus)
	}
	var openSlotCnt int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM strategies WHERE bot_id=$1 AND symbol='OPENUSDT' AND direction='long'`, botID).Scan(&openSlotCnt)
	if openSlotCnt != 2 {
		t.Errorf("expected 2 rows for OPENUSDT/long (open-cycle stopped + new), got %d", openSlotCnt)
	}
}
