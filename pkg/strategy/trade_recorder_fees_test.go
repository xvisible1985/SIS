//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestFeesAndFundingInRange_IgnoresUnreliablePositionIdx: Bybit's execution-list REST
// endpoint (synced into trader_executions by pkg/trader.Syncer) does not reliably report
// positionIdx — observed in production: it is always 0 or NULL, even for accounts that
// genuinely hold simultaneous long+short positions on the same symbol (Bybit hedge mode).
// RecordStrategyTrade's fee/funding lookup used to filter on position_idx = 1/2 (the
// hedge-mode convention), which never matched real rows and silently summed to zero for
// every automatic TP/SL close since — closed_pnl_syncer.go's manual-close fee lookup
// never had this filter and has worked correctly the whole time. This test seeds
// position_idx=0 rows (matching real production data) and asserts they ARE picked up.
func TestFeesAndFundingInRange_IgnoresUnreliablePositionIdx(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"feestest-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	from := time.Now().Add(-1 * time.Hour)
	to := time.Now()
	mid := time.Now().Add(-30 * time.Minute)

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time, position_idx)
		 VALUES
		   ($1,$2,'fee-exec-1','bybit','FEESUSDT','linear','Trade',0.75,$3,0),
		   ($1,$2,'fee-exec-2','bybit','FEESUSDT','linear','Trade',0.30,$3,0),
		   ($1,$2,'fund-exec-1','bybit','FEESUSDT','linear','Funding',0.05,$3,0),
		   ($1,$2,'fee-exec-OTHER','bybit','FEESUSDT','linear','Trade',999.0,$4,0)`,
		ownerID, accID, mid, from.Add(-1*time.Hour)); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	fees, funding := FeesAndFundingInRange(ctx, pool, accID, "FEESUSDT", "irrelev", from, to)

	if fees != 1.05 {
		t.Errorf("fees = %v, want 1.05 (0.75+0.30, excluding the out-of-range 999.0 row)", fees)
	}
	if funding != 0.05 {
		t.Errorf("funding = %v, want 0.05", funding)
	}
}

// TestFeesAndFundingInRange_DifferentSymbolExcluded: the account+symbol filter must still
// apply — a same-account row for a different symbol must not leak into the sum.
func TestFeesAndFundingInRange_DifferentSymbolExcluded(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"feestest2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	from := time.Now().Add(-1 * time.Hour)
	to := time.Now()
	mid := time.Now().Add(-30 * time.Minute)

	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time, position_idx)
		 VALUES ($1,$2,'other-symbol-exec','bybit','OTHERUSDT','linear','Trade',5.0,$3,0)`,
		ownerID, accID, mid); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	fees, funding := FeesAndFundingInRange(ctx, pool, accID, "FEESUSDT", "irrelev", from, to)

	if fees != 0 {
		t.Errorf("fees = %v, want 0 (OTHERUSDT row must not leak into FEESUSDT sum)", fees)
	}
	if funding != 0 {
		t.Errorf("funding = %v, want 0", funding)
	}
}

// TestFeesAndFundingInRange_ExcludesOtherStrategysTaggedOrders is the fix for the
// concurrent-legs case found live (2026-08-14): a matrix/hedge pair's two legs (or two
// unrelated bots) trading the same symbol with overlapping close-time windows used to
// have their Trade fees cross-attributed or double-counted, because the query only
// scoped by account+symbol+time. Every SIS-placed order carries an order_link_id tagged
// "SIS_STR-{stratID8}-...", so a fee definitively tagged as strategy B's must not count
// toward strategy A's close, even though both share account+symbol+time window. Manual
// fills (no SIS_STR tag) stay included — fail-open, unchanged from before.
func TestFeesAndFundingInRange_ExcludesOtherStrategysTaggedOrders(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"feestest3-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	from := time.Now().Add(-1 * time.Hour)
	to := time.Now()
	mid := time.Now().Add(-30 * time.Minute)

	// stratA closes and asks for fees; stratB is the pair's other leg, same symbol,
	// same overlapping window. A manual (untagged) fill is also present.
	if _, err := pool.Exec(ctx,
		`INSERT INTO trader_executions (owner_id, account_id, exec_id, exchange, symbol, category, exec_type, exec_fee, exec_time, position_idx, order_link_id)
		 VALUES
		   ($1,$2,'a-entry','bybit','PAIRUSDT','linear','Trade',0.40,$3,0,'SIS_STR-aaaaaaaa-1-1-1'),
		   ($1,$2,'a-tp','bybit','PAIRUSDT','linear','Trade',0.20,$3,0,'SIS_STR-aaaaaaaa-tp-1-1'),
		   ($1,$2,'b-entry','bybit','PAIRUSDT','linear','Trade',0.55,$3,0,'SIS_STR-bbbbbbbb-1-1-1'),
		   ($1,$2,'b-tp','bybit','PAIRUSDT','linear','Trade',0.35,$3,0,'SIS_STR-bbbbbbbb-tp-1-1'),
		   ($1,$2,'manual-fill','bybit','PAIRUSDT','linear','Trade',0.10,$3,0,NULL)`,
		ownerID, accID, mid); err != nil {
		t.Fatalf("seed trader_executions: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trader_executions WHERE account_id=$1", accID) })

	feesA, _ := FeesAndFundingInRange(ctx, pool, accID, "PAIRUSDT", "aaaaaaaa", from, to)
	if diff := feesA - 0.70; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("stratA fees = %v, want 0.70 (0.40+0.20 own + 0.10 manual, excluding stratB's 0.55+0.35)", feesA)
	}

	feesB, _ := FeesAndFundingInRange(ctx, pool, accID, "PAIRUSDT", "bbbbbbbb", from, to)
	if diff := feesB - 1.00; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("stratB fees = %v, want 1.00 (0.55+0.35 own + 0.10 manual, excluding stratA's 0.40+0.20)", feesB)
	}
}
