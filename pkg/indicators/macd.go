// pkg/indicators/macd.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// MACDResult holds the three MACD series.
type MACDResult struct {
	MACD      []float64 // fast EMA − slow EMA
	Signal    []float64 // EMA of MACD
	Histogram []float64 // MACD − Signal
}

// MACD computes the Moving Average Convergence/Divergence indicator.
// Standard params: fast=12, slow=26, signal=9.
// Returns slices of the same length as candles; leading values are NaN.
func MACD(candles []models.Candle, fast, slow, signalPeriod int) MACDResult {
	n := len(candles)
	nan := func() []float64 {
		s := make([]float64, n)
		for i := range s {
			s[i] = math.NaN()
		}
		return s
	}
	result := MACDResult{MACD: nan(), Signal: nan(), Histogram: nan()}
	if fast <= 0 || slow <= 0 || signalPeriod <= 0 || slow > n {
		return result
	}

	fastEMA := EMA(candles, fast)
	slowEMA := EMA(candles, slow)

	macdLine := make([]float64, n)
	for i := range macdLine {
		macdLine[i] = math.NaN()
	}
	for i := slow - 1; i < n; i++ {
		if !math.IsNaN(fastEMA[i]) && !math.IsNaN(slowEMA[i]) {
			macdLine[i] = fastEMA[i] - slowEMA[i]
		}
	}

	signalLine := EMAFromValues(macdLine, signalPeriod)

	result.MACD = macdLine
	result.Signal = signalLine
	for i := range result.Histogram {
		if !math.IsNaN(macdLine[i]) && !math.IsNaN(signalLine[i]) {
			result.Histogram[i] = macdLine[i] - signalLine[i]
		}
	}
	return result
}
