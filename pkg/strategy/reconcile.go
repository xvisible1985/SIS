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

// startReconcileLoop runs reconcile() immediately on startup, then every 60 seconds.
func (ar *AccountRunner) startReconcileLoop(ctx context.Context) {
	// Run once right away to catch orphans left from previous run.
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

	// --- 3. Query ALL Grid strategy IDs (any status) for orphan scan ---
	// This covers stopped strategies whose orders might still be on the exchange.
	allStratRows, err := ar.pool.Query(ctx,
		`SELECT id FROM strategies WHERE account_id=$1 AND strategy_type='grid'`,
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
	exchangeOrders, err := trader.FetchOpenOrders(ctx, ar.creds)
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
		if hist, _, err := trader.FetchOrderById(ctx, ar.creds, p.category, p.symbol, p.orderID); err == nil && hist.OrderStatus == "Filled" {
			filledPrice, _ := strconv.ParseFloat(hist.AvgPrice, 64)
			filledQty, _ := strconv.ParseFloat(hist.CumExecQty, 64)
			log.Printf("strategy reconcile: level %s order %s was Filled @ %.4f qty=%.6f — recording fill", p.levelID, p.orderID, filledPrice, filledQty)
			id8 := p.strategyID
			if len(id8) > 8 {
				id8 = id8[:8]
			}
			if snap, ok := stratByID8[id8]; ok {
				snap.sr.handleLevelFill(ctx, p.levelID, filledPrice, filledQty)
			}
			continue
		}

		log.Printf("strategy reconcile: level %s order %s missing from exchange — resetting to pending", p.levelID, p.orderID)
		ar.pool.Exec(ctx, //nolint:errcheck
			`UPDATE strategy_levels SET status='pending', exchange_order_id=NULL, placed_at=NULL WHERE id=$1`,
			p.levelID,
		)
		ar.UnregisterOrder(p.orderID)

		id8 := p.strategyID
		if len(id8) > 8 {
			id8 = id8[:8]
		}
		snap, ok := stratByID8[id8]
		if !ok {
			continue
		}
		sr := snap.sr
		sr.mu.Lock()
		for i := range sr.levels {
			if sr.levels[i].ID == p.levelID {
				sr.levels[i].Status = LevelPending
				sr.levels[i].ExchangeOrderID = ""
				break
			}
		}
		sr.placeNextLevels(ctx) //nolint:errcheck
		sr.mu.Unlock()
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
				sr := snap.sr
				sr.mu.Lock()
				if sr.tpOrderID == c.tpOrderID {
					sr.tpOrderID = ""
					_, posQty := sr.avgEntry()
					if posQty > 0 && sr.strategy.TPPct > 0 {
						if err := sr.updateTP(ctx); err != nil {
							log.Printf("strategy reconcile: TP re-place cycle %s: %v", c.cycleID, err)
						}
					}
				}
				sr.mu.Unlock()
			}
		} else if c.tpOrderID == "" && ok {
			// TP was never placed (e.g. instrument wasn't loaded at fill time).
			sr := snap.sr
			sr.mu.Lock()
			_, posQty := sr.avgEntry()
			if posQty > 0 && sr.tpOrderID == "" && sr.strategy.TPPct > 0 {
				log.Printf("strategy reconcile: cycle %s has position (qty=%.6f) but no TP — placing", c.cycleID, posQty)
				if err := sr.updateTP(ctx); err != nil {
					log.Printf("strategy reconcile: TP missing-place cycle %s: %v", c.cycleID, err)
				}
			}
			sr.mu.Unlock()
		}

		if c.slOrderID != "" && !live[c.slOrderID] {
			log.Printf("strategy reconcile: cycle %s SL %s missing from exchange — clearing and re-placing", c.cycleID, c.slOrderID)
			ar.pool.Exec(ctx, //nolint:errcheck
				`UPDATE strategy_cycles SET sl_order_id=NULL WHERE id=$1`, c.cycleID)
			ar.UnregisterOrder(c.slOrderID)
			if ok {
				sr := snap.sr
				sr.mu.Lock()
				if sr.slOrderID == c.slOrderID {
					sr.slOrderID = ""
					_, posQty := sr.avgEntry()
					if posQty > 0 && sr.strategy.SLPct > 0 {
						if err := sr.updateSL(ctx); err != nil {
							log.Printf("strategy reconcile: SL re-place cycle %s: %v", c.cycleID, err)
						}
					}
				}
				sr.mu.Unlock()
			}
		} else if c.slOrderID == "" && ok {
			// SL was never placed (same gap as TP: instrument not loaded at fill time, etc.).
			sr := snap.sr
			sr.mu.Lock()
			_, posQty := sr.avgEntry()
			if posQty > 0 && sr.slOrderID == "" && sr.strategy.SLPct > 0 {
				log.Printf("strategy reconcile: cycle %s has position (qty=%.6f) but no SL — placing", c.cycleID, posQty)
				if err := sr.updateSL(ctx); err != nil {
					log.Printf("strategy reconcile: SL missing-place cycle %s: %v", c.cycleID, err)
				}
			}
			sr.mu.Unlock()
		}
	}

	// --- 8. Orphan detection ---
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
		matchedPrefix := false
		for prefix := range allStratPrefixes {
			if strings.HasPrefix(o.OrderLinkId, prefix) {
				matchedPrefix = true
				break
			}
		}
		if !matchedPrefix {
			continue
		}

		snap, isRunning := stratByID8[id8]

		isOrphan := false
		var reason string

		if !isRunning {
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
			} else if !known[o.OrderId] {
				isOrphan = true
				reason = "не отслеживается в orderIndex"
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
		if err := ar.tradeStream.CancelOrder(ctx, trader.CancelRequest{
			Symbol:      o.Symbol,
			Category:    o.Category,
			OrderId:     o.OrderId,
			OrderFilter: orderFilter,
		}); err != nil && !isOrderGone(err) {
			log.Printf("strategy reconcile: orphan cancel %s: %v", o.OrderId, err)
		}
	}
}
