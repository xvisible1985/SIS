package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"sis/pkg/signal"
	"sis/pkg/strategy"
	"sis/pkg/trader"
)

// rescueSignalTrigger — named type for cfg.RescueTriggerSignal (T4). Named (rather
// than an inline anonymous struct) so it can be used as a standalone parameter type
// in rescueEvaluateTriggerSignal without relying on Go's anonymous-struct structural
// assignability, which is easy to get subtly wrong across files.
type rescueSignalTrigger struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}

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

// rescuePartialCloseRequest строит reduce-only рыночный ордер, снимающий qty с
// мейн-позиции. side — ПРОТИВОПОЛОЖНАЯ стороне мейна (гасит позицию, а не
// удваивает), та же конвенция, что в matrixLegCloseRequest (matrix_engine.go).
func rescuePartialCloseRequest(mainSide, symbol, category string, posIdx int, qty, qtyStep, minQty float64) trader.OrderRequest {
	closeSide := "Sell"
	if mainSide == "Sell" {
		closeSide = "Buy"
	}
	return trader.OrderRequest{
		Category:    category,
		Symbol:      symbol,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         trader.FormatQty(qty, qtyStep, minQty),
		ReduceOnly:  true,
		PositionIdx: posIdx,
	}
}

// rescueSelfCloseLinkID строит SIS_STR-{id8}-scl-{ms} orderLinkId для
// reduce-only partial-close ордера на мейн-стратегии — тот же формат
// "scl" (self-close), что strategy_handler.go использует для detach-close
// (см. pkg/strategy/linkid.go, reSelfClose/LinkIDSelfClose). Обязателен: без
// него ClosedPnlSyncer не сможет атрибутировать закрытие по linkId и
// свалится в устаревшую time-window zombie-эвристику, которая force-закроет
// ещё живой цикл мейна как ghost_close (см. вызов в checkRescuePartialClose).
func rescueSelfCloseLinkID(mainStrategyID string, now time.Time) string {
	id8 := mainStrategyID
	if len(id8) > 8 {
		id8 = id8[:8]
	}
	return fmt.Sprintf("SIS_STR-%s-scl-%d", id8, now.UnixMilli())
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

// recordRescuePartialClose фиксирует результат одного шага частичного снятия:
// увеличивает счётчики main_reduced_coin/main_reduced_usdt и обновляет
// last_partial_close_at (для кулдауна следующего шага). Матчится по main или
// hedge strategy id — та же конвенция, что AccumulateHedgeSessionPnl
// (pkg/strategy/trade_recorder.go), которой снятие накопленного PnL хеджа
// пользуется отдельно (см. Task 8).
func (s *Server) recordRescuePartialClose(ctx context.Context, stratID string, coinDelta, usdtDelta float64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE hedge_sessions
		 SET main_reduced_coin = main_reduced_coin + $1,
		     main_reduced_usdt = main_reduced_usdt + $2,
		     last_partial_close_at = NOW()
		 WHERE (main_strategy_id = $3 OR hedge_strategy_id = $3) AND ended_at IS NULL`,
		coinDelta, usdtDelta, stratID)
	return err
}

// rescueSessionRow — одна активная сессия хеджа этого бота, читаемая для оценки
// частичного снятия. Та же join-форма, что buildPairedCloseWatches (см.
// hedge_engine.go), плюс поля, нужные только RescueBot (hedge_entry_at_start,
// last_partial_close_at).
type rescueSessionRow struct {
	mainStrategyID     string
	hedgeStrategyID    string
	accumulatedPnl     float64
	hedgeEntryAtStart  *float64
	lastPartialCloseAt *time.Time
	symbol             string
	mainDir            string
}

// checkRescuePartialClose оценивает и, при срабатывании всех включённых триггеров,
// исполняет один шаг частичного снятия мейна для каждой активной hedge-сессии этого
// бота. Вызывается из конца processHedgeBot (hedge_engine.go), гейтится
// cfg.RescuePartialCloseEnabled на вызывающей стороне. Работает независимо от
// paired-close/обычной деактивации — они продолжают проверяться в processHedgeBot
// на каждом тике вне зависимости от состояния этой функции (см. дизайн, раздел
// «Архитектура»).
func (s *Server) checkRescuePartialClose(ctx context.Context, botID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) {
	rows, err := s.pool.Query(ctx, `
		SELECT hs.main_strategy_id, hs.hedge_strategy_id, hs.accumulated_pnl, hs.hedge_entry_at_start,
		       hs.last_partial_close_at, ms.symbol, ms.direction
		FROM hedge_sessions hs
		JOIN strategies ms ON ms.id = hs.main_strategy_id
		JOIN strategies hst ON hst.id = hs.hedge_strategy_id
		WHERE hs.bot_id = $1 AND hs.ended_at IS NULL
		  AND ms.status IN ('active','finishing') AND hst.status IN ('active','finishing')`,
		botID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("RescueBot: ошибка запроса сессий: %v", err), "error", "rescue")
		return
	}
	var sessions []rescueSessionRow
	for rows.Next() {
		var rs rescueSessionRow
		if rows.Scan(&rs.mainStrategyID, &rs.hedgeStrategyID, &rs.accumulatedPnl, &rs.hedgeEntryAtStart,
			&rs.lastPartialCloseAt, &rs.symbol, &rs.mainDir) == nil {
			sessions = append(sessions, rs)
		}
	}
	rows.Close()

	for _, rs := range sessions {
		if !rescueCooldownElapsed(rs.lastPartialCloseAt, cfg.RescueMinIntervalSec, time.Now()) {
			continue
		}

		bySymbol, ok := posMap[rs.symbol]
		if !ok {
			continue
		}
		mainSide := hedgeDirToSide(rs.mainDir)
		mainPos, hasMain := bySymbol[mainSide]
		if !hasMain || mainPos.Size <= 0 {
			continue
		}

		var hedgeEntryAtStart float64
		if rs.hedgeEntryAtStart != nil {
			hedgeEntryAtStart = *rs.hedgeEntryAtStart
		}

		signalFired := false
		if cfg.RescueTriggerSignal != nil {
			signalFired = s.rescueEvaluateTriggerSignal(rs.symbol, mainSide, *cfg.RescueTriggerSignal)
		}

		if !rescueTriggersMet(cfg, mainSide, hedgeEntryAtStart, mainPos.MarkPrice, rs.accumulatedPnl, signalFired) {
			continue
		}

		instr, err := trader.GetPublicInstrumentInfo(ctx, cfg.Category, rs.symbol)
		if err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ошибка получения параметров инструмента: %v", rs.symbol, err),
				"error", "rescue")
			continue
		}

		qty, _ := rescueCalcPartialCloseQty(mainSide, mainPos.EntryPrice, mainPos.MarkPrice, rs.accumulatedPnl, mainPos.Size, instr.QtyStep, instr.MinQty)
		if qty <= 0 {
			continue
		}

		posIdx := 1
		if mainSide == "Sell" {
			posIdx = 2
		}
		req := rescuePartialCloseRequest(mainSide, rs.symbol, cfg.Category, posIdx, qty, instr.QtyStep, instr.MinQty)
		// linkId is required so ClosedPnlSyncer's linkId-based attribution (step 1b)
		// recognizes this as a non-cycle-ending self-close on the MAIN strategy and
		// skips the time-window zombie-cycle heuristic (step 3), which would otherwise
		// force-close the main's still-open cycle as "ghost_close". Same fix pattern as
		// strategy_handler.go's detach-close and the historical stopMatrixPair/SIS_MPC_
		// and DetachFromBot/SIS_DTH_ bugs (see pkg/strategy/linkid.go's LinkIDSelfClose
		// doc comment).
		req.OrderLinkId = rescueSelfCloseLinkID(rs.mainStrategyID, time.Now())

		if _, err := trader.PlaceOrder(ctx, creds, req); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ошибка частичного закрытия мейна (%s qty=%s): %v",
					rs.symbol, req.Side, req.Qty, err),
				"error", "rescue")
			continue
		}

		realizedLoss := qty * (mainPos.EntryPrice - mainPos.MarkPrice)
		if mainSide == "Sell" {
			realizedLoss = qty * (mainPos.MarkPrice - mainPos.EntryPrice)
		}
		strategy.AccumulateHedgeSessionPnl(ctx, s.pool, rs.hedgeStrategyID, -realizedLoss)

		if err := s.recordRescuePartialClose(ctx, rs.hedgeStrategyID, qty, qty*mainPos.MarkPrice); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("RescueBot: %s — ордер исполнен, но не удалось записать состояние: %v", rs.symbol, err),
				"error", "rescue")
		}

		s.logBotEvent(ctx, botID,
			fmt.Sprintf("RescueBot: %s — частично снято %s (%.8f) с мейна, накоплено хеджа уменьшено на %.4g",
				rs.symbol, req.Qty, qty, realizedLoss),
			"info", "rescue")
	}
}

// rescueEvaluateTriggerSignal оценивает сигнал T4 тем же способом, что
// checkHedgeActivation оценивает ActivationSignals (см. hedge_engine.go): сигнал
// должен подтверждать направление, благоприятное для мейна — если мейн-лонг, ждём
// Buy; если мейн-шорт, ждём Sell.
func (s *Server) rescueEvaluateTriggerSignal(symbol, mainSide string, trig rescueSignalTrigger) bool {
	sc := signal.Config{Name: trig.Name, Params: trig.Params}
	if _, err := signal.Build(sc); err != nil {
		return false
	}
	interval := "15"
	if v, ok := trig.Params["tf"].(string); ok && v != "" {
		interval = v
	}
	state := s.signalEngine.ComputeStateForce(symbol, interval, []signal.Config{sc})
	want := signal.Buy
	if mainSide == "Sell" {
		want = signal.Sell
	}
	return state == want
}
