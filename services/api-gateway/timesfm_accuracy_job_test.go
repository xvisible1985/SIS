//go:build integration

package main

import (
	"context"
	"testing"
	"time"
)

func TestBackfillTimesfmPredictions_FillsOutcomeForPastTarget(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLUSDT") })
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM candles WHERE symbol=$1", "TFBACKFILLUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3)`,
		"TFBACKFILLUSDT", targetAt.Add(-15*time.Minute), targetAt,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}

	// A candle at-or-before target_at, close price up 3% from price_at_predict (100.0 → 103.0).
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO candles (exchange, symbol, market, timeframe, open_time, open, high, low, close, volume)
		VALUES ('bybit', $1, 'futures', '5m', $2, 102.0, 104.0, 101.0, 103.0, 1000)`,
		"TFBACKFILLUSDT", targetAt.Add(-1*time.Minute),
	); err != nil {
		t.Fatalf("seed candle: %v", err)
	}

	backfillTimesfmPredictions(ctx, s.pool)

	var actualPrice float64
	var actualDirection string
	var correct bool
	if err := s.pool.QueryRow(ctx, `
		SELECT actual_price, actual_direction, correct FROM timesfm_predictions WHERE symbol=$1`,
		"TFBACKFILLUSDT",
	).Scan(&actualPrice, &actualDirection, &correct); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if actualPrice != 103.0 {
		t.Errorf("actual_price = %v, want 103.0", actualPrice)
	}
	if actualDirection != "buy" {
		t.Errorf("actual_direction = %q, want %q (3%% >= 0.5%% threshold)", actualDirection, "buy")
	}
	if !correct {
		t.Error("correct = false, want true (predicted buy, actual buy)")
	}
}

func TestBackfillTimesfmPredictions_SkipsFutureTarget(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLFUTUREUSDT") })

	futureTarget := time.Now().Add(1 * time.Hour)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,NOW(),100.0,2.0,'buy',$2)`,
		"TFBACKFILLFUTUREUSDT", futureTarget,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}

	backfillTimesfmPredictions(ctx, s.pool)

	var actualPrice *float64
	if err := s.pool.QueryRow(ctx,
		`SELECT actual_price FROM timesfm_predictions WHERE symbol=$1`, "TFBACKFILLFUTUREUSDT",
	).Scan(&actualPrice); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if actualPrice != nil {
		t.Error("actual_price was filled in for a prediction whose target_at is still in the future")
	}
}

func TestBackfillTimesfmPredictions_NoCandleFound_IncrementsAttempts(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLNOCANDLEUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3)`,
		"TFBACKFILLNOCANDLEUSDT", targetAt.Add(-15*time.Minute), targetAt,
	); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
	// Deliberately no candle seeded — nearestCandleClose must find nothing for this symbol.

	backfillTimesfmPredictions(ctx, s.pool)

	var attempts int
	var actualPrice *float64
	if err := s.pool.QueryRow(ctx,
		`SELECT attempts, actual_price FROM timesfm_predictions WHERE symbol=$1`, "TFBACKFILLNOCANDLEUSDT",
	).Scan(&attempts, &actualPrice); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 after one failed lookup", attempts)
	}
	if actualPrice != nil {
		t.Error("actual_price was filled in despite no matching candle existing")
	}
}

func TestBackfillTimesfmPredictions_MaxAttemptsReached_StopsBeingSelected(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM timesfm_predictions WHERE symbol=$1", "TFBACKFILLMAXATTEMPTSUSDT") })

	targetAt := time.Now().Add(-10 * time.Minute)
	var id string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at, attempts)
		VALUES ('bybit',$1,'futures','5m',100,3,$2,100.0,2.0,'buy',$3,50) RETURNING id`,
		"TFBACKFILLMAXATTEMPTSUSDT", targetAt.Add(-15*time.Minute), targetAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
	// No candle seeded — if this row were (wrongly) selected, it would fail the lookup and
	// increment attempts past 50; the assertion below proves it was never selected at all.

	backfillTimesfmPredictions(ctx, s.pool)

	var attempts int
	if err := s.pool.QueryRow(ctx, `SELECT attempts FROM timesfm_predictions WHERE id=$1`, id).Scan(&attempts); err != nil {
		t.Fatalf("query result: %v", err)
	}
	if attempts != 50 {
		t.Errorf("attempts = %d, want unchanged 50 — a row at the cap must not be selected/incremented again", attempts)
	}
}
