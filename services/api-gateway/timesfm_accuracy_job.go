package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunTimesfmAccuracyBackfill starts a background goroutine that, every 5 minutes, fills in
// the outcome (actual_price/actual_direction/correct) for any timesfm_predictions row whose
// forecast horizon has passed. Mirrors RunLeverageRefresher's ticker/goroutine skeleton
// (services/api-gateway/leverage_cache.go): an immediate run on startup, then a fixed
// interval, exiting on ctx.Done().
func RunTimesfmAccuracyBackfill(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		for {
			backfillTimesfmPredictions(ctx, pool)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Minute):
			}
		}
	}()
}

type pendingTimesfmPrediction struct {
	id             string
	exchange       string
	symbol         string
	market         string
	timeframe      string
	priceAtPredict float64
	targetAt       time.Time
}

// timesfmMaxBackfillAttempts caps how many times the backfill job will retry looking up a
// candle for one prediction before giving up on it. Without this, a permanently
// unresolvable row (a delisted symbol, or candle history for it that simply never arrives)
// would keep occupying a slot in the oldest-first `LIMIT 200` pending scan forever and could
// crowd out genuinely-recent rows once the backlog grows past 200. A row that hits the cap
// stays in the table (still visible, still counted as "not checked") — it's just never
// selected again, distinguishable from a genuinely-pending row via its `attempts` value.
const timesfmMaxBackfillAttempts = 50

func backfillTimesfmPredictions(ctx context.Context, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT id, exchange, symbol, market, timeframe, price_at_predict, target_at
		FROM timesfm_predictions
		WHERE target_at <= NOW() AND actual_price IS NULL AND attempts < $1
		ORDER BY target_at ASC
		LIMIT 200`,
		timesfmMaxBackfillAttempts,
	)
	if err != nil {
		log.Printf("timesfm accuracy backfill: query pending: %v", err)
		return
	}
	var pending []pendingTimesfmPrediction
	for rows.Next() {
		var p pendingTimesfmPrediction
		if err := rows.Scan(&p.id, &p.exchange, &p.symbol, &p.market, &p.timeframe, &p.priceAtPredict, &p.targetAt); err != nil {
			log.Printf("timesfm accuracy backfill: scan pending row: %v", err)
			continue
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("timesfm accuracy backfill: iterate pending: %v", err)
	}
	rows.Close()

	for _, p := range pending {
		actualPrice, ok := nearestCandleClose(ctx, pool, p.exchange, p.symbol, p.market, p.timeframe, p.targetAt)
		if !ok {
			// No candle at/before target_at yet (or ever, for an unresolvable row) — count
			// the attempt and retry on a later sweep.
			if _, err := pool.Exec(ctx, `UPDATE timesfm_predictions SET attempts = attempts + 1 WHERE id=$1`, p.id); err != nil {
				log.Printf("timesfm accuracy backfill: increment attempts %s: %v", p.id, err)
			}
			continue
		}
		if p.priceAtPredict == 0 {
			// price_at_predict should never be 0 — timesfm_refresh.go guards lastClose == 0
			// before ever writing a prediction row — so this signals a data-integrity issue
			// (bad manual insert, a future bug elsewhere) rather than "candle not found yet."
			// Leave the row untouched (don't burn an attempt on it) so it stays visible for
			// investigation instead of being silently corrupted with +Inf/NaN direction.
			log.Printf("timesfm accuracy backfill: prediction %s has price_at_predict=0, skipping", p.id)
			continue
		}
		actualDirection := timesfmDirection((actualPrice - p.priceAtPredict) / p.priceAtPredict * 100)

		if _, err := pool.Exec(ctx, `
			UPDATE timesfm_predictions
			SET actual_price=$1, actual_direction=$2, correct=(predicted_direction=$2), checked_at=NOW()
			WHERE id=$3`,
			actualPrice, actualDirection, p.id,
		); err != nil {
			log.Printf("timesfm accuracy backfill: update %s: %v", p.id, err)
		}
	}
}

// nearestCandleClose returns the close price of the most recent candle at or before at, for
// the given exchange/symbol/market/timeframe — matching candles' own primary key shape
// (migrations/001_initial.sql) so a backfilled outcome is always scored against the correct
// candle series, not an assumed one.
func nearestCandleClose(ctx context.Context, pool *pgxpool.Pool, exchange, symbol, market, timeframe string, at time.Time) (float64, bool) {
	var close float64
	err := pool.QueryRow(ctx, `
		SELECT close FROM candles
		WHERE exchange=$1 AND symbol=$2 AND market=$3 AND timeframe=$4
		  AND open_time <= $5
		ORDER BY open_time DESC
		LIMIT 1`,
		exchange, symbol, market, timeframe, at,
	).Scan(&close)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			// A genuine "no row found yet" is the expected/common case and stays silent —
			// this branch is for everything else (connection drop, context timeout, ...),
			// which would otherwise burn one of the row's limited attempts with zero
			// diagnostic trail.
			log.Printf("timesfm accuracy backfill: nearestCandleClose %s/%s: %v", symbol, timeframe, err)
		}
		return 0, false
	}
	return close, true
}
