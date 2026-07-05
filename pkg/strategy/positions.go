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
