// services/signal-engine/patterns.go
package main

import (
	"fmt"
	"math"

	"sis/pkg/indicators"
	"sis/pkg/models"
)

// PatternStats holds win-rate and trade count for a pattern group.
type PatternStats struct {
	Label   string  `json:"label"`
	WinRate float64 `json:"win_rate"`
	Count   int     `json:"count"`
	AvgGain float64 `json:"avg_gain"`
}

// PatternReport contains all detected pattern statistics.
type PatternReport struct {
	ByDayOfWeek  []PatternStats `json:"by_day_of_week"`
	ByHourOfDay  []PatternStats `json:"by_hour_of_day"`
	ByMarketMode []PatternStats `json:"by_market_mode"` // trend | sideways | volatile
}

// DetectPatterns analyses trades for temporal and market-mode patterns.
// candles must be the same candle series used during backtesting (for ATR-based regime).
func DetectPatterns(trades []Trade, candles []models.Candle) PatternReport {
	if len(trades) == 0 {
		return PatternReport{}
	}

	// Market regime classification using ATR
	regimeMap := classifyMarketRegime(candles)

	// Group trades
	type group struct {
		wins, count int
		gainSum     float64
	}
	days := make(map[string]*group)
	hours := make(map[int]*group)
	modes := make(map[string]*group)

	for _, t := range trades {
		if t.Result == "open" {
			continue // exclude unresolved trades from pattern stats
		}

		isWin := t.Result == "win"

		// Day of week
		d := t.EntryTime.Weekday().String()
		if days[d] == nil {
			days[d] = &group{}
		}
		days[d].count++
		days[d].gainSum += t.GainPct
		if isWin {
			days[d].wins++
		}

		// Hour of day
		h := t.EntryTime.Hour()
		if hours[h] == nil {
			hours[h] = &group{}
		}
		hours[h].count++
		hours[h].gainSum += t.GainPct
		if isWin {
			hours[h].wins++
		}

		// Market mode
		mode := "unknown"
		if m, ok := regimeMap[t.EntryTime.Unix()]; ok {
			mode = m
		}
		if modes[mode] == nil {
			modes[mode] = &group{}
		}
		modes[mode].count++
		modes[mode].gainSum += t.GainPct
		if isWin {
			modes[mode].wins++
		}
	}

	toStats := func(label string, g *group) PatternStats {
		wr := 0.0
		if g.count > 0 {
			wr = float64(g.wins) / float64(g.count)
		}
		avg := 0.0
		if g.count > 0 {
			avg = g.gainSum / float64(g.count)
		}
		return PatternStats{Label: label, WinRate: wr, Count: g.count, AvgGain: avg}
	}

	report := PatternReport{}
	weekdays := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	for _, d := range weekdays {
		if g, ok := days[d]; ok {
			report.ByDayOfWeek = append(report.ByDayOfWeek, toStats(d, g))
		}
	}
	for h := 0; h < 24; h++ {
		if g, ok := hours[h]; ok {
			report.ByHourOfDay = append(report.ByHourOfDay, toStats(
				fmt.Sprintf("%02d:00", h), g,
			))
		}
	}
	for mode, g := range modes {
		report.ByMarketMode = append(report.ByMarketMode, toStats(mode, g))
	}

	return report
}

// classifyMarketRegime uses ATR to label each candle's timestamp as "trend", "sideways", or "volatile".
// Returns a map of unix timestamp → regime label.
func classifyMarketRegime(candles []models.Candle) map[int64]string {
	result := make(map[int64]string, len(candles))
	if len(candles) < 20 {
		return result
	}

	atr := indicators.ATR(candles, 14)
	sma20 := indicators.SMA(candles, 20)

	for i, c := range candles {
		if math.IsNaN(atr[i]) || math.IsNaN(sma20[i]) || sma20[i] == 0 {
			continue
		}
		atrPct := atr[i] / sma20[i] * 100

		var mode string
		switch {
		case atrPct > 3.0:
			mode = "volatile"
		case atrPct > 1.0:
			mode = "trend"
		default:
			mode = "sideways"
		}
		result[c.OpenTime.Unix()] = mode
	}
	return result
}
