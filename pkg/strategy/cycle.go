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
	mu      sync.Mutex
	startMu sync.Mutex // serializes loadOrStart and restartCycle to prevent duplicate placement
	strategy Strategy
	runner   *AccountRunner

	cycle        *Cycle
	levels       []GridLevel
	tpOrderID    string
	slOrderID    string
	tpPlaceSeq   int
	slPlaceSeq   int
	instr            trader.InstrumentInfo
	instrFetchedAt   time.Time // time of last successful GetInstrumentInfo; zero = never
	closedBySelf     bool      // set by handleTPFill/handleSLFill to suppress the WS position-close event
	unavailableCount int     // consecutive "instrument unavailable" errors; stops after 5
	repriceGen       int     // increments on each reprice so re-placed levels get fresh linkIds
	partialCloseQty      float64 // actual exchange position qty after manual partial close; 0 = unset
	lastTPSLQty          float64 // qty that TP/SL were last placed for — skip re-place if unchanged
	lastTPSLAvg          float64 // avg entry that TP/SL were last placed for
	lastTPPrice          float64 // TP price at last placement — changes when tp_pct changes
	lastSLPrice          float64 // SL trigger price at last placement
	positionModeVerified    bool // true after ensurePositionMode succeeded for current HedgeMode value
	lastVerifiedHedge       bool // HedgeMode value at the time positionModeVerified was set
	gridChangedWhileStopped bool // restartCycle called while stopped; apply on next loadOrStart
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
// Returns immediately for stopped strategies — they are managed via handleStopRequest.
func (sr *StrategyRunner) loadOrStart(ctx context.Context) {
	sr.mu.Lock()
	status := sr.strategy.Status
	sr.mu.Unlock()
	if status == StatusStopped {
		return
	}
	sr.mu.Lock()
	needInstr := sr.instrFetchedAt.IsZero() || time.Since(sr.instrFetchedAt) > time.Hour
	sr.mu.Unlock()
	if needInstr {
		instr, err := trader.GetInstrumentInfo(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
		if err != nil {
			log.Printf("strategy %s: instrument info: %v", sr.strategy.ID, err)
		} else {
			sr.mu.Lock()
			sr.instr = instr
			sr.instrFetchedAt = time.Now()
			sr.mu.Unlock()
		}
	}
	loadErr := sr.loadActiveCycle(ctx)
	if loadErr != nil && sr.strategy.Status == StatusActive {
		// No active cycle. If a previously stopped cycle has an open position on the
		// exchange, reactivate it instead of starting fresh — preserves fills and
		// prevents placing duplicate entry orders on top of an existing position.
		if sr.reopenCycleIfPositionOpen(ctx) {
			loadErr = sr.loadActiveCycle(ctx)
		}
	}

	if loadErr != nil {
		sr.startMu.Lock()
		err2 := sr.startCycle(ctx)
		sr.startMu.Unlock()
		if err2 != nil {
			log.Printf("strategy %s: start cycle: %v", sr.strategy.ID, err2)
		}
		return
	}

	// Levels that were cancelled by a previous stop (cancelPlacedLevels sets
	// status='cancelled' in DB but leaves in-memory Status unchanged) must be
	// reset to pending so placeNextLevels can re-place them on resume.
	sr.resetCancelledLevels(ctx)

	// Settings were changed while the strategy was stopped — apply them now.
	sr.mu.Lock()
	needsRestart := sr.gridChangedWhileStopped
	if needsRestart {
		sr.gridChangedWhileStopped = false
	}
	sr.mu.Unlock()
	if needsRestart {
		go sr.restartCycle(ctx)
		return
	}

	// Check if filled levels still have a real open position on exchange.
	// Handles the case where the position was closed while the service was down.
	if sr.checkPositionGone(ctx) {
		// Cycle was cleaned up; start fresh if strategy is still active.
		if sr.strategy.Status == StatusActive {
			sr.startMu.Lock()
			err2 := sr.startCycle(ctx)
			sr.startMu.Unlock()
			if err2 != nil {
				log.Printf("strategy %s: start cycle after position cleanup: %v", sr.strategy.ID, err2)
			}
		}
		return
	}

	// Verify exchange order state against DB. Handles stale TP/SL IDs, missed
	// level fills, and market closes when price passed TP/SL while service was down.
	if sr.reconcileOrders(ctx) {
		return
	}

	// Reprice any pending levels that the market has already passed.
	sr.repriceStale(ctx)

	// Cancel and remove levels that exceed the current grid size setting.
	sr.trimExcessLevels(ctx)

	// Placement section: hold startMu so restartCycle doesn't run concurrently
	// with level placement, which would cause duplicate "Выставлено N уровней" logs.
	sr.startMu.Lock()
	defer sr.startMu.Unlock()

	// Re-verify cycle is still open before placing levels.
	// Guards against race: restartCycle may have closed the cycle between
	// loadActiveCycle and here.
	sr.mu.Lock()
	if sr.cycle == nil {
		// Cycle was closed concurrently (e.g., restartCycle ran); it will handle placement.
		sr.mu.Unlock()
		return
	}
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
	// Bump repriceGen so re-placed levels get linkIds Bybit has never seen.
	// Prevents 110072 "duplicate linkId" when Stop→Start reuses the same cycle.
	sr.repriceGen++
	if err := sr.placeNextLevels(ctx); err != nil {
		log.Printf("strategy %s: place pending levels: %v", sr.strategy.ID, err)
	}
	// Restore TP/SL if absent — happens after cycle reopen (they were cancelled on stop).
	if sr.tpOrderID == "" {
		if err := sr.updateTP(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка восстановления TP: %v", err))
		}
	}
	if sr.slOrderID == "" {
		if err := sr.updateSL(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка восстановления SL: %v", err))
		}
	}
	sr.mu.Unlock()
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

// reconcileOrders verifies exchange order state against DB state on startup.
// Clears stale TP/SL IDs, re-queues missing level orders, and triggers a
// market close if price has passed the TP or SL while the service was down.
// Returns true if the cycle was closed (caller should return immediately).
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) reconcileOrders(ctx context.Context) bool {
	// Fast path: skip two REST calls if there is nothing to check.
	sr.mu.Lock()
	hasWork := sr.tpOrderID != "" || sr.slOrderID != ""
	if !hasWork {
		for _, l := range sr.levels {
			if l.Status == LevelPlaced && l.ExchangeOrderID != "" {
				hasWork = true
				break
			}
		}
	}
	sr.mu.Unlock()
	if !hasWork {
		return false
	}

	// Fetch mark price and open orders concurrently (order fetch uses category-only
	// query: 2 parallel requests instead of the 6-request FetchOpenOrders).
	type priceRes struct {
		p   float64
		err error
	}
	type ordersRes struct {
		o   []trader.Order
		err error
	}
	prCh := make(chan priceRes, 1)
	orCh := make(chan ordersRes, 1)
	go func() {
		p, e := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
		prCh <- priceRes{p, e}
	}()
	go func() {
		o, e := trader.FetchOpenOrdersForCategory(ctx, sr.runner.creds, sr.strategy.Category)
		orCh <- ordersRes{o, e}
	}()
	pr := <-prCh
	or := <-orCh
	if pr.err != nil {
		log.Printf("strategy %s: reconcile: fetch price: %v", sr.strategy.ID, pr.err)
		return false
	}
	if or.err != nil {
		log.Printf("strategy %s: reconcile: fetch orders: %v", sr.strategy.ID, or.err)
		return false
	}
	price := pr.p
	openOrders := or.o
	active := make(map[string]bool, len(openOrders))
	for _, o := range openOrders {
		active[o.OrderId] = true
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	// --- TP ---
	if sr.tpOrderID != "" && !active[sr.tpOrderID] {
		sr.info(ctx, fmt.Sprintf("reconcile: TP %s не найден на бирже", sr.tpOrderID))
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, sr.cycle.ID)

		avg, totalQty := sr.avgEntry()
		if avg > 0 && totalQty > 0 && sr.strategy.TPPct > 0 {
			_, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
			passed := (sr.strategy.Direction == DirectionLong && price >= tpPrice) ||
				(sr.strategy.Direction == DirectionShort && price <= tpPrice)
			if passed {
				sr.info(ctx, fmt.Sprintf("reconcile: цена %.4f прошла TP %.4f, закрываю позицию маркетом", price, tpPrice))
				sr.closePositionAtMarket(ctx, "tp")
				return true
			}
		}
	}

	// --- SL ---
	if sr.slOrderID != "" && !active[sr.slOrderID] {
		sr.info(ctx, fmt.Sprintf("reconcile: SL %s не найден на бирже", sr.slOrderID))
		sr.runner.UnregisterOrder(sr.slOrderID)
		sr.slOrderID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_cycles SET sl_order_id=NULL WHERE id=$1`, sr.cycle.ID)

		avg, totalQty := sr.avgEntry()
		if avg > 0 && totalQty > 0 && sr.strategy.SLPct > 0 {
			slSide, slTrigger, _ := slParams(sr.strategy.Direction, avg, sr.strategy.SLPct)
			passed := (slSide == "Sell" && price <= slTrigger) ||
				(slSide == "Buy" && price >= slTrigger)
			if passed {
				sr.info(ctx, fmt.Sprintf("reconcile: цена %.4f прошла SL %.4f, закрываю позицию маркетом", price, slTrigger))
				sr.closePositionAtMarket(ctx, "sl")
				return true
			}
		}
	}

	// --- Grid levels ---
	var passedLevels []*GridLevel
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPlaced || l.ExchangeOrderID == "" {
			continue
		}
		if active[l.ExchangeOrderID] {
			continue
		}
		passed := (l.Side == "Buy" && price <= l.TargetPrice) ||
			(l.Side == "Sell" && price >= l.TargetPrice)
		sr.info(ctx, fmt.Sprintf("reconcile: L%d ордер %s не найден на бирже (target=%.4f passed=%v)",
			l.LevelIdx, l.ExchangeOrderID, l.TargetPrice, passed))
		sr.runner.UnregisterOrder(l.ExchangeOrderID)
		l.ExchangeOrderID = ""
		if passed {
			passedLevels = append(passedLevels, l)
		} else {
			l.Status = LevelPending
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, l.ID)
		}
	}

	if len(passedLevels) == 0 {
		return false
	}

	// For each passed level: check execution history first — if the order was
	// filled on the exchange while we were offline, record the fill instead of
	// opening a duplicate position via market order.
	var stillMissing []*GridLevel
	for _, l := range passedLevels {
		linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
		existing, _, err := trader.FetchOrderByLinkId(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol, linkID)
		if err == nil && existing.OrderStatus == "Filled" {
			filledPrice, _ := strconv.ParseFloat(existing.Price, 64)
			if filledPrice == 0 {
				filledPrice = price
			}
			l.Status = LevelFilled
			l.FilledPrice = filledPrice
			l.ExchangeOrderID = existing.OrderId
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW(), exchange_order_id=$2 WHERE id=$3`,
				filledPrice, existing.OrderId, l.ID)
			sr.info(ctx, fmt.Sprintf("reconcile: L%d подхвачен из истории, исполнен @ %.4f", l.LevelIdx, filledPrice))
		} else {
			stillMissing = append(stillMissing, l)
		}
	}

	if len(stillMissing) == 0 {
		return false
	}

	// Nearest still-unaccounted passed level → fill at market immediately.
	// The rest → reset to pending so placeNextLevels re-queues them as limits.
	nearest := stillMissing[0]
	minDist := math.Abs(nearest.TargetPrice - price)
	for _, l := range stillMissing[1:] {
		if d := math.Abs(l.TargetPrice - price); d < minDist {
			minDist = d
			nearest = l
		}
	}
	for _, l := range stillMissing {
		if l == nearest {
			continue
		}
		l.Status = LevelPending
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, l.ID)
	}

	sr.info(ctx, fmt.Sprintf("reconcile: L%d цена прошла уровень, исполняю маркетом", nearest.LevelIdx))
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        nearest.Side,
		OrderType:   "Market",
		Qty:         nearest.Qty,
		PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, nearest.Side),
	})
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("reconcile: market fill L%d: %v", nearest.LevelIdx, err))
		nearest.Status = LevelPending
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, nearest.ID)
	} else {
		nearest.Status = LevelFilled
		nearest.FilledPrice = price
		nearest.ExchangeOrderID = result.OrderId
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW(), exchange_order_id=$2 WHERE id=$3`,
			price, result.OrderId, nearest.ID)
		sr.info(ctx, fmt.Sprintf("reconcile: L%d исполнен @ %.4f", nearest.LevelIdx, price))
	}
	return false
}

// closePositionAtMarket places a market close order for the full position and closes the cycle.
// Must be called with sr.mu held.
func (sr *StrategyRunner) closePositionAtMarket(ctx context.Context, reason string) {
	_, totalQty := sr.avgEntry()
	if totalQty == 0 {
		return
	}
	closeSide := "Sell"
	if sr.strategy.Direction == DirectionShort {
		closeSide = "Buy"
	}
	qty := trader.FormatQty(totalQty, sr.instr.QtyStep, sr.instr.MinQty)
	_, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         qty,
		ReduceOnly:  !sr.strategy.HedgeMode,
		PositionIdx: positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
	})
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("closePositionAtMarket: %v", err))
		return
	}
	sr.closedBySelf = true
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, reason)
	sr.maybeRestart(ctx)
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
	sr.levels = nil // clear stale in-memory state before reloading from DB

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

// ensurePositionMode switches the exchange position mode for this symbol to match
// the strategy's HedgeMode setting (one-way=0 or hedge=3). Returns false and logs
// if the switch fails (e.g. Bybit refuses because an open position already exists).
// Must be called with sr.mu held. Skips the REST call if the mode was already
// verified for the current HedgeMode value (cached across Stop→Active cycles).
func (sr *StrategyRunner) ensurePositionMode(ctx context.Context) bool {
	if sr.positionModeVerified && sr.lastVerifiedHedge == sr.strategy.HedgeMode {
		return true
	}
	mode := 0 // one-way (default)
	if sr.strategy.HedgeMode {
		mode = 3 // both-side / hedge
	}
	if err := trader.SwitchPositionMode(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol, mode); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Переключение position mode: %v", err))
		return false
	}
	sr.positionModeVerified = true
	sr.lastVerifiedHedge = sr.strategy.HedgeMode
	modeLabel := "one-way"
	if mode == 3 {
		modeLabel = "hedge"
	}
	sr.info(ctx, fmt.Sprintf("Position mode: %s (%s)", modeLabel, sr.strategy.Symbol))
	return true
}

// startCycle creates a new cycle: fetches price, calculates grid, inserts into DB, places initial window.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) startCycle(ctx context.Context) error {
	// Guard: check BEFORE taking the lock to avoid deadlock.
	// If there are already pending/placed levels in DB for an active (open) cycle,
	// do NOT create a new cycle — load those levels instead to avoid duplicates.
	// This catches the case where loadActiveCycle failed transiently but an active
	// cycle with orders actually exists in DB.
	var existingLevels int
	_ = sr.runner.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategy_levels sl
		 JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		 WHERE sc.strategy_id = $1 AND sc.ended_at IS NULL
		   AND sl.status IN ('pending','placed')`,
		sr.strategy.ID,
	).Scan(&existingLevels)
	if existingLevels > 0 {
		log.Printf("strategy %s: startCycle skipped — %d active levels already in DB", sr.strategy.ID, existingLevels)
		if err := sr.loadActiveCycle(ctx); err != nil {
			return fmt.Errorf("startCycle guard: reload failed: %w", err)
		}
		sr.resetCancelledLevels(ctx)
		sr.mu.Lock()
		defer sr.mu.Unlock()
		return sr.placeNextLevels(ctx)
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status == StatusStopped {
		return nil
	}

	// Ensure the exchange position mode matches the strategy config before placing
	// any orders. Bybit returns 10001 if positionIdx does not match the account mode.
	if !sr.ensurePositionMode(ctx) {
		sr.stopWithMessage(ctx, "Не удалось переключить position mode — стратегия остановлена")
		return fmt.Errorf("position mode mismatch, cycle not started")
	}

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
				qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
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
				qty := trader.FormatQty(sr.strategy.GridSizeUSDT/p, sr.instr.QtyStep, sr.instr.MinQty)
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
// Uses batch placement when more than one level needs to be placed.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeNextLevels(ctx context.Context) error {
	if sr.strategy.Status == StatusStopped {
		return nil
	}
	need := sr.strategy.GridActive - sr.placedCount()
	var toPlace []int
	for i := range sr.levels {
		if need <= 0 {
			break
		}
		if sr.levels[i].Status == LevelPending {
			toPlace = append(toPlace, i)
			need--
		}
	}
	if len(toPlace) == 0 {
		return nil
	}
	if len(toPlace) == 1 {
		if err := sr.placeLevel(ctx, toPlace[0]); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровня L%d: %v", sr.levels[toPlace[0]].LevelIdx, err))
		}
		return nil
	}
	// Batch placement in chunks of 20 (Bybit limit).
	for start := 0; start < len(toPlace); start += 20 {
		end := start + 20
		if end > len(toPlace) {
			end = len(toPlace)
		}
		sr.placeLevelsBatch(ctx, toPlace[start:end])
	}
	return nil
}

// placeLevelsBatch places a slice of level indices in one batch REST call.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeLevelsBatch(ctx context.Context, indices []int) {
	items := make([]trader.BatchOrderItem, len(indices))
	refs := make([]orderRef, len(indices))
	linkIDs := make([]string, len(indices))

	for j, i := range indices {
		l := &sr.levels[i]
		linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
		linkIDs[j] = linkID
		refs[j] = orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
		sr.runner.RegisterOrder(linkID, refs[j])

		item := trader.BatchOrderItem{
			Symbol:      sr.strategy.Symbol,
			Side:        l.Side,
			Qty:         l.Qty,
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		}
		if l.TargetPrice == 0 {
			item.OrderType = "Market"
		} else {
			formattedPrice := trader.FormatPrice(l.TargetPrice, sr.instr.TickSize)
			if sr.strategy.EntryOrderType == "stop_market" {
				trigDir := 1 // SHORT: price rises to trigger
				if sr.strategy.Direction == DirectionLong {
					trigDir = 2 // LONG: price falls to trigger
				}
				item.OrderType = "Market"
				item.TriggerPrice = formattedPrice
				item.TriggerDirection = trigDir
				item.OrderFilter = "StopOrder"
			} else {
				item.OrderType = "Limit"
				item.Price = formattedPrice
				item.TimeInForce = "GTC"
			}
		}
		items[j] = item
	}

	results, err := sr.runner.tradeStream.PlaceOrderBatch(ctx, trader.BatchPlaceRequest{
		Category: sr.strategy.Category,
		Request:  items,
	})
	if err != nil {
		for _, lid := range linkIDs {
			sr.runner.UnregisterOrder(lid)
		}
		sr.errlog(ctx, fmt.Sprintf("Batch выставление: %v", err))
		return
	}

	var placedLines []string
	for j, res := range results {
		// stopWithMessage → closeCycle zeros sr.levels; bail out immediately.
		if sr.cycle == nil || j >= len(results) {
			break
		}
		i := indices[j]
		if i >= len(sr.levels) {
			break
		}
		l := &sr.levels[i]
		linkID := linkIDs[j]
		if res.Code != 0 {
			sr.runner.UnregisterOrder(linkID)
			if res.Code == 110072 {
				sr.recoverDuplicateLevel(ctx, i, linkID)
			} else if res.Code == 110094 {
				sr.stopWithMessage(ctx, fmt.Sprintf("Размер ордера L%d ниже минимума биржи — увеличьте размер ордера. Стратегия остановлена", l.LevelIdx))
				return
			} else {
				sr.errlog(ctx, fmt.Sprintf("Batch L%d: [%d] %s", l.LevelIdx, res.Code, res.Msg))
			}
			continue
		}
		roundedPrice := l.TargetPrice
		if l.TargetPrice > 0 {
			roundedPrice, _ = strconv.ParseFloat(trader.FormatPrice(l.TargetPrice, sr.instr.TickSize), 64)
			l.TargetPrice = roundedPrice
		}
		if _, err := sr.runner.pool.Exec(ctx,
			`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW(),
			 target_price=CASE WHEN $4::numeric > 0 THEN $4::numeric ELSE target_price END WHERE id=$3`,
			res.OrderId, linkID, l.ID, roundedPrice,
		); err != nil {
			sr.runner.UnregisterOrder(linkID)
			sr.errlog(ctx, fmt.Sprintf("Batch DB L%d: %v", l.LevelIdx, err))
			continue
		}
		l.Status = LevelPlaced
		l.ExchangeOrderID = res.OrderId
		sr.runner.RegisterOrder(res.OrderId, refs[j])
		if l.TargetPrice == 0 {
			placedLines = append(placedLines, fmt.Sprintf("  • L%d %s MARKET (%.0f USDT)", l.LevelIdx, l.Side, l.SizeUSDT))
		} else {
			qty, _ := strconv.ParseFloat(l.Qty, 64)
			placedLines = append(placedLines, fmt.Sprintf("  • L%d %s @ %.4f (%.0f USDT)", l.LevelIdx, l.Side, l.TargetPrice, qty*l.TargetPrice))
		}
	}
	if len(placedLines) > 0 {
		sr.info(ctx, fmt.Sprintf("Выставлено %d %s:\n%s",
			len(placedLines), levelWord(len(placedLines)), strings.Join(placedLines, "\n")))
	}
}

// placeLevel places a single grid level on the exchange.
// Must be called with sr.mu held.
func (sr *StrategyRunner) placeLevel(ctx context.Context, idx int) error {
	l := &sr.levels[idx]
	linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
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
		formattedPrice := trader.FormatPrice(l.TargetPrice, sr.instr.TickSize)
		if sr.strategy.EntryOrderType == "stop_market" {
			trigDir := 1 // SHORT: price rises to trigger
			if sr.strategy.Direction == DirectionLong {
				trigDir = 2 // LONG: price falls to trigger
			}
			req = trader.OrderRequest{
				Symbol:           sr.strategy.Symbol,
				Category:         sr.strategy.Category,
				Side:             l.Side,
				OrderType:        "Market",
				Qty:              l.Qty,
				TriggerPrice:     formattedPrice,
				TriggerDirection: trigDir,
				OrderFilter:      "StopOrder",
				PositionIdx:      positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
				OrderLinkId:      linkID,
			}
		} else {
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
		}
	}
	// Pre-register by linkID before placing so market order fills that arrive
	// before RegisterOrder(orderId) is called are not dropped.
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.tradeStream.PlaceOrder(ctx, req)
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		if isDuplicateLinkId(err) {
			sr.recoverDuplicateLevel(ctx, idx, linkID)
			return nil
		}
		if isInsufficientBalance(err) {
			sr.stopWithMessage(ctx, "Недостаточно баланса для выставления ордера — стратегия остановлена")
			return err
		}
		if isMinOrderValue(err) {
			sr.stopWithMessage(ctx, "Размер ордера ниже минимума биржи — увеличьте размер ордера. Стратегия остановлена")
			return err
		}
		if isInstrumentUnavailable(err) {
			sr.unavailableCount++
			if sr.unavailableCount >= 5 {
				sr.stopWithMessage(ctx, fmt.Sprintf("Инструмент %s недоступен (5 попыток) — стратегия остановлена", sr.strategy.Symbol))
			}
			return err
		}
		return err
	}
	sr.unavailableCount = 0 // reset on successful placement
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
	sr.runner.RegisterOrder(result.OrderId, ref) // also register by orderId
	if l.TargetPrice == 0 {
		sr.info(ctx, fmt.Sprintf("Выставлен L%d %s MARKET (%.0f USDT)", l.LevelIdx, l.Side, l.SizeUSDT))
	} else {
		qty, _ := strconv.ParseFloat(l.Qty, 64)
		sr.info(ctx, fmt.Sprintf("Выставлен L%d %s @ %.4f (%.0f USDT)", l.LevelIdx, l.Side, l.TargetPrice, qty*l.TargetPrice))
	}
	return nil
}

// placedCount returns the number of grid levels currently placed on the exchange.
// TP and SL orders (held in sr.tpOrderID / sr.slOrderID) are managed separately
// and must NOT be counted toward GridActive.
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
	// A new level fill means the position grew — any partial-close override is stale.
	sr.partialCloseQty = 0
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

	tpSide, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
	if tpSide == "" {
		return nil
	}

	// After a manual partial close, use the actual exchange position size so the
	// TP qty matches the real remaining position and Bybit doesn't auto-cancel it.
	if sr.partialCloseQty > 0 && sr.partialCloseQty < totalQty {
		totalQty = sr.partialCloseQty
	}

	// Skip if TP is already placed with the same qty, avg entry, and price.
	// Must be checked BEFORE cancelling the existing order.
	if sr.tpOrderID != "" && sr.lastTPSLQty == totalQty && sr.lastTPSLAvg == avg && sr.lastTPPrice == tpPrice {
		return nil
	}

	if sr.tpOrderID != "" {
		// Unregister BEFORE sending cancel so the incoming WS cancel event
		// does not trigger handleTPSLCancelled for our own intentional cancel.
		oldTPID := sr.tpOrderID
		sr.runner.UnregisterOrder(oldTPID)
		sr.tpOrderID = ""
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: oldTPID,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("updateTP: отмена TP %s: %v", oldTPID, err))
		}
	}

	sr.tpPlaceSeq++
	tpQty := trader.FormatQty(totalQty, sr.instr.QtyStep, sr.instr.MinQty)
	linkID := fmt.Sprintf("SIS_STR-%s-tp-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, sr.tpPlaceSeq)
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
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
		if isDuplicateLinkId(err) {
			sr.recoverDuplicateTP(ctx, linkID)
			return nil
		}
		return fmt.Errorf("place TP: %w", err)
	}
	sr.tpOrderID = result.OrderId
	sr.lastTPSLQty = totalQty
	sr.lastTPSLAvg = avg
	sr.lastTPPrice = tpPrice
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
	// Do not place SL until the entire grid is filled — wait for no pending/placed levels.
	for _, l := range sr.levels {
		if l.Status == LevelPending || l.Status == LevelPlaced {
			return nil
		}
	}
	avg, totalQty := sr.avgEntry()
	if totalQty == 0 || avg == 0 {
		return nil
	}

	slSide, slTrigger, trigDir := slParams(sr.strategy.Direction, avg, sr.strategy.SLPct)
	if slSide == "" {
		return nil
	}

	// Same as updateTP: use actual exchange qty after partial manual close.
	if sr.partialCloseQty > 0 && sr.partialCloseQty < totalQty {
		totalQty = sr.partialCloseQty
	}

	// Skip if SL is already placed with the same qty, avg entry, and trigger price.
	// Must be checked BEFORE cancelling the existing order.
	if sr.slOrderID != "" && sr.lastTPSLQty == totalQty && sr.lastTPSLAvg == avg && sr.lastSLPrice == slTrigger {
		return nil
	}

	if sr.slOrderID != "" {
		oldSLID := sr.slOrderID
		sr.runner.UnregisterOrder(oldSLID)
		sr.slOrderID = ""
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
			OrderId: oldSLID, OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("updateSL: отмена SL %s: %v", oldSLID, err))
		}
	}

	sr.slPlaceSeq++
	slQty := trader.FormatQty(totalQty, sr.instr.QtyStep, sr.instr.MinQty)
	linkID := fmt.Sprintf("SIS_STR-%s-sl-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, sr.slPlaceSeq)
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
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
		if isDuplicateLinkId(err) {
			sr.recoverDuplicateSL(ctx, linkID)
			return nil
		}
		return fmt.Errorf("place SL: %w", err)
	}
	sr.slOrderID = result.OrderId
	sr.lastTPSLQty = totalQty
	sr.lastTPSLAvg = avg
	sr.lastSLPrice = slTrigger
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET sl_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("SL выставлен @ %.4f qty=%s", slTrigger, slQty))
	return nil
}

// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	avg, posQty := sr.avgEntry()
	if fillPrice == 0 {
		fillPrice = avg * (1 + sr.strategy.TPPct/100)
		if sr.strategy.Direction == DirectionShort {
			fillPrice = avg * (1 - sr.strategy.TPPct/100)
		}
	}
	if fillQty == 0 {
		fillQty = posQty
	}
	pnl := (fillPrice - avg) * fillQty
	if sr.strategy.Direction == DirectionShort {
		pnl = (avg - fillPrice) * fillQty
	}
	pnlPct := 0.0
	if avg > 0 {
		pnlPct = pnl / (avg * fillQty) * 100
	}
	sr.info(ctx, fmt.Sprintf("✅ TP исполнен — цикл %d | avg=%.4f → %.4f | qty=%.4f | PnL +%.2f USDT (+%.2f%%)",
		sr.cycle.CycleNum, avg, fillPrice, fillQty, pnl, pnlPct))
	sr.closedBySelf = true // suppress the position-close WS event that follows TP fill
	sr.tpOrderID = ""     // already filled — closeCycle must not try to cancel it
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "tp") // cancels SL if present
	sr.maybeRestart(ctx)
}

// handleSLFill is called when the SL order is filled.
func (sr *StrategyRunner) handleSLFill(ctx context.Context, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	avg, posQty := sr.avgEntry()
	if fillPrice == 0 {
		_, slTrigger, _ := slParams(sr.strategy.Direction, avg, sr.strategy.SLPct)
		fillPrice = slTrigger
	}
	if fillQty == 0 {
		fillQty = posQty
	}
	pnl := (fillPrice - avg) * fillQty
	if sr.strategy.Direction == DirectionShort {
		pnl = (avg - fillPrice) * fillQty
	}
	pnlPct := 0.0
	if avg > 0 {
		pnlPct = pnl / (avg * fillQty) * 100
	}
	sr.warn(ctx, fmt.Sprintf("❌ SL сработал — цикл %d | avg=%.4f → %.4f | qty=%.4f | PnL %.2f USDT (%.2f%%)",
		sr.cycle.CycleNum, avg, fillPrice, fillQty, pnl, pnlPct))
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
		`UPDATE strategy_cycles SET ended_at=NOW(), result=$1, tp_order_id=NULL, sl_order_id=NULL WHERE id=$2`, result, sr.cycle.ID)
	if sr.tpOrderID != "" {
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: sr.tpOrderID,
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("closeCycle: отмена TP %s: %v", sr.tpOrderID, err))
		}
		sr.runner.UnregisterOrder(sr.tpOrderID)
		sr.tpOrderID = ""
	}
	if sr.slOrderID != "" {
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
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
	sr.partialCloseQty = 0
	sr.lastTPSLQty = 0
	sr.lastTPSLAvg = 0
}

// cancelPlacedLevels cancels only the grid-level orders belonging to this strategy,
// leaving orders of other strategies on the same symbol untouched.
// Must be called with sr.mu held.
func (sr *StrategyRunner) cancelPlacedLevels(ctx context.Context) {
	var items []trader.BatchCancelItem
	var cancelLines []string
	for _, l := range sr.levels {
		if l.Status == LevelPlaced && l.ExchangeOrderID != "" {
			items = append(items, trader.BatchCancelItem{
				Symbol:  sr.strategy.Symbol,
				OrderId: l.ExchangeOrderID,
			})
			// Do NOT unregister here — if a market-entry order is still in flight its
			// WS fill event must still route to handleLevelFill so the DB level gets
			// marked filled. WS cancel/fill events handle deregistration naturally.
			if l.TargetPrice == 0 {
				cancelLines = append(cancelLines, fmt.Sprintf("  • L%d %s MARKET", l.LevelIdx, l.Side))
			} else {
				cancelLines = append(cancelLines, fmt.Sprintf("  • L%d %s @ %.4f", l.LevelIdx, l.Side, l.TargetPrice))
			}
		}
	}
	if len(cancelLines) > 0 {
		sr.info(ctx, fmt.Sprintf("Отменено %d %s:\n%s",
			len(cancelLines), levelWord(len(cancelLines)), strings.Join(cancelLines, "\n")))
	}
	for start := 0; start < len(items); start += 20 {
		end := start + 20
		if end > len(items) {
			end = len(items)
		}
		if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
			Category: sr.strategy.Category,
			Request:  items[start:end],
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("cancelPlacedLevels: batch cancel: %v", err))
		}
	}
	if sr.cycle != nil {
		if _, err := sr.runner.pool.Exec(ctx,
			`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status IN ('placed','pending')`, sr.cycle.ID,
		); err != nil {
			sr.errlog(ctx, fmt.Sprintf("cancelPlacedLevels: DB update: %v", err))
		}
	}
}

// handleStopRequest cancels placed L-orders but keeps TP and SL active so the
// current cycle ends naturally. Called when status transitions to stopped.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleStopRequest(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.cycle == nil {
		sr.info(ctx, "Стратегия остановлена (нет активного цикла)")
		return
	}
	sr.cancelPlacedLevels(ctx)

	// Check whether there is an open position. Only filled levels indicate a real
	// exchange position; placed-but-unfilled orders do not.
	hasPosition := sr.tpOrderID != "" || sr.slOrderID != ""
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasPosition = true
			break
		}
	}
	if !hasPosition {
		sr.closeCycle(ctx, "stopped")
		sr.info(ctx, "Стратегия остановлена — нет открытой позиции, цикл закрыт")
		return
	}
	sr.info(ctx, "Стратегия остановлена — L-ордера отменены, TP/SL продолжают работать до конца цикла")
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

// restartCycle applies updated strategy settings to the running cycle.
// If a position is open (filled levels exist), only the pending levels are repriced
// from the last filled level — the cycle stays open to protect the position.
// If there is no open position, the cycle is closed and a fresh one is started.
// Called after strategy settings are updated so new parameters take effect immediately.
func (sr *StrategyRunner) restartCycle(ctx context.Context) {
	sr.startMu.Lock()
	defer sr.startMu.Unlock()

	sr.mu.Lock()
	if sr.strategy.Status != StatusActive {
		// Detect open position: any filled level means an open trade.
		hasFills := false
		for _, l := range sr.levels {
			if l.Status == LevelFilled {
				hasFills = true
				break
			}
		}
		if hasFills {
			// Position is open — can't close the cycle.
			// Remember to reprice pending levels when the strategy is next activated.
			sr.gridChangedWhileStopped = true
		} else {
			// No open position — close the stale cycle immediately in DB so the
			// next loadOrStart calls startCycle with the updated settings.
			sr.cancelPlacedLevels(ctx)
			if sr.cycle != nil {
				sr.closeCycle(ctx, "settings_changed")
			}
		}
		sr.mu.Unlock()
		return
	}

	// Detect open position: any filled level means an open trade.
	hasFills := false
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasFills = true
			break
		}
	}

	if hasFills && sr.cycle != nil {
		// Position is open — do NOT close the cycle.
		// Cancel pending exchange orders and reprice them from the last fill price.
		sr.info(ctx, "Настройки изменены, позиция открыта — пересчёт отложенных уровней")
		sr.cancelPlacedLevels(ctx)
		// Reset placed→pending so they can be re-placed with new prices.
		for i := range sr.levels {
			if sr.levels[i].Status == LevelPlaced {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`,
					sr.levels[i].ID)
			}
		}
		// Trim levels that exceed the new step count (handles grid reduction with open position).
		// cancelPlacedLevels already set placed+pending levels to 'cancelled' in DB;
		// the for-loop above reset the originally-placed ones back to 'pending'.
		// Pending levels beyond the new limit remain in-memory but must not be re-placed.
		{
			expectedPerSide := len(sr.strategy.Steps)
			if expectedPerSide == 0 {
				expectedPerSide = sr.strategy.GridLevels
			}
			sideCount := map[string]int{}
			trimCount := 0
			n := 0
			for _, l := range sr.levels {
				sideCount[l.Side]++
				if l.Status != LevelFilled && sideCount[l.Side] > expectedPerSide {
					sr.runner.pool.Exec(ctx, //nolint:errcheck
						`UPDATE strategy_levels SET status='cancelled' WHERE id=$1`, l.ID)
					trimCount++
					continue
				}
				sr.levels[n] = l
				n++
			}
			sr.levels = sr.levels[:n]
			if trimCount > 0 {
				sr.info(ctx, fmt.Sprintf("Убрано %d лишних %s (лимит: %d на сторону)",
					trimCount, levelWord(trimCount), expectedPerSide))
			}
		}
		// Bump so re-placed levels get linkIds Bybit has never seen (avoids 110072).
		sr.repriceGen++
		sr.repriceRemainingFromFills(ctx)
		if err := sr.placeNextLevels(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровней: %v", err))
		}
		if err := sr.updateTP(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка обновления TP: %v", err))
		}
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

// repriceRemainingFromFills recalculates target prices for pending levels after a
// settings change while a position is open. Prices are computed from the last filled
// level's fill price using the updated strategy.Steps, preserving the compound chain.
// Must be called with sr.mu held.
func (sr *StrategyRunner) repriceRemainingFromFills(ctx context.Context) {
	if len(sr.strategy.Steps) == 0 {
		return
	}

	for _, side := range sidesForDirection(sr.strategy.Direction) {
		// Collect levels for this side in level_idx order (already ordered in sr.levels).
		var sideLevels []*GridLevel
		for i := range sr.levels {
			if sr.levels[i].Side == side {
				sideLevels = append(sideLevels, &sr.levels[i])
			}
		}

		// Find the last filled level and its effective price.
		lastFilledSideIdx := -1
		var basePrice float64
		for i, l := range sideLevels {
			if l.Status == LevelFilled {
				lastFilledSideIdx = i
				if l.FilledPrice > 0 {
					basePrice = l.FilledPrice
				} else {
					basePrice = l.TargetPrice
				}
			}
		}
		if lastFilledSideIdx < 0 || basePrice == 0 {
			continue
		}

		// Reprice each pending level that follows the last fill, using the corresponding step.
		prevPrice := basePrice
		repriced := 0
		stepIdx := lastFilledSideIdx + 1
		for _, l := range sideLevels[lastFilledSideIdx+1:] {
			if stepIdx >= len(sr.strategy.Steps) {
				break
			}
			if l.Status != LevelPending {
				stepIdx++
				continue
			}
			step := sr.strategy.Steps[stepIdx]
			var target float64
			if side == "Buy" {
				target = prevPrice * (1 - step.PriceMovePct/100)
			} else {
				target = prevPrice * (1 + step.PriceMovePct/100)
			}
			sizeUSDT := step.Lots * sr.strategy.GridSizeUSDT
			qty := trader.FormatQty(sizeUSDT/target, sr.instr.QtyStep, sr.instr.MinQty)
			l.TargetPrice = target
			l.SizeUSDT = sizeUSDT
			l.Qty = qty
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET target_price=$1, qty=$2, size_usdt=$3 WHERE id=$4`,
				target, qty, sizeUSDT, l.ID)
			prevPrice = target
			stepIdx++
			repriced++
		}
		// Create DB rows for steps that have no corresponding level yet (grid was expanded).
		created := 0
		if stepIdx < len(sr.strategy.Steps) {
			maxIdx := 0
			for _, l := range sr.levels {
				if l.LevelIdx > maxIdx {
					maxIdx = l.LevelIdx
				}
			}
			for ; stepIdx < len(sr.strategy.Steps); stepIdx++ {
				step := sr.strategy.Steps[stepIdx]
				var target float64
				if side == "Buy" {
					target = prevPrice * (1 - step.PriceMovePct/100)
				} else {
					target = prevPrice * (1 + step.PriceMovePct/100)
				}
				sizeUSDT := step.Lots * sr.strategy.GridSizeUSDT
				qty := trader.FormatQty(sizeUSDT/target, sr.instr.QtyStep, sr.instr.MinQty)
				maxIdx++
				var levelID string
				if err := sr.runner.pool.QueryRow(ctx,
					`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty)
					 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
					sr.strategy.ID, sr.cycle.ID, maxIdx, side, target, sizeUSDT, qty,
				).Scan(&levelID); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Создание нового уровня L%d: %v", maxIdx, err))
					break
				}
				sr.levels = append(sr.levels, GridLevel{
					ID:          levelID,
					LevelIdx:    maxIdx,
					Side:        side,
					TargetPrice: target,
					SizeUSDT:    sizeUSDT,
					Qty:         qty,
					Status:      LevelPending,
				})
				prevPrice = target
				created++
			}
		}
		if repriced > 0 {
			sr.info(ctx, fmt.Sprintf("Пересчитано %d отложенных уровней от %.4f (%s)", repriced, basePrice, side))
		}
		if created > 0 {
			sr.info(ctx, fmt.Sprintf("Добавлено %d новых уровней от %.4f (%s)", created, basePrice, side))
		}
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
		sr.partialCloseQty = 0
		return
	}

	if sr.cycle == nil {
		return // cycle already closed by TP/SL
	}

	// Determine whether our strategy had an open position.
	// Only LevelFilled counts — a placed (but unfilled) order does not mean
	// a position exists on the exchange. Bybit may send size=0 snapshot events
	// while we are waiting for entries; those must not terminate the strategy.
	hasPosition := sr.tpOrderID != "" || sr.slOrderID != ""
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasPosition = true
			break
		}
	}

	if !hasPosition {
		return
	}

	sr.closeManualPosition(ctx)
}

// closeManualPosition logs the manual-close event, cancels remaining level orders,
// closes the cycle, and stops the strategy. Must be called with sr.mu held.
// It is called both from handlePositionClose and from handleTPSLCancelled (race path
// where the TP cancel event arrives before the position-zero WS event).
func (sr *StrategyRunner) closeManualPosition(ctx context.Context) {
	avg, posQty := sr.avgEntry()
	sr.warn(ctx, fmt.Sprintf("Позиция закрыта вручную — цикл %d | avg=%.4f | qty=%.4f | PnL в разделе Closed P&L",
		sr.cycle.CycleNum, avg, posQty))

	sr.cancelPlacedLevels(ctx)
	cycleID := sr.cycle.ID
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='filled'`, cycleID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("closeManualPosition: DB update filled→cancelled: %v", err))
	}
	for i := range sr.levels {
		if sr.levels[i].Status == LevelFilled {
			sr.levels[i].Status = LevelCancelled
		}
	}
	sr.closeCycle(ctx, "manual_close")
	sr.strategy.Status = StatusStopped
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("closeManualPosition: DB update status→stopped: %v", err))
	}
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
// levelWord returns the Russian noun for "уровень" matching the count (1→уровень, 2-4→уровня, 5+→уровней).
func levelWord(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return "уровень"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		return "уровня"
	default:
		return "уровней"
	}
}

func positionIdxForClose(hedgeMode bool, dir Direction) int {
	if !hedgeMode {
		return 0
	}
	if dir == DirectionLong {
		return 1
	}
	return 2
}

// isDuplicateLinkId returns true for Bybit retCode=110072 (OrderLinkedID is duplicate).
// This happens when a level was successfully placed on the exchange but the DB/memory
// update was lost (e.g. service crash after placement). Recovery: fetch by linkId.
func isDuplicateLinkId(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "retCode=110072")
}

// recoverDuplicateLevel fetches the already-placed order for level idx by linkID,
// updates DB and in-memory state, and registers the order for WS routing.
// Must be called with sr.mu held.
func (sr *StrategyRunner) recoverDuplicateLevel(ctx context.Context, idx int, linkID string) {
	l := &sr.levels[idx]
	existing, active, err := trader.FetchOrderByLinkId(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol, linkID)
	if err != nil {
		sr.warn(ctx, fmt.Sprintf("L%d recover linkId: %v", l.LevelIdx, err))
		return
	}
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	if existing.OrderStatus == "Filled" {
		// Order was filled before we could record it — treat as a fill event.
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='filled', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW(), filled_at=NOW() WHERE id=$3`,
			existing.OrderId, linkID, l.ID,
		)
		l.Status = LevelFilled
		l.ExchangeOrderID = existing.OrderId
		price, _ := strconv.ParseFloat(existing.Price, 64)
		if price == 0 {
			price = l.TargetPrice
		}
		l.FilledPrice = price
		sr.info(ctx, fmt.Sprintf("L%d восстановлен как исполненный (linkId дубль)", l.LevelIdx))
		return
	}
	if !active {
		// Cancelled or unknown — leave pending so the next reconcile can retry.
		sr.warn(ctx, fmt.Sprintf("L%d linkId дубль, ордер отменён на бирже — ожидание реконсиляции", l.LevelIdx))
		return
	}
	// Order is active on the exchange — mark as placed.
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW() WHERE id=$3`,
		existing.OrderId, linkID, l.ID,
	)
	l.Status = LevelPlaced
	l.ExchangeOrderID = existing.OrderId
	sr.runner.RegisterOrder(existing.OrderId, ref)
	sr.info(ctx, fmt.Sprintf("L%d восстановлен после дублирования linkId (orderId=%s)", l.LevelIdx, existing.OrderId[:8]))
}

// trimExcessLevels cancels and removes levels that exceed the current grid size.
// Used when the strategy is resumed after a settings change reduced GridLevels/Steps:
// the old cycle may have more levels than the current config allows.
// Filled levels are never removed — they represent real position.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) trimExcessLevels(ctx context.Context) {
	sr.mu.Lock()

	expectedPerSide := sr.strategy.GridLevels
	if len(sr.strategy.Steps) > 0 {
		expectedPerSide = len(sr.strategy.Steps)
	}

	sides := sidesForDirection(sr.strategy.Direction)

	type excessItem struct {
		id              string
		exchangeOrderID string
		status          LevelStatus
	}
	var excess []excessItem

	for _, side := range sides {
		var sideIdxs []int
		for i, l := range sr.levels {
			if l.Side == side {
				sideIdxs = append(sideIdxs, i)
			}
		}
		if len(sideIdxs) <= expectedPerSide {
			continue
		}
		for _, i := range sideIdxs[expectedPerSide:] {
			l := sr.levels[i]
			excess = append(excess, excessItem{l.ID, l.ExchangeOrderID, l.Status})
		}
	}

	if len(excess) == 0 {
		sr.mu.Unlock()
		return
	}

	for _, e := range excess {
		if e.status == LevelPlaced && e.exchangeOrderID != "" {
			sr.runner.UnregisterOrder(e.exchangeOrderID)
		}
	}
	sr.mu.Unlock()

	// Cancel placed orders on exchange.
	var cancelItems []trader.BatchCancelItem
	for _, e := range excess {
		if e.status == LevelPlaced && e.exchangeOrderID != "" {
			cancelItems = append(cancelItems, trader.BatchCancelItem{
				Symbol:  sr.strategy.Symbol,
				OrderId: e.exchangeOrderID,
			})
		}
	}
	for start := 0; start < len(cancelItems); start += 20 {
		end := start + 20
		if end > len(cancelItems) {
			end = len(cancelItems)
		}
		if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
			Category: sr.strategy.Category,
			Request:  cancelItems[start:end],
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("trimExcessLevels: %v", err))
		}
	}

	// Mark non-filled excess levels as cancelled in DB.
	removeIDs := make(map[string]bool)
	for _, e := range excess {
		if e.status != LevelFilled {
			removeIDs[e.id] = true
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='cancelled' WHERE id=$1`, e.id)
		}
	}

	// Drop removed levels from in-memory slice.
	sr.mu.Lock()
	defer sr.mu.Unlock()
	n := 0
	for _, l := range sr.levels {
		if !removeIDs[l.ID] {
			sr.levels[n] = l
			n++
		}
	}
	sr.levels = sr.levels[:n]

	sr.info(ctx, fmt.Sprintf("Убрано %d лишних уровней (лимит: %d на сторону)", len(removeIDs), expectedPerSide))
}

// reopenCycleIfPositionOpen looks for the most recent closed cycle that has filled
// levels and checks whether the corresponding position is still open on the exchange.
// If found, it reactivates the cycle (clears ended_at) so loadActiveCycle can pick it
// up and resume TP/SL management without creating duplicate entry orders.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) reopenCycleIfPositionOpen(ctx context.Context) bool {
	var cycleID string
	var cycleNum int
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT sc.id, sc.cycle_num
		 FROM strategy_cycles sc
		 WHERE sc.strategy_id = $1
		   AND sc.ended_at IS NOT NULL
		   AND EXISTS (
		       SELECT 1 FROM strategy_levels sl
		       WHERE sl.cycle_id = sc.id AND sl.status = 'filled'
		   )
		 ORDER BY sc.cycle_num DESC
		 LIMIT 1`,
		sr.strategy.ID,
	).Scan(&cycleID, &cycleNum)
	if err != nil {
		return false
	}

	positions, err := trader.FetchPositions(ctx, sr.runner.creds)
	if err != nil {
		log.Printf("strategy %s: reopenCycleIfPositionOpen fetch positions: %v", sr.strategy.ID, err)
		return false
	}

	wantIdx := positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction)
	positionOpen := false
	for _, p := range positions {
		size, _ := strconv.ParseFloat(p.Size, 64)
		if p.Symbol != sr.strategy.Symbol || size == 0 {
			continue
		}
		if sr.strategy.HedgeMode && p.PositionIdx != wantIdx {
			continue
		}
		positionOpen = true
		break
	}
	if !positionOpen {
		return false
	}

	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_cycles SET ended_at=NULL, result=NULL WHERE id=$1`,
		cycleID,
	); err != nil {
		log.Printf("strategy %s: reopen cycle %d: %v", sr.strategy.ID, cycleNum, err)
		return false
	}
	log.Printf("strategy %s: cycle %d reopened (open position on exchange)", sr.strategy.ID, cycleNum)
	return true
}

// repriceStale checks whether any pending levels have been passed by the market
// (e.g. after a service restart or settings change). If so, it fetches the current
// price and recalculates those levels from the current price using the same step logic
// that was used when the cycle was started.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) repriceStale(ctx context.Context) {
	sr.mu.Lock()
	var hasPending bool
	for _, l := range sr.levels {
		if l.Status == LevelPending && l.TargetPrice > 0 {
			hasPending = true
			break
		}
	}
	sr.mu.Unlock()
	if !hasPending {
		return
	}

	currentPrice, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: repriceStale fetch price: %v", sr.strategy.ID, err)
		return
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	for _, side := range []string{"Buy", "Sell"} {
		var pending []int
		for i, l := range sr.levels {
			if l.Status == LevelPending && l.TargetPrice > 0 && l.Side == side {
				pending = append(pending, i)
			}
		}
		if len(pending) == 0 {
			continue
		}

		stale := false
		for _, i := range pending {
			l := sr.levels[i]
			if (side == "Buy" && l.TargetPrice >= currentPrice) ||
				(side == "Sell" && l.TargetPrice <= currentPrice) {
				stale = true
				break
			}
		}
		if !stale {
			continue
		}

		var newPrices []float64
		if len(sr.strategy.Steps) > 0 {
			prevPrice := currentPrice
			for k := range pending {
				if k >= len(sr.strategy.Steps) {
					break
				}
				step := sr.strategy.Steps[k]
				var p float64
				if side == "Buy" {
					p = prevPrice * (1 - step.PriceMovePct/100)
				} else {
					p = prevPrice * (1 + step.PriceMovePct/100)
				}
				prevPrice = p
				newPrices = append(newPrices, p)
			}
		} else {
			newPrices = calculateGridLevels(currentPrice, sr.strategy.GridStepPct, len(pending), side)
		}

		for k, i := range pending {
			if k >= len(newPrices) {
				break
			}
			l := &sr.levels[i]
			l.TargetPrice = newPrices[k]
			l.Qty = trader.FormatQty(l.SizeUSDT/l.TargetPrice, sr.instr.QtyStep, sr.instr.MinQty)
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET target_price=$1, qty=$2 WHERE id=$3`,
				l.TargetPrice, l.Qty, l.ID,
			)
		}
		sr.info(ctx, fmt.Sprintf("Уровни пересчитаны от %.4f (%d %s)", currentPrice, len(pending), side))
	}
}

// resetCancelledLevels resets non-filled cancelled levels to pending so they can
// be re-placed when a cycle is resumed. Levels become cancelled when the strategy
// is stopped (cancelPlacedLevels marks them in DB but not in memory); on the next
// loadOrStart those DB-cancelled levels must become pending again.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) resetCancelledLevels(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status == LevelCancelled && l.FilledPrice == 0 {
			l.Status = LevelPending
			l.ExchangeOrderID = ""
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, l.ID)
		}
	}
}

// recoverDuplicateTP handles Bybit retCode=110072 (OrderLinkedID is duplicate) for TP orders.
// The TP already exists on the exchange from a previous run — find it by linkId and register
// its orderId so WS fill events are routed correctly.
// Must be called with sr.mu held.
func (sr *StrategyRunner) recoverDuplicateTP(ctx context.Context, linkID string) {
	existing, active, err := trader.FetchOrderByLinkId(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol, linkID)
	if err != nil {
		sr.warn(ctx, fmt.Sprintf("recoverDuplicateTP: %v", err))
		return
	}
	if existing.OrderStatus == "Filled" {
		sr.info(ctx, "TP (linkId дубль) уже исполнен — обрабатывается как TP fill")
		sr.tpOrderID = ""
		sr.cancelPlacedLevels(ctx)
		sr.closeCycle(ctx, "tp")
		sr.maybeRestart(ctx)
		return
	}
	if !active {
		sr.warn(ctx, fmt.Sprintf("TP linkId дубль, ордер отменён на бирже — будет выставлен повторно"))
		return
	}
	sr.tpOrderID = existing.OrderId
	sr.runner.RegisterOrder(existing.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET tp_order_id=$1 WHERE id=$2`, existing.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("TP восстановлен после дублирования linkId (orderId=%s)", existing.OrderId[:8]))
}

// recoverDuplicateSL handles Bybit retCode=110072 for SL orders.
// Must be called with sr.mu held.
func (sr *StrategyRunner) recoverDuplicateSL(ctx context.Context, linkID string) {
	existing, active, err := trader.FetchOrderByLinkId(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol, linkID)
	if err != nil {
		sr.warn(ctx, fmt.Sprintf("recoverDuplicateSL: %v", err))
		return
	}
	if existing.OrderStatus == "Filled" {
		sr.info(ctx, "SL (linkId дубль) уже исполнен — обрабатывается как SL fill")
		sr.slOrderID = ""
		sr.cancelPlacedLevels(ctx)
		sr.closeCycle(ctx, "sl")
		sr.maybeRestart(ctx)
		return
	}
	if !active {
		sr.warn(ctx, fmt.Sprintf("SL linkId дубль, ордер отменён на бирже — будет выставлен повторно"))
		return
	}
	sr.slOrderID = existing.OrderId
	sr.runner.RegisterOrder(existing.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET sl_order_id=$1 WHERE id=$2`, existing.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("SL восстановлен после дублирования linkId (orderId=%s)", existing.OrderId[:8]))
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

// isInsufficientBalance returns true for Bybit retCode=110007 (insufficient available balance).
func isInsufficientBalance(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "retCode=110007")
}

// isMinOrderValue returns true for Bybit retCode=110094 (order value below exchange minimum).
func isMinOrderValue(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "retCode=110094")
}

// isPositionZero returns true for Bybit retCode=110017 (position size is zero).
// This happens when we try to place a reduce-only TP/SL after the position was already
// closed (race: cancel event arrives before the position-size-0 WS event).
func isPositionZero(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "retCode=110017")
}

// isInstrumentUnavailable returns true when the instrument is not currently tradable.
// Covers retCode=110021 (symbol not found) and retCode=10001 (params error for unavailable symbol).
func isInstrumentUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "retCode=110021") || strings.Contains(msg, "retCode=10001")
}

// stopWithMessage transitions the strategy to Stopped status in DB and logs a warning.
// Must be called with sr.mu held.
// stopWithMessage transitions the strategy to Stopped: updates DB, cancels placed
// L-orders, closes the cycle if there is no open position, and logs the reason.
// Must be called with sr.mu held.
func (sr *StrategyRunner) stopWithMessage(ctx context.Context, msg string) {
	sr.strategy.Status = StatusStopped
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategies SET status='stopped' WHERE id=$1`, sr.strategy.ID)
	sr.warn(ctx, msg)
	if sr.cycle == nil {
		return
	}
	sr.cancelPlacedLevels(ctx)
	hasPosition := false
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasPosition = true
			break
		}
	}
	if !hasPosition {
		sr.closeCycle(ctx, "stopped")
	}
}

// handlePartialPositionChange is called when the exchange reports a non-zero position
// size for this strategy's symbol. If the exchange size is smaller than what we track
// from filled levels, part of the position was closed manually — recalculate TP/SL.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handlePartialPositionChange(ctx context.Context, exchangeSize float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.cycle == nil {
		return
	}
	avg, ourQty := sr.avgEntry()
	if ourQty == 0 {
		return
	}
	// Allow small rounding differences (1%).
	if exchangeSize >= ourQty*0.99 {
		// Position grew back or matched — reset override.
		sr.partialCloseQty = 0
		return
	}
	// Guard: Bybit fires a position WS snapshot every time we place/cancel any order.
	// Without this check, each updateTP/updateSL call below would trigger another
	// position event with the same size, creating an infinite loop.
	if sr.partialCloseQty > 0 && math.Abs(exchangeSize-sr.partialCloseQty) < exchangeSize*0.01 {
		return
	}
	sr.partialCloseQty = exchangeSize
	sr.lastTPSLQty = 0 // force re-place with correct qty
	sr.lastTPSLAvg = 0
	sr.warn(ctx, fmt.Sprintf(
		"Позиция частично закрыта вручную (биржа=%.4f, наш учёт=%.4f, avg=%.4f) — пересчитываем TP/SL",
		exchangeSize, ourQty, avg,
	))
	if err := sr.updateTP(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Пересчёт TP после частичного закрытия: %v", err))
	}
	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Пересчёт SL после частичного закрытия: %v", err))
	}
}

// handleTPSLCancelled is called when a TP or SL order belonging to this strategy
// is cancelled on the exchange (manual cancel or exchange action). It warns the user
// and immediately re-places the order.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleTPSLCancelled(ctx context.Context, refType string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.cycle == nil || sr.strategy.Status == StatusStopped {
		return
	}
	// If position is already gone (e.g. closed via UI button), Bybit cancels
	// reduce-only TP/SL automatically. Don't try to re-place with no position.
	_, ourQty := sr.avgEntry()
	if ourQty == 0 {
		return
	}
	switch refType {
	case "tp":
		sr.warn(ctx, "TP ордер был отменён вручную — выставляем повторно")
		sr.tpOrderID = "" // already unregistered by OnOrderEvent
		if err := sr.updateTP(ctx); err != nil {
			if isPositionZero(err) {
				// Race: TP cancel event arrived before position-0 event.
				// Position is already gone — close the cycle now; the incoming
				// position event will see cycle==nil and exit silently.
				sr.closeManualPosition(ctx)
				return
			}
			sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления TP: %v", err))
		}
	case "sl":
		sr.warn(ctx, "SL ордер был отменён вручную — выставляем повторно")
		sr.slOrderID = ""
		if err := sr.updateSL(ctx); err != nil {
			if isPositionZero(err) {
				sr.closeManualPosition(ctx)
				return
			}
			sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления SL: %v", err))
		}
	}
}
