package strategy

import (
	"context"
	"testing"
	"time"

	"sis/pkg/signal"
)

// TestMatrixIsVirtual_UseSignalLevel_AlwaysVirtual is the matrix equivalent of the grid
// forceVirtual regression: a UseSignal=true level must be treated as virtual even when
// nothing else about it (Slot, OrderType) would normally make it so.
//
// Uses Direction: DirectionLong, slot: -1 deliberately — NOT slot: 1, which would hit
// matrixIsVirtual's "long: above levels always virtual" branch and return true
// unconditionally regardless of UseSignal, making the test pass for the wrong reason.
// slot=-1 for a long hits the below-slot branch instead
// (`idx := -slot-1; return idx < len(below) && below[idx].OrderType == "virtual"`), which
// with an empty MatrixLevels config returns false by default — so UseSignal=true is the
// only thing that can flip this particular case to true, genuinely proving the check.
func TestMatrixIsVirtual_UseSignalLevel_AlwaysVirtual(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionLong}}
	slot := -1
	l := &GridLevel{Slot: &slot, UseSignal: true}
	if !sr.matrixIsVirtual(l) {
		t.Error("matrixIsVirtual = false, want true — UseSignal must force virtual even for a below-slot whose OrderType config isn't 'virtual'")
	}
}

// TestMatrixIsVirtual_NoUseSignalNoConfig_NotVirtual is the negative control for the test
// above: the same slot/direction combination, without UseSignal, must still correctly
// return false — locking in that the pre-existing default-false behavior for an
// unconfigured below-slot wasn't silently changed by the new check.
func TestMatrixIsVirtual_NoUseSignalNoConfig_NotVirtual(t *testing.T) {
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionLong}}
	slot := -1
	l := &GridLevel{Slot: &slot, UseSignal: false}
	if sr.matrixIsVirtual(l) {
		t.Error("matrixIsVirtual = true, want false — an unconfigured below-slot with UseSignal=false must not be forced virtual")
	}
}

// TestMatrixTriggerVirtualLevel_SignalAbsent_DoesNotPlace is the matrix absolute-mode
// equivalent of the grid gating test.
func TestMatrixTriggerVirtualLevel_SignalAbsent_DoesNotPlace(t *testing.T) {
	// Mirror pkg/strategy/grid_signal_gate_test.go's technique for forcing
	// signalGateAllows() to return false through a real *signal.Engine: construct a real
	// signal.Engine, Subscribe to the same symbol/tf/configs signalGateAllows() will query,
	// and rely on the documented Neutral default before any confirmed kline-close arrives.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)

	runner := &AccountRunner{signalEngine: se}
	sigConfigs := []SignalConfig{{Name: "rsi-os"}}

	goCfgs := runner.resolveSignalConfigs(sigConfigs)
	if err := se.Subscribe("test-sub-2", "TESTUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-2")

	slot := 1
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionLong, Symbol: "TESTUSDT", SignalConfigs: sigConfigs},
		runner:   runner,
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
	}

	// Sanity check: confirm the gate genuinely reads false via the real engine before
	// asserting on the higher-level behavior.
	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: signalGateAllows() = true, want false (engine should still be reporting the Neutral default)")
	}

	l := &GridLevel{ID: "level-1", LevelIdx: 1, Side: "Buy", Status: LevelPending, Slot: &slot, UseSignal: true, Qty: "1.0"}
	sr.matrixTriggerVirtualLevel(context.Background(), l)
	if l.Status != LevelPending {
		t.Errorf("level status = %v, want still LevelPending", l.Status)
	}
	if l.ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must stay empty — signal-blocked level must never reach PlaceOrder")
	}
}

// TestMatrixPlaceRelativeVirtualOrder_SignalAbsent_DoesNotReachExchange is the
// relative-slots-mode sibling of TestMatrixTriggerVirtualLevel_SignalAbsent_DoesNotPlace
// above: a UseSignal=true relative slot must not reach PlaceOrder while signalGateAllows()
// is false, even when the (independent) risk gate would allow it through. Follows this
// file's own established fixture style for matrixPlaceRelativeVirtualOrder tests (see
// TestMatrixPlaceRelativeVirtualOrder_AllowedByRiskGate_ReachesExchange /
// _BlockedByRiskGate_DoesNotReachExchange in matrix_relative_test.go) rather than the
// panic-proof technique used above — the risk gate is deliberately configured to ALLOW so
// this test isolates the new signal gate, proven by the level staying Pending instead of
// panicking on the nil sr.runner.Exchange()/tradeStream.
func TestMatrixPlaceRelativeVirtualOrder_SignalAbsent_DoesNotReachExchange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	se := signal.NewEngine(ctx, nil)

	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.Symbol = "BTCUSDT"
	sr.strategy.SignalConfigs = []SignalConfig{{Name: "rsi-os"}}
	sr.runner = &AccountRunner{
		positions:    map[string]float64{},
		posAvgEntry:  map[string]float64{},
		signalEngine: se,
	}
	sr.runner.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25, well within reach below

	goCfgs := sr.runner.resolveSignalConfigs(sr.strategy.SignalConfigs)
	if err := se.Subscribe("test-sub-3", "BTCUSDT", "1h", goCfgs, func(signal.State) {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer se.Unsubscribe("test-sub-3")

	// Sanity check: Direction Short wants signal.Sell; the engine's Neutral default (no
	// confirmed kline-close has arrived) must genuinely evaluate to false.
	if sr.signalGateAllows() {
		t.Fatal("test setup invalid: signalGateAllows() = true, want false (engine should still be reporting the Neutral default)")
	}

	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "virt-1", Slot: &negOne, Status: LevelPending, TargetPrice: 102.0, Qty: "0.01", Side: "Sell", UseSignal: true,
	})
	placed := &sr.levels[len(sr.levels)-1]

	// qty=0.01 @ currentPrice=1000 → $10 notional, within cap=25 → risk gate alone would
	// allow this through, isolating the signal gate as the thing under test.
	sr.matrixPlaceRelativeVirtualOrder(context.Background(), placed, 1000.0)

	if placed.Status != LevelPending {
		t.Errorf("level status = %v, want still Pending — a signal-blocked entry must not advance to Placed", placed.Status)
	}
	if placed.ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must stay empty — signalGateAllows()=false must prevent ever reaching PlaceOrder")
	}
}
