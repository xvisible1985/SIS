package signal

import "testing"

func TestLeverageFilterSignal_Compute_AlwaysNeutral(t *testing.T) {
	sig := &leverageFilterSignal{minLeverage: 50}
	if got := sig.Compute(nil); got != Neutral {
		t.Errorf("Compute (symbol-unaware) = %v, want Neutral — matches whaleSignal's convention", got)
	}
}

func TestLeverageFilterSignal_ComputeWithSymbol_DisabledAlwaysPasses(t *testing.T) {
	sig := &leverageFilterSignal{minLeverage: 0}
	// No SetLeverageState call — cache is empty for this symbol, and it must still pass.
	if got := sig.ComputeWithSymbol("LEVSIGTEST1USDT", nil); got != Buy {
		t.Errorf("minLeverage=0, no cache data: ComputeWithSymbol = %v, want Buy (filter disabled)", got)
	}
}

func TestLeverageFilterSignal_ComputeWithSymbol_UnknownFailsClosed(t *testing.T) {
	sig := &leverageFilterSignal{minLeverage: 50}
	// Symbol never seeded via SetLeverageState — must NOT pass just because it's unconfigured.
	if got := sig.ComputeWithSymbol("LEVSIGTEST2USDT", nil); got != Neutral {
		t.Errorf("unknown leverage with filter enabled: ComputeWithSymbol = %v, want Neutral (fail closed)", got)
	}
}

func TestLeverageFilterSignal_ComputeWithSymbol_BelowThreshold(t *testing.T) {
	SetLeverageState("LEVSIGTEST3USDT", "linear", 20)
	sig := &leverageFilterSignal{minLeverage: 50}
	if got := sig.ComputeWithSymbol("LEVSIGTEST3USDT", nil); got != Neutral {
		t.Errorf("exchange max 20x < required 50x: ComputeWithSymbol = %v, want Neutral", got)
	}
}

func TestLeverageFilterSignal_ComputeWithSymbol_AtOrAboveThreshold(t *testing.T) {
	SetLeverageState("LEVSIGTEST4USDT", "linear", 50)
	sig := &leverageFilterSignal{minLeverage: 50}
	if got := sig.ComputeWithSymbol("LEVSIGTEST4USDT", nil); got != Buy {
		t.Errorf("exchange max 50x == required 50x: ComputeWithSymbol = %v, want Buy (>=, not strictly >)", got)
	}

	SetLeverageState("LEVSIGTEST5USDT", "linear", 100)
	if got := sig.ComputeWithSymbol("LEVSIGTEST5USDT", nil); got != Buy {
		t.Errorf("exchange max 100x > required 50x: ComputeWithSymbol = %v, want Buy", got)
	}
}

func TestLeverageFilterSignal_RegisteredInBuild(t *testing.T) {
	sig, err := Build(Config{Name: "leverage", Params: map[string]interface{}{"min_leverage": 25.0}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lf, ok := sig.(*leverageFilterSignal)
	if !ok {
		t.Fatalf("Build returned %T, want *leverageFilterSignal", sig)
	}
	if lf.minLeverage != 25 {
		t.Errorf("minLeverage = %v, want 25", lf.minLeverage)
	}
}

func TestLeverageFilterSignal_RegisteredWithDefault(t *testing.T) {
	sig, err := Build(Config{Name: "leverage", Params: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lf := sig.(*leverageFilterSignal)
	if lf.minLeverage != 0 {
		t.Errorf("default minLeverage = %v, want 0 (disabled)", lf.minLeverage)
	}
}

func TestLeverageState_GetSetRoundtrip(t *testing.T) {
	if got := GetLeverageState("LEVSTATETEST1USDT", "linear"); got != 0 {
		t.Errorf("unseeded symbol: GetLeverageState = %v, want 0 (unknown)", got)
	}
	SetLeverageState("LEVSTATETEST1USDT", "linear", 75)
	if got := GetLeverageState("LEVSTATETEST1USDT", "linear"); got != 75 {
		t.Errorf("GetLeverageState = %v, want 75", got)
	}
	// Different category must be a distinct cache entry.
	if got := GetLeverageState("LEVSTATETEST1USDT", "inverse"); got != 0 {
		t.Errorf("different category: GetLeverageState = %v, want 0 (not sharing the linear entry)", got)
	}
}
