//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/signal"
)

func TestNewTimesfmRefreshFunc_SuccessfulCall_UpdatesCacheAndInsertsRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH1USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"point_forecast":[100.5,101.0,102.0]}`))
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)

	candles := make([]signal.Candle, 5)
	for i := range candles {
		candles[i] = signal.Candle{Time: int64(i) * 300_000, Close: 100.0}
	}

	refresh("TFREFRESH1USDT", "5m", candles, 5, 3)

	// Cache must be updated: predicted = 102.0, lastClose = 100.0 → pct = 2.0%
	pct, fresh := signal.GetTimesfmForecast("TFREFRESH1USDT", "5m", 5, 3, time.Minute)
	if !fresh {
		t.Fatal("cache entry not fresh after a successful refresh")
	}
	if pct < 1.99 || pct > 2.01 {
		t.Errorf("cached predictedPct = %v, want ~2.0", pct)
	}

	var count int
	var predictedDirection string
	var predictedPct float64
	var contextBars int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH1USDT",
	).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Fatalf("timesfm_predictions rows = %d, want 1", count)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT predicted_direction, predicted_pct, context_bars FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH1USDT",
	).Scan(&predictedDirection, &predictedPct, &contextBars); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if predictedDirection != "buy" {
		t.Errorf("predicted_direction = %q, want %q (2%% >= the fixed 0.5%% log threshold)", predictedDirection, "buy")
	}
	if predictedPct < 1.99 || predictedPct > 2.01 {
		t.Errorf("predicted_pct = %v, want ~2.0", predictedPct)
	}
	if contextBars != 5 {
		t.Errorf("context_bars = %d, want 5", contextBars)
	}
}

func TestNewTimesfmRefreshFunc_ServiceError_DoesNotUpdateCacheOrInsertRow(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH2USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)
	candles := []signal.Candle{{Time: 0, Close: 100.0}, {Time: 300_000, Close: 100.0}}

	refresh("TFREFRESH2USDT", "5m", candles, 2, 3)

	if _, fresh := signal.GetTimesfmForecast("TFREFRESH2USDT", "5m", 2, 3, time.Minute); fresh {
		t.Error("cache marked fresh after a failed model-service call")
	}
	var count int
	s.pool.QueryRow(ctx, `SELECT count(*) FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH2USDT").Scan(&count)
	if count != 0 {
		t.Errorf("timesfm_predictions rows = %d, want 0 after a failed call", count)
	}
}

// TestNewTimesfmRefreshFunc_ContextBarsIndependentOfCandleLength is the regression for a
// cache-key mismatch bug caught in code review: the closure must cache/log under the
// EXPLICITLY PASSED contextBars, never under len(candles) — those two can legitimately
// differ (a symbol/timeframe that hasn't accumulated contextBars worth of history yet still
// gets forecast on whatever candles it has, but must be cached under the caller's configured
// contextBars so a later ComputeWithSymbol call — which always queries by configured
// contextBars, see pkg/signal/timesfm.go — actually finds it).
func TestNewTimesfmRefreshFunc_ContextBarsIndependentOfCandleLength(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFREFRESH3USDT") })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"point_forecast":[100.5,101.0,102.0]}`))
	}))
	defer srv.Close()

	refresh := newTimesfmRefreshFunc(s.pool, srv.URL)

	// Only 5 candles available, but the configured contextBars is 100 (simulating a
	// symbol/timeframe still ramping up its history).
	candles := make([]signal.Candle, 5)
	for i := range candles {
		candles[i] = signal.Candle{Time: int64(i) * 300_000, Close: 100.0}
	}

	refresh("TFREFRESH3USDT", "5m", candles, 100, 3)

	// Must be cached under contextBars=100 (the configured value) — NOT contextBars=5
	// (len(candles)) — since that's the only key a real ComputeWithSymbol call will ever
	// query with.
	if _, fresh := signal.GetTimesfmForecast("TFREFRESH3USDT", "5m", 100, 3, time.Minute); !fresh {
		t.Error("cache entry not found under the configured contextBars=100 — likely cached under len(candles)=5 instead")
	}
	if _, fresh := signal.GetTimesfmForecast("TFREFRESH3USDT", "5m", 5, 3, time.Minute); fresh {
		t.Error("cache entry found under contextBars=5 (len(candles)) — must only be cached under the configured contextBars")
	}

	var contextBars int
	if err := s.pool.QueryRow(ctx,
		`SELECT context_bars FROM timesfm_predictions WHERE symbol=$1`, "TFREFRESH3USDT",
	).Scan(&contextBars); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if contextBars != 100 {
		t.Errorf("context_bars logged = %d, want 100 (the configured value)", contextBars)
	}
}
