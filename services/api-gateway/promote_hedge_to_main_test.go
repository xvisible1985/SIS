//go:build integration

package main

import (
	"context"
	"testing"
)

// seedFlipFixture creates a bot_kind="hedge", hedge_tp_main_mode="flip" bot, a stopped
// "old main" strategy, and an active hedge strategy (hedged_strategy_id -> old main) with
// one filled level in an open cycle — the minimum state handleMainTpFlip needs to act.
// Returns botID, oldMainID, hedgeID.
func seedFlipFixture(t *testing.T, s *Server, userID, accID, symbol, hedgeDir string) (botID, oldMainID, hedgeID string) {
	t.Helper()
	ctx := context.Background()

	cfgJSON := `{"bot_kind":"hedge","hedge_tp_main_mode":"flip","strategy_type":"grid","grid_size_usdt":20,"sl_type":"conditional","sl_pct":-5}`
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO bots (owner_id, name, account_id, status, strategy_config)
		 VALUES ($1,'flip-bot','`+accID+`','active',$2::jsonb) RETURNING id`,
		userID, cfgJSON,
	).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })

	mainDir := "long"
	if hedgeDir == "long" {
		mainDir = "short"
	}
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,$3,$4,'grid','stopped',$5) RETURNING id`,
		userID, accID, symbol, mainDir, botID,
	).Scan(&oldMainID); err != nil {
		t.Fatalf("insert old main: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", oldMainID) })

	slPct := -5.0
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id, hedged_strategy_id, sl_type, sl_pct)
		 VALUES ($1,$2,$3,$4,'grid','active',$5,$6,'conditional',$7) RETURNING id`,
		userID, accID, symbol, hedgeDir, botID, oldMainID, slPct,
	).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", hedgeID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		hedgeID,
	).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	side := "Sell"
	if hedgeDir == "long" {
		side = "Buy"
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		 VALUES ($1,$2,1,$3,0,20,'400','filled',0.05)`,
		hedgeID, cycleID, side,
	); err != nil {
		t.Fatalf("insert filled level: %v", err)
	}

	return botID, oldMainID, hedgeID
}

// TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId is the regression test for
// the live incident (2026-10-25/26, ALS-Bot, APRUSDT): handleMainTpFlip used to detach the
// hedge (bot_id=NULL, strategy_type forced to 'grid', sl_pct=0) via releaseHedgeToGrid. It
// must now promote the hedge in place via promoteHedgeToMain instead — bot_id, strategy_type
// and sl_pct stay exactly as they were, only hedged_strategy_id is cleared — and a new hedge
// must exist immediately (not gated on any drawdown threshold), linked back to the promoted
// strategy via its own hedged_strategy_id.
func TestHandleMainTpFlip_PromotesHedgeInPlace_KeepsConfigAndBotId(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "flip1")
	accID := createTestAccount(t, s, userID)
	stubBybitPublicInstrumentInfoOK(t)

	botID, oldMainID, hedgeID := seedFlipFixture(t, s, userID, accID, "FLPAUSDT", "short")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE symbol='FLPAUSDT' AND bot_id=$1", botID)
	})

	s.handleMainTpFlip(context.Background(), oldMainID)

	var botIDAfter, stratType string
	var slPct *float64
	var hedgedStratID *string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT COALESCE(bot_id::text,''), strategy_type, sl_pct, hedged_strategy_id::text
		 FROM strategies WHERE id=$1`, hedgeID,
	).Scan(&botIDAfter, &stratType, &slPct, &hedgedStratID); err != nil {
		t.Fatalf("read promoted strategy: %v", err)
	}
	if botIDAfter != botID {
		t.Errorf("bot_id = %q, want %q — promoted leg must stay attached to the bot", botIDAfter, botID)
	}
	if stratType != "grid" {
		t.Errorf("strategy_type = %q, want unchanged %q", stratType, "grid")
	}
	if slPct == nil || *slPct != -5.0 {
		t.Errorf("sl_pct = %v, want unchanged -5.0 — promotion must not strip SL protection", slPct)
	}
	if hedgedStratID != nil {
		t.Errorf("hedged_strategy_id = %v, want NULL — promoted leg is now the main, not anyone's hedge", *hedgedStratID)
	}

	var oldMainExists bool
	s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, oldMainID).Scan(&oldMainExists) //nolint:errcheck
	if oldMainExists {
		t.Error("old main strategy still exists — must be deleted after promotion, same as releaseHedgeToGrid")
	}

	var newHedgeID, newHedgeDir string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id, direction FROM strategies WHERE hedged_strategy_id=$1 AND status IN ('active','finishing')`,
		hedgeID,
	).Scan(&newHedgeID, &newHedgeDir); err != nil {
		t.Fatalf("no new hedge found linked to the promoted strategy: %v", err)
	}
	if newHedgeDir != "long" {
		t.Errorf("new hedge direction = %q, want %q (opposite of the promoted short leg)", newHedgeDir, "long")
	}
}

// TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot reproduces the live blocking
// incident directly: two flips in a row on the same symbol must never leave two active
// rows occupying the same (account, symbol, direction) slot — each flip reuses the
// existing row in place instead of leaving a parallel orphan behind.
func TestHandleMainTpFlip_ChainedFlips_NeverTwoRowsInSameSlot(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "flip2")
	accID := createTestAccount(t, s, userID)
	stubBybitPublicInstrumentInfoOK(t)

	botID, oldMainID, hedgeAID := seedFlipFixture(t, s, userID, accID, "FLPBUSDT", "short")
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM strategies WHERE symbol='FLPBUSDT' AND bot_id=$1", botID)
	})

	// Flip 1: oldMain (long) closes by TP -> hedgeA (short) promoted to main, hedgeB (long) created.
	s.handleMainTpFlip(context.Background(), oldMainID)

	countActive := func(symbol, direction string) int {
		var n int
		s.pool.QueryRow(context.Background(), //nolint:errcheck
			`SELECT COUNT(*) FROM strategies WHERE symbol=$1 AND direction=$2 AND status IN ('active','finishing')`,
			symbol, direction,
		).Scan(&n)
		return n
	}
	if n := countActive("FLPBUSDT", "short"); n != 1 {
		t.Fatalf("after flip 1: %d active short rows, want 1", n)
	}
	if n := countActive("FLPBUSDT", "long"); n != 1 {
		t.Fatalf("after flip 1: %d active long rows, want 1", n)
	}

	var hedgeBID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT id FROM strategies WHERE symbol='FLPBUSDT' AND direction='long' AND status IN ('active','finishing')`,
	).Scan(&hedgeBID); err != nil {
		t.Fatalf("find hedgeB: %v", err)
	}

	// Give hedgeB a filled level too, so it qualifies for its own flip.
	var cycleBID string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		hedgeBID,
	).Scan(&cycleBID); err != nil {
		t.Fatalf("insert hedgeB cycle: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price)
		 VALUES ($1,$2,1,'Buy',0,20,'400','filled',0.05)`,
		hedgeBID, cycleBID,
	); err != nil {
		t.Fatalf("insert hedgeB filled level: %v", err)
	}
	// hedgeA (now promoted to main, short) must be 'stopped' for flip 2's cleanup DELETE to fire —
	// simulates hedgeA's own cycle later closing via TP, same as any main before a flip.
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE strategies SET status='stopped' WHERE id=$1`, hedgeAID,
	); err != nil {
		t.Fatalf("stop hedgeA: %v", err)
	}

	// Flip 2: hedgeA (short, now main) closes by TP -> hedgeB (long) promoted to main,
	// a new short hedge (hedgeC) created. hedgeA must be deleted.
	s.handleMainTpFlip(context.Background(), hedgeAID)

	if n := countActive("FLPBUSDT", "short"); n != 1 {
		t.Errorf("after flip 2: %d active short rows, want 1 (no orphan accumulation)", n)
	}
	if n := countActive("FLPBUSDT", "long"); n != 1 {
		t.Errorf("after flip 2: %d active long rows, want 1", n)
	}
	var hedgeAExists bool
	s.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM strategies WHERE id=$1)`, hedgeAID).Scan(&hedgeAExists) //nolint:errcheck
	if hedgeAExists {
		t.Error("hedgeA (old main after flip 2) still exists — must be deleted")
	}
}
