package strategy

import (
	"context"
	"strconv"
	"testing"

	"sis/pkg/trader"
)

// newMatrixTPTestStrategy builds a short matrix StrategyRunner with exactly one filled
// level (slot 1, tp_pct=1%, filled at 100.0 for 10 coins — so avgEntry()=100.0,
// computedQty=10, and the TP price for a 1% short target is 100*(1-1/100)=99.0), backed
// by fake and with a fresh WS cache (avoids exercising the staleness fallback from Task
// 3 — that's a separate concern, covered separately). lastMatrixPrice is the caller's to
// set afterward to control whether matrixTPCrossed is true or false.
//
// instr.QtyStep is deliberately set nonzero: matrixUpdateTP falls back to the real,
// unfaked trader.GetInstrumentInfo (a live signed HTTP call to Bybit, not part of the
// trader.Exchange interface fakeExchange covers) whenever QtyStep==0. A future test
// reusing this helper for a different scenario must keep instr.QtyStep nonzero (or
// otherwise avoid triggering that branch) to stay network-free.
func newMatrixTPTestStrategy(t *testing.T, fake *fakeExchange) *StrategyRunner {
	t.Helper()
	ar := newTestAccountRunner(t, fake)
	setWSPosition(ar, "TESTUSDT", 0, 10, 100.0)

	tpPct := 1.0
	slot1 := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			Symbol:       "TESTUSDT",
			Category:     "linear",
			Direction:    DirectionShort,
			StrategyType: "matrix",
			HedgeMode:    false,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", PriceStepPct: 3.0, SizePct: 10, TPPct: &tpPct},
			},
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 100.0, Qty: "10", SizeUSDT: 100.0},
		},
		instr: trader.InstrumentInfo{TickSize: 0.01, QtyStep: 0.001},
	}
	return sr
}

// placedOrderPrice parses req.Price (limit orders) or req.TriggerPrice (stop orders) back
// to float64, tolerating FormatPrice's tick-rounding rather than asserting an exact string.
func placedOrderPrice(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("placedOrderPrice: %q: %v", s, err)
	}
	return v
}

func TestMatrixUpdateTP_PriceNotCrossed_PlacesLimitOrder(t *testing.T) {
	fake := &fakeExchange{}
	sr := newMatrixTPTestStrategy(t, fake)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "tp-order-1"}, nil)
	// Mark price is 99.5 — above the 99.0 TP target, so a plain limit BUY at 99.0 would
	// rest normally (short TP: buy back cheaper). matrixTPCrossed(short, 99.5, 99.0) =
	// 99.5 <= 99.0 = false → not crossed.
	sr.lastMatrixPrice = 99.5

	sr.matrixUpdateTP(context.Background())

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.OrderType != "Limit" {
		t.Errorf("OrderType = %q, want %q (price hasn't crossed TP yet)", req.OrderType, "Limit")
	}
	if req.Side != "Buy" {
		t.Errorf("Side = %q, want %q (short position TP closes by buying back)", req.Side, "Buy")
	}
	if req.OrderFilter == "StopOrder" {
		t.Errorf("OrderFilter = %q, want empty — a not-yet-crossed TP must be a plain limit order, not a conditional stop", req.OrderFilter)
	}
	if got := placedOrderPrice(t, req.Price); got < 98.99 || got > 99.01 {
		t.Errorf("Price = %v, want ~99.0 (avgEntry 100.0 * (1 - 1%%))", got)
	}
}

func TestMatrixUpdateTP_PriceCrossed_PlacesStopOrder(t *testing.T) {
	fake := &fakeExchange{}
	sr := newMatrixTPTestStrategy(t, fake)
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "tp-order-2"}, nil)
	// Mark price is 98.0 — at/below the 99.0 TP target already: a plain limit BUY at 99.0
	// would fill instantly against the current market instead of waiting for a genuine
	// reversal. matrixTPCrossed(short, 98.0, 99.0) = 98.0 <= 99.0 = true → crossed.
	sr.lastMatrixPrice = 98.0

	sr.matrixUpdateTP(context.Background())

	if len(fake.placeOrderCalls) != 1 {
		t.Fatalf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
	req := fake.placeOrderCalls[0]
	if req.OrderType != "Market" {
		t.Errorf("OrderType = %q, want %q (a crossed TP must be a conditional trigger order, not a resting limit)", req.OrderType, "Market")
	}
	if req.OrderFilter != "StopOrder" {
		t.Errorf("OrderFilter = %q, want %q", req.OrderFilter, "StopOrder")
	}
	if req.Side != "Buy" {
		t.Errorf("Side = %q, want %q", req.Side, "Buy")
	}
	// Short TP-as-stop must fire when price RISES back up to the trigger (1), never on a
	// fall (2, that's the long-side convention) — see the trigger-direction comment in
	// matrixUpdateTP and matrixPlacePerLevelSL.
	if req.TriggerDirection != 1 {
		t.Errorf("TriggerDirection = %d, want 1 (short: fires on rise to/above trigger)", req.TriggerDirection)
	}
	if req.TriggerBy != "LastPrice" {
		t.Errorf("TriggerBy = %q, want %q", req.TriggerBy, "LastPrice")
	}
	if got := placedOrderPrice(t, req.TriggerPrice); got < 98.99 || got > 99.01 {
		t.Errorf("TriggerPrice = %v, want ~99.0 (avgEntry 100.0 * (1 - 1%%))", got)
	}
}
