// pkg/indicators/volume.go
package indicators

import "sis/pkg/models"

// Volume extracts the Volume field from each candle as a float64 slice.
// All values are valid (no NaN prefix).
func Volume(candles []models.Candle) []float64 {
	out := make([]float64, len(candles))
	for i, c := range candles {
		out[i] = c.Volume
	}
	return out
}
