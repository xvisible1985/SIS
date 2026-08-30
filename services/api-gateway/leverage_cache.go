package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/signal"
	"sis/pkg/trader"
)

// getMaxLeverageFromDB returns the cached max leverage for a symbol from the DB. Falls back
// to 0 if no row is present (caller should fall through to a live Bybit fetch) — used by
// GetInstrumentConstraints (instrument_handler.go).
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

// upsertMaxLeverage writes (or updates) max_leverage for a symbol into the DB, and warms
// pkg/signal's in-memory leverage cache (signal.SetLeverageState) so the "leverage"
// activation signal — pkg/signal never reaches out to a DB or exchange API itself, only
// ever reads whatever an external updater has pushed in (mirrors the whale-signal cache) —
// sees fresh data the moment the periodic refresher below learns it.
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
	signal.SetLeverageState(symbol, category, float64(maxLev))
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

// RunLeverageRefresher starts a background goroutine that re-fetches max leverage for
// every linear symbol on the exchange, every 10 minutes.
//
// Covers the full exchange universe, not just symbols with an active strategy: the
// "leverage" activation signal (pkg/signal) is evaluated against WHITELIST CANDIDATES —
// symbols a bot doesn't trade yet and is deciding whether to open — so the cache must be
// warm for those too, or the signal would only ever confirm for symbols already traded,
// making it useless for its actual purpose (gating which NEW symbols a bot opens).
// GetPublicInstrumentInfo is a cheap public (no API key) call with its own 5-min cache;
// ~400 symbols spread over the loop is well within Bybit's public rate limits — the same
// full-universe scan ensureMatrixStrategies already falls back to per-tick for a
// whitelist-less bot.
func RunLeverageRefresher(ctx context.Context, pool *pgxpool.Pool) {
	// Load whatever the DB already has into pkg/signal's in-memory cache FIRST — one fast
	// query, no network — so a restart doesn't leave the "leverage" activation signal blind
	// for the minutes it takes refreshAllLeverageSymbols to sequentially re-fetch ~700+
	// symbols one live Bybit call at a time. Found live (2026-08-19): right after a restart,
	// repeated "Проверка в моменте" clicks returned a growing match count (25 → 34) over
	// several minutes — not the exchange changing leverage tiers, but our own in-memory
	// cache still being empty and only catching up as the slow live loop progressed, even
	// though the DB already held perfectly good (if slightly stale — leverage tiers rarely
	// change) data for those symbols the whole time.
	preloadLeverageCacheFromDB(ctx, pool)
	go func() {
		// Run immediately on startup, then every 10 minutes.
		for {
			refreshAllLeverageSymbols(ctx, pool)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
			}
		}
	}()
}

// preloadLeverageCacheFromDB warms pkg/signal's in-memory leverage cache from every
// existing symbol_leverage row, synchronously, before RunLeverageRefresher returns.
func preloadLeverageCacheFromDB(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `SELECT symbol, category, max_leverage FROM symbol_leverage`)
	if err != nil {
		log.Printf("preloadLeverageCacheFromDB: query: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var symbol, category string
		var maxLev int
		if err := rows.Scan(&symbol, &category, &maxLev); err != nil {
			continue
		}
		signal.SetLeverageState(symbol, category, float64(maxLev))
		count++
	}
	log.Printf("preloadLeverageCacheFromDB: warmed %d symbols from DB", count)
}

func refreshAllLeverageSymbols(ctx context.Context, pool *pgxpool.Pool) {
	symbols, err := trader.FetchAllLinearSymbols(ctx)
	if err != nil {
		log.Printf("leverageRefresher: fetch exchange symbols: %v", err)
		return
	}
	for _, sym := range symbols {
		refreshLeverage(ctx, pool, sym, "linear")
	}
}
