// pkg/indicators/rsi.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// RSI computes the Relative Strength Index using Wilder's smoothing.
// Returns a slice of the same length as candles; first period values are NaN.
func RSI(candles []models.Candle, period int) []float64 {
	out := make([]float64, len(candles))
	for i := range out {
		out[i] = math.NaN()
	}
	if period <= 0 || len(candles) <= period {
		return out
	}

	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		change := candles[i].Close - candles[i-1].Close
		if change > 0 {
			avgGain += change
		} else {
			avgLoss -= change
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	rs := func(g, l float64) float64 {
		if l == 0 {
			return 100
		}
		return g / l
	}
	out[period] = 100 - 100/(1+rs(avgGain, avgLoss))

	for i := period + 1; i < len(candles); i++ {
		change := candles[i].Close - candles[i-1].Close
		gain, loss := 0.0, 0.0
		if change > 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		out[i] = 100 - 100/(1+rs(avgGain, avgLoss))
	}
	return out
}
