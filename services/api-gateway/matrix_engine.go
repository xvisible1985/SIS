// services/api-gateway/matrix_engine.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

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

	s.checkMatrixPairedClose(ctx, botID, cfg, posMap)
	s.checkMatrixZombieStrategies(ctx, botID, posMap)
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap)
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
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.symbol, s.direction
		 FROM strategies s
		 WHERE s.bot_id=$1 AND s.strategy_type='matrix' AND s.status='active'
		   AND NOT EXISTS (SELECT 1 FROM strategy_cycles c WHERE c.strategy_id=s.id AND c.ended_at IS NULL)
		   AND COALESCE(
		         (SELECT MAX(ended_at) FROM strategy_cycles c WHERE c.strategy_id=s.id),
		         s.created_at
		       ) < NOW() - INTERVAL '2 minutes'`,
		botID)
	if err != nil {
		return
	}
	type zombie struct{ id, symbol, dir string }
	var zombies []zombie
	for rows.Next() {
		var z zombie
		if rows.Scan(&z.id, &z.symbol, &z.dir) == nil {
			zombies = append(zombies, z)
		}
	}
	rows.Close()

	for _, z := range zombies {
		side := "Buy"
		if z.dir == "short" {
			side = "Sell"
		}
		if bySym, ok := posMap[z.symbol]; ok {
			if p, ok := bySym[side]; ok && p.Size > 0 {
				continue // position still open — leave it for the engine to reopen/adopt
			}
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

// checkMatrixPairedClose inspects all active strategy pairs (long+short) for this bot
// and fires the paired-close condition when the combined P&L target is met.
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID string, cfg botCfgJSON, posMap map[string]map[string]hedgePosInfo) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol, direction FROM strategies
		 WHERE bot_id=$1 AND status IN ('active','finishing')`,
		botID)
	if err != nil {
		return
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
		longPos, hasLong   := bySymbol["Buy"]
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
			s.stopMatrixPair(ctx, botID, sym, p.longID, p.shortID)
		}
	}
}

// stopMatrixPair stops both legs of a matrix strategy pair, notifies the engine,
// and closes the pair's session with end_reason='paired_close' — the only
// genuine reset trigger for the "Накоплено матрикс" cumulative counter.
func (s *Server) stopMatrixPair(ctx context.Context, botID, symbol, longID, shortID string) {
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

// ensureMatrixStrategies creates long and short strategies for each whitelisted symbol
// if they are not already active. Called every tick so the pair restarts automatically
// after a paired-close completes.
//
// posMap is the current exchange position snapshot. When a new strategy is created for a
// direction that already has an open exchange position (orphan from a previously failed
// strategy), the position is adopted so startMatrixCycle does not place a second L(0)
// market order that would double the exchange position.
func (s *Server) ensureMatrixStrategies(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) {
	delistSymbols := s.GetDelistingSymbols()

	for _, symbol := range whitelist {
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		for _, dir := range []string{"long", "short"} {
			var existingID string
			// Skip if bot already owns an active strategy for this slot,
			// or if a detached (bot_id=NULL) strategy is still active on this account —
			// the user detached it intentionally, don't create a duplicate.
			if err := s.pool.QueryRow(ctx,
				`SELECT id FROM strategies
				 WHERE account_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing')
				   AND (bot_id=$4 OR bot_id IS NULL)
				 LIMIT 1`,
				accountID, symbol, dir, botID).Scan(&existingID); err == nil {
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
