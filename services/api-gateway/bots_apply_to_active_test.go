//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPatchBot_ApplyToActiveFalse_LeavesStrategiesUntouched: strategyConfig меняется в
// шаблоне бота, но без applyToActive=true уже активная стратегия этого бота не трогается.
func TestPatchBot_ApplyToActiveFalse_LeavesStrategiesUntouched(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "noactive")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "noactive")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0,"grid_levels":5}'::jsonb WHERE id=$1`, botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, grid_levels)
		 VALUES ($1,$2,$3,'NOACTUSDT','linear','long','grid','active',2.0,5) RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body := `{"strategyConfig":{"tp_pct":7.7,"grid_levels":5}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	// syncBotStrategies (if it ran) is fire-and-forget — give it a moment, then assert it
	// did NOT touch the strategy row.
	time.Sleep(200 * time.Millisecond)
	var tpPct float64
	s.pool.QueryRow(ctx, `SELECT tp_pct FROM strategies WHERE id=$1`, stratID).Scan(&tpPct)
	if tpPct != 2.0 {
		t.Errorf("expected strategy tp_pct to stay 2.0 without applyToActive, got %v", tpPct)
	}
}

// TestPatchBot_ApplyToActiveTrue_UpdatesActiveStrategy: с applyToActive=true изменённые
// синхронизируемые поля из шаблона бота применяются к активной стратегии этого бота.
func TestPatchBot_ApplyToActiveTrue_UpdatesActiveStrategy(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "yesactive")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "yesactive")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0,"grid_levels":5,"grid_active":3,"grid_step_pct":1.0,"grid_size_usdt":100}'::jsonb WHERE id=$1`,
		botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, grid_levels, grid_active, grid_step_pct, grid_size_usdt)
		 VALUES ($1,$2,$3,'YESACTUSDT','linear','long','grid','active',2.0,5,3,1.0,100) RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body := `{"strategyConfig":{"tp_pct":7.7,"grid_levels":5,"grid_active":3,"grid_step_pct":1.0,"grid_size_usdt":100},"applyToActive":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	// syncBotStrategies runs as `go ...` — poll briefly for the async UPDATE to land.
	deadline := time.Now().Add(2 * time.Second)
	var tpPct float64
	for time.Now().Before(deadline) {
		s.pool.QueryRow(ctx, `SELECT tp_pct FROM strategies WHERE id=$1`, stratID).Scan(&tpPct)
		if tpPct == 7.7 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tpPct != 7.7 {
		t.Errorf("expected strategy tp_pct=7.7 after applyToActive=true, got %v", tpPct)
	}
}
