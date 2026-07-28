package main

import (
	"math"
	"testing"
)

// TestRescueCalcPartialCloseQty проверяет конвертацию доступного PnL → объём
// частичного закрытия с учётом qtyStep и minQty.
func TestRescueCalcPartialCloseQty(t *testing.T) {
	cases := []struct {
		name            string
		accumulatedPnl  float64
		mainReducedUsdt float64
		price           float64
		qtyStep         float64
		minQty          float64
		wantQty         float64 // 0 means "expect zero / insufficient"
	}{
		{
			name:            "basic: 50 usdt PnL, price 50000, step 0.001",
			accumulatedPnl:  50,
			mainReducedUsdt: 0,
			price:           50000,
			qtyStep:         0.001,
			minQty:          0.001,
			// availableUsdt=50, coinQty=0.001000, floor to 3dp → 0.001
			wantQty: 0.001,
		},
		{
			name:            "already reduced 30 of 50",
			accumulatedPnl:  50,
			mainReducedUsdt: 30,
			price:           50000,
			qtyStep:         0.001,
			minQty:          0.001,
			// available=20, qty=0.0004, floor to 0.000 → 0 (below minQty)
			wantQty: 0,
		},
		{
			name:            "qty step rounding down",
			accumulatedPnl:  200,
			mainReducedUsdt: 0,
			price:           50000,
			qtyStep:         0.001,
			minQty:          0.001,
			// qty=0.004000, floor to 3dp → 0.004
			wantQty: 0.004,
		},
		{
			name:            "accumulated less than already reduced — zero",
			accumulatedPnl:  10,
			mainReducedUsdt: 20,
			price:           50000,
			qtyStep:         0.001,
			minQty:          0.001,
			wantQty:         0,
		},
		{
			name:            "zero price returns 0",
			accumulatedPnl:  100,
			mainReducedUsdt: 0,
			price:           0,
			qtyStep:         0.001,
			minQty:          0.001,
			wantQty:         0,
		},
		{
			name:            "low-price altcoin, larger qty",
			accumulatedPnl:  100,
			mainReducedUsdt: 0,
			price:           0.5,
			qtyStep:         1,
			minQty:          1,
			// qty=200 coin, floor to step=1 → 200
			wantQty: 200,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rescueCalcPartialCloseQty(tc.accumulatedPnl, tc.mainReducedUsdt, tc.price, tc.qtyStep, tc.minQty)
			if math.Abs(got-tc.wantQty) > 1e-9 {
				t.Errorf("want %.8f, got %.8f", tc.wantQty, got)
			}
		})
	}
}
