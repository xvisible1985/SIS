//go:build integration

package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens confirms the TP-pause half of the
// TPControlEnabled feature: when a top-up takes the hedge from 0 to >0 volume, the MAIN
// strategy's hedge_tp_suppressed column flips to true and Engine.Notify fires (which,
// per the ALREADY-EXISTING engine.go:1157 transition logic, cancels the main's TP — not
// re-tested here, that path is covered by hedge_engine.go's own existing test suite).
func TestAlsPriceTick_SuppressesMainTpWhenHedgeOpens(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tpctl-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-top-tpctl", OrderLinkId: "link-tpctl"}, nil)
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "") // minimal Engine, just so Notify has somewhere to write (no runner registered for mainID is fine — Notify no-ops if it finds nothing to update in-memory, the DB UPDATE already happened separately)
	setWSPosition(ar, "ALSTPCTLUSDT", alsMainPositionIdx(DirectionShort), 10, 100) // main: long, 10 coins @ avg entry 100

	var mainStratID, hedgeStratID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		VALUES ($1,$2,'ALSTPCTLUSDT','long','grid','active') RETURNING id`,
		ownerID, accID,
	).Scan(&mainStratID); err != nil {
		t.Fatalf("create main strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainStratID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, tp_control_enabled)
		VALUES ($1,$2,'ALSTPCTLUSDT','short','als','active',true,true,true) RETURNING id`,
		ownerID, accID,
	).Scan(&hedgeStratID); err != nil {
		t.Fatalf("create hedge strategy: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeStratID) })

	var cycleID, levelID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, hedgeStratID).Scan(&cycleID)
	pool.QueryRow(ctx, `INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status) VALUES ($1,$2,1,'Sell',0,0,'0','pending') RETURNING id`,
		hedgeStratID, cycleID).Scan(&levelID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: hedgeStratID, AccountID: accID, Symbol: "ALSTPCTLUSDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, TPControlEnabled: true,
			AlsThresholds: []AlsThreshold{{TriggerType: 1, TriggerPct: -10, TargetHedgePct: 30}},
		},
		cycle:  &Cycle{ID: cycleID, CycleNum: 1},
		instr:  trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		levels: []GridLevel{{ID: levelID, LevelIdx: 1, Side: "Sell", Status: LevelPending}},
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 85) // crosses the -10% threshold (trigger=90), places the hedge
	sr.mu.Unlock()

	var suppressed bool
	if err := pool.QueryRow(ctx, `SELECT hedge_tp_suppressed FROM strategies WHERE id=$1`, mainStratID).Scan(&suppressed); err != nil {
		t.Fatalf("read main strategy: %v", err)
	}
	if !suppressed {
		t.Error("main strategy's hedge_tp_suppressed = false, want true (hedge just opened)")
	}
}

// TestAlsPriceTick_RestoresMainTpWhenHedgeCloses confirms the resume half: once the
// hedge's volume returns to 0 (e.g. its own stop filled — simulated here by just setting
// the WS-cached position back to 0 directly, since Task 3's stop-fill handling isn't
// this test's concern), the next alsPriceTick call must flip hedge_tp_suppressed back to
// false.
func TestAlsPriceTick_RestoresMainTpWhenHedgeCloses(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	var ownerID, accID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"als-tpctl2-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	ar.pool = pool
	ar.engine = New(pool, "")
	setWSPosition(ar, "ALSTPCTL2USDT", alsMainPositionIdx(DirectionShort), 10, 100)
	// Hedge position already at 0 — nothing open.

	var mainStratID, hedgeStratID string
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, hedge_tp_suppressed) VALUES ($1,$2,'ALSTPCTL2USDT','long','grid','active',true) RETURNING id`,
		ownerID, accID).Scan(&mainStratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainStratID) })
	pool.QueryRow(ctx, `INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, size_as_main, hedge_mode, tp_control_enabled) VALUES ($1,$2,'ALSTPCTL2USDT','short','als','active',true,true,true) RETURNING id`,
		ownerID, accID).Scan(&hedgeStratID)
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeStratID) })

	var cycleID string
	pool.QueryRow(ctx, `INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, hedgeStratID).Scan(&cycleID)

	sr := &StrategyRunner{
		runner: ar,
		strategy: Strategy{
			ID: hedgeStratID, AccountID: accID, Symbol: "ALSTPCTL2USDT", Category: "linear",
			Direction: DirectionShort, HedgeMode: true, SizeAsMain: true, TPControlEnabled: true,
		},
		cycle:        &Cycle{ID: cycleID, CycleNum: 1},
		instr:        trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001, MinQty: 0.001},
		alsHedgeOpen: true, // was open as of the previous tick
	}

	sr.mu.Lock()
	sr.alsPriceTick(ctx, 95) // no pending levels, no crossing — just the open/close check runs
	sr.mu.Unlock()

	var suppressed bool
	if err := pool.QueryRow(ctx, `SELECT hedge_tp_suppressed FROM strategies WHERE id=$1`, mainStratID).Scan(&suppressed); err != nil {
		t.Fatalf("read main strategy: %v", err)
	}
	if suppressed {
		t.Error("main strategy's hedge_tp_suppressed = true, want false (hedge just closed)")
	}
}
