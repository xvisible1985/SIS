//go:build integration

package main

import (
	"context"
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
