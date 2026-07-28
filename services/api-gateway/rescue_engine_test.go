package main

import (
	"math"
	"testing"
	"time"
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

// TestRescueTriggersMet проверяет AND-логику проверки всех активных триггеров.
func TestRescueTriggersMet(t *testing.T) {
	pctThresh := 5.0
	levelThresh := 90.0
	accumThresh := 20.0

	cases := []struct {
		name    string
		cfg     botCfgJSON
		current float64
		entry   float64
		mainDir string
		accum   float64
		want    bool
	}{
		{
			name: "no triggers configured → always true (gate is elsewhere)",
			cfg:  botCfgJSON{RescuePartialCloseEnabled: true},
			want: true,
		},
		{
			name: "price move trigger met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &pctThresh},
			// long: entry=100, current=93 → move=7% ≥ 5%
			entry: 100, current: 93, mainDir: "buy",
			want: true,
		},
		{
			name: "price move trigger not met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &pctThresh},
			// long: entry=100, current=97 → move=3% < 5%
			entry: 100, current: 97, mainDir: "buy",
			want: false,
		},
		{
			name: "price level trigger met",
			cfg:  botCfgJSON{RescueTriggerPriceLevel: &levelThresh},
			// long: current=88 ≤ 90
			entry: 100, current: 88, mainDir: "buy",
			want: true,
		},
		{
			name: "price level trigger not met",
			cfg:  botCfgJSON{RescueTriggerPriceLevel: &levelThresh},
			// long: current=95 > 90
			entry: 100, current: 95, mainDir: "buy",
			want: false,
		},
		{
			name: "accumulated trigger met",
			cfg:  botCfgJSON{RescueTriggerAccumulatedMinUsdt: &accumThresh},
			accum: 25,
			want:  true,
		},
		{
			name: "accumulated trigger not met",
			cfg:  botCfgJSON{RescueTriggerAccumulatedMinUsdt: &accumThresh},
			accum: 10,
			want:  false,
		},
		{
			name: "all three conditions, all met",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:       &pctThresh,
				RescueTriggerPriceLevel:         &levelThresh,
				RescueTriggerAccumulatedMinUsdt: &accumThresh,
			},
			entry: 100, current: 85, mainDir: "buy",
			accum: 30,
			want:  true,
		},
		{
			name: "all three conditions, accum not met",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:       &pctThresh,
				RescueTriggerPriceLevel:         &levelThresh,
				RescueTriggerAccumulatedMinUsdt: &accumThresh,
			},
			entry: 100, current: 85, mainDir: "buy",
			accum: 5, // < 20
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Signal trigger is tested separately (requires signal.Engine); pass nil here.
			got := rescueTriggersMet(tc.cfg, tc.current, tc.entry, tc.mainDir, tc.accum, false)
			if got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// TestRescueCooldownElapsed проверяет кулдаун между шагами частичного закрытия.
func TestRescueCooldownElapsed(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name           string
		lastClosedAgo  int // seconds ago; 0 = nil (never closed)
		minIntervalSec int
		want           bool
	}{
		{"never closed → elapsed", 0, 300, true},
		{"closed 400s ago, interval 300 → elapsed", 400, 300, true},
		{"closed 100s ago, interval 300 → not elapsed", 100, 300, false},
		{"closed exactly interval ago → elapsed (>=)", 300, 300, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lastAt *time.Time
			if tc.lastClosedAgo > 0 {
				ts := now.Add(-time.Duration(tc.lastClosedAgo) * time.Second)
				lastAt = &ts
			}
			got := rescueCooldownElapsed(lastAt, tc.minIntervalSec)
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
