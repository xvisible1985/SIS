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

// TestGetHedgeSession_IncludesRealizedPnl verifies that per-level matrix SL
// closes (strategy_levels.realized_pnl) count toward the cumulative counter,
// not just full-cycle trade_history rows — covering SL/TP/manual alike.
func TestGetHedgeSession_IncludesRealizedPnl(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hsrp1")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "rp1")
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id) VALUES ($1,$2)`,
		botID, stratID); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	now := time.Now()
	// A full-cycle TP close via trade_history.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		    cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'HSUSDT','linear','short',1,'tp',$4,$5,1.5)`,
		stratID, accID, userID, now.Add(-time.Hour), now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("insert trade_history: %v", err)
	}
	// A manual close via trade_history.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		    cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'HSUSDT','linear','short',2,'manual_close',$4,$5,0.25)`,
		stratID, accID, userID, now.Add(-time.Hour), now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("insert trade_history manual: %v", err)
	}
	// A per-level matrix SL close via strategy_levels.realized_pnl (needs a cycle row).
	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,3,$2) RETURNING id`,
		stratID, now.Add(-time.Hour)).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, realized_pnl, sl_closed_at)
		 VALUES ($1,$2,1,'Sell',1.0,10,'10','sl_closed',0.1,$3)`,
		stratID, cycleID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("insert level: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	want := 1.5 + 0.25 + 0.1
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (tp + manual + per-level SL realized_pnl)", got, want)
	}
}

// TestGetHedgeSession_IncludesMatrixTPProfits verifies that global matrix-TP
// re-arm profits (matrix_tp_profits rows) count toward the cumulative counter and
// respect the paired-close reset boundary — the matrix TP path never writes to
// trade_history, so without this source the counter would miss all TP profit.
func TestGetHedgeSession_IncludesMatrixTPProfits(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hstp1")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "tp1")
	ctx := context.Background()

	now := time.Now()
	pairedCloseAt := now.Add(-time.Hour)

	// Session #1 ended via paired_close (sets the floor), session #2 active.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason)
		 VALUES ($1,$2,$3,$4,'paired_close')`,
		botID, stratID, now.Add(-2*time.Hour), pairedCloseAt); err != nil {
		t.Fatalf("insert session1: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at) VALUES ($1,$2,$3)`,
		botID, stratID, pairedCloseAt); err != nil {
		t.Fatalf("insert session2: %v", err)
	}

	// A matrix-TP profit BEFORE the paired close — must be excluded.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO matrix_tp_profits (strategy_id, bot_id, account_id, cycle_num, symbol, net_pnl, bybit_order_id, closed_at)
		 VALUES ($1,$2,$3,1,'HSUSDT',9.0,'old-tp',$4)`,
		stratID, botID, accID, pairedCloseAt.Add(-5*time.Minute)); err != nil {
		t.Fatalf("insert old matrix tp: %v", err)
	}
	// Two matrix-TP profits AFTER the paired close — must count.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO matrix_tp_profits (strategy_id, bot_id, account_id, cycle_num, symbol, net_pnl, bybit_order_id, closed_at)
		 VALUES ($1,$2,$3,1,'HSUSDT',0.30,'tp-a',$4),
		        ($1,$2,$3,1,'HSUSDT',0.20,'tp-b',$5)`,
		stratID, botID, accID, now.Add(-40*time.Minute), now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("insert new matrix tp: %v", err)
	}
	// A full-cycle trade to confirm sources add together.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		    cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'HSUSDT','linear','short',2,'tp',$4,$5,1.0)`,
		stratID, accID, userID, now.Add(-time.Hour), now.Add(-15*time.Minute)); err != nil {
		t.Fatalf("insert trade_history: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	want := 1.0 + 0.30 + 0.20 // excludes the 9.0 before paired_close
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (trade_history + matrix TP profits after paired_close)", got, want)
	}
}

// TestGetHedgeSession_ResetsOnlyOnPairedClose verifies that trades closed
// before a "paired_close" session boundary are excluded, while trades from
// non-paired-close stops (deactivation, trailing_profit, position_gone) keep
// accumulating across session restarts.
func TestGetHedgeSession_ResetsOnlyOnPairedClose(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "hsrp2")
	accID := createTestAccount(t, s, userID)
	botID, stratID := createHSFixture(t, s, userID, accID, "rp2")
	ctx := context.Background()

	now := time.Now()
	pairedCloseAt := now.Add(-time.Hour)

	// Session #1: ended via genuine paired close.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason)
		 VALUES ($1,$2,$3,$4,'paired_close')`,
		botID, stratID, now.Add(-2*time.Hour), pairedCloseAt); err != nil {
		t.Fatalf("insert session1: %v", err)
	}
	// Session #2: ended via deactivation (should NOT reset the counter).
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason)
		 VALUES ($1,$2,$3,$4,'deactivation')`,
		botID, stratID, pairedCloseAt, now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("insert session2: %v", err)
	}
	// Session #3: currently active.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at)
		 VALUES ($1,$2,$3)`,
		botID, stratID, now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("insert session3: %v", err)
	}

	// Trade BEFORE the paired close — must be excluded.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		    cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'HSUSDT','linear','short',1,'tp',$4,$5,100)`,
		stratID, accID, userID, now.Add(-3*time.Hour), pairedCloseAt.Add(-5*time.Minute)); err != nil {
		t.Fatalf("insert old trade: %v", err)
	}
	// Trade AFTER the paired close, during session #2 (deactivation) — must count.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction,
		    cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'HSUSDT','linear','short',2,'sl',$4,$5,3)`,
		stratID, accID, userID, pairedCloseAt.Add(time.Minute), now.Add(-40*time.Minute)); err != nil {
		t.Fatalf("insert new trade: %v", err)
	}

	got := getHedgeSessionPnl(t, s, userID, stratID)
	want := 3.0
	if diff := got - want; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("cumulative_hedge_pnl = %v, want %v (only trades after last paired_close)", got, want)
	}
}
