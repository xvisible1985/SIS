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
	id        string
	ownerID   string
	apiKeyEnc string
	secretEnc string
}

func (s *ClosedPnlSyncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, api_key_enc, secret_enc FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("closed_pnl_syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a closedPnlAccount
		if err := rows.Scan(&a.id, &a.ownerID, &a.apiKeyEnc, &a.secretEnc); err != nil {
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
	creds := trader.Credentials{APIKey: apiKey, SecretKey: secret}

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
			s.processClosedPnl(ctx, a, p, closeTime)
		}
	}

	s.mu.Lock()
	s.lastSync[a.id] = newLast
	s.mu.Unlock()
}

func (s *ClosedPnlSyncer) processClosedPnl(ctx context.Context, a closedPnlAccount, p trader.ClosedPnl, closeTime time.Time) {
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

	// 3. Manual trade — write to trade_history.
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

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
