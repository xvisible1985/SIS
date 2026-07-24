//go:build integration

package main

import (
	"context"
	"testing"
)

// TestBuildPairedCloseWatches_OnePerCompletePair: a bot with one complete pair (both legs
// active, an open hedge_sessions row, both legs present in posMap) produces exactly one
// watch entry, keyed by symbol, with the right strategy IDs, накопление, and leverage.
func TestBuildPairedCloseWatches_OnePerCompletePair(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "watchbuild")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "watchbuild-bot")

	var mainID, hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'WATCHUSDT','long','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("create main: %v", err)
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'WATCHUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("create hedge: %v", err)
	}
	var sessionID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, accumulated_pnl) VALUES ($1,$2,$3,4.5) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	posMap := map[string]map[string]hedgePosInfo{
		"WATCHUSDT": {
			"Buy":  {Symbol: "WATCHUSDT", Side: "Buy", Size: 2.0, EntryPrice: 100.0, Leverage: 10},
			"Sell": {Symbol: "WATCHUSDT", Side: "Sell", Size: 1.0, EntryPrice: 100.0, Leverage: 5},
		},
	}
	cfg := botCfgJSON{HedgeDeactCloseType: 2, HedgeBreakevenProfit: 10.0}

	out := make(map[string]pairedCloseWatchEntry)
	s.buildPairedCloseWatches(ctx, botID, accID, "matrix", cfg, posMap, out)

	entry, ok := out["WATCHUSDT"]
	if !ok {
		t.Fatalf("expected a watch entry for WATCHUSDT, got %v", out)
	}
	if entry.mainID != mainID || entry.hedgeID != hedgeID {
		t.Errorf("entry ids = (%s,%s), want (%s,%s)", entry.mainID, entry.hedgeID, mainID, hedgeID)
	}
	if entry.accumulatedPnl != 4.5 {
		t.Errorf("entry.accumulatedPnl = %v, want 4.5", entry.accumulatedPnl)
	}
	if entry.mainLeverage != 10 || entry.hedgeLeverage != 5 {
		t.Errorf("entry leverage = (%v,%v), want (10,5)", entry.mainLeverage, entry.hedgeLeverage)
	}
}

// TestBuildPairedCloseWatches_SkipsIncompletePair: a symbol with only one leg active (no
// open hedge_sessions row matching both legs, or one leg missing from posMap) produces no
// watch entry — this feature only watches genuinely complete, currently-open pairs.
func TestBuildPairedCloseWatches_SkipsIncompletePair(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "watchskip")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "watchskip-bot")

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'SKIPUSDT','long','matrix','active',$3)`,
		userID, accID, botID); err != nil {
		t.Fatalf("create main: %v", err)
	}
	// No hedge leg, no hedge_sessions row — an orphaned single leg (exactly the AKEUSDT/
	// DEXEUSDT/VELVETUSDT scenario investigated live this session).

	posMap := map[string]map[string]hedgePosInfo{
		"SKIPUSDT": {"Buy": {Symbol: "SKIPUSDT", Side: "Buy", Size: 1.0, EntryPrice: 100.0, Leverage: 10}},
	}
	cfg := botCfgJSON{HedgeDeactCloseType: 0, HedgeDeactCloseValue: 5.0}

	out := make(map[string]pairedCloseWatchEntry)
	s.buildPairedCloseWatches(ctx, botID, accID, "matrix", cfg, posMap, out)

	if _, ok := out["SKIPUSDT"]; ok {
		t.Error("expected no watch entry for an orphaned single leg, got one")
	}
}
