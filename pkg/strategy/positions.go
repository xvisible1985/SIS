package strategy

import (
	"strconv"

	"sis/pkg/trader"
)

// positionOpenOnExchange reports whether a position for the given strategy leg is
// still open in an exchange position snapshot. In hedge mode it matches the leg's
// positionIdx (long=1, short=2); in one-way mode any matching-symbol position with
// size>0 counts.
//
// This is the authoritative confirmation used before treating a WS size=0 event as
// a genuine external close: Bybit can emit spurious/stale size=0 snapshots (notably
// during reconnects or gateway restarts), and acting on one would wrongly terminate
// a strategy whose position is actually still open.
// exchangeAvgEntry returns the average entry price of the matching open position from an
// exchange snapshot (symbol, and positionIdx in hedge mode), or `computed` when no such
// open position is present. This is the authoritative anchor for TP/SL, replacing the
// internally computed VWAP which can drift from the real position.
func exchangeAvgEntry(positions []trader.Position, symbol string, hedgeMode bool, wantIdx int, computed float64) float64 {
	for _, p := range positions {
		if p.Symbol != symbol {
			continue
		}
		if hedgeMode && p.PositionIdx != wantIdx {
			continue
		}
		size, _ := strconv.ParseFloat(p.Size, 64)
		entry, _ := strconv.ParseFloat(p.EntryPrice, 64)
		if size > 0 && entry > 0 {
			return entry
		}
	}
	return computed
}

func positionOpenOnExchange(positions []trader.Position, symbol string, hedgeMode bool, dir Direction) bool {
	wantIdx := positionIdxForClose(hedgeMode, dir)
	for _, p := range positions {
		size, _ := strconv.ParseFloat(p.Size, 64)
		if p.Symbol != symbol || size == 0 {
			continue
		}
		if hedgeMode && p.PositionIdx != wantIdx {
			continue
		}
		return true
	}
	return false
}
