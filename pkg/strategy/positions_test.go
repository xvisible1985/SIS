package strategy

import (
	"testing"

	"sis/pkg/trader"
)

// TestPositionOpenOnExchange verifies the pure helper that decides, from an
// exchange position snapshot, whether a given strategy's position is still open.
// This guards the ghost/false-close path: a spurious size=0 WS event must not
// terminate a strategy whose position is actually still open on the exchange.
func TestPositionOpenOnExchange(t *testing.T) {
	cases := []struct {
		name      string
		positions []trader.Position
		symbol    string
		hedgeMode bool
		dir       Direction
		want      bool
	}{
		{
			name:      "one-way: open position present",
			positions: []trader.Position{{Symbol: "XLMUSDT", Size: "10", PositionIdx: 0}},
			symbol:    "XLMUSDT", hedgeMode: false, dir: DirectionLong,
			want: true,
		},
		{
			name:      "one-way: no position",
			positions: []trader.Position{},
			symbol:    "XLMUSDT", hedgeMode: false, dir: DirectionLong,
			want: false,
		},
		{
			name:      "one-way: zero-size snapshot is not open",
			positions: []trader.Position{{Symbol: "XLMUSDT", Size: "0", PositionIdx: 0}},
			symbol:    "XLMUSDT", hedgeMode: false, dir: DirectionLong,
			want: false,
		},
		{
			name: "hedge: short leg open (idx 2), long leg empty",
			positions: []trader.Position{
				{Symbol: "XLMUSDT", Size: "0", PositionIdx: 1},
				{Symbol: "XLMUSDT", Size: "5", PositionIdx: 2},
			},
			symbol: "XLMUSDT", hedgeMode: true, dir: DirectionShort,
			want: true,
		},
		{
			name: "hedge: querying long leg while only short is open → not open",
			positions: []trader.Position{
				{Symbol: "XLMUSDT", Size: "5", PositionIdx: 2},
			},
			symbol: "XLMUSDT", hedgeMode: true, dir: DirectionLong,
			want: false,
		},
		{
			name:      "different symbol ignored",
			positions: []trader.Position{{Symbol: "BTCUSDT", Size: "10", PositionIdx: 0}},
			symbol:    "XLMUSDT", hedgeMode: false, dir: DirectionLong,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := positionOpenOnExchange(c.positions, c.symbol, c.hedgeMode, c.dir)
			if got != c.want {
				t.Errorf("positionOpenOnExchange() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestExchangeAvgEntry(t *testing.T) {
	pos := []trader.Position{
		{Symbol: "MIRAUSDT", Side: "Sell", Size: "518.5", EntryPrice: "0.04236", PositionIdx: 2},
		{Symbol: "MIRAUSDT", Side: "Buy", Size: "100", EntryPrice: "0.05", PositionIdx: 1},
	}
	// hedge short → positionIdx 2 → 0.04236 (the real exchange avg, not the computed drift)
	if got := exchangeAvgEntry(pos, "MIRAUSDT", true, 2, 0.0473); got != 0.04236 {
		t.Errorf("hedge short avg = %v, want 0.04236", got)
	}
	// hedge long → positionIdx 1 → 0.05
	if got := exchangeAvgEntry(pos, "MIRAUSDT", true, 1, 0.06); got != 0.05 {
		t.Errorf("hedge long avg = %v, want 0.05", got)
	}
	// no matching open position → computed fallback
	if got := exchangeAvgEntry(nil, "MIRAUSDT", true, 2, 0.0473); got != 0.0473 {
		t.Errorf("no position → fallback = %v, want 0.0473", got)
	}
	// zero-size position ignored → fallback
	zero := []trader.Position{{Symbol: "MIRAUSDT", Side: "Sell", Size: "0", EntryPrice: "0.04", PositionIdx: 2}}
	if got := exchangeAvgEntry(zero, "MIRAUSDT", true, 2, 0.0473); got != 0.0473 {
		t.Errorf("zero-size → fallback = %v, want 0.0473", got)
	}
	// one-way mode ignores positionIdx
	ow := []trader.Position{{Symbol: "AAAUSDT", Side: "Buy", Size: "10", EntryPrice: "1.5", PositionIdx: 0}}
	if got := exchangeAvgEntry(ow, "AAAUSDT", false, 0, 2.0); got != 1.5 {
		t.Errorf("one-way avg = %v, want 1.5", got)
	}
}
