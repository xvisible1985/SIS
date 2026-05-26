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

func slotStr(s *int) string {
	if s == nil {
		return "nil"
	}
	return strconv.Itoa(*s)
}

// calculateMatrixPrices returns a map from slot index to target price.
// Slot 0 = start price (market entry), negative slots = below, positive slots = above.
// For short direction stepMul=-1 inverts all steps so positive slots land below entry and
// negative slots land above entry (in-direction DCA for short is downward).
func calculateMatrixPrices(startPrice float64, above, below []MatrixLevel, dir Direction) map[int]float64 {
	stepMul := 1.0
	if dir == DirectionShort {
		stepMul = -1.0
	}
	prices := map[int]float64{0: startPrice}
	prev := startPrice
	for i, lvl := range below {
		prev = prev * (1 + stepMul*lvl.PriceStepPct/100)
		prices[-(i + 1)] = prev
	}
	prev = startPrice
	for i, lvl := range above {
		prev = prev * (1 + stepMul*lvl.PriceStepPct/100)
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
// ForceVirtual is set at runtime when the exchange rejects placement (e.g. 110007).
func (sr *StrategyRunner) matrixIsVirtual(l *GridLevel) bool {
	if l.ForceVirtual {
		return true
	}
	if l.Slot == nil {
		return false
	}
	slot := *l.Slot
	if slot == 0 {
		e := sr.strategy.MatrixEntryLevel
		return e != nil && e.OrderType == "virtual"
	}
	if sr.strategy.Direction == DirectionShort {
		if slot > 0 {
			// Short: positive slots are in-direction (below entry) — respect order_type config.
			above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
			idx := slot - 1
			return idx < len(above) && above[idx].OrderType == "virtual"
		}
		// Short: negative slots land above entry (against direction) — always virtual.
		return true
	}
	if slot > 0 {
		return true // long: above levels always virtual — triggered by price monitor when price rises
	}
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	idx := -slot - 1
	return idx < len(below) && below[idx].OrderType == "virtual"
}

// matrixEntryOrderType decides the order type and trigger direction based on
// the relationship between targetPrice and currentPrice for the given direction.
func matrixEntryOrderType(direction string, targetPrice, currentPrice float64) (orderType string, triggerDirection int) {
	if targetPrice == 0 || targetPrice == currentPrice {
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

	sr.matrixSafeZone = nil // defensive reset for fresh cycle

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
	prices := calculateMatrixPrices(price, above, below, sr.strategy.Direction)

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

	for _, l := range sr.levels {
		virt := ""
		if sr.matrixIsVirtual(&l) {
			virt = " [virtual]"
		}
		sr.info(ctx, fmt.Sprintf("  L%d slot=%v %.4f qty=%s size=%.2f USDT%s",
			l.LevelIdx, l.Slot, l.TargetPrice, l.Qty, l.SizeUSDT, virt))
	}
	sr.info(ctx, fmt.Sprintf("Matrix цикл %d запущен @ %.4f, уровней: %d (gridSize=%.2f USDT)",
		sr.cycle.CycleNum, price, len(sr.levels), sr.strategy.GridSizeUSDT))
	sr.clearManualAlert(ctx)

	// Place slot 0 (market entry) first. If virtual, trigger immediately — L0 is always
	// at the current price, so the "wait for price cross" condition is already met.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil {
			continue
		}
		slot := *l.Slot
		if slot == 0 {
			if sr.matrixIsVirtual(l) {
				sr.matrixTriggerVirtualLevel(ctx, l)
			} else {
				if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix entry L%d: %v", l.LevelIdx, err))
				}
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
			// Not enough balance — fall back to virtual so the level is triggered
			// by the price monitor instead of stopping the whole strategy.
			l.ForceVirtual = true
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET force_virtual=true WHERE id=$1`, l.ID)
			sr.warn(ctx, fmt.Sprintf("Matrix L%d (slot %s): недостаточно баланса — переведён в виртуальный режим", l.LevelIdx, slotStr(l.Slot)))
			return nil
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

// loadMatrixCycle loads an existing cycle when startMatrixCycle detects
// active levels already in DB (the guard path). It calls loadActiveCycle
// to read levels, restores the safe zone, and re-launches the price monitor.
// Service-restart resumption goes through loadOrStart → directly calls
// restoreMatrixSafeZone + launchMatrixPriceMonitor.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
	if err := sr.loadActiveCycle(ctx); err != nil {
		return err
	}
	sr.restoreMatrixSafeZone(ctx)
	// Trigger virtual L0 immediately if it's still pending — the price-cross condition
	// is already satisfied for the entry slot, and the price monitor won't fire it
	// because currentPrice >= TargetPrice only holds at the moment of cycle start.
	sr.mu.Lock()
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot != nil && *l.Slot == 0 && l.Status == LevelPending && sr.matrixIsVirtual(l) {
			sr.matrixTriggerVirtualLevel(ctx, l)
			break
		}
	}
	sr.mu.Unlock()
	sr.launchMatrixPriceMonitor()
	return nil
}

// restoreMatrixSafeZone checks the last sl_closed level in the current cycle
// and rebuilds the safe zone if it is recent (< 1 hour) and price is still inside.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) restoreMatrixSafeZone(ctx context.Context) {
	sr.mu.Lock()
	if sr.cycle == nil || sr.strategy.SafeZonePct <= 0 {
		sr.mu.Unlock()
		return
	}
	cycleID := sr.cycle.ID
	sr.mu.Unlock()

	var slPrice float64
	var closedAt time.Time
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT COALESCE(sl_price,0), COALESCE(filled_at, placed_at, NOW())
         FROM strategy_levels
         WHERE cycle_id=$1 AND status='sl_closed' AND sl_price IS NOT NULL
         ORDER BY filled_at DESC NULLS LAST LIMIT 1`,
		cycleID,
	).Scan(&slPrice, &closedAt)
	if err != nil || slPrice == 0 {
		return
	}
	if time.Since(closedAt) > time.Hour {
		return
	}

	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		return
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.cycle == nil {
		return
	}
	zone := createMatrixSafeZone(slPrice, sr.strategy.SafeZonePct)
	if zone.Contains(price) {
		sr.matrixSafeZone = zone
		log.Printf("strategy %s: restored Safe Zone [%.4f, %.4f]", sr.strategy.ID, zone.Low, zone.High)
	}
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

// launchMatrixPriceMonitor starts a goroutine that feeds real-time mark prices
// from TickerHub into matrixPriceTick. Falls back to 5-second REST polling if
// the signal engine is unavailable. Must be called WITHOUT sr.mu held.
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
	symbol := sr.strategy.Symbol
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	if se == nil {
		go sr.runMatrixPriceMonitorPolling(ctx, cancel, stratID, symbol)
		return
	}

	// Buffered channel holds the latest price; old values are overwritten.
	priceCh := make(chan float64, 1)

	go func() {
		unsub := se.PriceHub().Subscribe(symbol, func(markPrice float64) {
			// Non-blocking: drain stale price, insert latest.
			select {
			case <-priceCh:
			default:
			}
			select {
			case priceCh <- markPrice:
			default:
			}
		})
		defer cancel()
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case price := <-priceCh:
				sr.mu.Lock()
				hasCycle := sr.cycle != nil
				sr.mu.Unlock()
				if !hasCycle {
					return
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

// runMatrixPriceMonitorPolling is the REST fallback when signalEngine is nil.
func (sr *StrategyRunner) runMatrixPriceMonitorPolling(ctx context.Context, cancel context.CancelFunc, stratID, symbol string) {
	defer cancel()
	sr.mu.Lock()
	creds := sr.runner.creds
	category := sr.strategy.Category
	sr.mu.Unlock()

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
}

// matrixPriceTick is called on each mark price update from TickerHub or the REST fallback poll.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPriceTick(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil {
		return
	}
	sr.lastMatrixPrice = currentPrice

	// 1. Check Safe Zone exit
	if sr.matrixSafeZone != nil && !sr.matrixSafeZone.Contains(currentPrice) {
		sr.info(ctx, fmt.Sprintf("Safe Zone очищена: цена %.4f вышла из [%.4f, %.4f]",
			currentPrice, sr.matrixSafeZone.Low, sr.matrixSafeZone.High))
		sr.matrixSafeZone = nil
		// Trigger re-placement for sl_closed slots
		sr.matrixReplaceSlots(ctx, currentPrice)
	}

	// 2. Trigger virtual levels whose target price has been crossed.
	// Crossing condition is slot-based:
	//   slot < 0 (below levels): fire when price drops to/past target
	//   slot >= 0 (entry or above): fire when price rises to/past target
	// For entry (slot=0, targetPrice=0) the condition is always true.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || l.ExchangeOrderID != "" {
			continue
		}
		if !sr.matrixIsVirtual(l) {
			continue
		}
		var crossed bool
		if l.TargetPrice == 0 {
			crossed = true
		} else if sr.strategy.Direction == DirectionShort {
			// Short: positive slots land below entry (price must drop), negative above (price must rise).
			if l.Slot != nil && *l.Slot > 0 {
				crossed = currentPrice <= l.TargetPrice
			} else {
				crossed = currentPrice >= l.TargetPrice
			}
		} else if l.Slot != nil && *l.Slot < 0 {
			crossed = currentPrice <= l.TargetPrice
		} else {
			crossed = currentPrice >= l.TargetPrice
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
			sr.strategy.ID[:8], matrixSlotLinkStr(l), sr.matrixSLSeq)
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

	// 4. Retry placing per-level SL for filled levels where SL is missing.
	// Handles cases where SL placement failed at fill time: zero fill price (race with
	// position creation) or Bybit rejection. Retries only when price is at or past the
	// fill level and the trigger would not fire immediately.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
			continue
		}
		_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
		if stopPct == nil {
			continue
		}
		// Use fill price if available; fall back to current price (e.g. L0 market fill with AvgPrice=0).
		priceRef := l.FilledPrice
		if priceRef <= 0 {
			priceRef = currentPrice
		}
		trigger := matrixSLTrigger(sr.strategy.Direction, priceRef, *stopPct)
		// Skip if current price would immediately fire the stop.
		if sr.strategy.Direction == DirectionShort && currentPrice >= trigger {
			continue
		}
		if sr.strategy.Direction == DirectionLong && currentPrice <= trigger {
			continue
		}
		// For levels with a known fill price: only retry once price returns to the fill level.
		if l.FilledPrice > 0 {
			if sr.strategy.Direction == DirectionShort && currentPrice < l.FilledPrice {
				continue
			}
			if sr.strategy.Direction == DirectionLong && currentPrice > l.FilledPrice {
				continue
			}
		}
		sr.warn(ctx, fmt.Sprintf("Matrix L%d: SL не выставлен — повторная попытка (ref=%.4f)", l.LevelIdx, priceRef))
		sr.matrixPlacePerLevelSL(ctx, l, priceRef, *stopPct)
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
	sr.info(ctx, fmt.Sprintf("Matrix virtual L%d (slot %s) запущен @ market", l.LevelIdx, slotStr(l.Slot)))
}

// matrixReplaceSlots re-places levels for any slot that has no active (pending/placed/filled) row.
// Called after safe zone clears. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReplaceSlots(ctx context.Context, currentPrice float64) {
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(sr.cycle.StartPrice, above, below, sr.strategy.Direction)

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

		// Remove any sl_closed rows for this slot before inserting a fresh pending row.
		// This keeps exactly one strategy_levels row per slot in the DB.
		var kept []GridLevel
		for _, l := range sr.levels {
			if l.Slot != nil && *l.Slot == slot && l.Status == LevelSLClosed {
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`DELETE FROM strategy_levels WHERE id=$1`, l.ID)
				continue
			}
			kept = append(kept, l)
		}
		sr.levels = kept
		levelIdx = len(sr.levels) + 1

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
		placed := &sr.levels[len(sr.levels)-1]
		if slot == 0 && sr.matrixIsVirtual(placed) {
			// L0 virtual re-entry: trigger immediately (already at or near start price)
			sr.matrixTriggerVirtualLevel(ctx, placed)
		} else if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Matrix re-entry slot %d: %v", slot, err))
		} else {
			sr.info(ctx, fmt.Sprintf("Matrix re-entry slot %d @ %.4f (после Safe Zone)", slot, targetPrice))
		}
		levelIdx++
	}
}

// resumeMatrixAfterSignal re-places all pending/cancelled matrix levels after the signal
// resumes (mirror of resumeGridAfterSignal for DCA strategies).
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeMatrixAfterSignal(ctx context.Context) {
	sr.resetCancelledLevels(ctx)

	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: resumeMatrixAfterSignal: fetch price: %v", sr.strategy.ID, err)
		return
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	for i := range sr.levels {
		if sr.levels[i].Status == LevelPending {
			if err := sr.placeMatrixLevel(ctx, &sr.levels[i], price); err != nil {
				sr.errlog(ctx, fmt.Sprintf("resumeMatrixAfterSignal L%d: %v", sr.levels[i].LevelIdx, err))
			}
		}
	}
}

// matrixSLTrigger computes the per-level SL trigger price.
// stopPct is expected negative (e.g. -2.0 means 2% adverse from fill).
// matrixSlotLinkStr encodes a slot number for use inside a linkId segment.
// Negative slots use "n{abs}" to avoid a bare "-" that would split the linkId.
// Positive slots (including 0) are formatted as plain digits.
func matrixSlotLinkStr(l *GridLevel) string {
	if l.Slot == nil {
		return fmt.Sprintf("%d", l.LevelIdx) // fallback: should not happen
	}
	s := *l.Slot
	if s < 0 {
		return fmt.Sprintf("n%d", -s)
	}
	return fmt.Sprintf("%d", s)
}

func matrixSLTrigger(dir Direction, fillPrice, stopPct float64) float64 {
	if dir == DirectionLong {
		return fillPrice * (1 + stopPct/100) // stopPct < 0 → trigger below fill
	}
	return fillPrice * (1 - stopPct/100) // stopPct < 0 → trigger above fill
}

// handleMatrixLevelFill is the matrix-specific handler called when a level fill event arrives
// for a strategy_type="matrix" strategy.
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
			sr.info(ctx, fmt.Sprintf("Matrix L%d (slot %s) %s @ %.4f исполнен",
				sr.levels[i].LevelIdx, slotStr(sr.levels[i].Slot), sr.levels[i].Side, filledPrice))
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

	// Re-run price check so that any pending virtual levels whose target was already
	// crossed (but whose tick was dropped during fill processing) are triggered now.
	if sr.lastMatrixPrice > 0 {
		sr.matrixPriceTick(ctx, sr.lastMatrixPrice)
	}
}

// matrixPlacePerLevelSL places a reduce-only stop-market SL for one filled level.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixPlacePerLevelSL(ctx context.Context, l *GridLevel, fillPrice, stopPct float64) {
	if sr.strategy.MaxStopActive > 0 {
		active := 0
		for _, lv := range sr.levels {
			if lv.SLOrderID != "" {
				active++
			}
		}
		if active >= sr.strategy.MaxStopActive {
			sr.warn(ctx, fmt.Sprintf("Matrix per-level SL для L%d: лимит %d условных стопов на бирже достигнут — отложено",
				l.LevelIdx, sr.strategy.MaxStopActive))
			return
		}
	}
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
		sr.strategy.ID[:8], matrixSlotLinkStr(l), sr.matrixSLSeq)

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
		sr.errlog(ctx, fmt.Sprintf("Matrix per-level SL для L%d: trigger=%.4f fillRef=%.4f qty=%s err=%v",
			l.LevelIdx, trigger, fillPrice, fmtQty, err))
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
	sr.warn(ctx, fmt.Sprintf("Matrix SL сработал L%d (slot %s) @ %.4f",
		closed.LevelIdx, slotStr(closed.Slot), slTrigger))

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
		l.SLPrice = 0
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET sl_order_id=NULL, sl_price=NULL WHERE id=$1`, l.ID)
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
