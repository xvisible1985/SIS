package strategy

import "testing"

func TestAlsUnsafeHedgeClamp_FullAmountAllowedWithinLimit(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=0, Vstop=0 → Vh=400. A 300 USDT top-up is within it,
	// passes through unchanged.
	got := alsUnsafeHedgeClamp(300, 40, 1000, 0, 0, 1.0)
	want := 300.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_ClampsWhenOverLimit(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=350 (already unprotected), Vstop=0 → Vh=400-350=50.
	// A 300 USDT top-up gets clamped down to 50.
	got := alsUnsafeHedgeClamp(300, 40, 1000, 350, 0, 1.0)
	want := 50.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_StoppedVolumeDividedByK(t *testing.T) {
	// Vmax=40%*1000=400, Vopen=0, Vstop=200, stopMarketSizeK=2.0 → Vh=400-0+200/2=500.
	// A 450 USDT top-up fits within 500, passes through unchanged.
	got := alsUnsafeHedgeClamp(450, 40, 1000, 0, 200, 2.0)
	want := 450.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_NeverNegative(t *testing.T) {
	// Vopen already exceeds Vmax — Vh would be negative; clamp to 0, not a negative top-up.
	got := alsUnsafeHedgeClamp(100, 40, 1000, 500, 0, 1.0)
	want := 0.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v", got, want)
	}
}

func TestAlsUnsafeHedgeClamp_ZeroMaxPctMeansNoLimit(t *testing.T) {
	// UnsafeHedgeMaxPct==0 means the feature is unconfigured — pass the top-up through
	// unclamped (matches the config's documented "0 = no limit" convention already used
	// elsewhere on Strategy, e.g. MaxStopActive/GridActive).
	got := alsUnsafeHedgeClamp(9999, 0, 1000, 0, 0, 1.0)
	want := 9999.0
	if got != want {
		t.Errorf("alsUnsafeHedgeClamp = %v, want %v (unlimited when UnsafeHedgeMaxPct is 0)", got, want)
	}
}
