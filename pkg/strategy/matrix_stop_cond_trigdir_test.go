package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestMatrixApplyStopCondSLs_TriggerDirectionMatchesTriggerVsCurrentPrice is the
// regression for the 2026-09-16 ICXUSDT live incident: the stop-cond SL replace order
// hardcoded TriggerDirection from the strategy's long/short direction alone, as if this
// were a plain protective stop placed right at entry (always on the adverse side of a
// price that hasn't moved yet). But this trailing "replace" mechanism computes newTrigger
// from FilledPrice using stop_replace_pct, which can land on either side of currentPrice
// depending on the stop_cond_pct/stop_replace_pct relationship — Bybit rejects a Rising
// trigger whose price is at/below current price (and a Falling trigger above it) with
// retCode=110092. Every stop-cond SL replace for the live short kept failing this way.
//
// Uses stop_replace_pct (3%) LARGER than stop_cond_pct (1.5%) to force newTrigger below
// currentPrice even though the position is short — the scenario the old hardcoded
// direction got backwards. (When replace_pct < cond_pct, as is typical, newTrigger always
// ends up on the side the old hardcoded direction already got right, which is why this
// bug shipped unnoticed until a differently-configured level exposed it live.)
func TestMatrixApplyStopCondSLs_TriggerDirectionMatchesTriggerVsCurrentPrice(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "sl-replace-1"}, nil)

	ar := newTestAccountRunner(t, fake)
	slot1 := 1
	condPct := 1.5
	replacePct := 3.0 // > condPct: forces newTrigger (97.0) below currentPrice (98.5)
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "22222222-3333-4444-5555-666666666666",
			Symbol:    "TRIGDIRUSDT",
			Category:  "linear",
			Direction: DirectionShort,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		runner: ar,
		levels: []GridLevel{
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 100.0, Qty: "10"},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}

	// threshold = 100*(1-1.5/100) = 98.5; currentPrice=98.5 → condMet (currentPrice <= threshold).
	// newTrigger = 100*(1-3/100) = 97.0 — BELOW currentPrice, so the correct direction is
	// Falling (2): price must drop further to reach it. The old hardcoded "short → 1
	// (Rising)" is wrong here.
	sr.matrixApplyStopCondSLs(context.Background(), 98.5)

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.TriggerDirection != 2 {
		t.Errorf("TriggerDirection = %d, want 2 (Falling) — newTrigger=97.0 is below currentPrice=98.5, "+
			"a Rising trigger there is exactly what Bybit rejected live with retCode=110092", req.TriggerDirection)
	}
	if got := placedOrderPrice(t, req.TriggerPrice); got < 96.99 || got > 97.01 {
		t.Errorf("TriggerPrice = %v, want ~97.0", got)
	}
	if req.Side != "Buy" {
		t.Errorf("Side = %q, want %q (short position closes by buying)", req.Side, "Buy")
	}
}

// TestMatrixApplyStopCondSLs_TriggerAboveCurrent_StillUsesRising pins the common case
// (stop_replace_pct < stop_cond_pct, e.g. the ICXUSDT L0 config: 0.5% < 1.5%) still
// resolves to Rising — proving the fix is genuinely dynamic, not just flipped to always
// return Falling.
func TestMatrixApplyStopCondSLs_TriggerAboveCurrent_StillUsesRising(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "sl-replace-2"}, nil)

	ar := newTestAccountRunner(t, fake)
	slot1 := 1
	condPct := 1.5
	replacePct := 0.5 // < condPct: newTrigger (99.5) stays above currentPrice
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "33333333-4444-5555-6666-777777777777",
			Symbol:    "TRIGDIRUSDT2",
			Category:  "linear",
			Direction: DirectionShort,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		runner: ar,
		levels: []GridLevel{
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 100.0, Qty: "10"},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}

	sr.matrixApplyStopCondSLs(context.Background(), 98.5)

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.TriggerDirection != 1 {
		t.Errorf("TriggerDirection = %d, want 1 (Rising) — newTrigger=99.5 is above currentPrice=98.5", req.TriggerDirection)
	}
}
