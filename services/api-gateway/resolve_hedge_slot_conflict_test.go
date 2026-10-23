//go:build integration

package main

import (
	"context"
	"testing"
)

// TestResolveHedgeSlotConflict_SameBotDetachedRowIsAConflict is a regression for the
// 2026-10-22 live incident: a just-TP'd ALS hedge gets detached by releaseHedgeToGrid
// (bot_id set to NULL) but stays active/finishing, still occupying the exchange's
// (symbol,direction) slot. The checks that run before resolveHedgeSlotConflict in
// checkHedgeActivation are all scoped to bot_id = this bot (or a bot_kind='hedge' join),
// so none of them see a bot_id=NULL row — resolveHedgeSlotConflict was the only line of
// defense, and it used to EXCLUDE same-bot rows (bot_id <> $4), which doesn't matter for
// a NULL bot_id row, but the real-world failure was a race where the conflicting row still
// had THIS bot's own bot_id at the moment this query ran (the detach hadn't committed/become
// visible yet). The fix drops the bot-ownership exclusion entirely, matching matrix's own
// slot check (directionHasLiveStrategy, matrix_engine.go) which never special-cases
// same-bot ownership either — any live row in the exact slot is a conflict, full stop.
func TestResolveHedgeSlotConflict_SameBotDetachedRowIsAConflict(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	var ownerID, accID, botID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"hedge-slot-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO bots (owner_id, name, account_id) VALUES ($1,'x',$2) RETURNING id`,
		ownerID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })

	// Simulate the exact post-releaseHedgeToGrid state: still active/finishing, same bot_id
	// as the one about to re-activate a new ALS hedge for this slot (the race window), or
	// already NULL (the steady-state after the race resolves) — both must be caught.
	cases := []struct {
		name      string
		caseBotID *string
	}{
		{"bot_id_same_as_caller", &botID},
		{"bot_id_null", nil},
	}
	for _, tc := range cases {
		caseBotID := tc.caseBotID
		t.Run(tc.name, func(t *testing.T) {
			var stratID string
			if err := s.pool.QueryRow(ctx, `
				INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode, bot_id)
				VALUES ($1,$2,'BTRUSDT','short','grid','active',true,$3) RETURNING id`,
				ownerID, accID, caseBotID,
			).Scan(&stratID); err != nil {
				t.Fatalf("create conflicting strategy: %v", err)
			}
			t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

			cfg := botCfgJSON{HedgeCloseType: 0} // "wait for cycle end" — doesn't touch ex/posMap
			_, ok := s.resolveHedgeSlotConflict(ctx, botID, accID, "BTRUSDT", "short", cfg, nil, nil)
			if ok {
				t.Errorf("resolveHedgeSlotConflict ok=true, want false — a live strategy already occupies this (account,symbol,direction) slot")
			}
		})
	}
}

// TestResolveHedgeSlotConflict_NoConflictWhenSlotIsFree confirms the happy path still
// works: an empty slot (no active/finishing strategy for this account+symbol+direction)
// must report ok=true so a legitimate new hedge can be created.
func TestResolveHedgeSlotConflict_NoConflictWhenSlotIsFree(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	var ownerID, accID, botID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"hedge-slot-free-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO bots (owner_id, name, account_id) VALUES ($1,'x',$2) RETURNING id`,
		ownerID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })

	cfg := botCfgJSON{HedgeCloseType: 0}
	_, ok := s.resolveHedgeSlotConflict(ctx, botID, accID, "BTRUSDT", "short", cfg, nil, nil)
	if !ok {
		t.Errorf("resolveHedgeSlotConflict ok=false, want true — no strategy occupies this slot")
	}
}
