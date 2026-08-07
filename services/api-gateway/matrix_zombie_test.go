//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

// seedZombieMatrixStrategy creates an active bot matrix strategy whose only cycle ended
// more than the grace period ago (a "zombie" with no active cycle).
func seedZombieMatrixStrategy(t *testing.T, s *Server, botID, accID, ownerID, symbol, dir string) string {
	t.Helper()
	ctx := context.Background()
	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,$3,$4,'matrix','active',$5) RETURNING id`,
		ownerID, accID, symbol, dir, botID,
	).Scan(&stratID); err != nil {
		t.Fatalf("insert strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// A cycle that ended 5 minutes ago (past the 2-minute grace).
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,NOW()-INTERVAL '10 minutes',NOW()-INTERVAL '5 minutes','ghost_close')`,
		stratID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	return stratID
}

func statusOf(t *testing.T, s *Server, id string) string {
	t.Helper()
	var st string
	if err := s.pool.QueryRow(context.Background(), `SELECT status FROM strategies WHERE id=$1`, id).Scan(&st); err != nil {
		t.Fatalf("status: %v", err)
	}
	return st
}

func createZombieBot(t *testing.T, s *Server, ownerID, suffix string) string {
	t.Helper()
	var botID string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO bots (owner_id, name) VALUES ($1,$2) RETURNING id`, ownerID, "zbot-"+suffix,
	).Scan(&botID); err != nil {
		t.Fatalf("insert bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })
	return botID
}

// TestMatrixZombie_StopsWhenPositionGone: a zombie leg with no exchange position is stopped.
func TestMatrixZombie_StopsWhenPositionGone(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "zomb1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "1")
	stratID := seedZombieMatrixStrategy(t, s, botID, accID, userID, "ZMBUSDT", "short")

	// posMap has no ZMBUSDT position → should be stopped.
	s.checkMatrixZombieStrategies(context.Background(), botID, map[string]map[string]hedgePosInfo{})

	if got := statusOf(t, s, stratID); got != "stopped" {
		t.Errorf("zombie status = %q, want stopped", got)
	}
}

// TestMatrixZombie_StopsWhenPositionOpenButNoCycleFills: a zombie leg whose cycle has no
// filled levels AND the exchange shows a position. The position cannot belong to this zombie's
// cycle (a cycle with zero fills never placed any orders), so it comes from another source.
// The correct action is to STOP the zombie — ensureMatrixStrategies will then create a fresh
// replacement that properly adopts the exchange position via posMap (no double-opening).
func TestMatrixZombie_StopsWhenPositionOpenButNoCycleFills(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "zomb2")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "2")
	stratID := seedZombieMatrixStrategy(t, s, botID, accID, userID, "ZMBUSDT", "short")

	// posMap shows an open short (Sell) position, but the zombie's cycle has no filled levels.
	// The position does not belong to this zombie → zombie must be stopped for repair.
	posMap := map[string]map[string]hedgePosInfo{
		"ZMBUSDT": {"Sell": {Symbol: "ZMBUSDT", Side: "Sell", Size: 100}},
	}
	s.checkMatrixZombieStrategies(context.Background(), botID, posMap)

	if got := statusOf(t, s, stratID); got != "stopped" {
		t.Errorf("zombie (no fills) with orphan position status = %q, want stopped", got)
	}
}

// TestMatrixZombie_RevivesSplitBrainWhenPositionOpen: a matrix leg whose latest cycle is
// marked ended (ghost_close) but whose exchange position is still open, with a filled level
// in that cycle, is a split-brain — a false close that left the position live. It must be
// REVIVED (ended_at cleared) rather than stopped, and the phantom ghost_close trade removed.
func TestMatrixZombie_RevivesSplitBrainWhenPositionOpen(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "zomb4")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "4")
	stratID := seedZombieMatrixStrategy(t, s, botID, accID, userID, "ZMBUSDT", "long")
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM strategy_cycles WHERE strategy_id=$1 ORDER BY cycle_num DESC LIMIT 1`, stratID,
	).Scan(&cycleID); err != nil {
		t.Fatalf("cycle id: %v", err)
	}
	// A filled level in the ended cycle — the real, still-open position.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price, slot)
		 VALUES ($1,$2,0,'Buy',1.0,10,'10','filled',1.0,0)`, stratID, cycleID); err != nil {
		t.Fatalf("insert level: %v", err)
	}
	// Phantom ghost_close trade recorded for this cycle — must be removed on revive.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO trade_history (strategy_id, account_id, owner_id, symbol, category, direction, cycle_num, result, opened_at)
		 VALUES ($1,$2,$3,'ZMBUSDT','linear','long',1,'ghost_close',NOW())`, stratID, accID, userID); err != nil {
		t.Fatalf("insert phantom trade: %v", err)
	}

	// Open long (Buy) position exists → split-brain, not a dead zombie.
	posMap := map[string]map[string]hedgePosInfo{
		"ZMBUSDT": {"Buy": {Symbol: "ZMBUSDT", Side: "Buy", Size: 100}},
	}
	s.checkMatrixZombieStrategies(ctx, botID, posMap)

	if got := statusOf(t, s, stratID); got != "active" {
		t.Errorf("split-brain status = %q, want active", got)
	}
	var endedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT ended_at FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt); err != nil {
		t.Fatalf("ended_at: %v", err)
	}
	if endedAt != nil {
		t.Errorf("cycle ended_at = %v, want NULL (revived)", *endedAt)
	}
	var phantom int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE strategy_id=$1 AND result='ghost_close'`, stratID).Scan(&phantom)
	if phantom != 0 {
		t.Errorf("phantom ghost_close trades = %d, want 0", phantom)
	}
}

// TestMatrixZombie_SkipsWithinGrace: a leg whose cycle ended recently (within grace) is
// not stopped — avoids racing a normal in-flight restart.
func TestMatrixZombie_SkipsWithinGrace(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "zomb3")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "3")

	var stratID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id)
		 VALUES ($1,$2,'ZMBUSDT','short','matrix','active',$3) RETURNING id`,
		userID, accID, botID).Scan(&stratID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// Cycle ended 30 seconds ago — within the 2-minute grace.
	s.pool.Exec(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,$2,$3,'tp')`, stratID, time.Now().Add(-5*time.Minute), time.Now().Add(-30*time.Second))

	s.checkMatrixZombieStrategies(ctx, botID, map[string]map[string]hedgePosInfo{})

	if got := statusOf(t, s, stratID); got != "active" {
		t.Errorf("recently-closed leg status = %q, want active (within grace)", got)
	}
}
