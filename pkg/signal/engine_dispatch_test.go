package signal

import "testing"

// candleAwareOnlySignal implements plain Signal (candle-based) but NOT SymbolComputer —
// e.g. rsi-os, ema-x, and every other price-indicator signal.
type candleAwareOnlySignal struct{ state State }

func (s *candleAwareOnlySignal) Compute(_ []Candle) State { return s.state }

// symbolAwareSignal implements SymbolComputer — e.g. whaleSignal, leverageFilterSignal.
// Compute (candle-based) and ComputeWithSymbol deliberately return DIFFERENT states, so a
// test can tell which path actually ran.
type symbolAwareSignal struct {
	candleState State
	symbolState State
}

func (s *symbolAwareSignal) Compute(_ []Candle) State { return s.candleState }
func (s *symbolAwareSignal) ComputeWithSymbol(_ string, _ []Candle) State {
	return s.symbolState
}

// TestComputeSignalState_DispatchesToSymbolComputer is the regression for the bug found
// live 2026-08-19: ComputeMultiTFState (the bot-form "Проверка в моменте" preview) and
// ComputeStateForce's cold-cache fallback both called Signal.Compute(candles) directly,
// never checking whether the signal implements SymbolComputer — so leverageFilterSignal and
// whaleSignal, whose real logic lives in ComputeWithSymbol (Compute always returns Neutral
// for them, by design, since they have no candle-based answer), silently always read as
// Neutral through those two paths, while the third path (computeUnit's own continuous
// engine loop) worked correctly. All three now share this one function.
func TestComputeSignalState_DispatchesToSymbolComputer(t *testing.T) {
	sig := &symbolAwareSignal{candleState: Neutral, symbolState: Buy}
	got := computeSignalState(sig, "BTCUSDT", nil)
	if got != Buy {
		t.Errorf("computeSignalState = %v, want Buy (via ComputeWithSymbol) — got the Compute(candles) fallback (%v) instead", got, sig.candleState)
	}
}

// TestComputeSignalState_FallsBackToPlainComputeForNonSymbolSignals verifies the dispatch
// doesn't change behavior for the vast majority of signals (RSI, EMA, MACD, ...) that only
// implement plain Signal — computeSignalState must still call Compute(candles) for those.
func TestComputeSignalState_FallsBackToPlainComputeForNonSymbolSignals(t *testing.T) {
	sig := &candleAwareOnlySignal{state: Sell}
	got := computeSignalState(sig, "BTCUSDT", nil)
	if got != Sell {
		t.Errorf("computeSignalState = %v, want Sell (from Compute) — a plain Signal must be unaffected by this dispatch", got)
	}
}

// TestComputeSignalState_LeverageFilterSignal_ThroughSharedDispatch confirms the actual
// production signal type behaves correctly through the shared dispatch, end to end.
func TestComputeSignalState_LeverageFilterSignal_ThroughSharedDispatch(t *testing.T) {
	SetLeverageState("DISPATCHTESTUSDT", "linear", 100)
	sig := &leverageFilterSignal{minLeverage: 50}
	got := computeSignalState(sig, "DISPATCHTESTUSDT", nil)
	if got != Buy {
		t.Errorf("computeSignalState(leverageFilterSignal) = %v, want Buy — exchange max 100x >= required 50x", got)
	}
}
