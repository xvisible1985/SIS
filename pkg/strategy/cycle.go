package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"sync"
	"time"

	"sis/pkg/trader"
)

// StrategyRunner holds the runtime state of one strategy.
type StrategyRunner struct {
	mu       sync.Mutex
	strategy Strategy
	runner   *AccountRunner

	cycle     *Cycle
	levels    []GridLevel
	tpOrderID string
	slOrderID string
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
	if err := sr.loadActiveCycle(ctx); err != nil {
		if err2 := sr.startCycle(ctx); err2 != nil {
			log.Printf("strategy %s: start cycle: %v", sr.strategy.ID, err2)
		}
	}
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
	log.Printf("strategy %s: resumed cycle %d with %d levels", sr.strategy.ID, c.CycleNum, len(sr.levels))
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
	for _, side := range sides {
		prices := calculateGridLevels(price, sr.strategy.GridStepPct, sr.strategy.GridLevels, side)
		for _, p := range prices {
			qty := strconv.FormatFloat(sr.strategy.GridSizeUSDT/p, 'f', 6, 64)
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

	log.Printf("strategy %s: started cycle %d at %.2f with %d levels",
		sr.strategy.ID, sr.cycle.CycleNum, price, len(sr.levels))
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
			log.Printf("strategy %s: place level %d: %v", sr.strategy.ID, sr.levels[i].LevelIdx, err)
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
	linkID := fmt.Sprintf("STR-%s-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        l.Side,
		OrderType:   "Limit",
		Qty:         l.Qty,
		Price:       fmt.Sprintf("%.4f", l.TargetPrice),
		TimeInForce: "GoodTillCancel",
		OrderLinkId: linkID,
	})
	if err != nil {
		return err
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW() WHERE id=$3`,
		result.OrderId, linkID, l.ID,
	); err != nil {
		return err
	}
	l.Status = LevelPlaced
	l.ExchangeOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"})
	log.Printf("strategy %s cycle %d: placed level %d %s @ %.4f order=%s",
		sr.strategy.ID, sr.cycle.CycleNum, l.LevelIdx, l.Side, l.TargetPrice, result.OrderId)
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

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			sr.runner.UnregisterOrder(sr.levels[i].ExchangeOrderID)
			sr.levels[i].Status = LevelFilled
			sr.levels[i].FilledPrice = filledPrice
			break
		}
	}
	if err := sr.updateTP(ctx); err != nil {
		log.Printf("strategy %s: updateTP: %v", sr.strategy.ID, err)
	}
	if err := sr.placeNextLevels(ctx); err != nil {
		log.Printf("strategy %s: placeNextLevels: %v", sr.strategy.ID, err)
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

	tpQty := strconv.FormatFloat(totalQty, 'f', 6, 64)
	linkID := fmt.Sprintf("STP-%s-tp-%d", sr.strategy.ID[:8], sr.cycle.CycleNum)
	result, err := trader.PlaceOrder(ctx, sr.runner.creds, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        tpSide,
		OrderType:   "Limit",
		Qty:         tpQty,
		Price:       fmt.Sprintf("%.4f", tpPrice),
		TimeInForce: "GoodTillCancel",
		ReduceOnly:  true,
		OrderLinkId: linkID,
	})
	if err != nil {
		return fmt.Errorf("place TP: %w", err)
	}
	sr.tpOrderID = result.OrderId
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	log.Printf("strategy %s cycle %d: TP placed @ %.4f qty=%s order=%s",
		sr.strategy.ID, sr.cycle.CycleNum, tpPrice, tpQty, result.OrderId)
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

// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	log.Printf("strategy %s: TP filled, cycle %d done", sr.strategy.ID, sr.cycle.CycleNum)
	sr.closeCycle(ctx, "tp")
	sr.maybeRestart(ctx)
}

// handleSLFill is called when the SL order is filled.
func (sr *StrategyRunner) handleSLFill(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	log.Printf("strategy %s: SL filled, cycle %d done", sr.strategy.ID, sr.cycle.CycleNum)
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "sl")
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
		log.Printf("strategy %s: finishing → stopped", sr.strategy.ID)
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
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}
	if sr.slOrderID != "" {
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
}
