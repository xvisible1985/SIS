package strategy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// StrategyRunner holds the runtime state of one strategy.
type StrategyRunner struct {
	mu       sync.Mutex
	startMu  sync.Mutex // serializes loadOrStart and restartCycle to prevent duplicate placement
	strategy Strategy
	runner   *AccountRunner

	cycle                   *Cycle
	levels                  []GridLevel
	tpOrderID               string
	slOrderID               string
	tpPlaceSeq              int
	slPlaceSeq              int
	instr                   trader.InstrumentInfo
	instrFetchedAt          time.Time // time of last successful GetInstrumentInfo; zero = never
	closedBySelf            bool      // set by handleTPFill/handleSLFill to suppress the WS position-close event
	closedByReason          string    // human-readable reason why we closed/reduced (TP/SL/Matrix TP/…); cleared after use
	unavailableCount        int       // consecutive "instrument unavailable" errors; stops after 5
	repriceGen              int       // increments on each reprice so re-placed levels get fresh linkIds
	partialCloseQty         float64   // actual exchange position qty after manual partial close; 0 = unset
	lastTPSLQty             float64   // qty that TP/SL were last placed for — skip re-place if unchanged
	lastTPSLAvg             float64   // avg entry that TP/SL were last placed for
	lastTPPrice             float64   // TP price at last placement — changes when tp_pct changes
	lastSLPrice             float64   // SL trigger price at last placement
	positionModeVerified    bool      // true after ensurePositionMode succeeded for current HedgeMode value
	lastVerifiedHedge       bool      // HedgeMode value at the time positionModeVerified was set
	gridChangedWhileStopped bool      // restartCycle called while stopped; apply on next loadOrStart
	signalSubID             string    // non-empty while waiting for signal before starting a new cycle
	signalMonitorID         string    // non-empty while continuously monitoring signal during active cycle
	currentSignalState      string    // last observed signal state: "buy","sell","neutral","" (no filter)

	// Matrix strategy runtime state
	matrixMonitorStop   context.CancelFunc
	matrixSLSeq         int             // increments on each per-level SL placement for unique linkIds
	matrixWaitingSlots  map[int]float64 // positive slot → SL trigger price when SL'd and waiting to re-enter
	matrixLastSLSlot    int             // slot number of the most recently SL'd level (for SZ display)
	lastMatrixPrice     float64         // last mark price seen by matrixPriceTick; used to re-trigger virtual levels after a fill

	lastVirtualPrice float64 // last mark price seen by gridVirtualPriceTick; 0 = not yet seen

	// tpCancelStreak counts consecutive external TP cancellations (Bybit-side cancels) without
	// a successful TP fill in between. A high streak indicates either:
	//   a) a race: WS cancel arrives before Bybit's reduce-only budget is freed (REST lag), or
	//   b) position is zero (TP fill WS event was dropped) and every new TP is immediately cancelled.
	// Reset to 0 on TP fill or cycle close.
	tpCancelStreak int

	// pendingDelete is set (under sr.mu) before the delete goroutine fires so that
	// a concurrent Notify cannot submit a loadOrStart and restart the strategy.
	pendingDelete bool

	// Per-strategy worker goroutine — serializes all tasks and provides panic isolation.
	taskCh       chan func(context.Context) // buffered task queue (capacity 64)
	workerCancel context.CancelFunc         // cancels the worker when the strategy is removed

	// Worker metrics — written and read atomically (no lock required).
	workerRunning int32 // 1 while the worker is executing a task, 0 when idle
	panicCount    int32 // cumulative panics recovered by the worker
	tasksDropped  int32 // tasks rejected because taskCh was full
	lastTaskNano  int64 // unix nanoseconds at start of last task; 0 = never
}

// effectiveDeposit returns the USDT deposit to use for slot sizing.
// When SizeAsMain is enabled, it uses the opposite-direction main position's
// current USDT value (coins × currentPrice). Falls back to GridSizeUSDT if
// the main position size is unavailable or currentPrice is invalid.
func (sr *StrategyRunner) effectiveDeposit(currentPrice float64) float64 {
	if !sr.strategy.SizeAsMain || currentPrice <= 0 {
		return sr.strategy.GridSizeUSDT
	}
	// The hedge strategy is COUNTER to the main position:
	//   hedge long → main is short (positionIdx = 2)
	//   hedge short → main is long  (positionIdx = 1)
	var mainPosIdx int
	if sr.strategy.Direction == DirectionLong {
		mainPosIdx = 2
	} else {
		mainPosIdx = 1
	}
	sizeCoins := sr.runner.GetPositionSizeCoins(sr.strategy.Symbol, mainPosIdx)
	if sizeCoins <= 0 {
		return sr.strategy.GridSizeUSDT
	}
	return sizeCoins * currentPrice
}

// startWorker launches the per-strategy background goroutine.
func (sr *StrategyRunner) startWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	sr.workerCancel = cancel
	go sr.runWorker(ctx)
}

// stopWorker signals the worker to exit after its current task completes.
func (sr *StrategyRunner) stopWorker() {
	if sr.workerCancel != nil {
		sr.workerCancel()
	}
}

// runWorker is the serial task processor for this strategy.
// Each task gets a fresh 30-second context; panics are logged and recovered.
func (sr *StrategyRunner) runWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-sr.taskCh:
			if !ok {
				return
			}
			atomic.StoreInt32(&sr.workerRunning, 1)
			atomic.StoreInt64(&sr.lastTaskNano, time.Now().UnixNano())
			func() {
				defer func() {
					if r := recover(); r != nil {
						atomic.AddInt32(&sr.panicCount, 1)
						log.Printf("strategy worker %s: panic: %v\n%s", sr.strategy.ID, r, debug.Stack())
					}
				}()
				taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				task(taskCtx)
			}()
			atomic.StoreInt32(&sr.workerRunning, 0)
		}
	}
}

// submit enqueues a task on the worker. Returns false (and logs) if the queue is full.
func (sr *StrategyRunner) submit(task func(context.Context)) bool {
	select {
	case sr.taskCh <- task:
		return true
	default:
		atomic.AddInt32(&sr.tasksDropped, 1)
		log.Printf("strategy worker %s: task queue full — task dropped", sr.strategy.ID)
		return false
	}
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
		sr.cancelAllStrategyOrders(ctx)
		sr.mu.Lock()
		sf := sr.strategy.SignalFilter
		configs := sr.strategy.SignalConfigs
		sr.mu.Unlock()
		if sf && len(configs) > 0 && sr.runner.signalEngine != nil {
			sr.awaitSignal(ctx)
			return
		}
		sr.startMu.Lock()
		err2 := sr.startCycleByType(ctx)
		sr.startMu.Unlock()
		if err2 != nil {
			log.Printf("strategy %s: start cycle: %v", sr.strategy.ID, err2)
		}
		return
	}

	if sr.strategy.StrategyType == "matrix" {
		sr.resumeMatrixCycle(ctx)
	} else {
		sr.resumeGridCycle(ctx)
	}
}

// resumeMatrixCycle handles matrix-specific cycle resume after a successful loadActiveCycle.
// Restores waiting-slot state, triggers any pending virtual entry, re-places cancelled DCA levels,
// and launches the price monitor.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) resumeMatrixCycle(ctx context.Context) {
	sr.mu.Lock()
	sr.restoreMatrixWaitingSlots()
	sr.mu.Unlock()

	resumePrice, _ := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)

	sr.mu.Lock()

	if resumePrice > 0 {
		// Step 1: Reset cancelled levels back to pending, but only those whose slot
		// has no newer active (pending/placed/filled) row. When a mid-rebuild server
		// restart occurs, both the old pre-rebuild levels (cancelled) and the new
		// post-rebuild levels (pending/placed) exist in DB for the same slot. Without
		// this guard, resetting ALL cancelled levels would resurrect the old stale
		// levels alongside the newly placed ones, causing two pending rows for the
		// same slot at different prices.
		//
		// cancelPlacedLevels sets DB status='cancelled' for BOTH 'placed' AND 'pending'
		// rows. After loadActiveCycle reloads from DB, all unexecuted levels are
		// 'cancelled' in-memory — including virtual levels that were never placed on
		// the exchange. matrixPriceTick and the virtual-trigger loop below both check
		// l.Status == LevelPending, so they would silently skip 'cancelled' levels.
		// Resetting here makes them eligible again, guarded by slot deduplication.
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
		for i := range sr.levels {
			l := &sr.levels[i]
			if l.Status != LevelCancelled || l.Slot == nil {
				continue
			}
			if activeSlots[*l.Slot] {
				continue // newer active row exists for this slot — keep cancelled
			}
			l.Status = LevelPending
			l.ExchangeOrderID = ""
			l.ExchangeLinkID = ""
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, l.ID)
		}

		// Step 2: If settings changed while stopped, recalculate prices/quantities.
		if sr.gridChangedWhileStopped {
			sr.gridChangedWhileStopped = false
			basePrice := resumePrice
			if sr.cycle != nil && sr.cycle.StartPrice > 0 {
				basePrice = sr.cycle.StartPrice
			}
			sr.applyNewMatrixPrices(ctx, basePrice, resumePrice)
		}

		// Step 3: Place or trigger every pending level.
		//   • Non-virtual → place limit/stop order on exchange.
		//   • Virtual slot 0 → trigger market entry immediately.
		//   • Other virtual slots → price monitor fires them when price crosses target.
		// Bump repriceGen so freshly-generated linkIds differ from the ones used by
		// the previously-cancelled orders.  Without this bump Bybit returns 110072
		// (duplicate linkId) and recoverDuplicateLevel silently skips the level
		// because the old order is already cancelled on the exchange.
		sr.repriceGen++
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
				if err := sr.placeMatrixLevel(ctx, l, resumePrice); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Ошибка выставления %s при возобновлении: %v", slotLabel(l.Slot), err))
				}
			}
		}
		// Seed lastMatrixPrice so the first level fill immediately triggers virtual
		// levels via matrixPriceTick instead of waiting up to minutes for the next tick.
		sr.lastMatrixPrice = resumePrice
	}

	sr.mu.Unlock()
	sr.launchMatrixPriceMonitor()
}

// resumeGridCycle handles grid/bot-specific cycle resume after a successful loadActiveCycle.
// Resets cancelled levels, detects config changes, reconciles exchange state, and re-places
// pending orders.
// Must NOT be called with sr.mu or startMu held.
func (sr *StrategyRunner) resumeGridCycle(ctx context.Context) {
	// If signal filter is active and currently neutral, re-cancel any cancelled levels
	// (leaving them cancelled) and launch the signal monitor instead of re-placing them.
	// This prevents a 0%-step market order from firing on resume when signal is neutral.
	sr.mu.Lock()
	sf := sr.strategy.SignalFilter
	sigConfigs := sr.strategy.SignalConfigs
	sr.mu.Unlock()
	if sf && len(sigConfigs) > 0 && sr.runner.signalEngine != nil {
		tf := "1h"
		if len(sigConfigs) > 0 {
			if v, ok := sigConfigs[0].Params["tf"]; ok {
				if s, ok2 := v.(string); ok2 && s != "" {
					tf = s
				}
			}
		}
		goCfgs := make([]signal.Config, len(sigConfigs))
		for i, sc := range sigConfigs {
			goCfgs[i] = signal.Config{Name: sc.Name, Params: sc.Params}
		}
		sr.mu.Lock()
		dir := sr.strategy.Direction
		sr.mu.Unlock()
		curState, known := sr.runner.signalEngine.QueryState(sr.strategy.Symbol, tf, goCfgs)
		if known {
			var want signal.State
			switch dir {
			case DirectionLong:
				want = signal.Buy
			case DirectionShort:
				want = signal.Sell
			default:
				if curState != signal.Neutral {
					want = curState
				}
			}
			if curState != want {
				// Signal is neutral — keep cancelled levels as-is, subscribe to monitor.
				sr.launchSignalMonitor(ctx)
				return
			}
		}
	}

	// Levels that were cancelled by a previous stop (cancelPlacedLevels sets
	// status='cancelled' in DB but leaves in-memory Status unchanged) must be
	// reset to pending so placeNextLevels can re-place them on resume.
	sr.resetCancelledLevels(ctx)

	// Detect grid-parameter changes made while the strategy was stopped or the server
	// was offline (e.g. GridStepPct edited via API): compare stored level prices against
	// what calculateGridLevels would produce with current settings.
	sr.mu.Lock()
	needsRestart := sr.gridChangedWhileStopped
	var gridMismatchReason string
	if !needsRestart && sr.cycle != nil && len(sr.levels) > 0 {
		hasPending := false
		for _, l := range sr.levels {
			if l.Status == LevelPending || l.Status == LevelPlaced {
				hasPending = true
				break
			}
		}
		if hasPending {
			side := "Buy"
			if sr.strategy.Direction == DirectionShort {
				side = "Sell"
			}
			expected := calculateGridLevels(sr.cycle.StartPrice, sr.strategy.GridStepPct, sr.strategy.GridLevels, side)
			if len(sr.levels) != sr.strategy.GridLevels {
				gridMismatchReason = fmt.Sprintf("кол-во уровней %d→%d", len(sr.levels), sr.strategy.GridLevels)
			} else {
				for _, l := range sr.levels {
					idx := l.LevelIdx - 1
					if idx < 0 || idx >= len(expected) {
						gridMismatchReason = fmt.Sprintf("индекс уровня L%d вне диапазона", l.LevelIdx)
						break
					}
					if math.Abs(l.TargetPrice-expected[idx])/l.TargetPrice > 0.0001 {
						gridMismatchReason = fmt.Sprintf("цена L%d: %.4f→%.4f", l.LevelIdx, l.TargetPrice, expected[idx])
						break
					}
				}
			}
			if gridMismatchReason != "" {
				needsRestart = true
			}
		}
	}
	if needsRestart {
		sr.gridChangedWhileStopped = false
	}
	sr.mu.Unlock()
	if needsRestart {
		if gridMismatchReason != "" {
			sr.info(ctx, fmt.Sprintf("Настройки сетки изменились (%s) — перезапускаю цикл", gridMismatchReason))
		}
		sr.restartCycle(ctx)
		return
	}

	// Check if filled levels still have a real open position on exchange.
	// Handles the case where the position was closed while the service was down.
	if sr.checkPositionGone(ctx) {
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

	// Cancel exchange orders not tracked in our orderIndex (orphans from missed events).
	sr.sweepOrphanOrders(ctx)
	// Reprice any pending levels that the market has already passed.
	sr.repriceStale(ctx)
	// Cancel and remove levels that exceed the current grid size setting.
	sr.trimExcessLevels(ctx)

	// Hold startMu so restartCycle doesn't run concurrently with level placement.
	sr.startMu.Lock()
	defer sr.startMu.Unlock()

	// Re-verify cycle is still open before placing levels.
	// Guards against race: restartCycle may have closed the cycle between
	// loadActiveCycle and here.
	sr.mu.Lock()
	if sr.cycle == nil {
		// Cycle was closed concurrently (e.g. restartCycle ran); it will handle placement.
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
	if err := sr.updateTP(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка восстановления TP: %v", err))
	}
	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка восстановления SL: %v", err))
	}
	hasVirtual := false
	for _, l := range sr.levels {
		if l.ForceVirtual && l.Status == LevelPending {
			hasVirtual = true
			break
		}
	}
	if hasVirtual {
		go sr.launchGridVirtualMonitor()
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

	// Also load minQty for dust detection (best-effort; zero means unknown).
	sr.mu.Lock()
	minQty := sr.instr.MinQty
	sr.mu.Unlock()

	var dustSize float64 // non-zero if a sub-minQty fragment was found
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
		// If minQty is known and the position is smaller than one lot, treat it as
		// dust: attempt a market close, then clean up the cycle as if gone.
		if minQty > 0 && size < minQty {
			dustSize = size
			break
		}
		positionOpen = true
		break
	}
	if positionOpen {
		return false
	}

	// Dust position: close it with a market order using the exact size string so
	// Bybit accepts it as a full reduce-only close even though size < minQty.
	if dustSize > 0 {
		closeSide := "Sell"
		if sr.strategy.Direction == DirectionShort {
			closeSide = "Buy"
		}
		dustQty := strconv.FormatFloat(dustSize, 'f', -1, 64)
		_, closeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        closeSide,
			OrderType:   "Market",
			Qty:         dustQty,
			ReduceOnly:  !sr.strategy.HedgeMode,
			PositionIdx: wantIdx,
		})
		if closeErr != nil {
			log.Printf("strategy %s: dust close (%.8f %s): %v", sr.strategy.ID, dustSize, sr.strategy.Symbol, closeErr)
		} else {
			log.Printf("strategy %s: dust position %.8f %s закрыта маркетом", sr.strategy.ID, dustSize, sr.strategy.Symbol)
		}
	}

	// Position is gone (or was dust) — cancel TP, mark filled levels as cancelled, close cycle.
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.warn(ctx, fmt.Sprintf("Позиция не найдена на бирже при запуске, цикл %d очищен", sr.cycle.CycleNum))

	// Capture entry data BEFORE cancelling filled levels: avgEntry() reads LevelFilled status
	// and closeCycle() nils out sr.cycle/sr.levels — all must be read before both calls.
	avg, posQty := sr.avgEntry()
	closeSnap := externalCloseSnap{
		strategyID:     sr.strategy.ID,
		stratID8:       sr.strategy.ID[:8],
		botID:          sr.strategy.BotID,
		ownerID:        sr.strategy.OwnerID,
		accountID:      sr.strategy.AccountID,
		symbol:         sr.strategy.Symbol,
		category:       sr.strategy.Category,
		direction:      string(sr.strategy.Direction),
		cycleNum:       sr.cycle.CycleNum,
		cycleID:        sr.cycle.ID,
		cycleStartedAt: sr.cycle.StartedAt,
		avgEntry:       avg,
		posQty:         posQty,
		creds:          sr.runner.creds,
	}

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

	// Write trade_history for the cycle that was silently closed while the server was down.
	// resolveExternalClose queries trader_executions / Bybit API to find the real exit price
	// and result (TP/SL/manual). Without this call the cycle would be lost from history.
	if posQty > 0 {
		log.Printf("strategy %s: цикл %d — позиция закрылась пока сервер был выключен, определяем результат...",
			closeSnap.stratID8, closeSnap.cycleNum)
		go resolveExternalClose(sr.runner.pool, closeSnap)
	}
	return true
}

// closeDustPosition attempts to close a sub-minQty position fragment that may have been
// left by a previous cycle. Called at the start of a new cycle before placing any orders,
// so that SetLeverage / ensurePositionMode don't fail due to a residual open position.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) closeDustPosition(ctx context.Context) {
	sr.mu.Lock()
	symbol := sr.strategy.Symbol
	category := sr.strategy.Category
	direction := sr.strategy.Direction
	hedgeMode := sr.strategy.HedgeMode
	minQty := sr.instr.MinQty
	sr.mu.Unlock()

	if minQty == 0 {
		return // instrument not loaded yet; skip
	}

	positions, err := trader.FetchPositions(ctx, sr.runner.creds)
	if err != nil {
		return
	}

	wantIdx := positionIdxForClose(hedgeMode, direction)
	for _, p := range positions {
		if p.Symbol != symbol {
			continue
		}
		if hedgeMode && p.PositionIdx != wantIdx {
			continue
		}
		size, _ := strconv.ParseFloat(p.Size, 64)
		if size <= 0 || size >= minQty {
			continue
		}
		// Dust found — attempt market close with exact size.
		closeSide := "Sell"
		if direction == DirectionShort {
			closeSide = "Buy"
		}
		dustQty := strconv.FormatFloat(size, 'f', -1, 64)
		_, closeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:      symbol,
			Category:    category,
			Side:        closeSide,
			OrderType:   "Market",
			Qty:         dustQty,
			ReduceOnly:  !hedgeMode,
			PositionIdx: wantIdx,
		})
		if closeErr != nil {
			log.Printf("strategy %s: closeDust(%.8f %s): %v", sr.strategy.ID, size, symbol, closeErr)
		} else {
			log.Printf("strategy %s: dust %.8f %s закрыта перед новым циклом", sr.strategy.ID, size, symbol)
		}
		break // only one slot per direction
	}
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

	// Fetch mark price and open orders concurrently.
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
		o, e := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
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
				sr.closePositionAtMarket(ctx, "tp", tpPrice)
				sr.mu.Unlock()
				return true
			}
		}
	} else if sr.tpOrderID != "" {
		// TP confirmed on exchange — populate lastTPPrice with the ACTUAL order price so
		// updateTP can detect if TPPct was changed while the server was down.
		for _, o := range openOrders {
			if o.OrderId == sr.tpOrderID {
				if p, _ := strconv.ParseFloat(o.Price, 64); p > 0 {
					sr.lastTPPrice = p
				}
				break
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
		if avg > 0 && totalQty > 0 && sr.strategy.SLPct < 0 {
			slSide, slTrigger, _ := slParams(sr.strategy.Direction, avg, sr.strategy.SLPct)
			passed := (slSide == "Sell" && price <= slTrigger) ||
				(slSide == "Buy" && price >= slTrigger)
			if passed {
				sr.info(ctx, fmt.Sprintf("reconcile: цена %.4f прошла SL %.4f, закрываю позицию маркетом", price, slTrigger))
				sr.closePositionAtMarket(ctx, "sl", slTrigger)
				sr.mu.Unlock()
				return true
			}
		}
	} else if sr.slOrderID != "" {
		// SL confirmed on exchange — populate lastSLPrice from actual trigger price.
		for _, o := range openOrders {
			if o.OrderId == sr.slOrderID {
				if p, _ := strconv.ParseFloat(o.TriggerPrice, 64); p > 0 {
					sr.lastSLPrice = p
				}
				break
			}
		}
	}

	// --- Orphaned SL cleanup (grid only) ---
	// If updateSL's CancelOrder failed, the old stop order may still be on the exchange
	// alongside the new one. Detect any stop orders whose linkId matches our SL prefix
	// but doesn't match the current slOrderID and schedule them for cancellation.
	var orphanedSLs []trader.Order
	if sr.strategy.StrategyType != "matrix" {
		slPrefix := fmt.Sprintf("SIS_STR-%s-sl-", sr.strategy.ID[:8])
		for _, o := range openOrders {
			if strings.HasPrefix(o.OrderLinkId, slPrefix) && o.OrderId != sr.slOrderID {
				orphanedSLs = append(orphanedSLs, o)
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
		sr.mu.Unlock()
		sr.cancelOrphanedSLs(ctx, orphanedSLs)
		return len(orphanedSLs) > 0
	}

	// Snapshot data needed for REST calls, then release the lock.
	// REST calls must never be made while sr.mu is held — they can block for
	// tens of seconds and would stall WS event processing for this strategy.
	type passedSnap struct {
		l      *GridLevel
		linkID string
	}
	snaps := make([]passedSnap, 0, len(passedLevels))
	for _, l := range passedLevels {
		linkID := l.ExchangeLinkID
		if linkID == "" {
			linkID = fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
		}
		snaps = append(snaps, passedSnap{l: l, linkID: linkID})
	}
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	sr.cancelOrphanedSLs(ctx, orphanedSLs)

	// Check order history for each passed level — no lock held during REST calls.
	var stillMissing []*GridLevel
	for _, s := range snaps {
		existing, _, err := trader.FetchOrderByLinkId(ctx, sr.runner.creds, category, symbol, s.linkID)
		if err == nil && existing.OrderStatus == "Filled" {
			filledPrice, _ := strconv.ParseFloat(existing.Price, 64)
			if filledPrice == 0 {
				filledPrice = price
			}
			sr.mu.Lock()
			s.l.Status = LevelFilled
			s.l.FilledPrice = filledPrice
			s.l.ExchangeOrderID = existing.OrderId
			sr.mu.Unlock()
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW(), exchange_order_id=$2 WHERE id=$3`,
				filledPrice, existing.OrderId, s.l.ID)
			sr.info(ctx, fmt.Sprintf("reconcile: L%d подхвачен из истории, исполнен @ %.4f", s.l.LevelIdx, filledPrice))
		} else {
			stillMissing = append(stillMissing, s.l)
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

	// Reset non-nearest under lock, then snapshot data for PlaceOrder.
	sr.mu.Lock()
	for _, l := range stillMissing {
		if l == nearest {
			continue
		}
		l.Status = LevelPending
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, l.ID)
	}
	nearestSide := nearest.Side
	nearestQty := nearest.Qty
	nearestLevelIdx := nearest.LevelIdx
	nearestID := nearest.ID
	posIdx := positionIdxForOpen(sr.strategy.HedgeMode, nearest.Side)
	sr.mu.Unlock()

	// Place market order — no lock held.
	sr.info(ctx, fmt.Sprintf("reconcile: L%d цена прошла уровень, исполняю маркетом", nearestLevelIdx))
	result, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      symbol,
		Category:    category,
		Side:        nearestSide,
		OrderType:   "Market",
		Qty:         nearestQty,
		PositionIdx: posIdx,
	})
	sr.mu.Lock()
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("reconcile: market fill L%d: %v", nearestLevelIdx, err))
		nearest.Status = LevelPending
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, nearestID)
	} else {
		nearest.Status = LevelFilled
		nearest.FilledPrice = price
		nearest.ExchangeOrderID = result.OrderId
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW(), exchange_order_id=$2 WHERE id=$3`,
			price, result.OrderId, nearestID)
		sr.info(ctx, fmt.Sprintf("reconcile: L%d исполнен @ %.4f", nearestLevelIdx, price))
	}
	sr.mu.Unlock()
	return false
}

// sweepOrphanOrders cancels open exchange orders that belong to this cycle but
// are not tracked in our orderIndex (orphans), and resets placed levels that
// are tracked but missing from the exchange (ghosts from missed events).
// Also cancels orders from PREVIOUS cycles that were left behind by incomplete
// closeCycle sweeps (server restarted before async sweep finished).
func (sr *StrategyRunner) sweepOrphanOrders(ctx context.Context) {
	sr.mu.Lock()
	if sr.cycle == nil {
		sr.mu.Unlock()
		return
	}
	stratID8 := sr.strategy.ID
	if len(stratID8) > 8 {
		stratID8 = stratID8[:8]
	}
	prefix := fmt.Sprintf("SIS_STR-%s-%d-", stratID8, sr.cycle.CycleNum)
	widePrefix := "SIS_STR-" + stratID8 + "-"
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	openOrders, err := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, category, symbol)
	if err != nil {
		log.Printf("strategy %s: sweep orphans fetch: %v", stratID8, err)
		return
	}

	live := make(map[string]bool, len(openOrders)*2)
	for _, o := range openOrders {
		live[o.OrderId] = true
		live[o.OrderLinkId] = true
	}

	sr.runner.mu.RLock()
	known := make(map[string]bool, len(sr.runner.orderIndex))
	for id := range sr.runner.orderIndex {
		known[id] = true
	}
	sr.runner.mu.RUnlock()

	// Cancel orphan orders on exchange (not in our index)
	cancelled := 0
	for _, o := range openOrders {
		if !strings.HasPrefix(o.OrderLinkId, prefix) {
			continue
		}
		if known[o.OrderId] || known[o.OrderLinkId] {
			continue
		}
		orderFilter := ""
		if o.OrderFilter == "StopOrder" {
			orderFilter = "StopOrder"
		}
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      symbol,
			Category:    category,
			OrderId:     o.OrderId,
			OrderFilter: orderFilter,
		}); err != nil && !isOrderGone(err) {
			log.Printf("strategy %s: sweep orphan cancel %s: %v", stratID8, o.OrderId, err)
		} else {
			cancelled++
		}
	}

	// Cancel orders from PREVIOUS cycles that were left behind.
	// Guard: TP/SL linkIds use "SIS_STR-{id8}-tp/sl-{cyc}-{seq}" — they match widePrefix
	// but NOT prefix (which is "SIS_STR-{id8}-{cyc}-"). Without the known check we would
	// cancel the CURRENT cycle's TP/SL orders here, triggering a false handleTPSLCancelled.
	for _, o := range openOrders {
		if !strings.HasPrefix(o.OrderLinkId, widePrefix) {
			continue
		}
		// Skip current cycle — already handled above
		if strings.HasPrefix(o.OrderLinkId, prefix) {
			continue
		}
		// Skip orders tracked in our index (guards current-cycle TP/SL)
		if known[o.OrderId] || known[o.OrderLinkId] {
			continue
		}
		orderFilter := ""
		if o.OrderFilter == "StopOrder" {
			orderFilter = "StopOrder"
		}
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      symbol,
			Category:    category,
			OrderId:     o.OrderId,
			OrderFilter: orderFilter,
		}); err != nil && !isOrderGone(err) {
			log.Printf("strategy %s: sweep old-cycle cancel %s: %v", stratID8, o.OrderId, err)
		} else {
			cancelled++
		}
	}

	if cancelled > 0 {
		sr.info(ctx, fmt.Sprintf("Отменено %d потерянных ордеров при загрузке", cancelled))
	}

	// Reset ghost levels that are in our index but missing from exchange
	sr.mu.Lock()
	var resetCount int
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPlaced || l.ExchangeOrderID == "" {
			continue
		}
		if live[l.ExchangeOrderID] || live[l.ExchangeLinkID] {
			continue
		}
		oldOrderID := l.ExchangeOrderID
		oldLinkID := l.ExchangeLinkID
		l.Status = LevelPending
		l.ExchangeOrderID = ""
		l.ExchangeLinkID = ""
		sr.runner.UnregisterOrder(oldOrderID)
		sr.runner.UnregisterOrder(oldLinkID)
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, exchange_link_id=NULL WHERE id=$1`, l.ID)
		sr.info(ctx, fmt.Sprintf("Уровень L%d не найден на бирже при загрузке — сброшен в ожидание", l.LevelIdx))
		resetCount++
	}
	sr.mu.Unlock()
}

// cancelAllStrategyOrders cancels every open exchange order for this strategy,
// regardless of cycle number. Used before starting a fresh cycle so no stale
// orders from previous cycles remain on the exchange.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) cancelAllStrategyOrders(ctx context.Context) {
	sr.mu.Lock()
	stratID8 := sr.strategy.ID
	if len(stratID8) > 8 {
		stratID8 = stratID8[:8]
	}
	prefix := "SIS_STR-" + stratID8 + "-"
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	openOrders, err := trader.FetchOpenOrdersForSymbolAll(ctx, sr.runner.creds, category, symbol)
	if err != nil {
		log.Printf("strategy %s: cancelAll fetch: %v", stratID8, err)
		return
	}

	cancelled := 0
	for _, o := range openOrders {
		if !strings.HasPrefix(o.OrderLinkId, prefix) {
			continue
		}
		orderFilter := ""
		if o.OrderFilter == "StopOrder" {
			orderFilter = "StopOrder"
		}
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      symbol,
			Category:    category,
			OrderId:     o.OrderId,
			OrderFilter: orderFilter,
		}); err != nil && !isOrderGone(err) {
			log.Printf("strategy %s: cancelAll %s: %v", stratID8, o.OrderId, err)
		} else {
			cancelled++
		}
	}
	if cancelled > 0 {
		sr.info(ctx, fmt.Sprintf("Отменено %d старых ордеров перед запуском нового цикла", cancelled))
	}
}

// closePositionAtMarket places a market close order for the full position and closes the cycle.
// approxExitPrice is used to record the trade in trade_history (mark price or trigger price);
// pass 0 to skip the trade record.
// Must be called with sr.mu held.
func (sr *StrategyRunner) closePositionAtMarket(ctx context.Context, reason string, approxExitPrice float64) {
	avg, totalQty := sr.avgEntry()
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
	// Write trade record before closeCycle zeros sr.cycle.
	if approxExitPrice > 0 && avg > 0 && (reason == "tp" || reason == "sl") {
		pnl := (approxExitPrice - avg) * totalQty
		if sr.strategy.Direction == DirectionShort {
			pnl = (avg - approxExitPrice) * totalQty
		}
		pnlPct := pnl / (avg * totalQty) * 100
		sr.writeTrade(ctx, reason, avg, approxExitPrice, totalQty, pnl, pnlPct)
	}
	sr.closedBySelf = true
	sr.closedByReason = reason
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, reason)
	sr.maybeRestart(ctx)
}

// closeGhostPosition closes a position that exists on the exchange but is not tracked
// by any filled level (e.g. a remnant after a partial TP due to qty discrepancy).
// Uses exchangeSize directly instead of avgEntry(). Must be called with sr.mu held.
func (sr *StrategyRunner) closeGhostPosition(ctx context.Context, exchangeSize float64) {
	closeSide := "Sell"
	if sr.strategy.Direction == DirectionShort {
		closeSide = "Buy"
	}
	qty := trader.FormatQty(exchangeSize, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "" || qty == "0" {
		return
	}
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
		sr.errlog(ctx, fmt.Sprintf("closeGhostPosition: %v", err))
		return
	}
	sr.closedBySelf = true
	sr.closedByReason = "ghost_close"
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "ghost_close")
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
		        COALESCE(exchange_order_id,''), COALESCE(filled_price,0), COALESCE(exchange_link_id,''),
		        COALESCE(sl_order_id,''), COALESCE(sl_price,0), COALESCE(sl_replaced,false), slot, COALESCE(force_virtual,false)
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
		var slotVal *int16
		if err := rows.Scan(&l.ID, &l.LevelIdx, &l.Side, &l.TargetPrice, &l.SizeUSDT,
			&l.Qty, &stat, &l.ExchangeOrderID, &l.FilledPrice, &l.ExchangeLinkID,
			&l.SLOrderID, &l.SLPrice, &l.SLReplaced, &slotVal, &l.ForceVirtual); err != nil {
			continue
		}
		if slotVal != nil {
			s := int(*slotVal)
			l.Slot = &s
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
		if l.Status == LevelPlaced && l.ExchangeLinkID != "" {
			sr.runner.RegisterOrder(l.ExchangeLinkID, orderRef{
				strategyID: sr.strategy.ID,
				levelID:    l.ID,
				refType:    "level",
			})
		}
		// Restore repriceGen from the saved linkId so we don't reuse a number
		// Bybit has already seen (avoids duplicate linkId 110072 after restart).
		if l.ExchangeLinkID != "" {
			parts := strings.Split(l.ExchangeLinkID, "-")
			if len(parts) >= 5 {
				if gen, err := strconv.Atoi(parts[len(parts)-1]); err == nil && gen > sr.repriceGen {
					sr.repriceGen = gen
				}
			}
		}
		// Register per-level matrix SL orders on service restart.
		// sl_closed levels already had their SL order fire — clear the ID in memory
		// so the active-stop counter in matrixPlacePerLevelSL doesn't count dead orders.
		if l.SLOrderID != "" {
			if l.Status == LevelSLClosed {
				l.SLOrderID = "" // order no longer exists on exchange
			} else {
				sr.runner.RegisterOrder(l.SLOrderID, orderRef{
					strategyID: sr.strategy.ID,
					levelID:    l.ID,
					refType:    "matrix_sl",
				})
			}
		}
	}
	if sr.tpOrderID != "" {
		sr.runner.RegisterOrder(sr.tpOrderID, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
	}
	if sr.slOrderID != "" {
		sr.runner.RegisterOrder(sr.slOrderID, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	}
	// Restore lastTPSL* so updateTP/updateSL deduplication works correctly after
	// service restart. Without this the first call after reload always re-places
	// TP/SL because lastTPSLQty==0 ≠ actualQty, causing unnecessary replacements
	// and potential duplicates if the cancel fails mid-flight.
	if sr.tpOrderID != "" || sr.slOrderID != "" {
		avg, totalQty := sr.avgEntry()
		if totalQty > 0 && avg > 0 {
			sr.lastTPSLQty = totalQty
			sr.lastTPSLAvg = avg
			if sr.tpOrderID != "" && sr.strategy.TPPct > 0 {
				_, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
				sr.lastTPPrice = tpPrice
			}
			if sr.slOrderID != "" && sr.strategy.SLPct < 0 {
				// Use projected avg (same logic as updateSL) so lastSLPrice stays
				// consistent with what updateSL would compute on the next call.
				slAvg := sr.projectedAvgEntry()
				if slAvg == 0 {
					slAvg = avg
				}
				if sr.strategy.Direction == DirectionLong && slAvg > avg {
					slAvg = avg
				} else if sr.strategy.Direction == DirectionShort && slAvg < avg {
					slAvg = avg
				}
				_, slTrigger, _ := slParams(sr.strategy.Direction, slAvg, sr.strategy.SLPct)
				sr.lastSLPrice = slTrigger
			}
		}
	}
	sr.info(ctx, fmt.Sprintf("Цикл %d возобновлён, уровней: %d", c.CycleNum, len(sr.levels)))
	return nil
}

// ensurePositionMode switches the exchange position mode for this symbol to match
// the strategy's HedgeMode setting (one-way=0 or hedge=3). Returns false and logs
// if the switch fails with a transient error.
// Must NOT be called with sr.mu held — makes a blocking REST call.
// Skips the REST call if the mode was already verified for the current HedgeMode value.
func (sr *StrategyRunner) ensurePositionMode(ctx context.Context) bool {
	sr.mu.Lock()
	alreadyOK := sr.positionModeVerified && sr.lastVerifiedHedge == sr.strategy.HedgeMode
	hedgeMode := sr.strategy.HedgeMode
	category := sr.strategy.Category
	symbol := sr.strategy.Symbol
	sr.mu.Unlock()

	if alreadyOK {
		return true
	}
	mode := 0 // one-way (default)
	if hedgeMode {
		mode = 3 // both-side / hedge
	}
	if err := trader.SwitchPositionMode(ctx, sr.runner.creds, category, symbol, mode); err != nil {
		if errors.Is(err, trader.ErrPositionModeUnsupported) {
			// Dated futures / some symbols don't support mode switching — proceed in
			// the exchange default mode and cache so we never retry.
			sr.info(ctx, fmt.Sprintf("Position mode switch недоступен для %s — используется режим биржи по умолчанию", symbol))
			sr.mu.Lock()
			sr.positionModeVerified = true
			sr.lastVerifiedHedge = hedgeMode
			sr.mu.Unlock()
			return true
		}
		sr.errlog(ctx, fmt.Sprintf("Переключение position mode: %v", err))
		return false
	}
	modeLabel := "one-way"
	if mode == 3 {
		modeLabel = "hedge"
	}
	sr.info(ctx, fmt.Sprintf("Position mode: %s (%s)", modeLabel, symbol))
	sr.mu.Lock()
	sr.positionModeVerified = true
	sr.lastVerifiedHedge = hedgeMode
	sr.mu.Unlock()
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

	// Close any sub-minQty dust left from a previous cycle before starting fresh.
	// Must be called without sr.mu held (makes REST calls).
	sr.closeDustPosition(ctx)

	// Cancel any lingering orders from previous cycles before creating a new one.
	// Guards against stale orders left by an incomplete closeCycle sweep.
	sr.cancelAllStrategyOrders(ctx)

	// Close any orphaned open cycles in DB (e.g. left by a duplicate-cycle race).
	// Must be done before INSERT so MAX(cycle_num) correctly reflects the last real cycle.
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET ended_at=NOW(), result='orphan'
		 WHERE strategy_id=$1 AND ended_at IS NULL`, sr.strategy.ID)

	// Apply configured leverage on the exchange before opening a new cycle.
	// Error 110043 means "leverage not modified" (already correct) and is treated as success.
	// If the requested leverage exceeds the instrument's maximum, cap it and retry.
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
		log.Printf("strategy %s: set leverage %d: %v — querying exchange max", sr.strategy.ID, lev, lerr)
		if info, ierr := trader.GetPublicInstrumentInfo(ctx, sr.strategy.Category, sr.strategy.Symbol); ierr == nil && info.MaxLeverage > 0 {
			cappedLev := int(info.MaxLeverage)
			if cappedLev < lev {
				cappedStr := strconv.Itoa(cappedLev)
				if lerr2 := trader.SetLeverage(ctx, sr.runner.creds, trader.LeverageRequest{
					Symbol:       sr.strategy.Symbol,
					Category:     sr.strategy.Category,
					BuyLeverage:  cappedStr,
					SellLeverage: cappedStr,
				}); lerr2 != nil && !strings.Contains(lerr2.Error(), "110043") {
					log.Printf("strategy %s: set leverage capped to %d: %v", sr.strategy.ID, cappedLev, lerr2)
				} else {
					sr.info(ctx, fmt.Sprintf("Плечо ограничено биржей: запрошено %dx, установлено %dx", lev, cappedLev))
				}
			}
		}
	}

	// Ensure position mode BEFORE taking the lock — this makes a REST call and must
	// not block other goroutines that access sr.mu.
	if !sr.ensurePositionMode(ctx) {
		sr.mu.Lock()
		sr.stopWithMessage(ctx, "Не удалось переключить position mode — стратегия остановлена")
		sr.mu.Unlock()
		return fmt.Errorf("position mode mismatch, cycle not started")
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.Status == StatusStopped {
		return nil
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
				prev := prevPrice
				if step.PriceMovePct == 0 {
					targetPrice = 0 // market order
				} else if side == "Buy" {
					// Negative pct = against direction = below entry (averaging down for long)
					// Positive pct = with direction = above entry (scaling in on upward move)
					targetPrice = prevPrice * (1 + step.PriceMovePct/100)
				} else {
					// Negative pct = against direction = above entry (averaging up for short)
					// Positive pct = with direction = below entry (scaling in on downward move)
					targetPrice = prevPrice * (1 - step.PriceMovePct/100)
				}
				log.Printf("strategy %s: level L%d %s target=%.4f (step %.2f%% from prev=%.4f)",
					sr.strategy.ID, levelIdx, side, targetPrice, step.PriceMovePct, prev)
				if targetPrice > 0 {
					prevPrice = targetPrice
				}
				sizePct := step.SizePct
				if sizePct == 0 && step.Lots > 0 {
					sizePct = step.Lots * 100 // migrate legacy lots→size_pct
				}
				sizeUSDT := sizePct / 100 * sr.strategy.GridSizeUSDT
				priceForQty := price
				if targetPrice > 0 {
					priceForQty = targetPrice
				}
				qty := trader.FormatQty(sizeUSDT/priceForQty, sr.instr.QtyStep, sr.instr.MinQty)
				// Positive price_move_pct = with direction = virtual (tracks momentum)
				// Negative price_move_pct = against direction = exchange limit (passive, waits for price)
				forceVirtual := step.OrderType == "virtual" || step.PriceMovePct > 0
				var levelID string
				if err := sr.runner.pool.QueryRow(ctx,
					`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, force_virtual)
					 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
					sr.strategy.ID, cycleID, levelIdx, side, targetPrice, sizeUSDT, qty, forceVirtual,
				).Scan(&levelID); err != nil {
					log.Printf("strategy %s: insert level %d: %v", sr.strategy.ID, levelIdx, err)
					levelIdx++
					continue
				}
				sr.levels = append(sr.levels, GridLevel{
					ID: levelID, LevelIdx: levelIdx, Side: side,
					TargetPrice: targetPrice, SizeUSDT: sizeUSDT, Qty: qty,
					Status: LevelPending, ForceVirtual: forceVirtual,
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
	if sr.strategy.BotID != nil {
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1, $2, 'info', 'strategy')`,
			*sr.strategy.BotID,
			fmt.Sprintf("Стратегия открыта: %s %s | цикл %d @ %.4f",
				sr.strategy.Symbol, string(sr.strategy.Direction), sr.cycle.CycleNum, price))
	}
	sr.clearManualAlert(ctx)
	if err := sr.placeNextLevels(ctx); err != nil {
		return err
	}
	// Launch price monitor for virtual grid levels (fires market orders when price crosses target).
	hasVirtual := false
	for _, l := range sr.levels {
		if l.ForceVirtual {
			hasVirtual = true
			break
		}
	}
	if hasVirtual {
		go sr.launchGridVirtualMonitor()
	}
	return nil
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
	// In finishing mode bypass the sliding window: place every remaining pending level
	// so the position is always covered by a stop even after a reconnect.
	var need int
	if sr.strategy.Status == StatusFinishing {
		for i := range sr.levels {
			if sr.levels[i].Status == LevelPending {
				need++
			}
		}
	} else {
		need = sr.strategy.GridActive - sr.placedCount()
	}
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
	// Filter out virtual levels — they are watched by gridVirtualPriceTick.
	filtered := indices[:0]
	for _, i := range indices {
		if !sr.levels[i].ForceVirtual {
			filtered = append(filtered, i)
		}
	}
	indices = filtered
	if len(indices) == 0 {
		return
	}
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
	if l.ForceVirtual {
		// Virtual level — watched by gridVirtualPriceTick, not placed on exchange.
		return nil
	}
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

// lastGridLevelPrice returns the TargetPrice of the deepest grid level:
// Long → lowest target price (furthest below entry).
// Short → highest target price (furthest above entry).
// Returns 0 if no usable levels are found.
// Must be called with sr.mu held.
func (sr *StrategyRunner) lastGridLevelPrice() float64 {
	var result float64
	for _, l := range sr.levels {
		if l.Status == LevelCancelled || l.TargetPrice == 0 {
			continue
		}
		if result == 0 {
			result = l.TargetPrice
			continue
		}
		switch sr.strategy.Direction {
		case DirectionLong:
			if l.TargetPrice < result {
				result = l.TargetPrice
			}
		case DirectionShort:
			if l.TargetPrice > result {
				result = l.TargetPrice
			}
		}
	}
	return result
}

// projectedAvgEntry returns the weighted average entry across all grid levels —
// using actual fill prices for filled levels and planned limit prices for pending/placed ones.
// This lets updateSL place the stop from the first fill without waiting for all levels,
// while keeping the trigger stable as levels fill (limit orders fill at their target price).
//
// Safety: if projected avg drifts in the wrong direction versus filled avg (e.g. breakout
// grid where unfilled levels are above entry), the caller falls back to actual avg.
// Must be called with sr.mu held.
func (sr *StrategyRunner) projectedAvgEntry() float64 {
	var totalCost, totalQty float64
	for _, l := range sr.levels {
		var price, qty float64
		switch l.Status {
		case LevelFilled:
			if l.FilledPrice == 0 {
				continue
			}
			price = l.FilledPrice
			qty, _ = strconv.ParseFloat(l.Qty, 64)
		case LevelPending, LevelPlaced:
			if l.TargetPrice == 0 {
				continue // market entry not yet filled — skip, avgEntry handles it once filled
			}
			price = l.TargetPrice
			qty, _ = strconv.ParseFloat(l.Qty, 64)
			if qty == 0 && l.SizeUSDT > 0 {
				qty = l.SizeUSDT / price
			}
		default:
			continue
		}
		if qty == 0 {
			continue
		}
		totalCost += price * qty
		totalQty += qty
	}
	if totalQty == 0 {
		return 0
	}
	return totalCost / totalQty
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

	// Matrix strategies use their own fill handler.
	if sr.strategy.StrategyType == "matrix" {
		sr.handleMatrixLevelFill(ctx, levelID, filledPrice)
		return
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
	// TP suppressed by active hedge — do not place/re-place TP.
	if sr.strategy.HedgeTpSuppressed {
		return nil
	}
	if sr.instr.QtyStep == 0 {
		sr.warn(ctx, "updateTP: инструмент не загружен (QtyStep=0) — TP не выставлен, повтор при следующем событии")
		return nil
	}
	avg, totalQty := sr.avgEntry()
	if totalQty == 0 || avg == 0 {
		return nil
	}

	// Prefer the WS-cached exchange avg entry (updated by OnPositionEvent) over the
	// internally calculated avg. Falls back to avgEntry() if no WS data received yet
	// (e.g. first fill before the position event arrives, or after service restart).
	{
		wantIdx := positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction)
		if wsAvg := sr.runner.GetPositionAvgEntry(sr.strategy.Symbol, wantIdx); wsAvg > 0 {
			sr.info(ctx, fmt.Sprintf("updateTP: ТВХ биржи %.4f (расчётная %.4f)", wsAvg, avg))
			avg = wsAvg
		}
	}

	tpSide, tpPrice := tpParams(sr.strategy.Direction, avg, sr.strategy.TPPct)
	if tpSide == "" {
		sr.warn(ctx, fmt.Sprintf("updateTP: направление '%s' не поддерживается для TP — пропускаем", sr.strategy.Direction))
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

	// Position state changed (different qty or entry price) — reset the cancel streak
	// so the circuit breaker doesn't suppress a valid TP after a level fill.
	if totalQty != sr.lastTPSLQty || avg != sr.lastTPSLAvg {
		sr.tpCancelStreak = 0
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
			// Cancel failed — restore old TP state so we don't place a duplicate.
			// The next call to updateTP will retry the cancel + replace.
			sr.tpOrderID = oldTPID
			sr.runner.RegisterOrder(oldTPID, orderRef{strategyID: sr.strategy.ID, refType: "tp"})
			return fmt.Errorf("updateTP: cancel old TP %s: %w", oldTPID, err)
		}
	}

	sr.tpPlaceSeq++
	tpQty := trader.FormatQty(totalQty, sr.instr.QtyStep, sr.instr.MinQty)
	if tpQty == "0" {
		sr.warn(ctx, fmt.Sprintf("updateTP: qty %.8f округляется до нуля (step=%.6f) — TP не выставлен", totalQty, sr.instr.QtyStep))
		return nil
	}
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
		if isTruncatedToZero(err) {
			return sr.retryTPAfterCancelStale(ctx, tpSide, tpQty, linkID, tpPrice, totalQty, avg)
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
	sr.info(ctx, fmt.Sprintf("TP выставлен @ %.4f qty=%s (ТВХ=%.4f +%.2f%%)", tpPrice, tpQty, avg, sr.strategy.TPPct))
	return nil
}

// updateTPByType updates TP for any strategy type.
// For matrix strategies it calls matrixUpdateTP; for grid strategies it calls updateTP.
// Must be called with sr.mu held.
func (sr *StrategyRunner) updateTPByType(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixUpdateTP(ctx)
		return nil
	}
	return sr.updateTP(ctx)
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
// slPct is expected to be negative (e.g. -5.0 = 5% below avg entry for long).
// triggerDirection=2: trigger when price falls to/below (long SL). triggerDirection=1: rises to/above (short SL).
func slParams(dir Direction, avg, slPct float64) (side string, triggerPrice float64, triggerDirection int) {
	switch dir {
	case DirectionLong:
		return "Sell", avg * (1 + slPct/100), 2
	case DirectionShort:
		return "Buy", avg * (1 - slPct/100), 1
	default:
		return "", 0, 0
	}
}

// updateSL cancels the existing SL order (if any) and places a new stop-market SL for the full position.
// SL trigger is computed from projectedAvgEntry (includes unfilled levels at their limit prices),
// so the stop is placed from the very first fill and stays stable as subsequent levels fill.
// If the exchange rejects the trigger as ≥ current price (110093), it means the position is
// already past the SL level — the position is closed at market immediately.
// Must be called with sr.mu held.
func (sr *StrategyRunner) updateSL(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		return nil // Matrix manages SL per-level via matrixPlacePerLevelSL
	}
	// SL suppressed by active hedge — do not place/re-place SL.
	if sr.strategy.HedgeSlSuppressed {
		return nil
	}
	if sr.strategy.SLType != SLTypeConditional || sr.strategy.SLPct >= 0 {
		return nil
	}
	if sr.instr.QtyStep == 0 {
		sr.warn(ctx, "updateSL: инструмент не загружен (QtyStep=0) — SL не выставлен, повтор при следующем событии")
		return nil
	}

	actualAvg, totalQty := sr.avgEntry()
	if totalQty == 0 || actualAvg == 0 {
		return nil
	}

	// SL is anchored to the deepest grid level so all levels are reachable before
	// the stop fires. Falls back to actual avg entry if no level target prices exist.
	slAvg := sr.lastGridLevelPrice()
	if slAvg == 0 {
		slAvg = actualAvg
	}

	slSide, slTrigger, trigDir := slParams(sr.strategy.Direction, slAvg, sr.strategy.SLPct)
	if slSide == "" {
		return nil
	}

	// Use actual exchange qty after partial manual close.
	if sr.partialCloseQty > 0 && sr.partialCloseQty < totalQty {
		totalQty = sr.partialCloseQty
	}

	// Skip if SL is already placed with the same qty and trigger price.
	// Trigger price encodes both projected avg and sl_pct, so it's the sole dedup key.
	// Must be checked BEFORE cancelling the existing order.
	if sr.slOrderID != "" && sr.lastTPSLQty == totalQty && sr.lastSLPrice == slTrigger {
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
	if slQty == "0" {
		sr.warn(ctx, fmt.Sprintf("updateSL: qty %.8f округляется до нуля (step=%.6f) — SL не выставлен", totalQty, sr.instr.QtyStep))
		return nil
	}
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
		// 110093: trigger ≥ current price — price already passed the SL level while all
		// levels were filling. Close at market immediately (programmatic SL execution).
		if strings.Contains(err.Error(), "110093") {
			sr.warn(ctx, fmt.Sprintf("updateSL: 110093 — цена уже прошла SL %.4f, закрываю позицию маркетом", slTrigger))
			sr.closePositionAtMarket(ctx, "sl", slTrigger)
			return nil
		}
		return fmt.Errorf("place SL: %w", err)
	}
	sr.slOrderID = result.OrderId
	sr.lastTPSLQty = totalQty
	sr.lastTPSLAvg = actualAvg
	sr.lastSLPrice = slTrigger
	sr.runner.RegisterOrder(result.OrderId, orderRef{strategyID: sr.strategy.ID, refType: "sl"})
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_cycles SET sl_order_id=$1 WHERE id=$2`, result.OrderId, sr.cycle.ID)
	sr.info(ctx, fmt.Sprintf("SL выставлен @ %.4f qty=%s (от посл. уровня %.4f)", slTrigger, slQty, slAvg))
	return nil
}

// cancelOrphanedSLs отменяет стоп-ордера, которые принадлежат стратегии
// (по prefix SIS_STR-{id8}-sl-) но не совпадают с текущим slOrderID.
// Вызывается без мьютекса — только после sr.mu.Unlock().
func (sr *StrategyRunner) cancelOrphanedSLs(ctx context.Context, orders []trader.Order) {
	for _, o := range orders {
		sr.warn(ctx, fmt.Sprintf("reconcile: осиротевший SL %s (linkId=%s) — отменяю", o.OrderId, o.OrderLinkId))
		if err := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			OrderId:     o.OrderId,
			OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("reconcile: не удалось отменить осиротевший SL %s: %v", o.OrderId, err))
		}
	}
}

// logBotTrade writes a trade event to bot_events if this strategy is bot-managed.
func (sr *StrategyRunner) logBotTrade(ctx context.Context, msg string) {
	if sr.strategy.BotID == nil {
		return
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1, $2, 'info', 'trade')`,
		*sr.strategy.BotID, msg)
}

// writeTrade records the closed cycle to trade_history.
// Must be called with sr.mu held (reads sr.strategy and sr.cycle).
func (sr *StrategyRunner) writeTrade(ctx context.Context, result string, avgEntry, exitPrice, qty, pnl, pnlPct float64) {
	volumeUSDT := avgEntry * qty

	// Query exchange fees and funding payments accumulated during this cycle.
	// We sum trader_executions for account+symbol within the cycle's time window.
	// Note: if trader_executions syncer is slightly behind, some tail fees may be 0.
	var fees, funding float64
	sr.runner.pool.QueryRow(ctx, //nolint:errcheck
		`SELECT
		   COALESCE(SUM(exec_fee) FILTER (WHERE exec_type = 'Trade'),   0),
		   COALESCE(SUM(exec_fee) FILTER (WHERE exec_type = 'Funding'), 0)
		 FROM trader_executions
		 WHERE account_id = $1 AND symbol = $2 AND exec_time BETWEEN $3 AND NOW()`,
		sr.strategy.AccountID, sr.strategy.Symbol, sr.cycle.StartedAt,
	).Scan(&fees, &funding) //nolint:errcheck

	netPnl := pnl - fees - funding

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO trade_history
		  (strategy_id, bot_id, account_id, owner_id, symbol, category, direction,
		   cycle_num, result, avg_entry, exit_price, qty, volume_usdt, pnl, pnl_pct,
		   fees, funding, net_pnl, opened_at, closed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NOW())`,
		sr.strategy.ID, sr.strategy.BotID, sr.strategy.AccountID, sr.strategy.OwnerID,
		sr.strategy.Symbol, sr.strategy.Category, string(sr.strategy.Direction),
		sr.cycle.CycleNum, result, avgEntry, exitPrice, qty, volumeUSDT, pnl, pnlPct,
		fees, funding, netPnl, sr.cycle.StartedAt,
	)
}

// logBotStrategy writes a strategy lifecycle event to bot_events if this strategy is bot-managed.
func (sr *StrategyRunner) logBotStrategy(ctx context.Context, msg string) {
	if sr.strategy.BotID == nil {
		return
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO bot_events (bot_id, message, level, category) VALUES ($1, $2, 'info', 'strategy')`,
		*sr.strategy.BotID, msg)
}

// handleTPFill is called when the TP order is filled.
func (sr *StrategyRunner) handleTPFill(ctx context.Context, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.strategy.StrategyType == "matrix" {
		sr.handleMatrixTPFill(ctx, fillPrice, fillQty)
		return
	}
	// Guard against duplicate fill events (e.g. WS reconnect replay): if the
	// cycle was already closed by a previous call, there is nothing to do.
	if sr.cycle == nil {
		return
	}
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
	sr.logBotTrade(ctx, fmt.Sprintf("TP исполнен %s %s | avg=%.4f → %.4f | qty=%.4f | PnL +%.2f USDT (+%.2f%%)",
		sr.strategy.Symbol, string(sr.strategy.Direction), avg, fillPrice, fillQty, pnl, pnlPct))
	sr.logBotStrategy(ctx, fmt.Sprintf("Стратегия закрыта по TP: %s %s | цикл %d | PnL +%.2f USDT (+%.2f%%)",
		sr.strategy.Symbol, string(sr.strategy.Direction), sr.cycle.CycleNum, pnl, pnlPct))
	sr.writeTrade(ctx, "tp", avg, fillPrice, fillQty, pnl, pnlPct)
	sr.closedBySelf = true // suppress the position-close WS event that follows TP fill
	sr.closedByReason = "TP"
	sr.tpOrderID = "" // already filled — closeCycle must not try to cancel it
	sr.tpCancelStreak = 0
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "tp") // cancels SL if present
	sr.maybeRestart(ctx)
}

// handleSLFill is called when the SL order is filled.
func (sr *StrategyRunner) handleSLFill(ctx context.Context, fillPrice, fillQty float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.cycle == nil {
		return
	}
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
	sr.logBotTrade(ctx, fmt.Sprintf("SL сработал %s %s | avg=%.4f → %.4f | qty=%.4f | PnL %.2f USDT (%.2f%%)",
		sr.strategy.Symbol, string(sr.strategy.Direction), avg, fillPrice, fillQty, pnl, pnlPct))
	sr.logBotStrategy(ctx, fmt.Sprintf("Стратегия закрыта по SL: %s %s | цикл %d | PnL %.2f USDT (%.2f%%)",
		sr.strategy.Symbol, string(sr.strategy.Direction), sr.cycle.CycleNum, pnl, pnlPct))
	sr.writeTrade(ctx, "sl", avg, fillPrice, fillQty, pnl, pnlPct)
	sr.closedBySelf = true // suppress the position-close WS event that follows SL fill
	sr.closedByReason = "SL"
	sr.slOrderID = "" // already filled — closeCycle must not try to cancel it
	sr.cancelPlacedLevels(ctx)
	sr.closeCycle(ctx, "sl") // cancels TP if present
	sr.maybeRestart(ctx)
}

// maybeRestart checks status and either starts a new cycle or transitions to stopped.
// Must be called with sr.mu held.
func (sr *StrategyRunner) maybeRestart(ctx context.Context) {
	switch sr.strategy.Status {
	case StatusActive:
		// Bot strategies stop after TP/SL — the bot engine handles cleanup and deletion.
		// Manual strategies stop when max_cycles is reached.
		if sr.strategy.BotID != nil || (sr.strategy.MaxCycles > 0 && sr.strategy.CycleCount >= sr.strategy.MaxCycles) {
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, sr.strategy.ID)
			sr.strategy.Status = StatusStopped
			sr.info(ctx, "Стратегия остановлена после TP/SL")
			return
		}
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			sf := sr.strategy.SignalFilter
			configs := sr.strategy.SignalConfigs
			se := sr.runner.signalEngine
			sr.mu.Unlock()

			if sf && len(configs) > 0 && se != nil {
				sr.awaitSignal(ctx)
			} else {
				sr.cancelAllStrategyOrders(ctx)
				sr.startMu.Lock()
				if err := sr.startCycleByType(ctx); err != nil {
					log.Printf("strategy %s: restart cycle: %v", sr.strategy.ID, err)
				}
				sr.startMu.Unlock()
			}
		})
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
	sr.clearManualAlert(ctx)
	// Increment cycle_count for natural cycle completions.
	if result == "tp" || result == "sl" || result == "ghost_close" || result == "position_gone" {
		sr.strategy.CycleCount++
		if _, err := sr.runner.pool.Exec(ctx,
			`UPDATE strategies SET cycle_count=$1 WHERE id=$2`, sr.strategy.CycleCount, sr.strategy.ID); err != nil {
			log.Printf("strategy %s: closeCycle: failed to increment cycle_count: %v", sr.strategy.ID, err)
		}
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_cycles SET ended_at=NOW(), result=$1, tp_order_id=NULL, sl_order_id=NULL WHERE id=$2`, result, sr.cycle.ID); err != nil {
		log.Printf("strategy %s: closeCycle: failed to end cycle %s: %v", sr.strategy.ID, sr.cycle.ID, err)
	}
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
	// Variant А: safety sweep for Grid strategies — cancel any remaining
	// SIS_STR-{stratID8} orders for this symbol that slipped the explicit cancels.
	if sr.strategy.StrategyType == "grid" {
		stratID8 := sr.strategy.ID
		if len(stratID8) > 8 {
			stratID8 = stratID8[:8]
		}
		prefix := "SIS_STR-" + stratID8 + "-"
		symbol := sr.strategy.Symbol
		category := sr.strategy.Category
		creds := sr.runner.creds
		ts := sr.runner.tradeStream
		go func() {
			sweepCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			orders, err := trader.FetchOpenOrdersForSymbolAll(sweepCtx, creds, category, symbol)
			if err != nil {
				log.Printf("strategy %s: closeCycle sweep: %v", stratID8, err)
				return
			}
			for _, o := range orders {
				if !strings.HasPrefix(o.OrderLinkId, prefix) {
					continue
				}
				orderFilter := ""
				if o.OrderFilter == "StopOrder" {
					orderFilter = "StopOrder"
				}
				if err := ts.CancelOrder(sweepCtx, trader.CancelRequest{
					Symbol:      symbol,
					Category:    category,
					OrderId:     o.OrderId,
					OrderFilter: orderFilter,
				}); err != nil && !isOrderGone(err) {
					log.Printf("strategy %s: closeCycle sweep: cancel %s: %v", stratID8, o.OrderId, err)
				} else {
					log.Printf("strategy %s: closeCycle sweep: orphan %s (linkId=%s) отменён", stratID8, o.OrderId, o.OrderLinkId)
				}
			}
		}()
	}

	// Cancel per-level matrix SL orders and stop price monitor (noop for grid strategies)
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
	}
	sr.cycle = nil
	sr.levels = nil
	sr.partialCloseQty = 0
	sr.lastTPSLQty = 0
	sr.lastTPSLAvg = 0
	sr.tpCancelStreak = 0
	// Stop continuous signal monitor — the next cycle will start its own.
	if sr.signalMonitorID != "" {
		if sr.runner.signalEngine != nil {
			sr.runner.signalEngine.Unsubscribe(sr.signalMonitorID)
		}
		sr.signalMonitorID = ""
	}
	sr.currentSignalState = ""
}

// cancelPlacedLevels cancels only the grid-level orders belonging to this strategy,
// leaving orders of other strategies on the same symbol untouched.
// Must be called with sr.mu held.
func (sr *StrategyRunner) cancelPlacedLevels(ctx context.Context) {
	var items []trader.BatchCancelItem
	var stopItems []trader.BatchCancelItem // Matrix stop market orders need OrderFilter:"StopOrder"
	var cancelLines []string
	for _, l := range sr.levels {
		if l.Status == LevelPlaced && l.ExchangeOrderID != "" {
			items = append(items, trader.BatchCancelItem{
				Symbol:  sr.strategy.Symbol,
				OrderId: l.ExchangeOrderID,
			})
			// Matrix levels (Slot != nil) may be stop market orders on Bybit.
			// Bybit ignores cancel requests with wrong OrderFilter, so send a second
			// batch with OrderFilter:"StopOrder". Per-item errors for non-stop orders
			// are silently ignored by the batch endpoint.
			if sr.strategy.StrategyType == "matrix" && l.Slot != nil {
				stopItems = append(stopItems, trader.BatchCancelItem{
					Symbol:      sr.strategy.Symbol,
					OrderId:     l.ExchangeOrderID,
					OrderFilter: "StopOrder",
				})
			}
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
	for start := 0; start < len(stopItems); start += 20 {
		end := start + 20
		if end > len(stopItems) {
			end = len(stopItems)
		}
		if err := sr.runner.tradeStream.CancelOrderBatch(ctx, trader.BatchCancelRequest{
			Category: sr.strategy.Category,
			Request:  stopItems[start:end],
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("cancelPlacedLevels: stop batch cancel: %v", err))
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

// awaitSignal subscribes to the signal engine and calls startCycle when the
// signal state matches the strategy direction. Runs in a goroutine launched from loadOrStart.
// startCycleByType starts the correct cycle type: startMatrixCycle for matrix, startCycle for grid.
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) startCycleByType(ctx context.Context) error {
	if sr.strategy.StrategyType == "matrix" {
		return sr.startMatrixCycle(ctx)
	}
	return sr.startCycle(ctx)
}

// resumeCycleAfterSignalByType resumes the correct cycle type after signal fires.
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeCycleAfterSignalByType(ctx context.Context) {
	if sr.strategy.StrategyType == "matrix" {
		sr.resumeMatrixAfterSignal(ctx)
	} else {
		sr.resumeGridAfterSignal(ctx)
	}
}

func (sr *StrategyRunner) awaitSignal(ctx context.Context) {
	sr.mu.Lock()
	configs := sr.strategy.SignalConfigs
	symbol := sr.strategy.Symbol
	stratID := sr.strategy.ID
	signalEngine := sr.runner.signalEngine
	sr.mu.Unlock()

	if signalEngine == nil || len(configs) == 0 {
		sr.startMu.Lock()
		err := sr.startCycleByType(ctx)
		sr.startMu.Unlock()
		if err != nil {
			log.Printf("strategy %s: start cycle (no signal engine): %v", stratID, err)
		}
		return
	}

	sigConfigs := make([]signal.Config, len(configs))
	for i, sc := range configs {
		sigConfigs[i] = signal.Config{Name: sc.Name, Params: sc.Params}
	}

	tf := "1h"
	if len(configs) > 0 {
		if v, ok := configs[0].Params["tf"]; ok {
			if s, ok2 := v.(string); ok2 && s != "" {
				tf = s
			}
		}
	}

	subID := stratID + ":signal"
	sr.mu.Lock()
	sr.signalSubID = subID
	sr.mu.Unlock()

	sr.info(ctx, "Ожидание сигнала для запуска цикла")

	err := signalEngine.Subscribe(subID, symbol, tf, sigConfigs, func(state signal.State) {
		sr.mu.Lock()
		status := sr.strategy.Status
		curDir := sr.strategy.Direction
		curSubID := sr.signalSubID
		sr.mu.Unlock()

		if status != StatusActive || curSubID == "" {
			return
		}

		var want signal.State
		switch curDir {
		case DirectionLong:
			want = signal.Buy
		case DirectionShort:
			want = signal.Sell
		default: // both
			if state == signal.Neutral {
				return
			}
			want = state
		}
		if state != want {
			return
		}

		signalEngine.Unsubscribe(subID)
		sr.mu.Lock()
		sr.signalSubID = ""
		sr.currentSignalState = string(state)
		sr.mu.Unlock()

		sr.info(context.Background(), fmt.Sprintf("Сигнал получен (%s), запуск цикла", state))
		sr.submit(func(ctx context.Context) {
			sr.startMu.Lock()
			if err2 := sr.startCycleByType(ctx); err2 != nil {
				log.Printf("strategy %s: start cycle on signal: %v", stratID, err2)
			}
			sr.startMu.Unlock()
			sr.launchSignalMonitor(ctx)
		})
	})
	if err != nil {
		log.Printf("strategy %s: signal subscribe: %v", stratID, err)
		sr.mu.Lock()
		sr.signalSubID = ""
		sr.mu.Unlock()
		sr.startMu.Lock()
		if err2 := sr.startCycleByType(ctx); err2 != nil {
			log.Printf("strategy %s: start cycle (signal subscribe failed): %v", stratID, err2)
		}
		sr.startMu.Unlock()
		return
	}

	// Subscribe fires only on state *changes*. If the signal is already in the
	// desired state when we subscribe, the callback would never fire and the
	// cycle would never start. Check the current state immediately.
	sr.mu.Lock()
	curDir := sr.strategy.Direction
	curSubID := sr.signalSubID
	sr.mu.Unlock()

	if curSubID == subID {
		curState, known := signalEngine.QueryState(symbol, tf, sigConfigs)
		if known {
			matches := false
			switch curDir {
			case DirectionLong:
				matches = curState == signal.Buy
			case DirectionShort:
				matches = curState == signal.Sell
			default: // both
				matches = curState != signal.Neutral
			}
			if matches {
				signalEngine.Unsubscribe(subID)
				sr.mu.Lock()
				sr.signalSubID = ""
				sr.currentSignalState = string(curState)
				sr.mu.Unlock()
				sr.info(ctx, fmt.Sprintf("Текущий сигнал (%s) уже соответствует направлению — запуск цикла", curState))
				sr.startMu.Lock()
				if err2 := sr.startCycleByType(ctx); err2 != nil {
					log.Printf("strategy %s: start cycle on current signal: %v", stratID, err2)
				}
				sr.startMu.Unlock()
				sr.launchSignalMonitor(ctx)
			}
		}
	}
}

// handleSignalConfigUpdate is called when signal_filter or signal_configs change on
// an already-active strategy (detected in addStrategy).
//
// Two cases:
//   - awaiting signal (signalSubID != ""): unsubscribe, re-evaluate with new configs;
//     if matches → resume/start grid, otherwise re-subscribe with new configs.
//   - grid running (signalSubID == ""): check current signal state;
//     if matches → keep grid unchanged, if not → cancel orders and await signal.
//
// In both cases, if a position is already open (filled levels), the cycle is left untouched.
func (sr *StrategyRunner) handleSignalConfigUpdate(ctx context.Context) {
	sr.mu.Lock()

	sf := sr.strategy.SignalFilter
	configs := sr.strategy.SignalConfigs
	symbol := sr.strategy.Symbol
	dir := sr.strategy.Direction
	signalEngine := sr.runner.signalEngine
	subID := sr.signalSubID
	monitorID := sr.signalMonitorID
	hasCycle := sr.cycle != nil
	wasAwaiting := subID != ""

	// Stop the continuous monitor — we'll restart it with updated configs below.
	if monitorID != "" && signalEngine != nil {
		signalEngine.Unsubscribe(monitorID)
		sr.signalMonitorID = ""
	}

	if wasAwaiting {
		// Unsubscribe from the old pre-cycle subscription and re-evaluate.
		if signalEngine != nil {
			signalEngine.Unsubscribe(subID)
		}
		sr.signalSubID = ""
		sr.mu.Unlock()

		if !sf || len(configs) == 0 {
			sr.info(ctx, "Фильтр сигнала отключён — запускаю сетку")
			sr.startMu.Lock()
			sr.resumeCycleAfterSignalByType(ctx)
			sr.startMu.Unlock()
			return
		}
	} else {
		if !hasCycle {
			sr.mu.Unlock()
			return
		}
		hasFills := false
		for _, l := range sr.levels {
			if l.Status == LevelFilled {
				hasFills = true
				break
			}
		}
		sr.mu.Unlock()

		if hasFills {
			sr.info(ctx, "Сигнал изменён — позиция открыта, текущий цикл продолжается")
			// Restart monitor with updated configs even with open position.
			sr.launchSignalMonitor(ctx)
			return
		}
		if !sf || len(configs) == 0 {
			return
		}
	}

	if signalEngine == nil || len(configs) == 0 {
		return
	}

	tf := "1h"
	if v, ok := configs[0].Params["tf"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			tf = s
		}
	}
	sigConfigs := make([]signal.Config, len(configs))
	for i, sc := range configs {
		sigConfigs[i] = signal.Config{Name: sc.Name, Params: sc.Params}
	}

	state, known := signalEngine.QueryState(symbol, tf, sigConfigs)

	if !known {
		// Hub has no candle data yet — re-subscribe and wait for first candle.
		if wasAwaiting {
			sr.info(ctx, "Конфигурация сигнала изменена — состояние неизвестно, ожидаю нового сигнала")
			sr.awaitSignalResumeCycle(ctx)
		} else {
			sr.info(ctx, "Конфигурация сигнала изменена — состояние сигнала неизвестно, сетка продолжает работать")
			sr.launchSignalMonitor(ctx)
		}
		return
	}

	matches := false
	switch dir {
	case DirectionLong:
		matches = state == signal.Buy
	case DirectionShort:
		matches = state == signal.Sell
	default:
		matches = state != signal.Neutral
	}

	if matches {
		if wasAwaiting {
			sr.info(ctx, fmt.Sprintf("Конфигурация сигнала изменена — текущий сигнал (%s) соответствует направлению, запускаю сетку", state))
			sr.startMu.Lock()
			sr.resumeCycleAfterSignalByType(ctx)
			sr.startMu.Unlock()
		} else {
			sr.info(ctx, fmt.Sprintf("Конфигурация сигнала изменена — текущий сигнал (%s) соответствует направлению, сетка продолжает работать", state))
		}
		sr.mu.Lock()
		sr.currentSignalState = string(state)
		sr.mu.Unlock()
		sr.launchSignalMonitor(ctx)
		return
	}

	sr.info(ctx, fmt.Sprintf("Конфигурация сигнала изменена — текущий сигнал (%s) не соответствует направлению (%s), приостанавливаю сетку", state, dir))

	if !wasAwaiting {
		sr.mu.Lock()
		sr.cancelPlacedLevels(ctx)
		for i := range sr.levels {
			l := &sr.levels[i]
			if (l.Status == LevelPlaced || l.Status == LevelPending) && l.FilledPrice == 0 {
				l.Status = LevelCancelled
				l.ExchangeOrderID = ""
			}
		}
		sr.mu.Unlock()
	}

	sr.awaitSignalResumeCycle(ctx)
}

// awaitSignalResumeCycle is like awaitSignal but resumes the existing cycle instead
// of starting a new one. Used when signal_filter is added to a running strategy that
// had its grid orders cancelled because the signal didn't match.
func (sr *StrategyRunner) awaitSignalResumeCycle(ctx context.Context) {
	sr.mu.Lock()
	configs := sr.strategy.SignalConfigs
	symbol := sr.strategy.Symbol
	stratID := sr.strategy.ID
	signalEngine := sr.runner.signalEngine
	sr.mu.Unlock()

	if signalEngine == nil || len(configs) == 0 {
		sr.startMu.Lock()
		sr.resumeCycleAfterSignalByType(ctx)
		sr.startMu.Unlock()
		return
	}

	tf := "1h"
	if v, ok := configs[0].Params["tf"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			tf = s
		}
	}
	sigConfigs := make([]signal.Config, len(configs))
	for i, sc := range configs {
		sigConfigs[i] = signal.Config{Name: sc.Name, Params: sc.Params}
	}

	subID := stratID + ":signal"
	sr.mu.Lock()
	sr.signalSubID = subID
	sr.mu.Unlock()

	sr.info(ctx, "Ожидание сигнала — сетка приостановлена")

	err := signalEngine.Subscribe(subID, symbol, tf, sigConfigs, func(state signal.State) {
		sr.mu.Lock()
		status := sr.strategy.Status
		curDir := sr.strategy.Direction
		curSubID := sr.signalSubID
		sr.mu.Unlock()

		if status != StatusActive || curSubID == "" {
			return
		}

		var want signal.State
		switch curDir {
		case DirectionLong:
			want = signal.Buy
		case DirectionShort:
			want = signal.Sell
		default:
			if state == signal.Neutral {
				return
			}
			want = state
		}
		if state != want {
			return
		}

		signalEngine.Unsubscribe(subID)
		sr.mu.Lock()
		sr.signalSubID = ""
		sr.currentSignalState = string(state)
		sr.mu.Unlock()

		sr.info(context.Background(), fmt.Sprintf("Сигнал получен (%s), возобновляю сетку", state))
		sr.submit(func(ctx context.Context) {
			sr.startMu.Lock()
			sr.resumeCycleAfterSignalByType(ctx)
			sr.startMu.Unlock()
			sr.launchSignalMonitor(ctx)
		})
	})
	if err != nil {
		log.Printf("strategy %s: signal subscribe (resume cycle): %v", stratID, err)
		sr.mu.Lock()
		sr.signalSubID = ""
		sr.mu.Unlock()
		sr.startMu.Lock()
		sr.resumeCycleAfterSignalByType(ctx)
		sr.startMu.Unlock()
	}
}

// launchSignalMonitor subscribes to the signal engine for the duration of the active
// cycle. Unlike awaitSignal (which fires once and starts the cycle), this subscription
// stays alive and acts as a continuous filter:
//   - signal drops (mismatch)  → cancel placed orders, log "приостанавливаю сетку"
//   - signal returns (match)   → resume cancelled orders, log "возобновляю сетку"
//
// Must be called WITHOUT sr.mu or sr.startMu held. Replaces any prior monitor.
func (sr *StrategyRunner) launchSignalMonitor(ctx context.Context) {
	sr.mu.Lock()
	sf := sr.strategy.SignalFilter
	configs := sr.strategy.SignalConfigs
	symbol := sr.strategy.Symbol
	stratID := sr.strategy.ID
	signalEngine := sr.runner.signalEngine
	// Clear any existing monitor subscription before registering a new one.
	if sr.signalMonitorID != "" && signalEngine != nil {
		signalEngine.Unsubscribe(sr.signalMonitorID)
		sr.signalMonitorID = ""
	}
	sr.mu.Unlock()

	if !sf || len(configs) == 0 || signalEngine == nil {
		return
	}

	tf := "1h"
	if v, ok := configs[0].Params["tf"]; ok {
		if s, ok2 := v.(string); ok2 && s != "" {
			tf = s
		}
	}
	sigConfigs := make([]signal.Config, len(configs))
	sigParts := make([]string, len(configs))
	for i, sc := range configs {
		sigConfigs[i] = signal.Config{Name: sc.Name, Params: sc.Params}
		sigParts[i] = sc.Name
	}
	sigLabel := strings.Join(sigParts, "+")

	monitorID := stratID + ":monitor"
	sr.mu.Lock()
	sr.signalMonitorID = monitorID
	sr.mu.Unlock()

	err := signalEngine.Subscribe(monitorID, symbol, tf, sigConfigs, func(state signal.State) {
		sr.mu.Lock()
		status := sr.strategy.Status
		curDir := sr.strategy.Direction
		curMonitorID := sr.signalMonitorID
		hasCycle := sr.cycle != nil
		hasPlaced := false
		hasCancelled := false
		for _, l := range sr.levels {
			if l.Status == LevelPlaced {
				hasPlaced = true
			}
			if l.Status == LevelCancelled && l.FilledPrice == 0 {
				hasCancelled = true
			}
		}
		prevState := sr.currentSignalState
		curStateStr := string(state)
		sr.currentSignalState = curStateStr
		sr.mu.Unlock()

		if status != StatusActive || curMonitorID != monitorID {
			return
		}
		if curStateStr == prevState {
			return
		}

		sr.info(context.Background(), fmt.Sprintf("Сигнал [%s]: %s → %s", sigLabel, prevState, curStateStr))

		matches := false
		switch curDir {
		case DirectionLong:
			matches = state == signal.Buy
		case DirectionShort:
			matches = state == signal.Sell
		default:
			matches = state != signal.Neutral
		}

		if matches {
			if hasCycle && hasCancelled && !hasPlaced {
				sr.info(context.Background(), "Возобновляю сетку")
				sr.submit(func(ctx context.Context) {
					sr.startMu.Lock()
					sr.resumeCycleAfterSignalByType(ctx)
					sr.startMu.Unlock()
				})
			}
		} else {
			if hasPlaced {
				sr.info(context.Background(), "Приостанавливаю сетку")
				sr.submit(func(ctx context.Context) {
					sr.mu.Lock()
					sr.cancelPlacedLevels(ctx)
					for i := range sr.levels {
						l := &sr.levels[i]
						if (l.Status == LevelPlaced || l.Status == LevelPending) && l.FilledPrice == 0 {
							l.Status = LevelCancelled
							l.ExchangeOrderID = ""
						}
					}
					sr.mu.Unlock()
				})
			}
		}
	})

	if err != nil {
		log.Printf("strategy %s: signal monitor subscribe: %v", stratID, err)
		sr.mu.Lock()
		sr.signalMonitorID = ""
		sr.mu.Unlock()
	}
}

// resumeGridAfterSignal resumes the current cycle after the signal arrived while
// the grid was paused. Levels that were cancelled are reset to pending; any level
// whose target price has already been passed by the market is filled immediately
// at market price, and the rest are placed as limits via placeNextLevels.
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeGridAfterSignal(ctx context.Context) {
	sr.mu.Lock()
	hasCycle := sr.cycle != nil
	sr.mu.Unlock()

	if !hasCycle {
		if err := sr.startCycle(ctx); err != nil {
			log.Printf("strategy %s: resumeGridAfterSignal: start cycle: %v", sr.strategy.ID, err)
		}
		return
	}

	price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: resumeGridAfterSignal: fetch price: %v", sr.strategy.ID, err)
		return
	}

	sr.resetCancelledLevels(ctx)

	sr.mu.Lock()

	if sr.cycle == nil {
		sr.mu.Unlock()
		if err := sr.startCycle(ctx); err != nil {
			log.Printf("strategy %s: resumeGridAfterSignal: start cycle: %v", sr.strategy.ID, err)
		}
		return
	}

	sr.repriceGen++

	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending {
			continue
		}
		passed := (l.Side == "Buy" && price <= l.TargetPrice) ||
			(l.Side == "Sell" && price >= l.TargetPrice)
		if !passed {
			continue
		}
		linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
		sr.runner.RegisterOrder(linkID, ref)
		result, placeErr := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
			Symbol:      sr.strategy.Symbol,
			Category:    sr.strategy.Category,
			Side:        l.Side,
			OrderType:   "Market",
			Qty:         l.Qty,
			PositionIdx: positionIdxForOpen(sr.strategy.HedgeMode, l.Side),
			OrderLinkId: linkID,
		})
		if placeErr != nil {
			sr.runner.UnregisterOrder(linkID)
			sr.errlog(ctx, fmt.Sprintf("Сигнал: market L%d: %v", l.LevelIdx, placeErr))
			continue
		}
		l.Status = LevelFilled
		l.FilledPrice = price
		l.ExchangeOrderID = result.OrderId
		sr.runner.RegisterOrder(result.OrderId, ref)
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW(), exchange_order_id=$2 WHERE id=$3`,
			price, result.OrderId, l.ID)
		sr.info(ctx, fmt.Sprintf("Сигнал: L%d %s @ %.4f (маркет, цена прошла уровень)", l.LevelIdx, l.Side, price))
	}

	if err := sr.placeNextLevels(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровней после сигнала: %v", err))
	}
	if err := sr.updateTP(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка обновления TP: %v", err))
	}
	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка обновления SL: %v", err))
	}

	sr.mu.Unlock()
}

// handleStopRequest cancels placed L-orders but keeps TP and SL active so the
// current cycle ends naturally. Called when status transitions to stopped.
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleStopRequest(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Cancel continuous signal monitor if active.
	if sr.signalMonitorID != "" {
		if sr.runner.signalEngine != nil {
			sr.runner.signalEngine.Unsubscribe(sr.signalMonitorID)
		}
		sr.signalMonitorID = ""
		sr.currentSignalState = ""
	}

	// Cancel any pending signal subscription (awaiting first signal before cycle start).
	if sr.signalSubID != "" {
		if sr.runner.signalEngine != nil {
			sr.runner.signalEngine.Unsubscribe(sr.signalSubID)
		}
		sr.signalSubID = ""
		sr.info(ctx, "Стратегия остановлена (ожидание сигнала отменено)")
		return
	}

	if sr.cycle == nil {
		sr.info(ctx, "Стратегия остановлена (нет активного цикла)")
		return
	}
	if sr.strategy.StrategyType == "matrix" {
		sr.matrixCancelPerLevelSLs(ctx)
		if sr.matrixMonitorStop != nil {
			sr.matrixMonitorStop()
			sr.matrixMonitorStop = nil
		}
		sr.matrixWaitingSlots = make(map[int]float64)
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
		// For matrix strategy: keep the cycle alive so it can be resumed exactly
		// as-is (with its levels) when the strategy is re-activated.  Closing the
		// cycle here would cause loadOrStart to start a *new* cycle on restart.
		if sr.strategy.StrategyType == "matrix" {
			sr.info(ctx, "Стратегия остановлена — нет открытой позиции, матричный цикл сохранён")
			return
		}
		sr.closeCycle(ctx, "stopped")
		sr.info(ctx, "Стратегия остановлена — нет открытой позиции, цикл закрыт")
		return
	}
	sr.info(ctx, "Стратегия остановлена — L-ордера отменены, TP/SL продолжают работать до конца цикла")
	// If there's a position but no SL yet, place one now so the position is protected.
	if sr.slOrderID == "" && sr.strategy.SLPct < 0 {
		if err := sr.updateSL(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления SL после остановки: %v", err))
		}
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

// restartCycle applies updated strategy settings to the running cycle.
// If a position is open (filled levels exist), only the pending levels are repriced
// from the last filled level — the cycle stays open to protect the position.
// If there is no open position, the cycle is closed and a fresh one is started.
// Called after strategy settings are updated so new parameters take effect immediately.
// restartCycle applies updated strategy settings to a running cycle.
// Dispatches to the type-specific implementation so matrix and grid logic stay separate.
func (sr *StrategyRunner) restartCycle(ctx context.Context) {
	sr.startMu.Lock()
	defer sr.startMu.Unlock()
	sr.clearManualAlert(ctx)
	if sr.strategy.StrategyType == "matrix" {
		sr.restartMatrixCycle(ctx)
	} else {
		sr.restartGridCycle(ctx)
	}
}

// restartMatrixCycle applies updated settings to a running matrix strategy.
// If a position is open, cancels and re-places pending slots at the current price.
// If no position, closes the cycle and starts a fresh one with the new config.
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) restartMatrixCycle(ctx context.Context) {
	sr.mu.Lock()
	if sr.strategy.Status != StatusActive {
		hasFills := false
		for _, l := range sr.levels {
			if l.Status == LevelFilled {
				hasFills = true
				break
			}
		}
		if hasFills {
			sr.gridChangedWhileStopped = true
			if sr.strategy.Status == StatusFinishing && sr.cycle != nil {
				sr.matrixUpdateTP(ctx)
			}
		} else {
			sr.cancelPlacedLevels(ctx)
			if sr.cycle != nil {
				sr.closeCycle(ctx, "settings_changed")
			}
		}
		sr.mu.Unlock()
		return
	}

	hasFills := false
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasFills = true
			break
		}
	}

	if hasFills && sr.cycle != nil {
		// Position is open — cancel pending orders and re-place at new prices.
		sr.info(ctx, "Настройки изменены, позиция открыта — пересчёт отложенных уровней")
		sr.cancelPlacedLevels(ctx)
		// cancelPlacedLevels sets all placed+pending levels to 'cancelled' in DB.
		// Restore them to 'pending' so placeMatrixLevel can re-place them.
		for i := range sr.levels {
			if sr.levels[i].Status == LevelPlaced {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`,
					sr.levels[i].ID)
			} else if sr.levels[i].Status == LevelPending {
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending' WHERE id=$1`,
					sr.levels[i].ID)
			}
		}
		sr.repriceGen++ // fresh linkIds so Bybit never sees duplicates (avoids 110072)
		sr.mu.Unlock()
		price, ferr := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
		sr.mu.Lock()
		if ferr == nil {
			// Recalculate target prices / sizes from updated settings before placing.
			sr.applyNewMatrixPrices(ctx, sr.cycle.StartPrice, price)
			for i := range sr.levels {
				if sr.levels[i].Status == LevelPending {
					if err := sr.placeMatrixLevel(ctx, &sr.levels[i], price); err != nil {
						sr.errlog(ctx, fmt.Sprintf("restartMatrixCycle %s: %v", slotLabel(sr.levels[i].Slot), err))
					}
				}
			}
		}
		sr.matrixUpdateTP(ctx)
		sr.mu.Unlock()
		return
	}

	// No open position — close the current cycle and start fresh with the new config.
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
		log.Printf("strategy %s: matrix cycle %d closed", sr.strategy.ID, cycleNum)
	}
	sr.mu.Unlock()

	instr, err := trader.GetInstrumentInfo(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		log.Printf("strategy %s: instrument info: %v", sr.strategy.ID, err)
	} else {
		sr.mu.Lock()
		sr.instr = instr
		sr.mu.Unlock()
	}

	if err := sr.startMatrixCycle(ctx); err != nil {
		log.Printf("strategy %s: start matrix cycle after settings update: %v", sr.strategy.ID, err)
	}
}

// restartGridCycle applies updated settings to a running grid/bot strategy.
// If a position is open, cancels and re-places pending levels with recalculated prices.
// If no position, closes the cycle and starts a fresh one with the new config.
// Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) restartGridCycle(ctx context.Context) {
	sr.mu.Lock()
	if sr.strategy.Status != StatusActive {
		hasFills := false
		for _, l := range sr.levels {
			if l.Status == LevelFilled {
				hasFills = true
				break
			}
		}
		if hasFills {
			sr.gridChangedWhileStopped = true
			if sr.strategy.Status == StatusFinishing && sr.cycle != nil {
				if err := sr.updateTPByType(ctx); err != nil {
					sr.errlog(ctx, "Ошибка обновления TP (finishing): "+err.Error())
				}
				if err := sr.updateSL(ctx); err != nil {
					sr.errlog(ctx, "Ошибка обновления SL (finishing): "+err.Error())
				}
			}
		} else {
			sr.cancelPlacedLevels(ctx)
			if sr.cycle != nil {
				sr.closeCycle(ctx, "settings_changed")
			}
		}
		sr.mu.Unlock()
		return
	}

	hasFills := false
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasFills = true
			break
		}
	}

	if hasFills && sr.cycle != nil {
		// Position is open — cancel pending orders and reprice from the last fill.
		sr.info(ctx, "Настройки изменены, позиция открыта — пересчёт отложенных уровней")
		sr.cancelPlacedLevels(ctx)
		// cancelPlacedLevels blanket-sets placed+pending to 'cancelled' in DB;
		// restore the originally-placed ones back to 'pending' so they can be re-placed.
		for i := range sr.levels {
			if sr.levels[i].Status == LevelPlaced {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`,
					sr.levels[i].ID)
			} else if sr.levels[i].Status == LevelPending {
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending' WHERE id=$1`,
					sr.levels[i].ID)
			}
		}
		// Trim levels that exceed the new step count (handles grid reduction with open position).
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
		sr.repriceGen++
		sr.repriceRemainingFromFills(ctx)
		if err := sr.placeNextLevels(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка выставления уровней: %v", err))
		}
		if err := sr.updateTPByType(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка обновления TP: %v", err))
		}
		if err := sr.updateSL(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка обновления SL после пересчёта уровней: %v", err))
		}
		sr.mu.Unlock()
		return
	}

	// No open position — close the current cycle and start fresh with the new config.
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
		log.Printf("strategy %s: grid cycle %d closed", sr.strategy.ID, cycleNum)
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

	// Respect signal filter: if the signal is currently neutral, wait for it.
	sr.mu.Lock()
	sf2 := sr.strategy.SignalFilter
	sigCfgs2 := sr.strategy.SignalConfigs
	sr.mu.Unlock()
	if sf2 && len(sigCfgs2) > 0 && sr.runner.signalEngine != nil {
		sr.awaitSignal(ctx)
		return
	}
	if err := sr.startCycle(ctx); err != nil {
		log.Printf("strategy %s: start grid cycle after settings update: %v", sr.strategy.ID, err)
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
				// Advance prevPrice through non-pending levels so the chain stays intact.
				if l.TargetPrice > 0 {
					prevPrice = l.TargetPrice
				}
				stepIdx++
				continue
			}
			step := sr.strategy.Steps[stepIdx]
			var target float64
			if side == "Buy" {
				target = prevPrice * (1 + step.PriceMovePct/100)
			} else {
				target = prevPrice * (1 - step.PriceMovePct/100)
			}
			sizePct := step.SizePct
			if sizePct == 0 && step.Lots > 0 {
				sizePct = step.Lots * 100 // migrate legacy lots→size_pct
			}
			sizeUSDT := sizePct / 100 * sr.strategy.GridSizeUSDT
			qty := trader.FormatQty(sizeUSDT/target, sr.instr.QtyStep, sr.instr.MinQty)
			l.TargetPrice = target
			l.SizeUSDT = sizeUSDT
			l.Qty = qty
			sr.runner.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET target_price=$1, qty=$2, size_usdt=$3 WHERE id=$4`,
				target, qty, sizeUSDT, l.ID)
			if target > 0 {
				prevPrice = target
			}
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
					target = prevPrice * (1 + step.PriceMovePct/100)
				} else {
					target = prevPrice * (1 - step.PriceMovePct/100)
				}
				sizePct := step.SizePct
				if sizePct == 0 && step.Lots > 0 {
					sizePct = step.Lots * 100 // migrate legacy lots→size_pct
				}
				sizeUSDT := sizePct / 100 * sr.strategy.GridSizeUSDT
				qty := trader.FormatQty(sizeUSDT/target, sr.instr.QtyStep, sr.instr.MinQty)
				forceVirtual := step.OrderType == "virtual" || step.PriceMovePct > 0
				maxIdx++
				var levelID string
				if err := sr.runner.pool.QueryRow(ctx,
					`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, force_virtual)
					 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
					sr.strategy.ID, sr.cycle.ID, maxIdx, side, target, sizeUSDT, qty, forceVirtual,
				).Scan(&levelID); err != nil {
					sr.errlog(ctx, fmt.Sprintf("Создание нового уровня L%d: %v", maxIdx, err))
					break
				}
				sr.levels = append(sr.levels, GridLevel{
					ID:           levelID,
					LevelIdx:     maxIdx,
					Side:         side,
					TargetPrice:  target,
					SizeUSDT:     sizeUSDT,
					Qty:          qty,
					Status:       LevelPending,
					ForceVirtual: forceVirtual,
				})
				if target > 0 {
					prevPrice = target
				}
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

	// Matrix: the position-zero WS event can arrive before the per-level SL fill event
	// (both come from the same WS stream but ordering is not guaranteed by Bybit).
	// If any LevelFilled level still has an active per-level SL order registered,
	// defer once — handleMatrixSLFill will arrive next and update the level status.
	// On the retry (handlePositionCloseRetry) we proceed unconditionally so a genuine
	// manual close is never silently swallowed.
	if sr.strategy.StrategyType == "matrix" {
		for _, l := range sr.levels {
			if l.Status == LevelFilled && l.SLOrderID != "" {
				sr.submit(func(ctx context.Context) { sr.handlePositionCloseRetry(ctx) })
				return
			}
		}
	}

	sr.closeManualPosition(ctx)
}

// handlePositionCloseRetry is queued by handlePositionClose when a matrix per-level SL
// race is suspected. By the time it runs, handleMatrixSLFill has had a chance to update
// level statuses. If position is now accounted for, return silently; otherwise close.
func (sr *StrategyRunner) handlePositionCloseRetry(ctx context.Context) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.cycle == nil {
		return // already handled (closeCycle was called by handleMatrixSLFill path)
	}

	hasPosition := sr.tpOrderID != "" || sr.slOrderID != ""
	for _, l := range sr.levels {
		if l.Status == LevelFilled {
			hasPosition = true
			break
		}
	}
	if !hasPosition {
		return // SL fill processed correctly — no open position in our books
	}

	// Still has position after retry → treat as genuine manual close.
	sr.closeManualPosition(ctx)
}

// closeManualPosition is called when a position is detected as closed externally
// (not by our own TP/SL fill). The source parameter describes the trigger for the log.
// Must be called with sr.mu held.
func (sr *StrategyRunner) closeManualPosition(ctx context.Context) {
	sr.closePositionExternal(ctx, "вручную")
}

// closePositionExternal closes the cycle and stops the strategy because the position
// disappeared unexpectedly. source is appended to the log line, e.g. "вручную" or
// "биржей при отмене TP". Must be called with sr.mu held.
func (sr *StrategyRunner) closePositionExternal(ctx context.Context, source string) {
	avg, posQty := sr.avgEntry()
	sr.warn(ctx, fmt.Sprintf("Позиция закрыта %s — цикл %d | avg=%.4f | qty=%.4f | определяем результат...",
		source, sr.cycle.CycleNum, avg, posQty))

	// Capture immutable data before releasing the lock (goroutine runs outside mu).
	pool := sr.runner.pool
	stratID8 := sr.strategy.ID[:8]
	snap := externalCloseSnap{
		strategyID:     sr.strategy.ID,
		stratID8:       stratID8,
		botID:          sr.strategy.BotID,
		ownerID:        sr.strategy.OwnerID,
		accountID:      sr.strategy.AccountID,
		symbol:         sr.strategy.Symbol,
		category:       sr.strategy.Category,
		direction:      string(sr.strategy.Direction),
		cycleNum:       sr.cycle.CycleNum,
		cycleID:        sr.cycle.ID,
		cycleStartedAt: sr.cycle.StartedAt,
		avgEntry:       avg,
		posQty:         posQty,
		creds:          sr.runner.creds,
	}

	// NOTE: writeTrade is intentionally deferred to resolveExternalClose below.
	// We wait for trader_executions to sync so we can:
	//   a) detect whether it was our strategy's TP/SL order that filled (not a manual close)
	//   b) use actual exit price and P&L instead of dummy avg/0 values.

	sr.cancelPlacedLevels(ctx)
	cycleID := sr.cycle.ID
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategy_levels SET status='cancelled' WHERE cycle_id=$1 AND status='filled'`, cycleID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("closePositionExternal: DB update filled→cancelled: %v", err))
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
		sr.errlog(ctx, fmt.Sprintf("closePositionExternal: DB update status→stopped: %v", err))
	}

	if posQty > 0 {
		go resolveExternalClose(pool, snap)
	}
}

// externalCloseSnap holds all data from sr needed by resolveExternalClose (runs outside mu).
type externalCloseSnap struct {
	strategyID     string
	stratID8       string
	botID          *string
	ownerID        string
	accountID      string
	symbol         string
	category       string
	direction      string // "long" | "short"
	cycleNum       int
	cycleID        string
	cycleStartedAt time.Time
	avgEntry       float64
	posQty         float64
	creds          trader.Credentials // for direct Bybit API fallback
}

// resolveExternalClose runs in a goroutine after closePositionExternal.
// Retries with increasing delays until closing executions appear in trader_executions,
// then determines whether this was our TP/SL or a genuine manual close and writes
// the correct trade_history record.
// Retry schedule: 4s → 6s → 10s → 15s (max ~35s total).
func resolveExternalClose(pool *pgxpool.Pool, snap externalCloseSnap) {
	ctx := context.Background()

	tpPrefix := fmt.Sprintf("SIS_STR-%s-tp-%d-", snap.stratID8, snap.cycleNum)
	slPrefix := fmt.Sprintf("SIS_STR-%s-sl-%d-", snap.stratID8, snap.cycleNum)
	openPrefix := fmt.Sprintf("SIS_STR-%s-%d-", snap.stratID8, snap.cycleNum)
	closingSide := "Sell"
	if snap.direction == "short" {
		closingSide = "Buy"
	}

	type execRow struct {
		linkID string
		val    float64
		qty    float64
	}

	// queryTPSL returns fills for our TP or SL orders.
	queryTPSL := func() (rows []execRow, firstLink string) {
		r, err := pool.Query(ctx, `
			SELECT COALESCE(order_link_id, ''),
			       COALESCE(exec_value, 0),
			       COALESCE(qty, 0)
			FROM trader_executions
			WHERE account_id = $1 AND symbol = $2
			  AND exec_type = 'Trade'
			  AND exec_time >= $3
			  AND (order_link_id LIKE $4 OR order_link_id LIKE $5)
			ORDER BY exec_time`,
			snap.accountID, snap.symbol, snap.cycleStartedAt, tpPrefix+"%", slPrefix+"%")
		if err != nil {
			return
		}
		defer r.Close()
		for r.Next() {
			var row execRow
			if r.Scan(&row.linkID, &row.val, &row.qty) == nil && row.qty > 0 {
				rows = append(rows, row)
				if firstLink == "" {
					firstLink = row.linkID
				}
			}
		}
		return
	}

	// queryAnyClose returns total value and qty of closing (non-strategy) executions.
	queryAnyClose := func() (val, qty float64) {
		pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(exec_value), 0), COALESCE(SUM(qty), 0)
			FROM trader_executions
			WHERE account_id = $1 AND symbol = $2
			  AND exec_type = 'Trade' AND side = $3
			  AND exec_time >= $4
			  AND (order_link_id IS NULL OR order_link_id NOT LIKE $5)`,
			snap.accountID, snap.symbol, closingSide, snap.cycleStartedAt, openPrefix+"%",
		).Scan(&val, &qty) //nolint:errcheck
		return
	}

	// Retry loop: wait, then check. Stop as soon as any execution is found,
	// or after the last attempt (write best-effort fallback).
	retryDelays := []time.Duration{4, 6, 10, 15}
	result := "manual"
	exitPrice := snap.avgEntry
	exitQty := snap.posQty
	var firstLinkID string
	found := false

	for attempt, delay := range retryDelays {
		time.Sleep(delay * time.Second)

		// Check our TP/SL orders first.
		tpslRows, link := queryTPSL()
		if len(tpslRows) > 0 {
			var totalVal, totalQty float64
			for _, r := range tpslRows {
				totalVal += r.val
				totalQty += r.qty
			}
			exitPrice = totalVal / totalQty
			exitQty = totalQty
			firstLinkID = link
			if strings.HasPrefix(link, tpPrefix) {
				result = "tp"
			} else {
				result = "sl"
			}
			found = true
			break
		}

		// Check any closing execution (manual close with real price).
		anyVal, anyQty := queryAnyClose()
		if anyQty > 0 {
			exitPrice = anyVal / anyQty
			exitQty = anyQty
			found = true
			break
		}

		isLast := attempt == len(retryDelays)-1
		if !isLast {
			log.Printf("strategy %s: цикл %d — executions ещё не синхронизированы (попытка %d/%d), повтор...",
				snap.stratID8, snap.cycleNum, attempt+1, len(retryDelays))
		} else {
			log.Printf("strategy %s: цикл %d — executions не найдены в DB за %ds, пробуем Bybit API напрямую...",
				snap.stratID8, snap.cycleNum, int((4+6+10+15)))
		}
	}

	// Last-resort fallback: DB syncer runs every 60s but we only waited ~35s.
	// Query Bybit closed PnL API directly to get the real exit price.
	if !found {
		if pnls, _, err := trader.FetchClosedPnl(ctx, snap.creds, snap.category, ""); err == nil {
			for _, p := range pnls {
				if p.Symbol != snap.symbol {
					continue
				}
				// Match by creation time: must be after cycle started.
				createdMs, _ := strconv.ParseInt(p.CreatedTime, 10, 64)
				if createdMs == 0 {
					continue
				}
				createdAt := time.UnixMilli(createdMs)
				if createdAt.Before(snap.cycleStartedAt) {
					continue
				}
				// Match by entry price (within 0.5% tolerance for rounding).
				apiEntry, _ := strconv.ParseFloat(p.AvgEntryPrice, 64)
				if apiEntry > 0 && math.Abs(apiEntry-snap.avgEntry)/snap.avgEntry > 0.005 {
					continue
				}
				// Found a matching closed position record.
				exitPrice, _ = strconv.ParseFloat(p.AvgExitPrice, 64)
				exitQty, _ = strconv.ParseFloat(p.Qty, 64)
				// Use Bybit's PnL directly — most accurate.
				bybitPnl, _ := strconv.ParseFloat(p.ClosedPnl, 64)
				if exitQty > 0 && exitQty < snap.posQty*1.01 {
					// bybitPnl is gross (before funding). Use it for pnl; fees come from trader_executions.
					exitQty = snap.posQty // use our recorded qty for consistency
					_ = bybitPnl         // will be recalculated below from exit price
				}
				found = true
				log.Printf("strategy %s: цикл %d — exit price получен из Bybit API | exit=%.4f qty=%.4f",
					snap.stratID8, snap.cycleNum, exitPrice, exitQty)
				break
			}
		} else {
			log.Printf("strategy %s: цикл %d — Bybit API fallback failed: %v — пишем с avg как fallback",
				snap.stratID8, snap.cycleNum, err)
		}
	}

	// Calculate P&L.
	pnl, pnlPct := 0.0, 0.0
	if exitPrice > 0 && snap.avgEntry > 0 && exitQty > 0 {
		if snap.direction == "long" {
			pnl = (exitPrice - snap.avgEntry) * exitQty
		} else {
			pnl = (snap.avgEntry - exitPrice) * exitQty
		}
		if denom := snap.avgEntry * exitQty; denom > 0 {
			pnlPct = pnl / denom * 100
		}
	}

	// Fees and funding for the full cycle window.
	var fees, funding float64
	pool.QueryRow(ctx, `
		SELECT
		  COALESCE(SUM(exec_fee) FILTER (WHERE exec_type = 'Trade'),   0),
		  COALESCE(SUM(exec_fee) FILTER (WHERE exec_type = 'Funding'), 0)
		FROM trader_executions
		WHERE account_id = $1 AND symbol = $2 AND exec_time BETWEEN $3 AND NOW()`,
		snap.accountID, snap.symbol, snap.cycleStartedAt,
	).Scan(&fees, &funding) //nolint:errcheck
	netPnl := pnl - fees - funding

	// Write trade_history.
	pool.Exec(ctx, `
		INSERT INTO trade_history
		  (strategy_id, bot_id, account_id, owner_id, symbol, category, direction,
		   cycle_num, result, avg_entry, exit_price, qty, volume_usdt, pnl, pnl_pct,
		   fees, funding, net_pnl, opened_at, closed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,NOW())`, //nolint:errcheck
		snap.strategyID, snap.botID, snap.accountID, snap.ownerID,
		snap.symbol, snap.category, snap.direction,
		snap.cycleNum, result,
		snap.avgEntry, exitPrice, exitQty, snap.avgEntry*exitQty,
		pnl, pnlPct, fees, funding, netPnl, snap.cycleStartedAt)

	// If it was our TP/SL, correct the cycle result in DB.
	if result == "tp" || result == "sl" {
		pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_cycles SET result=$1 WHERE id=$2`, result, snap.cycleID)
		log.Printf("strategy %s: цикл %d — определён как %s (linkId=%s) | exit=%.4f | PnL=%.2f USDT",
			snap.stratID8, snap.cycleNum, result, firstLinkID, exitPrice, pnl)
	} else if found {
		log.Printf("strategy %s: цикл %d — закрыт вручную | exit=%.4f | PnL=%.2f USDT",
			snap.stratID8, snap.cycleNum, exitPrice, pnl)
	}
	// If !found, the retry-loop already logged the fallback message.
	_ = found
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

// isTruncatedToZero returns true for Bybit retCode=110017 (orderQty truncated to zero).
// Typically caused by a stale close order on the exchange that was lost from our tracking,
// consuming the entire reduce-only or position-close budget.
func isTruncatedToZero(err error) bool {
	return err != nil && strings.Contains(err.Error(), "retCode=110017")
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
					p = prevPrice * (1 + step.PriceMovePct/100)
				} else {
					p = prevPrice * (1 - step.PriceMovePct/100)
				}
				if p > 0 {
					prevPrice = p
				}
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

	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка обновления SL после reprice уровней: %v", err))
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
		avg, posQty := sr.avgEntry()
		fillPrice, _ := strconv.ParseFloat(existing.AvgPrice, 64)
		if fillPrice == 0 {
			fillPrice, _ = strconv.ParseFloat(existing.Price, 64)
		}
		fillQty, _ := strconv.ParseFloat(existing.CumExecQty, 64)
		if fillQty == 0 {
			fillQty = posQty
		}
		if avg > 0 && fillPrice > 0 && fillQty > 0 {
			pnl := (fillPrice - avg) * fillQty
			if sr.strategy.Direction == DirectionShort {
				pnl = (avg - fillPrice) * fillQty
			}
			pnlPct := pnl / (avg * fillQty) * 100
			sr.writeTrade(ctx, "tp", avg, fillPrice, fillQty, pnl, pnlPct)
		}
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

// retryTPAfterCancelStale handles Bybit retCode=110017 (orderQty truncated to zero) for TP orders.
// Root cause: a stale close-side order on the exchange was lost from our DB tracking, consuming
// the position-close budget so a new TP cannot be placed. We fetch all open orders for the
// symbol, cancel any close-side limit orders we don't recognise, then retry TP placement.
// Must be called with sr.mu held.
func (sr *StrategyRunner) retryTPAfterCancelStale(ctx context.Context, tpSide, tpQty, linkID string, tpPrice, totalQty, avg float64) error {
	openOrders, err := trader.FetchOpenOrdersForSymbol(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
	if err != nil {
		return fmt.Errorf("place TP (110017, fetch orders): %w", err)
	}

	// Collect IDs we already track so we don't accidentally cancel live grid levels.
	known := map[string]bool{sr.tpOrderID: true, sr.slOrderID: true}
	for _, l := range sr.levels {
		if l.ExchangeOrderID != "" {
			known[l.ExchangeOrderID] = true
		}
	}
	known[""] = false // empty string is never a valid order ID

	cancelled := 0
	for _, o := range openOrders {
		if o.Side != tpSide || known[o.OrderId] {
			continue
		}
		if e := sr.runner.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category, OrderId: o.OrderId,
		}); e != nil && !isOrderGone(e) {
			sr.warn(ctx, fmt.Sprintf("retryTP: cancel stale %s: %v", o.OrderId[:8], e))
		} else {
			cancelled++
		}
	}
	if cancelled > 0 {
		sr.info(ctx, fmt.Sprintf("TP (110017): отменено %d стейл-ордеров, повторная попытка", cancelled))
	}

	// Bybit's WS cancel events for close-side orders can arrive before the exchange's
	// internal reduce-only budget tracking is updated. Placing a new TP immediately after
	// cancelling stale orders can result in an instant cancel loop:
	//   cancel(stale) → place(new TP) → Bybit cancels again (budget not freed yet)
	// A short pause lets Bybit's matching engine process the cancellations first.
	time.Sleep(400 * time.Millisecond)

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
		return fmt.Errorf("place TP (retry after stale cancel): %w", err)
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
		avg, posQty := sr.avgEntry()
		fillPrice, _ := strconv.ParseFloat(existing.AvgPrice, 64)
		if fillPrice == 0 {
			fillPrice, _ = strconv.ParseFloat(existing.Price, 64)
		}
		fillQty, _ := strconv.ParseFloat(existing.CumExecQty, 64)
		if fillQty == 0 {
			fillQty = posQty
		}
		if avg > 0 && fillPrice > 0 && fillQty > 0 {
			pnl := (fillPrice - avg) * fillQty
			if sr.strategy.Direction == DirectionShort {
				pnl = (avg - fillPrice) * fillQty
			}
			pnlPct := pnl / (avg * fillQty) * 100
			sr.writeTrade(ctx, "sl", avg, fillPrice, fillQty, pnl, pnlPct)
		}
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
		// No filled levels tracked. For matrix strategy this can happen right after the
		// global TP fires and handleMatrixTPFill resets all levels to pending — the tail
		// position on the exchange should be closed at market WITHOUT ending the cycle.
		// For all other strategies, treat it as a ghost position and close the cycle.
		if exchangeSize > 0 {
			if sr.strategy.StrategyType == "matrix" {
				// Dedup: Bybit sends position snapshots on every order event; don't
				// issue a second tail-close for the same size we already handled.
				if sr.partialCloseQty > 0 && math.Abs(exchangeSize-sr.partialCloseQty) < exchangeSize*0.01 {
					return
				}
				sr.partialCloseQty = exchangeSize
				closeSide := "Buy" // SHORT → Buy to close
				if sr.strategy.Direction == DirectionLong {
					closeSide = "Sell"
				}
				qty := trader.FormatQty(exchangeSize, sr.instr.QtyStep, sr.instr.MinQty)
				if qty != "0" && qty != "" {
					sr.warn(ctx, fmt.Sprintf(
						"Matrix TP: хвостик %.6f лот — закрываю маркетом", exchangeSize))
					if _, err := sr.runner.tradeStream.PlaceOrder(ctx, trader.OrderRequest{
						Symbol:      sr.strategy.Symbol,
						Category:    sr.strategy.Category,
						Side:        closeSide,
						OrderType:   "Market",
						Qty:         qty,
						ReduceOnly:  !sr.strategy.HedgeMode,
						PositionIdx: positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction),
					}); err != nil {
						sr.errlog(ctx, fmt.Sprintf("Matrix tail close: %v", err))
					} else {
						sr.closedBySelf = true // suppress the position-zero event that follows
					}
				}
			} else {
				sr.warn(ctx, fmt.Sprintf(
					"Ghost-позиция обнаружена: биржа=%.6f, наш учёт=0 — закрываю маркетом",
					exchangeSize,
				))
				sr.closeGhostPosition(ctx, exchangeSize)
			}
		}
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
	// If the position was reduced by our own system (closedBySelf=true), log the actual
	// reason instead of "вручную". This handles the race where the position WS event
	// fires before the order-fill WS event.
	if sr.closedBySelf {
		reason := sr.closedByReason
		if reason == "" {
			reason = "системой"
		}
		sr.closedBySelf = false
		sr.closedByReason = ""
		sr.partialCloseQty = exchangeSize
		sr.lastTPSLQty = 0
		sr.lastTPSLAvg = 0
		sr.info(ctx, fmt.Sprintf(
			"Позиция частично закрыта %s (биржа=%.4f, наш учёт=%.4f, avg=%.4f) — пересчитываем TP/SL",
			reason, exchangeSize, ourQty, avg,
		))
		if err := sr.updateTPByType(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Пересчёт TP после частичного закрытия (%s): %v", reason, err))
		}
		if err := sr.updateSL(ctx); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Пересчёт SL после частичного закрытия (%s): %v", reason, err))
		}
		return
	}
	sr.partialCloseQty = exchangeSize
	sr.lastTPSLQty = 0 // force re-place with correct qty
	sr.lastTPSLAvg = 0
	sr.warn(ctx, fmt.Sprintf(
		"Позиция частично закрыта вручную (биржа=%.4f, наш учёт=%.4f, avg=%.4f) — пересчитываем TP/SL",
		exchangeSize, ourQty, avg,
	))
	if err := sr.updateTPByType(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Пересчёт TP после частичного закрытия: %v", err))
	}
	if err := sr.updateSL(ctx); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Пересчёт SL после частичного закрытия: %v", err))
	}
}

// handleTPSLCancelled is called when a TP or SL order belonging to this strategy
// is cancelled on the exchange (manual cancel or exchange action). It warns the user
// and immediately re-places the order.
// cancelType is the Bybit cancelType field from the WS order event (may be empty).
// Must NOT be called with sr.mu held.
func (sr *StrategyRunner) handleTPSLCancelled(ctx context.Context, refType string, cancelType string) {
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

	// CancelByAdminClosing means the exchange itself killed the order — typically
	// a tokenized stock (TSLA, AAPL...) had its market session close outside US
	// trading hours. Retrying immediately is pointless — Bybit will cancel again.
	// We clear the orderID and log an informational message; the next WS reconnect
	// reconcile will re-place the order once the market reopens.
	if cancelType == "CancelByAdminClosing" {
		switch refType {
		case "tp":
			sr.tpOrderID = ""
			sr.warn(ctx, "TP отменён Bybit (закрытие рынка/admin) — будет выставлен при следующей сверке")
		case "sl":
			sr.slOrderID = ""
			sr.warn(ctx, "SL отменён Bybit (закрытие рынка/admin) — будет выставлен при следующей сверке")
		}
		return
	}

	switch refType {
	case "tp":
		sr.tpOrderID = "" // already unregistered by OnOrderEvent
		sr.tpCancelStreak++
		// Circuit breaker: Bybit cancelling our TP repeatedly without a fill means either
		// (a) a reduce-only race — WS cancel arrives before Bybit frees the budget, so the
		//     replacement is immediately cancelled too; or
		// (b) the position is actually zero (TP fill event was dropped from a full queue).
		// After 5 consecutive cancels we stop retrying and log for manual inspection.
		// The tpCancelStreak resets on any successful TP fill or cycle close.
		if sr.tpCancelStreak >= 5 {
			cancelTypeInfo := ""
			if cancelType != "" {
				cancelTypeInfo = fmt.Sprintf(" (cancelType=%s)", cancelType)
			}
			sr.errlog(ctx, fmt.Sprintf(
				"TP отменён Bybit %d раз подряд без исполнения%s — прекращаем попытки. "+
					"Проверьте позицию и TP вручную или дождитесь сверки при переподключении WS.",
				sr.tpCancelStreak, cancelTypeInfo))
			return
		}
		if err := sr.updateTPByType(ctx); err != nil {
			if isPositionZero(err) {
				// Race: TP cancel event arrived before position-0 event.
				// Position is already gone — close the cycle now; the incoming
				// position event will see cycle==nil and exit silently.
				sr.tpCancelStreak = 0
				sr.closePositionExternal(ctx, "биржей (TP отменён, позиция уже закрыта)")
				return
			}
			sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления TP: %v", err))
		} else {
			sr.warn(ctx, "TP ордер отменён биржей или вручную — выставляем повторно")
		}
	case "sl":
		sr.slOrderID = ""
		if err := sr.updateSL(ctx); err != nil {
			if isPositionZero(err) {
				sr.closePositionExternal(ctx, "биржей (SL отменён, позиция уже закрыта)")
				return
			}
			sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления SL: %v", err))
		} else {
			sr.warn(ctx, "SL ордер отменён биржей или вручную — выставляем повторно")
		}
	}
}

func (sr *StrategyRunner) handleLevelCancelled(ctx context.Context, levelID string, cancelledOrderID string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.cycle == nil {
		return
	}

	var lvl *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			lvl = &sr.levels[i]
			break
		}
	}
	if lvl == nil {
		return
	}

	// Stale cancel event: restartCycle already re-placed this level with a new order.
	// The cancelled order is no longer the current one — skip to avoid duplicates.
	if lvl.ExchangeOrderID != cancelledOrderID {
		return
	}

	status := sr.strategy.Status
	if status != StatusActive {
		msg := fmt.Sprintf("Уровень L%d отменён вручную на бирже (стратегия %s)", lvl.LevelIdx, status)
		sr.info(ctx, msg)
		sr.setManualAlert(ctx, msg)
		return
	}

	if lvl.Status != LevelFilled {
		lvl.Status = LevelPending
		lvl.ExchangeOrderID = ""
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL WHERE id=$1`, lvl.ID)
	}

	msg := fmt.Sprintf("Уровень L%d был отменён вручную на бирже — перевыставляем, так как стратегия активна", lvl.LevelIdx)
	sr.warn(ctx, msg)
	sr.setManualAlert(ctx, msg)

	// Bump repriceGen so the new order gets a fresh linkId Bybit has never seen.
	// Without this Bybit returns 110072 (duplicate linkId) because the cancelled
	// order's linkId is still retained by the exchange for some time.
	sr.repriceGen++

	if lvl.Slot != nil {
		// Matrix level — use matrix placement logic (determines Limit vs StopMarket by price).
		price, err := trader.FetchMarkPrice(ctx, sr.runner.creds, sr.strategy.Category, sr.strategy.Symbol)
		if err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка получения цены для перевыставления L%d: %v", lvl.LevelIdx, err))
			return
		}
		if err := sr.placeMatrixLevel(ctx, lvl, price); err != nil {
			sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления L%d: %v", lvl.LevelIdx, err))
		} else {
			sr.info(ctx, fmt.Sprintf("L%d перевыставлен", lvl.LevelIdx))
		}
		return
	}

	if err := sr.placeLevel(ctx, lvl.LevelIdx-1); err != nil {
		sr.errlog(ctx, fmt.Sprintf("Ошибка повторного выставления L%d: %v", lvl.LevelIdx, err))
	} else {
		sr.info(ctx, fmt.Sprintf("L%d перевыставлен", lvl.LevelIdx))
	}
}

func (sr *StrategyRunner) setManualAlert(ctx context.Context, msg string) {
	sr.strategy.ManualAlert = msg
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategies SET manual_alert=$1 WHERE id=$2`, msg, sr.strategy.ID)
}

func (sr *StrategyRunner) clearManualAlert(ctx context.Context) {
	if sr.strategy.ManualAlert == "" {
		return
	}
	sr.strategy.ManualAlert = ""
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategies SET manual_alert=NULL WHERE id=$1`, sr.strategy.ID)
}

// ── Virtual grid level price monitor ─────────────────────────────────────────

// launchGridVirtualMonitor starts a price-watching goroutine for virtual grid levels.
// When the mark price crosses a virtual level's target, it places a market order.
// Uses the same matrixMonitorStop slot as matrix so the monitor is cleaned up on restart/stop.
func (sr *StrategyRunner) launchGridVirtualMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel
	symbol := sr.strategy.Symbol
	category := sr.strategy.Category
	creds := sr.runner.creds
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	// Immediate startup check: execute any levels whose trigger condition is already met
	// (handles server restart where price arrived at target while we were down).
	go func() {
		if price, err := trader.FetchMarkPrice(ctx, creds, category, symbol); err == nil {
			sr.submit(func(taskCtx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				sr.gridVirtualPriceTick(taskCtx, price)
			})
		}
	}()

	if se == nil {
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
					creds := sr.runner.creds
					category := sr.strategy.Category
					price, err := trader.FetchMarkPrice(ctx, creds, category, symbol)
					if err != nil {
						continue
					}
					sr.submit(func(taskCtx context.Context) {
						sr.mu.Lock()
						defer sr.mu.Unlock()
						sr.gridVirtualPriceTick(taskCtx, price)
					})
				}
			}
		}()
		return
	}

	priceCh := make(chan float64, 1)
	go func() {
		unsub := se.PriceHub().Subscribe(symbol, func(markPrice float64) {
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
					sr.gridVirtualPriceTick(taskCtx, price)
				})
			}
		}
	}()
}

// gridVirtualPriceTick checks if any pending virtual grid level's target has been crossed
// and executes a market order for each crossed level. Must be called with sr.mu held.
//
// Crossing detection: if lastVirtualPrice was on the trigger side of a target but current
// price has moved away, we still execute — this catches levels missed due to polling gaps
// (e.g. REST fallback at 5 s intervals) or failed placement on the previous tick.
func (sr *StrategyRunner) gridVirtualPriceTick(ctx context.Context, price float64) {
	if sr.cycle == nil {
		return
	}
	last := sr.lastVirtualPrice
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelPending || !l.ForceVirtual || l.ExchangeOrderID != "" {
			continue
		}
		var crossed bool
		if l.TargetPrice == 0 {
			crossed = true
		} else if l.Side == "Buy" {
			// Current price at/below target, OR last price was at/below target (bounce detection).
			crossed = price <= l.TargetPrice || (last > 0 && last <= l.TargetPrice)
		} else {
			// Current price at/above target, OR last price was at/above target (bounce detection).
			crossed = price >= l.TargetPrice || (last > 0 && last >= l.TargetPrice)
		}
		if !crossed {
			continue
		}
		// Execute at market price
		ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
		linkID := fmt.Sprintf("SIS_STR-%s-%d-%d-%d-v", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx, sr.repriceGen)
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
			sr.errlog(ctx, fmt.Sprintf("Виртуальный L%d: market fill: %v", l.LevelIdx, err))
			continue
		}
		l.Status = LevelPlaced
		l.ExchangeOrderID = result.OrderId
		sr.runner.RegisterOrder(result.OrderId, ref)
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, placed_at=NOW() WHERE id=$3`,
			result.OrderId, linkID, l.ID)
		sr.info(ctx, fmt.Sprintf("Виртуальный L%d %s: маркет @ %.4f (цена достигла %.4f)", l.LevelIdx, l.Side, price, l.TargetPrice))
	}
	sr.lastVirtualPrice = price
}
