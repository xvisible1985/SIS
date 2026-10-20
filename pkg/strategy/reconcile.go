package strategy

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"sis/pkg/trader"
)

// startReconcileLoop runs reconcile() after a brief startup delay, then every 20 seconds.
// The delay lets loadOrStart goroutines populate orderIndex before the first orphan scan —
// without it, all exchange orders look like orphans and get cancelled immediately on restart.
func (ar *AccountRunner) startReconcileLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	ar.tryReconcile(ctx)

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ar.tryReconcile(ctx)
		}
	}
}

// reconcile checks exchange state against DB/in-memory state and fixes anomalies.
// Only covers Grid strategies — other types have their own reconcile logic.
// Silent when everything matches; logs only when anomalies are found and fixed.
func (ar *AccountRunner) reconcile(ctx context.Context) {
	// --- 0. Kill zombie runners — in-memory strategies deleted from DB ---
	// Covers the case where a client deletes a strategy from the DB while its runner is
	// still live in memory (e.g. after a restart or a race with ForceRemoveStrategy).
	// NOTE: stopped strategies are kept in memory intentionally — they manage TP/SL for
	// open positions. Only strategies that no longer EXIST in DB are treated as zombies.
	ar.mu.RLock()
	inMemIDs := make([]string, 0, len(ar.strategies))
	for id := range ar.strategies {
		inMemIDs = append(inMemIDs, id)
	}
	ar.mu.RUnlock()
	if len(inMemIDs) > 0 {
		existsRows, err := ar.pool.Query(ctx,
			`SELECT id::text FROM strategies WHERE id = ANY($1::uuid[])`,
			inMemIDs,
		)
		if err == nil {
			existsInDB := make(map[string]bool, len(inMemIDs))
			for existsRows.Next() {
				var id string
				if existsRows.Scan(&id) == nil {
					existsInDB[id] = true
				}
			}
			existsRows.Close()
			for _, id := range inMemIDs {
				if existsInDB[id] {
					continue
				}
				ar.mu.RLock()
				sr, ok := ar.strategies[id]
				ar.mu.RUnlock()
				if !ok {
					continue
				}
				id8 := id
				if len(id8) > 8 {
					id8 = id8[:8]
				}
				log.Printf("strategy reconcile: zombie runner %s (удалена из БД) — отменяю ордера и останавливаю", id8)
				go func(sr *StrategyRunner, stratID string) {
					sr.cancelAllStrategyOrders(ctx)
					ar.removeStrategy(stratID)
				}(sr, id)
			}
		}
	}

	// --- 0b. Revive split-brain cycles — an external process (e.g. closed_pnl_syncer's
	// zombie-cycle heuristic) force-closed a cycle in strategy_cycles directly in the DB
	// while this runner was still alive and actively managing it — a "false ghost close"
	// that bypassed closeCycle/maybeRestart entirely, so the runner's in-memory sr.cycle
	// never got cleared and status never moved to 'stopped'. Found live (2026-09-24,
	// MUBARAKUSDT/pol): the runner kept placing/replacing per-level SL orders for 14+ hours
	// after strategy_cycles.ended_at was set from outside, leaving the exchange position
	// and its orders completely unmanaged afterward for over a week once the runner itself
	// eventually gave up.
	//
	// This complements services/api-gateway's checkMatrixZombieStrategies/
	// reviveMatrixSplitBrain, which only scans bot-attached strategy_type='matrix' rows —
	// this check instead covers every live runner for this account regardless of
	// strategy_type or bot attachment, since the invariant it protects ("the DB must not
	// disagree with a runner that's still alive and in charge") has nothing to do with bots.
	//
	// No race with a genuine closeCycle: closeCycle holds sr.mu for its entire body
	// (DB update through sr.cycle=nil), so by the time this RLock below is acquired, a
	// legitimately-just-ended cycle has already had sr.cycle cleared — cycleID comes back
	// empty and is skipped. Only an external DB write (never touching sr.mu) can produce
	// "sr.cycle still set" alongside "DB says ended".
	ar.mu.RLock()
	liveRunners := make([]*StrategyRunner, 0, len(ar.strategies))
	for _, sr := range ar.strategies {
		liveRunners = append(liveRunners, sr)
	}
	ar.mu.RUnlock()
	for _, sr := range liveRunners {
		sr.mu.RLock()
		cycleID, cycleNum, stratID := "", 0, sr.strategy.ID
		if sr.cycle != nil {
			cycleID, cycleNum = sr.cycle.ID, sr.cycle.CycleNum
		}
		sr.mu.RUnlock()
		if cycleID == "" {
			continue
		}
		var endedAt *time.Time
		var hasFilled bool
		if err := ar.pool.QueryRow(ctx,
			`SELECT c.ended_at, EXISTS(SELECT 1 FROM strategy_levels WHERE cycle_id=c.id AND status='filled')
			 FROM strategy_cycles c WHERE c.id=$1`, cycleID,
		).Scan(&endedAt, &hasFilled); err != nil || endedAt == nil || !hasFilled {
			continue // not ended externally, query failed, or nothing worth preserving
		}
		if _, err := ar.pool.Exec(ctx,
			`UPDATE strategy_cycles SET ended_at=NULL, result=NULL WHERE id=$1`, cycleID,
		); err != nil {
			continue
		}
		ar.pool.Exec(ctx, //nolint:errcheck
			`DELETE FROM trade_history WHERE strategy_id=$1 AND cycle_num=$2 AND result='ghost_close'`,
			stratID, cycleNum)
		sr.warn(ctx, "Цикл оживлён: был закрыт извне (ghost_close), пока я продолжал им управлять — ложное закрытие")
	}

	// --- 0c. Catch externally-closed positions on exchanges with no position push stream ---
	// handlePositionClose/handlePositionCloseRetry (cycle.go) — the only code that detects
	// "this strategy's position just went to zero" and ends the cycle — is driven entirely
	// by Bybit's private WS "position" topic (AccountRunner.OnPositionEvent). For every
	// other exchange that stream is never started at all (see AccountRunner.run's own
	// comment: "tradeStream ... and RunPrivateStream ... are both Bybit-specific"), so a
	// closed Binance position was never detected — the cycle stayed open, the filled level
	// stayed 'filled', and anything derived from that state (the strategy card, the chart's
	// SL overlay) stayed stale forever, until the process was restarted. Found live
	// (2026-10-19, account N2 Binance, TERUSDT/NEARUSDT/1000TURBOUSDT).
	//
	// handlePositionCloseRetry is already exchange-agnostic (confirms against
	// trader.Exchange.FetchPositions before closing anything) and already a safe no-op
	// when the position is genuinely still open or there's nothing filled to close — so
	// polling it here every reconcile tick for non-Bybit accounts is the minimal fix:
	// reuse the existing, already-tested confirm-then-close path instead of duplicating it.
	if ar.exchangeName != "bybit" {
		for _, sr := range liveRunners {
			sr.mu.RLock()
			hasPosition := sr.cycle != nil && (sr.tpOrderID != "" || sr.slOrderID != "")
			if !hasPosition {
				for _, l := range sr.levels {
					if l.Status == LevelFilled {
						hasPosition = true
						break
					}
				}
			}
			sr.mu.RUnlock()
			if hasPosition {
				sr.submit(func(ctx context.Context) { sr.handlePositionCloseRetry(ctx) })
			}
		}
	}

	// --- 1. Query placed grid levels ---
	rows, err := ar.pool.Query(ctx,
		`SELECT sl.id, sl.exchange_order_id, sl.strategy_id, s.symbol, s.category
		 FROM strategy_levels sl
		 JOIN strategies s ON s.id = sl.strategy_id
		 WHERE s.account_id = $1 AND s.strategy_type = 'grid' AND sl.status = 'placed'`,
		ar.accountID,
	)
	if err != nil {
		log.Printf("strategy reconcile %s: query levels: %v", ar.accountID, err)
		return
	}
	type placedLevel struct{ levelID, orderID, strategyID, symbol, category string }
	var dbLevels []placedLevel
	for rows.Next() {
		var p placedLevel
		if err := rows.Scan(&p.levelID, &p.orderID, &p.strategyID, &p.symbol, &p.category); err == nil {
			dbLevels = append(dbLevels, p)
		}
	}
	rows.Close()

	// --- 1b. Query placed matrix entry levels ---
	// Matrix has its own fill/repair semantics, so it is queried and handled
	// separately from grid (see block 6b). This closes the gap where a matrix
	// entry order fills on the exchange but the WS fill event is lost — grid
	// self-heals via block 6, matrix previously had no equivalent.
	mRows, mErr := ar.pool.Query(ctx,
		`SELECT sl.id, sl.exchange_order_id, sl.strategy_id, s.symbol, s.category
		 FROM strategy_levels sl
		 JOIN strategies s ON s.id = sl.strategy_id
		 WHERE s.account_id = $1 AND s.strategy_type = 'matrix' AND sl.status = 'placed'`,
		ar.accountID,
	)
	var matrixDbLevels []placedLevel
	if mErr != nil {
		log.Printf("strategy reconcile %s: query matrix levels: %v", ar.accountID, mErr)
	} else {
		for mRows.Next() {
			var p placedLevel
			if err := mRows.Scan(&p.levelID, &p.orderID, &p.strategyID, &p.symbol, &p.category); err == nil {
				matrixDbLevels = append(matrixDbLevels, p)
			}
		}
		mRows.Close()
	}

	// --- 2. Query active Grid cycles with TP/SL order IDs ---
	type cycleTPSL struct {
		cycleID, strategyID, tpOrderID, slOrderID string
	}
	cycleRows, err := ar.pool.Query(ctx,
		`SELECT sc.id, sc.strategy_id,
		        COALESCE(sc.tp_order_id,''), COALESCE(sc.sl_order_id,'')
		 FROM strategy_cycles sc
		 JOIN strategies s ON s.id = sc.strategy_id
		 WHERE s.account_id = $1 AND s.strategy_type = 'grid' AND sc.ended_at IS NULL`,
		ar.accountID,
	)
	if err != nil {
		log.Printf("strategy reconcile %s: query cycles: %v", ar.accountID, err)
		return
	}
	var activeCycles []cycleTPSL
	for cycleRows.Next() {
		var c cycleTPSL
		if err := cycleRows.Scan(&c.cycleID, &c.strategyID, &c.tpOrderID, &c.slOrderID); err == nil {
			activeCycles = append(activeCycles, c)
		}
	}
	cycleRows.Close()

	// --- 2b. Query active Matrix cycles with TP order IDs ---
	// Matrix TP (block 7b below) only needs tp_order_id — SL is per-level, handled
	// entirely by block 8, not by this cycle-level SL self-heal.
	matrixCycleRows, mcErr := ar.pool.Query(ctx,
		`SELECT sc.id, sc.strategy_id, COALESCE(sc.tp_order_id,'')
		 FROM strategy_cycles sc
		 JOIN strategies s ON s.id = sc.strategy_id
		 WHERE s.account_id = $1 AND s.strategy_type = 'matrix' AND sc.ended_at IS NULL`,
		ar.accountID,
	)
	var activeMatrixCycles []cycleTPSL
	if mcErr != nil {
		log.Printf("strategy reconcile %s: query matrix cycles: %v", ar.accountID, mcErr)
	} else {
		for matrixCycleRows.Next() {
			var c cycleTPSL
			if err := matrixCycleRows.Scan(&c.cycleID, &c.strategyID, &c.tpOrderID); err == nil {
				activeMatrixCycles = append(activeMatrixCycles, c)
			}
		}
		matrixCycleRows.Close()
	}

	// --- 3. Query ALL strategy IDs (any status, any type) for orphan scan ---
	// This covers stopped strategies whose orders might still be on the exchange.
	// Matrix strategies use the same SIS_STR-{id8}-{cycleNum}-... linkId format and
	// must be included so their active orders are not misidentified as orphans.
	allStratRows, err := ar.pool.Query(ctx,
		`SELECT id FROM strategies WHERE account_id=$1`,
		ar.accountID,
	)
	if err != nil {
		log.Printf("strategy reconcile %s: query all strategies: %v", ar.accountID, err)
		return
	}
	allStratPrefixes := make(map[string]bool)
	for allStratRows.Next() {
		var id string
		if err := allStratRows.Scan(&id); err == nil {
			id8 := id
			if len(id8) > 8 {
				id8 = id8[:8]
			}
			allStratPrefixes["SIS_STR-"+id8+"-"] = true
		}
	}
	allStratRows.Close()

	// Nothing at all to check → return silently.
	if len(dbLevels) == 0 && len(activeCycles) == 0 && len(allStratPrefixes) == 0 {
		return
	}

	// --- 4. Build in-memory snapshots from running strategy runners ---
	ar.mu.RLock()
	strategyRefs := make(map[string]*StrategyRunner, len(ar.strategies))
	for k, v := range ar.strategies {
		strategyRefs[k] = v
	}
	known := make(map[string]bool, len(ar.orderIndex))
	for id := range ar.orderIndex {
		known[id] = true
	}
	ar.mu.RUnlock()

	// Snapshot each running Grid runner's active cycle number (acquire sr.mu individually).
	type stratSnapshot struct {
		sr       *StrategyRunner
		hasCycle bool
		cycleNum int
	}
	stratByID8 := make(map[string]stratSnapshot, len(strategyRefs))
	for stratID, sr := range strategyRefs {
		id8 := stratID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		sr.mu.Lock()
		snap := stratSnapshot{sr: sr, hasCycle: sr.cycle != nil}
		if sr.cycle != nil {
			snap.cycleNum = sr.cycle.CycleNum
		}
		sr.mu.Unlock()
		stratByID8[id8] = snap
	}

	// --- 5. Fetch exchange orders once (reused for all checks) ---
	exchangeOrders, err := ar.exchange.FetchOpenOrders(ctx)
	if err != nil {
		log.Printf("strategy reconcile %s: fetch exchange orders: %v", ar.accountID, err)
		return
	}
	live := make(map[string]bool, len(exchangeOrders))
	for _, o := range exchangeOrders {
		live[o.OrderId] = true
	}

	// --- 6. Check grid levels: placed in DB but missing from exchange ---
	for _, p := range dbLevels {
		if live[p.orderID] {
			continue
		}
		// Before resetting to pending, check if the order was actually filled on the exchange.
		// Filled orders disappear from open orders just like cancelled ones.
		if hist, _, err := ar.exchange.FetchOrderById(ctx, p.category, p.symbol, p.orderID); err == nil && hist.OrderStatus == "Filled" {
			filledPrice, _ := strconv.ParseFloat(hist.AvgPrice, 64)
			filledQty, _ := strconv.ParseFloat(hist.CumExecQty, 64)
			log.Printf("strategy reconcile: level %s order %s was Filled @ %.4f qty=%.6f — recording fill", p.levelID, p.orderID, filledPrice, filledQty)
			id8 := p.strategyID
			if len(id8) > 8 {
				id8 = id8[:8]
			}
			if snap, ok := stratByID8[id8]; ok {
				levelID := p.levelID
				fp, fq := filledPrice, filledQty
				snap.sr.submit(func(ctx context.Context) {
					snap.sr.handleLevelFill(ctx, levelID, fp, fq)
				})
			}
			continue
		}

		id8 := p.strategyID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		snap, ok := stratByID8[id8]
		if !ok {
			// No runner — just reset DB record and move on.
			log.Printf("strategy reconcile: level %s order %s missing from exchange — resetting to pending (no runner)", p.levelID, p.orderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
				p.levelID,
			)
			ar.UnregisterOrder(p.orderID)
			continue
		}
		sr := snap.sr
		p := p // capture for closure
		// Submit repair to the strategy's worker so it's serialized with other tasks.
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			// Guard: restartCycle may have cancelled this order and re-placed the level
			// with a new order between our DB snapshot (step 1) and now. If ExchangeOrderID
			// changed, the level is already handled — touching it would create a duplicate.
			stale := false
			for i := range sr.levels {
				if sr.levels[i].ID == p.levelID {
					if sr.levels[i].ExchangeOrderID != p.orderID {
						stale = true
					}
					break
				}
			}
			if stale {
				sr.mu.Unlock()
				return
			}
			log.Printf("strategy reconcile: level %s order %s missing from exchange — resetting to pending", p.levelID, p.orderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
				p.levelID,
			)
			ar.UnregisterOrder(p.orderID)
			for i := range sr.levels {
				if sr.levels[i].ID == p.levelID {
					sr.levels[i].Status = LevelPending
					sr.levels[i].ExchangeOrderID = ""
					break
				}
			}
			sr.placeNextLevels(ctx) //nolint:errcheck
			sr.mu.Unlock()
		})
	}

	// --- 6b. Matrix entry levels: placed in DB but missing from exchange ---
	// Mirror of block 6 for matrix strategies (kept separate — matrix fill/repair
	// semantics differ from grid). If a placed matrix entry order is no longer on
	// the exchange, first check whether it was actually Filled (filled orders also
	// disappear from open orders): if so, record the fill through handleLevelFill,
	// which routes matrix strategies to handleMatrixLevelFill. Otherwise reset the
	// level to pending so matrixPriceTick / block 8b re-places it.
	for _, p := range matrixDbLevels {
		if live[p.orderID] {
			continue
		}
		id8 := p.strategyID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		snap, ok := stratByID8[id8]
		if !ok {
			// No live runner — reset DB record; a future load will re-place.
			log.Printf("strategy reconcile: matrix level %s order %s missing from exchange — resetting to pending (no runner)", p.levelID, p.orderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
				p.levelID,
			)
			ar.UnregisterOrder(p.orderID)
			continue
		}

		// Was it actually Filled? Filled orders vanish from open orders like cancelled ones.
		if hist, _, err := ar.exchange.FetchOrderById(ctx, p.category, p.symbol, p.orderID); err == nil && hist.OrderStatus == "Filled" {
			filledPrice, _ := strconv.ParseFloat(hist.AvgPrice, 64)
			filledQty, _ := strconv.ParseFloat(hist.CumExecQty, 64)
			log.Printf("strategy reconcile: matrix level %s order %s was Filled @ %.4f qty=%.6f — recording fill", p.levelID, p.orderID, filledPrice, filledQty)
			levelID := p.levelID
			fp, fq := filledPrice, filledQty
			snap.sr.submit(func(ctx context.Context) {
				snap.sr.handleLevelFill(ctx, levelID, fp, fq)
			})
			continue
		}

		// Missing but not filled → reset to pending; block 8b will re-place it.
		sr := snap.sr
		p := p // capture for closure
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			defer sr.mu.Unlock()
			// Guard: skip if the level's order changed since our DB snapshot (rebuild race).
			stale := false
			for i := range sr.levels {
				if sr.levels[i].ID == p.levelID {
					if sr.levels[i].ExchangeOrderID != p.orderID {
						stale = true
					}
					break
				}
			}
			if stale {
				return
			}
			log.Printf("strategy reconcile: matrix level %s order %s missing from exchange — resetting to pending", p.levelID, p.orderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
				p.levelID,
			)
			ar.UnregisterOrder(p.orderID)
			for i := range sr.levels {
				if sr.levels[i].ID == p.levelID {
					sr.levels[i].Status = LevelPending
					sr.levels[i].ExchangeOrderID = ""
					break
				}
			}
		})
	}

	// --- 7. Check TP/SL: in active Grid cycle DB row but missing from exchange ---
	for _, c := range activeCycles {
		id8 := c.strategyID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		snap, ok := stratByID8[id8]

		if c.tpOrderID != "" && !live[c.tpOrderID] {
			log.Printf("strategy reconcile: cycle %s TP %s missing from exchange — clearing and re-placing", c.cycleID, c.tpOrderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, c.cycleID)
			ar.UnregisterOrder(c.tpOrderID)
			if ok {
				cycleID := c.cycleID
				tpOrderID := c.tpOrderID
				sr := snap.sr
				sr.submit(func(ctx context.Context) {
					sr.mu.Lock()
					defer sr.mu.Unlock()
					if sr.strategy.HedgeTpSuppressed {
						return
					}
					// If the circuit breaker fired (>= 5 consecutive external cancels)
					// do not re-place — something on the exchange keeps rejecting it.
					// The streak resets on TP fill or cycle close.
					if sr.tpCancelStreak >= 5 {
						return
					}
					if sr.tpOrderID == tpOrderID {
						sr.tpOrderID = ""
						_, posQty := sr.avgEntry()
						if posQty > 0 && sr.strategy.TPPct > 0 {
							if err := sr.updateTPByType(ctx); err != nil {
								log.Printf("strategy reconcile: TP re-place cycle %s: %v", cycleID, err)
							}
						}
					}
				})
			}
		} else if c.tpOrderID == "" && ok {
			// TP was never placed (e.g. instrument wasn't loaded at fill time).
			cycleID := c.cycleID
			sr := snap.sr
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.strategy.HedgeTpSuppressed {
					return
				}
				// Do not re-place if the circuit breaker fired — exchange keeps rejecting it.
				if sr.tpCancelStreak >= 5 {
					return
				}
				_, posQty := sr.avgEntry()
				if posQty > 0 && sr.tpOrderID == "" && sr.strategy.TPPct > 0 {
					log.Printf("strategy reconcile: cycle %s has position (qty=%.6f) but no TP — placing", cycleID, posQty)
					if err := sr.updateTPByType(ctx); err != nil {
						log.Printf("strategy reconcile: TP missing-place cycle %s: %v", cycleID, err)
					}
				}
			})
		}

		if c.slOrderID != "" && !live[c.slOrderID] {
			log.Printf("strategy reconcile: cycle %s SL %s missing from exchange — clearing and re-placing", c.cycleID, c.slOrderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET sl_order_id=NULL WHERE id=$1`, c.cycleID)
			ar.UnregisterOrder(c.slOrderID)
			if ok {
				cycleID := c.cycleID
				slOrderID := c.slOrderID
				sr := snap.sr
				sr.submit(func(ctx context.Context) {
					sr.mu.Lock()
					defer sr.mu.Unlock()
					if sr.strategy.HedgeSlSuppressed {
						return
					}
					if sr.slOrderID == slOrderID {
						sr.slOrderID = ""
						_, posQty := sr.avgEntry()
						if posQty > 0 && sr.strategy.SLPct < 0 {
							if err := sr.updateSL(ctx); err != nil {
								log.Printf("strategy reconcile: SL re-place cycle %s: %v", cycleID, err)
							}
						}
					}
				})
			}
		} else if c.slOrderID == "" && ok {
			// SL was never placed (same gap as TP: instrument not loaded at fill time, etc.).
			cycleID := c.cycleID
			sr := snap.sr
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.strategy.HedgeSlSuppressed {
					return
				}
				_, posQty := sr.avgEntry()
				if posQty > 0 && sr.slOrderID == "" && sr.strategy.SLPct < 0 {
					log.Printf("strategy reconcile: cycle %s has position (qty=%.6f) but no SL — placing", cycleID, posQty)
					if err := sr.updateSL(ctx); err != nil {
						log.Printf("strategy reconcile: SL missing-place cycle %s: %v", cycleID, err)
					}
				}
			})
		}
	}

	// --- 7b. Check TP: active Matrix cycle DB row but missing from exchange ---
	// Matrix previously had no equivalent of block 7 — a position could sit unprotected
	// indefinitely if matrixUpdateTP silently no-op'd at fill time (e.g. instrument info
	// not loaded yet) and resumeMatrixCycle didn't re-check TP on every subsequent
	// restart/reconnect either. This closes that gap the same way block 7 does for grid.
	for _, c := range activeMatrixCycles {
		id8 := c.strategyID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		snap, ok := stratByID8[id8]
		if !ok {
			continue
		}

		if c.tpOrderID != "" && !live[c.tpOrderID] {
			log.Printf("strategy reconcile: matrix cycle %s TP %s missing from exchange — clearing and re-placing", c.cycleID, c.tpOrderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET tp_order_id=NULL WHERE id=$1`, c.cycleID)
			ar.UnregisterOrder(c.tpOrderID)
			cycleID := c.cycleID
			tpOrderID := c.tpOrderID
			sr := snap.sr
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				if sr.strategy.HedgeTpSuppressed {
					return
				}
				if sr.tpOrderID == tpOrderID {
					sr.tpOrderID = ""
					_, posQty := sr.avgEntry()
					if posQty > 0 {
						log.Printf("strategy reconcile: matrix cycle %s re-placing TP", cycleID)
						sr.matrixUpdateTP(ctx)
					}
				}
			})
		} else if c.tpOrderID == "" {
			cycleID := c.cycleID
			sr := snap.sr
			sr.submit(func(ctx context.Context) {
				sr.mu.Lock()
				defer sr.mu.Unlock()
				_, posQty := sr.avgEntry()
				if matrixNeedsTPRestore(sr.tpOrderID, posQty > 0, sr.strategy.HedgeTpSuppressed) {
					log.Printf("strategy reconcile: matrix cycle %s has position (qty=%.6f) but no TP — placing", cycleID, posQty)
					sr.matrixUpdateTP(ctx)
				}
			})
		}
	}

	// --- 8. Matrix per-level SL health-check ---
	// Runs every reconcile tick (20 s). For each active matrix runner, checks in-memory
	// filled levels where SL was never placed (SLOrderID == "") and retries placement.
	// Complements the price-triggered retry in matrixPriceTick (block 4): that one only
	// fires when price crosses back through the fill level; this provides a timer-based
	// safety net for levels that might never see that crossover.
	for stratID, sr := range strategyRefs {
		if sr.strategy.StrategyType != "matrix" {
			continue
		}
		stratIDcopy := stratID
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			defer sr.mu.Unlock()
			// SL suppressed by active hedge — skip health-check re-placement.
			if sr.strategy.HedgeSlSuppressed {
				return
			}
			for i := range sr.levels {
				l := &sr.levels[i]
				if l.Status != LevelFilled || l.SLOrderID != "" || l.Slot == nil {
					continue
				}
				_, stopPct, _, _ := sr.matrixLevelConfig(*l.Slot)
				if stopPct == nil {
					continue
				}
				ref := l.FilledPrice
				if ref <= 0 {
					// FilledPrice not recorded yet — matrixPriceTick will handle it on next tick.
					continue
				}
				log.Printf("strategy reconcile: matrix L%d (strategy %s) — SL не выставлен, повтор @ fillPrice=%.4f",
					l.LevelIdx, stratIDcopy[:8], ref)
				sr.matrixPlacePerLevelSL(ctx, l, ref, *stopPct)
			}
		})
	}

	// --- 8b. Matrix per-level SL ghost-fill detection ---
	// Block 8 above only catches SLOrderID=="" (never placed). This catches the opposite:
	// SLOrderID is set, but the order is no longer on the exchange — it already filled —
	// while the level is still marked 'filled' and SLOrderID still looks "live" to us.
	// handleMatrixSLFill is normally driven by the live WS order-Filled event
	// (engine.go OnOrderEvent), which requires the runner to be running at the exact moment
	// the fill happens; if the process was down or mid-restart then, the fill is missed
	// entirely by that path. Found live (2026-10-02, COTIUSDT/pol): L(1)'s stop-cond SL
	// fired for real (confirmed via its own exec_id/price already sitting in
	// trader_executions — the WS execution stream itself was fine) while api-gateway was
	// restarting, so the live handler never ran; the level stayed 'filled' and the exchange
	// position silently ended up smaller than every "filled" level's qty summed together.
	// Reuses the exact real fill-price from trader_executions (already populated by the
	// account's WS/execution ingestion, independent of this reconcile loop) and replays it
	// through the same handleMatrixSLFill the live WS path would have called — not a
	// hand-rolled substitute, so realized_pnl/hedge accounting/re-entry logic all run
	// exactly as they would have at the time. Never happens on symmetric "just gone"
	// ambiguity: if no execution record exists, this leaves the level untouched rather than
	// guessing (same caution as isOrderGone elsewhere in this codebase).
	for stratID, sr := range strategyRefs {
		if sr.strategy.StrategyType != "matrix" {
			continue
		}
		sr.mu.RLock()
		ghosts := matrixGhostSLCandidates(sr.levels, live)
		sr.mu.RUnlock()
		for _, g := range ghosts {
			var fillPrice float64
			if err := ar.pool.QueryRow(ctx,
				`SELECT price FROM trader_executions WHERE account_id=$1 AND order_id=$2 ORDER BY exec_time DESC LIMIT 1`,
				ar.accountID, g.slOrderID,
			).Scan(&fillPrice); err != nil {
				log.Printf("strategy reconcile: matrix level SL %s (strategy %s) missing from exchange with no matching execution found — leaving level as-is",
					g.slOrderID, stratID[:8])
				continue
			}
			levelID := g.levelID
			sr.submit(func(ctx context.Context) { sr.handleMatrixSLFill(ctx, levelID, fillPrice) })
		}
	}

	// --- 8d. ALS per-level SL ghost-fill detection ---
	// ALS counterpart of block 8b above, for the exact same reason: handleAlsLevelSLFill
	// is normally driven by the live WS order-Filled event (engine.go OnOrderEvent,
	// refType="als_sl"), which requires the runner to be running at the exact moment the
	// fill happens. If the process was down or mid-restart then, the fill is missed
	// entirely by that path, leaving the level LevelFilled with a now-dead SLOrderID —
	// silently shrinking the hedge's real exchange position below what our own
	// bookkeeping believes is still protected. Reuses the real fill price from
	// trader_executions and replays it through the same handleAlsLevelSLFill the live WS
	// path would have called, so realized_pnl/memory-bank-restore all run exactly as they
	// would have at the time — not a hand-rolled substitute.
	for stratID, sr := range strategyRefs {
		if sr.strategy.StrategyType != "als" {
			continue
		}
		sr.mu.RLock()
		ghosts := alsGhostSLCandidates(sr.levels, live)
		sr.mu.RUnlock()
		for _, g := range ghosts {
			var fillPrice float64
			if err := ar.pool.QueryRow(ctx,
				`SELECT price FROM trader_executions WHERE account_id=$1 AND order_id=$2 ORDER BY exec_time DESC LIMIT 1`,
				ar.accountID, g.slOrderID,
			).Scan(&fillPrice); err != nil {
				log.Printf("strategy reconcile: ALS level SL %s (strategy %s) missing from exchange with no matching execution found — leaving level as-is",
					g.slOrderID, stratID[:8])
				continue
			}
			levelID := g.levelID
			sr.submit(func(ctx context.Context) { sr.handleAlsLevelSLFill(ctx, levelID, fillPrice) })
		}
	}

	// --- 8c. Matrix pending entry health-check ---
	// For each active matrix runner, retries placing non-virtual pending entry levels
	// with no exchange_order_id. Covers placement failures during rebuild (transient
	// exchange errors, service restart race) that leave levels permanently stuck.
	// matrixPriceTick only fires virtual levels, so non-virtual stuck levels need
	// this timer-based fallback.
	for _, sr := range strategyRefs {
		if sr.strategy.StrategyType != "matrix" {
			continue
		}
		sr := sr // capture
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			defer sr.mu.Unlock()
			if sr.cycle == nil {
				return
			}
			price := sr.lastMatrixPrice
			if price <= 0 {
				return
			}
			for i := range sr.levels {
				l := &sr.levels[i]
				if l.Status != LevelPending || l.ExchangeOrderID != "" {
					continue
				}
				if sr.matrixIsVirtual(l) {
					continue
				}
				retryPrice := matrix8cRetryPrice(l, price)
				log.Printf("strategy reconcile: matrix L%d (strategy %s) — pending без ордера, повтор placeMatrixLevel",
					l.LevelIdx, sr.strategy.ID[:8])
				if err := sr.placeMatrixLevel(ctx, l, retryPrice); err != nil {
					log.Printf("strategy reconcile: matrix L%d place error: %v", l.LevelIdx, err)
				}
			}
		})
	}

	// --- 8d. Relative-slots numbering catch-up ---
	// Runs matrixCatchUpRelativeSlotNumbering for every live relative-slots runner — the
	// periodic self-heal counterpart to matrixAfterSLClose's reactive per-close renumbering.
	// That reactive half only fixes numbering going forward from the SL close that triggers
	// it; a strategy whose numbering had already drifted stale before this feature existed
	// (or for any other reason) needs an independent recheck to ever get corrected. Not
	// scoped to strategy_type='matrix' like block 8/8b/8c — relative_slots is also used by
	// grid-typed strategies that run matrix mechanics internally (e.g. UseSignal-gated grid
	// legs), and matrixCatchUpRelativeSlotNumbering itself already no-ops for anything that
	// isn't actually RelativeSlots, so every runner can be submitted unconditionally.
	for _, sr := range strategyRefs {
		sr := sr // capture
		sr.submit(func(ctx context.Context) {
			sr.matrixCatchUpRelativeSlotNumbering(ctx)
		})
	}

	// --- 9. Orphan detection ---
	// Format: SIS_STR-{id8}-{cycleNum}-{levelIdx}-{gen}  (grid level)
	//         SIS_STR-{id8}-tp-{cycleNum}-{seq}          (TP)
	//         SIS_STR-{id8}-sl-{cycleNum}-{seq}          (SL)
	//
	// An order is orphaned if it has our linkId prefix but either:
	//   a) belongs to a stopped/non-running strategy, OR
	//   b) its cycle number differs from the currently active cycle, OR
	//   c) it's same cycle but not tracked in orderIndex.
	for _, o := range exchangeOrders {
		if o.OrderLinkId == "" || !strings.HasPrefix(o.OrderLinkId, "SIS_STR-") {
			continue
		}
		parts := strings.Split(o.OrderLinkId, "-")
		// Minimum: SIS_STR | id8 | type_or_cycleNum | ... | ...
		if len(parts) < 5 {
			continue
		}
		id8 := parts[1]

		// Check if this id8 belongs to any of our Grid strategies on this account.
		// Orders that have SIS_STR- prefix but don't match any known strategy (even stopped ones)
		// belong to deleted strategies — they are unconditional orphans.
		matchedPrefix := false
		for prefix := range allStratPrefixes {
			if strings.HasPrefix(o.OrderLinkId, prefix) {
				matchedPrefix = true
				break
			}
		}

		snap, isRunning := stratByID8[id8]

		isOrphan := false
		var reason string

		if !matchedPrefix {
			// Strategy was deleted from DB but its exchange orders are still live.
			isOrphan = true
			reason = fmt.Sprintf("стратегия %s удалена из БД", id8)
		} else if !isRunning {
			// Strategy is stopped / not loaded — any order with its prefix is orphaned.
			isOrphan = true
			reason = "стратегия не запущена"
		} else {
			// Parse cycle number from linkId.
			cycleStr := parts[2]
			if cycleStr == "tp" || cycleStr == "sl" {
				if len(parts) < 5 {
					continue
				}
				cycleStr = parts[3]
			}
			linkCycleNum, err := strconv.Atoi(cycleStr)
			if err != nil {
				continue // malformed linkId
			}

			if !snap.hasCycle {
				isOrphan = true
				reason = fmt.Sprintf("стратегия без активного цикла (linkId cycle=%d)", linkCycleNum)
			} else if linkCycleNum != snap.cycleNum {
				isOrphan = true
				reason = fmt.Sprintf("цикл %d != активный %d", linkCycleNum, snap.cycleNum)
			} else if !known[o.OrderId] && !known[o.OrderLinkId] {
				isOrphan = true
				reason = "не отслеживается в orderIndex"
			}
		}

		// Guard: orderIndex is the ground truth. The cycle-number snapshot (step 4) is taken
		// before exchange orders are fetched (step 5), so a new cycle that starts in between
		// will appear mismatched but its orders ARE tracked in orderIndex. Tracked = not orphan.
		// Also guard by LinkId: order placement registers linkId BEFORE the REST call and
		// orderId AFTER, so there is a window where linkId is known but orderId is not yet.
		// We check the LIVE orderIndex (not the stale snapshot) to catch orders placed between
		// the snapshot and the orphan check — otherwise they appear as orphans and get cancelled.
		if isOrphan {
			ar.mu.RLock()
			_, hasID := ar.orderIndex[o.OrderId]
			_, hasLink := ar.orderIndex[o.OrderLinkId]
			ar.mu.RUnlock()
			if hasID || hasLink {
				isOrphan = false
			}
		}

		if !isOrphan {
			continue
		}

		log.Printf("strategy reconcile: orphan %s (linkId=%s, %s) — отменяю", o.OrderId, o.OrderLinkId, reason)
		orderFilter := ""
		if o.OrderFilter == "StopOrder" {
			orderFilter = "StopOrder"
		}
		if err := ar.exchange.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      o.Symbol,
			Category:    o.Category,
			OrderId:     o.OrderId,
			OrderFilter: orderFilter,
		}); err != nil && !isOrderGone(err) {
			log.Printf("strategy reconcile: orphan cancel %s: %v", o.OrderId, err)
		}
	}

	// --- 10. Leverage drift guard ---
	// Detects when the exchange-side leverage for a symbol has dropped below what this
	// runner last confirmed setting (startCycle/matrix's SetLeverage — see confirmedLeverage's
	// doc comment in cycle.go) and restores it. Nothing in this codebase ever lowers leverage
	// after a cycle starts, so any drop can only come from outside this system (e.g. a manual
	// change via the Bybit app). Found live (2026-08-20): TSMUSDT's confirmed 50x was running
	// at 10x on the exchange with no corresponding log entry anywhere in this codebase, which
	// more than quintupled the margin reserved for its resting DCA limit orders.
	for stratID, sr := range strategyRefs {
		sr.mu.Lock()
		expected := sr.confirmedLeverage
		status := sr.strategy.Status
		symbol := sr.strategy.Symbol
		category := sr.strategy.Category
		wantIdx := positionIdxForClose(sr.strategy.HedgeMode, sr.strategy.Direction)
		sr.mu.Unlock()
		if expected <= 0 || status != StatusActive {
			continue
		}
		current := ar.GetPositionLeverage(symbol, wantIdx)
		if current <= 0 || int(current) >= expected {
			continue
		}
		stratIDcopy := stratID
		currentCopy := current
		sr.submit(func(ctx context.Context) {
			sr.mu.Lock()
			exp := sr.confirmedLeverage
			sr.mu.Unlock()
			if exp <= 0 || int(currentCopy) >= exp {
				return
			}
			levStr := strconv.Itoa(exp)
			if err := sr.runner.Exchange().SetLeverage(ctx, trader.LeverageRequest{
				Symbol:       symbol,
				Category:     category,
				BuyLeverage:  levStr,
				SellLeverage: levStr,
			}); err != nil && !strings.Contains(err.Error(), "110043") {
				log.Printf("strategy reconcile: leverage restore %s: requested %dx (exchange showed %dx): %v", stratIDcopy[:8], exp, int(currentCopy), err)
			} else {
				sr.warn(ctx, fmt.Sprintf("Плечо на бирже оказалось снижено извне (%dx→%dx) — восстановлено до %dx", exp, int(currentCopy), exp))
			}
		})
	}
}

// ghostSL identifies a per-level matrix SL order that looks "live" to our own bookkeeping
// (level still LevelFilled, SLOrderID still set) but is no longer actually resting on the
// exchange — see matrixGhostSLCandidates.
type ghostSL struct{ levelID, slOrderID string }

// matrixGhostSLCandidates returns every level whose per-level SL order has vanished from the
// exchange's live order list while the level itself is still bookkept as filled-and-protected.
// Pure and DB/network-free so it can be tested directly — the caller (reconcile's block 8b)
// does the actual trader_executions lookup and handleMatrixSLFill replay.
func matrixGhostSLCandidates(levels []GridLevel, live map[string]bool) []ghostSL {
	var ghosts []ghostSL
	for i := range levels {
		l := &levels[i]
		if l.Status != LevelFilled || l.Slot == nil || l.SLOrderID == "" {
			continue
		}
		if live[l.SLOrderID] {
			continue // still genuinely resting on the exchange
		}
		ghosts = append(ghosts, ghostSL{l.ID, l.SLOrderID})
	}
	return ghosts
}

// alsGhostSLCandidates is matrixGhostSLCandidates's ALS counterpart — same ghost-fill
// detection (an SL order our own bookkeeping still thinks is live, but has vanished from
// the exchange's open-order list), but without the Slot requirement: ALS levels never
// set Slot (that's a matrix-only concept, see GridLevel's own field comment), so the
// equivalent "is this an ALS per-level stop" check is simply the level still being
// LevelFilled with a non-empty SLOrderID — the reconcile caller already scopes this to
// StrategyType=="als" runners only, so no further type discrimination is needed here.
func alsGhostSLCandidates(levels []GridLevel, live map[string]bool) []ghostSL {
	var ghosts []ghostSL
	for i := range levels {
		l := &levels[i]
		if l.Status != LevelFilled || l.SLOrderID == "" {
			continue
		}
		if live[l.SLOrderID] {
			continue // still genuinely resting on the exchange
		}
		ghosts = append(ghosts, ghostSL{l.ID, l.SLOrderID})
	}
	return ghosts
}

// matrix8cRetryPrice picks the price block 8c's health-check passes into placeMatrixLevel
// when retrying a stuck pending level. Relative-slots levels (l.Slot != nil) are only ever
// inserted after price has already reached/passed their TargetPrice (matrixRelativeExpand
// gates on matrixSlotReached before calling matrixPlaceRelativeSlot) — so retrying with the
// real live price here, instead of TargetPrice, hits matrixEntryOrderType's price-comparison
// branch and places a resting Limit at the stale target instead of a Market order, exactly
// like the bug already fixed in matrixPlaceRelativeSlot/matrixRetryStuckRelativeSlot (see
// their comments — found live 2026-07-21, HEMIUSDT). Found again live 2026-10-11
// (JCTUSDT/stepanovsa): this block's retry raced the tick-driven matrixRetryStuckRelativeSlot,
// won, and left a Limit Sell resting far above a falling market, unreachable without a
// reversal. Absolute-mode levels (l.Slot == nil) keep using the live price, as before.
func matrix8cRetryPrice(l *GridLevel, livePrice float64) float64 {
	if l.Slot != nil {
		return l.TargetPrice
	}
	return livePrice
}
