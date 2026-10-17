//go:build integration

package strategy

import (
	"context"
	"testing"
	"time"
)

// TestLoadActiveCycle_RestoresMatrixSLSeqFromSavedLinkId is the regression for the live
// incident (2026-09-24, MUBARAKUSDT/pol account): matrixSLSeq was never restored on process
// restart, so the first matrixApplyStopCondSLs/matrixPlacePerLevelSL call after a restart
// regenerated a per-level-SL orderLinkId ("SIS_STR-<id8>-msl-<slot>-<seq>") that Bybit had
// already seen, failing with retCode=110072 "OrderLinkedID is duplicate" — and because the
// old SL order had already been cancelled before the failed replace attempt, the level was
// left with no working stop-loss while repeated per-tick retries left orphaned order
// artifacts behind. loadActiveCycle must restore matrixSLSeq from the saved sl_link_id the
// same way it already restores repriceGen from exchange_link_id.
func TestLoadActiveCycle_RestoresMatrixSLSeqFromSavedLinkId(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID, stratID, cycleID, levelID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"slseq-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
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
	if err := pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price, started_at) VALUES ($1,1,0.05,NOW()) RETURNING id`,
		stratID).Scan(&cycleID); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategy_cycles WHERE id=$1", cycleID) })
	// sl_link_id's trailing "-7" is what loadActiveCycle must parse and restore into matrixSLSeq.
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot, sl_order_id, sl_link_id)
		VALUES ($1,$2,1,'Sell',0.05,20,'400','filled',1,'bybit-order-xyz','SIS_STR-`+stratID[:8]+`-msl-L1-7')
		RETURNING id`,
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
		strategy: Strategy{ID: stratID, Symbol: "MUBARAKUSDT", Direction: DirectionShort, StrategyType: "grid"},
		runner:   ar,
	}

	if err := sr.loadActiveCycle(ctx); err != nil {
		t.Fatalf("loadActiveCycle: %v", err)
	}

	if sr.matrixSLSeq != 7 {
		t.Errorf("matrixSLSeq = %d, want 7 (restored from sl_link_id's saved sequence number)", sr.matrixSLSeq)
	}
	if len(sr.levels) != 1 || sr.levels[0].SLLinkID != "SIS_STR-"+stratID[:8]+"-msl-L1-7" {
		t.Errorf("levels[0].SLLinkID not loaded correctly: %+v", sr.levels)
	}
}
