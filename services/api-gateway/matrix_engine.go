// services/api-gateway/matrix_engine.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"sis/pkg/signal"
	"sis/pkg/trader"
)

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
		// TEMP DIAGNOSTIC (see matching note in ensureMatrixStrategies): this stop makes
		// the strategy briefly invisible to ensureMatrixStrategies' active-count query
		// (status flips to 'stopped') before a later tick recreates it — if THIS is the
		// live limit-overshoot's real trigger, id here should match a strategy_id that
		// reappears in a "Матрикс[diag tick=...]: ... открыт" line for the SAME
		// symbol+direction on a subsequent tick, net-growing the bot's active count by one
		// per such stop+recreate pair instead of staying flat.
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Матрикс[diag]: %s %s — зомби-стратегия (active без цикла) остановлена для пересоздания (id=%s)", z.symbol, z.dir, z.id[:8]),
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
		if long.live && !short.live && !short.paused {
			result[sym] = "short"
		} else if short.live && !long.live && !long.paused {
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
		`SELECT count(*) FILTER (WHERE direction='long'), count(*) FILTER (WHERE direction='short')
		 FROM strategies WHERE bot_id=$1 AND status IN ('active','finishing')`,
		botID,
	).Scan(&activeLong, &activeShort); err != nil {
		s.logBotEvent(ctx, botID, fmt.Sprintf("Матрикс: ошибка подсчёта активных стратегий: %v", err), "error", "matrix")
		return
	}
	activeTotal := activeLong + activeShort

	// TEMP DIAGNOSTIC (remove once the live limit-overshoot incident is root-caused):
	// tickID lets us spot two ensureMatrixStrategies calls for the SAME bot overlapping
	// in time (their logged entry/creation lines would interleave with different tickIDs)
	// — the smoking-gun signature of a concurrency bug the static call-graph didn't reveal.
	// If instead every creation's "было" counters look internally consistent (each new
	// creation's logged snapshot correctly reflects all prior creations THIS tick, and no
	// tickID ever overlaps another for the same bot), the bug is elsewhere (e.g. a status
	// transition between ticks that a single snapshot can't catch) — check bot_events for
	// intervening 'stopped'→active flips on the runner between two "открыт" lines instead.
	tickID := time.Now().UnixNano()
	if maxTotal > 0 || maxLong > 0 || maxShort > 0 {
		s.logBotEvent(ctx, botID,
			fmt.Sprintf("Матрикс[diag tick=%d]: вход в тик, active total=%d/%d long=%d/%d short=%d/%d",
				tickID, activeTotal, maxTotal, activeLong, maxLong, activeShort, maxShort),
			"info", "matrix-limit-debug")
	}

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

	// Repair pass: symbols already missing one leg get priority over brand-new candidates
	// for the same scarce long/short capacity — see matrixRepairCandidates' doc comment
	// and docs/superpowers/specs/2026-07-24-matrix-slot-repair-priority-design.md for why.
	// Bypasses the activation-signal gate entirely (unlike the new-pair loop below): a pair
	// that already has one real leg in the market is worse off staying unbalanced than
	// getting its other leg back without waiting for a fresh signal — same philosophy the
	// existing adopt-orphan-position path below already uses.
	for symbol, dir := range s.matrixRepairCandidates(ctx, botID) {
		if maxTotal > 0 && activeTotal >= maxTotal {
			break
		}
		if !symbolPassesHedgeFilter(symbol, nil, blacklist, delistSymbols) {
			continue
		}
		if s.directionHasLiveStrategy(ctx, accountID, symbol, dir, botID) {
			continue // became live since the candidates query ran, or is actually 'paused'
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
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — ошибка восстановления: %v", symbol, dir, err),
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
			s.logBotEvent(ctx, botID,
				fmt.Sprintf("Матрикс[repair]: %s %s — восстановлена недостающая нога стало total=%d long=%d short=%d",
					symbol, dir, activeTotal, activeLong, activeShort),
				"info", "matrix")
		}
	}

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
		activationOK := s.matrixActivationSignalOK(symbol, cfg)
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
						fmt.Sprintf("Матрикс[diag tick=%d]: %s %s — открыт (поглощение существующей позиции %s) стало total=%d long=%d short=%d",
							tickID, symbol, dir, *adoptJSON, activeTotal, activeLong, activeShort),
						"info", "matrix")
				} else {
					s.logBotEvent(ctx, botID,
						fmt.Sprintf("Матрикс[diag tick=%d]: %s %s — открыт стало total=%d long=%d short=%d",
							tickID, symbol, dir, activeTotal, activeLong, activeShort),
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
	interval := "15"
	for _, a := range cfg.ActivationSignals {
		if v, ok := a.Params["tf"].(string); ok && v != "" {
			interval = v
			break
		}
	}
	state := s.signalEngine.ComputeStateForce(symbol, interval, sigCfgs)
	return state != signal.Neutral
}
