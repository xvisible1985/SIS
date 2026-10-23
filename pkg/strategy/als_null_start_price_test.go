//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestLoadActiveCycle_TolerateNullStartPrice is the regression for the live incident
// (2026-10-23, DNAUSDT): startAlsCycle's own INSERT never sets strategy_cycles.start_price
// (ALS computes each threshold from the main position's LIVE avg entry, not a cycle-start
// snapshot, so the column is intentionally left NULL there — unlike grid/matrix, which
// always set it). loadActiveCycle scanned it straight into Cycle.StartPrice (a plain
// float64, not a pointer), so the scan failed outright on EVERY restart for an ALS
// strategy. loadOrStart then silently treated that scan error as "no active cycle exists"
// and started a brand-new one — permanently orphaning the already-filled levels (and the
// real, still-open hedge position they tracked) under the old cycle, which was never
// closed. Three consecutive restarts that night produced three separate "live" cycles for
// the same strategy, each abandoning the last.
func TestLoadActiveCycle_TolerateNullStartPrice(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-nullstart-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'bybit','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_mode) VALUES ($1,$2,'ALSNULLSTARTUSDT','short','als','active',true) RETURNING id`,
		ownerID, accID).Scan(&stratID); err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })
	// Mirrors startAlsCycle's own INSERT exactly: no start_price column at all, so it
	// stays NULL — this is the real, live shape of every ALS cycle row, not a contrived one.
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, filled_price, exchange_order_id)
		VALUES ($1,$2,1,'Sell',0,0,'109','filled',0.1832,'real-exchange-order-id') RETURNING id`,
		stratID, cycleID).Scan(&levelID); err != nil {
		t.Fatalf("create level: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_levels WHERE id=$1", levelID) })

	ar := &AccountRunner{
		pool:                pool,
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posAvgEntrySize:     make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
	}
	sr := &StrategyRunner{
		strategy: Strategy{ID: stratID, Symbol: "ALSNULLSTARTUSDT", Direction: DirectionShort, StrategyType: "als", HedgeMode: true},
		runner:   ar,
	}

	if err := sr.loadActiveCycle(ctx); err != nil {
		t.Fatalf("loadActiveCycle returned an error on a real ALS cycle row (NULL start_price): %v — "+
			"this is exactly the scan failure that made loadOrStart silently start a duplicate cycle on every restart", err)
	}

	if sr.cycle == nil || sr.cycle.ID != cycleID {
		t.Fatalf("sr.cycle not loaded correctly: %+v", sr.cycle)
	}
	if sr.cycle.StartPrice != 0 {
		t.Errorf("sr.cycle.StartPrice = %v, want 0 (NULL column, no sensible default to fabricate)", sr.cycle.StartPrice)
	}
	if len(sr.levels) != 1 || sr.levels[0].Status != LevelFilled || sr.levels[0].ExchangeOrderID != "real-exchange-order-id" {
		t.Errorf("the real filled level (tracking an open position) was not loaded: %+v", sr.levels)
	}
}
