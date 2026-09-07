//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpdateStrategy_ApplyToBot_MergesIntoTemplate: правка открытой bot-стратегии с
// applyToBot=true записывает изменённые синхронизируемые поля (tp_pct) в
// bots.strategy_config, не трогая другие поля шаблона (bot_kind остаётся).
func TestUpdateStrategy_ApplyToBot_MergesIntoTemplate(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "applytobot")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "applytobot")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"bot_kind":"signal","tp_pct":2.0}'::jsonb WHERE id=$1`,
		botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, tp_mode, sl_pct, sl_type)
		 VALUES ($1,$2,$3,'APPLYUSDT','linear','long','grid','active',2.0,'total',-5.0,'conditional') RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"account_id": accID, "symbol": "APPLYUSDT", "category": "linear", "direction": "long",
		"strategy_type": "grid", "grid_levels": 5, "grid_active": 3, "grid_step_pct": 1.0,
		"grid_size_usdt": 100, "tp_mode": "total", "tp_pct": 3.5, "sl_type": "conditional",
		"sl_pct": -5.0, "leverage": 1, "margin_type": "isolated", "entry_order_type": "limit",
		"applyToBot": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+stratID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": stratID})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cfgBytes []byte
	if err := s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id=$1`, botID).Scan(&cfgBytes); err != nil {
		t.Fatalf("read bot config: %v", err)
	}
	var cfg map[string]interface{}
	json.Unmarshal(cfgBytes, &cfg)

	if got := cfg["tp_pct"]; got != 3.5 {
		t.Errorf("expected tp_pct=3.5 merged into bot template, got %v", got)
	}
	if got := cfg["bot_kind"]; got != "signal" {
		t.Errorf("expected unrelated key bot_kind to survive merge, got %v", got)
	}
}

// TestUpdateStrategy_NoApplyToBot_LeavesTemplateUntouched: без applyToBot (или false)
// шаблон бота не меняется вообще, даже если стратегия принадлежит боту.
func TestUpdateStrategy_NoApplyToBot_LeavesTemplateUntouched(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "noapplytobot")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "noapplytobot")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET strategy_config = '{"tp_pct":2.0}'::jsonb WHERE id=$1`, botID,
	); err != nil {
		t.Fatalf("seed bot config: %v", err)
	}

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, category, direction, strategy_type, status, tp_pct, tp_mode, sl_pct, sl_type)
		 VALUES ($1,$2,$3,'NOAPPLYUSDT','linear','long','grid','active',2.0,'total',-5.0,'conditional') RETURNING id`,
		userID, accID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"account_id": accID, "symbol": "NOAPPLYUSDT", "category": "linear", "direction": "long",
		"strategy_type": "grid", "grid_levels": 5, "grid_active": 3, "grid_step_pct": 1.0,
		"grid_size_usdt": 100, "tp_mode": "total", "tp_pct": 9.9, "sl_type": "conditional",
		"sl_pct": -5.0, "leverage": 1, "margin_type": "isolated", "entry_order_type": "limit",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+stratID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": stratID})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cfgBytes []byte
	s.pool.QueryRow(ctx, `SELECT strategy_config FROM bots WHERE id=$1`, botID).Scan(&cfgBytes)
	var cfg map[string]interface{}
	json.Unmarshal(cfgBytes, &cfg)
	if got := cfg["tp_pct"]; got != 2.0 {
		t.Errorf("expected bot template tp_pct to stay 2.0, got %v", got)
	}
}

// TestUpdateStrategy_ApplyToBot_ManualStrategyNoCrash: applyToBot=true on a manual
// strategy (bot_id IS NULL) must not error or panic — there is no owning bot template
// to merge into, so the merge is simply skipped and the request still succeeds.
func TestUpdateStrategy_ApplyToBot_ManualStrategyNoCrash(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "applytobotmanual")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, strategy_type, status, tp_pct, tp_mode, sl_pct, sl_type)
		 VALUES ($1,$2,'MANUALUSDT','linear','long','grid','active',2.0,'total',-5.0,'conditional') RETURNING id`,
		userID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	body, _ := json.Marshal(map[string]interface{}{
		"account_id": accID, "symbol": "MANUALUSDT", "category": "linear", "direction": "long",
		"strategy_type": "grid", "grid_levels": 5, "grid_active": 3, "grid_step_pct": 1.0,
		"grid_size_usdt": 100, "tp_mode": "total", "tp_pct": 3.5, "sl_type": "conditional",
		"sl_pct": -5.0, "leverage": 1, "margin_type": "isolated", "entry_order_type": "limit",
		"applyToBot": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+stratID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": stratID})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
}
