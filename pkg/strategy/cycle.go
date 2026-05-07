package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"sis/pkg/trader"
)

// StrategyRunner holds the runtime state of one strategy.
type StrategyRunner struct {
	mu       sync.Mutex
	strategy Strategy
	runner   *AccountRunner

	cycle        *Cycle
	levels       []GridLevel
	tpOrderID    string
	slOrderID    string
	instr        trader.InstrumentInfo
	closedBySelf bool // set by handleTPFill/handleSLFill to suppress the WS position-close event
}

// calculateGridLevels returns target prices for all N grid levels.
// For Buy: compound step down from base. For Sell: compound step up.
func calculateGridLevels(basePrice, stepPct float64, count int, side string) []float64 {
	prices := make([]float64, count)
	for i := 0; i < count; i++ {
		exp := float64(i + 1)
		if side == "Buy" {
			prices[i] = basePrice * math.Pow(1-stepPct/100, exp)
		} else {
			prices[i] = basePrice * math.Pow(1+stepPct/100, exp)
		}
	}
	return prices
}

// loadOrStart resumes an existing active cycle from DB or starts a fresh one.
func (sr *StrategyRunner) loadOrStart(ctx context.Context) {
	instr, err := trader.GetInstrumentInfo(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: instrument info: %v", sr.strategy.ID, err)
	} else {
		sr.mu.Lock()
		sr.instr = instr
		sr.mu.Unlock()
	}
	if err := sr.loadActiveCycle(ctx); err != nil {
		if err2 := sr.startCycle(ctx); err2 != nil {
			log.Printf("strategy %s: start cycle: %v", sr.strategy.ID, err2)
		}
	} else {
		// Check if filled levels still have a real open position on exchange.
		// Handles the case where the position was closed while the service was down.
		if sr.checkPositionGone(ctx) {
			// Cycle was cleaned up; start fresh if strategy is still active.
			if sr.strategy.Status == StatusActive {
				if err2 := sr.startCycle(ctx); err2 != nil {
					log.Printf("strategy %s: start cycle after position cleanup: %v", sr.strategy.ID, err2)
				}
			}
			return
		}

		// Re-verify cycle is still open before placing levels.
		// Guards against race: stop goroutine may have closed the cycle between
		// loadActiveCycle and placeNextLevels.
		sr.mu.Lock()
		var cycleOpen bool
		_ = sr.runner.pool.QueryRow(ctx,
			`SELECT ended_at IS NULL FROM strategy_cycles WHERE id=$1`, sr.cycle.ID,
		).Scan(&cycleOpen)
		if !cycleOpen {
			log.Printf("strategy %s: cycle %d was closed concurrently, starting fresh",
				sr.strategy.ID, sr.cycle.CycleNum)
			sr.cycle = nil
			sr.levels = nil
			sr.mu.Unlock()
			if err2 := sr.startCycle(ctx); err2 != nil {
				log.Printf("strategy %s: start cycle after stale load: %v", sr.strategy.ID, err2)
			}
			return
		}
		if err := sr.placeNextLevels(ctx); err != nil {
			log.Printf("strategy %s: place pending levels: %v", sr.strategy.ID, err)
		}
		sr.mu.Unlock()
	}
}

// checkPositionGone checks if there are filled levels in the current cycle but no
// corresponding open position on the exchange. If so, it cleans up the cycle and
// returns true. Called only from loadOrStart (no lock held).
func (sr *StrategyRunner) checkPositionGone(ctx context.Context) bool {
	sr.mu.Lock()
	hasFills := false
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasFills = true
			break
		}
	}
	sr.mu.Unlock()

	if !hasFills {
		return false
	}

	positions, err := trader.FetchPositions(ctx, sr.runner.creds)
	if err != nil {
		log.Printf("strategy %s: fetch positions for startup check: %v", sr.strategy.ID, err)
		return false
	}

	wantIdx := positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction)
	positionOpen := false
	for _, p := range positions {
		size, _ := strconv.ParseFloat(p.Size, 64)
		if p.Symbol != sr.strategy.Symbol || size == 0 {
			continue
		}
		// In hedge mode match the position slot to this strategy's direction.
		if sr.strategy.HedgeMode && p.PositionIdx != wantIdx {
			continue
		}
		positionOpen = true
		break
	}
	if positionOpen {
		return false
	}

	// Position is gone — cancel TP, mark filled levels as cancelled, close cycle.
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.warn(ctx, fmt.Sprintf("Позиция не найдена на бирже при запуске, цикл %d очищен", sr.cycle.CycleNum))

	sr.cancelPlacedLevels(ctx)
	cycleID := sr.cycle.ID
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='filled'`, cycleID)
	for i := range sr.levels {
		if sr.levels[i].Status == LevelFilled {
			sr.levels[i].Status = LevelCancelled
		}
	}
	sr.closeCycle(ctx, "position_gone")
	return true
}

// loadActiveCycle loads the current cycle and placed levels from DB.
// Returns an error if there is no active (unfinished) cycle.
func (sr *StrategyRunner) loadActiveCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var c Cycle
	var tpID, slID *string
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT id, strategy_id, cycle_num, start_price, started_at, tp_order_id, sl_order_id
		 FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL
		 ORDER BY cycle_num DESC LIMIT 1`,
		sr.strategy.ID,
	).Scan(&c.ID, &c.StrategyID, &c.CycleNum, &c.StartPrice, &c.StartedAt, &tpID, &slID)
	if err != nil {
		return err
	}
	if tpID != nil {
		c.TPOrderID = *tpID
	}
	if slID != nil {
		c.SLOrderID = *slID
	}
	sr.cycle = &c
	sr.tpOrderID = c.TPOrderID
	sr.slOrderID = c.SLOrderID

	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_idx, side, target_price, size_usdt, qty, status,
		        COALESCE(exchange_order_id,''), COALESCE(filled_price,0)
		 FROM strategy_levels WHERE cycle_id=$1 ORDER BY level_idx ASC`,
		c.ID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var l GridLevel
		var stat string
		if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
			&l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice); err != nil {
			continue
		}
		l.Status = LevelStatus(stat)
		sr.levels = append(sr.levels, l)
		if l.Status == LevelPlaced && l.ExchangeOrderID != "" {
			sr.runner.RegisterOrder(l.ExchangeOrderID, orderRef{
				strategyID: sr.strategy.ID,
				levelID:    l.ID,
				refType:    "level",
			})
		}
	}
	if sr.tpOrderID != "" {
		sr.runner.RegisterOrder(sr.tpOrderID, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	}
	if sr.slOrderID != "" {
		sr.runner.RegisterOrder(sr.slOrderID, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	}
	sr.info(ctx, fmt.Sprintf("Цикл %d возобновлён, уровней: %d", c.CycleNum, len(sr.levels)))
	return nil
}

// startCycle creates a new cycle: fetches price, calculates grid, inserts into DB, places initial window.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) startCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		return fmt.Errorf("fetch price: %w", err)
	}

	var maxCycle int
	sr.runner.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(cycle_num),0) FROM strategy_cycles WHERE strategy_id=$1`,
		sr.strategy.ID,
	).Scan(&maxCycle) //nolint:errcheck

	var cycleID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, start_price) VALUES ($1,$2,$3) RETURNING id`,
		sr.strategy.ID, maxCycle+1, price,
	).Scan(&cycleID); err != nil {
		return fmt.Errorf("insert cycle: %w", err)
	}
	sr.cycle = &Cycle{
		ID: cycleID, StrategyID: sr.strategy.ID,
		CycleNum: maxCycle + 1, StartPrice: price, StartedAt: time.Now(),
	}
	sr.levels = nil

	sides := sidesForDirection(sr.strategy.Direction)
	levelIdx := 1

	if len(sr.strategy.Steps) > 0 {
		for _, side := range sides {
			prevPrice := price
			for _, step := range sr.strategy.Steps {
				var targetPrice float64
				if step.PriceMovePct == 0 {
					targetPrice = 0 // market order
				} else if side == "Buy" {
					targetPrice = prevPrice * (1 - step.PriceMovePct/100)
					prevPrice = targetPrice
				} else {
					targetPrice = prevPrice * (1 + step.PriceMovePct/100)
					prevPrice = targetPrice
				}
				sizeUSDT := step.Lots * sr.strategy.GridSizeUSDT
				priceForQty := price
				if targetPrice > 0 {
					priceForQty = targetPrice
				}
				qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep)
				var levelID string
				if err := sr.runner.pool.QueryRow(ctx,
					`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty)
					 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
					sr.strategy.ID, cycleID, levelIdx, side, targetPrice, sizeUSDT, qty,
				).Scan(&levelID); err != nil {
					log.Printf("strategy %s: insert level %d: %v", sr.strategy.ID, levelIdx, err)
					levelIdx++
					continue
				}
				sr.levels = append(sr.levels, GridLevel{
					ID: levelID, LevelIdx: levelIdx, Side: side,
					TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
					Status: LevelPending,
				})
				levelIdx++
			}
		}
	} else {
		for _, side := range sides {
			prices := calculateGridLevels(price, sr.strategy.GridStepPct, sr.strategy.GridLevels, side)
			for _, p := range prices {
				qty := trader.FormatQty(sr.strategy.GridSizeUSDT/p, sr.instr.QtyStep)
				var levelID string
				if err := sr.runner.pool.QueryRow(ctx,
					`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty)
					 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
					sr.strategy.ID, cycleID, levelIdx, side, p, sr.strategy.GridSizeUSDT, qty,
				).Scan(&levelID); err != nil {
					log.Printf("strategy %s: insert level %d: %v", sr.strategy.ID, levelIdx, err)
					levelIdx++
					continue
				}
				sr.levels = append(sr.levels, GridLevel{
					ID: levelID, LevelIdx: levelIdx, Side: side,
					TargetPrice: p, SizeUSDT: sr.strategy.GridSizeUSDT, Qty: qty,
					Status: LevelPending,
				})
				levelIdx++
			}
		}
	}

	sr.info(ctx, fmt.Sprintf("Цикл %d запущен @ %.4f, уровней: %d",
		sr.cycle.CycleNum, price, len(sr.levels)))
	return sr.placeNextLevels(ctx)
}

func sidesForDirection(d Direction) []string {
	switch d {
	case DirectionLong:
		return []string{"Buy"}
	case DirectionShort:
		return []string{"Sell"}
	default:
		return []string{"Buy", "Sell"}
	}
}

// placeNextLevels places pending levels until the sliding window (GridActive) is full.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeNextLevels(ctx context.Context) error {
	need := sr.strategy.GridActive - sr.placedCount()
	for i := range sr.levels {
		if need <= 0 {
			break
		}
		if sr.levels[i].Status != LevelPending {
			continue
		}
		if err := sr.placeLevel(ctx, i); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровня L%d: %v", sr.levels[i].LevelIdx, err))
			continue
		}
		need--
	}
	return nil
}

// placeLevel places a single grid level on the exchange.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeLevel(ctx context.Context, idx int) error {
	l := &sr.levels[idx]
	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	var req trader.OrderRequest
	if l.TargetPrice == 0 {
		req = trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        l.Side,
			OrderType:   "Market",
			Qty:         l.Qty,
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		}
	} else {
		req = trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        l.Side,
			OrderType:   "Limit",
			Qty:         l.Qty,
			Price:       trader.FormatPrice(l.TargetPrice, sr.instr.TickSize),
			TimeInForce: "GTC",
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		}
	}
	// Pre-register by linkID before placing so market order fills that arrive
	// before RegisterOrder(orderId) is called are not dropped.
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := trader.PlaceOrder(ctx, sr.runner.creds, req)
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		return err
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW() WHERE id=$3`,
		result.OrderId, linkID, l.ID,
	); err != nil {
		sr.runner.UnregisterOrder(linkID)
		return err
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, ref) // also register by orderId
	if l.TargetPrice == 0 {
		sr.info(ctx, fmt.Sprintf("L%d %s @ %.0f USDT MARKET выставлен", l.LevelIdx, l.Side, l.SizeUSDT))
	} else {
		sr.info(ctx, fmt.Sprintf("L%d %s @ %.0f USDT выставлен", l.LevelIdx, l.Side, l.SizeUSDT))
	}
	return nil
}

func (sr *StrategyRunner) placedCount() int {
	n := 0
	for _, l := range sr.levels {
		if l.Status == LevelPlaced {
			n++
		}
	}
	return n
}

// avgEntry returns weighted average fill price and total quantity across filled levels.
func (sr *StrategyRunner) avgEntry() (avg, totalQty float64) {
	for _, l := range sr.levels {
		if l.Status != LevelFilled || l.FilledPrice == 0 {
			continue
		}
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		avg += l.FilledPrice * qty
		totalQty += qty
	}
	if totalQty > 0 {
		avg /= totalQty
	}
	return
}

// handleLevelFill is called by AccountRunner when a grid level order is filled.
func (sr *StrategyRunner) handleLevelFill(ctx context.Context, levelID string, filledPrice, _ float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Idempotency: market orders may trigger this via both linkID and orderId.
	for _, l := range sr.levels {
		if l.ID == levelID && l.Status == LevelFilled {
			return
		}
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			sr.runner.UnregisterOrder(sr.levels[i].ExchangeOrderID)
			sr.levels[i].Status = LevelFilled
			sr.levels[i].FilledPrice = filledPrice
			sr.info(ctx, fmt.Sprintf("L%d %s @ %.0f USDT исполнен @ %.4f", sr.levels[i].LevelIdx, sr.levels[i].Side, sr.levels[i].SizeUSDT, filledPrice))
			break
		}
	}
	if err := sr.updateTP(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка обновления TP: %v", err))
	}
	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка обновления SL: %v", err))
	}
	if err := sr.placeNextLevels(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровней: %v", err))
	}
}

// updateTP cancels the existing TP order (if any) and places a new one for the full position.
// Must be called with sr.mu held.
func (sr *StrategyRunner) updateTP(ctx context.Context) error {
	avg, totalQty := sr.avgEntry()
	if totalQty == 0 || avg == 0 {
		return nil
	}
	if sr.tpOrderID != "" {
		trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{ //nolint:errcheck
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: sr.tpOrderID,
		})
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}

	tpSide, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
	if tpSide == "" {
		return nil
	}

	tpQty := trader.FormatQty(totalQty, sr.instr.QtyStep)
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d", sr.strategy.ID[:8], sr.cycle.CycleNum)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        tpSide,
		OrderType:   "Limit",
		Qty:         tpQty,
		Price:       trader.FormatPrice(tpPrice, sr.instr.TickSize),
		TimeInForce: "GTC",
		ReduceOnly:  !sr.strategy.HedgeMode, // reduceOnly not valid in hedge mode; positionIdx handles slot
		PositionIdx: positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
		OrderLinkId: linkID,
	})
	if err != nil {
		return fmt.Errorf("place TP: %w", err)
	}
	sr.tpOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("TP выставлен @ %.4f qty=%s", tpPrice, tpQty))
	return nil
}

func tpParams(dir Direction, avg, tpPct float64) (side string, price float64) {
	switch dir {
	case DirectionLong:
		return "Sell", avg * (1 + tpPct/100)
	case DirectionShort:
		return "Buy", avg * (1 - tpPct/100)
	default:
		return "", 0
	}
}

// slParams returns the side, trigger price, and triggerDirection for a stop-market SL order.
// triggerDirection=2: trigger when price falls to/below (long SL). triggerDirection=1: rises to/above (short SL).
func slParams(dir Direction, avg, slPct float64) (side string, triggerPrice float64, triggerDirection int) {
	switch dir {
	case DirectionLong:
		return "Sell", avg * (1 - slPct/100), 2
	case DirectionShort:
		return "Buy", avg * (1 + slPct/100), 1
	default:
		return "", 0, 0
	}
}

// updateSL cancels the existing SL order (if any) and places a new stop-market SL for the full position.
// Must be called with sr.mu held.
func (sr *StrategyRunner) updateSL(ctx context.Context) error {
	if sr.strategy.SLType != SLTypeConditional || sr.strategy.SLPct <= 0 {
		return nil
	}
	avg, totalQty := sr.avgEntry()
	if totalQty == 0 || avg == 0 {
		return nil
	}
	if sr.slOrderID != "" {
		trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{ //nolint:errcheck
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
			OrderId: sr.slOrderID, OrderFilter: "StopOrder",
		})
		sr.runner.UnregisterOrder(sr.slOrderID)
		sr.slOrderID = ""
	}

	slSide, slTrigger, trigDir := slParams(sr.strategy.Direction, avg, sr.strategy.SLPct)
	if slSide == "" {
		return nil
	}

	slQty := trader.FormatQty(totalQty, sr.instr.QtyStep)
	linkID := fmt.Sprintf("SIS_STR-%s-sl-%d", sr.strategy.ID[:8], sr.cycle.CycleNum)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             slSide,
		OrderType:        "Market",
		Qty:              slQty,
		TriggerPrice:     trader.FormatPrice(slTrigger, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
		OrderLinkId:      linkID,
	})
	if err != nil {
		return fmt.Errorf("place SL: %w", err)
	}
	sr.slOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET sl_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("SL выставлен @ %.4f qty=%s", slTrigger, slQty))
	return nil
}

// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.info(ctx, fmt.Sprintf("TP исполнен, цикл %d завершён", sr.cycle.CycleNum))
	sr.closedBySelf = true // suppress the position-close WS event that follows TP fill
	sr.tpOrderID = ""     // already filled — closeCycle must not try to cancel it
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "tp") // cancels SL if present
	sr.maybeRestart(ctx)
}

// handleSLFill is called when the SL order is filled.
func (sr *StrategyRunner) handleSLFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.info(ctx, fmt.Sprintf("SL исполнен, цикл %d завершён", sr.cycle.CycleNum))
	sr.closedBySelf = true // suppress the position-close WS event that follows SL fill
	sr.slOrderID = ""     // already filled — closeCycle must not try to cancel it
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "sl") // cancels TP if present
	sr.maybeRestart(ctx)
}

// maybeRestart checks status and either starts a new cycle or transitions to stopped.
// Must be called with sr.mu held.
func (sr *StrategyRunner) maybeRestart(ctx context.Context) {
	switch sr.strategy.Status {
	case StatusActive:
		go func() {
			if err := sr.startCycle(ctx); err != nil {
				log.Printf("strategy %s: restart cycle: %v", sr.strategy.ID, err)
			}
		}()
	case StatusFinishing:
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID)
		sr.strategy.Status = StatusStopped
		sr.info(ctx, "Стратегия завершила последний цикл и остановлена")
	}
}

// closeCycle marks the current cycle as ended in DB and clears local state.
// Must be called with sr.mu held.
func (sr *StrategyRunner) closeCycle(ctx context.Context, result string) {
	if sr.cycle == nil {
		return
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET ended_at=NOW(), result=$1 WHERE id=$2`, result, sr.cycle.ID)
	if sr.tpOrderID != "" {
		if err := trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: sr.tpOrderID,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("closeCycle: отмена TP %s: %v", sr.tpOrderID, err))
		}
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}
	if sr.slOrderID != "" {
		if err := trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
			OrderId: sr.slOrderID, OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("closeCycle: отмена SL %s: %v", sr.slOrderID, err))
		}
		sr.runner.UnregisterOrder(sr.slOrderID)
		sr.slOrderID = ""
	}
	sr.cycle = nil
	sr.levels = nil
}

// cancelPlacedLevels cancels all placed grid level orders on the exchange.
// Must be called with sr.mu held.
func (sr *StrategyRunner) cancelPlacedLevels(ctx context.Context) {
	for _, l := range sr.levels {
		if l.Status != LevelPlaced {
			continue
		}
		trader.CancelOrder(ctx, sr.runner.creds, trader.CancelRequest{ //nolint:errcheck
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: l.ExchangeOrderID,
		})
		sr.runner.UnregisterOrder(l.ExchangeOrderID)
	}
	if sr.cycle != nil {
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='placed'`, sr.cycle.ID)
	}
}

// cancelAllPlaced is called when a strategy is stopped (removeStrategy goroutine).
func (sr *StrategyRunner) cancelAllPlaced(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.cancelPlacedLevels(ctx)
	if sr.cycle != nil {
		sr.info(ctx, fmt.Sprintf("Цикл %d остановлен, ордера отменены", sr.cycle.CycleNum))
		sr.closeCycle(ctx, "stopped")
	}
}

// restartCycle cancels placed orders, closes the current cycle, and starts a fresh one.
// Called after strategy settings are updated so new parameters take effect immediately.
func (sr *StrategyRunner) restartCycle(ctx context.Context) {
	sr.mu.Lock()
	if sr.strategy.Status != StatusActive {
		sr.mu.Unlock()
		return
	}
	cycleNum := 0
	if sr.cycle != nil {
		cycleNum = sr.cycle.CycleNum
	}
	placedCount := sr.placedCount()
	sr.mu.Unlock()

	sr.info(ctx, fmt.Sprintf("Настройки изменены, цикл %d перезапускается (активных ордеров: %d)", cycleNum, placedCount))

	sr.mu.Lock()
	sr.cancelPlacedLevels(ctx)
	if sr.cycle != nil {
		sr.closeCycle(ctx, "settings_changed")
		log.Printf("strategy %s: cycle %d closed", sr.strategy.ID, cycleNum)
	}
	sr.mu.Unlock()

	instr, err := trader.GetInstrumentInfo(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: instrument info: %v", sr.strategy.ID, err)
	} else {
		sr.mu.Lock()
		sr.instr = instr
		sr.mu.Unlock()
		log.Printf("strategy %s: instrument info loaded (tick=%.8g step=%.8g)",
			sr.strategy.ID, instr.TickSize, instr.QtyStep)
	}

	log.Printf("strategy %s: starting new cycle with updated settings", sr.strategy.ID)
	if err := sr.startCycle(ctx); err != nil {
		log.Printf("strategy %s: start cycle after settings update: %v", sr.strategy.ID, err)
	}
}

// handlePositionClose is called when a position for this strategy's symbol goes to zero.
// If the position was closed manually (not by TP/SL), the cycle is closed and the strategy
// is stopped with a log message.
func (sr *StrategyRunner) handlePositionClose(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// If we closed the position ourselves (TP/SL), ignore the WS position event.
	if sr.closedBySelf {
		sr.closedBySelf = false
		return
	}

	if sr.cycle == nil {
		return // cycle already closed by TP/SL
	}

	// Determine whether our strategy had an open position.
	// We trust the WS event if we have TP/SL orders registered (definitive proof of open position),
	// or if any level was already filled (position might have been opened before TP/SL was placed).
	hasPosition := sr.tpOrderID != "" || sr.slOrderID != ""
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasPosition = true
			break
		}
	}
	if !hasPosition {
		return // no fills and no TP/SL — position close is unrelated to this strategy
	}

	sr.warn(ctx, fmt.Sprintf("Позиция закрыта принудительно (на бирже), цикл %d остановлен", sr.cycle.CycleNum))

	// Cancel all remaining placed grid orders and mark filled levels as cancelled.
	sr.cancelPlacedLevels(ctx)
	cycleID := sr.cycle.ID
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='filled'`, cycleID)
	for i := range sr.levels {
		if sr.levels[i].Status == LevelFilled {
			sr.levels[i].Status = LevelCancelled
		}
	}

	// closeCycle cancels TP and SL orders on the exchange.
	sr.closeCycle(ctx, "manual_close")

	// Stop the strategy so it doesn't restart automatically.
	sr.strategy.Status = StatusStopped
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID)
}

// positionIdxForOpen returns the Bybit positionIdx for an opening order.
// In one-way mode (hedgeMode=false): 0. In hedge mode: 1 for Buy (long slot), 2 for Sell (short slot).
func positionIdxForOpen(hedgeMode bool, side string) int {
	if !hedgeMode {
		return 0
	}
	if side == "Buy" {
		return 1
	}
	return 2
}

// positionIdxForClose returns the Bybit positionIdx for a closing (TP/SL) order.
// In hedge mode, closing a long uses slot 1; closing a short uses slot 2.
func positionIdxForClose(hedgeMode bool, dir Direction) int {
	if !hedgeMode {
		return 0
	}
	if dir == DirectionLong {
		return 1
	}
	return 2
}

// isOrderGone returns true for Bybit errors meaning the order no longer exists.
// Bybit auto-cancels reduce-only TP/SL orders when the position closes, so
// retCode=110001 on cancel is expected and not worth logging as a warning.
func isOrderGone(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "retCode=110001")
}
