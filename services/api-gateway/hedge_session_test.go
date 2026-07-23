//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// createHSFixture creates a bot + hedge strategy owned by userID, ready to attach
// hedge_sessions rows to in tests.
func createHSFixture(t *testing.T, s *Server, userID, accountID, suffix string) (botID, stratID string) {
	t.Helper()
	ctx := context.Background()
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name) VALUES ($1,$2) RETURNING id`,
		userID, "hs-bot-"+suffix,
	).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })

	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, bot_id)
		 VALUES ($1,$2,'HSUSDT','short','matrix',$3) RETURNING id`,
		userID, accountID, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", stratID) })
	return botID, stratID
}

func getHedgeSessionPnl(t *testing.T, s *Server, userID, stratID string) float64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/hedge-session", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	rec := httptest.NewRecorder()
	s.GetHedgeSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetHedgeSession: got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CumulativeHedgePnl float64 `json:"cumulative_hedge_pnl"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.CumulativeHedgePnl
}

// TestGetHedgeSession_IncludesRealizedPnl verifies that the cumulative counter
// reflects hedge_sessions.accumulated_pnl directly — the single column that
// AccumulateHedgeSessionPnl increments as each PnL event (full-cycle close via
// trade_history, or per-level matrix SL via strategy_levels) is realized.
func TestGetHedgeSession_IncludesRealizedPnl(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hsrp1")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "rp1")
	ctx := context.Background()

	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id) VALUES ($1,$2) RETURNING id`,
		botID, stratID).Scan(&sessionID); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	want := 1.5 + 0.25 + 0.1
	if _, err := s.pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl=$1 WHERE id=$2`, want, sessionID); err != nil {
		t.Fatalf("seed accumulated_pnl: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (accumulated_pnl read directly)", got, want)
	}
}

// TestGetHedgeSession_ReadsAccumulatedPnl verifies that GetHedgeSession surfaces
// hedge_sessions.accumulated_pnl as-is, without any reconstruction from
// trade_history/strategy_levels/matrix_tp_profits — the column is the sole
// source of truth, kept up to date by AccumulateHedgeSessionPnl.
func TestGetHedgeSession_ReadsAccumulatedPnl(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hstp1")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "tp1")
	ctx := context.Background()

	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id) VALUES ($1,$2) RETURNING id`,
		botID, stratID).Scan(&sessionID); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	want := 1.0 + 0.30 + 0.20
	if _, err := s.pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl=$1 WHERE id=$2`, want, sessionID); err != nil {
		t.Fatalf("seed accumulated_pnl: %v", err)
	}

	// A matrix_tp_profits row must NOT influence the response — that table is
	// no longer read by GetHedgeSession.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO matrix_tp_profits (strategy_id, bot_id, account_id, cycle_num, symbol, net_pnl, bybit_order_id, closed_at)
		 VALUES ($1,$2,$3,1,'HSUSDT',999.0,'ignored-tp',now())`,
		stratID, botID, accID); err != nil {
		t.Fatalf("insert matrix tp: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (accumulated_pnl only, matrix_tp_profits ignored)", got, want)
	}
}

// TestGetHedgeSession_ResetsOnlyOnPairedClose verifies that a new session row
// (created after any stop reason, paired_close included) starts its own
// accumulated_pnl at 0 and GetHedgeSession reports exactly that session's
// value — the reset semantics now live entirely in how accumulated_pnl is
// seeded per row, not in a query-side floor_time computation.
func TestGetHedgeSession_ResetsOnlyOnPairedClose(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hsrp2")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "rp2")
	ctx := context.Background()

	now := time.Now()
	pairedCloseAt := now.Add(-time.Hour)

	// Session #1: ended via genuine paired close, had accumulated PnL that must
	// NOT leak into the latest session's reported value.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason, accumulated_pnl)
		 VALUES ($1,$2,$3,$4,'paired_close',100)`,
		botID, stratID, now.Add(-2*time.Hour), pairedCloseAt); err != nil {
		t.Fatalf("insert session1: %v", err)
	}
	// Session #2: currently active, starts fresh at 0 (default) then accumulates 3.
	var session2ID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at)
		 VALUES ($1,$2,$3) RETURNING id`,
		botID, stratID, pairedCloseAt).Scan(&session2ID); err != nil {
		t.Fatalf("insert session2: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE hedge_sessions SET accumulated_pnl=3 WHERE id=$1`, session2ID); err != nil {
		t.Fatalf("seed accumulated_pnl: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	want := 3.0
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (only the latest session's own accumulated_pnl)", got, want)
	}
}

// TestGetHedgeSession_IncludesCloseTypeAndThreshold: the response must surface the bot's
// active paired-close mode and threshold so the frontend can render a progress indicator
// without a second API call.
func TestGetHedgeSession_IncludesCloseTypeAndThreshold(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "sessct1")
	accID := createTestAccount(t, s, userID)

	cfgJSON := `{"bot_kind":"matrix","hedge_deact_close_type":2,"hedge_breakeven_profit":10.5}`
	var botID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, status, strategy_config)
		 VALUES ($1,'ct-bot','active',$2::jsonb) RETURNING id`,
		userID, cfgJSON).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		VALUES ($1,$2,'CTUSDT','long','matrix','active',$3) RETURNING id`, userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("insert main strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", mainID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		VALUES ($1,$2,'CTUSDT','short','matrix','active',$3) RETURNING id`, userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", hedgeID) })
	if _, err := s.pool.Exec(ctx, `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3)`,
		botID, mainID, hedgeID); err != nil {
		t.Fatalf("insert hedge_sessions: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/strategies/"+hedgeID+"/hedge-session", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": hedgeID})
	rec := httptest.NewRecorder()
	s.GetHedgeSession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CloseType      int     `json:"close_type"`
		CloseThreshold float64 `json:"close_threshold"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.CloseType != 2 {
		t.Errorf("CloseType = %d, want 2", resp.CloseType)
	}
	if resp.CloseThreshold != 10.5 {
		t.Errorf("CloseThreshold = %v, want 10.5", resp.CloseThreshold)
	}
}
