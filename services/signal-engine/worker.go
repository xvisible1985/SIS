// services/signal-engine/worker.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"sis/pkg/models"
	"sis/pkg/signals"
)

const (
	streamBacktest = "jobs:backtest"
	consumerGroup  = "signal-engine"
	consumerName   = "worker-1"
	progressKeyFmt = "jobs:%s:progress"
)

// JobPayload is the structure of a backtest job message in Redis Streams.
type JobPayload struct {
	JobID      string          `json:"job_id"`
	SignalID   string          `json:"signal_id"`
	Symbol     string          `json:"symbol"`
	Market     string          `json:"market"`
	Timeframe  string          `json:"timeframe"`
	Exchange   string          `json:"exchange"`
	Direction  string          `json:"direction"`
	PeriodFrom string          `json:"period_from"` // RFC3339
	PeriodTo   string          `json:"period_to"`   // RFC3339
	TakeProfit float64         `json:"take_profit"`
	StopLoss   float64         `json:"stop_loss"`
	Conditions json.RawMessage `json:"conditions"`
}

// Worker consumes backtest jobs from Redis Streams and executes them.
type Worker struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
}

func NewWorker(pool *pgxpool.Pool, rdb *redis.Client) *Worker {
	return &Worker{pool: pool, rdb: rdb}
}

// Start runs the consumer loop. Blocks until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	// Create consumer group if not exists
	w.rdb.XGroupCreateMkStream(ctx, streamBacktest, consumerGroup, "0")

	log.Printf("worker: listening on stream %s", streamBacktest)
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		msgs, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
			Streams:  []string{streamBacktest, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("worker: xreadgroup error: %v", err)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				w.handleMessage(ctx, msg)
			}
		}
	}
}

func (w *Worker) handleMessage(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("worker: message %s missing payload", msg.ID)
		w.ack(ctx, msg.ID)
		return
	}

	var job JobPayload
	if err := json.Unmarshal([]byte(raw.(string)), &job); err != nil {
		log.Printf("worker: unmarshal job %s: %v", msg.ID, err)
		w.ack(ctx, msg.ID)
		return
	}

	log.Printf("worker: processing job %s signal=%s %s/%s", job.JobID, job.SignalID, job.Symbol, job.Timeframe)

	if err := w.runJob(ctx, job); err != nil {
		log.Printf("worker: job %s failed: %v", job.JobID, err)
		progressKey := fmt.Sprintf(progressKeyFmt, job.JobID)
		w.rdb.HSet(ctx, progressKey, "status", "error", "error", err.Error(), "updated_at", time.Now().Unix())
	}
	w.ack(ctx, msg.ID)
}

func (w *Worker) runJob(ctx context.Context, job JobPayload) error {
	from, err := time.Parse(time.RFC3339, job.PeriodFrom)
	if err != nil {
		return fmt.Errorf("parse period_from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, job.PeriodTo)
	if err != nil {
		return fmt.Errorf("parse period_to: %w", err)
	}

	node, err := signals.ParseConditions(job.Conditions)
	if err != nil {
		return fmt.Errorf("parse conditions: %w", err)
	}

	progressKey := fmt.Sprintf(progressKeyFmt, job.JobID)
	params := BacktestParams{
		SignalID:   job.SignalID,
		Symbol:     job.Symbol,
		Market:     models.Market(job.Market),
		Timeframe:  models.Timeframe(job.Timeframe),
		Exchange:   models.Exchange(job.Exchange),
		Direction:  job.Direction,
		PeriodFrom: from,
		PeriodTo:   to,
		TakeProfit: job.TakeProfit,
		StopLoss:   job.StopLoss,
		Conditions: node,
	}

	progress := func(pct int) {
		w.rdb.HSet(ctx, progressKey, "pct", pct, "updated_at", time.Now().Unix())
	}
	progress(0)

	result, err := RunBacktest(ctx, w.pool, params, progress)
	if err != nil {
		return fmt.Errorf("run backtest: %w", err)
	}

	if err := w.saveResult(ctx, job, result); err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	w.rdb.HSet(ctx, progressKey, "pct", 100, "status", "done", "updated_at", time.Now().Unix())
	log.Printf("worker: job %s done — %d trades, win_rate=%.2f", job.JobID, result.TotalSignals, result.WinRate)
	return nil
}

func (w *Worker) saveResult(ctx context.Context, job JobPayload, r BacktestResult) error {
	tradesJSON, _ := json.Marshal(r.Trades)
	patternsJSON := []byte("{}")

	_, err := w.pool.Exec(ctx, `
		INSERT INTO backtest_results
			(signal_id, symbol, timeframe, period_from, period_to, mode,
			 total_signals, win_count, loss_count, win_rate, avg_gain,
			 max_drawdown, profit_factor, patterns, trades)
		VALUES ($1,$2,$3,$4,$5,'fast',$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		job.SignalID, job.Symbol, job.Timeframe,
		job.PeriodFrom, job.PeriodTo,
		r.TotalSignals, r.WinCount, r.LossCount,
		r.WinRate, r.AvgGain, r.MaxDrawdown, r.ProfitFactor,
		patternsJSON, tradesJSON,
	)
	return err
}

func (w *Worker) ack(ctx context.Context, msgID string) {
	if err := w.rdb.XAck(ctx, streamBacktest, consumerGroup, msgID).Err(); err != nil {
		log.Printf("worker: ack error %s: %v", msgID, err)
	}
}
