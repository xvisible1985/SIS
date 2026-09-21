package strategy

import "testing"

// TestMatrixLevelConfig_ReturnsUseSignal: matrixLevelConfig is the one place matrix code
// already looks up a slot's full TP/SL config (entry/slot 0, a positive "above" slot, or a
// negative "below" slot). Task 6 of the DCA signal-gating plan adds a 5th return value,
// useSignal, so later tasks have one consistent place to read the per-level UseSignal flag
// from instead of re-deriving it ad hoc at each level-construction call site.
func TestMatrixLevelConfig_ReturnsUseSignal(t *testing.T) {
	useSig := true
	tpPct := 2.0
	sr := &StrategyRunner{
		strategy: Strategy{
			MatrixEntryLevel: &MatrixEntryLevel{SizePct: 20, UseSignal: true},
			MatrixLevels: []MatrixLevel{
				{Direction: "below", PriceStepPct: -5, TPPct: &tpPct, UseSignal: useSig},
				{Direction: "above", PriceStepPct: 2, UseSignal: false},
			},
		},
	}
	if _, _, _, _, us := sr.matrixLevelConfig(0); us != true {
		t.Errorf("slot 0 useSignal = %v, want true", us)
	}
	if _, _, _, _, us := sr.matrixLevelConfig(-1); us != true {
		t.Errorf("slot -1 useSignal = %v, want true", us)
	}
	if _, _, _, _, us := sr.matrixLevelConfig(1); us != false {
		t.Errorf("slot 1 useSignal = %v, want false", us)
	}
}
