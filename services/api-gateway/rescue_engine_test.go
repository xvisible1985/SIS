package main

import "testing"

// TestRescueCalcPartialCloseQty_Long: мейн-лонг вошёл по 100, сейчас 90 (просадка
// 10/юнит). accumulated_pnl=50 -> должны снять 50/10=5 монет (без учёта округления,
// qtyStep=0 отключает округление в trader.FormatQty).
func TestRescueCalcPartialCloseQty_Long(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 50, 100, 0, 0)
	if closesEntirely {
		t.Fatal("expected partial close, not entire")
	}
	if qty < 4.999999 || qty > 5.000001 {
		t.Errorf("qty = %v, want ~5", qty)
	}
}

// TestRescueCalcPartialCloseQty_Short: мейн-шорт вошёл по 100, сейчас 110 (просадка
// 10/юнит — шорт теряет когда цена растёт). accumulated_pnl=30 -> 30/10=3 монеты.
func TestRescueCalcPartialCloseQty_Short(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Sell", 100, 110, 30, 100, 0, 0)
	if closesEntirely {
		t.Fatal("expected partial close, not entire")
	}
	if qty < 2.999999 || qty > 3.000001 {
		t.Errorf("qty = %v, want ~3", qty)
	}
}

// TestRescueCalcPartialCloseQty_NoAccumulatedProfit: accumulated_pnl<=0 — нечем
// финансировать снятие, шаг не должен ничего предлагать снять.
func TestRescueCalcPartialCloseQty_NoAccumulatedProfit(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 0, 100, 0, 0)
	if qty != 0 || closesEntirely {
		t.Errorf("qty=%v closesEntirely=%v, want 0/false when accumulated_pnl<=0", qty, closesEntirely)
	}
}

// TestRescueCalcPartialCloseQty_MainNotAtLoss: цена мейна восстановилась выше входа
// (лонг: markPrice > entryPrice) — на юнит убытка нет, формула объёма не имеет смысла
// (совпадает с обычной деактивацией/paired-close, а не с частичным снятием) — ожидаем
// no-op, а не деление на отрицательное число.
func TestRescueCalcPartialCloseQty_MainNotAtLoss(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 105, 50, 100, 0, 0)
	if qty != 0 || closesEntirely {
		t.Errorf("qty=%v closesEntirely=%v, want 0/false when main is profitable per-unit", qty, closesEntirely)
	}
}

// TestRescueCalcPartialCloseQty_CapsAtRemaining: accumulated_pnl покрывает больше,
// чем осталось от мейна (mainRemainingQty=3, расчётный объём был бы 5) — должны
// закрыть мейн целиком (qty=3, closesEntirely=true), не переливать.
func TestRescueCalcPartialCloseQty_CapsAtRemaining(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 50, 3, 0, 0)
	if !closesEntirely {
		t.Fatal("expected closesEntirely=true when computed qty exceeds remaining size")
	}
	if qty != 3 {
		t.Errorf("qty = %v, want exactly mainRemainingQty=3 (no overflow)", qty)
	}
}

// TestRescueCalcPartialCloseQty_RoundsDownToQtyStep: сырой объём 5.7 при qtyStep=0.5
// должен округлиться вниз до 5.5 (через trader.FormatQty), не до 6.
func TestRescueCalcPartialCloseQty_RoundsDownToQtyStep(t *testing.T) {
	// entryPrice-markPrice=10/юнит убытка, accumulated_pnl=57 -> сырой объём 5.7.
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 57, 100, 0.5, 0)
	if closesEntirely {
		t.Fatal("expected partial close")
	}
	if qty < 5.499999 || qty > 5.500001 {
		t.Errorf("qty = %v, want 5.5 (rounded down from 5.7 by qtyStep=0.5)", qty)
	}
}
