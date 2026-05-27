package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/trader"
)

// GetMaxLeverage returns the cached max leverage for a symbol from the DB.
// Falls back to 0 if no row is present (caller should fall through to Bybit).
func getMaxLeverageFromDB(ctx context.Context, pool *pgxpool.Pool, symbol, category string) int {
	var lev int
	err := pool.QueryRow(ctx,
		`SELECT max_leverage FROM symbol_leverage WHERE symbol=$1 AND category=$2`,
		symbol, category,
	).Scan(&lev)
	if err != nil {
		return 0
	}
	return lev
}

// upsertMaxLeverage writes (or updates) max_leverage for a symbol into the DB.
func upsertMaxLeverage(ctx context.Context, pool *pgxpool.Pool, symbol, category string, maxLev int) {
	_, err := pool.Exec(ctx,
		`INSERT INTO symbol_leverage (symbol, category, max_leverage, refreshed_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (symbol, category) DO UPDATE
		   SET max_leverage=EXCLUDED.max_leverage, refreshed_at=NOW()`,
		symbol, category, maxLev,
	)
	if err != nil {
		log.Printf("upsertMaxLeverage %s/%s: %v", symbol, category, err)
	}
}

// refreshLeverage fetches max_leverage from Bybit for one symbol and persists it.
func refreshLeverage(ctx context.Context, pool *pgxpool.Pool, symbol, category string) {
	info, err := trader.GetPublicInstrumentInfo(ctx, category, symbol)
	if err != nil {
		log.Printf("refreshLeverage %s/%s: %v", symbol, category, err)
		return
	}
	lev := int(info.MaxLeverage)
	if lev < 1 {
		return
	}
	upsertMaxLeverage(ctx, pool, symbol, category, lev)
}

// RunLeverageRefresher starts a background goroutine that re-fetches max leverage
// for all symbols that have active strategies every 10 minutes.
func RunLeverageRefresher(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		// Run immediately on startup, then every 10 minutes.
		for {
			refreshAllActiveSymbols(ctx, pool)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
			}
		}
	}()
}

func refreshAllActiveSymbols(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT symbol, category FROM strategies WHERE status NOT IN ('stopped','error')`,
	)
	if err != nil {
		log.Printf("leverageRefresher: query active symbols: %v", err)
		return
	}
	defer rows.Close()

	type sym struct{ symbol, category string }
	var symbols []sym
	for rows.Next() {
		var s sym
		if err := rows.Scan(&s.symbol, &s.category); err == nil {
			symbols = append(symbols, s)
		}
	}

	for _, s := range symbols {
		refreshLeverage(ctx, pool, s.symbol, s.category)
	}
}
