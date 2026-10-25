//go:build integration

package main

import (
	"context"
	"testing"
)

// TestCheckHedgeDeactivation_NoPositions_StopsAlsButKeepsGridWithPendingLevel: with no
// exchange position on the symbol at all, a hedge normally waits only while it has an entry
// order in flight (a pending/placed level). ALS levels are standing thresholds — virtual
// 'pending' rows that sit there for the whole cycle — so they must not count; before the
// fix an ALS hedge whose main closed was never stopped and its card lingered indefinitely
// (ARIAUSDT, 2026-10-25). A grid hedge with a pending entry level must still be kept.
func TestCheckHedgeDeactivation_NoPositions_StopsAlsButKeepsGridWithPendingLevel(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "alsorph")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "alsorph-bot")

	mk := func(symbol, stratType string) string {
		var id, cycleID string
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id, hedge_mode)
			VALUES ($1,$2,$3,'short',$4,'active',$5,true) RETURNING id`,
			userID, accID, symbol, stratType, botID,
		).Scan(&id); err != nil {
			t.Fatalf("insert strategy: %v", err)
		}
		t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, id,
		).Scan(&cycleID); err != nil {
			t.Fatalf("insert cycle: %v", err)
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
			VALUES ($1,$2,1,'Sell',0,0,'0','pending')`, id, cycleID); err != nil {
			t.Fatalf("insert level: %v", err)
		}
		return id
	}
	alsID := mk("ALSORPHAUSDT", "als")
	gridID := mk("ALSORPHBUSDT", "grid")

	cfg := botCfgJSON{Category: "linear", HedgeMode: true, HedgeDeactType: 4}
	s.checkHedgeDeactivation(ctx, botID, accID, cfg, &fakePairedCloseExchange{}, map[string]map[string]hedgePosInfo{})

	status := func(id string) string {
		var st string
		s.pool.QueryRow(ctx, `SELECT status FROM strategies WHERE id=$1`, id).Scan(&st) //nolint:errcheck
		return st
	}
	if got := status(alsID); got != "stopped" {
		t.Errorf("ALS hedge with no positions: status = %q, want stopped", got)
	}
	if got := status(gridID); got != "active" {
		t.Errorf("grid hedge with a pending entry level: status = %q, want active (entry may still fill)", got)
	}
}
