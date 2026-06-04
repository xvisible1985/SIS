// services/api-gateway/hedge_engine.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"sis/pkg/signal"
	"sis/pkg/trader"
)

const hedgeEngineInterval = 30 * time.Second

// runHedgeEngine runs the hedge bot automation loop every 30 s.
// It is launched as a goroutine alongside RunBotEngine.
func (s *Server) runHedgeEngine(ctx context.Context) {
	ticker := time.NewTicker(hedgeEngineInterval)
	defer ticker.Stop()
	s.hedgeEngineTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.hedgeEngineTick(ctx)
		}
	}
}

// hedgeEngineTick loads all active hedge bots and processes each one.
func (s *Server) hedgeEngineTick(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, account_id,
		       symbol_whitelist, symbol_blacklist,
		       strategy_config
		FROM bots
		WHERE status = 'active'
		  AND account_id IS NOT NULL
		  AND strategy_config->>'bot_kind' = 'hedge'`)
	if err != nil {
		log.Printf("hedge engine tick: %v", err)
		return
	}

	type hedgeBotRow struct {
		id        string
		ownerID   string
		accountID string
		whitelist []string
		blacklist []string
		stratCfg  []byte
	}

	var bots []hedgeBotRow
	for rows.Next() {
		var b hedgeBotRow
		if err := rows.Scan(&b.id, &b.ownerID, &b.accountID,
			&b.whitelist, &b.blacklist, &b.stratCfg); err == nil {
			bots = append(bots, b)
		}
	}
	rows.Close()

	for _, b := range bots {
		var cfg botCfgJSON
		if err := json.Unmarshal(b.stratCfg, &cfg); err != nil {
			continue
		}
		s.processHedgeBot(ctx, b.id, b.ownerID, b.accountID, b.whitelist, b.blacklist, cfg)
	}
}

// processHedgeBot processes a single hedge bot for one tick:
//  1. Fetches open exchange positions.
//  2. Checks existing hedges for deactivation.
//  3. Checks unhedged positions for activation.
func (s *Server) processHedgeBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON) {
	creds, err := s.loadBotAccountCreds(ctx, accountID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка ключей аккаунта: %v", err), "error", "system")
		return
	}

	rawPositions, err := trader.FetchPositions(ctx, creds)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка получения позиций: %v", err), "error", "system")
		return
	}

	posMap := buildHedgePosMap(rawPositions)

	s.checkHedgeDeactivation(ctx, botID, accountID, cfg, posMap)
	s.checkHedgeActivation(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap)
}

// ── Data types ────────────────────────────────────────────────────────────────

// hedgePosInfo holds parsed position data used for hedge decisions.
type hedgePosInfo struct {
	Symbol        string
	Side          string // "Buy" | "Sell"
	Size          float64
	EntryPrice    float64
	MarkPrice     float64
	UnrealisedPnl float64
	Leverage      float64
}

// buildHedgePosMap indexes positions by symbol → exchange-side → hedgePosInfo.
// Positions with zero size are excluded.
func buildHedgePosMap(positions []trader.Position) map[string]map[string]hedgePosInfo {
	m := make(map[string]map[string]hedgePosInfo)
	for _, raw := range positions {
		p, ok := parseHedgePos(raw)
		if !ok {
			continue
		}
		if m[p.Symbol] == nil {
			m[p.Symbol] = make(map[string]hedgePosInfo)
		}
		m[p.Symbol][p.Side] = p
	}
	return m
}

// parseHedgePos parses a raw trader.Position into hedgePosInfo.
// Returns false if the position is empty or contains invalid data.
func parseHedgePos(raw trader.Position) (hedgePosInfo, bool) {
	size, err := strconv.ParseFloat(raw.Size, 64)
	if err != nil || size <= 0 {
		return hedgePosInfo{}, false
	}
	entry, err := strconv.ParseFloat(raw.EntryPrice, 64)
	if err != nil || entry <= 0 {
		return hedgePosInfo{}, false
	}
	mark, err := strconv.ParseFloat(raw.MarkPrice, 64)
	if err != nil || mark <= 0 {
		return hedgePosInfo{}, false
	}
	pnl, _ := strconv.ParseFloat(raw.UnrealisedPnl, 64)
	lev, _ := strconv.ParseFloat(raw.Leverage, 64)
	if lev <= 0 {
		lev = 1
	}
	return hedgePosInfo{
		Symbol:        raw.Symbol,
		Side:          raw.Side,
		Size:          size,
		EntryPrice:    entry,
		MarkPrice:     mark,
		UnrealisedPnl: pnl,
		Leverage:      lev,
	}, true
}

// ── Direction helpers ─────────────────────────────────────────────────────────

func hedgeDirToSide(dir string) string {
	if dir == "long" {
		return "Buy"
	}
	return "Sell"
}

func hedgeSideToDir(side string) string {
	if side == "Buy" {
		return "long"
	}
	return "short"
}

func oppositeHedgeDir(dir string) string {
	if dir == "long" {
		return "short"
	}
	return "long"
}

// ── Metric helpers ────────────────────────────────────────────────────────────

// hedgeDrawdown returns how far the position is from entry in %, always ≥ 0 when losing.
func hedgeDrawdown(p hedgePosInfo) float64 {
	if p.Side == "Buy" { // long: losing when mark < entry
		return (p.EntryPrice - p.MarkPrice) / p.EntryPrice * 100
	}
	// short: losing when mark > entry
	return (p.MarkPrice - p.EntryPrice) / p.EntryPrice * 100
}

// hedgeROI returns ROI % relative to initial margin.
func hedgeROI(p hedgePosInfo) float64 {
	margin := p.EntryPrice * p.Size / p.Leverage
	if margin == 0 {
		return 0
	}
	return p.UnrealisedPnl / margin * 100
}

// ── Criteria checkers ─────────────────────────────────────────────────────────

// meetsActivationCriteria returns true when the main position should trigger a hedge.
func meetsActivationCriteria(p hedgePosInfo, cfg botCfgJSON) bool {
	threshold := cfg.HedgeActValue
	switch cfg.HedgeActType {
	case 0: // last_order% — user stores threshold as negative (e.g. -4 means 4% against position).
		// Use math.Abs so both -4 and 4 correctly require 4% drawdown.
		return hedgeDrawdown(p) >= math.Abs(threshold)
	case 1: // drawdown% — threshold stored as positive (e.g. 5 means 5% drawdown).
		return hedgeDrawdown(p) >= math.Abs(threshold)
	case 2: // pnl$ (position is losing, so pnl is negative)
		return p.UnrealisedPnl <= -math.Abs(threshold)
	case 3: // roi% (negative = loss)
		return hedgeROI(p) <= -math.Abs(threshold)
	}
	return false
}

// meetsDeactivationCriteria returns true when the main position has recovered enough
// to warrant deactivating the hedge.
func meetsDeactivationCriteria(mainPos hedgePosInfo, cfg botCfgJSON) bool {
	threshold := cfg.HedgeDeactValue
	switch cfg.HedgeDeactType {
	case 0: // drawdown% — deactivate when drawdown is below threshold (recovered)
		return hedgeDrawdown(mainPos) < threshold
	case 1: // pnl$ — deactivate when main pnl ≥ threshold
		return mainPos.UnrealisedPnl >= threshold
	case 2: // roi% — deactivate when main roi ≥ threshold
		return hedgeROI(mainPos) >= threshold
	case 3: // last_order% — treat as drawdown%
		return hedgeDrawdown(mainPos) < threshold
	case 4: // wait_pair — only paired-close condition controls deactivation
		return false
	}
	return false
}

// meetsPairedCloseCriteria returns true when both positions together satisfy the
// combined P&L/ROI/breakeven condition.
func meetsPairedCloseCriteria(mainPos, hPos hedgePosInfo, cfg botCfgJSON) bool {
	combined := mainPos.UnrealisedPnl + hPos.UnrealisedPnl
	switch cfg.HedgeDeactCloseType {
	case 0: // combined pnl$ ≥ threshold
		return combined >= cfg.HedgeDeactCloseValue
	case 1: // combined roi%
		mainMargin := mainPos.EntryPrice * mainPos.Size / mainPos.Leverage
		hMargin := hPos.EntryPrice * hPos.Size / hPos.Leverage
		totalMargin := mainMargin + hMargin
		if totalMargin == 0 {
			return false
		}
		return combined/totalMargin*100 >= cfg.HedgeDeactCloseValue
	case 2: // breakeven (combined pnl ≥ 0)
		return combined >= 0
	}
	return false
}

// ── Bot filter ────────────────────────────────────────────────────────────────

// positionPassesBotFilter returns true if the position at symbol/mainDir on
// accountID is allowed by the hedge bot's bot whitelist/blacklist.
//
// Logic:
//   - Queries which bots (if any) own active strategies for this symbol+direction+account.
//   - If the position has no bot (manual trading) it is treated as bot_id = "".
//   - Blacklist takes priority: if ANY matching bot is blacklisted → false.
//   - Whitelist (non-empty): at LEAST ONE matching bot must be whitelisted → true.
//   - Empty whitelist + empty blacklist → always true.
func (s *Server) positionPassesBotFilter(
	ctx context.Context,
	accountID, symbol, mainDir string,
	whitelist, blacklist []string,
) bool {
	if len(whitelist) == 0 && len(blacklist) == 0 {
		return true
	}

	// Find all bot_ids of strategies for this symbol+direction+account.
	// Include 'stopped' so that a stopped strategy still counts for whitelist/blacklist
	// matching — the exchange position may still be open even if the strategy is stopped.
	// NULL bot_id (manual strategy) is coalesced to empty string "".
	rows, err := s.pool.Query(ctx,
		`SELECT COALESCE(bot_id, '') AS bot_id
		 FROM strategies
		 WHERE account_id = $1
		   AND symbol     = $2
		   AND direction  = $3
		   AND status IN ('active', 'finishing', 'stopped')`,
		accountID, symbol, mainDir)
	if err != nil {
		return true // on DB error, don't block
	}
	defer rows.Close()

	var botIDs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			botIDs = append(botIDs, id)
		}
	}

	// Blacklist check — any match → reject.
	for _, bid := range botIDs {
		for _, bl := range blacklist {
			if bid == bl {
				return false
			}
		}
	}

	// Whitelist check — must match at least one.
	if len(whitelist) > 0 {
		for _, bid := range botIDs {
			for _, wl := range whitelist {
				if bid == wl {
					return true
				}
			}
		}
		return false
	}

	return true
}

// ── Symbol filter ─────────────────────────────────────────────────────────────

// symbolPassesHedgeFilter checks whether a symbol should be considered by this hedge bot.
// For hedge bots, whitelist/blacklist are applied directly to position symbols
// (not expanded via glob against the full exchange universe).
func symbolPassesHedgeFilter(symbol string, whitelist, blacklist, delistSymbols []string) bool {
	for _, d := range delistSymbols {
		if d == symbol {
			return false
		}
	}
	for _, b := range blacklist {
		if b == symbol {
			return false
		}
	}
	if len(whitelist) == 0 {
		return true
	}
	for _, w := range whitelist {
		if w == symbol {
			return true
		}
	}
	return false
}

// ── Activation ────────────────────────────────────────────────────────────────

// checkHedgeActivation iterates over open exchange positions and creates hedge
// strategies for those that meet the activation criteria and have no active hedge yet.
func (s *Server) checkHedgeActivation(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) {
	delistSymbols := s.GetDelistingSymbols()

	for _, bySymbol := range posMap {
		for side, pos := range bySymbol {
			if !symbolPassesHedgeFilter(pos.Symbol, whitelist, blacklist, delistSymbols) {
				continue
			}

			mainDir := hedgeSideToDir(side)

			// Bot whitelist/blacklist filter — skip positions not owned by allowed bots.
			if !s.positionPassesBotFilter(ctx, accountID, pos.Symbol, mainDir,
				cfg.HedgeBotWhitelist, cfg.HedgeBotBlacklist) {
				continue
			}

			// Direction filter
			switch cfg.Direction {
			case "long":
				if mainDir != "long" {
					continue
				}
			case "short":
				if mainDir != "short" {
					continue
				}
			// "both": accept any direction
			}

			hedgeDir := oppositeHedgeDir(mainDir)

			// ── Find the main strategy record for this position ───────────────
			// Used to populate hedged_strategy_id so only ONE hedge bot can claim
			// a given main strategy (enforced by a DB unique partial index).
			var mainStrategyID string
			s.pool.QueryRow(ctx, //nolint:errcheck
				`SELECT id FROM strategies
				 WHERE account_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing','stopped')
				 ORDER BY CASE status
				          WHEN 'active'    THEN 0
				          WHEN 'finishing' THEN 1
				          ELSE 2 END,
				          created_at DESC
				 LIMIT 1`,
				accountID, pos.Symbol, mainDir,
			).Scan(&mainStrategyID)

			// ── Cross-bot check: already have an active hedge for this position? ──
			// Primary path: look for any hedge whose hedged_strategy_id matches.
			if mainStrategyID != "" {
				var existingID string
				if err := s.pool.QueryRow(ctx,
					`SELECT id FROM strategies
					 WHERE hedged_strategy_id=$1
					   AND status IN ('active','finishing')
					 LIMIT 1`,
					mainStrategyID).Scan(&existingID); err == nil {
					continue // already claimed by another hedge bot
				}
			}
			// Fallback (manual positions with no strategy record): check by account+symbol+dir.
			{
				var existingID string
				if err := s.pool.QueryRow(ctx,
					`SELECT s.id FROM strategies s
					 JOIN bots b ON b.id = s.bot_id
					 WHERE s.account_id=$1 AND s.symbol=$2 AND s.direction=$3
					   AND s.status IN ('active','finishing')
					   AND b.strategy_config->>'bot_kind' = 'hedge'
					 LIMIT 1`,
					accountID, pos.Symbol, hedgeDir).Scan(&existingID); err == nil {
					continue // already hedged by another hedge bot
				}
			}

			// ── This bot's own active hedge (quick self-check) ────────────────
			{
				var existingID string
				if err := s.pool.QueryRow(ctx,
					`SELECT id FROM strategies
					 WHERE bot_id=$1 AND symbol=$2 AND direction=$3
					   AND status IN ('active','finishing')
					 LIMIT 1`,
					botID, pos.Symbol, hedgeDir).Scan(&existingID); err == nil {
					continue // this bot already has a hedge
				}
			}

			// For last_order% (type 0): measure drawdown from the last filled grid level's
			// price rather than avg entry.  We no longer wait for all levels to fill —
			// if the hedge slot is empty the hedge is created immediately once the
			// threshold is crossed; if the slot already has a position,
			// resolveHedgeSlotConflict below handles "wait" or "force-close" logic.
			evalPos := pos
			if cfg.HedgeActType == 0 && mainStrategyID != "" {
				var cycleID string
				_ = s.pool.QueryRow(ctx,
					`SELECT id FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL LIMIT 1`,
					mainStrategyID).Scan(&cycleID)

				if cycleID != "" {
					var lastFilledPrice float64
					_ = s.pool.QueryRow(ctx,
						`SELECT COALESCE(filled_price, target_price)
						   FROM strategy_levels
						  WHERE cycle_id=$1 AND status='filled'
						  ORDER BY level_idx DESC LIMIT 1`,
						cycleID).Scan(&lastFilledPrice)
					if lastFilledPrice > 0 {
						evalPos.EntryPrice = lastFilledPrice
					}
				}
			}

			// Check activation criteria
			if !meetsActivationCriteria(evalPos, cfg) {
				continue
			}

			// Optional activation signal filter
			if len(cfg.ActivationSignals) > 0 {
				sigCfgs := make([]signal.Config, 0, len(cfg.ActivationSignals))
				valid := true
				for _, a := range cfg.ActivationSignals {
					sc := signal.Config{Name: a.Name, Params: a.Params}
					if _, err := signal.Build(sc); err != nil {
						valid = false
						break
					}
					sigCfgs = append(sigCfgs, sc)
				}
				if !valid {
					continue
				}

				interval := "15"
				for _, a := range cfg.ActivationSignals {
					if v, ok := a.Params["tf"].(string); ok && v != "" {
						interval = v
						break
					}
				}

				state := s.signalEngine.ComputeStateForce(pos.Symbol, interval, sigCfgs)
				var want signal.State
				if hedgeDir == "short" {
					want = signal.Sell
				} else {
					want = signal.Buy
				}
				if state != want {
					s.logBotEvent(ctx, botID,
						fmt.Sprintf("Хедж: %s %s — условие выполнено, сигнал не подтверждён (%v), пропуск",
							pos.Symbol, mainDir, state),
						"info", "hedge")
					continue
				}
			}

			// Handle conflicting strategy in the hedge slot.
			// suspendedSlotID is non-empty when HedgeCloseType=2: the strategy was suspended
			// and must be linked to the new hedge for restoration on deactivation.
			suspendedSlotID, ok := s.resolveHedgeSlotConflict(ctx, botID, accountID, pos.Symbol, hedgeDir, cfg, creds, posMap)
			if !ok {
				continue
			}

			// Remove any empty stopped card for this bot+symbol+direction before
			// creating a new one, so duplicate cards don't pile up in the UI.
			s.cleanupStoppedHedgeCards(ctx, botID, pos.Symbol, hedgeDir)

			// Create hedge strategy — passes mainStrategyID so the DB unique index
			// prevents a second bot from creating a duplicate even under a race condition.
			b := botEngineRow{
				id:        botID,
				ownerID:   ownerID,
				accountID: accountID,
				whitelist: whitelist,
				blacklist: blacklist,
			}
			hedgeStrategyID, err := s.createBotStrategy(ctx, b, cfg, pos.Symbol, hedgeDir, 0, mainStrategyID)
			if err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка создания %s %s: %v", pos.Symbol, hedgeDir, err),
					"error", "hedge")
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: активирован %s %s (тип=%d, порог=%.4g)",
						pos.Symbol, hedgeDir, cfg.HedgeActType, cfg.HedgeActValue),
					"info", "hedge")

				// Link any suspended slot strategy to the new hedge so it gets restored on deactivation.
				if suspendedSlotID != "" && hedgeStrategyID != "" {
					if _, linkErr := s.pool.Exec(ctx,
						`UPDATE strategies SET hedge_stopped_by=$1 WHERE id=$2`,
						hedgeStrategyID, suspendedSlotID); linkErr != nil {
						s.logBotEvent(ctx, botID,
							fmt.Sprintf("Хедж: ошибка привязки приостановленной стратегии %s к хеджу %s: %v",
								suspendedSlotID[:8], hedgeStrategyID[:8], linkErr),
							"warn", "hedge")
					} else {
						s.logBotEvent(ctx, botID,
							fmt.Sprintf("Хедж: стратегия слота %s привязана к хеджу %s — восстановится при деактивации",
								suspendedSlotID[:8], hedgeStrategyID[:8]),
							"info", "hedge")
					}
				}

				if hedgeStrategyID != "" {
					s.applyHedgeMainControls(ctx, botID, mainStrategyID, hedgeStrategyID, cfg)

					// ── Record hedge session ──────────────────────────────────────────────
					var mainEntryAtStart *float64
					if pos.EntryPrice > 0 {
						v := pos.EntryPrice
						mainEntryAtStart = &v
					}
					var mainStratIDPtr *string
					if mainStrategyID != "" {
						mainStratIDPtr = &mainStrategyID
					}
					if _, sessErr := s.pool.Exec(ctx,
						`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, main_entry_at_start)
						 VALUES ($1, $2, $3, $4)`,
						botID, mainStratIDPtr, hedgeStrategyID, mainEntryAtStart,
					); sessErr != nil {
						s.logBotEvent(ctx, botID,
							fmt.Sprintf("Хедж: ошибка записи сессии для %s: %v", hedgeStrategyID[:8], sessErr),
							"warn", "hedge")
					}
				}
			}
		}
	}
}

// applyHedgeMainControls sets suppression flags on the main strategy when a hedge activates.
// If HedgeStopMain: sets status='stopped' + tp/sl suppressed + hedge_stopped_by.
// If HedgeCancelMainTp/Sl (without stop): only sets suppression flags.
// Notifies the strategy engine so the runner reacts immediately.
func (s *Server) applyHedgeMainControls(ctx context.Context, botID, mainStrategyID, hedgeStrategyID string, cfg botCfgJSON) {
	if mainStrategyID == "" {
		return
	}
	if !cfg.HedgeCancelMainTp && !cfg.HedgeCancelMainSl && !cfg.HedgeStopMain {
		return
	}

	tpSuppressed := cfg.HedgeCancelMainTp || cfg.HedgeStopMain
	slSuppressed := cfg.HedgeCancelMainSl || cfg.HedgeStopMain

	var err error
	if cfg.HedgeStopMain {
		// Hard stop: set stopped status, suppress both TP and SL, record which hedge stopped it.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET status='stopped', hedge_tp_suppressed=true, hedge_sl_suppressed=true, hedge_stopped_by=$1
			 WHERE id=$2`,
			hedgeStrategyID, mainStrategyID)
	} else {
		// Only suppress TP and/or SL — do not stop the main strategy.
		_, err = s.pool.Exec(ctx,
			`UPDATE strategies
			 SET hedge_tp_suppressed=$1, hedge_sl_suppressed=$2
			 WHERE id=$3`,
			tpSuppressed, slSuppressed, mainStrategyID)
	}
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка применения управления Main (%s): %v", mainStrategyID[:8], err),
			"error", "hedge")
		return
	}

	// Log to the main strategy's event log so it appears in Log Visualizer.
	var parts []string
	if tpSuppressed {
		parts = append(parts, "TP подавлен")
	}
	if slSuppressed {
		parts = append(parts, "SL подавлен")
	}
	if cfg.HedgeStopMain {
		parts = append(parts, "бот остановлен")
	}
	stratMsg := "Хедж активирован: " + strings.Join(parts, ", ")
	s.pool.Exec(ctx, //nolint:errcheck
		`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
		mainStrategyID, stratMsg)

	go s.engine.Notify(context.Background(), mainStrategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: управление Main применено (tp_sup=%v sl_sup=%v stop=%v) → %s",
			tpSuppressed, slSuppressed, cfg.HedgeStopMain, mainStrategyID[:8]),
		"info", "hedge")
}

// restoreHedgeMainControls clears suppression flags on the main strategy when a hedge deactivates.
// If the hedge had stopped the main (hedge_stopped_by = this hedge), also restores status='active'.
// Also restores any slot strategies that were suspended via resolveHedgeSlotConflict case 2
// (they have hedge_stopped_by = hedgeStrategyID but are NOT the hedged_strategy_id strategy).
func (s *Server) restoreHedgeMainControls(ctx context.Context, botID, hedgeStrategyID string) {
	// ── Part 1: Restore main strategy (hedged via hedged_strategy_id) ────────────
	var mainStrategyID string
	_ = s.pool.QueryRow(ctx,
		`SELECT COALESCE(hedged_strategy_id::text,'') FROM strategies WHERE id=$1`,
		hedgeStrategyID).Scan(&mainStrategyID)

	if mainStrategyID != "" {
		var stoppedByID string
		if err := s.pool.QueryRow(ctx,
			`SELECT COALESCE(hedge_stopped_by::text,'') FROM strategies WHERE id=$1`,
			mainStrategyID).Scan(&stoppedByID); err == nil {

			var execErr error
			if stoppedByID == hedgeStrategyID {
				// We stopped the main — restore it to active and clear all suppression.
				_, execErr = s.pool.Exec(ctx,
					`UPDATE strategies
					 SET status='active', hedge_tp_suppressed=false, hedge_sl_suppressed=false, hedge_stopped_by=NULL
					 WHERE id=$1`,
					mainStrategyID)
			} else {
				// We only suppressed TP/SL — just clear those flags.
				_, execErr = s.pool.Exec(ctx,
					`UPDATE strategies
					 SET hedge_tp_suppressed=false, hedge_sl_suppressed=false
					 WHERE id=$1`,
					mainStrategyID)
			}
			if execErr != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: ошибка восстановления Main (%s): %v", mainStrategyID[:8], execErr),
					"error", "hedge")
			} else {
				restoreMsg := "Хедж деактивирован: TP/SL восстановлены"
				if stoppedByID == hedgeStrategyID {
					restoreMsg = "Хедж деактивирован: бот возобновлён, TP/SL восстановлены"
				}
				s.pool.Exec(ctx, //nolint:errcheck
					`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
					mainStrategyID, restoreMsg)
				go s.engine.Notify(context.Background(), mainStrategyID)
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: управление Main сброшено → %s (restore_stop=%v)",
						mainStrategyID[:8], stoppedByID == hedgeStrategyID),
					"info", "hedge")
			}
		}
	}

	// ── Part 2: Restore suspended slot strategies ─────────────────────────────
	// These are strategies suspended during hedge slot conflict resolution (HedgeCloseType=2).
	// Identified by hedge_stopped_by = hedgeStrategyID; excludes the main strategy already handled above.
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM strategies WHERE hedge_stopped_by = $1`,
		hedgeStrategyID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: ошибка поиска приостановленных стратегий слота: %v", err),
			"warn", "hedge")
		return
	}
	var slotIDs []string
	for rows.Next() {
		var slotID string
		if rows.Scan(&slotID) == nil && slotID != mainStrategyID {
			slotIDs = append(slotIDs, slotID)
		}
	}
	rows.Close()

	for _, slotID := range slotIDs {
		if _, err := s.pool.Exec(ctx,
			`UPDATE strategies
			 SET status='active', hedge_stopped_by=NULL,
			     hedge_tp_suppressed=false, hedge_sl_suppressed=false,
			     updated_at=NOW()
			 WHERE id=$1`, slotID); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: ошибка восстановления стратегии слота %s: %v", slotID[:8], err),
				"error", "hedge")
			continue
		}
		s.pool.Exec(ctx, //nolint:errcheck
			`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
			slotID, "Хедж деактивирован: стратегия слота возобновлена, новый цикл")
		go s.engine.Notify(context.Background(), slotID)
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: стратегия слота %s восстановлена → новый цикл", slotID[:8]),
			"info", "hedge")
	}
}

// cleanupStoppedHedgeCards deletes stopped hedge strategy cards owned by this bot
// for the given symbol+direction that have no filled levels (i.e. no open position).
// Called before creating a new hedge strategy to prevent empty duplicate cards in the UI.
// ON DELETE CASCADE ensures strategy_cycles, strategy_levels, strategy_events are removed too.
func (s *Server) cleanupStoppedHedgeCards(ctx context.Context, botID, symbol, hedgeDir string) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM strategies
		 WHERE bot_id=$1 AND symbol=$2 AND direction=$3
		   AND status='stopped'
		   AND NOT EXISTS (
		     SELECT 1 FROM strategy_cycles sc
		     JOIN strategy_levels sl ON sl.cycle_id=sc.id
		     WHERE sc.strategy_id=strategies.id AND sl.status='filled'
		   )`,
		botID, symbol, hedgeDir)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		if _, err := s.pool.Exec(ctx, `DELETE FROM strategies WHERE id=$1`, id); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: не удалось удалить пустую карточку %s %s (%s): %v", symbol, hedgeDir, id[:8], err),
				"warn", "hedge")
		} else {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: удалена пустая остановленная карточка %s %s", symbol, hedgeDir),
				"info", "hedge")
		}
	}
}

// resolveHedgeSlotConflict checks for a strategy already occupying the hedge slot
// (owned by a different bot or manual).
// Returns (suspendedStrategyID, ok):
//   - ok=false  → slot is occupied and cannot be freed yet; skip this activation
//   - ok=true   → slot is free or was freed; proceed with hedge creation
//   - suspendedStrategyID non-empty only for HedgeCloseType=2: the strategy that was suspended
//     and must be linked to the new hedge strategy via hedge_stopped_by for later restoration.
func (s *Server) resolveHedgeSlotConflict(ctx context.Context, botID, accountID, symbol, hedgeDir string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) (string, bool) {
	var conflictID string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM strategies
		 WHERE account_id=$1 AND symbol=$2 AND direction=$3
		   AND status IN ('active','finishing')
		   AND (bot_id IS NULL OR bot_id <> $4)
		 LIMIT 1`,
		accountID, symbol, hedgeDir, botID).Scan(&conflictID)
	if err != nil {
		return "", true // no conflict
	}

	switch cfg.HedgeCloseType {
	case 0: // wait for cycle end — leave the conflicting strategy alone
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s %s — слот занят (%s), ожидание завершения цикла", symbol, hedgeDir, conflictID[:8]),
			"info", "hedge")
		return "", false

	case 1: // force-close if the conflicting position's loss exceeds the threshold
		conflictSide := hedgeDirToSide(hedgeDir)
		if bySymbol, ok := posMap[symbol]; ok {
			if p, ok := bySymbol[conflictSide]; ok {
				if p.UnrealisedPnl <= -math.Abs(cfg.HedgeCloseValue) {
					s.pool.Exec(ctx, //nolint:errcheck
						`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, conflictID)
					go s.engine.Notify(context.Background(), conflictID)
					s.logBotEvent(ctx, botID,
						fmt.Sprintf("Хедж: %s — конфликтующий слот принудительно закрыт (PnL %.4g ≤ -%.4g)",
							symbol, p.UnrealisedPnl, cfg.HedgeCloseValue),
						"info", "hedge")
					return "", true
				}
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: %s %s — слот занят, убыток %.4g ниже порога %.4g, ожидание",
						symbol, hedgeDir, p.UnrealisedPnl, cfg.HedgeCloseValue),
					"info", "hedge")
				return "", false
			}
		}
		return "", false

	case 2: // suspend & restore — cancel orders, stop strategy, restore when hedge ends
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s %s — приостанавливаем конфликтующую стратегию %s",
				symbol, hedgeDir, conflictID[:8]),
			"info", "hedge")

		// Get category from the conflicting strategy for order cancellation.
		var category string
		s.pool.QueryRow(ctx, `SELECT category FROM strategies WHERE id=$1`, conflictID).Scan(&category) //nolint:errcheck
		if category == "" {
			category = "linear" // safe fallback
		}

		// Cancel all active orders synchronously — maximum speed before stopping the strategy.
		cancelled, cancelErrors := s.cancelStrategyOrders(ctx, botID, conflictID, symbol, category, creds)

		// Stop the strategy in DB.
		if _, err := s.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, conflictID); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: %s — ошибка остановки конфликтующей стратегии %s: %v",
					symbol, conflictID[:8], err),
				"error", "hedge")
			return "", false
		}
		// Notify engine async so it synchronises internal state (may attempt cancel again — harmless).
		go s.engine.Notify(context.Background(), conflictID)

		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s %s — стратегия %s приостановлена (отменено ордеров: %d, ошибок отмены: %d)",
				symbol, hedgeDir, conflictID[:8], cancelled, cancelErrors),
			"info", "hedge")
		s.pool.Exec(ctx, //nolint:errcheck
			`INSERT INTO strategy_events (strategy_id, message, level) VALUES ($1, $2, 'info')`,
			conflictID, "Приостановлена хедж-ботом — будет восстановлена после деактивации хеджа")

		return conflictID, true
	}
	return "", true
}

// cancelStrategyOrders cancels all active exchange orders for the given strategy:
// the cycle-level TP and SL orders, all placed grid-level orders, and per-level SL orders.
// Orders are cancelled directly via the exchange API for maximum speed.
// Returns count of successfully cancelled orders and count of errors.
func (s *Server) cancelStrategyOrders(ctx context.Context, botID, stratID, symbol, category string, creds trader.Credentials) (cancelled, errors int) {
	// Get active cycle and its TP/SL order IDs.
	var cycleID, tpOrderID, slOrderID string
	if err := s.pool.QueryRow(ctx,
		`SELECT id, COALESCE(tp_order_id,''), COALESCE(sl_order_id,'')
		 FROM strategy_cycles WHERE strategy_id=$1 AND ended_at IS NULL LIMIT 1`,
		stratID).Scan(&cycleID, &tpOrderID, &slOrderID); err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — нет активного цикла, ордера не отменялись", symbol),
			"info", "hedge")
		return 0, 0
	}

	// Cancel TP order (regular limit/market order).
	if tpOrderID != "" {
		if err := trader.CancelOrder(ctx, creds, trader.CancelRequest{
			Symbol: symbol, Category: category, OrderId: tpOrderID,
		}); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: %s — TP ордер %s не отменён: %v", symbol, tpOrderID[:8], err),
				"warn", "hedge")
			errors++
		} else {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: %s — TP ордер %s отменён", symbol, tpOrderID[:8]),
				"info", "hedge")
			cancelled++
		}
	}

	// Cancel SL order (conditional stop — try StopOrder filter first, then plain).
	if slOrderID != "" {
		err1 := trader.CancelOrder(ctx, creds, trader.CancelRequest{
			Symbol: symbol, Category: category, OrderId: slOrderID, OrderFilter: "StopOrder",
		})
		if err1 != nil {
			err2 := trader.CancelOrder(ctx, creds, trader.CancelRequest{
				Symbol: symbol, Category: category, OrderId: slOrderID,
			})
			if err2 != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: %s — SL ордер %s не отменён: %v", symbol, slOrderID[:8], err1),
					"warn", "hedge")
				errors++
			} else {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: %s — SL ордер %s отменён", symbol, slOrderID[:8]),
					"info", "hedge")
				cancelled++
			}
		} else {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: %s — SL ордер %s отменён", symbol, slOrderID[:8]),
				"info", "hedge")
			cancelled++
		}
	}

	// Get all placed level orders.
	rows, err := s.pool.Query(ctx,
		`SELECT COALESCE(exchange_order_id,''), COALESCE(sl_order_id,'')
		 FROM strategy_levels
		 WHERE cycle_id=$1 AND status='placed' AND COALESCE(exchange_order_id,'') != ''`,
		cycleID)
	if err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — ошибка запроса ордеров уровней: %v", symbol, err),
			"warn", "hedge")
		return cancelled, errors + 1
	}
	type lvlOrder struct{ orderID, slID string }
	var lvlOrders []lvlOrder
	for rows.Next() {
		var o lvlOrder
		if rows.Scan(&o.orderID, &o.slID) == nil {
			lvlOrders = append(lvlOrders, o)
		}
	}
	rows.Close()

	for _, o := range lvlOrders {
		if err := trader.CancelOrder(ctx, creds, trader.CancelRequest{
			Symbol: symbol, Category: category, OrderId: o.orderID,
		}); err != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Хедж: %s — ордер уровня %s не отменён: %v", symbol, o.orderID[:8], err),
				"warn", "hedge")
			errors++
		} else {
			cancelled++
		}
		// Level SL (conditional) — best-effort, not counted in errors.
		if o.slID != "" {
			trader.CancelOrder(ctx, creds, trader.CancelRequest{ //nolint:errcheck
				Symbol: symbol, Category: category, OrderId: o.slID, OrderFilter: "StopOrder",
			})
		}
	}

	if len(lvlOrders) > 0 {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — обработано %d ордеров уровней", symbol, len(lvlOrders)),
			"info", "hedge")
	}
	return cancelled, errors
}

// ── Deactivation ──────────────────────────────────────────────────────────────

// hedgeHasOpenFilledLevels returns true if the hedge strategy has at least one
// filled level in an open (not-yet-ended) cycle. Used as a safeguard before
// stopping a hedge due to "no exchange positions" — if the DB says there are
// filled levels, the exchange API likely returned stale/empty data after
// a server reconnect, and we should skip the stop for this tick.
func (s *Server) hedgeHasOpenFilledLevels(ctx context.Context, strategyID string) bool {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM strategy_levels sl
		 JOIN strategy_cycles sc ON sl.cycle_id = sc.id
		 WHERE sc.strategy_id = $1 AND sc.ended_at IS NULL AND sl.status = 'filled'`,
		strategyID,
	).Scan(&count)
	return err == nil && count > 0
}

// checkHedgeDeactivation inspects all active hedge strategies for this bot and
// stops them when deactivation or paired-close conditions are met.
func (s *Server) checkHedgeDeactivation(ctx context.Context, botID, accountID string, cfg botCfgJSON, posMap map[string]map[string]hedgePosInfo) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol, direction FROM strategies
		 WHERE bot_id=$1 AND status IN ('active','finishing')`,
		botID)
	if err != nil {
		return
	}
	type activeHedge struct{ id, symbol, dir string }
	var hedges []activeHedge
	for rows.Next() {
		var h activeHedge
		if rows.Scan(&h.id, &h.symbol, &h.dir) == nil {
			hedges = append(hedges, h)
		}
	}
	rows.Close()

	if len(hedges) == 0 {
		return
	}

	for _, h := range hedges {
		mainDir := oppositeHedgeDir(h.dir)
		mainSide := hedgeDirToSide(mainDir)
		hedgeSide := hedgeDirToSide(h.dir)

		bySymbol, hasSymbol := posMap[h.symbol]
		if !hasSymbol {
			// No positions for this symbol at all.
			// Guard against false positives right after server reconnect: if the hedge
			// strategy still has filled levels in an open cycle, the API likely returned
			// stale/empty data. Skip this tick — the next tick will re-fetch.
			if s.hedgeHasOpenFilledLevels(ctx, h.id) {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: позиции %s не найдены, но в БД есть заполненные уровни — пропускаем тик (возможно временный ответ API)", h.symbol),
					"warn", "hedge")
				continue
			}
			s.stopHedgeStrategy(ctx, botID, h.id, h.symbol, "нет позиций на бирже")
			continue
		}

		mainPos, hasMain := bySymbol[mainSide]
		if !hasMain {
			// Main position gone. Same guard: if the hedge still has an open cycle with
			// filled levels, be conservative and skip rather than risk a false stop.
			if s.hedgeHasOpenFilledLevels(ctx, h.id) {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: основная позиция %s не найдена, но хедж имеет заполненные уровни — пропускаем тик", h.symbol),
					"warn", "hedge")
				continue
			}
			// Main position gone — no reason to keep hedge.
			s.stopHedgeStrategy(ctx, botID, h.id, h.symbol, "основная позиция закрыта")
			continue
		}

		hedgePos, hasHedge := bySymbol[hedgeSide]

		// Fill hedge_entry_at_start once the hedge position is visible on the exchange.
		// gap_at_start = |main_entry - hedge_entry| — immutable once set.
		if hasHedge && hedgePos.EntryPrice > 0 {
			s.pool.Exec(ctx, //nolint:errcheck
				`UPDATE hedge_sessions
				 SET hedge_entry_at_start = $1,
				     gap_at_start = CASE
				         WHEN main_entry_at_start IS NOT NULL
				         THEN ABS(main_entry_at_start - $1)
				         ELSE NULL
				     END
				 WHERE hedge_strategy_id = $2
				   AND ended_at IS NULL
				   AND hedge_entry_at_start IS NULL`,
				hedgePos.EntryPrice, h.id)
		}

		// Deactivation: main position recovered.
		if cfg.HedgeDeactType != 4 && meetsDeactivationCriteria(mainPos, cfg) {
			s.stopHedgeStrategy(ctx, botID, h.id, h.symbol,
				fmt.Sprintf("деактивация: основная позиция восстановилась (тип=%d, порог=%.4g)",
					cfg.HedgeDeactType, cfg.HedgeDeactValue))
			continue
		}

		// Paired close: combined P&L condition.
		if hasHedge && meetsPairedCloseCriteria(mainPos, hedgePos, cfg) {
			combined := mainPos.UnrealisedPnl + hedgePos.UnrealisedPnl
			s.stopHedgeStrategy(ctx, botID, h.id, h.symbol,
				fmt.Sprintf("парное закрытие: суммарный PnL %.4g (тип=%d, порог=%.4g)",
					combined, cfg.HedgeDeactCloseType, cfg.HedgeDeactCloseValue))

			// Also stop the main strategy if one exists (managed by another bot or manual).
			var mainStratID string
			if qerr := s.pool.QueryRow(ctx,
				`SELECT id FROM strategies
				 WHERE account_id=$1 AND symbol=$2 AND direction=$3
				   AND status IN ('active','finishing')
				   AND (bot_id IS NULL OR bot_id <> $4)
				 LIMIT 1`,
				accountID, h.symbol, mainDir, botID).Scan(&mainStratID); qerr == nil {
				s.pool.Exec(ctx, //nolint:errcheck
					`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`, mainStratID)
				go s.engine.Notify(context.Background(), mainStratID)
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Хедж: %s — основная стратегия %s остановлена (парное закрытие)", h.symbol, mainStratID),
					"info", "hedge")
			}
			continue
		}

		// Trailing profit: if hedge position in profit ≥ step%, take profit now.
		if cfg.HedgeProfitLazy && hasHedge {
			hROI := hedgeROI(hedgePos)
			if hROI >= cfg.HedgeProfitLazyPct {
				s.stopHedgeStrategy(ctx, botID, h.id, h.symbol,
					fmt.Sprintf("трейлинг профит: ROI хеджа %.4g%% ≥ шаг %.4g%%", hROI, cfg.HedgeProfitLazyPct))
				continue
			}
		}
	}
}

// stopHedgeStrategy sets a hedge strategy to 'stopped' and notifies the engine.
func (s *Server) stopHedgeStrategy(ctx context.Context, botID, strategyID, symbol, reason string) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW() WHERE id=$1`,
		strategyID); err != nil {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Хедж: %s — ошибка остановки стратегии: %v", symbol, err),
			"error", "hedge")
		return
	}
	go s.engine.Notify(context.Background(), strategyID)
	// Close the hedge session.
	s.pool.Exec(ctx, //nolint:errcheck
		`UPDATE hedge_sessions SET ended_at = NOW()
		 WHERE hedge_strategy_id = $1 AND ended_at IS NULL`,
		strategyID)
	// Restore main strategy controls when this hedge deactivates.
	s.restoreHedgeMainControls(ctx, botID, strategyID)
	s.logBotEvent(ctx, botID,
		fmt.Sprintf("Хедж: %s — стратегия остановлена (%s)", symbol, reason),
		"info", "hedge")
}
