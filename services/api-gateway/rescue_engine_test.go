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

// TestRescueCalcPartialCloseQty_MinQtySnapUpCappedAtRemaining: the second cap check
// (after trader.FormatQty) is only non-redundant when minQty forces FormatQty to snap
// a floored qty *up* past mainRemainingQty — the first cap check (on the pre-rounding
// raw value) can't catch this because raw itself is comfortably under
// mainRemainingQty; only rounding pushes it over.
//
// entryPrice-markPrice=10/юнит убытка, accumulated_pnl=21 -> raw=2.1, which is below
// mainRemainingQty=3 (so the first cap check does NOT fire). trader.FormatQty(2.1, 1,
// 5) floors 2.1->2 to qtyStep=1, then — since 2 < minQty=5 — snaps up to
// ceil(5/1)*1=5, which exceeds mainRemainingQty=3. Without the second cap check this
// would overshoot and report qty=5, closing more than the position has left; with it,
// the result must be capped at exactly mainRemainingQty=3, closesEntirely=true.
func TestRescueCalcPartialCloseQty_MinQtySnapUpCappedAtRemaining(t *testing.T) {
	qty, closesEntirely := rescueCalcPartialCloseQty("Buy", 100, 90, 21, 3, 1, 5)
	if !closesEntirely {
		t.Fatal("expected closesEntirely=true when minQty snap-up pushes qty past mainRemainingQty")
	}
	if qty != 3 {
		t.Errorf("qty = %v, want exactly mainRemainingQty=3 (minQty snap-up must not overshoot)", qty)
	}
}

// TestRescuePriceMoveTowardMainPct_Long: мейн-лонг, хедж открылся по 100, сейчас
// 105 -> движение в пользу мейна +5%.
func TestRescuePriceMoveTowardMainPct_Long(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Buy", 100, 105)
	if got < 4.999999 || got > 5.000001 {
		t.Errorf("got %v, want 5.0", got)
	}
}

// TestRescuePriceMoveTowardMainPct_Short: мейн-шорт, хедж открылся по 100, сейчас
// 95 -> движение в пользу мейна +5% (для шорта падение цены — это плюс).
func TestRescuePriceMoveTowardMainPct_Short(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Sell", 100, 95)
	if got < 4.999999 || got > 5.000001 {
		t.Errorf("got %v, want 5.0", got)
	}
}

// TestRescuePriceMoveTowardMainPct_AgainstMain: движение цены ПРОТИВ мейна должно
// давать отрицательный процент, не искусственно зажиматься в 0 — так
// rescueTriggersMet корректно сравнивает с положительным порогом и не пропускает
// ложных срабатываний.
func TestRescuePriceMoveTowardMainPct_AgainstMain(t *testing.T) {
	got := rescuePriceMoveTowardMainPct("Buy", 100, 95)
	if got > -4.999999 {
		t.Errorf("got %v, want ~-5.0 (against main)", got)
	}
}

// TestRescuePriceMoveTowardMainPct_UnpopulatedEntry: hedge_entry_at_start ещё не
// заполнен асинхронным обработчиком (0 или отрицательное значение) — функция должна
// вернуть 0, а не делить на ноль/отрицательное число.
func TestRescuePriceMoveTowardMainPct_UnpopulatedEntry(t *testing.T) {
	if got := rescuePriceMoveTowardMainPct("Buy", 0, 105); got != 0 {
		t.Errorf("got %v, want 0 when hedgeEntryAtStart==0", got)
	}
	if got := rescuePriceMoveTowardMainPct("Sell", -5, 105); got != 0 {
		t.Errorf("got %v, want 0 when hedgeEntryAtStart<0", got)
	}
}

// TestRescuePriceLevelReached_Long: мейн-лонг, уровень 61200, цена 61500 -> reached.
func TestRescuePriceLevelReached_Long(t *testing.T) {
	if !rescuePriceLevelReached("Buy", 61500, 61200) {
		t.Error("expected reached=true when markPrice above level for long main")
	}
	if rescuePriceLevelReached("Buy", 61000, 61200) {
		t.Error("expected reached=false when markPrice below level for long main")
	}
	if !rescuePriceLevelReached("Buy", 61200, 61200) {
		t.Error("expected reached=true when markPrice exactly equals level for long main (inclusive >=)")
	}
}

// TestRescuePriceLevelReached_Short: мейн-шорт, уровень 61200, цена 61000 -> reached
// (для шорта "в пользу мейна" — цена НИЖЕ уровня).
func TestRescuePriceLevelReached_Short(t *testing.T) {
	if !rescuePriceLevelReached("Sell", 61000, 61200) {
		t.Error("expected reached=true when markPrice below level for short main")
	}
	if rescuePriceLevelReached("Sell", 61500, 61200) {
		t.Error("expected reached=false when markPrice above level for short main")
	}
	if !rescuePriceLevelReached("Sell", 61200, 61200) {
		t.Error("expected reached=true when markPrice exactly equals level for short main (inclusive <=)")
	}
}
