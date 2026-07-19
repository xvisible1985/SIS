//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// TestNotifyExpectedClose_NoPanicForLoadedStrategy is a wiring smoke test only — it does
// NOT verify the resulting trade_history.result label (see the note at the end of this
// test for why that's out of reach here). It exercises the same call path
// checkMatrixPairedClose/stopMatrixPair use: load a strategy into the engine's in-memory
// runner, then call NotifyExpectedClose against it, catching nil-pointer/map-key
// mismatches between stopMatrixPair's args and Notify's in-memory keys. The actual
// TTL/label decision is covered in isolation by TestResolveCloseResult.
func TestNotifyExpectedClose_NoPanicForLoadedStrategy(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "expclose")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "expclose")

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, bot_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,$3,'EXPCUSDT','long','matrix','active') RETURNING id`,
		userID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// Load the strategy into the engine's in-memory runner (same path Notify uses).
	s.engine.Notify(ctx, stratID)
	time.Sleep(200 * time.Millisecond) // let addStrategy's goroutine register the runner

	s.engine.NotifyExpectedClose(stratID, accID, "paired_close")

	// Can't reach unexported StrategyRunner fields from this package — verify indirectly
	// isn't possible without exposing test hooks. Assert instead that NotifyExpectedClose
	// did not panic/error for a loaded strategy (smoke test for the wiring); the pure
	// resolveCloseResult unit test (Task 5, Step 1) covers the TTL/label logic itself.
	// If a lower-level hook becomes available later, extend this test to close the
	// position and assert trade_history.result = 'paired_close' end-to-end.
}
