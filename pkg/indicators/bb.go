// pkg/indicators/bb.go
package indicators

import (
	"math"

	"sis/pkg/models"
)

// BBResult holds the three Bollinger Band series.
type BBResult struct {
	Upper  []float64
	Middle []float64 // SMA
	Lower  []float64
}

// BollingerBands computes Bollinger Bands (SMA ± stdDev * multiplier).
// Standard params: period=20, multiplier=2.0.
func BollingerBands(candles []models.Candle, period int, multiplier float64) BBResult {
	n := len(candles)
	nan := func() []float64 {
		s := make([]float64, n)
		for i := range s {
			s[i] = math.NaN()
		}
		return s
	}
	result := BBResult{Upper: nan(), Middle: nan(), Lower: nan()}
	if period <= 0 || n < period {
		return result
	}

	sma := SMA(candles, period)
	result.Middle = sma

	for i := period - 1; i < n; i++ {
		if math.IsNaN(sma[i]) {
			continue
		}
		sum := 0.0
		for j := i - period + 1; j <= i; j++ {
			diff := candles[j].Close - sma[i]
			sum += diff * diff
		}
		std := math.Sqrt(sum / float64(period))
		result.Upper[i] = sma[i] + multiplier*std
		result.Lower[i] = sma[i] - multiplier*std
	}
	return result
}
