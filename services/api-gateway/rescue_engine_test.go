package main

import (
	"math"
	"testing"
)

// TestRescuePriceMoveTowardMainPct проверяет расчёт движения цены в сторону
// мейн-позиции (т.е. против неё — убыток для мейна).
func TestRescuePriceMoveTowardMainPct(t *testing.T) {
	cases := []struct {
		name      string
		entryAt   float64
		currentAt float64
		mainDir   string // "buy" = long, "sell" = short
		wantPct   float64
	}{
		{
			// Long main, price fell 5% from entry → движение к убытку = 5%
			name:      "long: price down 5pct",
			entryAt:   100,
			currentAt: 95,
			mainDir:   "buy",
			wantPct:   5.0,
		},
		{
			// Long main, price rose → движение в сторону прибыли, возвращаем 0
			name:      "long: price up — no adverse move",
			entryAt:   100,
			currentAt: 110,
			mainDir:   "buy",
			wantPct:   0,
		},
		{
			// Short main, price rose 10% from entry → убыток для мейна = 10%
			name:      "short: price up 10pct",
			entryAt:   100,
			currentAt: 110,
			mainDir:   "sell",
			wantPct:   10.0,
		},
		{
			// Short main, price fell → в сторону прибыли
			name:      "short: price down — no adverse move",
			entryAt:   100,
			currentAt: 90,
			mainDir:   "sell",
			wantPct:   0,
		},
		{
			name:    "zero entry returns 0",
			entryAt: 0, currentAt: 90, mainDir: "buy",
			wantPct: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rescuePriceMoveTowardMainPct(tc.entryAt, tc.currentAt, tc.mainDir)
			if math.Abs(got-tc.wantPct) > 1e-9 {
				t.Errorf("want %.6f, got %.6f", tc.wantPct, got)
			}
		})
	}
}

// TestRescuePriceLevelReached проверяет пересечение ценового уровня в зависимости
// от направления мейн-позиции.
func TestRescuePriceLevelReached(t *testing.T) {
	cases := []struct {
		name      string
		level     float64
		currentAt float64
		mainDir   string
		want      bool
	}{
		// Long main: уровень below entry — price falls to/past it → reached
		{"long: price at level", 90, 90, "buy", true},
		{"long: price below level", 90, 85, "buy", true},
		{"long: price above level", 90, 95, "buy", false},
		// Short main: уровень above entry — price rises to/past it → reached
		{"short: price at level", 110, 110, "sell", true},
		{"short: price above level", 110, 115, "sell", true},
		{"short: price below level", 110, 105, "sell", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rescuePriceLevelReached(tc.level, tc.currentAt, tc.mainDir)
			if got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

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
