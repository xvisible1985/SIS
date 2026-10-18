//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// seedEndedCycle inserts an ended strategy_cycles row (result='tp') for stratID and returns
// its cycle_num.
func seedEndedCycle(t *testing.T, s *Server, stratID string) int {
	t.Helper()
	const cycleNum = 1
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,$2,NOW()-INTERVAL '10 minutes',NOW()-INTERVAL '6 minutes','tp')`,
		stratID, cycleNum,
	); err != nil {
		t.Fatalf("seed ended cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategy_cycles WHERE strategy_id=$1", stratID) })
	return cycleNum
}

// seedTradeHistoryForCycle inserts a trade_history row for (stratID, cycleNum), is_partial
// as given — mirrors the minimal set of columns the real recorders always populate.
func seedTradeHistoryForCycle(t *testing.T, s *Server, ownerID, accID, stratID string, cycleNum int, isPartial bool) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `
		INSERT INTO trade_history (
			strategy_id, account_id, owner_id, symbol, category, direction,
			cycle_num, result, source, is_partial,
			avg_entry, exit_price, qty, volume_usdt, pnl, pnl_pct, opened_at, closed_at,
			fees, funding, net_pnl
		) VALUES (
			$1,$2,$3,'ICXUSDT','linear','short',
			$4,'tp','strategy',$5,
			1,1,1,1,1,1,NOW(),NOW(),
			0,0,1
		)`,
		stratID, accID, ownerID, cycleNum, isPartial,
	); err != nil {
		t.Fatalf("seed trade_history: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })
}

func countStrategyRows(t *testing.T, s *Server, stratID string) int {
	t.Helper()
	var count int
	if err := s.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM strategies WHERE id=$1`, stratID).Scan(&count); err != nil {
		t.Fatalf("query strategy: %v", err)
	}
	return count
}

// TestCleanupStoppedStrategies_EndedCycleMissingTradeHistory_WaitsWithoutDeleting
// reproduces the bug found via a full account audit (2026-10-18): a strategy whose position
// just closed (TP/SL) was being deleted immediately, racing ahead of its own
// RecordStrategyTrade write. Once the strategies row is gone, strategy_id/bot_id can never
// be recovered for that close — it permanently falls back to an unattributed
// source='manual' trade_history row. The strategy must stay until its last ended cycle has
// a matching (non-partial) trade_history row.
func TestCleanupStoppedStrategies_EndedCycleMissingTradeHistory_WaitsWithoutDeleting(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanhistgap")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanhistgap")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")
	seedEndedCycle(t, s, stratID)
	// No trade_history row seeded — this is the gap.

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{} // exchange reports the position is already closed

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	if count := countStrategyRows(t, s, stratID); count != 1 {
		t.Errorf("strategy row count = %d, want 1 — must NOT be deleted while its last cycle has no trade_history row", count)
	}
}

// TestCleanupStoppedStrategies_TradeHistoryGapPastGracePeriod_NeverDeletes mirrors
// TestCleanupStoppedStrategies_PositionStillOpenPastGracePeriod_NeverDeletes for the new
// guard: even once the grace period has fully elapsed, a strategy with an unrecorded ended
// cycle must still never be force-deleted — only escalated to an error-level alert for a
// human to investigate.
func TestCleanupStoppedStrategies_TradeHistoryGapPastGracePeriod_NeverDeletes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanhistgappast")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanhistgappast")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")
	seedEndedCycle(t, s, stratID)

	s.cleanupWaiters.Store(stratID, time.Now().Add(-cleanupStoppedStrategiesMaxWait-time.Hour))

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{}

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	if count := countStrategyRows(t, s, stratID); count != 1 {
		t.Errorf("strategy row count = %d, want 1 — a missing trade_history row must never be worked around by force-deleting", count)
	}
}

// TestCleanupStoppedStrategies_EndedCycleHasOnlyPartialTradeHistory_StillWaits: a partial
// row (e.g. one of the matrix per-level SL closes this same fix now records, is_partial=true)
// does NOT satisfy "this cycle's close was recorded" — only the terminal (non-partial) row
// does. Without this distinction, a matrix strategy with at least one recorded partial close
// could get deleted before its actual cycle-ending TP/SL close is ever written.
func TestCleanupStoppedStrategies_EndedCycleHasOnlyPartialTradeHistory_StillWaits(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanhistpartial")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanhistpartial")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")
	cycleNum := seedEndedCycle(t, s, stratID)
	seedTradeHistoryForCycle(t, s, userID, accID, stratID, cycleNum, true) // partial only

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{}

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	if count := countStrategyRows(t, s, stratID); count != 1 {
		t.Errorf("strategy row count = %d, want 1 — a partial-only row must not count as the cycle's recorded terminal close", count)
	}
}

// TestCleanupStoppedStrategies_EndedCycleHasTradeHistory_Deletes verifies the legitimate
// case still works once the terminal close is actually recorded: the strategy is deleted
// exactly as before this fix.
func TestCleanupStoppedStrategies_EndedCycleHasTradeHistory_Deletes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "cleanhistok")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "cleanhistok")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE bot_id=$1", botID) })

	stratID := seedStoppedStrategy(t, s, userID, accID, botID, "ICXUSDT", "short")
	cycleNum := seedEndedCycle(t, s, stratID)
	seedTradeHistoryForCycle(t, s, userID, accID, stratID, cycleNum, false) // terminal row present

	b := botEngineRow{id: botID, ownerID: userID, accountID: accID}
	stopped := []stoppedStrategy{{id: stratID, symbol: "ICXUSDT", category: "linear", direction: "short"}}
	openPositions := map[string]bool{}

	s.cleanupStoppedStrategies(ctx, b, stopped, openPositions)

	if count := countStrategyRows(t, s, stratID); count != 0 {
		t.Errorf("strategy row count = %d, want 0 — once the cycle's close is recorded, cleanup must proceed as before", count)
	}
}
