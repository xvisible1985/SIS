//go:build integration

package main

import (
	"context"
	"testing"
)

// TestRecordRescuePartialClose_UpdatesOpenSession: the helper adds coinDelta/usdtDelta
// to whichever currently-open (ended_at IS NULL) hedge_sessions row matches the strategy,
// whether it's the main leg or the hedge leg, and stamps last_partial_close_at — same
// matching convention as strategy.AccumulateHedgeSessionPnl (accumulated_pnl_test.go).
func TestRecordRescuePartialClose_UpdatesOpenSession(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "rescuepc1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "rescuepc-bot")

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

	if err := s.recordRescuePartialClose(ctx, mainID, 1.5, 150); err != nil {
		t.Fatalf("recordRescuePartialClose(main): %v", err)
	}
	if err := s.recordRescuePartialClose(ctx, hedgeID, 0.5, 50); err != nil {
		t.Fatalf("recordRescuePartialClose(hedge): %v", err)
	}

	var coin, usdt float64
	var lastAt *string
	if err := s.pool.QueryRow(ctx,
		`SELECT main_reduced_coin, main_reduced_usdt, last_partial_close_at::text FROM hedge_sessions WHERE id=$1`,
		sessionID).Scan(&coin, &usdt, &lastAt); err != nil {
		t.Fatalf("read hedge_sessions: %v", err)
	}
	if coin != 2.0 {
		t.Errorf("main_reduced_coin = %v, want 2.0 (1.5 + 0.5, both legs' calls land on the same session)", coin)
	}
	if usdt != 200 {
		t.Errorf("main_reduced_usdt = %v, want 200 (150 + 50)", usdt)
	}
	if lastAt == nil {
		t.Errorf("last_partial_close_at = nil, want non-null timestamp")
	}
}

// TestRecordRescuePartialClose_NoOpenSession_NoOp: a strategy with no open hedge_sessions
// row (a standalone strategy) must not error — mirrors
// TestAccumulateHedgeSessionPnl_NoOpenSession_NoOp.
func TestRecordRescuePartialClose_NoOpenSession_NoOp(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "rescuepc2")
	accID := createTestAccount(t, s, userID)

	var soloID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'SOLOUSDT','long','matrix','active') RETURNING id`,
		userID, accID).Scan(&soloID); err != nil {
		t.Fatalf("insert solo strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", soloID) })

	// Must not error — there is no hedge_sessions row for this strategy at all.
	if err := s.recordRescuePartialClose(ctx, soloID, 1.0, 100); err != nil {
		t.Errorf("recordRescuePartialClose with no open session: unexpected error %v", err)
	}
}

// TestRecordRescuePartialClose_EndedSession_NotUpdated: a session whose ended_at is already
// set must not be touched by the WHERE ... AND ended_at IS NULL guard — same guard clause
// AccumulateHedgeSessionPnl relies on.
func TestRecordRescuePartialClose_EndedSession_NotUpdated(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "rescuepc3")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "rescuepc-bot3")

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','long','matrix','stopped',$3) RETURNING id`,
		userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("insert main strategy: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ACCUSDT','short','matrix','stopped',$3) RETURNING id`,
		userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, ended_at) VALUES ($1,$2,$3,NOW()) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("insert ended hedge_sessions: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	if err := s.recordRescuePartialClose(ctx, mainID, 1.5, 150); err != nil {
		t.Fatalf("recordRescuePartialClose: %v", err)
	}

	var coin, usdt float64
	var lastAt *string
	if err := s.pool.QueryRow(ctx,
		`SELECT main_reduced_coin, main_reduced_usdt, last_partial_close_at::text FROM hedge_sessions WHERE id=$1`,
		sessionID).Scan(&coin, &usdt, &lastAt); err != nil {
		t.Fatalf("read hedge_sessions: %v", err)
	}
	if coin != 0 || usdt != 0 {
		t.Errorf("ended session was updated: main_reduced_coin=%v main_reduced_usdt=%v, want 0/0", coin, usdt)
	}
	if lastAt != nil {
		t.Errorf("last_partial_close_at = %v, want nil (ended session must not be touched)", *lastAt)
	}
}
