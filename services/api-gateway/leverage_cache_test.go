//go:build integration

package main

import (
	"context"
	"testing"

	"sis/pkg/signal"
)

// TestPreloadLeverageCacheFromDB_WarmsInMemorySignalCache is the regression for the gap
// found live 2026-08-19: right after a restart, the "leverage" activation signal's
// in-memory cache (pkg/signal) started completely empty, even though the DB (symbol_leverage)
// already held perfectly good data from a prior run — the only way to populate the
// in-memory cache was the slow live-refresh loop working through ~700+ symbols one Bybit
// call at a time, so repeated "Проверка в моменте" checks right after a restart returned a
// growing match count over several minutes (not the exchange changing leverage tiers).
// preloadLeverageCacheFromDB must close that gap: one fast DB read, no network, before the
// live loop ever runs.
func TestPreloadLeverageCacheFromDB_WarmsInMemorySignalCache(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO symbol_leverage (symbol, category, max_leverage) VALUES ($1,$2,$3)
		 ON CONFLICT (symbol, category) DO UPDATE SET max_leverage=EXCLUDED.max_leverage`,
		"PRELOADTESTUSDT", "linear", 40,
	); err != nil {
		t.Fatalf("seed symbol_leverage: %v", err)
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM symbol_leverage WHERE symbol=$1", "PRELOADTESTUSDT")
	})

	// Confirm the in-memory cache does NOT already know this symbol (a stale run of this
	// same test, or another test, could otherwise make this test pass for the wrong reason).
	if got := signal.GetLeverageState("PRELOADTESTUSDT", "linear"); got != 0 {
		t.Fatalf("test setup: in-memory cache already has PRELOADTESTUSDT=%v before preload — test is not isolated", got)
	}

	preloadLeverageCacheFromDB(ctx, s.pool)

	if got := signal.GetLeverageState("PRELOADTESTUSDT", "linear"); got != 40 {
		t.Errorf("signal.GetLeverageState after preload = %v, want 40 (from the DB row, no network call needed)", got)
	}
}
