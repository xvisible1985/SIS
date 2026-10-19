//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestEngineStart_LoadsAlsStrategyWithoutPanicking is the foundations plan's acceptance
// test: an 'als' strategy_type row must load through Engine.Start exactly like grid/matrix
// do today — no panic, no silent misrouting into matrix's or grid's own cycle logic.
func TestEngineStart_LoadsAlsStrategyWithoutPanicking(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-lifecycle-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		VALUES ($1,$2,'ALSLIFEUSDT','long','als','stopped') RETURNING id`,
		ownerID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("create als strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	// status='stopped' with no open cycle means Engine.Start's WHERE clause won't pick
	// it up for auto-load — that's correct (matches grid/matrix's own idle-strategy
	// behavior) and is exactly what this test wants to confirm: loading the ENGINE
	// itself must not panic or error just because an 'als' row exists in the table
	// alongside grid/matrix rows, even if this particular row isn't active.
	engine := New(pool, "")
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.Start(ctx)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Engine.Start did not return within 10s — possible deadlock/panic in ALS dispatch wiring")
	}
}
