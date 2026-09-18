package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/signal"
)

// timesfmForecastRequest/Response mirror services/timesfm-service's HTTP contract exactly
// (POST /forecast — see services/timesfm-service/main.py).
type timesfmForecastRequest struct {
	Series  []float64 `json:"series"`
	Horizon int       `json:"horizon"`
}

type timesfmForecastResponse struct {
	PointForecast []float64 `json:"point_forecast"`
}

// callTimesfmService POSTs a closing-price series to the local TimesFM Python service and
// returns its point forecast. Uses a plain http.Client — NOT proxy.HTTPClient() — the proxy
// pool is for outbound exchange traffic and would misroute a call to a local service if any
// proxies happen to be configured (proxy.HTTPClient() has no localhost-aware bypass).
func callTimesfmService(ctx context.Context, baseURL string, series []float64, horizon int) ([]float64, error) {
	body, err := json.Marshal(timesfmForecastRequest{Series: series, Horizon: horizon})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/forecast", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("timesfm service: status %d", resp.StatusCode)
	}
	var out timesfmForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.PointForecast) == 0 {
		return nil, fmt.Errorf("timesfm service: empty forecast")
	}
	return out.PointForecast, nil
}

// timesfmIntervalDuration maps this codebase's canonical timeframe strings to their
// wall-clock bar length — used to compute target_at (predicted_at + horizon_bars bars).
var timesfmIntervalDuration = map[string]time.Duration{
	"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"1h": time.Hour, "4h": 4 * time.Hour, "1d": 24 * time.Hour,
}

// timesfmExchange/timesfmMarket are the only exchange/market this platform's candles table
// currently stores (models.ExchangeBybit / models.MarketFutures — see pkg/models/candle.go).
// Written explicitly into every row (timesfm_predictions.exchange/.market, added in Task 1)
// rather than relying on a DB default, so it's always clear from the Go code — not just the
// schema — which candle series a prediction is scored against.
const (
	timesfmExchange = "bybit"
	timesfmMarket   = "futures"
)

// timesfmLogThresholdPct is the FIXED threshold used to classify predicted_direction/
// actual_direction in the timesfm_predictions log — deliberately independent of any
// individual bot's own threshold_pct (which can differ per bot config; see timesfm.go's
// deriveTimesfmState for the per-caller version). The log exists to answer "is the model
// directionally correct at all," not "would this specific bot's threshold have profited" —
// using one fixed reference keeps every logged row comparable to every other.
const timesfmLogThresholdPct = 0.5

func timesfmDirection(pct float64) string {
	switch {
	case pct >= timesfmLogThresholdPct:
		return "buy"
	case pct <= -timesfmLogThresholdPct:
		return "sell"
	default:
		return "neutral"
	}
}

func insertTimesfmPrediction(ctx context.Context, pool *pgxpool.Pool, symbol, timeframe string, contextBars, horizonBars int, priceAtPredict, predictedPct float64) error {
	barDur, ok := timesfmIntervalDuration[timeframe]
	if !ok {
		return fmt.Errorf("timesfm: unknown timeframe %q", timeframe)
	}
	predictedAt := time.Now()
	targetAt := predictedAt.Add(time.Duration(horizonBars) * barDur)
	_, err := pool.Exec(ctx, `
		INSERT INTO timesfm_predictions
			(exchange, symbol, market, timeframe, context_bars, horizon_bars, predicted_at, price_at_predict, predicted_pct, predicted_direction, target_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		timesfmExchange, symbol, timesfmMarket, timeframe, contextBars, horizonBars, predictedAt, priceAtPredict, predictedPct, timesfmDirection(predictedPct), targetAt,
	)
	return err
}

// newTimesfmRefreshFunc builds the closure assigned to signal.TimesfmRefreshFunc at startup
// (main.go). It performs the work pkg/signal itself is never allowed to do: an HTTP call to
// the external model service, and a DB write — then pushes the result back into pkg/signal's
// cache via signal.SetTimesfmForecast. On any failure (service unreachable, bad response, DB
// error) the cache is left untouched — the next stale ComputeWithSymbol call will retry.
//
// contextBars is used verbatim for both the cache key (signal.SetTimesfmForecast) and the
// logged row (insertTimesfmPrediction) — NEVER re-derived from len(candles), which can
// legitimately be smaller (a symbol/timeframe still ramping up its history). Caching under
// len(candles) instead would write to a key no future ComputeWithSymbol call — which always
// queries by the signal's CONFIGURED contextBars — would ever read back, silently defeating
// the cache. See TimesfmRefreshFunc's doc comment in pkg/signal/timesfm_state.go.
func newTimesfmRefreshFunc(pool *pgxpool.Pool, baseURL string) func(symbol, timeframe string, candles []signal.Candle, contextBars, horizonBars int) {
	return func(symbol, timeframe string, candles []signal.Candle, contextBars, horizonBars int) {
		if len(candles) == 0 {
			return
		}
		series := make([]float64, len(candles))
		for i, c := range candles {
			series[i] = c.Close
		}
		lastClose := series[len(series)-1]

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		forecast, err := callTimesfmService(ctx, baseURL, series, horizonBars)
		if err != nil {
			log.Printf("timesfm: forecast %s/%s: %v", symbol, timeframe, err)
			return
		}

		predicted := forecast[len(forecast)-1]
		if lastClose == 0 {
			log.Printf("timesfm: %s/%s: lastClose is 0, cannot compute predicted_pct", symbol, timeframe)
			return
		}
		predictedPct := (predicted - lastClose) / lastClose * 100

		if err := insertTimesfmPrediction(ctx, pool, symbol, timeframe, contextBars, horizonBars, lastClose, predictedPct); err != nil {
			log.Printf("timesfm: insert prediction %s/%s: %v", symbol, timeframe, err)
			return
		}
		// Cache is only updated after the DB write succeeds, so a DB failure doesn't leave
		// pkg/signal confidently serving a forecast this service has no record of.
		signal.SetTimesfmForecast(symbol, timeframe, contextBars, horizonBars, predictedPct)
	}
}
