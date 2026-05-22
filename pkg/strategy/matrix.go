package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"sis/pkg/trader"
)

// calculateMatrixPrices returns a map from slot index to target price.
// Slot 0 = start price (market entry), negative slots = below, positive slots = above.
func calculateMatrixPrices(startPrice float64, above, below []MatrixLevel) map[int]float64 {
	prices := map[int]float64{0: startPrice}
	prev := startPrice
	for i, lvl := range below {
		prev = prev * (1 - lvl.PriceStepPct/100)
		prices[-(i + 1)] = prev
	}
	prev = startPrice
	for i, lvl := range above {
		prev = prev * (1 + lvl.PriceStepPct/100)
		prices[i+1] = prev
	}
	return prices
}

// filterMatrixLevels returns only the levels with the given direction ("above" or "below").
func filterMatrixLevels(levels []MatrixLevel, dir string) []MatrixLevel {
	var out []MatrixLevel
	for _, l := range levels {
		if l.Direction == dir {
			out = append(out, l)
		}
	}
	return out
}

// matrixLevelConfig looks up TP/SL config pointers for a given slot.
// Returns nil pointers when the slot is out of range or config is absent.
func (sr *StrategyRunner) matrixLevelConfig(slot int) (tpPct, stopPct, stopCondPct, stopReplacePct *float64) {
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		if e == nil {
			return
		}
		return e.TPPct, e.StopPct, e.StopCondPct, e.StopReplacePct
	}
	if slot > 0 {
		above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
		if idx := slot - 1; idx < len(above) {
			l := above[idx]
			return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct
		}
		return
	}
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	if idx := -slot - 1; idx < len(below) {
		l := below[idx]
		return l.TPPct, l.StopPct, l.StopCondPct, l.StopReplacePct
	}
	return
}

// matrixActiveQty returns the sum of qty for all LevelFilled matrix levels.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixActiveQty() float64 {
	total := 0.0
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			qty, _ := strconv.ParseFloat(l.Qty, 64)
			total += qty
		}
	}
	return total
}

// matrixLatestActiveFill returns the last filled non-sl_closed level (by slice order).
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixLatestActiveFill() (*GridLevel, bool) {
	for i := len(sr.levels) - 1; i >= 0; i-- {
		if sr.levels[i].Status == LevelFilled && sr.levels[i].FilledPrice > 0 {
			return &sr.levels[i], true
		}
	}
	return nil, false
}

// matrixLevelSide returns "Buy" for long direction, "Sell" for short.
func matrixLevelSide(dir Direction) string {
	if dir == DirectionShort {
		return "Sell"
	}
	return "Buy"
}

// matrixIsVirtual returns true if the given level uses a virtual order type.
func (sr *StrategyRunner) matrixIsVirtual(l *GridLevel) bool {
	if l.Slot == nil {
		return false
	}
	slot := *l.Slot
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		return e != nil && e.OrderType == "virtual"
	}
	if slot > 0 {
		above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
		idx := slot - 1
		return idx < len(above) && above[idx].OrderType == "virtual"
	}
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	idx := -slot - 1
	return idx < len(below) && below[idx].OrderType == "virtual"
}

// matrixEntryOrderType decides the order type and trigger direction based on
// the relationship between targetPrice and currentPrice for the given direction.
func matrixEntryOrderType(direction string, targetPrice, currentPrice float64) (orderType string, triggerDirection int) {
	if targetPrice == 0 {
		return "Market", 0
	}
	isLong := direction == "long" || direction == string(DirectionLong)
	if isLong {
		if targetPrice < currentPrice {
			return "Limit", 0
		}
		return "StopMarket", 1 // price needs to rise to target
	}
	// Short
	if targetPrice > currentPrice {
		return "Limit", 0
	}
	return "StopMarket", 2 // price needs to fall to target
}

// startMatrixCycle creates a new matrix cycle: fetches price, builds levels, inserts into DB, places orders.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) startMatrixCycle(ctx context.Context) error {
	// Guard: if there are already pending/placed levels for an open cycle, reload instead.
	var existingLevels int
	_ = sr.runner.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategy_levels sl
		 JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		 WHERE sc.strategy_id = $1 AND sc.ended_at IS NULL
		   AND sl.status IN ('pending','placed')`,
		sr.strategy.ID,
	).Scan(&existingLevels)
	if existingLevels > 0 {
		log.Printf("strategy %s: startMatrixCycle skipped — %d active levels already in DB", sr.strategy.ID, existingLevels)
		if err := sr.loadMatrixCycle(ctx); err != nil {
			return fmt.Errorf("startMatrixCycle guard: reload failed: %w", err)
		}
		return nil
	}

	// Close any sub-minQty dust left from a previous cycle before starting fresh.
	sr.closeDustPosition(ctx)

	// Cancel any lingering orders from previous cycles before creating a new one.
	sr.cancelAllStrategyOrders(ctx)

	// Close any orphaned open cycles in DB.
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET ended_at=NOW(), result='orphan'
		 WHERE strategy_id=$1 AND ended_at IS NULL`, sr.strategy.ID)

	// Apply configured leverage.
	lev := sr.strategy.Leverage
	if lev <= 0 {
		lev = 1
	}
	levStr := strconv.Itoa(lev)
	if lerr := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
		Symbol:       sr.strategy.Symbol,
		Category:     sr.strategy.Category,
		BuyLeverage:  levStr,
		SellLeverage: levStr,
	}); lerr != nil && !strings.Contains(lerr.Error(), "110043") {
		log.Printf("strategy %s: set leverage %d: %v", sr.strategy.ID, lev, lerr)
	}

	// Ensure position mode BEFORE taking the lock.
	if !sr.ensurePositionMode(ctx) {
		sr.mu.Lock()
		sr.stopWithMessage(ctx, "Не удалось переключить position mode — стратегия остановлена")
		sr.mu.Unlock()
		return fmt.Errorf("position mode mismatch, matrix cycle not started")
	}

	// Fetch mark price before taking lock.
	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		return fmt.Errorf("fetch price: %w", err)
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.strategy.Status == StatusStopped {
		return nil
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
		ID:        cycleID,
		StrategyID: sr.strategy.ID,
		CycleNum:  maxCycle + 1,
		StartPrice: price,
		StartedAt: time.Now(),
	}
	sr.levels = nil

	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(price, above, below)

	side := matrixLevelSide(sr.strategy.Direction)
	levelIdx := 1

	// Helper: get SizePct for a given slot.
	sizePctForSlot := func(slot int) float64 {
		if slot == 0 {
			if sr.strategy.MatrixEntryLevel != nil {
				return sr.strategy.MatrixEntryLevel.SizePct
			}
			return 0
		}
		if slot > 0 {
			idx := slot - 1
			if idx < len(above) {
				return above[idx].SizePct
			}
			return 0
		}
		idx := -slot - 1
		if idx < len(below) {
			return below[idx].SizePct
		}
		return 0
	}

	// Build slot list: entry (0), below (-1, -2, ...), above (+1, +2, ...)
	var slots []int
	slots = append(slots, 0)
	for i := range below {
		slots = append(slots, -(i + 1))
	}
	for i := range above {
		slots = append(slots, i+1)
	}

	for _, slot := range slots {
		targetPrice, ok := prices[slot]
		if !ok {
			continue
		}
		sizeUSDT := sizePctForSlot(slot) / 100 * sr.strategy.GridSizeUSDT
		// Guard: skip slots with zero quantity (prevents zero-qty orders to exchange)
		if sizeUSDT == 0 {
			levelIdx++
			continue
		}
		priceForQty := targetPrice
		if priceForQty == 0 {
			priceForQty = price // market entry: use current price for qty calc
		}
		qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)

		slotCopy := slot
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side, targetPrice, sizeUSDT, qty, slotCopy,
		).Scan(&levelID); err != nil {
			log.Printf("strategy %s: insert matrix level slot=%d: %v", sr.strategy.ID, slot, err)
			levelIdx++
			continue
		}
		sr.levels = append(sr.levels, GridLevel{
			ID:          levelID,
			LevelIdx:    levelIdx,
			Side:        side,
			TargetPrice: targetPrice,
			SizeUSDT:    sizeUSDT,
			Qty:         qty,
			Status:      LevelPending,
			Slot:        &slotCopy,
		})
		levelIdx++
	}

	sr.info(ctx, fmt.Sprintf("Matrix цикл %d запущен @ %.4f, уровней: %d",
		sr.cycle.CycleNum, price, len(sr.levels)))
	sr.clearManualAlert(ctx)

	// Place slot 0 (market entry) first, then non-virtual remaining levels.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil {
			continue
		}
		slot := *l.Slot
		if slot == 0 {
			if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
				sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix entry L%d: %v", l.LevelIdx, err))
			}
		}
	}
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil {
			continue
		}
		slot := *l.Slot
		if slot == 0 {
			continue // already placed above
		}
		if sr.matrixIsVirtual(l) {
			continue // virtual levels are fired by the price monitor
		}
		if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix L%d (slot=%d): %v", l.LevelIdx, slot, err))
		}
	}

	go sr.launchMatrixPriceMonitor()
	return nil
}

// placeMatrixLevel places a single pending matrix level on the exchange.
// currentPrice is the mark price at the time of placement, used to decide order type.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeMatrixLevel(ctx context.Context, l *GridLevel, currentPrice float64) error {
	if sr.strategy.Status == StatusStopped {
		return nil
	}

	// Guard: only place if level is pending (prevent duplicate orders)
	if l.Status != LevelPending {
		return nil
	}

	// Safe zone suppression.
	if sr.matrixSafeZone != nil && l.TargetPrice > 0 && sr.matrixSafeZone.Contains(l.TargetPrice) {
		return nil
	}

	// Virtual levels are handled by the price monitor.
	if sr.matrixIsVirtual(l) {
		return nil
	}

	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}

	orderTypeName, trigDir := matrixEntryOrderType(string(sr.strategy.Direction), l.TargetPrice, currentPrice)

	var req trader.OrderRequest
	switch orderTypeName {
	case "Market":
		req = trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        l.Side,
			OrderType:   "Market",
			Qty:         l.Qty,
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		}
	case "Limit":
		formattedPrice := trader.FormatPrice(l.TargetPrice, sr.instr.TickSize)
		req = trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        l.Side,
			OrderType:   "Limit",
			Qty:         l.Qty,
			Price:       formattedPrice,
			TimeInForce: "GTC",
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		}
	default: // StopMarket
		formattedPrice := trader.FormatPrice(l.TargetPrice, sr.instr.TickSize)
		req = trader.OrderRequest{
			Symbol:           sr.strategy.Symbol,
			Category:         sr.strategy.Category,
			Side:             l.Side,
			OrderType:        "Market",
			Qty:              l.Qty,
			TriggerPrice:     formattedPrice,
			TriggerDirection: trigDir,
			TriggerBy:        "LastPrice",
			OrderFilter:      "StopOrder",
			PositionIdx:      positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId:      linkID,
		}
	}

	// Pre-register by linkID before placing so market order fills that arrive
	// before RegisterOrder(orderId) is called are not dropped.
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.tradeStream.PlaceOrder(ctx, req)
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		if isDuplicateLinkId(err) {
			// Find index of this level in sr.levels for recoverDuplicateLevel.
			for idx := range sr.levels {
				if sr.levels[idx].ID == l.ID {
					sr.recoverDuplicateLevel(ctx, idx, linkID)
					break
				}
			}
			return nil
		}
		if isInsufficientBalance(err) {
			sr.stopWithMessage(ctx, "Недостаточно баланса для выставления ордера — стратегия остановлена")
			return err
		}
		return err
	}

	roundedPrice := l.TargetPrice
	if l.TargetPrice > 0 {
		roundedPrice, _ = strconv.ParseFloat(trader.FormatPrice(l.TargetPrice, sr.instr.TickSize), 64)
		l.TargetPrice = roundedPrice
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW(),
		 target_price=CASE WHEN $4::numeric > 0 THEN $4::numeric ELSE target_price END WHERE id=$3`,
		result.OrderId, linkID, l.ID, roundedPrice,
	); err != nil {
		sr.runner.UnregisterOrder(linkID)
		return err
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	if l.TargetPrice == 0 {
		sr.info(ctx, fmt.Sprintf("Matrix L%d %s MARKET (%.0f USDT)", l.LevelIdx, l.Side, l.SizeUSDT))
	} else {
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		sr.info(ctx, fmt.Sprintf("Matrix L%d %s @ %.4f (%.0f USDT)", l.LevelIdx, l.Side, l.TargetPrice, qty*l.TargetPrice))
	}
	return nil
}

// loadMatrixCycle is a stub — fully implemented in Task 9.
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
	return sr.loadActiveCycle(ctx)
}

// matrixStopCondThreshold computes the price level that must be crossed to
// trigger a stop-condition SL replacement. condPct is always positive (absolute value).
func matrixStopCondThreshold(dir Direction, fillPrice, condPct float64) float64 {
	if dir == DirectionLong {
		return fillPrice * (1 + condPct/100)
	}
	return fillPrice * (1 - condPct/100)
}

// matrixStopReplaceTrigger computes the new SL trigger price after stop-condition fires.
func matrixStopReplaceTrigger(dir Direction, fillPrice, replacePct float64) float64 {
	if dir == DirectionLong {
		return fillPrice * (1 + replacePct/100)
	}
	return fillPrice * (1 - replacePct/100)
}

// launchMatrixPriceMonitor starts a goroutine that polls mark price every 5 seconds.
// It replaces any existing monitor. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) launchMatrixPriceMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel
	stratID := sr.strategy.ID
	creds := sr.runner.creds
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	go func() {
		defer cancel()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sr.mu.Lock()
				hasCycle := sr.cycle != nil
				sr.mu.Unlock()
				if !hasCycle {
					return
				}
				price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
				if err != nil {
					log.Printf("strategy %s: matrix price monitor: %v", stratID, err)
					continue
				}
				sr.submit(func(taskCtx context.Context) {
					sr.mu.Lock()
					defer sr.mu.Unlock()
					sr.matrixPriceTick(taskCtx, price)
				})
			}
		}
	}()
}

// matrixPriceTick is called on each 5-second price poll.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPriceTick(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil {
		return
	}

	// 1. Check Safe Zone exit
	if sr.matrixSafeZone != nil && !sr.matrixSafeZone.Contains(currentPrice) {
		sr.info(ctx, fmt.Sprintf("Safe Zone очищена: цена %.4f вышла из [%.4f, %.4f]",
			currentPrice, sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
		sr.matrixSafeZone = nil
		// Trigger re-placement for sl_closed slots
		sr.matrixReplaceSlots(ctx, currentPrice)
	}

	// 2. Trigger virtual levels whose target price has been crossed
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || l.ExchangeOrderID != "" {
			continue
		}
		if !sr.matrixIsVirtual(l) {
			continue
		}
		var crossed bool
		if sr.strategy.Direction == DirectionLong {
			crossed = currentPrice >= l.TargetPrice
		} else {
			crossed = currentPrice <= l.TargetPrice
		}
		if crossed {
			sr.matrixTriggerVirtualLevel(ctx, l)
		}
	}

	// 3. Check stop conditions for filled levels
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLReplaced || l.Slot == nil {
			continue
		}
		_, _, stopCondPct, stopReplacePct := sr.matrixLevelConfig(*l.Slot)
		if stopCondPct == nil || stopReplacePct == nil {
			continue
		}
		condPct := math.Abs(*stopCondPct)
		threshold := matrixStopCondThreshold(sr.strategy.Direction, l.FilledPrice, condPct)
		var condMet bool
		if sr.strategy.Direction == DirectionLong {
			condMet = currentPrice >= threshold
		} else {
			condMet = currentPrice <= threshold
		}
		if !condMet {
			continue
		}
		// Replace SL
		newTrigger := matrixStopReplaceTrigger(sr.strategy.Direction, l.FilledPrice, *stopReplacePct)
		if l.SLOrderID != "" {
			old := l.SLOrderID
			sr.runner.UnregisterOrder(old)
			l.SLOrderID = ""
			sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
				Symbol:      sr.strategy.Symbol,
				Category:    sr.strategy.Category,
				OrderId:     old,
				OrderFilter: "StopOrder",
			})
		}

		var slSide string
		var trigDir int
		if sr.strategy.Direction == DirectionLong {
			slSide, trigDir = "Sell", 2
		} else {
			slSide, trigDir = "Buy", 1
		}
		sr.matrixSLSeq++
		linkID := fmt.Sprintf("SIS_STR-%s-msl-%s-%d",
			sr.strategy.ID[:8], l.ID[:8], sr.matrixSLSeq)
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		fmtQty := trader.FormatQty(qty, sr.instr.QtyStep, sr.instr.MinQty)
		if fmtQty == "0" || fmtQty == "" {
			sr.warn(ctx, fmt.Sprintf("matrixPriceTick: stop-cond SL replace L%d: qty rounds to zero, skipping", l.LevelIdx))
			continue
		}
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
		sr.runner.RegisterOrder(linkID, ref)

		result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:           sr.strategy.Symbol,
			Category:         sr.strategy.Category,
			Side:             slSide,
			OrderType:        "Market",
			Qty:              fmtQty,
			TriggerPrice:     trader.FormatPrice(newTrigger, sr.instr.TickSize),
			TriggerBy:        "LastPrice",
			TriggerDirection: trigDir,
			OrderFilter:      "StopOrder",
			ReduceOnly:       !sr.strategy.HedgeMode,
			PositionIdx:      positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
			OrderLinkId:      linkID,
		})
		if err != nil {
			sr.runner.UnregisterOrder(linkID)
			sr.errlog(ctx, fmt.Sprintf("Matrix stop-cond SL replace L%d: %v", l.LevelIdx, err))
			continue
		}
		l.SLOrderID = result.OrderId
		l.SLPrice = newTrigger
		l.SLReplaced = true
		sr.runner.RegisterOrder(result.OrderId, ref)
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET sl_order_id=$1, sl_price=$2, sl_replaced=true WHERE id=$3`,
			result.OrderId, newTrigger, l.ID,
		)
		sr.info(ctx, fmt.Sprintf("Matrix SL L%d переставлен → %.4f (stop cond сработал)", l.LevelIdx, newTrigger))
	}
}

// matrixTriggerVirtualLevel fires a virtual level by placing a market order.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixTriggerVirtualLevel(ctx context.Context, l *GridLevel) {
	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-v%d",
		sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        l.Side,
		OrderType:   "Market",
		Qty:         l.Qty,
		PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
		OrderLinkId: linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("Matrix virtual L%d: %v", l.LevelIdx, err))
		return
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.info(ctx, fmt.Sprintf("Matrix virtual L%d (slot %v) запущен @ market", l.LevelIdx, l.Slot))
}

// matrixReplaceSlots re-places levels for any slot that has no active (pending/placed/filled) row.
// Called after safe zone clears. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReplaceSlots(ctx context.Context, currentPrice float64) {
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(sr.cycle.StartPrice, above, below)

	// Collect all slots in the config
	slots := []int{0}
	for i := range above {
		slots = append(slots, i+1)
	}
	for i := range below {
		slots = append(slots, -(i + 1))
	}

	// Find which slots have no active level
	activeSlots := map[int]bool{}
	for _, l := range sr.levels {
		if l.Slot == nil {
			continue
		}
		switch l.Status {
		case LevelPending, LevelPlaced, LevelFilled:
			activeSlots[*l.Slot] = true
		}
	}

	side := matrixLevelSide(sr.strategy.Direction)
	levelIdx := len(sr.levels) + 1

	for _, slot := range slots {
		if activeSlots[slot] {
			continue
		}
		targetPrice, ok := prices[slot]
		if !ok {
			continue
		}
		var sizeUSDT float64
		if slot == 0 {
			if sr.strategy.MatrixEntryLevel == nil {
				continue
			}
			sizeUSDT = sr.strategy.MatrixEntryLevel.SizePct / 100 * sr.strategy.GridSizeUSDT
		} else if slot > 0 {
			idx := slot - 1
			if idx >= len(above) {
				continue
			}
			sizeUSDT = above[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
		} else {
			idx := -slot - 1
			if idx >= len(below) {
				continue
			}
			sizeUSDT = below[idx].SizePct / 100 * sr.strategy.GridSizeUSDT
		}
		priceForQty := targetPrice
		if priceForQty == 0 {
			priceForQty = currentPrice
		}
		qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
		s := slot
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, sr.cycle.ID, levelIdx, side, targetPrice, sizeUSDT, qty, slot,
		).Scan(&levelID); err != nil {
			log.Printf("strategy %s: matrix re-entry slot %d insert: %v", sr.strategy.ID, slot, err)
			continue
		}
		newLevel := GridLevel{
			ID: levelID, LevelIdx: levelIdx, Side: side,
			TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
			Status: LevelPending, Slot: &s,
		}
		sr.levels = append(sr.levels, newLevel)
		if err := sr.placeMatrixLevel(ctx, &sr.levels[len(sr.levels)-1], currentPrice); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Matrix re-entry slot %d: %v", slot, err))
		} else {
			sr.info(ctx, fmt.Sprintf("Matrix re-entry slot %d @ %.4f (после Safe Zone)", slot, targetPrice))
		}
		levelIdx++
	}
}

// matrixSLTrigger computes the per-level SL trigger price.
// stopPct is expected negative (e.g. -2.0 means 2% adverse from fill).
func matrixSLTrigger(dir Direction, fillPrice, stopPct float64) float64 {
	if dir == DirectionLong {
		return fillPrice * (1 + stopPct/100) // stopPct < 0 → trigger below fill
	}
	return fillPrice * (1 - stopPct/100) // stopPct < 0 → trigger above fill
}

// handleMatrixLevelFill is the matrix-specific handler called when a level fill event arrives
// for a strategy_type="dca" strategy.
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixLevelFill(ctx context.Context, levelID string, filledPrice float64) {
	// Idempotency guard
	for _, l := range sr.levels {
		if l.ID == levelID && l.Status == LevelFilled {
			return
		}
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	var filled *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			sr.runner.UnregisterOrder(sr.levels[i].ExchangeOrderID)
			sr.levels[i].Status = LevelFilled
			sr.levels[i].FilledPrice = filledPrice
			filled = &sr.levels[i]
			sr.info(ctx, fmt.Sprintf("Matrix L%d (slot %v) %s @ %.4f исполнен",
				sr.levels[i].LevelIdx, sr.levels[i].Slot, sr.levels[i].Side, filledPrice))
			break
		}
	}
	if filled == nil {
		return
	}

	// Place per-level SL if configured for this slot
	if filled.Slot != nil {
		_, stopPct, _, _ := sr.matrixLevelConfig(*filled.Slot)
		if stopPct != nil {
			sr.matrixPlacePerLevelSL(ctx, filled, filledPrice, *stopPct)
		}
	}

	// Update global TP based on latest active fill
	sr.matrixUpdateTP(ctx)
}

// matrixPlacePerLevelSL places a reduce-only stop-market SL for one filled level.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlacePerLevelSL(ctx context.Context, l *GridLevel, fillPrice, stopPct float64) {
	trigger := matrixSLTrigger(sr.strategy.Direction, fillPrice, stopPct)

	var slSide string
	var trigDir int
	if sr.strategy.Direction == DirectionLong {
		slSide, trigDir = "Sell", 2 // fires when price falls to/below trigger
	} else {
		slSide, trigDir = "Buy", 1 // fires when price rises to/above trigger
	}

	sr.matrixSLSeq++
	linkID := fmt.Sprintf("SIS_STR-%s-msl-%s-%d",
		sr.strategy.ID[:8], l.ID[:8], sr.matrixSLSeq)

	qty, _ := strconv.ParseFloat(l.Qty, 64)
	fmtQty := trader.FormatQty(qty, sr.instr.QtyStep, sr.instr.MinQty)
	if fmtQty == "0" || fmtQty == "" {
		sr.warn(ctx, fmt.Sprintf("matrixPlacePerLevelSL: qty rounds to zero for L%d", l.LevelIdx))
		return
	}

	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "matrix_sl"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             slSide,
		OrderType:        "Market",
		Qty:              fmtQty,
		TriggerPrice:     trader.FormatPrice(trigger, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("Matrix per-level SL для L%d: %v", l.LevelIdx, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLPrice = trigger
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_price=$2 WHERE id=$3`,
		result.OrderId, trigger, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("Matrix SL для L%d @ %.4f выставлен", l.LevelIdx, trigger))
}

// matrixUpdateTP cancels the existing global TP and places a new one based on the
// latest active filled level's fill_price and that level's tp_pct config.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixUpdateTP(ctx context.Context) {
	if sr.instr.QtyStep == 0 {
		return
	}
	latest, ok := sr.matrixLatestActiveFill()
	if !ok {
		// No active fills — cancel TP if it exists
		if sr.tpOrderID != "" {
			old := sr.tpOrderID
			sr.runner.UnregisterOrder(old)
			sr.tpOrderID = ""
			sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
				Symbol:   sr.strategy.Symbol,
				Category: sr.strategy.Category,
				OrderId:  old,
			})
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)
		}
		return
	}

	var tpPctVal *float64
	if latest.Slot != nil {
		tpPctVal, _, _, _ = sr.matrixLevelConfig(*latest.Slot)
	}
	if tpPctVal == nil {
		return // this level has no TP configured
	}

	var tpPrice float64
	var tpSide string
	if sr.strategy.Direction == DirectionLong {
		tpSide = "Sell"
		tpPrice = latest.FilledPrice * (1 + *tpPctVal/100)
	} else {
		tpSide = "Buy"
		tpPrice = latest.FilledPrice * (1 - *tpPctVal/100)
	}

	activeQty := sr.matrixActiveQty()
	if activeQty == 0 {
		return
	}
	tpQty := trader.FormatQty(activeQty, sr.instr.QtyStep, sr.instr.MinQty)
	if tpQty == "0" || tpQty == "" {
		return
	}

	// Cancel existing TP
	if sr.tpOrderID != "" {
		old := sr.tpOrderID
		sr.runner.UnregisterOrder(old)
		sr.tpOrderID = ""
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:   sr.strategy.Symbol,
			Category: sr.strategy.Category,
			OrderId:  old,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("matrixUpdateTP: cancel old TP: %v", err))
		}
	}

	sr.tpPlaceSeq++
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, sr.tpPlaceSeq)
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        tpSide,
		OrderType:   "Limit",
		Qty:         tpQty,
		Price:       trader.FormatPrice(tpPrice, sr.instr.TickSize),
		TimeInForce: "GTC",
		ReduceOnly:  !sr.strategy.HedgeMode,
		PositionIdx: positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
		OrderLinkId: linkID,
	})
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("matrixUpdateTP: place TP: %v", err))
		return
	}
	sr.tpOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("Matrix TP @ %.4f qty=%s выставлен", tpPrice, tpQty))
}

// createMatrixSafeZone builds a safe zone centered on slTrigger with ±safeZonePct.
func createMatrixSafeZone(slTrigger, safeZonePct float64) *MatrixSafeZone {
	return &MatrixSafeZone{
		Low:       slTrigger * (1 - safeZonePct/100),
		High:      slTrigger * (1 + safeZonePct/100),
		CreatedAt: time.Now(),
	}
}

// handleMatrixSLFill is called when a per-level SL fires (refType="matrix_sl").
// Must NOT be called with sr.mu held (submitted to worker via sr.submit).
func (sr *StrategyRunner) handleMatrixSLFill(ctx context.Context, levelID string, filledPrice float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.cycle == nil {
		return
	}

	// Find and mark the level sl_closed
	var closed *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			closed = &sr.levels[i]
			break
		}
	}
	if closed == nil {
		return
	}

	slTrigger := closed.SLPrice
	if slTrigger == 0 {
		slTrigger = filledPrice
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='sl_closed' WHERE id=$1`, levelID,
	)
	closed.Status = LevelSLClosed
	closed.SLOrderID = ""
	sr.warn(ctx, fmt.Sprintf("Matrix SL сработал L%d (slot %v) @ %.4f",
		closed.LevelIdx, closed.Slot, slTrigger))

	// Create Safe Zone
	if sr.strategy.SafeZonePct > 0 {
		sr.matrixSafeZone = createMatrixSafeZone(slTrigger, sr.strategy.SafeZonePct)
		sr.info(ctx, fmt.Sprintf("Safe Zone создана: [%.4f, %.4f]",
			sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
	}

	// Recalculate global TP
	sr.matrixUpdateTP(ctx)

	// If no active filled levels remain → close cycle
	if sr.matrixActiveQty() == 0 {
		sr.info(ctx, "Все уровни закрыты по SL — цикл завершается")
		sr.closedBySelf = true
		sr.closeCycle(ctx, "sl")
		sr.maybeRestart(ctx)
	}
}

// handleMatrixSLCancelled re-places the per-level SL when it is externally cancelled.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixSLCancelled(ctx context.Context, levelID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	for i := range sr.levels {
		l := &sr.levels[i]
		if l.ID == levelID && l.Status == LevelFilled && l.FilledPrice > 0 {
			if l.Slot == nil {
				return
			}
			_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
			if stopPct == nil {
				return
			}
			l.SLOrderID = ""
			sr.warn(ctx, fmt.Sprintf("Matrix SL для L%d отменён биржей — переставляю", l.LevelIdx))
			sr.matrixPlacePerLevelSL(ctx, l, l.FilledPrice, *stopPct)
			return
		}
	}
}

// matrixCancelPerLevelSLs cancels all active per-level SL orders.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixCancelPerLevelSLs(ctx context.Context) {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.SLOrderID == "" {
			continue
		}
		orderID := l.SLOrderID
		sr.runner.UnregisterOrder(orderID)
		l.SLOrderID = ""
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			OrderId:     orderID,
			OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("matrixCancelPerLevelSLs: %s: %v", orderID, err))
		}
	}
}
