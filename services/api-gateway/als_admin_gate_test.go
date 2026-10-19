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

// This file locks in the admin-only gate for strategy_type="als" (user instruction,
// 2026-10-19: "рзблокировать только для админа" — the ALS button in HedgeBotForm.tsx
// stays client-side disabled for non-admins, but these tests cover the actual
// server-side enforcement, same "don't trust the client" posture as the PLAYSOUTUSDT
// symbol-validation fix). Four entry points can carry strategy_type end to end:
// CreateStrategy/UpdateStrategy (strategy_handler.go) and CreateBot/PatchBot
// (bots_handler.go, via the strategyConfig JSON blob).

func TestCreateStrategy_RejectsAlsForNonAdmin(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "als_create_nonadmin")
	accID := createTestAccountExchange(t, s, userID, "binance")

	body, _ := json.Marshal(map[string]any{
		"account_id":    accID,
		"symbol":        "BTCUSDT",
		"direction":     "long",
		"strategy_type": "als",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/strategies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateStrategy(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("CreateStrategy: got %d %s, want 403", rec.Code, rec.Body.String())
	}
	var count int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM strategies WHERE account_id=$1 AND strategy_type='als'`, accID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("ALS strategy was persisted for a non-admin — count=%d, want 0", count)
	}
}

func TestCreateStrategy_AllowsAlsForAdmin(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "als_create_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	accID := createTestAccountExchange(t, s, userID, "binance")

	body, _ := json.Marshal(map[string]any{
		"account_id":    accID,
		"symbol":        "BTCUSDT",
		"direction":     "long",
		"strategy_type": "als",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/strategies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateStrategy(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateStrategy: got %d %s, want 201", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(rec.Body).Decode(&created)
	defer s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", created.ID)
}

func TestUpdateStrategy_RejectsAlsForNonAdmin(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "als_update_nonadmin")
	accID := createTestAccountExchange(t, s, userID, "binance")

	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, strategy_type)
		 VALUES ($1,$2,'BTCUSDT','linear','long','grid') RETURNING id`,
		userID, accID,
	).Scan(&id); err != nil {
		t.Fatalf("seed strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })

	body, _ := json.Marshal(map[string]any{
		"symbol":        "BTCUSDT",
		"direction":     "long",
		"strategy_type": "als",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/strategies/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": id})
	s.UpdateStrategy(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("UpdateStrategy: got %d %s, want 403", rec.Code, rec.Body.String())
	}
	var gotType string
	if err := s.pool.QueryRow(ctx, `SELECT strategy_type FROM strategies WHERE id=$1`, id).Scan(&gotType); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if gotType != "grid" {
		t.Errorf("strategy_type = %q, want unchanged %q (update must be rejected, not partially applied)", gotType, "grid")
	}
}

func TestCreateBot_RejectsAlsStrategyTypeForNonAdmin(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "als_bot_create_nonadmin@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	body := `{"name":"ALS bot","strategyConfig":{"strategy_type":"als","bot_kind":"hedge"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateBot(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("CreateBot: got %d %s, want 403", rec.Code, rec.Body.String())
	}
	var count int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM bots WHERE owner_id=$1`, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("bot was persisted despite the ALS strategy_type being rejected — count=%d, want 0", count)
	}
}

func TestCreateBot_AllowsAlsStrategyTypeForAdmin(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "als_bot_create_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	body := `{"name":"ALS bot","strategyConfig":{"strategy_type":"als","bot_kind":"hedge"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateBot(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateBot: got %d %s, want 201", rec.Code, rec.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&bot)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", bot["id"])
}

func TestPatchBot_RejectsAlsStrategyTypeForNonAdmin(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "als_bot_patch_nonadmin@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	botID := createTestBot(t, s, userID, "Grid bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	body := `{"strategyConfig":{"strategy_type":"als","bot_kind":"hedge"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("PatchBot: got %d %s, want 403", rec.Code, rec.Body.String())
	}
	var gotCfg []byte
	if err := s.pool.QueryRow(context.Background(),
		`SELECT strategy_config FROM bots WHERE id=$1`, botID,
	).Scan(&gotCfg); err != nil {
		t.Fatalf("read back: %v", err)
	}
	var cfg botCfgJSON
	json.Unmarshal(gotCfg, &cfg)
	if cfg.StrategyType == "als" {
		t.Errorf("strategy_config.strategy_type = als, want unchanged (update must be rejected)")
	}
}
