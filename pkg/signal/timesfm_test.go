package signal

import (
	"testing"
	"time"
)

func TestInferTimeframe_KnownSpacings(t *testing.T) {
	cases := []struct {
		deltaMs int64
		want    string
	}{
		{60_000, "1m"},
		{300_000, "5m"},
		{900_000, "15m"},
		{3_600_000, "1h"},
		{14_400_000, "4h"},
		{86_400_000, "1d"},
	}
	for _, c := range cases {
		candles := []Candle{{Time: 1000}, {Time: 1000 + c.deltaMs}}
		if got := inferTimeframe(candles); got != c.want {
			t.Errorf("inferTimeframe(delta=%dms) = %q, want %q", c.deltaMs, got, c.want)
		}
	}
}

func TestInferTimeframe_TooFewCandles(t *testing.T) {
	if got := inferTimeframe(nil); got != "" {
		t.Errorf("inferTimeframe(nil) = %q, want empty", got)
	}
	if got := inferTimeframe([]Candle{{Time: 1000}}); got != "" {
		t.Errorf("inferTimeframe(1 candle) = %q, want empty", got)
	}
}

func TestInferTimeframe_UnknownSpacing(t *testing.T) {
	candles := []Candle{{Time: 0}, {Time: 12345}}
	if got := inferTimeframe(candles); got != "" {
		t.Errorf("inferTimeframe(unrecognized delta) = %q, want empty", got)
	}
}

func TestDeriveTimesfmState(t *testing.T) {
	cases := []struct {
		predictedPct, thresholdPct float64
		want                       State
	}{
		{2.0, 0.5, Buy},
		{0.5, 0.5, Buy},   // exactly at threshold counts as Buy
		{-2.0, 0.5, Sell},
		{-0.5, 0.5, Sell}, // exactly at -threshold counts as Sell
		{0.2, 0.5, Neutral},
		{-0.2, 0.5, Neutral},
		{0, 0.5, Neutral},
	}
	for _, c := range cases {
		if got := deriveTimesfmState(c.predictedPct, c.thresholdPct); got != c.want {
			t.Errorf("deriveTimesfmState(%.2f, %.2f) = %v, want %v", c.predictedPct, c.thresholdPct, got, c.want)
		}
	}
}

func TestTimesfmSignal_ComputeWithSymbol_ColdCache_TriggersRefreshOnce(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	calls := make(chan struct{}, 10)
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) {
		calls <- struct{}{}
	}

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 150)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0} // 5m spacing
	}

	// Fire 5 concurrent calls for the same symbol — only one refresh must start.
	done := make(chan State, 5)
	for i := 0; i < 5; i++ {
		go func() { done <- s.ComputeWithSymbol("TFSIG_COLD", candles) }()
	}
	for i := 0; i < 5; i++ {
		<-done
	}

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("TimesfmRefreshFunc was never called")
	}
	select {
	case <-calls:
		t.Fatal("TimesfmRefreshFunc was called more than once for concurrent requests on the same key")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTimesfmSignal_ComputeWithSymbol_FreshCache_DoesNotRefresh(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	SetTimesfmForecast("TFSIG_FRESH", "5m", 100, 12, 1.0)
	called := false
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) { called = true }

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 150)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0}
	}

	got := s.ComputeWithSymbol("TFSIG_FRESH", candles)
	if called {
		t.Error("TimesfmRefreshFunc was called even though the cache entry is fresh")
	}
	if got != Buy {
		t.Errorf("state = %v, want Buy (predictedPct=1.0 >= threshold=0.5)", got)
	}
}

func TestTimesfmSignal_ComputeWithSymbol_UnrecognizedSpacing_ReturnsNeutral(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()
	called := false
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) { called = true }

	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := []Candle{{Time: 0, Close: 1.0}, {Time: 12345, Close: 1.0}}

	if got := s.ComputeWithSymbol("TFSIG_BADTF", candles); got != Neutral {
		t.Errorf("state = %v, want Neutral for unrecognized candle spacing", got)
	}
	if called {
		t.Error("TimesfmRefreshFunc was called despite an unrecognized timeframe")
	}
}

// TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars is the
// regression for a cache-key mismatch bug caught in code review before this task was ever
// implemented: if ComputeWithSymbol has fewer candles available than s.contextBars (e.g. a
// symbol/timeframe that hasn't accumulated enough history yet), it must still pass the
// CONFIGURED contextBars (not len(candles)) to TimesfmRefreshFunc — otherwise the eventual
// SetTimesfmForecast call (made by the refresh function, in a later task) would cache its
// result under a different key than every future GetTimesfmForecast call ever queries,
// permanently missing the cache for that symbol/timeframe.
func TestTimesfmSignal_ComputeWithSymbol_ShortHistory_PassesConfiguredContextBars(t *testing.T) {
	orig := TimesfmRefreshFunc
	defer func() { TimesfmRefreshFunc = orig }()

	var gotContextBars int
	var gotCandleLen int
	calls := make(chan struct{}, 1)
	TimesfmRefreshFunc = func(symbol, timeframe string, candles []Candle, contextBars, horizonBars int) {
		gotContextBars = contextBars
		gotCandleLen = len(candles)
		calls <- struct{}{}
	}

	// contextBars=100 configured, but only 40 candles actually available — shorter history.
	s := &timesfmSignal{contextBars: 100, horizonBars: 12, thresholdPct: 0.5, refreshIntervalSec: 300}
	candles := make([]Candle, 40)
	for i := range candles {
		candles[i] = Candle{Time: int64(i) * 300_000, Close: 1.0} // 5m spacing
	}

	s.ComputeWithSymbol("TFSIG_SHORTHIST", candles)

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("TimesfmRefreshFunc was never called")
	}
	if gotContextBars != 100 {
		t.Errorf("contextBars passed to TimesfmRefreshFunc = %d, want 100 (the configured value, not len(candles))", gotContextBars)
	}
	if gotCandleLen != 40 {
		t.Errorf("len(candles) passed to TimesfmRefreshFunc = %d, want 40 (all available candles, unpadded)", gotCandleLen)
	}
}
