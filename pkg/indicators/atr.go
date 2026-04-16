// pkg/indicators/atr.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// ATR computes the Average True Range using Wilder's smoothing.
// Returns a slice of the same length as candles; first period values are NaN.
func ATR(candles []models.Candle, period int) []float64 {
	out := make([]float64, len(candles))
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 || len(candles) < period+1 {
		return out
	}

	trueRange := func(i int) float64 {
		high := candles[i].High
		low := candles[i].Low
		prevClose := candles[i-1].Close
		return math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
	}

	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trueRange(i)
	}
	out[period] = sum / float64(period)

	for i := period + 1; i < len(candles); i++ {
		out[i] = (out[i-1]*float64(period-1) + trueRange(i)) / float64(period)
	}
	return out
}
