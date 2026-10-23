package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// TestAlsPlaceLevelStop_TriggerDirectionMatchesTriggerVsCurrentPrice is the regression
// for the 2026-10-23 live incident (BIRBUSDT, ALTUSDT): alsPlaceLevelStop computed
// TriggerDirection by comparing the new trigger against the level's stale FilledPrice
// instead of the live current price, same bug class as
// TestMatrixApplyStopCondSLs_TriggerDirectionMatchesTriggerVsCurrentPrice fixed earlier
// for matrix.go. Bybit rejected the resulting conditional order with
// retCode=110092/110093 ("expect Rising/Falling, but trigger_price <=/>= current") once
// price had moved meaningfully past FilledPrice by the time the stop-cond-replace ran.
//
// Short hedge filled @100, AlsStopReplacePct=3 (> hypothetical cond pct, mirroring the
// matrix regression's setup) forces the computed trigger (97.0) BELOW FilledPrice (100)
// AND below the live currentPrice (98.5) used here — the old code compared trigger
// against FilledPrice (97 < 100 → Falling, which happens to be correct here by
// coincidence) so this test alone wouldn't catch it; the second case below is the one
// that actually flips between the two comparisons.
func TestAlsPlaceLevelStop_TriggerDirectionMatchesTriggerVsCurrentPrice(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-sl-replace-1"}, nil)
	ar := newTestAccountRunner(t, fake)

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "44444444-5555-6666-7777-888888888888",
			Symbol:    "ALSTRIGDIRUSDT",
			Category:  "linear",
			Direction: DirectionShort,
			HedgeMode: true,
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Sell", Status: LevelFilled, FilledPrice: 100.0, Qty: "10"},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}

	// trigger = matrixStopReplaceTrigger(Short, 100, 3%) = 97.0 — below both FilledPrice
	// (100) and currentPrice (98.5), so the correct direction is Falling (2) either way
	// here. The real flip is exercised by the second test below.
	sr.mu.Lock()
	sr.alsPlaceLevelStop(context.Background(), &sr.levels[0], 100.0, 3.0, 98.5)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.TriggerDirection != 2 {
		t.Errorf("TriggerDirection = %d, want 2 (Falling) — trigger=97.0 is below currentPrice=98.5", req.TriggerDirection)
	}
}

// TestAlsPlaceLevelStop_TriggerAboveCurrentButBelowFilled_UsesRising is the case that
// actually distinguishes the fix from the bug: price has fallen from FilledPrice=100 all
// the way to currentPrice=95 (a fast move, exactly the live scenario), and the computed
// trigger (97.0) sits BETWEEN the two — below FilledPrice but above the live
// currentPrice. The buggy comparison (trigger vs FilledPrice: 97 < 100) would send
// Falling (2) and Bybit would reject it, since price must RISE from 95 to reach 97. The
// fix (trigger vs currentPrice: 97 > 95) correctly sends Rising (1).
func TestAlsPlaceLevelStop_TriggerAboveCurrentButBelowFilled_UsesRising(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "als-sl-replace-2"}, nil)
	ar := newTestAccountRunner(t, fake)

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "55555555-6666-7777-8888-999999999999",
			Symbol:    "ALSTRIGDIRUSDT2",
			Category:  "linear",
			Direction: DirectionShort,
			HedgeMode: true,
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Sell", Status: LevelFilled, FilledPrice: 100.0, Qty: "10"},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}

	sr.mu.Lock()
	sr.alsPlaceLevelStop(context.Background(), &sr.levels[0], 100.0, 3.0, 95.0)
	sr.mu.Unlock()

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.TriggerDirection != 1 {
		t.Errorf("TriggerDirection = %d, want 1 (Rising) — trigger=97.0 is above currentPrice=95.0, "+
			"a Falling trigger there is exactly what Bybit rejected live with retCode=110092/110093", req.TriggerDirection)
	}
	if got := placedOrderPrice(t, req.TriggerPrice); got < 96.99 || got > 97.01 {
		t.Errorf("TriggerPrice = %v, want ~97.0", got)
	}
}
