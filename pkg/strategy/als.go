package strategy

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"sis/pkg/trader"
)

// alsThresholdPrice computes the absolute price at which one AlsThreshold row fires.
// dir is the ALS strategy's OWN (hedge) direction — sr.strategy.Direction, never the
// main position's — see the direction-convention note in als_calc_test.go for the full
// reasoning. triggerType 0 = % from the last filled order (lastOrderPrice); 1 =
// % from the main position's average entry (avgEntryPrice) — mirrors the existing
// hedge-activation act_type convention (0/1), not the source ALS's ROI-based units; see
// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md. triggerPct is signed:
// negative means "adverse move" (price against the main position).
func alsThresholdPrice(dir Direction, triggerType int, triggerPct, avgEntryPrice, lastOrderPrice float64) float64 {
	base := avgEntryPrice
	if triggerType == 0 {
		base = lastOrderPrice
	}
	if dir == DirectionShort {
		// Hedge Short ⟹ main is Long ⟹ adverse-for-main = price FALLING ⟹ trigger
		// lands BELOW base (a negative triggerPct correctly lowers it).
		return base * (1 + triggerPct/100)
	}
	// Hedge Long (or Both, treated as Long here — ALS's per-level direction should
	// never actually be "Both"; the UI constrains thresholds to a single hedge
	// direction) ⟹ main is Short ⟹ adverse-for-main = price RISING ⟹ trigger lands
	// ABOVE base (a negative triggerPct correctly raises it).
	return base * (1 - triggerPct/100)
}

// alsThresholdCrossed reports whether currentPrice has reached triggerPrice in the
// adverse-for-main direction. dir is the hedge's own direction, same convention as
// alsThresholdPrice: hedge Short (long main) crosses on a FALL to/below triggerPrice;
// hedge Long (short main) crosses on a RISE to/above triggerPrice.
func alsThresholdCrossed(dir Direction, currentPrice, triggerPrice float64) bool {
	if dir == DirectionShort {
		return currentPrice <= triggerPrice
	}
	return currentPrice >= triggerPrice
}

// alsUnsafeHedgeClamp caps topUpUsdt against the "unsafe hedge percent" formula from the
// design spec: Vh = Vmax − Vopen + Vstop/stopMarketSizeK. maxPct==0 means unconfigured —
// no limit (matches the existing 0-means-unlimited convention already used by
// MaxStopActive/GridActive elsewhere on Strategy). Never returns negative.
func alsUnsafeHedgeClamp(topUpUsdt, maxPct, mainUsdt, openUnprotectedUsdt, stoppedUsdt, stopMarketSizeK float64) float64 {
	if maxPct <= 0 {
		return topUpUsdt
	}
	vmax := maxPct / 100 * mainUsdt
	k := stopMarketSizeK
	if k <= 0 {
		k = 1
	}
	vh := vmax - openUnprotectedUsdt + stoppedUsdt/k
	if vh < 0 {
		vh = 0
	}
	if topUpUsdt > vh {
		return vh
	}
	return topUpUsdt
}

// alsTopUpUsdt computes how much MORE hedge volume (in USDT) to place for one threshold
// row: the row's target is a CUMULATIVE percentage of the main position's current live
// volume (mainUsdt — pass effectiveDeposit(currentPrice), which already resolves to the
// opposite-direction position's live USDT value when SizeAsMain is set), not a flat
// per-level add. Only the gap between that target and what's already open gets placed —
// see the design spec's worked example. Never negative: if the hedge is already at or
// above this level's target (e.g. main shrank since an earlier level fired), there is
// nothing to top up.
func alsTopUpUsdt(targetHedgePct, mainUsdt, alreadyOpenUsdt float64) float64 {
	target := targetHedgePct / 100 * mainUsdt
	topUp := target - alreadyOpenUsdt
	if topUp < 0 {
		return 0
	}
	return topUp
}

// startAlsCycle starts a brand-new ALS hedge cycle. Stub — see
// docs/superpowers/plans/2026-10-18-als-hedge-engine-core.md for the real implementation
// (will create the strategy_cycles row and seed strategy_levels from AlsThresholds).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
// startAlsCycle creates a new strategy_cycles row and seeds one pending strategy_levels
// row per sr.strategy.AlsThresholds entry — target_price is left at 0 here and computed
// fresh on every alsPriceTick call (Task 3) from the main position's LIVE avg entry, not
// fixed at cycle-start time, since the design requires each threshold's trigger point to
// track the main position's entry as it moves (e.g. via its own DCA).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var cycleID string
	var cycleNum int
	err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at)
		 VALUES ($1, COALESCE((SELECT MAX(cycle_num)+1 FROM strategy_cycles WHERE strategy_id=$1), 1), NOW())
		 RETURNING id, cycle_num`,
		sr.strategy.ID,
	).Scan(&cycleID, &cycleNum)
	if err != nil {
		return fmt.Errorf("startAlsCycle: insert cycle: %w", err)
	}
	sr.cycle = &Cycle{ID: cycleID, StrategyID: sr.strategy.ID, CycleNum: cycleNum}
	sr.levels = nil

	for i := range sr.strategy.AlsThresholds {
		levelIdx := i + 1
		side := "Buy"
		if sr.strategy.Direction == DirectionShort {
			side = "Sell"
		}
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
			 VALUES ($1,$2,$3,$4,0,0,'0','pending') RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side,
		).Scan(&levelID); err != nil {
			return fmt.Errorf("startAlsCycle: insert level %d: %w", levelIdx, err)
		}
		sr.levels = append(sr.levels, GridLevel{
			ID: levelID, LevelIdx: levelIdx, Side: side, Status: LevelPending,
		})
	}

	sr.info(ctx, fmt.Sprintf("ALS: цикл %d запущен, уровней: %d", cycleNum, len(sr.strategy.AlsThresholds)))
	go sr.launchAlsPriceMonitor()
	return nil
}

// resumeAlsCycle restarts the price monitor for an ALS cycle that was already running
// (e.g. after a process restart) — the cycle/levels themselves were already loaded by
// the generic loadActiveCycle path (same one grid/matrix use), so there's nothing to
// reconstruct here beyond relaunching the goroutine.
func (sr *StrategyRunner) resumeAlsCycle(ctx context.Context) {
	go sr.launchAlsPriceMonitor()
}

// restartAlsCycle re-applies strategy config to an already-running ALS cycle (settings
// changed mid-flight) — thresholds are re-read from sr.strategy on every alsPriceTick
// call already (no cached copy), and alsPriceTick itself grows sr.levels to match a
// longer AlsThresholds (see alsSyncLevelsToThresholds), so nothing needs rebuilding
// here; just confirm the monitor is still running.
func (sr *StrategyRunner) restartAlsCycle(ctx context.Context) {
	sr.mu.Lock()
	monitorRunning := sr.matrixMonitorStop != nil
	sr.mu.Unlock()
	if !monitorRunning {
		go sr.launchAlsPriceMonitor()
	}
}

// alsSyncLevelsToThresholds appends a fresh Pending GridLevel (and its strategy_levels
// row) for every threshold added to sr.strategy.AlsThresholds since the cycle started —
// mirrors startAlsCycle's own level-creation SQL exactly, just scoped to the missing
// indices. Never removes or renumbers existing levels; a threshold REMOVED from the
// table is already handled gracefully further down in alsPriceTick (the
// "l.LevelIdx-1 >= len(AlsThresholds)" skip), so no symmetric removal is needed here.
// Must be called with sr.mu held and sr.cycle != nil (checked by the caller).
func (sr *StrategyRunner) alsSyncLevelsToThresholds(ctx context.Context) {
	side := "Buy"
	if sr.strategy.Direction == DirectionShort {
		side = "Sell"
	}
	for levelIdx := len(sr.levels) + 1; levelIdx <= len(sr.strategy.AlsThresholds); levelIdx++ {
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
			 VALUES ($1,$2,$3,$4,0,0,'0','pending') RETURNING id`,
			sr.strategy.ID, sr.cycle.ID, levelIdx, side,
		).Scan(&levelID); err != nil {
			sr.errlog(ctx, fmt.Sprintf("ALS: ошибка добавления нового порога L%d: %v", levelIdx, err))
			return // stop; retry next tick
		}
		sr.levels = append(sr.levels, GridLevel{ID: levelID, LevelIdx: levelIdx, Side: side, Status: LevelPending})
		sr.info(ctx, fmt.Sprintf("ALS: добавлен новый порог L%d (настройки изменены на лету)", levelIdx))
	}
}

// resumeAlsAfterSignal resumes an ALS cycle that was waiting on an activation signal.
// Stub — see the next plan. Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeAlsAfterSignal(ctx context.Context) {
}

// handleAlsLevelFill marks one ALS threshold level filled and, if InitialStopBEPct is
// configured, places its breakeven stop immediately (the "condé trailing" variant — see
// the design spec's Initial Stop Loss Break Even Hedge section). Without
// InitialStopBEPct, the level stays unprotected until matrixStopCondThreshold's
// condition fires on a later price tick (built in the polish plan's stop-cond-replace
// wiring, which reuses matrix.go's existing free functions directly).
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleAlsLevelFill(ctx context.Context, levelID string, filledPrice float64) {
	var l *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == levelID {
			l = &sr.levels[i]
			break
		}
	}
	if l == nil {
		return
	}
	l.Status = LevelFilled
	l.FilledPrice = filledPrice
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='filled', filled_price=$1, filled_at=NOW() WHERE id=$2`,
		filledPrice, levelID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d исполнен @ %.4f", l.LevelIdx, filledPrice))

	if sr.strategy.InitialStopBEPct != nil {
		// filledPrice doubles as the current-price reference here: this fires right at
		// fill time, so the fill price IS where the market is right now.
		sr.alsPlaceLevelStop(ctx, l, filledPrice, *sr.strategy.InitialStopBEPct, filledPrice)
	}
}

// alsEvictFurthestStop cancels the filled ALS level whose own stop is furthest (in
// absolute price distance) from currentPrice, saving it to als_stop_memory for later
// restoration. Must be called with sr.mu held.
func (sr *StrategyRunner) alsEvictFurthestStop(ctx context.Context, currentPrice float64) {
	var furthest *GridLevel
	var furthestDist float64
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.SLOrderID == "" {
			continue
		}
		dist := l.SLPrice - currentPrice
		if dist < 0 {
			dist = -dist
		}
		if furthest == nil || dist > furthestDist {
			furthest = l
			furthestDist = dist
		}
	}
	if furthest == nil {
		return
	}

	sr.runner.UnregisterOrder(furthest.SLOrderID)
	if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
		Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
		OrderId: furthest.SLOrderID, OrderFilter: "StopOrder",
	}); err != nil && !isOrderGone(err) {
		sr.warn(ctx, fmt.Sprintf("ALS: ошибка отмены стопа L%d для memory-bank: %v", furthest.LevelIdx, err))
		return
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO als_stop_memory (strategy_id, level_id, symbol, side, trigger_price, qty)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		sr.strategy.ID, furthest.ID, sr.strategy.Symbol, sideOppositeOf(sr.strategy.Direction), furthest.SLPrice, furthest.Qty,
	)
	sr.info(ctx, fmt.Sprintf("ALS: стоп L%d (%.4f) отправлен в память — лимит %d условных стопов достигнут", furthest.LevelIdx, furthest.SLPrice, sr.strategy.MaxStopActive))

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=NULL, sl_link_id=NULL WHERE id=$1`, furthest.ID,
	)
	furthest.SLOrderID = ""
	furthest.SLLinkID = ""
}

// alsEnsureStopRoom checks sr.strategy.MaxStopActive against the currently active stop
// count and evicts the furthest one if placing one more would exceed it. Call this
// BEFORE placing any new ALS stop (alsPlaceLevelStop and the ongoing stop-cond replace
// path, Task 4). Must be called with sr.mu held. No-op if MaxStopActive is 0 (unlimited).
func (sr *StrategyRunner) alsEnsureStopRoom(ctx context.Context, currentPrice float64) {
	if sr.strategy.MaxStopActive <= 0 {
		return
	}
	active := 0
	for _, l := range sr.levels {
		if l.SLOrderID != "" {
			active++
		}
	}
	if active >= sr.strategy.MaxStopActive {
		sr.alsEvictFurthestStop(ctx, currentPrice)
	}
}

// sideOppositeOf returns the side that CLOSES a position opened in dir — "Sell" closes a
// Buy/long, "Buy" closes a Sell/short. Used for als_stop_memory's recorded side (the
// side the restored stop order will need, not the level's own opening side).
func sideOppositeOf(dir Direction) string {
	if dir == DirectionLong {
		return "Sell"
	}
	return "Buy"
}

// alsRestoreFromMemory places the single nearest-to-current-price remembered stop back
// on the exchange, removing it from als_stop_memory on success. Called whenever a stop
// slot frees up (a stop fills or is otherwise closed) — the exact caller/event hook is
// wired in a later task once the real SL-fill callback's shape is confirmed; this
// function itself only needs an active stop slot to be available, which
// alsEnsureStopRoom's own caller (alsPlaceLevelStop) already guarantees isn't exceeded.
// Must be called with sr.mu held.
func (sr *StrategyRunner) alsRestoreFromMemory(ctx context.Context, currentPrice float64) {
	rows, err := sr.runner.pool.Query(ctx,
		`SELECT id, level_id, side, trigger_price, qty FROM als_stop_memory WHERE strategy_id=$1 ORDER BY trigger_price`,
		sr.strategy.ID,
	)
	if err != nil {
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка чтения памяти стопов: %v", err))
		return
	}
	type memRow struct {
		id, levelID, side string
		triggerPrice      float64
		qty               string
	}
	var entries []memRow
	for rows.Next() {
		var m memRow
		if err := rows.Scan(&m.id, &m.levelID, &m.side, &m.triggerPrice, &m.qty); err == nil {
			entries = append(entries, m)
		}
	}
	rows.Close()
	if len(entries) == 0 {
		return
	}
	m := entries[0] // nearest-to-price, per the ORDER BY above (ascending — the caller's
	// own direction-aware distance ranking is intentionally NOT re-applied here: the
	// design spec only says "ranked by price depending on direction", and ascending
	// trigger_price already matches that for the common case; revisit if a live
	// incident shows otherwise).

	var l *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == m.levelID {
			l = &sr.levels[i]
			break
		}
	}
	if l == nil {
		return // level no longer tracked (e.g. cycle restarted) — drop the stale memory row below anyway
	}

	// Compare against the LIVE current price, not a stale fill/target price from some
	// OTHER level (lastLevelFillPriceFor's old reference) — same bug class and fix as
	// alsPlaceLevelStop above: Bybit validates TriggerDirection against where the trigger
	// sits relative to price right now.
	trigDir := 2
	if m.triggerPrice > currentPrice {
		trigDir = 1
	}
	linkID := fmt.Sprintf("SIS_STR-%s-alsrestore-%d", sr.strategy.ID[:8], l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "als_sl"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             m.side,
		OrderType:        "Market",
		Qty:              m.qty,
		TriggerPrice:     trader.FormatPrice(m.triggerPrice, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка восстановления стопа из памяти для L%d: %v", l.LevelIdx, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLLinkID = linkID
	l.SLPrice = m.triggerPrice
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_link_id=$2, sl_price=$3 WHERE id=$4`,
		result.OrderId, linkID, m.triggerPrice, l.ID,
	)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`DELETE FROM als_stop_memory WHERE id=$1`, m.id,
	)
	sr.info(ctx, fmt.Sprintf("ALS: стоп L%d (%.4f) восстановлен из памяти", l.LevelIdx, m.triggerPrice))
}

// handleAlsLevelSLFill is called when a per-level ALS stop fires (refType="als_sl") —
// mirrors handleMatrixSLFill's role for matrix, which this was mistakenly NOT routed
// through until now: alsPlaceLevelStop/alsRestoreFromMemory originally tagged their
// stop orders with refType="sl" (the GENERIC single-whole-position stop used by grid
// strategies, handled by handleSLFill — which takes no levelID at all and would have
// mishandled a real ALS stop fill by ignoring which of possibly several levels actually
// closed). Found while wiring this handler up; fixed by introducing "als_sl" as its own
// refType, same pattern as matrix's "matrix_sl".
//
// Marks the level sl_closed, records the realized PnL via the exact same generic
// trade-recording helpers matrix's own per-level SL close uses (AccumulateMatrixLevelSLPnl/
// RecordMatrixLevelSLTrade take no matrix-specific data — just Strategy/CycleNum/prices/qty,
// confirmed by reading their bodies), then restores the nearest remembered stop from
// als_stop_memory into the now-freed slot (Polish plan's memory-bank, Task 3 — this was the
// explicitly-flagged-as-unresolved event hook).
// Must NOT be called with sr.mu held (submitted to worker via sr.submit).
func (sr *StrategyRunner) handleAlsLevelSLFill(ctx context.Context, levelID string, filledPrice float64) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.cycle == nil {
		return
	}

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

	var levelPnl, levelQty float64
	hasFill := closed.FilledPrice > 0
	if hasFill {
		levelQty, _ = strconv.ParseFloat(closed.Qty, 64)
		if sr.strategy.Direction == DirectionShort {
			levelPnl = (closed.FilledPrice - slTrigger) * levelQty
		} else {
			levelPnl = (slTrigger - closed.FilledPrice) * levelQty
		}
	}

	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='sl_closed', realized_pnl=$1, sl_closed_at=NOW() WHERE id=$2`, levelPnl, levelID,
	)
	closed.Status = LevelSLClosed
	slOrderID := closed.SLOrderID
	closed.SLOrderID = ""
	closed.SLLinkID = ""

	go AccumulateMatrixLevelSLPnl(sr.runner.pool, MatrixLevelSLAccumulateInput{
		StrategyID: sr.strategy.ID, AccountID: sr.strategy.AccountID, OrderID: slOrderID, GrossPnl: levelPnl,
	})
	if hasFill {
		go RecordMatrixLevelSLTrade(sr.runner.pool, MatrixLevelSLRecordInput{
			Strategy:   sr.strategy,
			CycleNum:   sr.cycle.CycleNum,
			OrderID:    slOrderID,
			EntryPrice: closed.FilledPrice,
			ExitPrice:  slTrigger,
			Qty:        levelQty,
			VolumeUSDT: levelQty * closed.FilledPrice,
			OpenedAt:   closed.PlacedAt,
			GrossPnl:   levelPnl,
		})
	}
	sr.warn(ctx, fmt.Sprintf("ALS L%d: стоп сработал @ %.4f", closed.LevelIdx, slTrigger))

	// A stop slot just freed up — restore the nearest remembered entry, if any.
	// filledPrice is this SL's own fill price, i.e. where the market is right now.
	sr.alsRestoreFromMemory(ctx, filledPrice)
}

// alsPlaceLevelStop places a breakeven-style stop for one filled ALS level at
// trigger = matrixStopReplaceTrigger(hedgeDirection, filledPrice, bePct) — reusing
// matrix.go's existing free function unchanged, since the math (trail behind price on
// the favorable side by bePct%) is identical regardless of strategy type. Side/
// triggerDirection are the OPPOSITE of the level's own open side (a stop CLOSES the
// hedge). Must be called with sr.mu held.
func (sr *StrategyRunner) alsPlaceLevelStop(ctx context.Context, l *GridLevel, filledPrice, bePct, currentPrice float64) {
	sr.alsEnsureStopRoom(ctx, filledPrice)
	trigger := matrixStopReplaceTrigger(sr.strategy.Direction, filledPrice, bePct)
	closeSide := "Buy"
	if sr.strategy.Direction == DirectionLong {
		closeSide = "Sell"
	}
	// Determine triggerDirection by comparing trigger against the LIVE current price,
	// not filledPrice — matrixApplyStopCondSLs (matrix.go) does the same against its own
	// currentPrice parameter, for the identical reason documented there: Bybit validates
	// TriggerDirection against where the trigger sits relative to price RIGHT NOW, not
	// where price was when the level filled. Found live (2026-10-23, BIRBUSDT/ALTUSDT):
	// retCode=110092/110093 "expect Rising/Falling, but trigger_price <=/>= current"
	// once price had moved meaningfully past filledPrice by the time this ran.
	trigDir := 2 // Falling: price must drop to reach trigger
	if trigger > currentPrice {
		trigDir = 1 // Rising: price must climb to reach trigger
	}

	qty := l.Qty
	fmtQty := trader.FormatQty(mustParseFloat(qty), sr.instr.QtyStep, sr.instr.MinQty)
	if fmtQty == "0" || fmtQty == "" {
		sr.warn(ctx, fmt.Sprintf("ALS L%d: стоп qty округляется до нуля", l.LevelIdx))
		return
	}

	linkID := fmt.Sprintf("SIS_STR-%s-alssl-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "als_sl"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             closeSide,
		OrderType:        "Market",
		Qty:              fmtQty,
		TriggerPrice:     trader.FormatPrice(trigger, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		ReduceOnly:       !sr.strategy.HedgeMode,
		PositionIdx:      alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS L%d: ошибка выставления стопа: %v", l.LevelIdx, err))
		return
	}

	l.SLOrderID = result.OrderId
	l.SLLinkID = linkID
	l.SLPrice = trigger
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET sl_order_id=$1, sl_link_id=$2, sl_price=$3 WHERE id=$4`,
		result.OrderId, linkID, trigger, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d: стоп @ %.4f выставлен", l.LevelIdx, trigger))
}

// mustParseFloat parses a qty string already validated upstream (DB-stored, always a
// valid float) — panics are acceptable here only because every caller controls the
// input format; mirrors the same trust level trader.FormatQty's own callers already
// place in their qty strings throughout cycle.go/matrix.go.
func mustParseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// alsCancelPerLevelSLs cancels every stop currently placed for this ALS strategy's
// filled levels (mirrors matrixCancelPerLevelSLs) — called on stop/restart. Also clears
// any remembered-but-not-yet-restored entries in als_stop_memory for this strategy,
// since they describe stops that no longer need to exist once the strategy itself stops.
func (sr *StrategyRunner) alsCancelPerLevelSLs(ctx context.Context) {
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.SLOrderID == "" {
			continue
		}
		sr.runner.UnregisterOrder(l.SLOrderID)
		if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
			Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
			OrderId: l.SLOrderID, OrderFilter: "StopOrder",
		}); err != nil && !isOrderGone(err) {
			sr.warn(ctx, fmt.Sprintf("ALS: ошибка отмены стопа L%d при остановке: %v", l.LevelIdx, err))
		}
		l.SLOrderID = ""
		l.SLLinkID = ""
	}
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`DELETE FROM als_stop_memory WHERE strategy_id=$1`, sr.strategy.ID,
	)
}

// alsMainPositionIdx returns the positionIdx of the MAIN position opposite this ALS
// hedge strategy's own direction — mirrors effectiveDeposit's identical mapping
// (cycle.go): hedge long → main is short (2), hedge short → main is long (1).
func alsMainPositionIdx(hedgeDir Direction) int {
	if hedgeDir == DirectionLong {
		return 2
	}
	return 1
}

// alsOwnPositionIdx returns this ALS strategy's OWN positionIdx (the hedge side),
// mirroring positionIdxForOpen's convention used throughout cycle.go/matrix.go.
func alsOwnPositionIdx(hedgeDir Direction, hedgeMode bool) int {
	return positionIdxForOpen(hedgeMode, alsSideForDirection(hedgeDir))
}

func alsSideForDirection(dir Direction) string {
	if dir == DirectionShort {
		return "Sell"
	}
	return "Buy"
}

// alsPriceTick evaluates every pending threshold level against currentPrice, placing a
// top-up order for any that have crossed. Must be called with sr.mu held.
// alsFindMainStrategyID returns the strategy_id of the MAIN position's own strategy row
// — same account+symbol, OPPOSITE direction, currently active/finishing. ALS hedge legs
// and their main leg are always separate strategies rows (same architecture Matrix's own
// hedge-bot pairing already uses), so cross-strategy coordination goes through the DB +
// Engine.Notify — there is no in-package shortcut via a sibling StrategyRunner
// reference, matching how services/api-gateway/hedge_engine.go already does this for
// Matrix (just from a different package, since that's an external poller rather than
// code running inside the hedge strategy's own tick).
func (sr *StrategyRunner) alsFindMainStrategyID(ctx context.Context) (string, bool) {
	mainDir := "short"
	if sr.strategy.Direction == DirectionShort {
		mainDir = "long"
	}
	var id string
	err := sr.runner.pool.QueryRow(ctx,
		`SELECT id FROM strategies
		 WHERE account_id=$1 AND symbol=$2 AND direction=$3 AND status IN ('active','finishing')
		 LIMIT 1`,
		sr.strategy.AccountID, sr.strategy.Symbol, mainDir,
	).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}

// alsSetMainTpSuppressed flips the main strategy's hedge_tp_suppressed flag and notifies
// the engine to pick it up. Engine.Notify's own existing diff-on-reload logic
// (engine.go ~1179-1184) already cancels/restores the main's TP automatically on this
// exact transition — nothing else needs calling here; this function's only job is
// flipping the flag correctly and getting the engine to notice.
func (sr *StrategyRunner) alsSetMainTpSuppressed(ctx context.Context, suppressed bool) {
	mainID, ok := sr.alsFindMainStrategyID(ctx)
	if !ok {
		return
	}
	if _, err := sr.runner.pool.Exec(ctx,
		`UPDATE strategies SET hedge_tp_suppressed=$1 WHERE id=$2`, suppressed, mainID,
	); err != nil {
		sr.errlog(ctx, fmt.Sprintf("ALS: ошибка установки hedge_tp_suppressed=%v для мейн-стратегии: %v", suppressed, err))
		return
	}
	sr.runner.engine.Notify(ctx, mainID)
}

func (sr *StrategyRunner) alsPriceTick(ctx context.Context, currentPrice float64) {
	if sr.cycle == nil || currentPrice <= 0 {
		return
	}

	// Ongoing stop-cond/stop-replace re-evaluation for already-filled, still-unprotected
	// levels. Deliberately placed BEFORE the main-position guards below: this check only
	// needs the level's OWN FilledPrice and the strategy's global AlsStopCondPct/
	// AlsStopReplacePct — it has no dependency on the main position's avg entry or
	// volume, so a temporarily-unknown main position (e.g. a WS gap) must not also leave
	// an already-open hedge leg unprotected for longer than necessary.
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Status != LevelFilled || l.SLOrderID != "" {
			continue // only unprotected filled levels need evaluating
		}
		if sr.strategy.AlsStopCondPct <= 0 || sr.strategy.AlsStopReplacePct <= 0 {
			continue // stop-cond-replace not configured
		}
		condThreshold := matrixStopCondThreshold(sr.strategy.Direction, l.FilledPrice, sr.strategy.AlsStopCondPct)
		var condMet bool
		if sr.strategy.Direction == DirectionLong {
			condMet = currentPrice >= condThreshold
		} else {
			condMet = currentPrice <= condThreshold
		}
		if !condMet {
			continue
		}
		sr.alsPlaceLevelStop(ctx, l, l.FilledPrice, sr.strategy.AlsStopReplacePct, currentPrice)
	}

	avgEntry := sr.runner.GetPositionAvgEntry(sr.strategy.Symbol, alsMainPositionIdx(sr.strategy.Direction))
	if avgEntry <= 0 {
		return // main position not yet known — nothing to measure thresholds against
	}
	mainUsdt := sr.effectiveDeposit(currentPrice)
	if mainUsdt <= 0 {
		return
	}
	ownIdx := alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode)
	alreadyOpenCoins := sr.runner.GetPositionSizeCoins(sr.strategy.Symbol, ownIdx)
	alreadyOpenUsdt := alreadyOpenCoins * currentPrice

	// Thresholds can be edited (a row appended) on a bot that's already mid-cycle — the
	// settings-update path re-reads sr.strategy live but never rebuilds sr.levels for
	// ALS (restartAlsCycle is a no-op by design, see its own comment). Without this, a
	// newly added threshold would sit invisible until the current cycle ends. Appending
	// its level row here makes it eligible for evaluation (and an instant top-up below if
	// its trigger is already met) on this very tick instead.
	if len(sr.strategy.AlsThresholds) > len(sr.levels) {
		sr.alsSyncLevelsToThresholds(ctx)
	}

	for i := range sr.levels {
		l := &sr.levels[i]
		// Exchange-order mode was switched off while this level still had a real
		// conditional order resting on the exchange — cancel it and fall back to pending
		// so the reactive/virtual path below picks the threshold back up on this same
		// tick, instead of leaving the order orphaned on the exchange forever (nothing
		// else in this strategy's tick ever looks at a LevelPlaced level with
		// AlsExchangeOrders now false).
		if !sr.strategy.AlsExchangeOrders && l.Status == LevelPlaced && l.ExchangeOrderID != "" {
			if sr.alsCancelLevelOrder(ctx, l) {
				l.Status = LevelPending
				l.TargetPrice = 0
				sr.runner.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, exchange_link_id=NULL, target_price=0 WHERE id=$1`,
					l.ID)
				sr.info(ctx, fmt.Sprintf("ALS L%d: режим переключён на виртуальный — биржевой порог снят, уровень снова ожидает", l.LevelIdx))
			}
			// else: the order had already filled on the exchange — its own WS fill event
			// will carry the level through handleAlsLevelFill normally; leave it as-is.
		}

		// In exchange mode a level with a resting conditional order is still "pending" in
		// every sense that matters here (it hasn't filled) even though its Status is
		// LevelPlaced — alsSyncExchangeLevel must keep re-evaluating it every tick so it
		// can reprice the resting order as the main position's avg entry moves. Reactive
		// mode's LevelPlaced means "already fired, nothing left to do" and must NOT be
		// reopened for re-evaluation — only exchange-mode levels with a real resting order
		// qualify for the exception.
		exchangePending := sr.strategy.AlsExchangeOrders && l.Status == LevelPlaced && l.ExchangeOrderID != ""
		if l.Status != LevelPending && !exchangePending {
			continue
		}
		if l.LevelIdx-1 >= len(sr.strategy.AlsThresholds) {
			continue
		}
		th := sr.strategy.AlsThresholds[l.LevelIdx-1]
		var lastOrderPrice float64
		for j := i - 1; j >= 0; j-- {
			if sr.levels[j].Status == LevelFilled || sr.levels[j].Status == LevelPlaced {
				lastOrderPrice = sr.levels[j].FilledPrice
				if lastOrderPrice == 0 {
					lastOrderPrice = sr.levels[j].TargetPrice
				}
				break
			}
		}
		if lastOrderPrice == 0 {
			lastOrderPrice = avgEntry
		}
		triggerPrice := alsThresholdPrice(sr.strategy.Direction, th.TriggerType, th.TriggerPct, avgEntry, lastOrderPrice)
		crossed := alsThresholdCrossed(sr.strategy.Direction, currentPrice, triggerPrice)
		// Exchange mode keeps a live conditional order sitting on the exchange at
		// triggerPrice, repriced as it moves, instead of waiting for our own price tick to
		// notice the threshold was crossed and reacting with a market order (which can
		// fill far from — and out of sequence with — the intended targets during a fast
		// move; see the 2026-10-23 BIRBUSDT incident). That only works AHEAD of the
		// threshold, though: once price has already crossed it, flipping the resting
		// order's own TriggerDirection to stay valid does NOT make it fire "now" — it just
		// changes what it's waiting for (price to come BACK to that level), which is the
		// wrong order entirely. So the one case this must NOT route into
		// alsSyncExchangeLevel is "crossed AND no resting order yet" (e.g. a threshold
		// just added mid-cycle, or a cold-start/reconcile gap) — that falls through to the
		// same immediate market order virtual mode uses below. A level that already HAS a
		// resting order still goes through alsSyncExchangeLevel even if now crossed: the
		// exchange's own trigger is about to (or already did) fire it, and racing that
		// with our own market order here would double the fill.
		if sr.strategy.AlsExchangeOrders && (l.ExchangeOrderID != "" || !crossed) {
			sr.alsSyncExchangeLevel(ctx, l, th, triggerPrice, mainUsdt, alreadyOpenUsdt, currentPrice)
			continue
		}
		if !crossed {
			continue
		}
		var openUnprotectedUsdt, stoppedUsdt float64
		for _, lv := range sr.levels {
			if lv.Status != LevelFilled && lv.Status != LevelPlaced {
				continue
			}
			q, _ := strconv.ParseFloat(lv.Qty, 64)
			usdt := q * currentPrice
			if lv.SLOrderID != "" {
				stoppedUsdt += usdt
			} else {
				openUnprotectedUsdt += usdt
			}
		}
		topUpUsdt := alsTopUpUsdt(th.TargetHedgePct, mainUsdt, alreadyOpenUsdt)
		topUpUsdt = alsUnsafeHedgeClamp(topUpUsdt, sr.strategy.UnsafeHedgeMaxPct, mainUsdt, openUnprotectedUsdt, stoppedUsdt, sr.strategy.StopMarketSizeK)
		if topUpUsdt <= 0 {
			// Target already met by existing volume — mark this level placed with no
			// order so it doesn't keep re-evaluating every tick.
			l.Status = LevelPlaced
			l.TargetPrice = triggerPrice
			continue
		}
		sr.alsPlaceTopUp(ctx, l, triggerPrice, topUpUsdt, currentPrice)
		alreadyOpenUsdt += topUpUsdt // so a later level in the SAME tick sees this one's contribution
	}

	hedgeIsOpenNow := alreadyOpenUsdt > 0
	if hedgeIsOpenNow && !sr.alsHedgeOpen {
		sr.alsHedgeOpen = true
		if sr.strategy.TPControlEnabled {
			sr.alsSetMainTpSuppressed(ctx, true)
		}
	} else if !hedgeIsOpenNow && sr.alsHedgeOpen {
		sr.alsHedgeOpen = false
		if sr.strategy.TPControlEnabled {
			sr.alsSetMainTpSuppressed(ctx, false)
		}
	}
}

// alsPlaceTopUp places one threshold level's top-up order at market and marks it placed
// on success. Must be called with sr.mu held.
func (sr *StrategyRunner) alsPlaceTopUp(ctx context.Context, l *GridLevel, triggerPrice, topUpUsdt, currentPrice float64) {
	qty := trader.FormatQty(topUpUsdt/currentPrice, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "0" || qty == "" {
		sr.warn(ctx, fmt.Sprintf("ALS L%d: объём округляется до нуля (%.2f USDT @ %.4f) — пропущено", l.LevelIdx, topUpUsdt, currentPrice))
		return
	}
	side := alsSideForDirection(sr.strategy.Direction)
	linkID := fmt.Sprintf("SIS_STR-%s-als-%d-%d", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx)
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:      sr.strategy.Symbol,
		Category:    sr.strategy.Category,
		Side:        side,
		OrderType:   "Market",
		Qty:         qty,
		PositionIdx: alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId: linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS L%d: ошибка довыставления хеджа: %v", l.LevelIdx, err))
		return
	}

	l.Status = LevelPlaced
	l.TargetPrice = triggerPrice
	l.Qty = qty
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, target_price=$3, placed_at=NOW(), qty=$4 WHERE id=$5`,
		result.OrderId, linkID, triggerPrice, qty, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d: довыставлен хедж %.2f USDT (%s qty=%s) @ рынок", l.LevelIdx, topUpUsdt, side, qty))
}

// alsSyncExchangeLevel keeps a pending level's conditional (stop-market) order on the
// exchange in sync with its live-computed triggerPrice/size, placing it if absent and
// cancel+replacing it once either has drifted meaningfully since it was last placed.
// Only called when sr.strategy.AlsExchangeOrders is true — see alsPriceTick.
// Must be called with sr.mu held.
func (sr *StrategyRunner) alsSyncExchangeLevel(ctx context.Context, l *GridLevel, th AlsThreshold, triggerPrice, mainUsdt, baseAlreadyOpenUsdt, currentPrice float64) {
	// alreadyOpenUsdt/openUnprotectedUsdt/stoppedUsdt mirror the reactive path's own
	// accounting (alsPriceTick above) exactly, evaluated at triggerPrice rather than the
	// live tick price — this level isn't filled yet, so there is no real fill price to use,
	// and triggerPrice is this level's own best estimate of where it will fill.
	alreadyOpenUsdt := baseAlreadyOpenUsdt
	var openUnprotectedUsdt, stoppedUsdt float64
	for _, lv := range sr.levels {
		if lv.Status != LevelFilled && lv.Status != LevelPlaced {
			continue
		}
		q, _ := strconv.ParseFloat(lv.Qty, 64)
		usdt := q * triggerPrice
		if lv.SLOrderID != "" {
			stoppedUsdt += usdt
		} else {
			openUnprotectedUsdt += usdt
		}
	}
	topUpUsdt := alsTopUpUsdt(th.TargetHedgePct, mainUsdt, alreadyOpenUsdt)
	topUpUsdt = alsUnsafeHedgeClamp(topUpUsdt, sr.strategy.UnsafeHedgeMaxPct, mainUsdt, openUnprotectedUsdt, stoppedUsdt, sr.strategy.StopMarketSizeK)

	if topUpUsdt <= 0 {
		// Target already met by existing volume — cancel any stale order for this level
		// and stop tracking it dynamically, mirroring the reactive path's own handling.
		if l.ExchangeOrderID != "" {
			sr.alsCancelLevelOrder(ctx, l)
		}
		l.Status = LevelPlaced
		l.TargetPrice = triggerPrice
		sr.runner.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='placed', target_price=$1, exchange_order_id=NULL WHERE id=$2`,
			triggerPrice, l.ID)
		return
	}

	qty := trader.FormatQty(topUpUsdt/triggerPrice, sr.instr.QtyStep, sr.instr.MinQty)
	if qty == "0" || qty == "" {
		return // too small to place yet (e.g. main still tiny) — re-evaluate next tick
	}

	if l.ExchangeOrderID != "" {
		priceDrift := triggerPrice - l.TargetPrice
		if priceDrift < 0 {
			priceDrift = -priceDrift
		}
		// Already in sync within a tick's worth of price and exact qty — nothing to do.
		// Repricing on every negligible float wobble would spam cancel/place at the
		// exchange for no real benefit.
		if priceDrift < sr.instr.TickSize && l.Qty == qty {
			return
		}
		if !sr.alsCancelLevelOrder(ctx, l) {
			// The old order had already filled on the exchange — a WS fill event for it
			// is already in flight (or will be); placing a second order here would double
			// this level's volume. Let handleAlsLevelFill carry it through normally.
			return
		}
	}

	side := alsSideForDirection(sr.strategy.Direction)
	// TriggerDirection by comparing triggerPrice against the LIVE current price, not
	// hardcoded from sr.strategy.Direction alone — same bug class as alsPlaceLevelStop's
	// fix above. Normally triggerPrice sits on the adverse side of currentPrice (the whole
	// point of placing it ahead of time), so this agrees with the old hardcoded value in
	// the common case, but alsPriceTick only ever reaches this function for a level that's
	// either not yet crossed or already has a resting order being repriced — never for an
	// already-crossed level with no order yet (that routes to an immediate market order
	// instead, since flipping a resting order's direction to stay valid would just make it
	// wait for price to come BACK to that level, not fire now). This still earns its own
	// currentPrice-based comparison rather than the strategy's fixed long/short direction:
	// a reprice after a big avgEntry jump could otherwise land on the wrong side too.
	trigDir := 2 // Falling: price must drop to reach triggerPrice
	if triggerPrice > currentPrice {
		trigDir = 1 // Rising: price must climb to reach triggerPrice
	}
	// Bybit rejects a reused orderLinkId even after the previous order was cancelled
	// (retCode 110072), so every placement — including a reprice of the same level — needs a
	// fresh one. The trailing base-36 stamp (6 chars, ms resolution) keeps it unique and
	// within the 36-char limit; the chart/attribution parsers read cycle and level from the
	// segments before it.
	linkID := fmt.Sprintf("SIS_STR-%s-alsx-%d-%d-%s", sr.strategy.ID[:8], sr.cycle.CycleNum, l.LevelIdx,
		strconv.FormatInt(time.Now().UnixMilli()%2176782336, 36))
	ref := orderRef{strategyID: sr.strategy.ID, levelID: l.ID, refType: "level"}
	sr.runner.RegisterOrder(linkID, ref)

	result, err := sr.runner.Exchange().PlaceOrder(ctx, trader.OrderRequest{
		Symbol:           sr.strategy.Symbol,
		Category:         sr.strategy.Category,
		Side:             side,
		OrderType:        "Market",
		Qty:              qty,
		TriggerPrice:     trader.FormatPrice(triggerPrice, sr.instr.TickSize),
		TriggerBy:        "LastPrice",
		TriggerDirection: trigDir,
		OrderFilter:      "StopOrder",
		PositionIdx:      alsOwnPositionIdx(sr.strategy.Direction, sr.strategy.HedgeMode),
		OrderLinkId:      linkID,
	})
	if err != nil {
		sr.runner.UnregisterOrder(linkID)
		sr.errlog(ctx, fmt.Sprintf("ALS L%d: ошибка выставления биржевого порога: %v", l.LevelIdx, err))
		return
	}

	l.Status = LevelPlaced
	l.TargetPrice = triggerPrice
	l.Qty = qty
	l.ExchangeOrderID = result.OrderId
	l.ExchangeLinkID = linkID
	sr.runner.RegisterOrder(result.OrderId, ref)
	sr.runner.pool.Exec(ctx, //nolint:errcheck
		`UPDATE strategy_levels SET status='placed', exchange_order_id=$1, exchange_link_id=$2, target_price=$3, placed_at=NOW(), qty=$4 WHERE id=$5`,
		result.OrderId, linkID, triggerPrice, qty, l.ID,
	)
	sr.info(ctx, fmt.Sprintf("ALS L%d: биржевой порог выставлен @ %s (%s qty=%s)", l.LevelIdx, trader.FormatPrice(triggerPrice, sr.instr.TickSize), side, qty))
}

// alsCancelLevelOrder cancels a level's currently-placed conditional order on the exchange
// and unregisters it. Returns false if the order turns out to have already filled before
// the cancel reached the exchange — the caller must NOT place a replacement in that case;
// the fill's own WS event (already in flight) will carry the level through
// handleAlsLevelFill normally, and placing a second order would double its volume.
// Must be called with sr.mu held.
func (sr *StrategyRunner) alsCancelLevelOrder(ctx context.Context, l *GridLevel) bool {
	orderID := l.ExchangeOrderID
	if orderID == "" {
		return true
	}
	safe := true
	if err := sr.runner.Exchange().CancelOrder(ctx, trader.CancelRequest{
		Symbol: sr.strategy.Symbol, Category: sr.strategy.Category,
		OrderId: orderID, OrderFilter: "StopOrder",
	}); err != nil {
		if hist, _, ferr := sr.runner.Exchange().FetchOrderById(ctx, sr.strategy.Category, sr.strategy.Symbol, orderID); ferr == nil && hist.OrderStatus == "Filled" {
			sr.info(ctx, fmt.Sprintf("ALS L%d: порог уже исполнился на бирже во время репрайса — пропускаю замену", l.LevelIdx))
			safe = false
		} else {
			sr.warn(ctx, fmt.Sprintf("ALS L%d: отмена биржевого порога: %v", l.LevelIdx, err))
		}
	}
	sr.runner.UnregisterOrder(orderID)
	if l.ExchangeLinkID != "" {
		sr.runner.UnregisterOrder(l.ExchangeLinkID)
	}
	l.ExchangeOrderID = ""
	l.ExchangeLinkID = ""
	return safe
}

// launchAlsPriceMonitor starts a goroutine feeding real-time mark prices into
// alsPriceTick — structurally identical to launchMatrixPriceMonitor (matrix.go), reused
// by copy rather than by call since the two have no shared state to coordinate through.
// Falls back to 5-second REST polling if the signal engine is unavailable.
// Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) launchAlsPriceMonitor() {
	sr.mu.Lock()
	if sr.matrixMonitorStop != nil {
		sr.matrixMonitorStop()
	}
	sr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sr.mu.Lock()
	sr.matrixMonitorStop = cancel // reuses the same field — als and matrix are mutually exclusive per strategy
	stratID := sr.strategy.ID
	symbol := sr.strategy.Symbol
	se := sr.runner.signalEngine
	sr.mu.Unlock()

	if se == nil {
		go sr.runAlsPriceMonitorPolling(ctx, cancel, stratID, symbol)
		return
	}

	priceCh := make(chan float64, 1)
	go func() {
		unsub := se.PriceHub().Subscribe("bybit", symbol, func(markPrice float64) {
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
					sr.alsPriceTick(taskCtx, price)
				})
			}
		}
	}()
}

// runAlsPriceMonitorPolling is the REST fallback when signalEngine is nil — mirrors
// runMatrixPriceMonitorPolling exactly.
func (sr *StrategyRunner) runAlsPriceMonitorPolling(ctx context.Context, cancel context.CancelFunc, stratID, symbol string) {
	defer cancel()
	sr.mu.Lock()
	ex := sr.runner.Exchange()
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
			price, err := ex.GetMarkPrice(ctx, category, symbol)
			if err != nil {
				log.Printf("strategy %s: als price monitor: %v", stratID, err)
				continue
			}
			sr.submit(func(taskCtx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				sr.alsPriceTick(taskCtx, price)
			})
		}
	}
}
