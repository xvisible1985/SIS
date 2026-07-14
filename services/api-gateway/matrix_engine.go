// services/api-gateway/matrix_engine.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"sis/pkg/trader"
)

// processMatrixBot processes a single matrix bot for one tick:
//  1. Checks existing strategy pairs for the paired-close condition.
//  2. Ensures both long and short strategies are running for each whitelisted symbol.
func (s *Server) processMatrixBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка получения позиций: %v", err), "error", "system")
		return
	}

	posMap, _ := buildHedgePosMap(rawPositions)

	// Symbols whose pair was just closed this tick must NOT be re-opened by
	// ensureMatrixStrategies using the now-stale posMap (it would re-adopt the closing
	// position and re-fire the trigger). They reopen fresh on the next tick from flat.
	closed := s.checkMatrixPairedClose(ctx, botID, cfg, creds, posMap)
	s.checkMatrixZombieStrategies(ctx, botID, posMap)
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, closed)
}

// checkMatrixZombieStrategies stops bot matrix strategies stuck in status='active'
// with no active cycle — a "zombie". maybeRestart stops bot strategies after a cycle
// closes and relies on the bot to recreate them, but ensureMatrixStrategies only
// recreates when NO active strategy exists for the direction. So a strategy left
// 'active' with a dead cycle (e.g. a dropped restart task, or a ghost_close that did
// not set 'stopped') never reopens on its own. Stopping it here lets
// ensureMatrixStrategies recreate a fresh leg on the same tick.
//
// Safety: a leg whose exchange position is still open is skipped — the strategy engine
// reopens/adopts an existing position; stopping it would let ensureMatrixStrategies open
// a SECOND position. A 2-minute grace period avoids racing a normal in-flight restart.
func (s *Server) checkMatrixZombieStrategies(ctx context.Context, botID string, posMap map[string]map[string]hedgePosInfo) {
	// Latest cycle (cycle_id/num) is pulled alongside so a split-brain leg — one whose
	// latest cycle is ended but whose position is still open — can be revived, not stopped.
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.symbol, s.direction, lc.cycle_id, lc.cycle_num
		 FROM strategies s
		 LEFT JOIN LATERAL (
		     SELECT c.id AS cycle_id, c.cycle_num, c.ended_at
		     FROM strategy_cycles c WHERE c.strategy_id=s.id
		     ORDER BY c.cycle_num DESC LIMIT 1
		 ) lc ON true
		 WHERE s.bot_id=$1 AND s.strategy_type='matrix' AND s.status='active'
		   AND (lc.cycle_id IS NULL OR lc.ended_at IS NOT NULL)
		   AND COALESCE(lc.ended_at, s.created_at) < NOW() - INTERVAL '2 minutes'`,
		botID)
	if err != nil {
		return
	}
	type zombie struct {
		id, symbol, dir string
		cycleID         *string
		cycleNum        *int
	}
	var zombies []zombie
	for rows.Next() {
		var z zombie
		if rows.Scan(&z.id, &z.symbol, &z.dir, &z.cycleID, &z.cycleNum) == nil {
			zombies = append(zombies, z)
		}
	}
	rows.Close()

	for _, z := range zombies {
		side := "Buy"
		if z.dir == "short" {
			side = "Sell"
		}
		posOpen := false
		if bySym, ok := posMap[z.symbol]; ok {
			if p, ok := bySym[side]; ok && p.Size > 0 {
				posOpen = true
			}
		}
		if posOpen {
			// Position still open on the exchange while the latest cycle is marked ended:
			// a false close (ghost_close) left the position live and trading continued on
			// the ended cycle. Revive it so the DB matches reality — stopping instead would
			// let ensureMatrixStrategies open a SECOND position on top of the existing one.
			if z.cycleID != nil {
				s.reviveMatrixSplitBrain(ctx, botID, z.id, *z.cycleID, z.cycleNum, z.symbol, z.dir)
			}
			continue
		}
		if _, err := s.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1 AND status='active'`, z.id,
		); err != nil {
			continue
		}
		if s.engine != nil {
			go s.engine.Notify(context.Background(), z.id)
		}
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Матрикс: %s %s — зомби-стратегия (active без цикла) остановлена для пересоздания", z.symbol, z.dir),
			"warn", "matrix")
	}
}

// reviveMatrixSplitBrain repairs a matrix leg whose latest cycle is marked ended while its
// exchange position is still open (a false ghost_close). It clears the cycle's ended_at so
// loadActiveCycle picks it up again and the chart/counters match the live position, and it
// removes the phantom ghost_close trade recorded for that false close so realised PnL is not
// double-counted when the position eventually closes for real. Notify makes the runner adopt
// the revived cycle's existing orders without placing a duplicate entry.
func (s *Server) reviveMatrixSplitBrain(ctx context.Context, botID, stratID, cycleID string, cycleNum *int, symbol, dir string) {
	// Only revive a cycle that actually holds a filled level — otherwise there is no cycle
	// state to preserve and ensureMatrixStrategies' adopt path is the right handler.
	var hasFilled bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM strategy_levels WHERE cycle_id=$1 AND status='filled')`, cycleID,
	).Scan(&hasFilled); err != nil || !hasFilled {
		return
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE strategy_cycles SET ended_at=NULL, result=NULL WHERE id=$1 AND ended_at IS NOT NULL`, cycleID)
	if err != nil || tag.RowsAffected() == 0 {
		return
	}
	if cycleNum != nil {
		s.pool.Exec(ctx, //nolint:errcheck
			`DELETE FROM trade_history WHERE strategy_id=$1 AND cycle_num=$2 AND result='ghost_close'`,
			stratID, *cycleNum)
	}
	if s.engine != nil {
		go s.engine.Notify(context.Background(), stratID)
	}
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Матрикс: %s %s — цикл оживлён (был помечен завершённым, но позиция открыта — ложное закрытие)", symbol, dir),
		"warn", "matrix")
}

// buildAdoptData returns adopt_position_data JSON ({size, entry_price}) for an open
// position on the given leg direction, or nil when there is no open position. Used so a
// leg that gets (re)attached to a bot adopts its live exchange position instead of placing
// a fresh L0 market entry on top of it (double-entry).
func buildAdoptData(posMap map[string]map[string]hedgePosInfo, symbol, dir string) *string {
	side := "Buy"
	if dir == "short" {
		side = "Sell"
	}
	bySym, ok := posMap[symbol]
	if !ok {
		return nil
	}
	pos, ok := bySym[side]
	if !ok || pos.Size <= 0 {
		return nil
	}
	raw, _ := json.Marshal(struct {
		Size       string `json:"size"`
		EntryPrice string `json:"entry_price"`
	}{
		Size:       strconv.FormatFloat(pos.Size, 'f', -1, 64),
		EntryPrice: strconv.FormatFloat(pos.EntryPrice, 'f', -1, 64),
	})
	s := string(raw)
	return &s
}

// checkMatrixPairedClose inspects all active strategy pairs (long+short) for this bot
// and fires the paired-close condition when the combined P&L target is met.
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) map[string]bool {
	closed := map[string]bool{}
	category := cfg.Category
	if category == "" {
		category = "linear"
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol, direction FROM strategies
		 WHERE bot_id=$1 AND status IN ('active','finishing')`,
		botID)
	if err != nil {
		return closed
	}
	type stratRef struct{ id, symbol, dir string }
	var strats []stratRef
	for rows.Next() {
		var r stratRef
		if rows.Scan(&r.id, &r.symbol, &r.dir) == nil {
			strats = append(strats, r)
		}
	}
	rows.Close()

	type pair struct{ longID, shortID string }
	pairs := make(map[string]*pair)
	for _, r := range strats {
		if pairs[r.symbol] == nil {
			pairs[r.symbol] = &pair{}
		}
		switch r.dir {
		case "long":
			pairs[r.symbol].longID = r.id
		case "short":
			pairs[r.symbol].shortID = r.id
		}
	}

	for sym, p := range pairs {
		if p.longID == "" || p.shortID == "" {
			continue
		}
		// Ensure a session row exists for this pair (idempotent — matches the
		// hedge_sessions bootstrap pattern used for grid+matrix hedge pairs).
		// long=main, short=hedge by convention; GetHedgeSession sums whichever
		// leg's own strategy_id is requested using this row's started_at/end_reason
		// as the accumulation window boundary.
		s.pool.Exec(ctx, //nolint:errcheck
			`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (hedge_strategy_id) WHERE ended_at IS NULL DO NOTHING`,
			botID, p.longID, p.shortID)

		bySymbol, ok := posMap[sym]
		if !ok {
			continue
		}
		longPos, hasLong := bySymbol["Buy"]
		shortPos, hasShort := bySymbol["Sell"]
		if !hasLong || !hasShort {
			continue
		}
		if meetsPairedCloseCriteria(longPos, shortPos, cfg) {
			combined := longPos.UnrealisedPnl + shortPos.UnrealisedPnl
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс: %s — парное закрытие (PnL=%.4g, тип=%d, порог=%.4g)",
					sym, combined, cfg.HedgeDeactCloseType, cfg.HedgeDeactCloseValue),
				"info", "matrix")
			s.stopMatrixPair(ctx, botID, sym, p.longID, p.shortID, creds, category, longPos, shortPos)
			closed[sym] = true
		}
	}
	return closed
}

// matrixLegCloseRequest builds a reduce-only market order that flattens one leg of a
// matrix pair. Side is the OPPOSITE of the position side (a Buy position is closed with a
// Sell and vice-versa, so it flattens rather than doubles); posIdx is the hedge-mode slot
// (long=1, short=2); qty is the raw exchange size string. Returns ok=false when there is
// nothing to close.
func matrixLegCloseRequest(pos hedgePosInfo, symbol, category string, posIdx int) (trader.OrderRequest, bool) {
	if pos.Size <= 0 || pos.SizeStr == "" {
		return trader.OrderRequest{}, false
	}
	closeSide := "Sell"
	if pos.Side == "Sell" {
		closeSide = "Buy"
	}
	return trader.OrderRequest{
		Category:    category,
		Symbol:      symbol,
		Side:        closeSide,
		OrderType:   "Market",
		Qty:         pos.SizeStr,
		ReduceOnly:  true,
		PositionIdx: posIdx,
	}, true
}

// stopMatrixPair stops both legs of a matrix strategy pair, notifies the engine,
// and closes the pair's session with end_reason='paired_close' — the only
// genuine reset trigger for the "Накоплено матрикс" cumulative counter.
func (s *Server) stopMatrixPair(ctx context.Context, botID, symbol, longID, shortID string, creds trader.Credentials, category string, longPos, shortPos hedgePosInfo) {
	// Realize the combined profit: paired-close must CLOSE both exchange positions.
	// Stopping the strategies alone does NOT flat a matrix position (the cycle is kept
	// open by design), so without this the positions linger, ensureMatrixStrategies
	// re-adopts them, and the trigger re-fires every tick without ever taking profit.
	for _, leg := range []struct {
		pos    hedgePosInfo
		posIdx int
	}{{longPos, 1}, {shortPos, 2}} {
		req, ok := matrixLegCloseRequest(leg.pos, symbol, category, leg.posIdx)
		if !ok {
			continue
		}
		req.OrderLinkId = fmt.Sprintf("SIS_MPC_%d_%d", leg.posIdx, time.Now().UnixMilli())
		if _, err := trader.PlaceOrder(ctx, creds, req); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс: %s — ошибка закрытия позиции (%s idx%d): %v", symbol, req.Side, leg.posIdx, err),
				"error", "matrix")
		}
	}

	for _, id := range []string{longID, shortID} {
		if _, err := s.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, id); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс: %s — ошибка остановки %s: %v", symbol, id[:8], err),
				"error", "matrix")
		} else {
			go s.engine.Notify(context.Background(), id)
		}
	}
	s.pool.Exec(ctx, //nolint:errcheck
		`UPDATE hedge_sessions SET ended_at=NOW(), end_reason='paired_close'
		 WHERE hedge_strategy_id=$1 AND ended_at IS NULL`,
		shortID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Матрикс: %s — пара остановлена", symbol),
		"info", "matrix")
}

// directionHasLiveStrategy сообщает, есть ли по (account, symbol, direction) стратегия,
// которая должна блокировать пересоздание боту: активная/завершающаяся, ПРИОСТАНОВЛЕННАЯ
// пользователем (paused), либо отцепленная (bot_id IS NULL — пользователь оставил её сам).
func (s *Server) directionHasLiveStrategy(ctx context.Context, accountID, symbol, dir, botID string) bool {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM strategies
			WHERE account_id=$1 AND symbol=$2 AND direction=$3
			  AND status IN ('active','finishing','paused')
			  AND (bot_id=$4 OR bot_id IS NULL))`,
		accountID, symbol, dir, botID,
	).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// ensureMatrixStrategies creates long and short strategies for each whitelisted symbol
// if they are not already active. Called every tick so the pair restarts automatically
// after a paired-close completes.
//
// posMap is the current exchange position snapshot. When a new strategy is created for a
// direction that already has an open exchange position (orphan from a previously failed
// strategy), the position is adopted so startMatrixCycle does not place a second L(0)
// market order that would double the exchange position.
func (s *Server) ensureMatrixStrategies(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo, skipSymbols map[string]bool) {
	delistSymbols := s.GetDelistingSymbols()

	for _, symbol := range whitelist {
		if skipSymbols[symbol] {
			// Pair was just closed this tick — posMap still shows the (closing) position;
			// re-adopting it here would re-fire the trigger. Reopens fresh next tick.
			continue
		}
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		for _, dir := range []string{"long", "short"} {
			// Skip if the bot already owns a live strategy for this slot, if the user
			// paused this leg (manual close → paused, don't recreate), or if a detached
			// (bot_id=NULL) strategy is still live on this account.
			if s.directionHasLiveStrategy(ctx, accountID, symbol, dir, botID) {
				continue
			}

			s.cleanupStoppedHedgeCards(ctx, botID, symbol, dir)

			b := botEngineRow{
				id:        botID,
				ownerID:   ownerID,
				accountID: accountID,
				whitelist: whitelist,
				blacklist: blacklist,
			}

			// If there is already an open exchange position in this direction (left by a
			// previously failed strategy), adopt it instead of opening a fresh market order.
			var adoptJSON *string
			exchangeSide := "Buy"
			if dir == "short" {
				exchangeSide = "Sell"
			}
			if bySymbol, ok := posMap[symbol]; ok {
				if pos, hasPos := bySymbol[exchangeSide]; hasPos && pos.Size > 0 {
					type adoptData struct {
						Size       string `json:"size"`
						EntryPrice string `json:"entry_price"`
					}
					raw, _ := json.Marshal(adoptData{
						Size:       strconv.FormatFloat(pos.Size, 'f', -1, 64),
						EntryPrice: strconv.FormatFloat(pos.EntryPrice, 'f', -1, 64),
					})
					adoptStr := string(raw)
					adoptJSON = &adoptStr
				}
			}

			if _, err := s.createBotStrategy(ctx, b, cfg, symbol, dir, 0, "", adoptJSON); err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Матрикс: %s %s — ошибка создания: %v", symbol, dir, err),
					"error", "matrix")
			} else if adoptJSON != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Матрикс: %s %s — открыт (поглощение существующей позиции %s)", symbol, dir, *adoptJSON),
					"info", "matrix")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Матрикс: %s %s — открыт", symbol, dir),
					"info", "matrix")
			}
		}
	}
}
