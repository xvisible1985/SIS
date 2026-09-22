package strategy

import (
	"context"
	"testing"

	"sis/pkg/signal"
	"sis/pkg/trader"
)

// --- gridStepForceVirtual ---
//
// Regression for the gap found live 2026-09-22 (Мультибот "Мэйн позиция" tab / any grid
// strategy): GridStep.UseSignal already existed on the type and round-tripped through the
// DB, but the frontend had no control to set it and the backend never gated placement on
// it — every level placed unconditionally at cycle start regardless of the flag.

func TestGridStepForceVirtual_UseSignalAlone_ForcesVirtual(t *testing.T) {
	step := GridStep{PriceMovePct: -3, OrderType: "", UseSignal: true}
	if !gridStepForceVirtual(step) {
		t.Error("a UseSignal step with a negative (exchange-limit-shaped) PriceMovePct must still be forced virtual")
	}
}

func TestGridStepForceVirtual_NoneOfTheFlags_NotVirtual(t *testing.T) {
	step := GridStep{PriceMovePct: -3, OrderType: "", UseSignal: false}
	if gridStepForceVirtual(step) {
		t.Error("a plain negative-step exchange-limit level must not be forced virtual")
	}
}

func TestGridStepForceVirtual_ExistingReasonsUnchanged(t *testing.T) {
	if !gridStepForceVirtual(GridStep{OrderType: "virtual"}) {
		t.Error("explicit order_type=virtual must still force virtual")
	}
	if !gridStepForceVirtual(GridStep{PriceMovePct: 2}) {
		t.Error("positive price_move_pct must still force virtual")
	}
}

// --- signalWantsDirection ---

func TestSignalWantsDirection(t *testing.T) {
	if want, ok := signalWantsDirection(DirectionLong); !ok || want != signal.Buy {
		t.Errorf("long: want=%v ok=%v, want signal.Buy/true", want, ok)
	}
	if want, ok := signalWantsDirection(DirectionShort); !ok || want != signal.Sell {
		t.Errorf("short: want=%v ok=%v, want signal.Sell/true", want, ok)
	}
	if _, ok := signalWantsDirection(""); ok {
		t.Error("unknown/both direction must report ok=false (nothing to gate against)")
	}
}

// --- currentSignalMatchesDirection: fallback branches (no live engine required) ---
//
// Both cases must behave exactly as if UseSignal were false — the design's explicit
// invariant that a signal gate with nothing to evaluate must never block trading forever.

func TestCurrentSignalMatchesDirection_NoSignalEngine_AlwaysMatches(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionShort, SignalConfigs: []SignalConfig{{Name: "st-flip"}}},
		runner:   &AccountRunner{}, // signalEngine left nil
	}
	if !sr.currentSignalMatchesDirection() {
		t.Error("nil signal engine must fail open (matches=true)")
	}
}

func TestCurrentSignalMatchesDirection_NoConfigs_AlwaysMatches(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionShort, SignalConfigs: nil},
		runner:   &AccountRunner{},
	}
	if !sr.currentSignalMatchesDirection() {
		t.Error("empty SignalConfigs must fail open (matches=true)")
	}
}

// --- gridVirtualPriceTick: signal gate on placement ---

// TestGridVirtualPriceTick_UseSignalFalse_PlacesOnPriceAloneUnchanged pins the pre-existing
// behavior: a level with UseSignal=false must place purely on price crossing, exactly as
// before this feature existed — no accidental new gating for the common case.
func TestGridVirtualPriceTick_UseSignalFalse_PlacesOnPriceAloneUnchanged(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "o1"}, nil)
	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionLong},
		runner:   ar,
		cycle:    &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Side: "Buy", TargetPrice: 100, Qty: "1", Status: LevelPending, ForceVirtual: true, UseSignal: false},
		},
	}

	sr.gridVirtualPriceTick(context.Background(), 99.0) // price crossed (<=100 for Buy)

	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %v, want Placed — UseSignal=false must place on price alone", sr.levels[0].Status)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Errorf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
}

// TestGridVirtualPriceTick_UseSignalTrue_NoSignalEngine_StillPlaces is the fail-open
// regression: a UseSignal=true level on a strategy with no signal engine configured must
// behave exactly like UseSignal=false — place once price is reached — never block forever.
func TestGridVirtualPriceTick_UseSignalTrue_NoSignalEngine_StillPlaces(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "o1"}, nil)
	ar := newTestAccountRunner(t, fake) // signalEngine nil
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionLong},
		runner:   ar,
		cycle:    &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Side: "Buy", TargetPrice: 100, Qty: "1", Status: LevelPending, ForceVirtual: true, UseSignal: true},
		},
	}

	sr.gridVirtualPriceTick(context.Background(), 99.0)

	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %v, want Placed — no signal engine must fail open, not block", sr.levels[0].Status)
	}
	if len(fake.placeOrderCalls) != 1 {
		t.Errorf("PlaceOrder called %d times, want 1", len(fake.placeOrderCalls))
	}
}

// TestGridCancelSignalLostLevels_NoSignalEngine_NeverCancels mirrors the placement-side
// fail-open guarantee on the cancel-after-placement path: with nothing to evaluate, an
// already-placed UseSignal level must never get spuriously cancelled.
func TestGridCancelSignalLostLevels_NoSignalEngine_NeverCancels(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionLong},
		runner:   ar,
		cycle:    &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, ExchangeOrderID: "o1", UseSignal: true},
		},
	}

	sr.gridCancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID != "o1" {
		t.Errorf("level = %+v, want untouched (still Placed with its order id)", sr.levels[0])
	}
	if len(fake.cancelOrderCalls) != 0 {
		t.Errorf("CancelOrder called %d times, want 0", len(fake.cancelOrderCalls))
	}
}

// TestGridCancelSignalLostLevels_FilledLevelNeverTouched pins the hard safety invariant:
// even if somehow constructed with UseSignal on a Filled level, this pass must only ever
// consider Status==Placed — a real open position must never be cancelled by a signal drop.
func TestGridCancelSignalLostLevels_FilledLevelNeverTouched(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionLong},
		runner:   ar,
		cycle:    &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Side: "Buy", Status: LevelFilled, ExchangeOrderID: "o1", UseSignal: true, FilledPrice: 100},
		},
	}

	sr.gridCancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %v, want still Filled — a filled position must never be touched", sr.levels[0].Status)
	}
	if len(fake.cancelOrderCalls) != 0 {
		t.Errorf("CancelOrder called %d times, want 0", len(fake.cancelOrderCalls))
	}
}
