package main

import (
	"strconv"
	"time"

	"sis/pkg/trader"
)

// rescueCalcPartialCloseQty вычисляет объём мейн-позиции для снятия на этом шаге,
// профинансированный реализованным PnL хеджа (accumulatedPnl): реализованный убыток
// от закрытия этого объёма должен примерно равняться accumulatedPnl, так что трата
// накопленной хеджем прибыли на уменьшение экспозиции мейна даёт ~0 изменения
// суммарного реализованного PnL пары, но перманентно снижает риск на мейне.
//
// mainSide — "Buy" (лонг) или "Sell" (шорт). Возвращает (qty, closesEntirely):
// qty округлён вниз до qtyStep через trader.FormatQty; closesEntirely=true, когда
// расчётный объём достиг бы или превысил mainRemainingQty — в этом случае qty
// зажимается ровно в mainRemainingQty (без перелива), и вызывающий код должен
// трактовать это как полное закрытие, а не частичное.
//
// Возвращает (0, false), если финансировать нечем (accumulatedPnl<=0) или если
// мейн сейчас не в убытке по юниту (perUnitLoss<=0 — формула неприменима, обычная
// деактивация/paired-close должны обрабатывать восстановившуюся позицию отдельно).
func rescueCalcPartialCloseQty(mainSide string, mainEntryPrice, markPrice, accumulatedPnl, mainRemainingQty, qtyStep, minQty float64) (qty float64, closesEntirely bool) {
	var perUnitLoss float64
	if mainSide == "Sell" {
		perUnitLoss = markPrice - mainEntryPrice // шорт теряет, когда цена растёт
	} else {
		perUnitLoss = mainEntryPrice - markPrice // лонг теряет, когда цена падает
	}
	if perUnitLoss <= 0 || accumulatedPnl <= 0 {
		return 0, false
	}

	raw := accumulatedPnl / perUnitLoss
	if raw >= mainRemainingQty {
		return mainRemainingQty, true
	}

	formatted := trader.FormatQty(raw, qtyStep, minQty)
	rounded, _ := strconv.ParseFloat(formatted, 64)
	if rounded >= mainRemainingQty {
		return mainRemainingQty, true
	}
	return rounded, false
}

// rescuePriceMoveTowardMainPct возвращает, на сколько процентов цена сдвинулась от
// hedgeEntryAtStart (цена, по которой открылась хедж-нога — момент активации) в
// пользу мейна. Положительное значение = движение в пользу мейна, отрицательное =
// против. Возвращает 0, если точка отсчёта ещё не известна (hedge_entry_at_start
// заполняется асинхронно после фактического открытия хедж-позиции на бирже).
func rescuePriceMoveTowardMainPct(mainSide string, hedgeEntryAtStart, markPrice float64) float64 {
	if hedgeEntryAtStart <= 0 {
		return 0
	}
	if mainSide == "Sell" {
		// мейн-шорт: в его пользу — падение цены ниже точки отсчёта.
		return (hedgeEntryAtStart - markPrice) / hedgeEntryAtStart * 100
	}
	// мейн-лонг: в его пользу — рост цены выше точки отсчёта.
	return (markPrice - hedgeEntryAtStart) / hedgeEntryAtStart * 100
}

// rescuePriceLevelReached сообщает, достигла ли markPrice абсолютного уровня level
// в благоприятном для мейна направлении: для лонга — цена на уровне или выше, для
// шорта — на уровне или ниже.
func rescuePriceLevelReached(mainSide string, markPrice, level float64) bool {
	if mainSide == "Sell" {
		return markPrice <= level
	}
	return markPrice >= level
}

// rescueTriggersMet оценивает все ВКЛЮЧЁННЫЕ триггеры (T1-T4) и возвращает true,
// только если истинны все включённые (логическое «И»). Триггер, который не
// настроен (nil-указатель / нулевое значение поля), пропускается — не участвует в
// условии. Если ни один триггер не включён, результат истинен (пустое «И») — это
// осознанное поведение: включение RescuePartialCloseEnabled без единого триггера
// означает "срабатывать на каждом тике, ограничено только кулдауном".
//
// currentSignalFired сообщает, сработал ли на этом тике сигнал, настроенный в
// cfg.RescueTriggerSignal, для символа бота — вызывающий код отвечает за оценку
// самого сигнала (через существующий pkg/signal, как ActivationSignals), поскольку
// это требует доступа к движку сигналов/рыночным данным, которых у этой чистой
// функции нет.
func rescueTriggersMet(cfg botCfgJSON, mainSide string, hedgeEntryAtStart, markPrice, accumulatedPnl float64, currentSignalFired bool) bool {
	if cfg.RescueTriggerPriceMovePct != nil {
		moved := rescuePriceMoveTowardMainPct(mainSide, hedgeEntryAtStart, markPrice)
		if moved < *cfg.RescueTriggerPriceMovePct {
			return false
		}
	}
	if cfg.RescueTriggerPriceLevel != nil {
		if !rescuePriceLevelReached(mainSide, markPrice, *cfg.RescueTriggerPriceLevel) {
			return false
		}
	}
	if cfg.RescueTriggerAccumulatedMinUsdt != nil {
		if accumulatedPnl < *cfg.RescueTriggerAccumulatedMinUsdt {
			return false
		}
	}
	if cfg.RescueTriggerSignal != nil {
		if !currentSignalFired {
			return false
		}
	}
	return true
}

// rescueCooldownElapsed сообщает, прошёл ли кулдаун между шагами частичного
// снятия. lastPartialCloseAt=nil (шага ещё не было) или minIntervalSec<=0
// (кулдаун отключён в конфиге) — всегда true.
func rescueCooldownElapsed(lastPartialCloseAt *time.Time, minIntervalSec int, now time.Time) bool {
	if lastPartialCloseAt == nil || minIntervalSec <= 0 {
		return true
	}
	return now.Sub(*lastPartialCloseAt) >= time.Duration(minIntervalSec)*time.Second
}
