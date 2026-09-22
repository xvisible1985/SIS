package strategy

import (
	"context"
	"errors"
	"testing"

	"sis/pkg/signal"
)

// TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels proves the gate is
// genuinely reached and evaluated for a LevelPlaced/UseSignal=true level once the signal no
// longer agrees. Proven by reaching CancelOrder on a nil sr.runner.Exchange() (panics) — this
// only proves the CALL is reached, not which response branch runs afterward; that distinction
// (ambiguous isOrderGone vs. genuine success) is covered separately by
// TestCancelSignalLostLevels_CancelReturnsOrderGone_LeavesLevelTrackedAsPlaced and
// TestCancelSignalLostLevels_CancelSucceeds_RevertsToPending below, which use a fakeExchange
// to distinguish the two outcomes. The panic happens at the CancelOrder call itself (nil
// interface dispatch), before any response is even available, so it holds regardless of how
// this function's response-handling was rewritten.
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

// newSignalGateCancelTestRunner builds a StrategyRunner wired to fake for
// cancelSignalLostLevels tests that need to distinguish CancelOrder response branches (as
// opposed to the panic-proof technique above, which can't tell them apart). Forces
// signalGateAllows() to genuinely evaluate false via the same real-signal.Engine technique
// as TestCancelSignalLostLevels_PlacedUseSignalLevel_SignalGone_Cancels above: Direction
// Long wants signal.Buy, and the engine's Neutral default (no confirmed kline-close has
// arrived) never satisfies that.
func newSignalGateCancelTestRunner(t *testing.T, fake *fakeExchange, level GridLevel) (*StrategyRunner, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	se := signal.NewEngine(ctx, nil)
	ar := newTestAccountRunner(t, fake)
	ar.signalEngine = se

	sigConfigs := []SignalConfig{{Name: "rsi-os"}}
	goCfgs := ar.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-cancel-gone", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
			SignalConfigs: sigConfigs,
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{level},
	}

	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: signalGateAllows() = true, want false (engine should still be reporting the Neutral default)")
	}

	return sr, func() { se.Unsubscribe("test-sub-cancel-gone"); cancel() }
}

// TestCancelSignalLostLevels_CancelReturnsOrderGone_LeavesLevelTrackedAsPlaced is the fix
// for the significant bug found in whole-branch review: every UseSignal=true level places
// as a Market order exclusively, which fills essentially instantly — there is no real
// resting phase for it to sit in. So when CancelOrder comes back with a Bybit "order not
// found" (isOrderGone, retCode=110001), that is at least as likely to mean "already filled"
// as "genuinely cancelled." Treating it as a confirmed cancel (the original, buggy
// behavior) would revert the level to Pending and drop its order-index registration —
// silently abandoning a real, untracked open position if it actually filled, and risking a
// doubled position when the level re-triggers. The fix must leave the level exactly as it
// was: still LevelPlaced, still carrying its ExchangeOrderID, so the normal WS
// fill-handling path (which needs the order to still be registered) can resolve it
// correctly if it really filled.
func TestCancelSignalLostLevels_CancelReturnsOrderGone_LeavesLevelTrackedAsPlaced(t *testing.T) {
	fake := &fakeExchange{}
	fake.cancelOrderQ.push(struct{}{}, errors.New("bybit error: retCode=110001, retMsg=order not exists"))

	sr, done := newSignalGateCancelTestRunner(t, fake, GridLevel{
		ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: true, ExchangeOrderID: "order-abc",
	})
	defer done()
	// Register the order first, mirroring how the order was actually registered at
	// placement time — so the assertion below can prove the ambiguous-gone path leaves it
	// registered, rather than merely observing an empty map that was never populated.
	sr.runner.RegisterOrder("order-abc", orderRef{strategyID: sr.strategy.ID, levelID: "level-1", refType: "level"})

	sr.cancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelPlaced {
		t.Errorf("level status = %v, want unchanged LevelPlaced — an ambiguous isOrderGone must not be treated as a confirmed cancel", sr.levels[0].Status)
	}
	if sr.levels[0].ExchangeOrderID != "order-abc" {
		t.Errorf("ExchangeOrderID = %q, want unchanged %q — tracking must not be dropped on an ambiguous gone response", sr.levels[0].ExchangeOrderID, "order-abc")
	}
	if _, tracked := sr.runner.orderIndex["order-abc"]; !tracked {
		t.Error("order-abc must remain registered in orderIndex so a real WS fill event can still be matched to it")
	}
}

// TestCancelSignalLostLevels_CancelSucceeds_RevertsToPending is the positive-path
// counterpart: a genuine, unambiguous cancel success (err == nil) must still revert the
// level to Pending, clear its ExchangeOrderID, and drop its order-index registration — the
// behavior the ambiguous-isOrderGone fix above must not have broken.
func TestCancelSignalLostLevels_CancelSucceeds_RevertsToPending(t *testing.T) {
	fake := &fakeExchange{}
	fake.cancelOrderQ.push(struct{}{}, nil)

	sr, done := newSignalGateCancelTestRunner(t, fake, GridLevel{
		ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPlaced, UseSignal: true, ExchangeOrderID: "order-abc",
	})
	defer done()
	sr.runner.RegisterOrder("order-abc", orderRef{strategyID: sr.strategy.ID, levelID: "level-1", refType: "level"})

	sr.cancelSignalLostLevels(context.Background())

	if sr.levels[0].Status != LevelPending {
		t.Errorf("level status = %v, want LevelPending — a genuine cancel success must revert the level", sr.levels[0].Status)
	}
	if sr.levels[0].ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must be cleared on a genuine cancel success")
	}
	if _, tracked := sr.runner.orderIndex["order-abc"]; tracked {
		t.Error("order-abc must be unregistered after a genuine cancel success")
	}
	if len(fake.cancelOrderCalls) != 1 || fake.cancelOrderCalls[0].OrderId != "order-abc" {
		t.Errorf("cancelOrderCalls = %+v, want exactly one call for order-abc", fake.cancelOrderCalls)
	}
}
