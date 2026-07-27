package main

import (
	"strconv"

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
