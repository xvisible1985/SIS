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
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap)
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

// stopMatrixPair stops both legs of a matrix strategy pair and notifies the engine.
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
			if err := s.pool.QueryRow(ctx,
				`SELECT id FROM strategies
				 WHERE bot_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing')
				 LIMIT 1`,
				botID, symbol, dir).Scan(&existingID); err == nil {
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
