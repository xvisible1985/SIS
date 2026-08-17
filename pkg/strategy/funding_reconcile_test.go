//go:build integration

package strategy

import (
	"context"
	"testing"
)

func TestApplyUnappliedFunding_NoActivePair_LeavesUnclaimed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"fundnoact-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'FRUSDT','long','matrix','stopped') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", stratID) })

	var execID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES ($1,$2,'fr-noact-1','bybit','FRUSDT','linear','Funding',0.05,now()) RETURNING id`,
		ownerID, accID).Scan(&execID); err != nil {
		t.Fatalf("seed funding exec: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM trader_executions WHERE id=$1", execID) })

	// No hedge_sessions row at all for FRUSDT — nothing to apply into.
	ApplyUnappliedFunding(ctx, pool, accID, "FRUSDT")

	var applied bool
	if err := pool.QueryRow(ctx, `SELECT applied_to_session FROM trader_executions WHERE id=$1`, execID).Scan(&applied); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if applied {
		t.Error("applied_to_session = true, want false — no active pair exists to apply into")
	}
}

// TestApplyUnappliedFunding_AppliesToActiveSession is the core regression: funding paid
// (positive exec_fee) must reduce accumulated_pnl, funding received (negative) must
// increase it, and every claimed row must be marked applied_to_session=true.
func TestApplyUnappliedFunding_AppliesToActiveSession(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, botID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"fundact-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO bots (owner_id, name) VALUES ($1,'fund-bot') RETURNING id`, ownerID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, bot_id, status)
		 VALUES ($1,$2,'FRUSDT','short','matrix',$3,'active') RETURNING id`,
		ownerID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", stratID) })

	var sessionID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, accumulated_pnl) VALUES ($1,$2,10.0) RETURNING id`,
		botID, stratID).Scan(&sessionID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	// Paid 0.30 (positive) + received 0.10 (negative) → net funding cost 0.20, so
	// accumulated_pnl must move from 10.0 to 9.80.
	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES
		   ($1,$2,'fr-act-paid','bybit','FRUSDT','linear','Funding',0.30,now()),
		   ($1,$2,'fr-act-recv','bybit','FRUSDT','linear','Funding',-0.10,now())`,
		ownerID, accID); err != nil {
		t.Fatalf("seed funding execs: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	ApplyUnappliedFunding(ctx, pool, accID, "FRUSDT")

	var accumulated float64
	if err := pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&accumulated); err != nil {
		t.Fatalf("read back session: %v", err)
	}
	if diff := accumulated - 9.80; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("accumulated_pnl = %v, want 9.80 (10.0 - (0.30-0.10) net funding paid)", accumulated)
	}

	var unclaimed int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM trader_executions WHERE account_id=$1 AND applied_to_session=false`, accID,
	).Scan(&unclaimed); err != nil {
		t.Fatalf("count unclaimed: %v", err)
	}
	if unclaimed != 0 {
		t.Errorf("unclaimed rows = %d, want 0 (both should be marked applied_to_session)", unclaimed)
	}

	// Idempotency: calling again must not re-apply the same rows a second time.
	ApplyUnappliedFunding(ctx, pool, accID, "FRUSDT")
	if err := pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&accumulated); err != nil {
		t.Fatalf("read back session (2nd call): %v", err)
	}
	if diff := accumulated - 9.80; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("accumulated_pnl after 2nd call = %v, want unchanged 9.80 (rows already claimed)", accumulated)
	}
}

// TestApplyUnappliedFunding_IgnoresOtherSymbol: a funding row for a different symbol on
// the same account must not leak into this symbol's accumulated_pnl.
func TestApplyUnappliedFunding_IgnoresOtherSymbol(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, botID, stratID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"fundother-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO bots (owner_id, name) VALUES ($1,'fund-bot2') RETURNING id`, ownerID).Scan(&botID); err != nil {
		t.Fatalf("create bot: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, bot_id, status)
		 VALUES ($1,$2,'FRAUSDT','short','matrix',$3,'active') RETURNING id`,
		ownerID, accID, botID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM strategies WHERE id=$1", stratID) })

	var sessionID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO hedge_sessions (bot_id, hedge_strategy_id, accumulated_pnl) VALUES ($1,$2,5.0) RETURNING id`,
		botID, stratID).Scan(&sessionID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM hedge_sessions WHERE id=$1", sessionID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time)
		 VALUES ($1,$2,'fr-other-sym','bybit','FRBUSDT','linear','Funding',999.0,now())`,
		ownerID, accID); err != nil {
		t.Fatalf("seed funding exec: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	ApplyUnappliedFunding(ctx, pool, accID, "FRAUSDT")

	var accumulated float64
	if err := pool.QueryRow(ctx, `SELECT accumulated_pnl FROM hedge_sessions WHERE id=$1`, sessionID).Scan(&accumulated); err != nil {
		t.Fatalf("read back session: %v", err)
	}
	if diff := accumulated - 5.0; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("accumulated_pnl = %v, want unchanged 5.0 (FRBUSDT's 999.0 must not leak into FRAUSDT)", accumulated)
	}
}
