// pkg/indicators/stochastic.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// StochasticResult holds %K and %D series.
type StochasticResult struct {
	K []float64
	D []float64 // SMA(K, 3) by default
}

// Stochastic computes the Stochastic Oscillator.
// kPeriod: lookback for highest-high/lowest-low (default 14).
// dPeriod: smoothing for %D (default 3).
func Stochastic(candles []models.Candle, kPeriod, dPeriod int) StochasticResult {
	n := len(candles)
	nan := func() []float64 {
		s := make([]float64, n)
		for i := range s {
			s[i] = math.NaN()
		}
		return s
	}
	result := StochasticResult{K: nan(), D: nan()}
	if kPeriod <= 0 || n < kPeriod {
		return result
	}

	for i := kPeriod - 1; i < n; i++ {
		hh, ll := candles[i].High, candles[i].Low
		for j := i - kPeriod + 1; j < i; j++ {
			if candles[j].High > hh {
				hh = candles[j].High
			}
			if candles[j].Low < ll {
				ll = candles[j].Low
			}
		}
		if hh == ll {
			result.K[i] = 50
		} else {
			result.K[i] = (candles[i].Close - ll) / (hh - ll) * 100
		}
	}

	for i := kPeriod - 1 + dPeriod - 1; i < n; i++ {
		sum := 0.0
		allValid := true
		for j := i - dPeriod + 1; j <= i; j++ {
			if math.IsNaN(result.K[j]) {
				allValid = false
				break
			}
			sum += result.K[j]
		}
		if allValid {
			result.D[i] = sum / float64(dPeriod)
		}
	}
	return result
}
