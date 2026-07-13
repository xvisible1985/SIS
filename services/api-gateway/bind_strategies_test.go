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

// TestBindStrategiesToBot: две одиночные (bot_id NULL) matrix-леги одного символа с
// противоположными направлениями привязываются к matrix-боту → у обеих bot_id=бот,
// status active. Невалидные комбинации отклоняются.
func TestBindStrategiesToBot(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "bind")
	accID := createTestAccount(t, s, userID)
	var botID string
	s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'bindbot',$2,'active','{"bot_kind":"matrix","hedge_mode":true,"grid_size_usdt":20}'::jsonb) RETURNING id`,
		userID, accID).Scan(&botID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })

	ins := func(dir string) string {
		var id string
		s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
			 VALUES ($1,$2,'BNDUSDT',$3,'matrix','active') RETURNING id`, userID, accID, dir).Scan(&id)
		t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })
		return id
	}
	longID, shortID := ins("long"), ins("short")

	body, _ := json.Marshal(map[string]string{"strategy_a_id": longID, "strategy_b_id": shortID, "bot_id": botID})
	req := httptest.NewRequest(http.MethodPost, "/strategies/bind", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.BindStrategiesToBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bind: got %d: %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{longID, shortID} {
		var bot *string
		var status string
		s.pool.QueryRow(ctx, `SELECT bot_id::text, status FROM strategies WHERE id=$1`, id).Scan(&bot, &status)
		if bot == nil || *bot != botID {
			t.Errorf("strategy %s bot_id=%v, want %s", id[:8], bot, botID)
		}
		if status != "active" {
			t.Errorf("strategy %s status=%s, want active", id[:8], status)
		}
	}

	// Invalid: same direction → non-200.
	long2 := ins("long")
	body2, _ := json.Marshal(map[string]string{"strategy_a_id": longID, "strategy_b_id": long2, "bot_id": botID})
	req2 := httptest.NewRequest(http.MethodPost, "/strategies/bind", bytes.NewReader(body2))
	req2 = withUserID(req2, userID)
	rec2 := httptest.NewRecorder()
	s.BindStrategiesToBot(rec2, req2)
	if rec2.Code == http.StatusOK {
		t.Errorf("same-direction bind should fail, got 200")
	}

	// Invalid: одна из лег уже привязана к ДРУГОМУ боту → non-200 (нельзя увести).
	var otherBotID string
	s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'otherbot',$2,'active','{"bot_kind":"matrix","hedge_mode":true,"grid_size_usdt":20}'::jsonb) RETURNING id`,
		userID, accID).Scan(&otherBotID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", otherBotID) })

	var boundShort string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'BNDUSDT','short','matrix','active') RETURNING id`,
		userID, accID, otherBotID).Scan(&boundShort)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", boundShort) })

	freeLong := ins("long")
	body3, _ := json.Marshal(map[string]string{"strategy_a_id": freeLong, "strategy_b_id": boundShort, "bot_id": botID})
	req3 := httptest.NewRequest(http.MethodPost, "/strategies/bind", bytes.NewReader(body3))
	req3 = withUserID(req3, userID)
	rec3 := httptest.NewRecorder()
	s.BindStrategiesToBot(rec3, req3)
	if rec3.Code == http.StatusOK {
		t.Errorf("bind of leg attached to another bot should fail, got 200")
	}
}
