package trader

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/crypto"
)

type accountRow struct {
	id        string
	ownerID   string
	exchange  string
	apiKeyEnc string
	secretEnc string
}

// Syncer periodically pulls execution history from Bybit and upserts into trader_executions.
type Syncer struct {
	pool     *pgxpool.Pool
	encKey   string
	syncDays int
	mu       sync.Mutex
	running  map[string]context.CancelFunc
}

// NewSyncer creates a Syncer. encKey is the hex encryption key. syncDays is backfill depth.
func NewSyncer(pool *pgxpool.Pool, encKey string, syncDays int) *Syncer {
	return &Syncer{
		pool:     pool,
		encKey:   encKey,
		syncDays: syncDays,
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
		`SELECT id, owner_id, exchange, api_key_enc, secret_enc
		 FROM exchange_accounts WHERE is_active = TRUE`)
	if err != nil {
		log.Printf("syncer: load accounts: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.id, &a.ownerID, &a.exchange, &a.apiKeyEnc, &a.secretEnc); err != nil {
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
	creds := Credentials{APIKey: apiKey, SecretKey: secret}

	s.syncExecutions(ctx, a, creds)

	execTicker := time.NewTicker(60 * time.Second)
	histTicker := time.NewTicker(5 * time.Minute)
	defer execTicker.Stop()
	defer histTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-execTicker.C:
			s.syncExecutions(ctx, a, creds)
		case <-histTicker.C:
			s.syncOrderHistory(ctx, a, creds)
		}
	}
}

func (s *Syncer) syncExecutions(ctx context.Context, a accountRow, creds Credentials) {
	since := time.Now().AddDate(0, 0, -s.syncDays)
	for _, category := range []string{"linear", "inverse", "spot"} {
		cursor := ""
		for {
			execs, next, err := FetchExecutions(ctx, creds, category, cursor)
			if err != nil {
				log.Printf("syncer: fetch executions account=%s category=%s: %v", a.id, category, err)
				break
			}
			for _, e := range execs {
				var execTimeMs int64
				fmt.Sscanf(e.ExecTimeMs, "%d", &execTimeMs)
				execTime := time.UnixMilli(execTimeMs)
				if execTime.Before(since) {
					next = ""
					break
				}
				isMaker := strconv.FormatBool(e.IsMaker)
				_ = isMaker
				_, err := s.pool.Exec(ctx, `
					INSERT INTO trader_executions
					  (owner_id, account_id, exec_id, order_id, order_link_id,
					   exchange, symbol, category, side, exec_type,
					   qty, price, exec_value, exec_fee, fee_rate, is_maker, exec_time)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
					ON CONFLICT (account_id, exec_id) DO NOTHING`,
					a.ownerID, a.id, e.ExecId, nullStr(e.OrderId), nullStr(e.OrderLinkId),
					a.exchange, e.Symbol, category, nullStr(e.Side), e.ExecType,
					nullNum(e.ExecQty), nullNum(e.ExecPrice), nullNum(e.ExecValue),
					nullNum(e.ExecFee), nullNum(e.FeeRate), e.IsMaker, execTime,
				)
				if err != nil {
					log.Printf("syncer: upsert exec %s: %v", e.ExecId, err)
				}
			}
			if next == "" {
				break
			}
			cursor = next
		}
	}
}

func (s *Syncer) syncOrderHistory(ctx context.Context, a accountRow, creds Credentials) {
	for _, category := range []string{"linear", "inverse", "spot"} {
		cursor := ""
		for {
			orders, next, err := FetchOrderHistory(ctx, creds, category, cursor)
			if err != nil {
				break
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
			if next == "" {
				break
			}
			cursor = next
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
