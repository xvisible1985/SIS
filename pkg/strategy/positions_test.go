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
