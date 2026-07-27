package main

import (
	"testing"
	"time"

	"sis/pkg/strategy"
)

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

// TestRescueTriggersMet_AllDisabled: ни один триггер не включён (все указатели
// nil) -> "И" по пустому множеству включённых триггеров считается истинным. Это
// осознанное, задокументированное поведение: включение RescuePartialCloseEnabled
// без единого настроенного триггера означает "снимать на каждом тике, ограничено
// только кулдауном" — не баг, форма используется намеренно.
func TestRescueTriggersMet_AllDisabled(t *testing.T) {
	cfg := botCfgJSON{}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 10, false) {
		t.Error("expected true when no triggers are configured (vacuous AND)")
	}
}

// TestRescueTriggersMet_PriceMoveTrigger: только T1 включён.
func TestRescueTriggersMet_PriceMoveTrigger(t *testing.T) {
	threshold := 3.0
	cfg := botCfgJSON{RescueTriggerPriceMovePct: &threshold}
	// markPrice=104 при hedgeEntryAtStart=100 -> движение +4% >= порога 3% -> true.
	if !rescueTriggersMet(cfg, "Buy", 100, 104, 10, false) {
		t.Error("expected true: price moved 4%% >= 3%% threshold")
	}
	// markPrice=101 -> движение +1% < порога 3% -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 101, 10, false) {
		t.Error("expected false: price moved only 1%% < 3%% threshold")
	}
}

// TestRescueTriggersMet_PriceLevelTrigger: только T2 включён.
func TestRescueTriggersMet_PriceLevelTrigger(t *testing.T) {
	level := 61200.0
	cfg := botCfgJSON{RescueTriggerPriceLevel: &level}
	// markPrice=61500 при mainSide="Buy" -> цена выше уровня -> достигнут -> true.
	if !rescueTriggersMet(cfg, "Buy", 100, 61500, 10, false) {
		t.Error("expected true: markPrice 61500 reached level 61200 for long main")
	}
	// markPrice=61000 -> цена ниже уровня -> не достигнут -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 61000, 10, false) {
		t.Error("expected false: markPrice 61000 did not reach level 61200 for long main")
	}
}

// TestRescueTriggersMet_AccumulatedMinTrigger: только T3 включён.
func TestRescueTriggersMet_AccumulatedMinTrigger(t *testing.T) {
	minAccum := 20.0
	cfg := botCfgJSON{RescueTriggerAccumulatedMinUsdt: &minAccum}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 25, false) {
		t.Error("expected true: accumulated 25 >= min 20")
	}
	if rescueTriggersMet(cfg, "Buy", 100, 105, 15, false) {
		t.Error("expected false: accumulated 15 < min 20")
	}
}

// TestRescueTriggersMet_SignalTrigger: только T4 включён — зависит целиком от
// переданного currentSignalFired (оценка самого сигнала — забота вызывающего кода,
// см. Task 8).
func TestRescueTriggersMet_SignalTrigger(t *testing.T) {
	cfg := botCfgJSON{}
	cfg.RescueTriggerSignal = &rescueSignalTrigger{Name: "st-flip"}
	if !rescueTriggersMet(cfg, "Buy", 100, 105, 10, true) {
		t.Error("expected true when signal fired")
	}
	if rescueTriggersMet(cfg, "Buy", 100, 105, 10, false) {
		t.Error("expected false when signal did not fire")
	}
}

// TestRescueTriggersMet_MultipleTriggers_AllMustPass: T1+T3 оба включены — «И»:
// один провален -> итог false, даже если другой пройден.
func TestRescueTriggersMet_MultipleTriggers_AllMustPass(t *testing.T) {
	movePct := 3.0
	minAccum := 20.0
	cfg := botCfgJSON{
		RescueTriggerPriceMovePct:       &movePct,
		RescueTriggerAccumulatedMinUsdt: &minAccum,
	}
	// Оба выполнены -> true.
	if !rescueTriggersMet(cfg, "Buy", 100, 104, 25, false) {
		t.Error("expected true: both T1 and T3 satisfied")
	}
	// T1 выполнен, T3 — нет (accumulated=15<20) -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 104, 15, false) {
		t.Error("expected false: T3 not satisfied even though T1 is")
	}
	// T3 выполнен, T1 — нет (движение всего 1%%<3%%) -> false.
	if rescueTriggersMet(cfg, "Buy", 100, 101, 25, false) {
		t.Error("expected false: T1 not satisfied even though T3 is")
	}
}

// TestRescueCooldownElapsed_NoPreviousStep: lastPartialCloseAt=nil (шага ещё не
// было) -> кулдаун всегда пройден.
func TestRescueCooldownElapsed_NoPreviousStep(t *testing.T) {
	if !rescueCooldownElapsed(nil, 300, time.Now()) {
		t.Error("expected true when there is no previous step yet")
	}
}

// TestRescueCooldownElapsed_ZeroInterval: MinIntervalSec<=0 -> кулдаун отключён,
// всегда пройден вне зависимости от lastPartialCloseAt.
func TestRescueCooldownElapsed_ZeroInterval(t *testing.T) {
	now := time.Now()
	if !rescueCooldownElapsed(&now, 0, now) {
		t.Error("expected true when MinIntervalSec<=0 (cooldown disabled)")
	}
}

// TestRescueCooldownElapsed_WithinCooldown: последний шаг был 100с назад,
// MinIntervalSec=300 -> кулдаун ещё не прошёл.
func TestRescueCooldownElapsed_WithinCooldown(t *testing.T) {
	now := time.Now()
	last := now.Add(-100 * time.Second)
	if rescueCooldownElapsed(&last, 300, now) {
		t.Error("expected false: only 100s elapsed of 300s cooldown")
	}
}

// TestRescueCooldownElapsed_AfterCooldown: последний шаг был 400с назад,
// MinIntervalSec=300 -> кулдаун прошёл.
func TestRescueCooldownElapsed_AfterCooldown(t *testing.T) {
	now := time.Now()
	last := now.Add(-400 * time.Second)
	if !rescueCooldownElapsed(&last, 300, now) {
		t.Error("expected true: 400s elapsed >= 300s cooldown")
	}
}

// TestRescuePartialCloseRequest_Long: мейн-лонг -> закрывающий ордер должен быть
// Sell, reduce-only, тот же формат, что matrixLegCloseRequest.
func TestRescuePartialCloseRequest_Long(t *testing.T) {
	req := rescuePartialCloseRequest("Buy", "BTCUSDT", "linear", 1, 0.5, 0.001, 0)
	if req.Side != "Sell" || req.PositionIdx != 1 || !req.ReduceOnly ||
		req.OrderType != "Market" || req.Symbol != "BTCUSDT" || req.Category != "linear" {
		t.Errorf("long partial-close request wrong: %+v", req)
	}
	// trader.FormatQty formats to the qtyStep's implied decimal places (stepDecimals),
	// so qtyStep=0.001 (3 decimals) yields "0.500", not a trimmed "0.5" — see
	// FormatQty/stepDecimals in pkg/trader/instruments.go (~lines 80-118).
	if req.Qty != "0.500" {
		t.Errorf("Qty = %q, want %q", req.Qty, "0.500")
	}
}

// TestRescuePartialCloseRequest_Short: мейн-шорт -> закрывающий ордер Buy.
func TestRescuePartialCloseRequest_Short(t *testing.T) {
	req := rescuePartialCloseRequest("Sell", "BTCUSDT", "linear", 2, 0.5, 0.001, 0)
	if req.Side != "Buy" || req.PositionIdx != 2 || !req.ReduceOnly {
		t.Errorf("short partial-close request wrong: %+v", req)
	}
}

// TestRescueSelfCloseLinkID_ParsesAsSelfCloseForMainStrategy проверяет, что
// rescueSelfCloseLinkID производит orderLinkId, который pkg/strategy.ParseStrategyLinkID
// распознаёт как LinkIDSelfClose с правильным 8-символьным префиксом ID МЕЙН-стратегии —
// это единственное, что не даёт ClosedPnlSyncer свалиться в zombie-эвристику и
// force-закрыть ещё живой цикл мейна как ghost_close (см. checkRescuePartialClose).
func TestRescueSelfCloseLinkID_ParsesAsSelfCloseForMainStrategy(t *testing.T) {
	mainID := "abcdef12-3456-7890-abcd-ef1234567890"
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	linkID := rescueSelfCloseLinkID(mainID, now)

	parsed, ok := strategy.ParseStrategyLinkID(linkID)
	if !ok {
		t.Fatalf("ParseStrategyLinkID(%q) failed to recognize linkId as ours", linkID)
	}
	if parsed.Kind != strategy.LinkIDSelfClose {
		t.Errorf("Kind = %v, want LinkIDSelfClose", parsed.Kind)
	}
	if parsed.StrategyID8 != "abcdef12" {
		t.Errorf("StrategyID8 = %q, want %q (main strategy's prefix, not hedge's)", parsed.StrategyID8, "abcdef12")
	}
}

// TestRescueSelfCloseLinkID_ShortID: защитная проверка на случай если mainStrategyID
// когда-либо окажется короче 8 символов (не должно случиться для настоящих UUID, но
// rescueSelfCloseLinkID не должен паниковать при срезе).
func TestRescueSelfCloseLinkID_ShortID(t *testing.T) {
	linkID := rescueSelfCloseLinkID("ab12", time.Unix(0, 0))
	if linkID != "SIS_STR-ab12-scl-0" {
		t.Errorf("linkID = %q, want %q", linkID, "SIS_STR-ab12-scl-0")
	}
}
