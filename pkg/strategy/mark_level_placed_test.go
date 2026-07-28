//go:build integration

package strategy

import (
	"context"
	"testing"
)

// TestMarkLevelPlaced_WritesPlacedAtToDB: matrixTriggerVirtualLevel and
// matrixPlaceRelativeVirtualOrder place a real Market order and then update the level's
// status, but historically only updated the in-memory GridLevel — never the DB row —
// leaving strategy_levels.placed_at permanently NULL for every virtual (matrix
// above/below market-triggered) level. markLevelPlaced is the shared helper that fixes
// this: it must update both the in-memory level and the DB row identically to what
// placeMatrixLevel already does for resting limit orders.
func TestMarkLevelPlaced_WritesPlacedAtToDB(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"mlp-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'MLPUSDT','long','matrix','active') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
		 VALUES ($1,$2,1,'Buy',1.0,10,'10','pending') RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}

	sr := &StrategyRunner{runner: &AccountRunner{pool: pool}}
	l := &GridLevel{ID: levelID, LevelIdx: 1, Side: "Buy", Status: LevelPending}

	sr.markLevelPlaced(ctx, l, "order-xyz-1", "link-xyz-1")

	if l.Status != LevelPlaced {
		t.Errorf("in-memory Status = %q, want %q", l.Status, LevelPlaced)
	}
	if l.ExchangeOrderID != "order-xyz-1" {
		t.Errorf("in-memory ExchangeOrderID = %q, want %q", l.ExchangeOrderID, "order-xyz-1")
	}
	if l.PlacedAt.IsZero() {
		t.Error("in-memory PlacedAt is zero, want set")
	}

	var status, orderID, linkID string
	var placedAtSet bool
	if err := pool.QueryRow(ctx,
		`SELECT status, exchange_order_id, exchange_link_id, placed_at IS NOT NULL FROM strategy_levels WHERE id=$1`,
		levelID,
	).Scan(&status, &orderID, &linkID, &placedAtSet); err != nil {
		t.Fatalf("query level: %v", err)
	}
	if status != "placed" {
		t.Errorf("DB status = %q, want %q", status, "placed")
	}
	if orderID != "order-xyz-1" {
		t.Errorf("DB exchange_order_id = %q, want %q", orderID, "order-xyz-1")
	}
	if linkID != "link-xyz-1" {
		t.Errorf("DB exchange_link_id = %q, want %q", linkID, "link-xyz-1")
	}
	if !placedAtSet {
		t.Error("DB placed_at is NULL, want set (this is the bug being fixed)")
	}
}
