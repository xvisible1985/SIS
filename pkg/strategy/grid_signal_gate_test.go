package strategy

import (
	"context"
	"testing"

	"sis/pkg/signal"
)

// TestGridVirtualPriceTick_UseSignalLevel_SignalAbsent_DoesNotPlace is the regression for the
// live incident (2026-09-21, PTBUSDT/Semera): a UseSignal=true level whose price trigger has
// been crossed must NOT place an order while the strategy's signalGateAllows() is false —
// proven by the level staying LevelPending (not advancing to LevelPlaced), since reaching
// PlaceOrder on a nil sr.runner.Exchange() would otherwise panic and this test would need a
// different proof technique if that path were reached.
//
// To get a real, deterministic "false" out of signalGateAllows() without any network access,
// this constructs a genuine *signal.Engine (signal.NewEngine) and Subscribes it to the exact
// same (symbol, interval, configs) tuple signalGateAllows() will itself query. Engine.Subscribe
// synchronously inserts a computeUnit into the engine with lastState defaulting to
// signal.Neutral (see pkg/signal/engine.go newComputeUnit: "Safe default — overwritten on
// first kline close"). That default is only ever overwritten by computeUnit.compute(), which
// this package only invokes from a CONFIRMED kline-close message arriving over a live Bybit
// WebSocket connection (pkg/signal/hub.go handleMessage) — never from the REST prefetch that
// Subscribe kicks off in the background. Within a unit test's lifetime no such WS message can
// plausibly arrive, so QueryState (called by signalGateAllows) deterministically observes the
// still-Neutral default. Since the strategy's Direction is Long (wants signal.Buy),
// Neutral != Buy, so signalGateAllows() genuinely evaluates to false through the real
// production code path — not a stub or a bypassed check.
func TestGridVirtualPriceTick_UseSignalLevel_SignalAbsent_DoesNotPlace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)

	runner := &AccountRunner{signalEngine: se}
	sigConfigs := []SignalConfig{{Name: "rsi-os"}}

	// Build the exact same []signal.Config that signalGateAllows() will build via
	// sr.runner.resolveSignalConfigs, and Subscribe the engine to it under the same
	// (symbol, interval) key signalGateAllows() queries (default tf "1h"), so the compute
	// unit hash matches exactly.
	goCfgs := runner.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-1", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-1")

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
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, ForceVirtual: true,
				UseSignal: true, TargetPrice: 100.0, Qty: "1.0"},
		},
	}

	// Sanity check: confirm the gate genuinely reads false via the real engine before
	// asserting on the higher-level behavior — if this ever flips true the test below would
	// pass for the wrong reason.
	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: signalGateAllows() = true, want false (engine should still be reporting the Neutral default)")
	}

	sr.gridVirtualPriceTick(context.Background(), 100.0)
	if sr.levels[0].Status != LevelPending {
		t.Errorf("level status = %v, want still LevelPending — signal absent must block placement", sr.levels[0].Status)
	}
	if sr.levels[0].ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must stay empty — signal-blocked level must never reach PlaceOrder")
	}
}

// TestGridVirtualPriceTick_NonSignalLevel_Unaffected is the regression-of-the-regression:
// confirms Task 5's new gate check doesn't accidentally block a level that was never
// UseSignal-gated in the first place. Reaching PlaceOrder on the nil sr.runner.Exchange()
// proves the existing (pre-this-plan) placement path still runs for a plain virtual level.
func TestGridVirtualPriceTick_NonSignalLevel_Unaffected(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:        "11111111-2222-3333-4444-555555555555",
			Direction: DirectionLong,
			Symbol:    "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, ForceVirtual: true,
				UseSignal: false, TargetPrice: 100.0, Qty: "1.0"},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching PlaceOrder on nil Exchange — a non-signal-gated level must place unconditionally, unchanged from before this plan")
		}
	}()
	sr.gridVirtualPriceTick(context.Background(), 100.0)
}
