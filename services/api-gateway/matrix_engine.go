// services/api-gateway/matrix_engine.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sis/pkg/signal"
	"sis/pkg/trader"
)

// ensureMatrixHedgeSession makes sure a hedge_sessions row exists for this matrix pair
// (idempotent — matches the bootstrap pattern used for grid+matrix hedge pairs). long=main,
// hedge=short by convention; GetHedgeSession sums whichever leg's own strategy_id is
// requested using this row's started_at/end_reason as the accumulation window boundary.
//
// Always syncs main_strategy_id to the caller-supplied longID, not just when it was
// previously NULL: createBotStrategy refuses to reuse a stopped strategy row with a still-
// open cycle (would resurrect a stale cycle), so the long leg can get a brand-new
// strategy_id after a restart while hedge_strategy_id (the short leg) stays the same. A
// narrower guard (update only if NULL) left main_strategy_id pointing at the superseded
// stopped strategy forever — GetHedgeSession looks up by the CURRENT main.id and found no
// row, so the UI's "Накоплено матрикс" silently went blank even though this row kept
// accumulating correctly via hedge_strategy_id. longID here is always the caller's current
// tick's live main leg, so it's always safe to write.
func ensureMatrixHedgeSession(ctx context.Context, pool *pgxpool.Pool, botID, longID, shortID string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (hedge_strategy_id) WHERE ended_at IS NULL
		 DO UPDATE SET main_strategy_id = EXCLUDED.main_strategy_id
		 WHERE hedge_sessions.main_strategy_id IS DISTINCT FROM EXCLUDED.main_strategy_id`,
		botID, longID, shortID)
	return err
}

// processMatrixBot processes a single matrix bot for one tick:
//  1. Checks existing strategy pairs for the paired-close condition.
//  2. Ensures both long and short strategies are running for each whitelisted symbol.
func (s *Server) processMatrixBot(ctx context.Context, botID, ownerID, accountID string, whitelist, blacklist []string, cfg botCfgJSON, pairedWatches map[string]pairedCloseWatchEntry) {
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
	closed := s.checkMatrixPairedClose(ctx, botID, accountID, cfg, creds, posMap)
	s.checkMatrixZombieStrategies(ctx, botID, posMap)
	s.ensureMatrixStrategies(ctx, botID, ownerID, accountID, whitelist, blacklist, cfg, creds, posMap, closed)
	s.buildPairedCloseWatches(ctx, botID, accountID, "matrix", cfg, posMap, pairedWatches)
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
			// Ghost-close: a position is open while the latest cycle is ended.
			// Revive the cycle only when it has filled levels — there is real state to preserve.
			// If cycleID=nil (no cycle at all) or hasFilled=false (cycle had no fills before it
			// ended), the position was not created by this zombie's cycle — either the position
			// belongs to another strategy/bot, or it is a posMap artifact. Fall through to stop
			// the zombie; ensureMatrixStrategies will create a fresh replacement that properly
			// adopts the exchange position via posMap on this same tick.
			if z.cycleID != nil && s.reviveMatrixSplitBrain(ctx, botID, z.id, *z.cycleID, z.cycleNum, z.symbol, z.dir) {
				continue // successfully revived — engine resumes managing the live position
			}
			// Not revived (no cycle, or 0-fill ended cycle): stop the zombie below.
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
			fmt.Sprintf("Матрикс: %s %s — зомби-стратегия (active без цикла) остановлена для пересоздания (id=%s)", z.symbol, z.dir, z.id[:8]),
			"warn", "matrix")
	}
}

// reviveMatrixSplitBrain repairs a matrix leg whose latest cycle is marked ended while its
// exchange position is still open (a false ghost_close). It clears the cycle's ended_at so
// loadActiveCycle picks it up again and the chart/counters match the live position, and it
// removes the phantom ghost_close trade recorded for that false close so realised PnL is not
// double-counted when the position eventually closes for real. Notify makes the runner adopt
// the revived cycle's existing orders without placing a duplicate entry.
//
// Returns true when the cycle was successfully revived, false when the cycle has no filled
// levels (nothing to revive — the caller should stop the zombie strategy instead and let
// ensureMatrixStrategies create a fresh replacement that adopts the position via posMap).
func (s *Server) reviveMatrixSplitBrain(ctx context.Context, botID, stratID, cycleID string, cycleNum *int, symbol, dir string) bool {
	// Only revive a cycle that actually holds a filled level — otherwise there is no cycle
	// state to preserve and ensureMatrixStrategies' adopt path is the right handler.
	var hasFilled bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM strategy_levels WHERE cycle_id=$1 AND status='filled')`, cycleID,
	).Scan(&hasFilled); err != nil || !hasFilled {
		return false
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE strategy_cycles SET ended_at=NULL, result=NULL WHERE id=$1 AND ended_at IS NOT NULL`, cycleID)
	if err != nil || tag.RowsAffected() == 0 {
		return false
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
	return true
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
func (s *Server) checkMatrixPairedClose(ctx context.Context, botID, accountID string, cfg botCfgJSON, creds trader.Credentials, posMap map[string]map[string]hedgePosInfo) map[string]bool {
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
		ensureMatrixHedgeSession(ctx, s.pool, botID, p.longID, p.shortID) //nolint:errcheck

		bySymbol, ok := posMap[sym]
		if !ok {
			continue
		}
		longPos, hasLong := bySymbol["Buy"]
		shortPos, hasShort := bySymbol["Sell"]
		if !hasLong || !hasShort {
			continue
		}
		var accumulatedPnl float64
		if err := s.pool.QueryRow(ctx,
			`SELECT accumulated_pnl FROM hedge_sessions
			 WHERE hedge_strategy_id=$1 AND ended_at IS NULL`, p.shortID).Scan(&accumulatedPnl); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			s.logBotEvent(ctx, botID, fmt.Sprintf("checkMatrixPairedClose: accumulated_pnl fetch for %s: %v — falling back to live-only PnL for this tick", sym, err), "warn", "matrix")
		}
		if meetsPairedCloseCriteria(longPos, shortPos, cfg, accumulatedPnl) {
			combined := longPos.UnrealisedPnl + shortPos.UnrealisedPnl
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс: %s — парное закрытие (PnL=%.4g, тип=%d, порог=%.4g)",
					sym, combined, cfg.HedgeDeactCloseType, cfg.HedgeDeactCloseValue),
				"info", "matrix")
			s.stopMatrixPair(ctx, botID, accountID, sym, p.longID, p.shortID, creds, category, longPos, shortPos)
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
func (s *Server) stopMatrixPair(ctx context.Context, botID, accountID, symbol, longID, shortID string, creds trader.Credentials, category string, longPos, shortPos hedgePosInfo) {
	// Tell the strategy engine BEFORE placing the closing orders: when the WS position-
	// zero event arrives for these legs, label the resulting trade_history row
	// "paired_close" (this is a bot decision, not the user closing by hand) instead of
	// the default "manual_close". bot_id is already correct either way — this only
	// fixes the misleading label on the История сделок page.
	if s.engine != nil {
		s.engine.NotifyExpectedClose(longID, accountID, "paired_close")
		s.engine.NotifyExpectedClose(shortID, accountID, "paired_close")
	}

	// Realize the combined profit: paired-close must CLOSE both exchange positions.
	// Stopping the strategies alone does NOT flat a matrix position (the cycle is kept
	// open by design), so without this the positions linger, ensureMatrixStrategies
	// re-adopts them, and the trigger re-fires every tick without ever taking profit.
	for _, leg := range []struct {
		pos     hedgePosInfo
		posIdx  int
		stratID string
	}{{longPos, 1, longID}, {shortPos, 2, shortID}} {
		req, ok := matrixLegCloseRequest(leg.pos, symbol, category, leg.posIdx)
		if !ok {
			continue
		}
		// Was "SIS_MPC_{posIdx}_{ms}" — no embedded strategy id at all, so
		// ClosedPnlSyncer's linkId-based step 1b could never recognize this order and it
		// always fell through to the old time-window heuristics (steps 2-4). Step 3 there
		// matches "the currently open cycle for this symbol+direction+account" with NO
		// check that it's actually the SAME cycle the closing order belongs to — for a
		// pair that reopens quickly after a paired-close (ensureMatrixStrategies routinely
		// does), that query can catch the BRAND NEW cycle instead and force-close it with
		// the OLD order's closeTime, well within the "2 minutes old" zombie threshold.
		// Found live (2026-07-20): a HEMIUSDT short cycle flagged "цикл оживлён" 53s after
		// starting — far too fast to be its own paired-close, but exactly consistent with
		// the PRIOR incarnation's SIS_MPC_ close being misattributed onto it. Tagging with
		// the real owning strategy's id (LinkIDSelfClose, -scl-) routes it through step 1b
		// instead, which is scoped to the exact strategy — immune to this cross-cycle mixup.
		req.OrderLinkId = fmt.Sprintf("SIS_STR-%s-scl-%d-%d", leg.stratID[:8], leg.posIdx, time.Now().UnixMilli())
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
// пользователем (paused), отцепленная (bot_id IS NULL — пользователь оставил её сам), или
// принадлежащая ЛЮБОМУ ДРУГОМУ боту на этом же аккаунте. Раньше проверка была ограничена
// (bot_id=$4 OR bot_id IS NULL) — своим ботом и открепленными, но не видела активные
// стратегии чужих ботов вовсе. Живой инцидент (2026-07-21): MatrixNova и ST-Fast оба
// открыли ARKMUSDT short с разницей в пару минут, каждый считая символ свободным, потому
// что каждый смотрел только на свои собственные строки. У хедж-ботов уже была отдельная
// защита от этого (resolveHedgeSlotConflict, hedge_engine.go) — здесь применяем тот же
// принцип: любая чужая активная/завершающаяся/приостановленная стратегия по этому
// symbol+direction на аккаунте блокирует открытие, независимо от bot_id.
func (s *Server) directionHasLiveStrategy(ctx context.Context, accountID, symbol, dir, botID string) bool {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM strategies
			WHERE account_id=$1 AND symbol=$2 AND direction=$3
			  AND status IN ('active','finishing','paused'))`,
		accountID, symbol, dir,
	).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// matrixRepairCandidates returns symbols (for this bot) that currently have exactly one
// direction ('long' or 'short') active/finishing, mapped to the OTHER, missing direction
// that should be reopened to restore a balanced pair. A symbol is a candidate whether its
// missing direction has no strategy row at all, or has one that's 'stopped' (TP/SL closed
// naturally, eligible for auto-restart) — but never if the missing direction is 'paused'
// (user-initiated stop, must stay closed). The query also fetches 'paused' rows (not just
// 'active'/'finishing') specifically so this exclusion can be enforced here, in the
// candidate query itself, rather than relying on every future caller to separately guard
// against ever touching a paused leg.
//
// A symbol can have at most one row per (symbol, direction) among the statuses queried
// here: `uniq_bot_active_strategy` is a partial unique index on (bot_id, symbol, direction)
// WHERE status IN ('active','finishing'), and 'paused' is only ever reached via an in-place
// UPDATE of an existing active/finishing row (pkg/strategy/cycle.go), never a fresh INSERT —
// so the per-(symbol,direction) map assignment below can never silently pick between two
// conflicting rows for the same key.
func (s *Server) matrixRepairCandidates(ctx context.Context, botID string) map[string]string {
	rows, err := s.pool.Query(ctx,
		`SELECT symbol, direction, status FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing','paused')`,
		botID)
	if err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("matrixRepairCandidates: query error: %v", err), "error", "matrix")
		return nil
	}
	defer rows.Close()

	type dirState struct{ live, paused bool }
	haveDir := make(map[string]map[string]dirState)
	for rows.Next() {
		var sym, dir, status string
		if rows.Scan(&sym, &dir, &status) != nil {
			continue
		}
		if haveDir[sym] == nil {
			haveDir[sym] = make(map[string]dirState)
		}
		haveDir[sym][dir] = dirState{
			live:   status == "active" || status == "finishing",
			paused: status == "paused",
		}
	}

	result := make(map[string]string)
	for sym, dirs := range haveDir {
		long, short := dirs["long"], dirs["short"]
		// In a matrix bot, 'paused' is set automatically when a position is externally
		// closed (phantom-adopt, manual exchange close, etc.) — not by user action. A
		// paused leg with an active partner is therefore a repair candidate: the partner
		// is still live and the pair needs to be restored. We intentionally no longer
		// exclude paused directions here; the repair pass will clear the paused row to
		// 'stopped' before creating the replacement, keeping state consistent.
		if long.live && !short.live {
			result[sym] = "short"
		} else if short.live && !long.live {
			result[sym] = "long"
		}
	}
	return result
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

	// Per-bot strategy count limits (0 = unlimited) — stored on the bots table itself,
	// not botCfgJSON, and were NEVER enforced anywhere in the matrix engine before this
	// fix. Before the empty-whitelist-means-all-symbols fallback below existed, this
	// limit was "accidentally" enforced by whoever sized their whitelist to match it;
	// once that fallback could expand the loop to 400+ exchange symbols, nothing bounded
	// how many pairs got opened in a single tick (live incident, 2026-07-17: a bot
	// configured for max 4 opened 10+ before being stopped by hand).
	var maxTotal, maxLong, maxShort int
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(max_strategies,0), COALESCE(max_long_strategies,0), COALESCE(max_short_strategies,0) FROM bots WHERE id=$1`,
		botID,
	).Scan(&maxTotal, &maxLong, &maxShort); err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка чтения лимитов бота: %v", err), "error", "matrix")
		return
	}
	var activeLong, activeShort int
	if err := s.pool.QueryRow(ctx,
		// Include 'paused' in slot accounting: a paused leg (externally closed, e.g.
		// phantom-adopt) still occupies its pair's slot and must not free it for the main
		// loop to fill with a brand-new symbol. Without this, a one-legged pair with its
		// partner paused looks like a half-empty slot → the main loop opens a new pair
		// for a different symbol, exceeding the configured pair limit.
		`SELECT count(*) FILTER (WHERE direction='long'), count(*) FILTER (WHERE direction='short')
		 FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing','paused')`,
		botID,
	).Scan(&activeLong, &activeShort); err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка подсчёта активных стратегий: %v", err), "error", "matrix")
		return
	}
	activeTotal := activeLong + activeShort

	// Empty whitelist means "all symbols" everywhere else in this app (the bot form's own
	// hint says so, and symbolPassesHedgeFilter treats it that way) — but unlike hedge
	// bots, which iterate existing exchange positions and only use the whitelist as a
	// pass/fail filter, matrix bots need an actual symbol list to drive this loop. Without
	// this fallback a matrix bot with no whitelist configured silently opened nothing,
	// ever, regardless of activation signals — found live (2026-07-16) when a bot's own
	// signal scan (which fetches all symbols independently) showed matching pairs, but
	// the tick itself had zero symbols to iterate over in the first place.
	symbols := whitelist
	if len(symbols) == 0 {
		all, err := trader.FetchAllLinearSymbols(ctx)
		if err != nil {
			s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка получения списка символов: %v", err), "error", "matrix")
			return
		}
		symbols = all
	}

	// Aggregate counters for a single end-of-tick summary log — NOT logged per symbol,
	// since with the empty-whitelist fallback above this loop can now cover 400+ exchange
	// symbols per tick and a per-symbol "signal didn't confirm" log (like hedge bots emit,
	// hedge_engine.go's "условие выполнено, сигнал не подтверждён") would flood bot_events.
	// This summary line is also the main diagnostic for "signal scan shows matches but the
	// bot isn't opening anything": if it shows 0/0 confirmed every tick while the scan
	// finds hits, the live gate is genuinely evaluating differently from the scan (config/
	// signal bug); if the line never appears at all, the tick isn't completing (timeout).
	checkedActivation, confirmedActivation := 0, 0

	// repairCooldown is the minimum time between successive failed repair attempts for the
	// same (bot, symbol, direction) tuple. Prevents a persistently-failing symbol (delisted,
	// order rejected, etc.) from spamming createBotStrategy and logBotEvent every 30s tick.
	const repairCooldown = 5 * time.Minute

	// adoptCooldown: if a strategy transitioned to 'stopped' within the last 30 seconds
	// (e.g. just TP'd), posMap may still show the now-closed position — a stale WS snapshot
	// that arrives in the gap between the TP fill event and the subsequent position-0 update.
	// Adopting a phantom position causes cycle N+1 to "manage" a non-existent position for
	// minutes, then close as manual_close/paused. Fix: skip that (symbol, dir) this tick;
	// on the next tick (≤30s later) posMap will reflect the true 0 position.
	const adoptCooldown = 30 * time.Second
	type symDirKey struct{ sym, dir string }
	recentlyStopped := make(map[symDirKey]bool)
	if cdRows, cdErr := s.pool.Query(ctx,
		`SELECT symbol, direction FROM strategies
		 WHERE bot_id=$1 AND status='stopped' AND updated_at > NOW() - $2::interval`,
		botID, adoptCooldown,
	); cdErr == nil {
		for cdRows.Next() {
			var sym, dir string
			if cdRows.Scan(&sym, &dir) == nil {
				recentlyStopped[symDirKey{sym, dir}] = true
			}
		}
		cdRows.Close()
	}

	// Repair pass: symbols already missing one leg get priority over brand-new candidates
	// for the same scarce long/short capacity — see matrixRepairCandidates' doc comment
	// and docs/superpowers/specs/2026-07-24-matrix-slot-repair-priority-design.md for why.
	// Bypasses the activation-signal gate entirely (unlike the new-pair loop below): a pair
	// that already has one real leg in the market is worse off staying unbalanced than
	// getting its other leg back without waiting for a fresh signal — same philosophy the
	// existing adopt-orphan-position path below already uses.
	for symbol, dir := range s.matrixRepairCandidates(ctx, botID) {
		// Repair restores a missing leg of an ALREADY-EXISTING pair — the partner is
		// still live and the slot was already allocated before the leg went paused/stopped.
		// Strategy limits (maxTotal/maxLong/maxShort) must NOT block repair: applying them
		// here would permanently strand one-legged pairs whenever the bot is at capacity
		// (e.g. max_total=4, 4 active legs across 2 complete pairs + 2 partners of broken
		// pairs → activeTotal=4=maxTotal → break before any repair candidate is processed).
		// Limits are checked in the main-pair loop below, which only opens brand-new pairs.
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		// Narrower than directionHasLiveStrategy: only block on active/finishing.
		// A paused leg (externally closed, partner still live) is exactly what the
		// repair pass is here to fix — don't treat it as a blocker.
		// Cross-bot race is still caught: if another bot opened this (account, symbol,
		// dir) between the candidates query and now, it shows up as 'active' here.
		var activeLive bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM strategies WHERE account_id=$1 AND symbol=$2 AND direction=$3 AND status IN ('active','finishing'))`,
			accountID, symbol, dir,
		).Scan(&activeLive); err != nil || activeLive {
			continue
		}
		// If the missing leg is paused (external close / phantom-adopt), reset it to
		// 'stopped' so the new repair strategy doesn't coexist with a stale paused row.
		pauseTag, uerr := s.pool.Exec(ctx,
			`UPDATE strategies SET status='stopped' WHERE bot_id=$1 AND symbol=$2 AND direction=$3 AND status='paused'`,
			botID, symbol, dir,
		)
		if uerr != nil {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — ошибка сброса паузы перед восстановлением: %v", symbol, dir, uerr),
				"error", "matrix")
			continue
		}
		// wasPaused=true means the leg was paused: its slot was already counted in
		// activeLong/activeShort (initial query includes 'paused'). Restoring it does not
		// add a new slot — it's the same slot transitioning paused→active. So we must NOT
		// increment the counters after repair in this case (to avoid double-counting and
		// incorrectly blocking the main loop from opening legitimately new pairs).
		wasPaused := pauseTag.RowsAffected() > 0
		if wasPaused {
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — паузированная нога переведена в stopped, создаю замену", symbol, dir),
				"info", "matrix")
		}

		cooldownKey := botID + ":" + symbol + ":" + dir
		if t, ok := s.repairFailedAt.Load(cooldownKey); ok && time.Since(t.(time.Time)) < repairCooldown {
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

		// Skip adopt if posMap may be stale: the leg just TP'd and the position-0 WS event
		// hasn't landed yet. On the next tick posMap will be accurate.
		if recentlyStopped[symDirKey{symbol, dir}] {
			if bySymbol, ok := posMap[symbol]; ok {
				exchangeSide := "Buy"
				if dir == "short" {
					exchangeSide = "Sell"
				}
				if pos, hasPos := bySymbol[exchangeSide]; hasPos && pos.Size > 0 {
					continue // posMap stale post-TP; retry next tick with fresh snapshot
				}
			}
		}

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

		if id, err := s.createBotStrategy(ctx, b, cfg, symbol, dir, 0, "", adoptJSON); err != nil {
			s.repairFailedAt.Store(cooldownKey, time.Now())
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — ошибка восстановления: %v", symbol, dir, err),
				"error", "matrix")
		} else if id != "" {
			s.repairFailedAt.Delete(cooldownKey)
			if !wasPaused {
				// The restored leg was stopped/missing (not paused), so it was NOT
				// counted in the initial activeLong/activeShort query. Increment now
				// so the main loop knows this slot is occupied for this tick.
				activeTotal++
				if dir == "long" {
					activeLong++
				} else {
					activeShort++
				}
			}
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — восстановлена недостающая нога стало total=%d long=%d short=%d",
					symbol, dir, activeTotal, activeLong, activeShort),
				"info", "matrix")
		} else {
			// createBotStrategy returned empty id without error: ON CONFLICT DO NOTHING fired —
			// another process claimed the slot between our activeLive check and the INSERT.
			// The repair will retry on the next tick (repairFailedAt NOT set — this is not a
			// persistent failure, just a transient race).
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — слот занят (race), повтор через 30с", symbol, dir),
				"warn", "matrix")
		}
	}

	// Computed concurrently (bounded) up front — see matrixBatchCheckActivation for why
	// a sequential per-symbol call in this loop is dangerous now that symbols can number
	// in the hundreds.
	activationResults := s.matrixBatchCheckActivation(ctx, symbols, cfg)

	for _, symbol := range symbols {
		if maxTotal > 0 && activeTotal >= maxTotal {
			break // total limit reached — no point scanning remaining symbols this tick
		}
		if skipSymbols[symbol] {
			// Pair was just closed this tick — posMap still shows the (closing) position;
			// re-adopting it here would re-fire the trigger. Reopens fresh next tick.
			continue
		}
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		// Checked once per symbol — direction-agnostic, matrix opens both legs together.
		activationOK := activationResults[symbol]
		if len(cfg.ActivationSignals) > 0 {
			checkedActivation++
			if activationOK {
				confirmedActivation++
			}
		}
		for _, dir := range []string{"long", "short"} {
			// Skip if the bot already owns a live strategy for this slot, if the user
			// paused this leg (manual close → paused, don't recreate), or if a detached
			// (bot_id=NULL) strategy is still live on this account.
			if s.directionHasLiveStrategy(ctx, accountID, symbol, dir, botID) {
				continue
			}
			if maxTotal > 0 && activeTotal >= maxTotal {
				continue
			}
			if dir == "long" && maxLong > 0 && activeLong >= maxLong {
				continue
			}
			if dir == "short" && maxShort > 0 && activeShort >= maxShort {
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
					// Guard against stale posMap: if this direction just TP'd, the position-0
					// WS event may not have arrived yet. Skip adopt; the activation gate below
					// will still allow a fresh open if the signal confirms.
					if !recentlyStopped[symDirKey{symbol, dir}] {
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
			}

			// An already-open exchange position must always be adopted regardless of the
			// activation signal — leaving it unmanaged is worse than opening early. A fresh
			// open (nothing to adopt) waits for activation to confirm, same as hedge bots.
			if adoptJSON == nil && !activationOK {
				continue
			}

			if id, err := s.createBotStrategy(ctx, b, cfg, symbol, dir, 0, "", adoptJSON); err != nil {
				s.logBotEvent(ctx, botID,
					fmt.Sprintf("Матрикс: %s %s — ошибка создания: %v", symbol, dir, err),
					"error", "matrix")
			} else {
				if id != "" {
					activeTotal++
					if dir == "long" {
						activeLong++
					} else {
						activeShort++
					}
				}
				if adoptJSON != nil {
					s.logBotEvent(ctx, botID,
						fmt.Sprintf("Матрикс: %s %s — открыт (поглощение существующей позиции %s) стало total=%d long=%d short=%d",
							symbol, dir, *adoptJSON, activeTotal, activeLong, activeShort),
						"info", "matrix")
				} else {
					s.logBotEvent(ctx, botID,
						fmt.Sprintf("Матрикс: %s %s — открыт стало total=%d long=%d short=%d",
							symbol, dir, activeTotal, activeLong, activeShort),
						"info", "matrix")
				}
			}
		}
	}

	if checkedActivation > 0 {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Матрикс: проверка активации — %d/%d символов подтвердили сигнал", confirmedActivation, checkedActivation),
			"info", "matrix")
	}
}

// matrixActivationSignalOK reports whether a matrix bot's configured activation signals
// (if any) currently confirm activation for symbol. Mirrors the hedge bot's "Optional
// activation signal filter" (hedge_engine.go), but direction-agnostic: matrix opens
// long and short together (see ensureMatrixStrategies), so any non-Neutral state counts
// — Buy or Sell both pass. This lets a signal like price-change be used purely for its
// |change| >= threshold% magnitude check here; its trend/counter-trend mode still picks
// Buy vs Sell under the hood, but matrix ignores which one fired, only that one did.
// Empty ActivationSignals always passes — backward compatible with existing matrix bots,
// which opened immediately before this gate existed.
func (s *Server) matrixActivationSignalOK(symbol string, cfg botCfgJSON) bool {
	if len(cfg.ActivationSignals) == 0 {
		return true
	}
	sigCfgs := make([]signal.Config, 0, len(cfg.ActivationSignals))
	for _, a := range cfg.ActivationSignals {
		sc := signal.Config{Name: a.Name, Params: a.Params}
		if _, err := signal.Build(sc); err != nil {
			return false
		}
		sigCfgs = append(sigCfgs, sc)
	}
	state := s.signalEngine.ComputeStateForce(symbol, matrixActivationInterval(cfg), sigCfgs)
	return state != signal.Neutral
}

// matrixActivationInterval extracts the candle interval a matrix bot's activation
// signals should be evaluated on — the first "tf" param found among ActivationSignals,
// falling back to "15". Shared by matrixActivationSignalOK (per-symbol check) and
// matrixBatchCheckActivation (which also uses it to warm the interval) so they can never
// disagree on which interval is actually being evaluated.
func matrixActivationInterval(cfg botCfgJSON) string {
	for _, a := range cfg.ActivationSignals {
		if v, ok := a.Params["tf"].(string); ok && v != "" {
			return v
		}
	}
	return "15"
}

// matrixBatchCheckActivation computes matrixActivationSignalOK for many symbols
// concurrently (bounded, mirroring ScanSignals' semaphore pattern in bots_handler.go),
// after first warming the interval via the SAME GlobalWarmer signal-bot activation
// already uses (bot_engine.go's "STEP 3: EnsureIntervals"). Two separate problems, one
// fix:
//   - Cold ticks: matrix/hedge bots were explicitly skipped from the warming path
//     (bot_engine.go's botEngineTick), so every symbol's first check ever did a REST
//     fetch. With ~275 symbols checked sequentially, a single tick could take many
//     minutes and stall the entire bot engine (hedge bots included, since they share the
//     tick loop) — found live (2026-07-17): zero bot_events logged anywhere for 11+
//     minutes after a restart. The bounded concurrency below addresses this even without
//     warming, but warming is what makes the fetch a one-time cost instead of per-tick.
//   - Stale data forever after: SnapshotOrFetch's one-shot REST fallback caches whatever
//     it fetched and, having ≥2 candles, never re-fetches — there is no live WS feed
//     refreshing it. Without EnsureIntervals establishing a real subscription, a matrix
//     bot's activation signal would keep evaluating against the same frozen candle
//     snapshot from the first cold fetch indefinitely, never reflecting real price
//     movement again.
func (s *Server) matrixBatchCheckActivation(ctx context.Context, symbols []string, cfg botCfgJSON) map[string]bool {
	result := make(map[string]bool, len(symbols))
	if len(cfg.ActivationSignals) == 0 {
		for _, sym := range symbols {
			result[sym] = true
		}
		return result
	}
	s.globalWarmer.EnsureIntervals([]string{matrixActivationInterval(cfg)})
	sem := make(chan struct{}, 20)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sym := range symbols {
		sym := sym
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			ok := s.matrixActivationSignalOK(sym, cfg)
			mu.Lock()
			result[sym] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	return result
}
