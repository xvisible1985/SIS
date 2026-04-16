// pkg/indicators/ema_sma.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// SMA computes the Simple Moving Average over `period` candles.
// Returns a slice of the same length as candles; first period-1 values are NaN.
func SMA(candles []models.Candle, period int) []float64 {
	out := make([]float64, len(candles))
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 || len(candles) < period {
		return out
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	out[period-1] = sum / float64(period)
	for i := period; i < len(candles); i++ {
		sum += candles[i].Close - candles[i-period].Close
		out[i] = sum / float64(period)
	}
	return out
}

// EMA computes the Exponential Moving Average over `period` candles.
// Returns a slice of the same length as candles; first period-1 values are NaN.
func EMA(candles []models.Candle, period int) []float64 {
	out := make([]float64, len(candles))
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 || len(candles) < period {
		return out
	}
	k := 2.0 / float64(period+1)
	// Seed with SMA of first `period` values
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	out[period-1] = sum / float64(period)
	for i := period; i < len(candles); i++ {
		out[i] = candles[i].Close*k + out[i-1]*(1-k)
	}
	return out
}

// EMAFromValues computes EMA on a float64 slice (used internally by MACD, etc.)
// It skips any leading NaN values before seeding, so MACD signal/histogram
// are computed correctly even when the input macdLine has a NaN prefix.
func EMAFromValues(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 {
		return out
	}

	// Find the first valid (non-NaN) index
	start := -1
	for i, v := range values {
		if !math.IsNaN(v) {
			start = i
			break
		}
	}
	if start < 0 || len(values)-start < period {
		return out
	}

	k := 2.0 / float64(period+1)
	sum := 0.0
	for i := start; i < start+period; i++ {
		sum += values[i]
	}
	out[start+period-1] = sum / float64(period)
	for i := start + period; i < len(values); i++ {
		if math.IsNaN(values[i]) {
			out[i] = math.NaN()
			continue
		}
		out[i] = values[i]*k + out[i-1]*(1-k)
	}
	return out
}
