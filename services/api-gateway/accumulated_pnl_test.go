//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/strategy"
)

// TestAccumulateHedgeSessionPnl_AddsToOpenSession: the helper adds netPnl to whichever
// currently-open (ended_at IS NULL) hedge_sessions row matches the strategy, whether it's
// the main leg or the hedge/matrix leg.
func TestAccumulateHedgeSessionPnl_AddsToOpenSession(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "accpnl1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "accpnl-bot")

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','long','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("insert main strategy: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("insert hedge_sessions: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, mainID, 12.5)
	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, hedgeID, -3.25)

	var got float64
	if err := s.pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&got); err != nil {
		t.Fatalf("read accumulated_pnl: %v", err)
	}
	if got != 9.25 {
		t.Errorf("accumulated_pnl = %v, want 9.25 (12.5 + -3.25, both legs' events land on the same session)", got)
	}
}

// TestHedgeSessionMainIDRestored_OnConflictRepairsNullMain guards the regression where a
// matrix main (long) leg gets recreated: ON DELETE SET NULL clears main_strategy_id in
// hedge_sessions, and the next checkMatrixPairedClose tick must restore it via
// ON CONFLICT DO UPDATE … WHERE main_strategy_id IS NULL, so GetHedgeSession can find
// the session again by the new main strategy ID.
func TestHedgeSessionMainIDRestored_OnConflictRepairsNullMain(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "mainrestore1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "mainrestore-bot")

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'RSTUSDT','long','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("insert main strategy: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'RSTUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM strategies WHERE id IN ($1,$2)", mainID, hedgeID)
	})

	// Simulate post-recreate state: session row exists but main_strategy_id was NULLed
	// by ON DELETE SET NULL when the old main leg was deleted and recreated.
	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, accumulated_pnl)
		 VALUES ($1,$2,0.5) RETURNING id`,
		botID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("insert hedge_sessions with NULL main: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	// The same INSERT that checkMatrixPairedClose runs each tick must restore main_strategy_id.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (hedge_strategy_id) WHERE ended_at IS NULL
		 DO UPDATE SET main_strategy_id = EXCLUDED.main_strategy_id
		 WHERE hedge_sessions.main_strategy_id IS NULL`,
		botID, mainID, hedgeID); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var gotMain *string
	if err := s.pool.QueryRow(ctx, `SELECT main_strategy_id FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&gotMain); err != nil {
		t.Fatalf("read main_strategy_id: %v", err)
	}
	if gotMain == nil || *gotMain != mainID {
		t.Errorf("main_strategy_id = %v, want %s", gotMain, mainID)
	}

	// GetHedgeSession must now find the session when queried by the main strategy ID.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+mainID+"/hedge-session", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": mainID})
	s.GetHedgeSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetHedgeSession: got %d: %s", rec.Code, rec.Body.String())
	}
	var hs map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&hs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hs["id"] != sessionID {
		t.Errorf("GetHedgeSession returned id=%v, want %s", hs["id"], sessionID)
	}
	if hs["cumulative_hedge_pnl"] == nil {
		t.Error("cumulative_hedge_pnl must be present after main_strategy_id restored")
	}
}

// TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp: a strategy with no open hedge_sessions
// row (a standalone strategy, or one whose session already ended) must not error or touch
// any other bot's session.
func TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "accpnl2")
	accID := createTestAccount(t, s, userID)

	var soloID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'SOLOUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&soloID); err != nil {
		t.Fatalf("insert solo strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", soloID) })

	// Must not panic or error — there is no hedge_sessions row for this strategy at all.
	strategy.AccumulateHedgeSessionPnl(ctx, s.pool, soloID, 5.0)
}
