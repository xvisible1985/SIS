// services/signal-engine/backtest.go
package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"sis/pkg/models"
	"sis/pkg/signals"
)

// BacktestParams defines the parameters for a single backtest run.
type BacktestParams struct {
	SignalID    string
	Symbol     string
	Market     models.Market
	Timeframe  models.Timeframe
	Exchange   models.Exchange
	PeriodFrom time.Time
	PeriodTo   time.Time
	Direction  string  // "LONG" | "SHORT" | "BOTH"
	TakeProfit float64 // percent, e.g. 2.0 = 2%
	StopLoss   float64 // percent, e.g. 1.0 = 1%
	Conditions signals.Node
}

// Trade records a single simulated trade.
type Trade struct {
	EntryTime  time.Time `json:"entry_time"`
	EntryPrice float64   `json:"entry_price"`
	ExitTime   time.Time `json:"exit_time"`
	ExitPrice  float64   `json:"exit_price"`
	Direction  string    `json:"direction"`
	Result     string    `json:"result"` // "win" | "loss" | "open"
	GainPct    float64   `json:"gain_pct"`
	DayOfWeek  string    `json:"day_of_week"`
	HourOfDay  int       `json:"hour_of_day"`
}

// BacktestResult holds the summary statistics for a completed backtest.
type BacktestResult struct {
	TotalSignals int     `json:"total_signals"`
	WinCount     int     `json:"win_count"`
	LossCount    int     `json:"loss_count"`
	WinRate      float64 `json:"win_rate"`
	AvgGain      float64 `json:"avg_gain"`
	MaxDrawdown  float64 `json:"max_drawdown"`
	ProfitFactor float64 `json:"profit_factor"`
	Trades       []Trade `json:"trades"`
}

// RunBacktest executes a backtest for the given parameters.
// It fetches candles from TimescaleDB, evaluates the signal on each candle,
// and simulates trades using a fixed TP/SL model.
// progress is called with a value from 0 to 100 every 5% of progress.
func RunBacktest(ctx context.Context, pool *pgxpool.Pool, p BacktestParams, progress func(pct int)) (BacktestResult, error) {
	candles, err := fetchCandles(ctx, pool, p)
	if err != nil {
		return BacktestResult{}, fmt.Errorf("backtest: fetch candles: %w", err)
	}
	if len(candles) == 0 {
		return BacktestResult{}, fmt.Errorf("backtest: no candles for %s %s %s in period", p.Symbol, p.Market, p.Timeframe)
	}

	cache := signals.IndicatorCache{}
	var trades []Trade
	total := len(candles)
	lastPct := 0

	i := 0
	for i < total {
		// Report progress every 5%
		pct := i * 100 / total
		if pct >= lastPct+5 {
			if progress != nil {
				progress(pct)
			}
			lastPct = pct
		}

		fired, err := signals.Evaluate(p.Conditions, candles, i, cache, nil)
		if err != nil {
			return BacktestResult{}, fmt.Errorf("backtest: evaluate at %d: %w", i, err)
		}

		if fired {
			entry := candles[i]
			trade, skip := simulateTrade(candles, i, p, entry)
			trades = append(trades, trade)
			if skip > 0 {
				i += skip // skip ahead past the trade resolution candle
				continue
			}
		}
		i++
	}

	if progress != nil {
		progress(100)
	}
	return computeMetrics(trades), nil
}

// simulateTrade simulates a trade starting at candle[entryIdx].
// Returns the trade and how many candles to skip (to avoid overlapping trades).
func simulateTrade(candles []models.Candle, entryIdx int, p BacktestParams, entry models.Candle) (Trade, int) {
	dir := p.Direction
	if dir == "BOTH" {
		dir = "LONG" // default for BOTH in simple sim
	}

	entryPrice := entry.Close
	var tpPrice, slPrice float64
	if dir == "LONG" {
		tpPrice = entryPrice * (1 + p.TakeProfit/100)
		slPrice = entryPrice * (1 - p.StopLoss/100)
	} else {
		tpPrice = entryPrice * (1 - p.TakeProfit/100)
		slPrice = entryPrice * (1 + p.StopLoss/100)
	}

	trade := Trade{
		EntryTime:  entry.OpenTime,
		EntryPrice: entryPrice,
		Direction:  dir,
		DayOfWeek:  entry.OpenTime.Weekday().String(),
		HourOfDay:  entry.OpenTime.Hour(),
	}

	// Scan future candles for TP or SL hit
	for j := entryIdx + 1; j < len(candles); j++ {
		c := candles[j]
		if dir == "LONG" {
			if c.High >= tpPrice {
				trade.ExitTime = c.OpenTime
				trade.ExitPrice = tpPrice
				trade.Result = "win"
				trade.GainPct = p.TakeProfit
				return trade, j - entryIdx
			}
			if c.Low <= slPrice {
				trade.ExitTime = c.OpenTime
				trade.ExitPrice = slPrice
				trade.Result = "loss"
				trade.GainPct = -p.StopLoss
				return trade, j - entryIdx
			}
		} else {
			if c.Low <= tpPrice {
				trade.ExitTime = c.OpenTime
				trade.ExitPrice = tpPrice
				trade.Result = "win"
				trade.GainPct = p.TakeProfit
				return trade, j - entryIdx
			}
			if c.High >= slPrice {
				trade.ExitTime = c.OpenTime
				trade.ExitPrice = slPrice
				trade.Result = "loss"
				trade.GainPct = -p.StopLoss
				return trade, j - entryIdx
			}
		}
	}

	// Trade still open at end of data
	last := candles[len(candles)-1]
	trade.ExitTime = last.OpenTime
	trade.ExitPrice = last.Close
	trade.Result = "open"
	if dir == "LONG" {
		trade.GainPct = (last.Close - entryPrice) / entryPrice * 100
	} else {
		trade.GainPct = (entryPrice - last.Close) / entryPrice * 100
	}
	return trade, 0
}

// computeMetrics calculates summary statistics from a list of trades.
func computeMetrics(trades []Trade) BacktestResult {
	result := BacktestResult{
		TotalSignals: len(trades),
		Trades:       trades,
	}
	if len(trades) == 0 {
		return result
	}

	var totalGain, totalLoss float64
	var equity, peak, maxDD float64

	for _, t := range trades {
		switch t.Result {
		case "win":
			result.WinCount++
			totalGain += t.GainPct
		case "loss":
			result.LossCount++
			totalLoss += math.Abs(t.GainPct)
		}
		equity += t.GainPct
		if equity > peak {
			peak = equity
		}
		dd := peak - equity
		if dd > maxDD {
			maxDD = dd
		}
	}

	if result.TotalSignals > 0 {
		result.WinRate = float64(result.WinCount) / float64(result.TotalSignals)
	}
	closedTrades := result.WinCount + result.LossCount
	if closedTrades > 0 {
		result.AvgGain = (totalGain - totalLoss) / float64(closedTrades)
	}
	result.MaxDrawdown = maxDD
	if totalLoss > 0 {
		result.ProfitFactor = totalGain / totalLoss
	} else if totalGain > 0 {
		result.ProfitFactor = math.MaxFloat64
	}

	return result
}

// fetchCandles retrieves candles from TimescaleDB for the given parameters.
func fetchCandles(ctx context.Context, pool *pgxpool.Pool, p BacktestParams) ([]models.Candle, error) {
	rows, err := pool.Query(ctx, `
		SELECT open_time, open, high, low, close, volume
		FROM candles
		WHERE exchange=$1 AND symbol=$2 AND market=$3 AND timeframe=$4
		  AND open_time >= $5 AND open_time < $6
		ORDER BY open_time ASC`,
		string(p.Exchange), p.Symbol, string(p.Market), string(p.Timeframe),
		p.PeriodFrom, p.PeriodTo,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch candles: query: %w", err)
	}
	defer rows.Close()

	var candles []models.Candle
	for rows.Next() {
		var c models.Candle
		c.Exchange = p.Exchange
		c.Symbol = p.Symbol
		c.Market = p.Market
		c.Timeframe = p.Timeframe
		c.Closed = true
		if err := rows.Scan(&c.OpenTime, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("fetch candles: scan: %w", err)
		}
		candles = append(candles, c)
	}
	return candles, rows.Err()
}
