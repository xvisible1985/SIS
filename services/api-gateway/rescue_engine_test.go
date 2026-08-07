package main

import (
	"math"
	"testing"
	"time"
)

// TestRescuePriceMovePct проверяет знаковый расчёт движения цены относительно
// позиции: отрицательно = против позиции (убыток), положительно = в сторону прибыли.
func TestRescuePriceMovePct(t *testing.T) {
	cases := []struct {
		name      string
		entryAt   float64
		currentAt float64
		dir       string
		wantPct   float64
	}{
		{"long: price down 5pct → -5", 100, 95, "buy", -5.0},
		{"long: price up 10pct → +10", 100, 110, "buy", 10.0},
		{"long: at entry → 0", 100, 100, "buy", 0},
		{"short: price up 10pct → -10 (adverse)", 100, 110, "sell", -10.0},
		{"short: price down 10pct → +10 (profit)", 100, 90, "sell", 10.0},
		{"short: at entry → 0", 100, 100, "sell", 0},
		{"zero entry returns 0", 0, 90, "buy", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rescuePriceMovePct(tc.entryAt, tc.currentAt, tc.dir)
			if math.Abs(got-tc.wantPct) > 1e-9 {
				t.Errorf("want %.6f, got %.6f", tc.wantPct, got)
			}
		})
	}
}

// TestRescuePriceMoveExceeds проверяет знаковое пересечение порога.
func TestRescuePriceMoveExceeds(t *testing.T) {
	cases := []struct {
		movePct   float64
		threshold float64
		want      bool
	}{
		// Negative threshold: fire when loss ≥ |threshold|
		{-5, -5, true},  // ровно на пороге
		{-7, -5, true},  // хуже порога
		{-3, -5, false}, // не достигли
		{0, -5, false},  // прибыль — тем более нет
		{5, -5, false},  // прибыль — нет
		// Positive threshold: fire when profit ≥ threshold
		{5, 5, true},   // ровно на пороге
		{7, 5, true},   // больше порога
		{3, 5, false},  // не достигли
		{-3, 5, false}, // убыток — нет
		// Zero threshold: always true
		{-5, 0, true},
		{5, 0, true},
		{0, 0, true},
	}

	for _, tc := range cases {
		got := rescuePriceMoveExceeds(tc.movePct, tc.threshold)
		if got != tc.want {
			t.Errorf("rescuePriceMoveExceeds(%.1f, %.1f) = %v, want %v", tc.movePct, tc.threshold, got, tc.want)
		}
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
// Знак порога: отрицательный = против позиции (убыток), положительный = в сторону прибыли.
func TestRescueTriggersMet(t *testing.T) {
	mainAdversePct := -5.0  // срабатывает когда мэйн просел на 5%
	mainProfitPct := 2.0    // срабатывает когда мэйн в прибыли на 2%
	hedgeAdversePct := -3.0 // срабатывает когда хедж просел на 3%
	hedgeProfitPct := 4.0   // срабатывает когда хедж в прибыли на 4%
	levelThresh := 90.0
	accumThresh := 20.0

	cases := []struct {
		name         string
		cfg          botCfgJSON
		current      float64
		entry        float64
		mainDir      string
		accum        float64
		hedgeEntry   float64
		hedgeCurrent float64
		hedgeDir     string
		want         bool
	}{
		{
			name: "no triggers → always true",
			cfg:  botCfgJSON{RescuePartialCloseEnabled: true},
			want: true,
		},
		// Main price move (отрицательный порог = убыток)
		{
			name: "main adverse move met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &mainAdversePct},
			// long: entry=100, current=93 → movePct=-7 ≤ -5 → met
			entry: 100, current: 93, mainDir: "buy",
			want: true,
		},
		{
			name: "main adverse move not met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &mainAdversePct},
			// long: entry=100, current=97 → movePct=-3 > -5 → not met
			entry: 100, current: 97, mainDir: "buy",
			want: false,
		},
		{
			name: "main profit move met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &mainProfitPct},
			// long: entry=100, current=103 → movePct=+3 ≥ +2 → met
			entry: 100, current: 103, mainDir: "buy",
			want: true,
		},
		{
			name: "main profit move not met",
			cfg:  botCfgJSON{RescueTriggerPriceMovePct: &mainProfitPct},
			// long: entry=100, current=101 → movePct=+1 < +2 → not met
			entry: 100, current: 101, mainDir: "buy",
			want: false,
		},
		// Hedge price move
		{
			name: "hedge adverse move met",
			cfg:  botCfgJSON{RescueTriggerHedgePriceMovePct: &hedgeAdversePct},
			// hedge=short: entry=100, current=104 → movePct=(100-104)/100*100=-4 ≤ -3 → met
			hedgeEntry: 100, hedgeCurrent: 104, hedgeDir: "sell",
			want: true,
		},
		{
			name: "hedge adverse move not met",
			cfg:  botCfgJSON{RescueTriggerHedgePriceMovePct: &hedgeAdversePct},
			// hedge=short: entry=100, current=102 → movePct=-2 > -3 → not met
			hedgeEntry: 100, hedgeCurrent: 102, hedgeDir: "sell",
			want: false,
		},
		{
			name: "hedge profit move met",
			cfg:  botCfgJSON{RescueTriggerHedgePriceMovePct: &hedgeProfitPct},
			// hedge=short: entry=100, current=95 → movePct=(100-95)/100*100=+5 ≥ +4 → met
			hedgeEntry: 100, hedgeCurrent: 95, hedgeDir: "sell",
			want: true,
		},
		{
			name: "hedge profit move not met",
			cfg:  botCfgJSON{RescueTriggerHedgePriceMovePct: &hedgeProfitPct},
			// hedge=short: entry=100, current=97 → movePct=+3 < +4 → not met
			hedgeEntry: 100, hedgeCurrent: 97, hedgeDir: "sell",
			want: false,
		},
		// Price level
		{
			name: "price level trigger met",
			cfg:  botCfgJSON{RescueTriggerPriceLevel: &levelThresh},
			entry: 100, current: 88, mainDir: "buy",
			want: true,
		},
		{
			name: "price level trigger not met",
			cfg:  botCfgJSON{RescueTriggerPriceLevel: &levelThresh},
			entry: 100, current: 95, mainDir: "buy",
			want: false,
		},
		// Accumulated
		{
			name: "accumulated trigger met",
			cfg:  botCfgJSON{RescueTriggerAccumulatedMinUsdt: &accumThresh},
			accum: 25, want: true,
		},
		{
			name: "accumulated trigger not met",
			cfg:  botCfgJSON{RescueTriggerAccumulatedMinUsdt: &accumThresh},
			accum: 10, want: false,
		},
		// AND logic
		{
			name: "main+level+accum all met",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:       &mainAdversePct,
				RescueTriggerPriceLevel:         &levelThresh,
				RescueTriggerAccumulatedMinUsdt: &accumThresh,
			},
			entry: 100, current: 85, mainDir: "buy", accum: 30,
			want: true,
		},
		{
			name: "main+level+accum, accum not met",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:       &mainAdversePct,
				RescueTriggerPriceLevel:         &levelThresh,
				RescueTriggerAccumulatedMinUsdt: &accumThresh,
			},
			entry: 100, current: 85, mainDir: "buy", accum: 5,
			want: false,
		},
		{
			name: "main+hedge both met",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:      &mainAdversePct,
				RescueTriggerHedgePriceMovePct: &hedgeAdversePct,
			},
			// main long in -7% loss, hedge short in -4% loss
			entry: 100, current: 93, mainDir: "buy",
			hedgeEntry: 100, hedgeCurrent: 104, hedgeDir: "sell",
			want: true,
		},
		{
			name: "main met, hedge not met → false",
			cfg: botCfgJSON{
				RescueTriggerPriceMovePct:      &mainAdversePct,
				RescueTriggerHedgePriceMovePct: &hedgeAdversePct,
			},
			entry: 100, current: 93, mainDir: "buy",
			hedgeEntry: 100, hedgeCurrent: 102, hedgeDir: "sell", // hedge only -2% < -3%
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rescueTriggersMet(
				tc.cfg, tc.current, tc.entry, tc.mainDir, tc.accum, false,
				tc.hedgeEntry, tc.hedgeCurrent, tc.hedgeDir,
			)
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

// TestRescuePartialCloseRequest проверяет сборку запроса частичного закрытия.
func TestRescuePartialCloseRequest(t *testing.T) {
	mainAdversePct := -5.0 // убыток мэйна ≥ 5%
	accumMin := 20.0

	// helper: вызов с нулевыми hedge-данными (триггер хеджа не настроен)
	call := func(cfg botCfgJSON, symbol, mainDir string, current, entry, accum, reduced, step, minQ float64, last *time.Time, sig bool) *rescueCloseReq {
		return rescuePartialCloseRequest(cfg, symbol, mainDir, current, entry, accum, reduced, step, minQ, last, sig, 0, 0, "sell")
	}

	t.Run("all conditions met → request returned", func(t *testing.T) {
		cfg := botCfgJSON{
			RescuePartialCloseEnabled:       true,
			RescueTriggerPriceMovePct:       &mainAdversePct,
			RescueTriggerAccumulatedMinUsdt: &accumMin,
			RescueMinIntervalSec:            300,
		}
		// long main, price dropped ~8% from entry, 50 USDT accumulated
		req := call(cfg, "BTCUSDT", "buy", 50000, 54348, 50, 0, 0.001, 0.001, nil, false)
		if req == nil {
			t.Fatal("expected request, got nil")
		}
		if req.Symbol != "BTCUSDT" {
			t.Errorf("symbol: %s", req.Symbol)
		}
		if req.Side != "sell" {
			t.Errorf("side: %s (expected sell for long)", req.Side)
		}
		if req.Qty <= 0 {
			t.Errorf("qty must be > 0, got %f", req.Qty)
		}
	})

	t.Run("trigger not met → nil", func(t *testing.T) {
		cfg := botCfgJSON{
			RescuePartialCloseEnabled: true,
			RescueTriggerPriceMovePct: &mainAdversePct,
		}
		// price dropped only 2% → movePct=-2 > -5 → not met
		req := call(cfg, "BTCUSDT", "buy", 98000, 100000, 50, 0, 0.001, 0.001, nil, false)
		if req != nil {
			t.Errorf("expected nil, got %+v", req)
		}
	})

	t.Run("cooldown not elapsed → nil", func(t *testing.T) {
		cfg := botCfgJSON{
			RescuePartialCloseEnabled: true,
			RescueMinIntervalSec:      300,
		}
		recent := time.Now().Add(-60 * time.Second)
		req := call(cfg, "BTCUSDT", "buy", 90000, 100000, 50, 0, 0.001, 0.001, &recent, false)
		if req != nil {
			t.Errorf("expected nil (cooldown), got %+v", req)
		}
	})

	t.Run("insufficient pnl for minQty → nil", func(t *testing.T) {
		cfg := botCfgJSON{RescuePartialCloseEnabled: true}
		req := call(cfg, "BTCUSDT", "buy", 50000, 55000, 0.5, 0, 0.001, 0.001, nil, false)
		if req != nil {
			t.Errorf("expected nil (insufficient qty), got %+v", req)
		}
	})

	t.Run("short main closing side = buy", func(t *testing.T) {
		cfg := botCfgJSON{RescuePartialCloseEnabled: true}
		req := call(cfg, "ETHUSDT", "sell", 1200, 1000, 100, 0, 0.01, 0.01, nil, false)
		if req == nil {
			t.Fatal("expected request for short")
		}
		if req.Side != "buy" {
			t.Errorf("side: %s (expected buy for short)", req.Side)
		}
	})

	t.Run("hedge trigger met → request returned", func(t *testing.T) {
		hedgePct := -4.0 // хедж просел на 4%
		cfg := botCfgJSON{
			RescuePartialCloseEnabled:      true,
			RescueTriggerHedgePriceMovePct: &hedgePct,
		}
		// hedge=short: entry=100, current=105 → movePct=-5 ≤ -4 → met
		req := rescuePartialCloseRequest(cfg, "SOLUSDT", "buy", 100, 110, 50, 0, 0.1, 0.1, nil, false, 100, 105, "sell")
		if req == nil {
			t.Fatal("expected request when hedge trigger met")
		}
	})

	t.Run("hedge trigger not met → nil", func(t *testing.T) {
		hedgePct := -4.0
		cfg := botCfgJSON{
			RescuePartialCloseEnabled:      true,
			RescueTriggerHedgePriceMovePct: &hedgePct,
		}
		// hedge=short: entry=100, current=102 → movePct=-2 > -4 → not met
		req := rescuePartialCloseRequest(cfg, "SOLUSDT", "buy", 100, 110, 50, 0, 0.1, 0.1, nil, false, 100, 102, "sell")
		if req != nil {
			t.Errorf("expected nil when hedge trigger not met, got %+v", req)
		}
	})
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
