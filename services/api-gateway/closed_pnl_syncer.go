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
}

func (s *ClosedPnlSyncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, api_key_enc, secret_enc, whitelisted_ips FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("closed_pnl_syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a closedPnlAccount
		if err := rows.Scan(&a.id, &a.ownerID, &a.apiKeyEnc, &a.secretEnc, &a.whitelistedIPs); err != nil {
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

	// Offset from execution syncer (60s) to spread API calls.
	time.Sleep(30 * time.Second)
	s.syncAccount(ctx, a, creds)

	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncAccount(ctx, a, creds)
		}
	}
}

func (s *ClosedPnlSyncer) syncAccount(ctx context.Context, a closedPnlAccount, creds trader.Credentials) {
	s.mu.Lock()
	since, ok := s.lastSync[a.id]
	if !ok {
		// First run: look back 2 minutes to catch any recent closes.
		since = time.Now().Add(-2 * time.Minute)
	}
	s.mu.Unlock()

	// Strategy recorder writes within ~20s of cycle close.
	// We only consider items that are at least 30s old to let PATH 1 write first.
	cutoff := time.Now().Add(-30 * time.Second)
	newLast := since

	for _, category := range []string{"linear", "inverse"} {
		pnls, err := trader.FetchRecentClosedPnl(ctx, creds, category, since)
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
			if closeTime.After(newLast) {
				newLast = closeTime
			}
			s.processClosedPnl(ctx, a, creds, p, closeTime)
		}
	}

	s.mu.Lock()
	s.lastSync[a.id] = newLast
	s.mu.Unlock()
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
		ORDER BY sc.started_at DESC
		LIMIT 1`,
		a.id, p.Symbol, dir,
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
