package strategy

import (
	"math"
	"testing"
)

// floatEq compares with a small tolerance — alsThresholdPrice's arithmetic (e.g.
// 100*(1-(-10)/100)) is exact in decimal but not always exactly representable in
// float64 (observed: 110.00000000000001), so an exact == comparison is too strict for
// a formula that is otherwise correct.
func floatEq(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestAlsThresholdPrice_ShortHedgeProtectsLongMain_TriggersBelowBase(t *testing.T) {
	// Hedge direction Short (protects a Long main) — adverse for main is price FALLING,
	// so trigger_pct=-10 against a base of 100 must land BELOW 100.
	got := alsThresholdPrice(DirectionShort, 1, -10, 100, 0)
	want := 90.0
	if !floatEq(got, want) {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdPrice_LongHedgeProtectsShortMain_TriggersAboveBase(t *testing.T) {
	// Hedge direction Long (protects a Short main) — adverse for main is price RISING,
	// so trigger_pct=-10 against a base of 100 must land ABOVE 100.
	got := alsThresholdPrice(DirectionLong, 1, -10, 100, 0)
	want := 110.0
	if !floatEq(got, want) {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdPrice_FromLastOrder(t *testing.T) {
	// trigger_type=0 (from last order) uses lastOrderPrice, not avgEntry. Hedge Short
	// (long main): -5% from a last-order base of 95 lands below it.
	got := alsThresholdPrice(DirectionShort, 0, -5, 100, 95)
	want := 90.25 // 95 * (1 - 5/100)
	if !floatEq(got, want) {
		t.Errorf("alsThresholdPrice = %v, want %v", got, want)
	}
}

func TestAlsThresholdCrossed_ShortHedgeProtectsLongMain_CrossesOnFall(t *testing.T) {
	cases := []struct {
		name    string
		price   float64
		trigger float64
		want    bool
	}{
		{"not yet crossed", 95, 90, false},
		{"exactly at trigger", 90, 90, true},
		{"crossed", 85, 90, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := alsThresholdCrossed(DirectionShort, c.price, c.trigger); got != c.want {
				t.Errorf("alsThresholdCrossed(short, %v, %v) = %v, want %v", c.price, c.trigger, got, c.want)
			}
		})
	}
}

func TestAlsThresholdCrossed_LongHedgeProtectsShortMain_CrossesOnRise(t *testing.T) {
	if !alsThresholdCrossed(DirectionLong, 110, 105) {
		t.Error("alsThresholdCrossed(long, 110, 105) = false, want true (price rose past trigger)")
	}
	if alsThresholdCrossed(DirectionLong, 100, 105) {
		t.Error("alsThresholdCrossed(long, 100, 105) = true, want false (price hasn't reached trigger)")
	}
}

func TestAlsTopUpUsdt_FirstLevel_NoExistingHedge(t *testing.T) {
	// target=30% of a 1000 USDT main, nothing open yet → full 300 USDT top-up.
	got := alsTopUpUsdt(30, 1000, 0)
	want := 300.0
	if !floatEq(got, want) {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_SecondLevel_OnlyDelta(t *testing.T) {
	// target=50% of a 1000 USDT main, 300 already open → top up only 200 more.
	got := alsTopUpUsdt(50, 1000, 300)
	want := 200.0
	if !floatEq(got, want) {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_MainGrewBetweenLevels(t *testing.T) {
	// target=50% of a main that's now 1200 USDT (grew via its own DCA since level 1
	// fired), 300 already open → target is 600, top up 300 more, not a flat re-add.
	got := alsTopUpUsdt(50, 1200, 300)
	want := 300.0
	if !floatEq(got, want) {
		t.Errorf("alsTopUpUsdt = %v, want %v", got, want)
	}
}

func TestAlsTopUpUsdt_NeverNegative(t *testing.T) {
	// Already-open hedge exceeds this level's target (e.g. main shrank) — must not
	// return a negative top-up (that would mean "close some hedge", out of scope here).
	got := alsTopUpUsdt(30, 1000, 400)
	want := 0.0
	if !floatEq(got, want) {
		t.Errorf("alsTopUpUsdt = %v, want %v (clamped to zero, never negative)", got, want)
	}
}
