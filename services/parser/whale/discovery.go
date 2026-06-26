// services/parser/whale/discovery.go
package whale

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Discovery runs weekly to find top whale addresses sending tokens to Bybit.
type Discovery struct {
	db   *pgxpool.Pool
	eth  *EthClient
	tron *TronClient
	cfg  Config
}

// NewDiscovery creates a Discovery runner.
func NewDiscovery(db *pgxpool.Pool, eth *EthClient, tron *TronClient, cfg Config) *Discovery {
	return &Discovery{db: db, eth: eth, tron: tron, cfg: cfg}
}

// Start runs the discovery weekly. Blocks until ctx is cancelled.
func (d *Discovery) Start(ctx context.Context) {
	d.run(ctx)
	ticker := time.NewTicker(7 * 24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.run(ctx)
		}
	}
}

// ForceRun triggers an immediate discovery run.
func (d *Discovery) ForceRun(ctx context.Context) {
	go d.run(ctx)
}

func (d *Discovery) run(ctx context.Context) {
	log.Println("whale discovery: starting")
	since30d := time.Now().AddDate(0, 0, -30).Unix()

	for wallet := range knownBybitETHWallets {
		d.scanETHWallet(ctx, wallet, since30d)
	}
	for wallet := range knownBybitTronWallets {
		d.scanTronWallet(ctx, wallet, since30d)
	}

	_, err := d.db.Exec(ctx, `
		UPDATE whale_addresses
		SET is_active = false
		WHERE is_manual = false
		  AND (last_seen_at IS NULL OR last_seen_at < now() - interval '90 days')
	`)
	if err != nil {
		log.Printf("whale discovery: deactivate: %v", err)
	}
	log.Println("whale discovery: done")
}

func (d *Discovery) scanETHWallet(ctx context.Context, bybitWallet string, since int64) {
	txs, err := d.eth.TokenTxSince(ctx, bybitWallet, since)
	if err != nil {
		log.Printf("whale discovery: eth %s: %v", bybitWallet[:8], err)
		return
	}
	vol := make(map[string]float64)
	for _, tx := range txs {
		if strings.ToLower(tx.To) != bybitWallet {
			continue
		}
		amt := rawToAmount(tx.Value, tx.TokenDecimals)
		vol[strings.ToLower(tx.From)] += amt
	}
	d.upsertTopSenders(ctx, vol, "eth")
}

func (d *Discovery) scanTronWallet(ctx context.Context, bybitWallet string, since int64) {
	txs, err := d.tron.TokenTxSince(ctx, bybitWallet, since*1000)
	if err != nil {
		log.Printf("whale discovery: tron %s: %v", bybitWallet[:8], err)
		return
	}
	vol := make(map[string]float64)
	for _, tx := range txs {
		if tx.To != bybitWallet {
			continue
		}
		amt := rawToAmount(tx.Value, tx.Decimals)
		vol[tx.From] += amt
	}
	d.upsertTopSenders(ctx, vol, "tron")
}

func (d *Discovery) upsertTopSenders(ctx context.Context, vol map[string]float64, chain string) {
	for addr, amount := range vol {
		if amount < d.cfg.ThresholdUSDT {
			continue
		}
		_, err := d.db.Exec(ctx, `
			INSERT INTO whale_addresses (address, chain, volume_30d, is_manual)
			VALUES ($1, $2, $3, false)
			ON CONFLICT (address, chain) DO UPDATE
			SET volume_30d = EXCLUDED.volume_30d, is_active = true
		`, addr, chain, amount)
		if err != nil {
			log.Printf("whale discovery: upsert %s: %v", addr[:8], err)
		}
	}
}
