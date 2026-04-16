// pkg/indicators/indicators_test.go
package indicators_test

import (
	"math"
	"testing"
	"time"

	"sis/pkg/indicators"
	"sis/pkg/models"
)

// makeCandles creates synthetic candles with linearly increasing Close prices.
func makeCandles(n int, startClose float64) []models.Candle {
	candles := make([]models.Candle, n)
	for i := range candles {
		c := startClose + float64(i)
		candles[i] = models.Candle{
			OpenTime: time.Now().Add(time.Duration(i) * time.Minute),
			Open:     c - 0.5,
			High:     c + 1,
			Low:      c - 1,
			Close:    c,
			Volume:   100 + float64(i),
		}
	}
	return candles
}

func TestSMA_Basic(t *testing.T) {
	candles := makeCandles(5, 10)
	// Closes: 10, 11, 12, 13, 14
	sma := indicators.SMA(candles, 3)

	if !math.IsNaN(sma[0]) || !math.IsNaN(sma[1]) {
		t.Error("first two values should be NaN")
	}
	// SMA(3) at index 2 = (10+11+12)/3 = 11
	if math.Abs(sma[2]-11.0) > 1e-9 {
		t.Errorf("sma[2]: got %v, want 11", sma[2])
	}
	// SMA(3) at index 4 = (12+13+14)/3 = 13
	if math.Abs(sma[4]-13.0) > 1e-9 {
		t.Errorf("sma[4]: got %v, want 13", sma[4])
	}
}

func TestEMA_Converges(t *testing.T) {
	candles := makeCandles(30, 100)
	ema := indicators.EMA(candles, 9)

	// First 8 values must be NaN
	for i := 0; i < 8; i++ {
		if !math.IsNaN(ema[i]) {
			t.Errorf("ema[%d] should be NaN", i)
		}
	}
	// EMA should be valid and positive from index 8 onward
	for i := 8; i < len(ema); i++ {
		if math.IsNaN(ema[i]) || ema[i] <= 0 {
			t.Errorf("ema[%d] invalid: %v", i, ema[i])
		}
	}
}

func TestRSI_Range(t *testing.T) {
	candles := makeCandles(50, 100)
	rsi := indicators.RSI(candles, 14)

	// First 14 must be NaN
	for i := 0; i < 14; i++ {
		if !math.IsNaN(rsi[i]) {
			t.Errorf("rsi[%d] should be NaN", i)
		}
	}
	// RSI must be in [0, 100]
	for i := 14; i < len(rsi); i++ {
		if math.IsNaN(rsi[i]) || rsi[i] < 0 || rsi[i] > 100 {
			t.Errorf("rsi[%d] = %v out of range [0,100]", i, rsi[i])
		}
	}
	// Linearly increasing prices → RSI should be high (above 50)
	last := rsi[len(rsi)-1]
	if last < 50 {
		t.Errorf("RSI for rising prices should be > 50, got %v", last)
	}
}

func TestRSI_FlatPrices(t *testing.T) {
	candles := make([]models.Candle, 30)
	for i := range candles {
		candles[i] = models.Candle{Close: 100, High: 101, Low: 99, Volume: 100}
	}
	rsi := indicators.RSI(candles, 14)
	for i := 14; i < len(rsi); i++ {
		if math.IsNaN(rsi[i]) {
			t.Errorf("rsi[%d] should not be NaN for flat prices", i)
		}
	}
}

func TestMACD_Basic(t *testing.T) {
	candles := makeCandles(60, 100)
	result := indicators.MACD(candles, 12, 26, 9)

	// Indices before slow EMA is seeded should be NaN
	if !math.IsNaN(result.MACD[24]) {
		t.Error("MACD[24] should be NaN (slow EMA not yet seeded)")
	}
	// After enough bars, values should be valid
	validFrom := 26 + 9 - 2 // approx
	for i := validFrom; i < len(result.MACD); i++ {
		if math.IsNaN(result.MACD[i]) {
			t.Errorf("MACD[%d] unexpected NaN", i)
			break
		}
	}
	// Signal line should also be valid after enough bars
	for i := validFrom; i < len(result.Signal); i++ {
		if math.IsNaN(result.Signal[i]) {
			t.Errorf("Signal[%d] unexpected NaN", i)
			break
		}
	}
	// Histogram should also be valid
	for i := validFrom; i < len(result.Histogram); i++ {
		if math.IsNaN(result.Histogram[i]) {
			t.Errorf("Histogram[%d] unexpected NaN", i)
			break
		}
	}
}

func TestBollingerBands_Width(t *testing.T) {
	candles := makeCandles(30, 100)
	bb := indicators.BollingerBands(candles, 20, 2.0)

	// Before period is reached values are NaN
	if !math.IsNaN(bb.Upper[18]) {
		t.Error("Upper[18] should be NaN")
	}
	// After period, Upper > Middle > Lower
	for i := 19; i < len(candles); i++ {
		if math.IsNaN(bb.Upper[i]) {
			continue
		}
		if !(bb.Upper[i] > bb.Middle[i] && bb.Middle[i] > bb.Lower[i]) {
			t.Errorf("band ordering violated at i=%d: U=%v M=%v L=%v", i, bb.Upper[i], bb.Middle[i], bb.Lower[i])
		}
	}
}

func TestATR_Positive(t *testing.T) {
	candles := makeCandles(30, 100)
	atr := indicators.ATR(candles, 14)

	for i := 14; i < len(atr); i++ {
		if math.IsNaN(atr[i]) || atr[i] <= 0 {
			t.Errorf("ATR[%d] should be positive, got %v", i, atr[i])
		}
	}
}

func TestStochastic_Range(t *testing.T) {
	candles := makeCandles(30, 100)
	stoch := indicators.Stochastic(candles, 14, 3)

	for i := 13; i < len(stoch.K); i++ {
		if math.IsNaN(stoch.K[i]) {
			continue
		}
		if stoch.K[i] < 0 || stoch.K[i] > 100 {
			t.Errorf("K[%d] = %v out of range [0,100]", i, stoch.K[i])
		}
	}
}

func TestVolume(t *testing.T) {
	candles := makeCandles(5, 100)
	vol := indicators.Volume(candles)
	if len(vol) != 5 {
		t.Fatalf("expected 5 volumes, got %d", len(vol))
	}
	for i, v := range vol {
		if v != candles[i].Volume {
			t.Errorf("volume[%d]: got %v, want %v", i, v, candles[i].Volume)
		}
	}
}

func TestEMAFromValues_NaNPrefix(t *testing.T) {
	// Simulate MACD's macdLine: 25 NaN values, then valid values
	values := make([]float64, 40)
	for i := 0; i < 25; i++ {
		values[i] = math.NaN()
	}
	for i := 25; i < 40; i++ {
		values[i] = float64(i - 25) // 0, 1, 2, ...
	}
	out := indicators.EMAFromValues(values, 9)

	// First 25 + 8 = 33 values should be NaN
	for i := 0; i < 33; i++ {
		if !math.IsNaN(out[i]) {
			t.Errorf("out[%d] should be NaN, got %v", i, out[i])
		}
	}
	// From index 33 onward should be valid
	for i := 33; i < len(out); i++ {
		if math.IsNaN(out[i]) {
			t.Errorf("out[%d] unexpected NaN", i)
		}
	}
}
