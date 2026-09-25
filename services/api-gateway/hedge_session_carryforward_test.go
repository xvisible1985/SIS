//go:build integration

package main

import (
	"context"
	"testing"
)

// createHSStrategy inserts a bare strategy row for the given bot/account/symbol/direction,
// for tests that need to simulate hedge reactivation creating a brand-new strategy id.
func createHSStrategy(t *testing.T, s *Server, userID, accID, botID, symbol, direction string) string {
	t.Helper()
	ctx := context.Background()
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, bot_id)
		 VALUES ($1,$2,$3,$4,'matrix',$5) RETURNING id`,
		userID, accID, symbol, direction, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", stratID) })
	return stratID
}

// TestCarryForwardHedgeAccumulatedPnl_NoPriorSession_ReturnsZero: first-ever activation for
// a bot+symbol+direction has nothing to carry forward.
func TestCarryForwardHedgeAccumulatedPnl_NoPriorSession_ReturnsZero(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cfnoprior")
	accID := createTestAccount(t, s, userID)
	botID, _ := createHSFixture(t, s, userID, accID, "cfnoprior")

	got := s.carryForwardHedgeAccumulatedPnl(ctx, botID, "NOPRIORUSDT", "short")
	if got != 0 {
		t.Errorf("carryForwardHedgeAccumulatedPnl = %v, want 0 (no prior session at all)", got)
	}
}

// TestCarryForwardHedgeAccumulatedPnl_PriorNonPairedClose_CarriesForward is the regression
// for the incident found live 2026-09-23: clicking "Закрыть Хедж" (a manual close) leaves
// end_reason as something other than "paired_close" (stopHedgeStrategy already keeps the
// old session open in that case — see TestStopHedgeStrategy_OnlyEndsSessionOnPairedClose),
// but reactivation creates a brand-new hedge_strategy_id, orphaning the old session from
// GetHedgeSession's per-strategy lookup. The new session must start from the old one's
// accumulated_pnl, not 0.
func TestCarryForwardHedgeAccumulatedPnl_PriorNonPairedClose_CarriesForward(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cfcarry")
	accID := createTestAccount(t, s, userID)
	botID, _ := createHSFixture(t, s, userID, accID, "cfcarry")

	oldStratID := createHSStrategy(t, s, userID, accID, botID, "CARRYUSDT", "short")
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, ended_at, end_reason, accumulated_pnl)
		 VALUES ($1,$2,NOW(),'position_gone',5.5)`,
		botID, oldStratID); err != nil {
		t.Fatalf("insert old session: %v", err)
	}

	got := s.carryForwardHedgeAccumulatedPnl(ctx, botID, "CARRYUSDT", "short")
	if got != 5.5 {
		t.Errorf("carryForwardHedgeAccumulatedPnl = %v, want 5.5 (carried from the manually-closed prior session)", got)
	}
}

// TestCarryForwardHedgeAccumulatedPnl_PriorPairedClose_ReturnsZero: a genuine paired close
// IS the intended reset point — the next activation must start fresh at 0, not carry the
// old total forward.
func TestCarryForwardHedgeAccumulatedPnl_PriorPairedClose_ReturnsZero(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cfpaired")
	accID := createTestAccount(t, s, userID)
	botID, _ := createHSFixture(t, s, userID, accID, "cfpaired")

	oldStratID := createHSStrategy(t, s, userID, accID, botID, "PAIREDUSDT", "short")
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, ended_at, end_reason, accumulated_pnl)
		 VALUES ($1,$2,NOW(),'paired_close',100)`,
		botID, oldStratID); err != nil {
		t.Fatalf("insert old session: %v", err)
	}

	got := s.carryForwardHedgeAccumulatedPnl(ctx, botID, "PAIREDUSDT", "short")
	if got != 0 {
		t.Errorf("carryForwardHedgeAccumulatedPnl = %v, want 0 — a genuine paired close must reset, not carry forward", got)
	}
}

// TestCarryForwardHedgeAccumulatedPnl_StillOpenPriorSession_CarriesForward: a prior session
// that was never properly ended (ended_at IS NULL, e.g. a race or a code path that missed
// closing it) must still carry forward rather than silently dropping its total.
func TestCarryForwardHedgeAccumulatedPnl_StillOpenPriorSession_CarriesForward(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cfopen")
	accID := createTestAccount(t, s, userID)
	botID, _ := createHSFixture(t, s, userID, accID, "cfopen")

	oldStratID := createHSStrategy(t, s, userID, accID, botID, "OPENUSDT", "short")
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, accumulated_pnl)
		 VALUES ($1,$2,2.25)`,
		botID, oldStratID); err != nil {
		t.Fatalf("insert old session: %v", err)
	}

	got := s.carryForwardHedgeAccumulatedPnl(ctx, botID, "OPENUSDT", "short")
	if got != 2.25 {
		t.Errorf("carryForwardHedgeAccumulatedPnl = %v, want 2.25", got)
	}
}

// TestCarryForwardHedgeAccumulatedPnl_UsesMostRecentSession: with multiple prior sessions
// for the same bot+symbol+direction, only the most recent one's accumulated_pnl carries
// forward (older ones are already folded into it, not summed again).
func TestCarryForwardHedgeAccumulatedPnl_UsesMostRecentSession(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cfrecent")
	accID := createTestAccount(t, s, userID)
	botID, _ := createHSFixture(t, s, userID, accID, "cfrecent")

	strat1 := createHSStrategy(t, s, userID, accID, botID, "RECENTUSDT", "short")
	strat2 := createHSStrategy(t, s, userID, accID, botID, "RECENTUSDT", "short")
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason, accumulated_pnl)
		 VALUES ($1,$2,NOW()-INTERVAL '2 hours',NOW()-INTERVAL '1 hour','position_gone',1.0)`,
		botID, strat1); err != nil {
		t.Fatalf("insert session1: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, started_at, ended_at, end_reason, accumulated_pnl)
		 VALUES ($1,$2,NOW()-INTERVAL '1 hour',NOW(),'main_closed',7.0)`,
		botID, strat2); err != nil {
		t.Fatalf("insert session2: %v", err)
	}

	got := s.carryForwardHedgeAccumulatedPnl(ctx, botID, "RECENTUSDT", "short")
	if got != 7.0 {
		t.Errorf("carryForwardHedgeAccumulatedPnl = %v, want 7.0 (the most recent session's total, not summed with the older one)", got)
	}
}
