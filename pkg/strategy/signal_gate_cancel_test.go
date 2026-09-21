package strategy

import (
	"context"
	"testing"

	"sis/pkg/signal"
)

// TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels is the core new
// behavior this task adds: a resting, unfilled, signal-gated order must be pulled once the
// signal no longer agrees. Proven by reaching CancelOrder on a nil sr.runner.Exchange()
// (panics), the same proof technique used throughout this plan and this codebase's existing
// matrix tests.
//
// Uses the established real-signal.Engine technique from grid_signal_gate_test.go: a genuine
// *signal.Engine is constructed and Subscribed to the exact (symbol, interval, configs) tuple
// signalGateAllows() itself queries. Subscribe synchronously inserts a computeUnit whose
// lastState defaults to signal.Neutral (see pkg/signal/engine.go newComputeUnit) until a real
// kline-close WS message arrives — which cannot happen within a unit test. Since the
// strategy's Direction is Long (wants signal.Buy), Neutral != Buy, so signalGateAllows()
// genuinely evaluates to false through real production code, not a stub.
func TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)

	runner := &AccountRunner{signalEngine: se}
	sigConfigs := []SignalConfig{{Name: "rsi-os"}}

	goCfgs := runner.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-cancel-1", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-cancel-1")

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: sigConfigs,
		},
		runner: runner,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: true, ExchangeOrderID: "order-abc"},
		},
	}

	// Sanity check: confirm the gate genuinely reads false via the real engine before
	// asserting on the higher-level behavior — if this ever flips true the test below would
	// pass for the wrong reason.
	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: signalGateAllows() = true, want false (engine should still be reporting the Neutral default)")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching CancelOrder on nil Exchange — signal-lost placed level must be cancelled")
		}
	}()
	sr.cancelSignalLostLevels(context.Background())
}

// TestCancelSignalLostLevels_FilledLevel_NeverTouched is the critical safety regression:
// signal loss must NEVER touch an already-filled (real, open) position — only pending/placed
// unfilled orders. No panic expected; the filled level must be completely ignored.
func TestCancelSignalLostLevels_FilledLevel_NeverTouched(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelFilled, UseSignal: true, FilledPrice: 100.0},
		},
	}
	// No signal-forcing needed — even if signalGateAllows() were false, a LevelFilled level
	// must never reach CancelOrder. A nil sr.runner is deliberately left in place: if this
	// test panics, the fix is wrong (it touched a filled level).
	sr.cancelSignalLostLevels(context.Background())
	if sr.levels[0].Status != LevelFilled {
		t.Errorf("level status = %v, want unchanged LevelFilled", sr.levels[0].Status)
	}
}

// TestCancelSignalLostLevels_NonSignalLevel_NeverTouched confirms a plain (UseSignal=false)
// placed level is left alone regardless of what the signal is doing — this cancellation path
// only applies to levels that opted into signal gating.
func TestCancelSignalLostLevels_NonSignalLevel_NeverTouched(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: false, ExchangeOrderID: "order-abc"},
		},
	}
	sr.cancelSignalLostLevels(context.Background())
	if sr.levels[0].Status != LevelPlaced || sr.levels[0].ExchangeOrderID == "" {
		t.Error("non-signal-gated placed level must be untouched")
	}
}
