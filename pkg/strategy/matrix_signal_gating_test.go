package strategy

import (
	"context"
	"testing"

	"sis/pkg/trader"
)

// --- matrixLevelUseSignal / matrixIsVirtual ---
//
// Regression for the gap found live 2026-09-22: MatrixLevel.UseSignal and
// MatrixEntryLevel.UseSignal already had working toggle UI, but nothing in the placement
// engine ever read the flag — every level placed exactly as if UseSignal didn't exist.

func TestMatrixLevelUseSignal_EntrySlot(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionLong, MatrixEntryLevel: &MatrixEntryLevel{UseSignal: true}}}
	if !sr.matrixLevelUseSignal(0) {
		t.Error("entry level with UseSignal=true must report true")
	}
	sr.strategy.MatrixEntryLevel = &MatrixEntryLevel{UseSignal: false}
	if sr.matrixLevelUseSignal(0) {
		t.Error("entry level with UseSignal=false must report false")
	}
	sr.strategy.MatrixEntryLevel = nil
	if sr.matrixLevelUseSignal(0) {
		t.Error("nil entry level config must report false, not panic")
	}
}

func TestMatrixLevelUseSignal_Short_PositiveSlotMapsToAboveConfig(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{
		Direction: DirectionShort,
		MatrixLevels: []MatrixLevel{
			{Direction: "above", PriceStepPct: 2, UseSignal: true},
			{Direction: "above", PriceStepPct: 3, UseSignal: false},
		},
	}}
	if !sr.matrixLevelUseSignal(1) {
		t.Error("short slot 1 (first above entry) must reflect its own UseSignal=true")
	}
	if sr.matrixLevelUseSignal(2) {
		t.Error("short slot 2 (second above entry) must reflect its own UseSignal=false")
	}
}

func TestMatrixLevelUseSignal_Short_NegativeSlotAlwaysFalse(t *testing.T) {
	// Short's negative slots are unconditionally virtual (matrixIsVirtual) — no config
	// entry of their own, so UseSignal can never apply to them.
	sr := &StrategyRunner{strategy: Strategy{
		Direction:    DirectionShort,
		MatrixLevels: []MatrixLevel{{Direction: "below", PriceStepPct: -2, UseSignal: true}},
	}}
	if sr.matrixLevelUseSignal(-1) {
		t.Error("short negative slot must never report UseSignal=true — it has no matching config branch")
	}
}

func TestMatrixLevelUseSignal_Long_NegativeSlotMapsToBelowConfig(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{
		Direction: DirectionLong,
		MatrixLevels: []MatrixLevel{
			{Direction: "below", PriceStepPct: -2, UseSignal: true},
		},
	}}
	if !sr.matrixLevelUseSignal(-1) {
		t.Error("long slot -1 (first below entry) must reflect its own UseSignal=true")
	}
}

func TestMatrixIsVirtual_UseSignalForcesVirtual_ShortAboveSlot(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{
		Direction: DirectionShort,
		MatrixLevels: []MatrixLevel{
			{Direction: "above", PriceStepPct: 2, UseSignal: true}, // no order_type set — would be a real resting order otherwise
		},
	}}
	one := 1
	l := &GridLevel{Slot: &one}
	if !sr.matrixIsVirtual(l) {
		t.Error("a UseSignal=true above-config level must be forced virtual even without order_type=virtual")
	}
}

func TestMatrixIsVirtual_NoUseSignalNoOrderTypeVirtual_NotVirtual(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{
		Direction:    DirectionShort,
		MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 2}},
	}}
	one := 1
	l := &GridLevel{Slot: &one}
	if sr.matrixIsVirtual(l) {
		t.Error("a plain above-config level (no virtual order_type, no UseSignal) must not be forced virtual — regression guard for existing behavior")
	}
}

// --- matrixCancelSignalLostLevels ---

func TestMatrixCancelSignalLostLevels_NoSignalEngine_NeverCancels(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionShort,
			MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 2, UseSignal: true}},
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", Slot: &one, Status: LevelPlaced, ExchangeOrderID: "o1"},
		},
	}

	sr.matrixCancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID != "o1" {
		t.Errorf("level = %+v, want untouched (no signal engine must fail open)", sr.levels[0])
	}
	if len(fake.cancelOrderCalls) != 0 {
		t.Errorf("CancelOrder called %d times, want 0", len(fake.cancelOrderCalls))
	}
}

func TestMatrixCancelSignalLostLevels_FilledLevelNeverTouched(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionShort,
			MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 2, UseSignal: true}},
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", Slot: &one, Status: LevelFilled, ExchangeOrderID: "o1", FilledPrice: 100},
		},
	}

	sr.matrixCancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %v, want still Filled — a filled position must never be cancelled by a signal drop", sr.levels[0].Status)
	}
	if len(fake.cancelOrderCalls) != 0 {
		t.Errorf("CancelOrder called %d times, want 0", len(fake.cancelOrderCalls))
	}
}

func TestMatrixCancelSignalLostLevels_NonSignalLevelNeverTouched(t *testing.T) {
	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionShort,
			MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 2, UseSignal: false}},
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", Slot: &one, Status: LevelPlaced, ExchangeOrderID: "o1"},
		},
	}

	sr.matrixCancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID != "o1" {
		t.Errorf("level = %+v, want untouched — UseSignal=false levels must never be cancelled by this pass", sr.levels[0])
	}
}

// --- matrixPriceTick: absolute-mode signal gate on the virtual-trigger decision point ---

func TestMatrixPriceTick_UseSignalTrue_NoSignalEngine_StillTriggers(t *testing.T) {
	fake := &fakeExchange{}
	fake.placeOrderQ.push(trader.OrderResult{OrderId: "o1"}, nil)
	ar := newTestAccountRunner(t, fake) // signalEngine nil
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Symbol: "TESTUSDT", Category: "linear", Direction: DirectionShort, HedgeMode: true,
			MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 2, UseSignal: true}},
		},
		runner: ar,
		cycle:  &Cycle{CycleNum: 1},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Slot: &one, Side: "Sell", TargetPrice: 100, Qty: "1", Status: LevelPending},
		},
	}

	// Short, slot>0 (above/in-direction): crossed when currentPrice <= TargetPrice.
	sr.matrixPriceTick(context.Background(), 99.0)

	if len(fake.placeOrderCalls) != 1 {
		t.Errorf("PlaceOrder called %d times, want 1 — no signal engine must fail open, not block", len(fake.placeOrderCalls))
	}
}
