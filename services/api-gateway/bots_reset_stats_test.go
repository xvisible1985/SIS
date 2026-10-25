//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// patchBotConfig PATCHes a bot's strategyConfig with the given extra JSON fields.
func patchBotConfig(t *testing.T, s *Server, userID, botID, extra string) {
	t.Helper()
	body := `{"strategyConfig":{"strategy_type":"grid","direction":"long","leverage":5}` + extra + `}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /bots/%s: got %d: %s", botID, rec.Code, rec.Body.String())
	}
}

// TestPatchBot_ResetStatsFlag pins the history-wipe contract of PATCH /bots/{id}: changing
// strategyConfig on a private bot wipes its trade history only when the caller sends
// resetStats=true (the editor's confirm dialog), and always for a public bot, whose
// published statistics must keep matching the config shown with them. Regression for the
// 2026-10-24 incident: saving an ALS bot's "size as main" silently erased its trades.
func TestPatchBot_ResetStatsFlag(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		isPublic  bool
		extra     string
		wantKept  bool
		wantTimer bool // approval timer still running (not reset)
	}{
		{"private, no flag: history and timer kept", false, ``, true, true},
		{"private, resetStats=false: history kept", false, `,"resetStats":false`, true, true},
		{"private, resetStats=true: wiped", false, `,"resetStats":true`, false, false},
		{"public, no flag: always wiped", true, ``, false, false},
	}
	for i, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := newTestServer(t)
			email := "reset_stats_" + string(rune('a'+i)) + "@example.com"
			userID := createAdminTestUser(t, s, email, "pass1234", false)
			defer s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", userID)
			accID := createTestAccount(t, s, userID)
			botID := createTestBot(t, s, userID, "Reset Stats Bot", c.isPublic)
			defer s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID)

			s.pool.Exec(ctx, `UPDATE bots SET active_seconds_acc = 10*86400, status = 'stopped' WHERE id = $1`, botID)
			if _, err := s.pool.Exec(ctx, `
				INSERT INTO trade_history (bot_id, account_id, owner_id, symbol, category, direction, cycle_num, result, opened_at)
				VALUES ($1,$2,$3,'RSTATSUSDT','linear','long',1,'tp',NOW())`, botID, accID, userID); err != nil {
				t.Fatalf("seed trade_history: %v", err)
			}

			patchBotConfig(t, s, userID, botID, c.extra)

			var n int
			var acc int64
			s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE bot_id=$1`, botID).Scan(&n)
			s.pool.QueryRow(ctx, `SELECT active_seconds_acc FROM bots WHERE id=$1`, botID).Scan(&acc)
			if kept := n == 1; kept != c.wantKept {
				t.Errorf("trade_history rows = %d, wantKept=%v", n, c.wantKept)
			}
			if timerKept := acc > 0; timerKept != c.wantTimer {
				t.Errorf("active_seconds_acc = %d, want timer kept=%v", acc, c.wantTimer)
			}
		})
	}
}
