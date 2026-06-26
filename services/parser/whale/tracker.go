// services/parser/whale/tracker.go
package whale

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Config holds tracker configuration.
type Config struct {
	EtherscanKey    string
	TronGridKey     string
	ThresholdUSDT   float64
	PollInterval    time.Duration
	DiscoveryWeekly bool
}

// Tracker monitors known whale addresses for large deposits to Bybit.
type Tracker struct {
	db       *pgxpool.Pool
	rdb      *redis.Client
	cfg      Config
	eth      *EthClient
	tron     *TronClient
	analyzer *Analyzer
}

// NewTracker creates a Tracker with all sub-clients initialized.
func NewTracker(db *pgxpool.Pool, rdb *redis.Client, cfg Config) *Tracker {
	eth := newEthClient(cfg.EtherscanKey)
	tron := newTronClient(cfg.TronGridKey)
	return &Tracker{
		db:       db,
		rdb:      rdb,
		cfg:      cfg,
		eth:      eth,
		tron:     tron,
		analyzer: newAnalyzer(eth, tron),
	}
}

// Start begins the polling loop and optional weekly discovery. Blocks until ctx is cancelled.
func (t *Tracker) Start(ctx context.Context) {
	log.Println("whale tracker: starting")

	if t.cfg.DiscoveryWeekly {
		d := NewDiscovery(t.db, t.eth, t.tron, t.cfg)
		go d.Start(ctx)
	}

	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	t.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.poll(ctx)
		}
	}
}

func (t *Tracker) poll(ctx context.Context) {
	addrs, err := t.loadActiveAddresses(ctx)
	if err != nil {
		log.Printf("whale poll: load addresses: %v", err)
		return
	}

	sinceTS := time.Now().Add(-10 * time.Minute).Unix()
	sinceMS := sinceTS * 1000

	for _, addr := range addrs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t.checkAddress(ctx, addr, sinceTS, sinceMS)
	}
}

type whaleAddress struct {
	ID      string
	Address string
	Chain   string
}

func (t *Tracker) loadActiveAddresses(ctx context.Context) ([]whaleAddress, error) {
	rows, err := t.db.Query(ctx, `
		SELECT id, address, chain FROM whale_addresses WHERE is_active = true
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []whaleAddress
	for rows.Next() {
		var a whaleAddress
		if err := rows.Scan(&a.ID, &a.Address, &a.Chain); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (t *Tracker) checkAddress(ctx context.Context, addr whaleAddress, sinceTS, sinceMS int64) {
	switch addr.Chain {
	case "eth":
		txs, err := t.eth.TokenTxSince(ctx, addr.Address, sinceTS)
		if err != nil {
			log.Printf("whale: eth %s: %v", addr.Address[:8], err)
			return
		}
		for _, tx := range txs {
			if !IsExchangeDeposit(tx.To, "eth") {
				continue
			}
			sym := strings.ToUpper(tx.TokenSymbol)
			amountUSD := rawToAmount(tx.Value, tx.TokenDecimals)
			if amountUSD < t.cfg.ThresholdUSDT {
				continue
			}
			t.handleDeposit(ctx, addr, tx.Hash, sym, amountUSD, tx.Timestamp)
		}

	case "tron":
		txs, err := t.tron.TokenTxSince(ctx, addr.Address, sinceMS)
		if err != nil {
			log.Printf("whale: tron %s: %v", addr.Address[:8], err)
			return
		}
		for _, tx := range txs {
			if !IsExchangeDeposit(tx.To, "tron") {
				continue
			}
			sym := strings.ToUpper(tx.TokenSymbol)
			amountUSD := rawToAmount(tx.Value, tx.Decimals)
			if amountUSD < t.cfg.ThresholdUSDT {
				continue
			}
			t.handleDeposit(ctx, addr, tx.TxID, sym, amountUSD, tx.Timestamp/1000)
		}
	}
}

func (t *Tracker) handleDeposit(ctx context.Context, addr whaleAddress, txHash, tokenSym string, amountUSD float64, ts int64) {
	var exists bool
	_ = t.db.QueryRow(ctx, `SELECT true FROM whale_events WHERE tx_hash = $1`, txHash).Scan(&exists)
	if exists {
		return
	}

	scoreResult := t.analyzer.Analyze(ctx, addr.Address, addr.Chain)

	symbol := tokenToSymbol(tokenSym)
	if symbol == "" && scoreResult.TopToken != "" {
		symbol = tokenToSymbol(scoreResult.TopToken)
	}
	if symbol == "" {
		return
	}

	if _, err := t.db.Exec(ctx, `
		INSERT INTO whale_events (address, chain, symbol, amount_usd, direction, score, tx_hash, detected_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, to_timestamp($8))
		ON CONFLICT DO NOTHING
	`, addr.Address, addr.Chain, symbol, amountUSD, scoreResult.Direction, scoreResult.Score, txHash, ts); err != nil {
		log.Printf("whale: insert event: %v", err)
		return
	}

	_, _ = t.db.Exec(ctx, `
		UPDATE whale_addresses
		SET last_seen_at = now(), volume_30d = volume_30d + $1
		WHERE id = $2
	`, amountUSD, addr.ID)

	pipe := t.rdb.Pipeline()
	pipe.HSet(ctx, "whale:state", symbol, scoreResult.Direction)
	pipe.Expire(ctx, "whale:state", 2*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("whale: redis publish: %v", err)
	}

	log.Printf("whale: %s %s deposit $%.0f → %s %s (score %.2f)",
		addr.Chain, fmt.Sprintf("%.8s...", addr.Address), amountUSD, symbol, scoreResult.Direction, scoreResult.Score)
}
