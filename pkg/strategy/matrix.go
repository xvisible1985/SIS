package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
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

func slotLabel(s *int) string {
	if s == nil {
		return "L(?)"
	}
	return fmt.Sprintf("L(%d)", *s)
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

// matrixLatestActiveFill returns the "most extreme" DCA level among all LevelFilled entries.
// For SHORT it is the level with the HIGHEST fill price (furthest against the short position).
// For LONG  it is the level with the LOWEST  fill price (furthest against the long position).
//
// This is the level whose TP% and fill price govern the global TP placement.
//
// Why not slice order (old implementation):
//   matrixReplaceSlots appends re-inserted levels to the END of sr.levels.  When a slot
//   re-fills after a SafeZone cycle it becomes the last element of the slice, making the
//   old LIFO scan return it as "latest" — even though another slot (e.g. L(-1)) is still
//   filled at a more extreme price and has the TP% configured.  If the re-inserted slot
//   has no TP% the old code silently cancels the standing TP.
//
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixLatestActiveFill() (*GridLevel, bool) {
	var best *GridLevel
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.FilledPrice <= 0 {
			continue
		}
		if best == nil {
			best = l
			continue
		}
		if sr.strategy.Direction == DirectionShort {
			if l.FilledPrice > best.FilledPrice {
				best = l // higher price = more against a short = more extreme DCA
			}
		} else {
			if l.FilledPrice < best.FilledPrice {
				best = l // lower price = more against a long = more extreme DCA
			}
		}
	}
	return best, best != nil
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

	sr.matrixWaitingSlots = make(map[int]float64) // defensive reset for fresh cycle

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
		sizeUSDT := sizePctForSlot(slot) / 100 * sr.effectiveDeposit(price)
		// Guard: skip slots with zero quantity (prevents zero-qty orders to exchange)
		if sizeUSDT == 0 {
			levelIdx++
			continue
		}
		priceForQty := targetPrice
		if priceForQty == 0 {
			priceForQty = price // market entry: use current price for qty calc
		}
		rawQty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
		// Bump up if needed so qty*price meets exchange minimum notional value.
		if sr.instr.MinNotionalValue > 0 {
			if qf, _ := strconv.ParseFloat(rawQty, 64); qf > 0 {
				bumped := trader.EnsureMinNotional(qf, sr.instr.QtyStep, priceForQty, sr.instr.MinNotionalValue)
				if bumped != qf {
					rawQty = trader.FormatQty(bumped, sr.instr.QtyStep, sr.instr.MinQty)
				}
			}
		}
		qty := rawQty

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
		sr.info(ctx, fmt.Sprintf("  %s %.4f qty=%s size=%.2f USDT%s",
			slotLabel(l.Slot), l.TargetPrice, l.Qty, l.SizeUSDT, virt))
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
			} else if sr.strategy.AdoptPositionData != nil {
				// Adopt mode: an existing exchange position is absorbed — no market order placed.
				adopt := sr.strategy.AdoptPositionData
				fillPrice, _ := strconv.ParseFloat(adopt.EntryPrice, 64)
				if fillPrice <= 0 {
					fillPrice = price // fallback to current mark price
				}
				// Clear adopt flag in DB before calling handleMatrixLevelFill (prevents re-use on reload).
				if _, err := sr.runner.pool.Exec(ctx,
					`UPDATE strategies SET adopt_position_data=NULL, updated_at=NOW() WHERE id=$1`,
					sr.strategy.ID); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Matrix L(0) adopt: не удалось сбросить adopt_position_data: %v", err))
				}
				sr.strategy.AdoptPositionData = nil
				sr.info(ctx, fmt.Sprintf("Matrix L(0): поглощение существующей позиции @ %.4f (qty=%s, adopt mode)",
					fillPrice, adopt.Size))
				sr.handleMatrixLevelFill(ctx, l.ID, fillPrice)
			} else {
				if err := sr.placeMatrixLevel(ctx, l, price); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix entry %s: %v", slotLabel(l.Slot), err))
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
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления matrix L(%d): %v", slot, err))
		}
	}

	// Seed lastMatrixPrice so handleMatrixLevelFill → matrixPriceTick fires
	// virtual levels immediately instead of waiting for the first price tick.
	sr.lastMatrixPrice = price

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
			sr.warn(ctx, fmt.Sprintf("Matrix %s: недостаточно баланса — переведён в виртуальный режим", slotLabel(l.Slot)))
			return nil
		}
		if isMinOrderValue(err) {
			// Stored qty is below exchange minimum notional (e.g. < 5 USDT) — likely due to
			// rounding at cycle creation before MinNotionalValue was checked.
			// Try bumping qty by one step and retrying once; if still too small, go virtual.
			priceForBump := l.TargetPrice
			if priceForBump <= 0 {
				priceForBump, _ = trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
			}
			qtyF, _ := strconv.ParseFloat(l.Qty, 64)
			bumped := trader.EnsureMinNotional(qtyF, sr.instr.QtyStep, priceForBump, sr.instr.MinNotionalValue)
			if bumped > qtyF && sr.instr.QtyStep > 0 {
				newQtyStr := trader.FormatQty(bumped, sr.instr.QtyStep, sr.instr.MinQty)
				sr.warn(ctx, fmt.Sprintf("Matrix %s: qty %s → %s (ниже минимального объёма, корректируем)", slotLabel(l.Slot), l.Qty, newQtyStr))
				l.Qty = newQtyStr
				req.Qty = newQtyStr
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET qty=$1 WHERE id=$2`, newQtyStr, l.ID)
				sr.runner.RegisterOrder(linkID, ref)
				result, err = sr.runner.tradeStream.PlaceOrder(ctx, req)
				if err == nil {
					// Retry succeeded — fall through to the success path below.
					goto placed
				}
				sr.runner.UnregisterOrder(linkID)
			}
			// Still failing or bump not possible — mark virtual.
			l.ForceVirtual = true
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET force_virtual=true WHERE id=$1`, l.ID)
			sr.warn(ctx, fmt.Sprintf("Matrix %s: размер ордера ниже минимума биржи — переведён в виртуальный режим", slotLabel(l.Slot)))
			return nil
		}
		return err
	}
placed:

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
		sr.info(ctx, fmt.Sprintf("Matrix %s %s MARKET (%.0f USDT)", slotLabel(l.Slot), l.Side, l.SizeUSDT))
	} else {
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		sr.info(ctx, fmt.Sprintf("Matrix %s %s @ %.4f (%.0f USDT)", slotLabel(l.Slot), l.Side, l.TargetPrice, qty*l.TargetPrice))
	}
	return nil
}

// loadMatrixCycle loads an existing cycle when startMatrixCycle detects
// active levels already in DB (the guard path). It calls loadActiveCycle
// to read levels, restores waiting-slot state, and re-launches the price monitor.
// Service-restart resumption goes through loadOrStart → directly calls
// restoreMatrixWaitingSlots + launchMatrixPriceMonitor.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) loadMatrixCycle(ctx context.Context) error {
	if err := sr.loadActiveCycle(ctx); err != nil {
		return err
	}
	sr.mu.Lock()
	sr.restoreMatrixWaitingSlots()
	sr.mu.Unlock()
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

	// 1. Check waiting slots for re-entry
	if len(sr.matrixWaitingSlots) > 0 {
		sr.matrixCheckWaitingReentry(ctx, currentPrice)
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
			// ProtectedBuild: don't trigger next virtual level until previous slot has stop confirmed
			if sr.strategy.ProtectedBuild && l.Slot != nil && *l.Slot > 0 {
				prevSlot := *l.Slot - 1
				if prevSlot > 0 && !sr.matrixSlotCovered(prevSlot) {
					continue // previous slot not yet covered by a stop
				}
			}
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
			sr.errlog(ctx, fmt.Sprintf("Matrix stop-cond SL replace %s: %v", slotLabel(l.Slot), err))
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
		sr.info(ctx, fmt.Sprintf("Matrix SL %s переставлен → %.4f (stop cond сработал)", slotLabel(l.Slot), newTrigger))
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
		if *l.Slot < 0 {
			continue // negative slots don't use per-level SL
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
		sr.errlog(ctx, fmt.Sprintf("Matrix virtual %s: %v", slotLabel(l.Slot), err))
		return
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.info(ctx, fmt.Sprintf("Matrix virtual %s запущен @ market", slotLabel(l.Slot)))
}

// matrixReplaceSlots re-places levels for any slot that has no active (pending/placed/filled) row.
// Called after safe zone clears. Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReplaceSlots(ctx context.Context, currentPrice float64) {
	sr.info(ctx, fmt.Sprintf("[SZ RE-ENTRY] matrixReplaceSlots: price=%.4f startPrice=%.4f — пересоздаём все слоты",
		currentPrice, sr.cycle.StartPrice))

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

	// Use a monotonically increasing counter starting from max(existing levelIdx)+1.
	// Do NOT recompute from len(sr.levels) inside the loop — each delete+insert keeps
	// len stable, so naive recompute gives the same index for every replaced slot,
	// causing "110072: OrderLinkedID duplicate" errors on the exchange.
	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}
	nextLevelIdx++ // first free index

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
			sizeUSDT = sr.strategy.MatrixEntryLevel.SizePct / 100 * sr.effectiveDeposit(currentPrice)
		} else if slot > 0 {
			idx := slot - 1
			if idx >= len(above) {
				continue
			}
			sizeUSDT = above[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
		} else {
			idx := -slot - 1
			if idx >= len(below) {
				continue
			}
			sizeUSDT = below[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
		}
		priceForQty := targetPrice
		if priceForQty == 0 {
			priceForQty = currentPrice
		}
		rawQty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
		// Bump up if needed so qty*price meets exchange minimum notional value.
		if sr.instr.MinNotionalValue > 0 {
			if qf, _ := strconv.ParseFloat(rawQty, 64); qf > 0 {
				bumped := trader.EnsureMinNotional(qf, sr.instr.QtyStep, priceForQty, sr.instr.MinNotionalValue)
				if bumped != qf {
					rawQty = trader.FormatQty(bumped, sr.instr.QtyStep, sr.instr.MinQty)
				}
			}
		}
		qty := rawQty

		// Leave any sl_closed rows for this slot in place — they serve as a historical
		// record and are needed by the frontend to resolve level_idx → slot for old fills.
		// The activeSlots check above already skips sl_closed rows, so a new pending row
		// will be inserted alongside the old sl_closed one without conflict.

		levelIdx := nextLevelIdx
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
			orderType := "limit"
			if placed.ExchangeOrderID == "" {
				orderType = "virtual"
			}
			sr.info(ctx, fmt.Sprintf("[SZ RE-ENTRY] L%d: %s %s qty=%s @ %.4f (цена_рынка=%.4f)",
				slot, side, orderType, qty, targetPrice, currentPrice))
		}
		nextLevelIdx++
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
			sr.info(ctx, fmt.Sprintf("Matrix %s %s @ %.4f исполнен",
				slotLabel(sr.levels[i].Slot), sr.levels[i].Side, filledPrice))
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
	// Negative slots (L-1, L-2…) are averaging positions for managed hedge — no per-level SL.
	if l.Slot != nil && *l.Slot < 0 {
		return
	}
	// SL suppressed by active hedge — do not place/re-place per-level SL.
	if sr.strategy.HedgeSlSuppressed {
		return
	}
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
		sr.errlog(ctx, fmt.Sprintf("Matrix per-level SL для %s: trigger=%.4f fillRef=%.4f qty=%s err=%v",
			slotLabel(l.Slot), trigger, fillPrice, fmtQty, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLPrice = trigger
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_price=$2 WHERE id=$3`,
		result.OrderId, trigger, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("Matrix SL для %s @ %.4f выставлен", slotLabel(l.Slot), trigger))
}

// matrixUpdateTP cancels the existing global TP and places a new one based on the
// latest active filled level's fill_price and that level's tp_pct config.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixUpdateTP(ctx context.Context) {
	if sr.instr.QtyStep == 0 {
		return
	}
	// TP suppressed by active hedge — do not place/re-place TP.
	if sr.strategy.HedgeTpSuppressed {
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
		// TP percentage removed from config — cancel any standing TP order.
		if sr.tpOrderID != "" {
			old := sr.tpOrderID
			sr.runner.UnregisterOrder(old)
			sr.tpOrderID = ""
			if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
				Symbol:   sr.strategy.Symbol,
				Category: sr.strategy.Category,
				OrderId:  old,
			}); err != nil && !isOrderGone(err) {
				sr.warn(ctx, fmt.Sprintf("matrixUpdateTP: cancel TP (config removed): %v", err))
			}
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)
		}
		return
	}

	// TP считается от средневзвешенной цены входа (ТВХ), чтобы гарантировать
	// закрытие в плюс вне зависимости от глубины DCA.
	// Используем ТВХ с биржи (pos.EntryPrice), а не расчётный avgEntry(),
	// чтобы корректно учесть проскальзывание маркет-ордеров.
	avgEntryPrice, _ := sr.avgEntry()
	if avgEntryPrice == 0 {
		return
	}
	// Prefer the WS-cached exchange avg entry over the internal avgEntry() calculation.
	{
		wantIdx := positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction)
		if wsAvg := sr.runner.GetPositionAvgEntry(sr.strategy.Symbol, wantIdx); wsAvg > 0 {
			sr.info(ctx, fmt.Sprintf("matrixUpdateTP: ТВХ биржи %.4f (расчётная %.4f)", wsAvg, avgEntryPrice))
			avgEntryPrice = wsAvg
		}
	}
	var tpPrice float64
	var tpSide string
	if sr.strategy.Direction == DirectionLong {
		tpSide = "Sell"
		tpPrice = avgEntryPrice * (1 + *tpPctVal/100)
	} else {
		tpSide = "Buy"
		tpPrice = avgEntryPrice * (1 - *tpPctVal/100)
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
	// Embed the initiating slot into the linkId so the frontend can display
	// "TP L(N)" in the execution marker. Uses same encoding as matrixSlotLinkStr:
	// positive slots → plain digits, negative slots → "n{abs}".
	slotSuffix := ""
	if latest.Slot != nil {
		s := *latest.Slot
		var enc string
		if s < 0 {
			enc = fmt.Sprintf("n%d", -s)
		} else {
			enc = fmt.Sprintf("%d", s)
		}
		slotSuffix = "l" + enc // e.g. "l2" or "ln1" → link looks like "…-tpl2-…"
	}
	linkID := fmt.Sprintf("SIS_STR-%s-tp%s-%d-%d", sr.strategy.ID[:8], slotSuffix, sr.cycle.CycleNum, sr.tpPlaceSeq)
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
	sr.info(ctx, fmt.Sprintf("Matrix TP @ %.4f qty=%s выставлен (ТВХ=%.4f +%.2f%%)", tpPrice, tpQty, avgEntryPrice, *tpPctVal))
}

// applyNewMatrixPrices recalculates target_price, size_usdt and qty for every pending
// matrix level in sr.levels using the current strategy settings.
// basePrice is the reference price for step calculations (use cycle.StartPrice when a
// position is open, or the live mark price when no position exists).
// currentPrice is the live mark price, used as qty reference when targetPrice == 0 (market entry).
// Must be called with sr.mu held.
func (sr *StrategyRunner) applyNewMatrixPrices(ctx context.Context, basePrice, currentPrice float64) {
	if basePrice <= 0 {
		basePrice = currentPrice
	}
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	newPrices := calculateMatrixPrices(basePrice, above, below, sr.strategy.Direction)

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

	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || l.Slot == nil {
			continue
		}
		slot := *l.Slot
		newTarget, ok := newPrices[slot]
		if !ok {
			// Slot was removed from config — cancel any outstanding exchange order
			// and mark the level cancelled so it won't be re-placed.
			if l.ExchangeOrderID != "" {
				sr.runner.UnregisterOrder(l.ExchangeOrderID)
				sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
					Symbol:   sr.strategy.Symbol,
					Category: sr.strategy.Category,
					OrderId:  l.ExchangeOrderID,
				})
				l.ExchangeOrderID = ""
			}
			l.Status = LevelCancelled
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='cancelled', exchange_order_id=NULL WHERE id=$1`, l.ID)
			continue
		}
		sizeUSDT := sizePctForSlot(slot) / 100 * sr.effectiveDeposit(currentPrice)
		if sizeUSDT == 0 {
			continue
		}
		priceForQty := newTarget
		if priceForQty == 0 {
			priceForQty = currentPrice
		}
		newQty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)

		l.TargetPrice = newTarget
		l.SizeUSDT = sizeUSDT
		l.Qty = newQty
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET target_price=$1, size_usdt=$2, qty=$3 WHERE id=$4`,
			newTarget, sizeUSDT, newQty, l.ID)
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
	sr.warn(ctx, fmt.Sprintf("Matrix SL сработал %s @ %.4f",
		slotLabel(closed.Slot), slTrigger))

	// Mark slot as waiting for re-entry.
	// RebuildFromEntry: L(0) SL also triggers waiting — it re-enters at market after SZ clears.
	if closed.Slot != nil {
		slot := *closed.Slot
		if slot == 0 && sr.strategy.RebuildFromEntry {
			// L(0) SL fired — anchor point lost. Cancel all pending/placed positive slots
			// (DCA levels); they will be rebuilt anchored to the new L(0) fill price once
			// price exits the Safe Zone and L(0) re-enters at market.
			sr.matrixLastSLSlot = 0
			for i := range sr.levels {
				l := &sr.levels[i]
				if l.Slot == nil || *l.Slot <= 0 {
					continue
				}
				if l.Status != LevelPending && l.Status != LevelPlaced {
					continue
				}
				if l.ExchangeOrderID != "" {
					cancelErr := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
						Symbol:   sr.strategy.Symbol,
						Category: sr.strategy.Category,
						OrderId:  l.ExchangeOrderID,
					})
					if cancelErr != nil && isOrderGone(cancelErr) {
						// Regular cancel found nothing — retry as conditional (StopMarket) order.
						sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{ //nolint:errcheck
							Symbol:      sr.strategy.Symbol,
							Category:    sr.strategy.Category,
							OrderId:     l.ExchangeOrderID,
							OrderFilter: "StopOrder",
						})
					}
					sr.runner.UnregisterOrder(l.ExchangeOrderID)
				}
				l.Status = LevelCancelled
				l.ExchangeOrderID = ""
				l.ExchangeLinkID = ""
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='cancelled', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
			}
			if sr.matrixWaitingSlots == nil {
				sr.matrixWaitingSlots = make(map[int]float64)
			}
			sr.matrixWaitingSlots[0] = slTrigger
			sr.info(ctx, fmt.Sprintf("Matrix L(0) SL @ %.4f — ожидаем SZ для перезахода в точку входа", slTrigger))
		} else if slot > 0 {
			sr.matrixLastSLSlot = slot // track most-recently-stopped slot for SZ display
			if sr.strategy.RebuildOnSL {
				// New behaviour: immediately rebuild the grid from the SZ lower boundary.
				sr.matrixRebuildFromSZLow(ctx, slot, slTrigger)
			} else {
				// Legacy behaviour: add to waiting map, re-enter when price exits SZ.
				if sr.matrixWaitingSlots == nil {
					sr.matrixWaitingSlots = make(map[int]float64)
				}
				sr.matrixWaitingSlots[slot] = slTrigger
				sr.info(ctx, fmt.Sprintf("Matrix L%d ожидает перезахода (SL @ %.4f)", slot, slTrigger))
			}
		}
	}

	// Recalculate global TP (will cancel it if no filled levels remain)
	sr.matrixUpdateTP(ctx)
	// Per-level SL closes only part of the position — the cycle continues.
	// When the full position goes to zero on the exchange, handlePositionClose
	// will detect it (closedBySelf=false, hasPosition=false) and return early,
	// leaving the cycle alive so matrixReplaceSlots can re-enter after SafeZone.
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

// handleMatrixTPFill is called when the global matrix TP order fills.
// It cancels all per-level SL orders, resets filled/sl_closed levels back to
// pending, re-anchors the grid at the TP fill price, and re-places all non-virtual
// levels so the matrix can immediately start re-entering.
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleMatrixTPFill(ctx context.Context, fillPrice, fillQty float64) {
	if sr.cycle == nil {
		return
	}

	avg, _ := sr.avgEntry()
	activeQty := sr.matrixActiveQty()
	if fillQty == 0 {
		fillQty = activeQty
	}
	if avg == 0 {
		avg = fillPrice
	}

	pnl := (avg - fillPrice) * fillQty
	if sr.strategy.Direction == DirectionLong {
		pnl = (fillPrice - avg) * fillQty
	}
	pnlPct := 0.0
	if avg > 0 && fillQty > 0 {
		pnlPct = pnl / (avg * fillQty) * 100
	}

	sr.info(ctx, fmt.Sprintf("✅ Matrix TP исполнен @ %.4f | qty=%.4f | PnL +%.2f USDT (+%.2f%%)",
		fillPrice, fillQty, pnl, pnlPct))
	sr.writeTrade(ctx, "tp", avg, fillPrice, fillQty, pnl, pnlPct)

	// 1. Cancel all per-level SL orders — position closed by global TP.
	sr.matrixCancelPerLevelSLs(ctx)

	// 2. Reset all filled/sl_closed levels to pending so the grid can re-enter.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled && l.Status != LevelSLClosed {
			continue
		}
		l.Status = LevelPending
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		l.FilledPrice = 0
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL,
			 filled_price=NULL, filled_at=NULL WHERE id=$1`, l.ID)
	}

	// 3. Clear global TP reference.
	sr.tpOrderID = ""
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)

	// 4. Clear waiting slots — TP close is a fresh entry.
	sr.matrixWaitingSlots = make(map[int]float64)

	// 5. Suppress the position-zero WS event that follows TP fill.
	// Also covers the tail case: handlePartialPositionChange sees closedBySelf=true
	// and treats any remainder as a tail (not a manual close).
	sr.closedBySelf = true
	sr.closedByReason = "Matrix TP"

	// 6. Re-anchor the grid at the TP fill price and recalculate level targets.
	sr.cycle.StartPrice = fillPrice
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET start_price=$1 WHERE id=$2`, fillPrice, sr.cycle.ID)
	sr.applyNewMatrixPrices(ctx, fillPrice, fillPrice)

	// 7. Bump repriceGen to avoid Bybit 110072 (duplicate linkId after cancel).
	sr.repriceGen++

	// 8. Re-place all non-virtual pending levels; trigger virtual L(0) immediately.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || l.Slot == nil {
			continue
		}
		if sr.matrixIsVirtual(l) {
			if *l.Slot == 0 {
				sr.matrixTriggerVirtualLevel(ctx, l)
			}
			// Other virtual levels: price monitor handles via matrixPriceTick.
		} else {
			if err := sr.placeMatrixLevel(ctx, l, fillPrice); err != nil {
				sr.errlog(ctx, fmt.Sprintf("Matrix TP re-place %s: %v", slotLabel(l.Slot), err))
			}
		}
	}

	// Seed lastMatrixPrice so the next level fill immediately triggers virtual levels.
	sr.lastMatrixPrice = fillPrice

	sr.info(ctx, "Matrix TP: сетка сброшена и переставлена")
}

// matrixSlotCovered returns true if the filled level for the given slot has an active stop order.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixSlotCovered(slot int) bool {
	for _, l := range sr.levels {
		if l.Slot != nil && *l.Slot == slot && l.Status == LevelFilled {
			return l.SLOrderID != ""
		}
	}
	return false
}

// matrixFindDeepestActiveRef finds the active (filled/placed/pending) level with the
// NEAREST slot number greater than waitingSlot. Using the nearest neighbour (not the
// highest) ensures that each waiting slot chains to the level directly above it, so
// two waiting slots with the same stepPct don't both land on the same target price.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixFindDeepestActiveRef(waitingSlot int) *GridLevel {
	var best *GridLevel
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot <= waitingSlot {
			continue
		}
		switch l.Status {
		case LevelFilled, LevelPlaced, LevelPending:
		default:
			continue
		}
		if best == nil || *l.Slot < *best.Slot { // nearest (minimum) slot > waitingSlot
			best = l
		}
	}
	return best
}

// matrixReentryConditionMet returns true when current price has crossed past ref's fill price
// in the favorable direction (price < ref.FilledPrice for SHORT, > for LONG).
func (sr *StrategyRunner) matrixReentryConditionMet(ref *GridLevel, currentPrice float64) bool {
	if ref.FilledPrice <= 0 {
		return false
	}
	if sr.strategy.Direction == DirectionShort {
		return currentPrice < ref.FilledPrice
	}
	return currentPrice > ref.FilledPrice
}

// matrixCheckWaitingReentry iterates all waiting slots and re-enters those whose
// re-entry condition is now met. Called from matrixPriceTick.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixCheckWaitingReentry(ctx context.Context, currentPrice float64) {
	// Build slot list. For RebuildFromEntry, process slot 0 first (ascending) so L(0)
	// re-enters before positive slots try to anchor to its fill price.
	// For the default path keep the historical descending order (deepest first).
	slots := make([]int, 0, len(sr.matrixWaitingSlots))
	for s := range sr.matrixWaitingSlots {
		slots = append(slots, s)
	}
	if sr.strategy.RebuildFromEntry {
		sort.Ints(slots) // ascending: slot 0 first, then 1, 2, …
	} else {
		sort.Sort(sort.Reverse(sort.IntSlice(slots))) // descending: deepest first
	}

	for _, slot := range slots {
		// SafeZone: block re-entry until current price has recovered safe_zone_pct
		// above (LONG) or below (SHORT) the SL trigger that closed this slot.
		// This prevents immediate re-entry and repeated SL hits in volatile conditions.
		if sr.strategy.SafeZonePct > 0 {
			if slTrigger := sr.matrixWaitingSlots[slot]; slTrigger > 0 {
				var safePrice float64
				if sr.strategy.Direction == DirectionLong {
					safePrice = slTrigger * (1 + sr.strategy.SafeZonePct/100)
					if currentPrice < safePrice {
						continue
					}
				} else {
					safePrice = slTrigger * (1 - sr.strategy.SafeZonePct/100)
					if currentPrice > safePrice {
						continue
					}
				}
			}
		}

		// RebuildFromEntry: slot 0 re-enters at market; positive slots anchor to L(0).
		if sr.strategy.RebuildFromEntry {
			if slot == 0 {
				sr.matrixReenterL0AtMarket(ctx, currentPrice)
				continue
			}
			// Positive slot: wait for L(0) to fill first, then re-enter at L(0) anchor.
			l0 := sr.matrixFindFilledL0()
			if l0 == nil {
				continue // L(0) not yet filled — will retry on next price tick after L(0) fills
			}
			sr.matrixReenterFromL0(ctx, slot, l0.FilledPrice, currentPrice)
			continue
		}

		// Default path (RebuildFromEntry=false)
		ref := sr.matrixFindDeepestActiveRef(slot)

		if ref == nil {
			// No deeper active level — re-enter at configured price (rebuild from scratch)
			sr.matrixReenterAtConfigPrice(ctx, slot, currentPrice)
			continue
		}

		// Wait for price to pass below (SHORT) or above (LONG) the reference's fill price
		if !sr.matrixReentryConditionMet(ref, currentPrice) {
			continue
		}

		// ProtectedBuild: reference must have a stop confirmed
		if sr.strategy.ProtectedBuild && !sr.matrixSlotCovered(*ref.Slot) {
			continue
		}

		sr.matrixReenterRelativeToRef(ctx, slot, ref, currentPrice)
	}
}

// matrixReenterRelativeToRef places a new pending level for waitingSlot at a price
// one step below (SHORT) or above (LONG) the reference level's fill price.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterRelativeToRef(ctx context.Context, waitingSlot int, ref *GridLevel, currentPrice float64) {
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")

	var stepPct, sizeUSDT float64
	if waitingSlot > 0 {
		idx := waitingSlot - 1
		if idx >= len(above) {
			return
		}
		stepPct = above[idx].PriceStepPct
		sizeUSDT = above[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	} else {
		idx := -waitingSlot - 1
		if idx >= len(below) {
			return
		}
		stepPct = below[idx].PriceStepPct
		sizeUSDT = below[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	}

	if stepPct == 0 || sizeUSDT == 0 {
		return
	}

	// stepMul inverts direction for SHORT (positive slots land below entry for SHORT)
	stepMul := 1.0
	if sr.strategy.Direction == DirectionShort {
		stepMul = -1.0
	}
	newTargetPrice := ref.FilledPrice * (1 + stepMul*math.Abs(stepPct)/100)
	if newTargetPrice <= 0 {
		return
	}

	qty := trader.FormatQty(sizeUSDT/newTargetPrice, sr.instr.QtyStep, sr.instr.MinQty)
	side := matrixLevelSide(sr.strategy.Direction)

	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}
	nextLevelIdx++

	slotCopy := waitingSlot
	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, newTargetPrice, sizeUSDT, qty, slotCopy,
	).Scan(&levelID); err != nil {
		log.Printf("strategy %s: matrixReenterRelativeToRef slot=%d: %v", sr.strategy.ID, waitingSlot, err)
		return
	}

	newLevel := GridLevel{
		ID: levelID, LevelIdx: nextLevelIdx, Side: side,
		TargetPrice: newTargetPrice, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &slotCopy,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]

	delete(sr.matrixWaitingSlots, waitingSlot)

	if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Matrix re-entry L%d relative to ref L%d: %v", waitingSlot, *ref.Slot, err))
	} else {
		sr.info(ctx, fmt.Sprintf("[RE-ENTRY] L%d @ %.4f (ref L%d @ %.4f)", waitingSlot, newTargetPrice, *ref.Slot, ref.FilledPrice))
	}
}

// matrixReenterAtConfigPrice re-enters a waiting slot at its original configured price
// (relative to cycle.StartPrice). Used when no deeper active reference exists.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterAtConfigPrice(ctx context.Context, waitingSlot int, currentPrice float64) {
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(sr.cycle.StartPrice, above, below, sr.strategy.Direction)

	targetPrice, ok := prices[waitingSlot]
	if !ok {
		return
	}

	var sizeUSDT float64
	if waitingSlot > 0 {
		idx := waitingSlot - 1
		if idx >= len(above) {
			return
		}
		sizeUSDT = above[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	} else {
		idx := -waitingSlot - 1
		if idx >= len(below) {
			return
		}
		sizeUSDT = below[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	}

	if sizeUSDT == 0 || targetPrice == 0 {
		return
	}

	qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
	side := matrixLevelSide(sr.strategy.Direction)

	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}
	nextLevelIdx++

	slotCopy := waitingSlot
	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, targetPrice, sizeUSDT, qty, slotCopy,
	).Scan(&levelID); err != nil {
		log.Printf("strategy %s: matrixReenterAtConfigPrice slot=%d: %v", sr.strategy.ID, waitingSlot, err)
		return
	}

	newLevel := GridLevel{
		ID: levelID, LevelIdx: nextLevelIdx, Side: side,
		TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &slotCopy,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]

	delete(sr.matrixWaitingSlots, waitingSlot)

	if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Matrix re-entry L%d at config price: %v", waitingSlot, err))
	} else {
		sr.info(ctx, fmt.Sprintf("[RE-ENTRY] L%d @ %.4f (config price, no deeper ref)", waitingSlot, targetPrice))
	}
}

// matrixFindFilledL0 returns the most recently inserted filled L(0) level, or nil if none.
// "Most recently inserted" = highest LevelIdx among all filled slot-0 rows, since new
// re-entry rows are appended with monotonically increasing LevelIdx.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixFindFilledL0() *GridLevel {
	var best *GridLevel
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot != 0 || l.Status != LevelFilled {
			continue
		}
		if best == nil || l.LevelIdx > best.LevelIdx {
			best = l
		}
	}
	return best
}

// matrixReenterL0AtMarket re-enters the L(0) slot at market after the Safe Zone clears.
// Used by RebuildFromEntry mode. Inserts a new strategy_levels row, places a market order,
// and removes slot 0 from matrixWaitingSlots.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterL0AtMarket(ctx context.Context, currentPrice float64) {
	var sizeUSDT float64
	if sr.strategy.MatrixEntryLevel != nil {
		sizeUSDT = sr.strategy.MatrixEntryLevel.SizePct / 100 * sr.effectiveDeposit(currentPrice)
	}
	if sizeUSDT == 0 {
		sr.warn(ctx, "matrixReenterL0AtMarket: size_usdt=0, пропускаем")
		return
	}
	priceForQty := currentPrice
	if priceForQty <= 0 {
		return
	}
	qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "" || qty == "0" {
		sr.warn(ctx, "matrixReenterL0AtMarket: qty=0, пропускаем")
		return
	}

	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}
	nextLevelIdx++

	side := matrixLevelSide(sr.strategy.Direction)
	slotZero := 0
	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, 0.0, sizeUSDT, qty, slotZero,
	).Scan(&levelID); err != nil {
		log.Printf("strategy %s: matrixReenterL0AtMarket insert: %v", sr.strategy.ID, err)
		return
	}

	newLevel := GridLevel{
		ID:       levelID,
		LevelIdx: nextLevelIdx,
		Side:     side,
		SizeUSDT: sizeUSDT,
		Qty:      qty,
		Status:   LevelPending,
		Slot:     &slotZero,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]

	// Remove from waiting before placement so a concurrent price tick doesn't double-trigger.
	delete(sr.matrixWaitingSlots, 0)

	sr.matrixTriggerVirtualLevel(ctx, placed)
	sr.info(ctx, fmt.Sprintf("[REBUILD-FROM-ENTRY] L(0) перезаход @ market (SZ пройдена)"))
}

// matrixReenterFromL0 re-enters a waiting slot anchored to L(0)'s fill price.
// targetPrice = calculateMatrixPrices(l0FillPrice, ...)[slot].
// Used by RebuildFromEntry mode once L(0) is filled.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixReenterFromL0(ctx context.Context, waitingSlot int, l0FillPrice, currentPrice float64) {
	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")
	below := filterMatrixLevels(sr.strategy.MatrixLevels, "below")
	prices := calculateMatrixPrices(l0FillPrice, above, below, sr.strategy.Direction)

	targetPrice, ok := prices[waitingSlot]
	if !ok || targetPrice <= 0 {
		return
	}

	var sizeUSDT float64
	if waitingSlot > 0 {
		idx := waitingSlot - 1
		if idx >= len(above) {
			return
		}
		sizeUSDT = above[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	} else {
		idx := -waitingSlot - 1
		if idx >= len(below) {
			return
		}
		sizeUSDT = below[idx].SizePct / 100 * sr.effectiveDeposit(currentPrice)
	}
	if sizeUSDT == 0 {
		return
	}

	qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "" || qty == "0" {
		return
	}
	side := matrixLevelSide(sr.strategy.Direction)

	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}
	nextLevelIdx++

	slotCopy := waitingSlot
	var levelID string
	if err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, targetPrice, sizeUSDT, qty, slotCopy,
	).Scan(&levelID); err != nil {
		log.Printf("strategy %s: matrixReenterFromL0 slot=%d: %v", sr.strategy.ID, waitingSlot, err)
		return
	}

	newLevel := GridLevel{
		ID: levelID, LevelIdx: nextLevelIdx, Side: side,
		TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
		Status: LevelPending, Slot: &slotCopy,
	}
	sr.levels = append(sr.levels, newLevel)
	placed := &sr.levels[len(sr.levels)-1]

	delete(sr.matrixWaitingSlots, waitingSlot)

	if sr.matrixIsVirtual(placed) {
		// Virtual level: price monitor will trigger when price crosses target.
		sr.info(ctx, fmt.Sprintf("[REBUILD-FROM-ENTRY] L(%d) @ %.4f virtual (L(0)=%.4f)", waitingSlot, targetPrice, l0FillPrice))
	} else if err := sr.placeMatrixLevel(ctx, placed, currentPrice); err != nil {
		sr.errlog(ctx, fmt.Sprintf("[REBUILD-FROM-ENTRY] L(%d) @ %.4f: %v", waitingSlot, targetPrice, err))
	} else {
		sr.info(ctx, fmt.Sprintf("[REBUILD-FROM-ENTRY] L(%d) @ %.4f (L(0)=%.4f)", waitingSlot, targetPrice, l0FillPrice))
	}
}

// matrixRebuildFromSZBoundary immediately rebuilds all unfilled grid levels at or below
// stoppedSlot, anchoring the new grid at the SafeZone boundary nearest to safe re-entry:
//   - SHORT: lower boundary = slTrigger × (1 − safe_zone_pct%)
//   - LONG:  upper boundary = slTrigger × (1 + safe_zone_pct%)
// If safe_zone_pct == 0, anchor = slTrigger itself.
//
// Algorithm:
//  1. Compute anchor from SZ boundary.
//  2. Cancel all PENDING/PLACED levels with slot ≥ stoppedSlot on the exchange and in DB.
//  3. Place new level for stoppedSlot @ anchor, then cascade each deeper slot one
//     price_step_pct below (SHORT) / above (LONG) the previous placed price.
//  4. FILLED levels are skipped — their position is preserved.
//
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixRebuildFromSZLow(ctx context.Context, stoppedSlot int, slTrigger float64) {
	// 1. Compute anchor
	var anchor float64
	if sr.strategy.SafeZonePct > 0 {
		if sr.strategy.Direction == DirectionShort {
			anchor = slTrigger * (1 - sr.strategy.SafeZonePct/100)
		} else {
			anchor = slTrigger * (1 + sr.strategy.SafeZonePct/100)
		}
	} else {
		anchor = slTrigger
	}

	above := filterMatrixLevels(sr.strategy.MatrixLevels, "above")

	// 2. Determine which slots to rebuild.
	//    • stoppedSlot itself
	//    • deeper slots (slot > stoppedSlot) that are pending/placed
	//    • shallower slots (slot < stoppedSlot) that are sl_closed with no active level
	//      — these were stopped in a prior SL event and must be rebuilt together with the
	//      current one so the full sub-grid is anchored at the SZ boundary.
	type slotCfg struct {
		stepPct float64
		sizePct float64
	}
	rebuildCfg := make(map[int]slotCfg) // slot → config

	// Helper: which slots currently have an active level (pending/placed/filled)?
	activeSlotSet := map[int]bool{}
	for _, l := range sr.levels {
		if l.Slot == nil || *l.Slot <= 0 {
			continue
		}
		if l.Status == LevelPending || l.Status == LevelPlaced || l.Status == LevelFilled {
			activeSlotSet[*l.Slot] = true
		}
	}

	// Config for the stopped slot.
	if idx := stoppedSlot - 1; idx >= 0 && idx < len(above) {
		rebuildCfg[stoppedSlot] = slotCfg{
			stepPct: above[idx].PriceStepPct,
			sizePct: above[idx].SizePct,
		}
	}

	// Previously stopped slots (sl_closed, no active level) shallower than stoppedSlot.
	// These must be included so the whole stopped sub-grid is rebuilt together.
	for _, l := range sr.levels {
		if l.Slot == nil || *l.Slot <= 0 || *l.Slot >= stoppedSlot {
			continue
		}
		if l.Status != LevelSLClosed {
			continue
		}
		if activeSlotSet[*l.Slot] {
			continue // a fresh pending level already exists for this slot
		}
		idx := *l.Slot - 1
		if idx >= 0 && idx < len(above) {
			rebuildCfg[*l.Slot] = slotCfg{
				stepPct: above[idx].PriceStepPct,
				sizePct: above[idx].SizePct,
			}
		}
	}

	// Cancel deeper pending/placed levels and collect their configs.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot <= stoppedSlot || *l.Slot <= 0 {
			continue
		}
		if l.Status != LevelPending && l.Status != LevelPlaced {
			continue
		}
		idx := *l.Slot - 1
		if idx >= 0 && idx < len(above) {
			rebuildCfg[*l.Slot] = slotCfg{
				stepPct: above[idx].PriceStepPct,
				sizePct: above[idx].SizePct,
			}
		}
		// Cancel on exchange (ignore "order not found" — may have raced with a fill).
		// DCA levels may be regular Limit orders OR conditional StopMarket orders,
		// depending on whether the target was above or below the mark price at placement
		// time. For SHORT strategies, targets below entry become StopMarket orders and
		// require OrderFilter="StopOrder" to cancel. Try the plain cancel first; on
		// "order not found" retry as a conditional order so neither type is silently skipped.
		if l.ExchangeOrderID != "" {
			cancelErr := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
				Symbol:   sr.strategy.Symbol,
				Category: sr.strategy.Category,
				OrderId:  l.ExchangeOrderID,
			})
			if cancelErr != nil && isOrderGone(cancelErr) {
				// Regular cancel found nothing — retry as a conditional (StopMarket) order.
				if err2 := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
					Symbol:      sr.strategy.Symbol,
					Category:    sr.strategy.Category,
					OrderId:     l.ExchangeOrderID,
					OrderFilter: "StopOrder",
				}); err2 != nil && !isOrderGone(err2) {
					sr.warn(ctx, fmt.Sprintf("matrixRebuildFromSZLow: cancel L%d (stop): %v", *l.Slot, err2))
				}
			} else if cancelErr != nil {
				sr.warn(ctx, fmt.Sprintf("matrixRebuildFromSZLow: cancel L%d: %v", *l.Slot, cancelErr))
			}
		}
		// Mark cancelled in memory and DB (do NOT unregister — WS fill may still arrive).
		l.Status = LevelCancelled
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='cancelled', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
	}

	if len(rebuildCfg) == 0 {
		sr.warn(ctx, fmt.Sprintf("matrixRebuildFromSZLow: нет конфига для L%d, пропускаем", stoppedSlot))
		return
	}

	// 3. Sort slots ascending (stoppedSlot first, then deeper).
	slots := make([]int, 0, len(rebuildCfg))
	for s := range rebuildCfg {
		slots = append(slots, s)
	}
	sort.Ints(slots)

	stepMul := 1.0
	if sr.strategy.Direction == DirectionShort {
		stepMul = -1.0
	}

	currentPrice := sr.lastMatrixPrice
	side := matrixLevelSide(sr.strategy.Direction)

	nextLevelIdx := 0
	for _, l := range sr.levels {
		if l.LevelIdx > nextLevelIdx {
			nextLevelIdx = l.LevelIdx
		}
	}

	prevPrice := anchor
	placed := 0
	firstPlaced := true // tracks whether this is the first non-skipped slot (anchor)

	for _, slot := range slots {
		cfg := rebuildCfg[slot]

		// Skip slots that already have a FILLED level — position is real, don't touch.
		filledExists := false
		for _, l := range sr.levels {
			if l.Slot != nil && *l.Slot == slot && l.Status == LevelFilled {
				filledExists = true
				break
			}
		}
		if filledExists {
			continue
		}

		// The first non-skipped slot (smallest slot number in sorted list) is placed at
		// the anchor. Subsequent slots cascade from there. This ensures that when multiple
		// previously-stopped slots are rebuilt together, L1 (smallest) always lands at the
		// SZ boundary regardless of which slot triggered the current rebuild.
		var targetPrice float64
		if firstPlaced {
			targetPrice = anchor
			firstPlaced = false
		} else {
			targetPrice = prevPrice * (1 + stepMul*math.Abs(cfg.stepPct)/100)
		}
		if targetPrice <= 0 {
			continue
		}

		sizeUSDT := cfg.sizePct / 100 * sr.effectiveDeposit(currentPrice)
		qty := trader.FormatQty(sizeUSDT/targetPrice, sr.instr.QtyStep, sr.instr.MinQty)
		if qty == "" || qty == "0" {
			sr.warn(ctx, fmt.Sprintf("matrixRebuildFromSZLow: L%d qty=0 пропускаем", slot))
			continue
		}

		nextLevelIdx++
		slotCopy := slot
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, slot)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			sr.strategy.ID, sr.cycle.ID, nextLevelIdx, side, targetPrice, sizeUSDT, qty, slotCopy,
		).Scan(&levelID); err != nil {
			sr.errlog(ctx, fmt.Sprintf("matrixRebuildFromSZLow: insert L%d: %v", slot, err))
			continue
		}

		newLevel := GridLevel{
			ID: levelID, LevelIdx: nextLevelIdx, Side: side,
			TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
			Status: LevelPending, Slot: &slotCopy,
		}
		sr.levels = append(sr.levels, newLevel)
		newPtr := &sr.levels[len(sr.levels)-1]

		if err := sr.placeMatrixLevel(ctx, newPtr, currentPrice); err != nil {
			sr.errlog(ctx, fmt.Sprintf("[SZ-REBUILD] L%d @ %.4f: %v", slot, targetPrice, err))
		} else {
			sr.info(ctx, fmt.Sprintf("[SZ-REBUILD] L%d @ %.4f", slot, targetPrice))
			placed++
		}

		prevPrice = targetPrice
	}

	// Clear waiting-slot entries for every slot that was just rebuilt so that stale
	// entries from a previous RebuildOnSL=false run (or a restored state) do not
	// cause SZ to flicker or display a stale boundary.
	for _, slot := range slots {
		delete(sr.matrixWaitingSlots, slot)
	}

	sr.info(ctx, fmt.Sprintf("Перестройка сетки от SZ завершена: anchor=%.4f, размещено=%d", anchor, placed))
}

// restoreMatrixWaitingSlots derives waiting-slot state from sr.levels after a service restart.
// A slot is "waiting" if it has an sl_closed level but no active (pending/placed/filled) level.
// Levels are loaded ORDER BY level_idx ASC, so iterating in order means the last sl_closed entry
// for a given slot wins (most recent SL trigger price).
// Must be called with sr.mu held.
func (sr *StrategyRunner) restoreMatrixWaitingSlots() {
	activeSlots := map[int]bool{}
	slClosedSlots := map[int]float64{} // slot → SL trigger price (from sl_price column)
	for _, l := range sr.levels {
		if l.Slot == nil {
			continue
		}
		slot := *l.Slot
		// Positive slots always tracked; slot 0 only when RebuildFromEntry is enabled.
		if slot < 0 || (slot == 0 && !sr.strategy.RebuildFromEntry) {
			continue
		}
		switch l.Status {
		case LevelPending, LevelPlaced, LevelFilled:
			activeSlots[slot] = true
		case LevelSLClosed:
			// sl_price is persisted when the SL order is placed and not cleared on fill,
			// so l.SLPrice is non-zero for any level closed by a proper SL order.
			slClosedSlots[slot] = l.SLPrice
		}
	}
	sr.matrixWaitingSlots = make(map[int]float64)
	for slot, slPrice := range slClosedSlots {
		if !activeSlots[slot] {
			sr.matrixWaitingSlots[slot] = slPrice
			log.Printf("strategy %s: restored waiting slot L(%d) (SL trigger %.4f)", sr.strategy.ID, slot, slPrice)
		}
	}
	// Restore matrixLastSLSlot to the minimum waiting slot.
	// For a typical SHORT/LONG grid, the lowest slot number is the one that was stopped most
	// recently (it is closest to the entry price and fires last as price moves against the trade).
	// Using the minimum gives deterministic SZ display after a service restart.
	sr.matrixLastSLSlot = 0
	for slot := range sr.matrixWaitingSlots {
		if sr.matrixLastSLSlot == 0 || slot < sr.matrixLastSLSlot {
			sr.matrixLastSLSlot = slot
		}
	}
}
