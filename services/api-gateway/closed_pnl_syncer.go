// services/api-gateway/closed_pnl_syncer.go
package main

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
	"sis/pkg/strategy"
	"sis/pkg/trader"
)

// ClosedPnlSyncer detects closed positions that are NOT attributed to any strategy
// (manual trades) and writes them to trade_history with source='manual'.
// Strategy trades are handled by RecordStrategyTrade called from closeCycle().
type ClosedPnlSyncer struct {
	pool   *pgxpool.Pool
	encKey string
	mu     sync.Mutex
	// lastSync tracks the last processed time per account.
	// On first run we look back 90 seconds (matching the ticker interval + buffer).
	lastSync map[string]time.Time
	running  map[string]context.CancelFunc
}

func NewClosedPnlSyncer(pool *pgxpool.Pool, encKey string) *ClosedPnlSyncer {
	return &ClosedPnlSyncer{
		pool:     pool,
		encKey:   encKey,
		lastSync: make(map[string]time.Time),
		running:  make(map[string]context.CancelFunc),
	}
}

// Start launches account discovery and per-account sync goroutines.
func (s *ClosedPnlSyncer) Start(ctx context.Context) {
	go func() {
		s.loadAndLaunch(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.loadAndLaunch(ctx)
			}
		}
	}()
}

type closedPnlAccount struct {
	id             string
	ownerID        string
	apiKeyEnc      string
	secretEnc      string
	whitelistedIPs []string
	exchange       string
}

func (s *ClosedPnlSyncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, api_key_enc, secret_enc, whitelisted_ips, exchange FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("closed_pnl_syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a closedPnlAccount
		if err := rows.Scan(&a.id, &a.ownerID, &a.apiKeyEnc, &a.secretEnc, &a.whitelistedIPs, &a.exchange); err != nil {
			continue
		}
		s.mu.Lock()
		_, running := s.running[a.id]
		s.mu.Unlock()
		if !running {
			s.launch(ctx, a)
		}
	}
}

func (s *ClosedPnlSyncer) launch(ctx context.Context, a closedPnlAccount) {
	childCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.running[a.id] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, a.id)
			s.mu.Unlock()
		}()
		s.runAccount(childCtx, a)
	}()
}

func (s *ClosedPnlSyncer) runAccount(ctx context.Context, a closedPnlAccount) {
	apiKey, err := crypto.Decrypt(a.apiKeyEnc, s.encKey)
	if err != nil {
		log.Printf("closed_pnl_syncer: decrypt account=%s: %v", a.id, err)
		return
	}
	secret, err := crypto.Decrypt(a.secretEnc, s.encKey)
	if err != nil {
		log.Printf("closed_pnl_syncer: decrypt account=%s: %v", a.id, err)
		return
	}
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret, AccountID: a.id, WhitelistedIPs: a.whitelistedIPs}
	ex := strategy.ResolveExchange(a.exchange, creds, trader.NewTradeStream(creds))

	// Offset from execution syncer (60s) to spread API calls.
	time.Sleep(30 * time.Second)
	s.syncAccount(ctx, a, creds, ex)
	s.fullReconcile(ctx, a, creds, ex)

	tailTicker := time.NewTicker(90 * time.Second)
	defer tailTicker.Stop()
	fullTicker := time.NewTicker(fullReconcileInterval)
	defer fullTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tailTicker.C:
			s.syncAccount(ctx, a, creds, ex)
		case <-fullTicker.C:
			s.fullReconcile(ctx, a, creds, ex)
		}
	}
}

func (s *ClosedPnlSyncer) syncAccount(ctx context.Context, a closedPnlAccount, creds trader.Credentials, ex trader.Exchange) {
	s.mu.Lock()
	since, ok := s.lastSync[a.id]
	if !ok {
		// First run: look back 2 minutes to catch any recent closes.
		since = time.Now().Add(-2 * time.Minute)
	}
	s.mu.Unlock()

	newLast := s.fetchAndProcess(ctx, a, creds, ex, since)

	s.mu.Lock()
	s.lastSync[a.id] = newLast
	s.mu.Unlock()

	s.reconcileMissingTradeHistory(ctx, a, creds, ex)
}

// fullReconcileInterval/fullReconcileLookback are the periodic safety net's cadence and
// depth — independent of syncAccount's own lastSync watermark. syncAccount's watermark
// advances on ANY symbol's close (whichever is most recent across the whole account), so
// a single order that's missed on its own tick for a transient reason (a request error,
// pagination hiccup, exchange-side delay) can fall permanently outside its rolling window
// once a later close on some OTHER symbol pushes the watermark past it — syncAccount alone
// never re-checks older ground. fullReconcile re-fetches a much wider window on a slow
// cadence and re-runs the exact same idempotent processClosedPnl attribution, so anything
// syncAccount missed gets a second (third, ...) chance regardless of cause.
const fullReconcileInterval = 6 * time.Hour
const fullReconcileLookback = 14 * 24 * time.Hour

func (s *ClosedPnlSyncer) fullReconcile(ctx context.Context, a closedPnlAccount, creds trader.Credentials, ex trader.Exchange) {
	s.fetchAndProcess(ctx, a, creds, ex, time.Now().Add(-fullReconcileLookback))
}

// fetchAndProcess fetches closed-pnl entries since `since` (both linear and inverse) and
// runs each one through processClosedPnl, which is itself idempotent (step 1 checks
// bybit_close_order_id against trade_history) — so calling this with a wide, overlapping
// `since` is always safe, just wasted API calls for anything already recorded. Returns the
// newest close time seen, for callers that track a rolling watermark (syncAccount); callers
// that don't (fullReconcile) simply discard it.
func (s *ClosedPnlSyncer) fetchAndProcess(ctx context.Context, a closedPnlAccount, creds trader.Credentials, ex trader.Exchange, since time.Time) time.Time {
	// Strategy recorder writes within ~20s of cycle close.
	// We only consider items that are at least 30s old to let PATH 1 write first.
	cutoff := time.Now().Add(-30 * time.Second)
	newest := since

	for _, category := range []string{"linear", "inverse"} {
		pnls, err := ex.FetchRecentClosedPnl(ctx, category, since)
		if err != nil {
			log.Printf("closed_pnl_syncer account=%s category=%s: %v", a.id, category, err)
			continue
		}
		for _, p := range pnls {
			ms, _ := strconv.ParseInt(p.CreatedTime, 10, 64)
			closeTime := time.UnixMilli(ms)
			if closeTime.After(cutoff) {
				continue // too recent — wait for strategy recorder
			}
			if closeTime.After(newest) {
				newest = closeTime
			}
			s.processClosedPnl(ctx, a, creds, p, closeTime)
		}
	}
	return newest
}

// tradeHistoryGap is one strategy_cycles row that ended via TP/SL long enough ago that
// closeCycle's own RecordStrategyTrade goroutine should have finished, but has no
// matching trade_history row.
type tradeHistoryGap struct {
	cycleID    string
	strategyID string
	cycleNum   int
	startedAt  time.Time
	endedAt    time.Time
	result     string
	symbol     string
	category   string
	direction  string
	botID      *string
	ownerID    string
}

// reconcileMissingTradeHistory is the permanent safety net for the 2026-09-02 incident:
// a long-lived process's REST connectivity to Bybit died silently (VPN/proxy drop) while
// its WS execution stream kept flowing — strategy_cycles kept ending correctly with
// result='tp', but closeCycle's async RecordStrategyTrade goroutine could never reach
// Bybit's closed-pnl endpoint (or the DB write itself timed out) to write the
// trade_history row, and nothing surfaced that anywhere short of a manual DB query.
//
// This runs every syncAccount tick (~90s) and finds any such gap older than
// gapStaleAfter, best-effort backfills it from Bybit's own closed-pnl history (symbol +
// side + closest time — the same fallback matching RecordStrategyTrade itself falls
// back to when it can't match by exact order ID), and logs loudly either way. A cycle
// whose strategy has since been deleted (e.g. a bot that creates/retires one strategy
// per symbol) can only be backfilled with bot-level attribution — strategy_id/cycle_num
// context genuinely no longer exists to attach to.
const gapStaleAfter = 5 * time.Minute
const gapLookback = 48 * time.Hour
const gapMatchWindow = 10 * time.Minute

func (s *ClosedPnlSyncer) reconcileMissingTradeHistory(ctx context.Context, a closedPnlAccount, creds trader.Credentials, ex trader.Exchange) {
	rows, err := s.pool.Query(ctx, `
		SELECT sc.id, sc.strategy_id, sc.cycle_num, sc.started_at, sc.ended_at, sc.result,
		       s.symbol, s.category, s.direction, s.bot_id, s.owner_id
		FROM strategy_cycles sc
		JOIN strategies s ON s.id = sc.strategy_id
		LEFT JOIN trade_history th ON th.strategy_id = sc.strategy_id AND th.cycle_num = sc.cycle_num
		WHERE s.account_id = $1
		  AND sc.result IN ('tp', 'sl')
		  AND sc.ended_at < $2
		  AND sc.ended_at > $3
		  AND th.id IS NULL`,
		a.id, time.Now().Add(-gapStaleAfter), time.Now().Add(-gapLookback),
	)
	if err != nil {
		log.Printf("closed_pnl_syncer: reconcile gaps query account=%s: %v", a.id, err)
		return
	}
	var gaps []tradeHistoryGap
	for rows.Next() {
		var g tradeHistoryGap
		if err := rows.Scan(&g.cycleID, &g.strategyID, &g.cycleNum, &g.startedAt, &g.endedAt, &g.result,
			&g.symbol, &g.category, &g.direction, &g.botID, &g.ownerID); err != nil {
			continue
		}
		gaps = append(gaps, g)
	}
	rows.Close()
	if len(gaps) == 0 {
		return
	}

	// Group by symbol so each symbol's Bybit closed-pnl history is fetched once, and
	// each Bybit entry is matched to at most one gap.
	bySymbol := make(map[string][]tradeHistoryGap)
	for _, g := range gaps {
		bySymbol[g.symbol] = append(bySymbol[g.symbol], g)
	}

	for symbol, symGaps := range bySymbol {
		log.Printf("closed_pnl_syncer: WARNING %d trade_history gap(s) on %s (account=%s) — no row written within %s of cycle close, backfilling from exchange",
			len(symGaps), symbol, a.id, gapStaleAfter)

		pnls, err := ex.FetchClosedPnlForSymbol(ctx, symGaps[0].category, symbol, 50)
		if err != nil {
			log.Printf("closed_pnl_syncer: gap backfill %s: fetch closed pnl: %v — will retry next tick", symbol, err)
			continue
		}

		var usedSlice []string
		_ = s.pool.QueryRow(ctx,
			`SELECT COALESCE(array_agg(bybit_close_order_id), '{}') FROM trade_history
			 WHERE account_id = $1 AND symbol = $2 AND bybit_close_order_id IS NOT NULL`,
			a.id, symbol,
		).Scan(&usedSlice)
		used := make(map[string]bool, len(usedSlice))
		for _, id := range usedSlice {
			used[id] = true
		}

		for _, g := range symGaps {
			wantSide := "Sell" // closing a long = Sell on Bybit
			if g.direction == "short" {
				wantSide = "Buy"
			}
			var best *trader.ClosedPnl
			var bestDelta time.Duration
			for i := range pnls {
				p := &pnls[i]
				if p.Side != wantSide || used[p.OrderId] {
					continue
				}
				ms, _ := strconv.ParseInt(p.CreatedTime, 10, 64)
				closeTime := time.UnixMilli(ms)
				delta := closeTime.Sub(g.endedAt)
				if delta < 0 {
					delta = -delta
				}
				if delta > gapMatchWindow {
					continue
				}
				if best == nil || delta < bestDelta {
					best = p
					bestDelta = delta
				}
			}
			if best == nil {
				log.Printf("closed_pnl_syncer: gap backfill %s cy%d strategy=%s: NO matching exchange close found within %s of %s — cannot recover, needs manual review",
					symbol, g.cycleNum, g.strategyID, gapMatchWindow, g.endedAt.Format(time.RFC3339))
				continue
			}
			used[best.OrderId] = true
			if err := s.writeGapTradeHistory(ctx, a, g, best); err != nil {
				log.Printf("closed_pnl_syncer: gap backfill %s cy%d strategy=%s: write: %v",
					symbol, g.cycleNum, g.strategyID, err)
				continue
			}
			log.Printf("closed_pnl_syncer: gap backfill %s cy%d strategy=%s: recorded from exchange order=%s pnl=%s",
				symbol, g.cycleNum, g.strategyID, best.OrderId, best.ClosedPnl)
		}
	}
}

// writeGapTradeHistory computes and inserts one backfilled trade_history row for a gap,
// mirroring RecordStrategyTrade's own computation (VWAP from strategy_levels, fees/
// funding scoped to the cycle's actual lifetime — using the cycle's real endedAt as the
// upper bound, not time.Now(), unlike a live recording this runs long after the fact).
func (s *ClosedPnlSyncer) writeGapTradeHistory(ctx context.Context, a closedPnlAccount, g tradeHistoryGap, p *trader.ClosedPnl) error {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(filled_price, target_price), size_usdt
		FROM strategy_levels
		WHERE cycle_id = $1 AND status = 'filled'
		ORDER BY level_idx`, g.cycleID)
	var totalValue, totalQty, totalUSDT float64
	if err == nil {
		for rows.Next() {
			var price, sizeUSDT float64
			if err := rows.Scan(&price, &sizeUSDT); err != nil || price == 0 {
				continue
			}
			qty := sizeUSDT / price
			totalValue += price * qty
			totalQty += qty
			totalUSDT += sizeUSDT
		}
		rows.Close()
	}
	avgEntry := 0.0
	if totalQty > 0 {
		avgEntry = totalValue / totalQty
	}

	grossPnl, _ := strconv.ParseFloat(p.ClosedPnl, 64)
	exitPrice, _ := strconv.ParseFloat(p.AvgExitPrice, 64)
	closedQty := totalQty
	if q, err := strconv.ParseFloat(p.Qty, 64); err == nil && q > 0 {
		closedQty = q
	}

	stratID8 := g.strategyID
	if len(stratID8) > 8 {
		stratID8 = stratID8[:8]
	}
	fees, funding := strategy.FeesAndFundingInRange(ctx, s.pool, a.id, g.symbol, stratID8, g.startedAt, g.endedAt)
	netPnl := grossPnl - fees
	pnlPct := 0.0
	if totalUSDT > 0 {
		pnlPct = grossPnl / totalUSDT * 100
	}

	oid := p.OrderId
	_, err = s.pool.Exec(ctx, `
		INSERT INTO trade_history (
			strategy_id, bot_id, account_id, owner_id,
			symbol, category, direction, cycle_num, result, source,
			avg_entry, exit_price, qty, volume_usdt,
			pnl, pnl_pct, opened_at, closed_at,
			fees, funding, net_pnl, bybit_close_order_id
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, 'strategy',
			$10, $11, $12, $13,
			$14, $15, $16, $17,
			$18, $19, $20, $21
		)
		ON CONFLICT (strategy_id, cycle_num) WHERE strategy_id IS NOT NULL
		DO NOTHING`,
		g.strategyID, g.botID, a.id, g.ownerID,
		g.symbol, g.category, g.direction, g.cycleNum, g.result,
		avgEntry, exitPrice, closedQty, totalUSDT,
		grossPnl, pnlPct, g.startedAt, g.endedAt,
		fees, funding, netPnl, oid,
	)
	return err
}

func (s *ClosedPnlSyncer) processClosedPnl(ctx context.Context, a closedPnlAccount, creds trader.Credentials, p trader.ClosedPnl, closeTime time.Time) {
	if p.OrderId == "" {
		return
	}

	// 1. Already recorded? (strategy recorder sets bybit_close_order_id)
	var exists bool
	_ = s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM trade_history WHERE account_id=$1 AND bybit_close_order_id=$2)`,
		a.id, p.OrderId,
	).Scan(&exists)
	if exists {
		return
	}

	// 1b. Direct attribution via our own orderLinkId — Bybit echoes it back unchanged.
	// Authoritative: tells us exactly which strategy (and bot) owns this close,
	// independent of whether the cycle looks "ended" or "zombie" from the outside —
	// which is exactly what breaks for matrix bots (a global TP re-arms the SAME cycle
	// instead of ending it, so the time-window heuristics below misfire on it).
	if parsed, ok := strategy.ParseStrategyLinkID(p.OrderLinkId); ok {
		if s.processLinkIDAttributed(ctx, a, p, parsed) {
			return
		}
	}

	// 1c. Fallback attribution via strategy_levels.sl_order_id — catches a per-level matrix
	// SL close whose OrderLinkId didn't survive the round trip. Bybit does not reliably echo
	// a conditional/stop order's OrderLinkId back once it triggers and converts to a market
	// fill, so 1b's parse can miss orders that were, in fact, placed by us with a proper
	// SIS_STR-...-msl-... linkID. Without this check such a close fell through all the way
	// to the zombie-cycle heuristic (step 3) and force-closed the ENTIRE cycle — even with
	// other levels still open on the exchange. Found live 2026-09-19, CROSSUSDT: two hedge
	// legs (on two separate accounts) each had a level SL-close ghost-close their whole
	// cycle this way, orphaning a live ~524-qty position with no TP/SL. handleMatrixSLFill
	// already recorded this level's PnL directly into strategy_levels — trade_history and
	// strategy_cycles must stay untouched.
	var levelStratID string
	if err := s.pool.QueryRow(ctx,
		`SELECT st.id FROM strategy_levels sl JOIN strategies st ON st.id = sl.strategy_id
		 WHERE st.account_id = $1 AND sl.sl_order_id = $2`,
		a.id, p.OrderId,
	).Scan(&levelStratID); err == nil {
		log.Printf("closed_pnl_syncer: %s per-level matrix SL (order=%s) matched via strategy_levels.sl_order_id — already tracked, skip", p.Symbol, p.OrderId)
		return
	}

	// 2. Does a strategy cycle own this close?
	// A strategy cycle that ended near this close time for this symbol+direction.
	dir := "long"
	if p.Side == "Sell" { // Bybit: side of closing order — Sell closes long, Buy closes short
		dir = "long"
	} else {
		dir = "short"
	}
	var stratCycleID string
	_ = s.pool.QueryRow(ctx, `
		SELECT sc.id
		FROM strategy_cycles sc
		JOIN strategies st ON st.id = sc.strategy_id
		WHERE st.account_id = $1
		  AND st.symbol     = $2
		  AND st.direction  = $3
		  AND sc.ended_at BETWEEN $4 AND $5
		LIMIT 1`,
		a.id, p.Symbol, dir,
		closeTime.Add(-2*time.Minute), closeTime.Add(2*time.Minute),
	).Scan(&stratCycleID)

	if stratCycleID != "" {
		// Strategy recorder will handle (or has already handled) this trade.
		// Verify it was written; if not yet, we'll retry next tick.
		var written bool
		_ = s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM trade_history WHERE account_id=$1 AND bybit_close_order_id=$2)`,
			a.id, p.OrderId,
		).Scan(&written)
		if !written {
			log.Printf("closed_pnl_syncer: %s %s strategy cycle %s found, waiting for recorder", p.Symbol, dir, stratCycleID)
		}
		return
	}

	// 3. Zombie cycle — open cycle whose closeCycle was missed (gateway was down).
	// Ghost-close it and record as a strategy trade instead of manual.
	//
	// sc.started_at < closeTime is a correctness invariant, not an optimization: a cycle
	// cannot have been closed by an order that predates its own start. Without this guard,
	// a stale/unrelated closed-pnl entry (an old order this syncer never managed to
	// attribute — e.g. fullReconcile reaching back further than usual) can match whichever
	// open cycle currently happens to share symbol+direction, even one that started well
	// after that old order closed — silently ghost-closing a live, currently-trading
	// position. Found live (2026-09-16): a 2026-09-11 order matched and ghost-closed a
	// matrix cycle that had opened on 2026-09-16, ending it with ended_at BEFORE
	// started_at and halting all further order placement on a real open position.
	var zc struct {
		cycleID   string
		cycleNum  int
		startedAt time.Time
		tpOrderID string
		slOrderID string
		stratID   string
		botID     *string
		category  string
		hedgeMode bool
	}
	zombieErr := s.pool.QueryRow(ctx, `
		SELECT sc.id, sc.cycle_num, sc.started_at,
		       COALESCE(sc.tp_order_id,''), COALESCE(sc.sl_order_id,''),
		       st.id, st.bot_id, st.category, COALESCE(st.hedge_mode, true)
		FROM strategy_cycles sc
		JOIN strategies st ON st.id = sc.strategy_id
		WHERE st.account_id = $1
		  AND st.symbol     = $2
		  AND st.direction  = $3
		  AND sc.ended_at IS NULL
		  AND sc.started_at < $4
		ORDER BY sc.started_at DESC
		LIMIT 1`,
		a.id, p.Symbol, dir, closeTime,
	).Scan(&zc.cycleID, &zc.cycleNum, &zc.startedAt,
		&zc.tpOrderID, &zc.slOrderID,
		&zc.stratID, &zc.botID, &zc.category, &zc.hedgeMode)
	if zombieErr == nil {
		tag, _ := s.pool.Exec(ctx,
			`UPDATE strategy_cycles SET ended_at=$1, result='ghost_close'
			 WHERE id=$2 AND ended_at IS NULL`,
			closeTime, zc.cycleID)
		if tag.RowsAffected() > 0 {
			log.Printf("closed_pnl_syncer: %s %s zombie цикл %s → ghost_close, запись сделки...", p.Symbol, dir, zc.cycleID[:8])
			in := strategy.TradeRecordInput{
				Strategy: strategy.Strategy{
					ID:        zc.stratID,
					AccountID: a.id,
					OwnerID:   a.ownerID,
					Symbol:    p.Symbol,
					Category:  zc.category,
					Direction: strategy.Direction(dir),
					BotID:     zc.botID,
					HedgeMode: zc.hedgeMode,
				},
				CycleID:   zc.cycleID,
				CycleNum:  zc.cycleNum,
				StartedAt: zc.startedAt,
				Result:    "ghost_close",
				TPOrderID: zc.tpOrderID,
				SLOrderID: zc.slOrderID,
			}
			go strategy.RecordStrategyTrade(s.pool, creds, in)
		}
		return
	}

	// 4. Manual trade — write to trade_history.
	grossPnl, _ := strconv.ParseFloat(p.ClosedPnl, 64)
	avgEntry, _ := strconv.ParseFloat(p.AvgEntryPrice, 64)
	avgExit, _ := strconv.ParseFloat(p.AvgExitPrice, 64)
	qty, _ := strconv.ParseFloat(p.Qty, 64)

	// Estimate open time from cumEntryValue / AvgEntryPrice for fee/funding query.
	// We don't know exact open time for manual trades so use a 24h look-back.
	openEstimate := closeTime.Add(-24 * time.Hour)

	var fees float64
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		FROM trader_executions
		WHERE account_id=$1 AND symbol=$2 AND exec_type='Trade'
		  AND exec_time BETWEEN $3 AND $4`,
		a.id, p.Symbol, openEstimate, closeTime,
	).Scan(&fees)

	var funding float64
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(ABS(exec_fee)), 0)
		FROM trader_executions
		WHERE account_id=$1 AND symbol=$2 AND exec_type='Funding'
		  AND exec_time BETWEEN $3 AND $4`,
		a.id, p.Symbol, openEstimate, closeTime,
	).Scan(&funding)

	netPnl := grossPnl - fees - funding
	volumeUSDT := qty * avgEntry

	oid := p.OrderId
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trade_history (
			account_id, owner_id, symbol, category, direction,
			cycle_num, result, source,
			avg_entry, exit_price, qty, volume_usdt,
			pnl, pnl_pct, opened_at, closed_at,
			fees, funding, net_pnl, bybit_close_order_id
		) VALUES (
			$1, $2, $3, $4, $5,
			0, 'manual', 'manual',
			$6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16, $17
		)
		ON CONFLICT (account_id, bybit_close_order_id) WHERE bybit_close_order_id IS NOT NULL
		DO NOTHING`,
		a.id, a.ownerID, p.Symbol, p.Category, dir,
		avgEntry, avgExit, qty, volumeUSDT,
		grossPnl, safeDiv(grossPnl, volumeUSDT)*100, openEstimate, closeTime,
		fees, funding, netPnl, oid,
	)
	if err != nil {
		log.Printf("closed_pnl_syncer: insert manual trade %s %s: %v", p.Symbol, p.OrderId, err)
		return
	}
	log.Printf("closed_pnl_syncer: ручная сделка %s %s %s gross=%.4f net=%.4f",
		p.Symbol, dir, p.OrderId, grossPnl, netPnl)
}

// processLinkIDAttributed handles a ClosedPnl entry whose orderLinkId directly identifies
// the owning strategy via ParseStrategyLinkID. Returns true when the caller must NOT fall
// through to the legacy time-window heuristics (steps 2-4) — either because this function
// fully handled the entry, or because it positively identified the owning strategy and the
// primary in-process recorder is expected to catch up on its own. Returns false only when
// the parsed strategy id prefix doesn't resolve to a real row (e.g. deleted strategy) —
// the legacy path is a safety net for that edge case.
func (s *ClosedPnlSyncer) processLinkIDAttributed(ctx context.Context, a closedPnlAccount, p trader.ClosedPnl, parsed strategy.ParsedLinkID) bool {
	var stratID, symbol, direction, category string
	var botID *string
	var cycleID *string
	var cycleNum *int
	err := s.pool.QueryRow(ctx, `
		SELECT st.id, st.symbol, st.direction, st.category, st.bot_id, lc.cycle_id, lc.cycle_num
		FROM strategies st
		LEFT JOIN LATERAL (
			SELECT c.id AS cycle_id, c.cycle_num
			FROM strategy_cycles c WHERE c.strategy_id = st.id
			ORDER BY c.cycle_num DESC LIMIT 1
		) lc ON true
		WHERE st.account_id = $1 AND st.id::text LIKE $2`,
		a.id, parsed.StrategyID8+"%",
	).Scan(&stratID, &symbol, &direction, &category, &botID, &cycleID, &cycleNum)
	if err != nil {
		return false // no matching strategy on this account — let the legacy path try
	}

	switch parsed.Kind {
	case strategy.LinkIDMatrixTP:
		// Global matrix TP re-arm: the cycle does NOT end. Record directly into
		// matrix_tp_profits — same table/idempotency the in-process engine uses via
		// RecordMatrixTPProfit. Never touch ended_at: this cycle is healthy and still
		// trading, not a zombie — that is exactly the bug this fixes.
		grossPnl, perr := strconv.ParseFloat(p.ClosedPnl, 64)
		if perr != nil {
			// Don't insert a wrong 0.0 profit — InsertMatrixTPProfit is idempotent
			// (ON CONFLICT DO NOTHING keyed on order id), so a bad insert now would
			// permanently block a correct one later. Fall through to the legacy path.
			log.Printf("closed_pnl_syncer: %s parse closedPnl=%q (order=%s): %v", p.Symbol, p.ClosedPnl, p.OrderId, perr)
			return false
		}
		cn := 0
		if cycleNum != nil {
			cn = *cycleNum
		}
		strategy.InsertMatrixTPProfit(ctx, s.pool, strategy.MatrixTPInsertInput{
			StrategyID: stratID, BotID: botID, AccountID: a.id,
			CycleNum: cn, Symbol: symbol, GrossPnl: grossPnl, OrderID: p.OrderId,
		})
		return true

	case strategy.LinkIDMatrixLevelSL:
		// Already recorded by the in-process engine directly into
		// strategy_levels.realized_pnl (handleMatrixSLFill) — writing this into
		// trade_history too would create a duplicate/orphan row. No-op.
		log.Printf("closed_pnl_syncer: %s per-level matrix SL (order=%s) already tracked via strategy_levels — skip", p.Symbol, p.OrderId)
		return true

	case strategy.LinkIDGridTP, strategy.LinkIDGridSL:
		// A genuine cycle-ending close, now with a PRECISE strategy match instead of
		// the fuzzy time-window guess below. The primary in-process recorder
		// (closeCycle → RecordStrategyTrade) is expected to write this; if it hasn't
		// yet (timing race), the next poll's step-1 existence check will pick it up
		// once written. We do NOT force anything here, and we do NOT fall through to
		// zombie-detection for a strategy we've positively identified.
		if cycleID != nil {
			log.Printf("closed_pnl_syncer: %s %s linkId-attributed to strategy %s cycle %s, waiting for recorder",
				p.Symbol, direction, stratID[:8], (*cycleID)[:8])
		}
		return true

	case strategy.LinkIDSelfClose:
		// A generic self-issued market close (closePositionAtMarket, closeGhostPosition,
		// the matrix TP re-arm's leftover-tail cleanup, or a post-cycle remnant-dust
		// close). Whether this ends the cycle or not is decided entirely by the
		// in-process runner that placed it (closeCycle is called separately, or — for the
		// matrix tail-close — deliberately not called at all). Never force anything here;
		// same wait-for-recorder treatment as LinkIDGridTP/LinkIDGridSL. Before this kind
		// existed these orders had no orderLinkId at all, so they always fell through to
		// the zombie heuristic below and could force-close a perfectly healthy cycle.
		if cycleID != nil {
			log.Printf("closed_pnl_syncer: %s %s linkId-attributed self-close for strategy %s cycle %s, waiting for recorder",
				p.Symbol, direction, stratID[:8], (*cycleID)[:8])
		}
		return true

	default: // strategy.LinkIDUnrecognized — matched our prefix, not a close-type suffix.
		return false
	}
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
