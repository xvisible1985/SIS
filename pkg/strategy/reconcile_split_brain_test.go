//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestReconcile_RevivesSplitBrainCycle is the regression for the live incident (2026-09-24,
// MUBARAKUSDT/pol account): services/api-gateway's closed_pnl_syncer force-closed a cycle
// directly in strategy_cycles (a "false ghost close") while this account's live StrategyRunner
// was still actively managing it — closeCycle/maybeRestart were never invoked, since the DB
// write never went through the runner's own locked code path. The runner kept running against
// a cycle the DB considered ended, and reconcile's periodic sweep must notice the mismatch and
// revive it (clear ended_at/result, drop the phantom ghost_close trade_history row) — covering
// every live runner regardless of strategy_type or bot attachment, unlike api-gateway's
// checkMatrixZombieStrategies which only scans bot-attached strategy_type='matrix' rows.
func TestReconcile_RevivesSplitBrainCycle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"splitbrain-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, relative_slots) VALUES ($1,$2,'MUBARAKUSDT','short','grid','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// Simulate the external syncer's force-close: ended_at/result set directly, bypassing closeCycle.
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price, started_at, ended_at, result) VALUES ($1,1,0.05,NOW()-INTERVAL '1 hour',NOW(),'ghost_close') RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot)
		VALUES ($1,$2,1,'Sell',0.05,20,'400','filled',1) RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_levels WHERE id=$1", levelID) })
	// The phantom trade recorded for the false close — must be deleted on revival.
	if _, err := pool.Exec(ctx, `
		INSERT INTO trade_history (account_id, owner_id, strategy_id, symbol, category, direction, cycle_num, result, opened_at, closed_at, net_pnl)
		VALUES ($1,$2,$3,'MUBARAKUSDT','linear','short',1,'ghost_close',NOW(),NOW(),0)`,
		accID, ownerID, stratID); err != nil {
		t.Fatalf("seed phantom trade_history: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM trade_history WHERE strategy_id=$1", stratID) })

	// exchange must be non-nil: reconcile() now calls ar.exchange.FetchOpenOrders
	// (Plan #4d), which panics on a nil interface value.
	ar := &AccountRunner{
		accountID:           accID,
		pool:                pool,
		exchange:            &fakeExchange{},
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posAvgEntrySize:     make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
	sr := &StrategyRunner{
		strategy: Strategy{ID: stratID, AccountID: accID, Symbol: "MUBARAKUSDT", Category: "linear", Direction: DirectionShort, StrategyType: "grid"},
		runner:   ar,
		cycle:    &Cycle{ID: cycleID, StrategyID: stratID, CycleNum: 1},
	}
	ar.strategies[stratID] = sr

	ar.reconcile(ctx)

	var endedAt *time.Time
	var result *string
	if err := pool.QueryRow(ctx, `SELECT ended_at, result FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt, &result); err != nil {
		t.Fatalf("read back cycle: %v", err)
	}
	if endedAt != nil {
		t.Errorf("ended_at = %v, want NULL — reconcile must revive the externally-force-closed cycle", *endedAt)
	}
	if result != nil {
		t.Errorf("result = %v, want NULL", *result)
	}

	var phantomCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trade_history WHERE strategy_id=$1 AND result='ghost_close'`, stratID).Scan(&phantomCount); err != nil {
		t.Fatalf("count phantom trades: %v", err)
	}
	if phantomCount != 0 {
		t.Errorf("phantom ghost_close trade_history rows = %d, want 0 (must be deleted on revival)", phantomCount)
	}
}

// TestReconcile_DoesNotReviveGenuinelyEndedCycle is the negative control: a cycle with no
// filled levels (nothing worth preserving) must NOT be revived — this is the same "no filled
// levels" exit api-gateway's reviveMatrixSplitBrain uses to decide there's no state to save,
// so reconcile's version of the check must honor it too instead of reviving every ended cycle
// a live runner happens to still be holding a reference to.
func TestReconcile_DoesNotReviveGenuinelyEndedCycle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"splitbrain2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status) VALUES ($1,$2,'NOFILLUSDT','short','grid','stopped') RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price, started_at, ended_at, result) VALUES ($1,1,0.05,NOW()-INTERVAL '1 hour',NOW(),'ghost_close') RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })

	// exchange must be non-nil: reconcile() now calls ar.exchange.FetchOpenOrders
	// (Plan #4d), which panics on a nil interface value.
	ar := &AccountRunner{
		accountID:           accID,
		pool:                pool,
		exchange:            &fakeExchange{},
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posAvgEntrySize:     make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
	sr := &StrategyRunner{
		strategy: Strategy{ID: stratID, AccountID: accID, Symbol: "NOFILLUSDT", Category: "linear", Direction: DirectionShort, StrategyType: "grid"},
		runner:   ar,
		cycle:    &Cycle{ID: cycleID, StrategyID: stratID, CycleNum: 1},
	}
	ar.strategies[stratID] = sr

	ar.reconcile(ctx)

	var endedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT ended_at FROM strategy_cycles WHERE id=$1`, cycleID).Scan(&endedAt); err != nil {
		t.Fatalf("read back cycle: %v", err)
	}
	if endedAt == nil {
		t.Error("ended_at = NULL, want still set — a cycle with no filled levels must not be revived")
	}
}
