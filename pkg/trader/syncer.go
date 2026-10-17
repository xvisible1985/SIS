package trader

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
)

type accountRow struct {
	id             string
	ownerID        string
	exchange       string
	apiKeyEnc      string
	secretEnc      string
	whitelistedIPs []string
}

// ExchangeFactory builds the Exchange implementation for one account — injected rather
// than called directly, since pkg/trader (this package) cannot import
// pkg/trader/binance (which itself imports pkg/trader — a direct cycle). The real
// factory passed in by services/api-gateway/main.go is strategy.ResolveExchange; see its
// doc comment for the exchangeName dispatch rule this must match.
type ExchangeFactory func(exchangeName string, creds Credentials) Exchange

// Syncer periodically pulls execution/order history from each account's own exchange
// (via ExchangeFactory, not Bybit-only free functions — fixed 2026-10-17: this used to
// call FetchExecutions/FetchOrderHistory/QueryAPI directly regardless of the account's
// actual exchange, exactly the bug class Plan #4d fixed elsewhere in pkg/strategy and
// services/api-gateway, just not caught by that earlier pass since this is a separate
// package with its own account-sync loop) and upserts into trader_executions/trader_orders.
type Syncer struct {
	pool     *pgxpool.Pool
	encKey   string
	syncDays int
	factory  ExchangeFactory
	mu       sync.Mutex
	running  map[string]context.CancelFunc
}

// NewSyncer creates a Syncer. encKey is the hex encryption key. syncDays is backfill
// depth. factory resolves each account's Exchange — pass strategy.ResolveExchange.
func NewSyncer(pool *pgxpool.Pool, encKey string, syncDays int, factory ExchangeFactory) *Syncer {
	return &Syncer{
		pool:     pool,
		encKey:   encKey,
		syncDays: syncDays,
		factory:  factory,
		running:  make(map[string]context.CancelFunc),
	}
}

// Start loads all active accounts and begins a sync goroutine per account.
// It also rescans for new accounts every 5 minutes.
func (s *Syncer) Start(ctx context.Context) {
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

func (s *Syncer) loadAndLaunch(ctx context.Context) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, exchange, api_key_enc, secret_enc, whitelisted_ips
		 FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.id, &a.ownerID, &a.exchange, &a.apiKeyEnc, &a.secretEnc, &a.whitelistedIPs); err != nil {
			continue
		}
		s.mu.Lock()
		_, ok := s.running[a.id]
		s.mu.Unlock()
		if !ok {
			s.launch(ctx, a)
		}
	}
}

func (s *Syncer) launch(ctx context.Context, a accountRow) {
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

func (s *Syncer) runAccount(ctx context.Context, a accountRow) {
	apiKey, err := crypto.Decrypt(a.apiKeyEnc, s.encKey)
	if err != nil {
		log.Printf("syncer: decrypt api_key account=%s: %v", a.id, err)
		return
	}
	secret, err := crypto.Decrypt(a.secretEnc, s.encKey)
	if err != nil {
		log.Printf("syncer: decrypt secret account=%s: %v", a.id, err)
		return
	}
	creds := Credentials{APIKey: apiKey, SecretKey: secret, AccountID: a.id, WhitelistedIPs: a.whitelistedIPs}
	ex := s.factory(a.exchange, creds)

	s.syncExecutions(ctx, a, ex)

	execTicker := time.NewTicker(60 * time.Second)
	histTicker := time.NewTicker(5 * time.Minute)
	whitelistTicker := time.NewTicker(5 * time.Minute)
	defer execTicker.Stop()
	defer histTicker.Stop()
	defer whitelistTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-execTicker.C:
			s.syncExecutions(ctx, a, ex)
		case <-histTicker.C:
			s.syncOrderHistory(ctx, a, ex)
		case <-whitelistTicker.C:
			// refreshWhitelistedIPs may narrow creds.WhitelistedIPs, which changes which
			// proxy ex's underlying HTTP calls are allowed to use (see
			// proxy.HTTPClientFor) — ex must be rebuilt afterward to pick that up,
			// exactly like the pre-2026-10-17 code mutated creds in place so the NEXT
			// tick's direct free-function calls would see it.
			if s.refreshWhitelistedIPs(ctx, &a, &creds) {
				ex = s.factory(a.exchange, creds)
			}
		}
	}
}

// refreshWhitelistedIPs re-fetches the account's exchange-API-key IP whitelist (if the
// exchange's API exposes it at all — see trader.ErrWhitelistNotSupported) and persists
// it to exchange_accounts.whitelisted_ips, also updating creds in place. Returns whether
// creds.WhitelistedIPs actually changed, so the caller knows whether it needs to rebuild
// its Exchange instance (ex was built with a COPY of creds, so mutating this creds value
// alone does not change what ex's own HTTP calls use).
func (s *Syncer) refreshWhitelistedIPs(ctx context.Context, a *accountRow, creds *Credentials) bool {
	// Built fresh (not reused from runAccount's `ex`) so this call is unrestricted by
	// creds.WhitelistedIPs — which, from the 2nd refresh onward, holds the PREVIOUS
	// result; reusing it here would self-lock the account forever the moment the
	// whitelist ever narrows to IPs our proxy pool can't match.
	unrestricted := s.factory(a.exchange, Credentials{APIKey: creds.APIKey, SecretKey: creds.SecretKey})
	ips, err := unrestricted.FetchWhitelistedIPs(ctx)
	if err != nil {
		if !errors.Is(err, ErrWhitelistNotSupported) {
			log.Printf("syncer: refresh whitelist account=%s: %v", a.id, err)
		}
		return false
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE exchange_accounts SET whitelisted_ips=$1 WHERE id=$2`,
		ips, a.id,
	); err != nil {
		log.Printf("syncer: persist whitelist account=%s: %v", a.id, err)
		return false
	}
	a.whitelistedIPs = ips
	creds.WhitelistedIPs = ips
	return true
}

// UpsertExecution inserts one execution row into trader_executions, matching by
// (account_id, exec_id) — a duplicate (the same exchange execution observed twice, e.g.
// via both this REST syncer and the real-time WS "execution" topic — see
// AccountRunner.OnExecutionEvent in pkg/strategy) is silently ignored.
func UpsertExecution(ctx context.Context, pool *pgxpool.Pool, ownerID, accountID, exchange, category string, e Execution) error {
	var execTimeMs int64
	fmt.Sscanf(e.ExecTimeMs, "%d", &execTimeMs)
	execTime := time.UnixMilli(execTimeMs)
	_, err := pool.Exec(ctx, `
		INSERT INTO trader_executions
		  (owner_id, account_id, exec_id, order_id, order_link_id,
		   exchange, symbol, category, side, exec_type,
		   qty, price, exec_value, exec_fee, fee_rate, is_maker, exec_time,
		   position_idx)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (account_id, exec_id) DO NOTHING`,
		ownerID, accountID, e.ExecId, nullStr(e.OrderId), nullStr(e.OrderLinkId),
		exchange, e.Symbol, category, nullStr(e.Side), e.ExecType,
		nullNum(e.ExecQty), nullNum(e.ExecPrice), nullNum(e.ExecValue),
		nullNum(e.ExecFee), nullNum(e.FeeRate), e.IsMaker, execTime,
		e.PositionIdx,
	)
	return err
}

// bybitCategories is every category Bybit splits account-wide history into. Binance
// (USDⓈ-M futures only, no inverse/spot split) only ever needs one pass — its
// Exchange.FetchAccountExecutions/FetchAccountOrderHistory implementations ignore the
// category argument entirely, but syncExecutions/syncOrderHistory still only call once
// for it instead of uselessly repeating the exact same account-wide fetch 3x.
func bybitCategories(exchangeName string) []string {
	if exchangeName == "binance" {
		return []string{"linear"}
	}
	return []string{"linear", "inverse", "spot"}
}

func (s *Syncer) syncExecutions(ctx context.Context, a accountRow, ex Exchange) {
	since := time.Now().AddDate(0, 0, -s.syncDays)
	for _, category := range bybitCategories(a.exchange) {
		execs, err := ex.FetchAccountExecutions(ctx, category, since)
		if err != nil {
			log.Printf("syncer: fetch executions account=%s category=%s: %v", a.id, category, err)
		}
		for _, e := range execs {
			if err := UpsertExecution(ctx, s.pool, a.ownerID, a.id, a.exchange, category, e); err != nil {
				log.Printf("syncer: upsert exec %s: %v", e.ExecId, err)
			}
		}
	}
}

func (s *Syncer) syncOrderHistory(ctx context.Context, a accountRow, ex Exchange) {
	since := time.Now().AddDate(0, 0, -s.syncDays)
	for _, category := range bybitCategories(a.exchange) {
		orders, err := ex.FetchAccountOrderHistory(ctx, category, since)
		if err != nil {
			log.Printf("syncer: fetch order history account=%s category=%s: %v", a.id, category, err)
		}
		for _, o := range orders {
			if o.OrderLinkId == "" || len(o.OrderLinkId) < 3 || o.OrderLinkId[:3] != "sis" {
				continue
			}
			_, err := s.pool.Exec(ctx, `
				UPDATE trader_orders
				SET status=$1, cum_exec_qty=$2, cum_exec_fee=$3, order_id=COALESCE(NULLIF($4,''), order_id), updated_at=NOW()
				WHERE order_link_id=$5`,
				o.OrderStatus, nullNum(o.CumExecQty), nullNum(o.CumExecFee), o.OrderId, o.OrderLinkId,
			)
			if err != nil {
				log.Printf("syncer: update order %s: %v", o.OrderLinkId, err)
			}
		}
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullNum(s string) any {
	if s == "" {
		return nil
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return nil
	}
	return f
}
