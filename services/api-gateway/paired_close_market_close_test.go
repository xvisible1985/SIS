//go:build integration

package main

import (
	"context"
	"sync"
	"testing"

	"sis/pkg/trader"
)

// fakePairedCloseExchange wraps fakeReconcileExchange (already a full trader.Exchange
// no-op stub, see closed_pnl_syncer_reconcile_test.go) and records every PlaceOrderREST
// call so tests can assert on them.
type fakePairedCloseExchange struct {
	fakeReconcileExchange
	mu    sync.Mutex
	calls []trader.OrderRequest
}

func (f *fakePairedCloseExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return trader.OrderResult{OrderId: "fake-close-" + req.Symbol}, nil
}

// TestCheckHedgeDeactivation_PairedClose_ClosesBothLegsAtMarket is a regression for the
// 2026-10-22/23 incident: paired close used to only flip strategies.status to 'stopped' and
// rely on an existing TP/SL to eventually close the real exchange position. grid/matrix
// always have a whole-position TP nearby, so this was rarely noticed — ALS hedges have no
// such TP (only per-level protective stops), so the position stayed open, the paired-close
// condition kept re-evaluating true every tick, and "стратегия остановлена (парное
// закрытие)" repeated every ~30s for ~9.5 minutes while PnL drifted (10.24 → 7.735 USDT)
// before the position happened to close on its own. The fix places an immediate reduce-only
// market close for both legs the instant the condition is met, regardless of strategy type.
func TestCheckHedgeDeactivation_PairedClose_ClosesBothLegsAtMarket(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "pcmc1")
	accID := createTestAccount(t, s, userID)
	botID := createZombieBot(t, s, userID, "pcmc-bot")

	var hedgeID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, bot_id, hedge_mode)
		VALUES ($1,$2,'PCMCUSDT','short','als','active',$3,true) RETURNING id`,
		userID, accID, botID,
	).Scan(&hedgeID); err != nil {
		t.Fatalf("insert hedge strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeID) })

	fake := &fakePairedCloseExchange{}
	posMap := map[string]map[string]hedgePosInfo{
		"PCMCUSDT": {
			"Buy":  {Symbol: "PCMCUSDT", Side: "Buy", Size: 100, SizeStr: "100", EntryPrice: 1.0, MarkPrice: 1.0, UnrealisedPnl: 5.0, Leverage: 10},
			"Sell": {Symbol: "PCMCUSDT", Side: "Sell", Size: 50, SizeStr: "50", EntryPrice: 1.0, MarkPrice: 1.0, UnrealisedPnl: 2.0, Leverage: 10},
		},
	}
	// HedgeDeactType=4 ("wait_pair") short-circuits the earlier "deactivation" branch so
	// only the paired-close condition below is exercised. Combined PnL (5.0+2.0=7.0) clears
	// the pnl$ threshold (1.0).
	cfg := botCfgJSON{
		Category: "linear", HedgeMode: true,
		HedgeDeactType: 4, HedgeDeactCloseType: 0, HedgeDeactCloseValue: 1.0,
	}

	s.checkHedgeDeactivation(ctx, botID, accID, cfg, fake, posMap)

	fake.mu.Lock()
	calls := append([]trader.OrderRequest(nil), fake.calls...)
	fake.mu.Unlock()

	if len(calls) != 2 {
		t.Fatalf("PlaceOrderREST calls = %d, want 2 (one per leg): %+v", len(calls), calls)
	}
	bySide := map[string]trader.OrderRequest{}
	for _, c := range calls {
		if c.Symbol != "PCMCUSDT" {
			t.Errorf("call symbol = %q, want PCMCUSDT", c.Symbol)
		}
		if !c.ReduceOnly {
			t.Errorf("call %+v: ReduceOnly = false, want true", c)
		}
		if c.OrderType != "Market" {
			t.Errorf("call %+v: OrderType = %q, want Market", c, c.OrderType)
		}
		bySide[c.Side] = c
	}
	// The Buy (long main) position closes via a Sell order; the Sell (short hedge)
	// position closes via a Buy order.
	if closeMain, ok := bySide["Sell"]; !ok || closeMain.Qty != "100" {
		t.Errorf("missing/wrong close order for the main (Buy) position: %+v", bySide["Sell"])
	}
	if closeHedge, ok := bySide["Buy"]; !ok || closeHedge.Qty != "50" {
		t.Errorf("missing/wrong close order for the hedge (Sell) position: %+v", bySide["Buy"])
	}

	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM strategies WHERE id=$1`, hedgeID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "stopped" {
		t.Errorf("hedge strategy status = %q, want stopped", status)
	}
}
