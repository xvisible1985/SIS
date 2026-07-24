//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestAccumulateHedgeSessionPnl_HookFiresOnSuccess: an optional package-level hook, when
// registered, fires after a successful accumulate with the exact strategy ID and netPnl
// that were passed in. Used by services/api-gateway to react to накопление changes in
// near-real-time — see docs/superpowers/specs/2026-07-23-paired-close-realtime-design.md.
func TestAccumulateHedgeSessionPnl_HookFiresOnSuccess(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, mainID, hedgeID, sessionID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"acchook-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	var botID string
	if err := pool.QueryRow(ctx, `INSERT INTO bots (owner_id, account_id, name, status, strategy_config) VALUES ($1,$2,'x','active','{}'::jsonb) RETURNING id`,
		ownerID, accID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM bots WHERE id=$1", botID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,'HOOKUSDT','long','matrix','active',$3) RETURNING id`,
		ownerID, accID, botID).Scan(&mainID); err != nil {
		t.Fatalf("create main strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id) VALUES ($1,$2,'HOOKUSDT','short','matrix','active',$3) RETURNING id`,
		ownerID, accID, botID).Scan(&hedgeID); err != nil {
		t.Fatalf("create hedge strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeID) })
	if err := pool.QueryRow(ctx, `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id) VALUES ($1,$2,$3) RETURNING id`,
		botID, mainID, hedgeID).Scan(&sessionID); err != nil {
		t.Fatalf("create hedge_sessions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	var calls int
	var gotStratID string
	var gotNetPnl float64
	OnAccumulate = func(stratID string, netPnl float64) {
		calls++
		gotStratID = stratID
		gotNetPnl = netPnl
	}
	t.Cleanup(func() { OnAccumulate = nil })

	AccumulateHedgeSessionPnl(ctx, pool, mainID, 7.5)

	if calls != 1 {
		t.Fatalf("hook called %d times, want 1", calls)
	}
	if gotStratID != mainID {
		t.Errorf("hook stratID = %q, want %q", gotStratID, mainID)
	}
	if gotNetPnl != 7.5 {
		t.Errorf("hook netPnl = %v, want 7.5", gotNetPnl)
	}
}
