package signal

import "testing"

// candlesHourly builds candles spaced exactly 1 hour apart (Time in unix millis),
// with the given close prices in chronological order (oldest first).
func candlesHourly(closes ...float64) []Candle {
	const hourMs = int64(3600 * 1000)
	out := make([]Candle, len(closes))
	start := int64(1_700_000_000_000) // arbitrary fixed epoch, irrelevant to the logic
	for i, c := range closes {
		out[i] = Candle{Time: start + int64(i)*hourMs, Close: c}
	}
	return out
}

// flatRun returns n hourly candles all at the given price, followed by one
// final candle at lastClose — used to simulate "price sat at `price` for the
// whole lookback window, then moved to lastClose on the most recent candle".
func flatRun(hours int, price, lastClose float64) []Candle {
	closes := make([]float64, hours+1)
	for i := 0; i < hours; i++ {
		closes[i] = price
	}
	closes[hours] = lastClose
	return candlesHourly(closes...)
}

func TestPriceChangeSignal_RiseAboveThreshold_TrendMode_Buy(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	// 100 -> 125 over 24h = +25%, past the 20% threshold.
	c := flatRun(24, 100, 125)
	if got := s.Compute(c); got != Buy {
		t.Fatalf("Compute() = %v, want Buy (rise + trend)", got)
	}
}

func TestPriceChangeSignal_FallAboveThreshold_TrendMode_Sell(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	// 100 -> 75 over 24h = -25%.
	c := flatRun(24, 100, 75)
	if got := s.Compute(c); got != Sell {
		t.Fatalf("Compute() = %v, want Sell (fall + trend)", got)
	}
}

func TestPriceChangeSignal_RiseAboveThreshold_CounterMode_Sell(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "counter"}
	c := flatRun(24, 100, 125) // +25%
	if got := s.Compute(c); got != Sell {
		t.Fatalf("Compute() = %v, want Sell (rise + counter-trend fades the move)", got)
	}
}

func TestPriceChangeSignal_FallAboveThreshold_CounterMode_Buy(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "counter"}
	c := flatRun(24, 100, 75) // -25%
	if got := s.Compute(c); got != Buy {
		t.Fatalf("Compute() = %v, want Buy (fall + counter-trend buys the dip)", got)
	}
}

func TestPriceChangeSignal_BelowThreshold_Neutral(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	// 100 -> 110 over 24h = +10%, below the 20% threshold — neutral regardless of mode.
	c := flatRun(24, 100, 110)
	if got := s.Compute(c); got != Neutral {
		t.Fatalf("Compute() = %v, want Neutral (10%% move < 20%% threshold)", got)
	}
}

func TestPriceChangeSignal_ExactlyAtThreshold_NotNeutral(t *testing.T) {
	// The condition is |change| >= threshold, not strictly greater — an exact
	// 20% move on a 20% threshold must fire, not sit on the neutral fence.
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	c := flatRun(24, 100, 120) // exactly +20%
	if got := s.Compute(c); got != Buy {
		t.Fatalf("Compute() = %v, want Buy (exactly at threshold should fire)", got)
	}
}

func TestPriceChangeSignal_NotEnoughHistory_Neutral(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	// Only 5 hours of candles — can't cover a 24h lookback window yet.
	c := flatRun(5, 100, 200) // even a huge move must not fire without enough history
	if got := s.Compute(c); got != Neutral {
		t.Fatalf("Compute() = %v, want Neutral (insufficient history for a 24h window)", got)
	}
}

func TestPriceChangeSignal_UsesWallClockTime_NotCandleCount(t *testing.T) {
	// Regression: the lookback must be driven by Candle.Time (real elapsed
	// hours), not by "N candles back" — build 48 candles spaced 30 minutes
	// apart (i.e. 24 real hours = 48 candles) and confirm the 24h window
	// correctly reaches back only 24 real hours (48 candles), not 24 candles
	// (which would only be 12 real hours and should NOT yet show the move).
	const halfHourMs = int64(1800 * 1000)
	start := int64(1_700_000_000_000)
	closes := make([]float64, 49)
	for i := 0; i < 48; i++ {
		closes[i] = 100
	}
	closes[48] = 125 // +25% on the most recent (49th) candle
	c := make([]Candle, 49)
	for i, cl := range closes {
		c[i] = Candle{Time: start + int64(i)*halfHourMs, Close: cl}
	}

	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	if got := s.Compute(c); got != Buy {
		t.Fatalf("Compute() = %v, want Buy — 24h window over 30m candles should reach back 48 candles", got)
	}

	// Same data, but demand a 48h window — only 24 real hours of history
	// exist, so this must NOT have enough history yet.
	s48 := &priceChangeSignal{periodHours: 48, thresholdPct: 20, mode: "trend"}
	if got := s48.Compute(c); got != Neutral {
		t.Fatalf("Compute() = %v, want Neutral — only 24h of history exists, can't cover a 48h window", got)
	}
}

func TestPriceChangeSignal_Value_ReturnsPct(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	c := flatRun(24, 100, 125)
	if got := s.Value(c); got != 25 {
		t.Fatalf("Value() = %v, want 25 (25%% rise)", got)
	}
}

func TestPriceChangeSignal_Value_ZeroWhenNotEnoughHistory(t *testing.T) {
	s := &priceChangeSignal{periodHours: 24, thresholdPct: 20, mode: "trend"}
	c := flatRun(5, 100, 200)
	if got := s.Value(c); got != 0 {
		t.Fatalf("Value() = %v, want 0 (insufficient history)", got)
	}
}

func TestPriceChangeSignal_RegisteredWithDefaults(t *testing.T) {
	sig, err := Build(Config{Name: "price-change", Params: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pc, ok := sig.(*priceChangeSignal)
	if !ok {
		t.Fatalf("Build returned %T, want *priceChangeSignal", sig)
	}
	if pc.periodHours != 24 {
		t.Errorf("default periodHours = %v, want 24", pc.periodHours)
	}
	if pc.thresholdPct != 20 {
		t.Errorf("default thresholdPct = %v, want 20", pc.thresholdPct)
	}
	if pc.mode != "trend" {
		t.Errorf("default mode = %q, want trend", pc.mode)
	}
}

func TestPriceChangeSignal_RegisteredWithCustomParams(t *testing.T) {
	// Keys are camelCase to match what the frontend actually saves
	// (frontend/src/features/indicators/indicators.tsx, PriceChangeP) — a prior mismatch
	// here (snake_case) meant custom values were silently ignored (2026-07-16).
	sig, err := Build(Config{Name: "price-change", Params: map[string]interface{}{
		"periodHours":  6.0,
		"thresholdPct": 5.0,
		"mode":         "counter",
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pc := sig.(*priceChangeSignal)
	if pc.periodHours != 6 || pc.thresholdPct != 5 || pc.mode != "counter" {
		t.Fatalf("Build params = %+v, want periodHours=6 thresholdPct=5 mode=counter", pc)
	}
}
