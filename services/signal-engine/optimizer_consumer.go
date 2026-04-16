// services/signal-engine/optimizer_consumer.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"sis/pkg/models"
)

const (
	streamOptimize        = "jobs:optimize"
	optimizeConsumerGroup = "signal-engine-optimize"
	optimizeConsumerName  = "optimizer-1"
	optimizeProgressFmt   = "jobs:%s:optimize:progress"
)

// OptimizeJobPayload is the Redis Streams message structure for optimization jobs.
type OptimizeJobPayload struct {
	JobID              string          `json:"job_id"`
	SignalID           string          `json:"signal_id"`
	Symbol             string          `json:"symbol"`
	Market             string          `json:"market"`
	Timeframe          string          `json:"timeframe"`
	Exchange           string          `json:"exchange"`
	Direction          string          `json:"direction"`
	PeriodFrom         string          `json:"period_from"` // RFC3339
	PeriodTo           string          `json:"period_to"`   // RFC3339
	Mode               string          `json:"mode"`        // "fast" | "walk_forward"
	ScoreBy            string          `json:"score_by"`    // "profit_factor" | "win_rate" | "avg_gain"
	TopN               int             `json:"top_n"`
	TakeProfits        []float64       `json:"take_profits"`
	StopLosses         []float64       `json:"stop_losses"`
	ConditionsTemplate json.RawMessage `json:"conditions_template"`
	ParamSpace         ParamSpace      `json:"param_space"`
	WFFolds            int             `json:"wf_folds"` // walk-forward folds, default 4
}

// RunOptimizer starts the optimizer job consumer. Blocks until ctx is cancelled.
func (w *Worker) RunOptimizer(ctx context.Context) {
	w.rdb.XGroupCreateMkStream(ctx, streamOptimize, optimizeConsumerGroup, "0")

	log.Printf("optimizer: listening on stream %s", streamOptimize)
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    optimizeConsumerGroup,
			Consumer: optimizeConsumerName,
			Streams:  []string{streamOptimize, ">"},
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
			log.Printf("optimizer: xreadgroup error: %v", err)
			continue
		}

		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				w.handleOptimizeMessage(ctx, msg)
			}
		}
	}
}

func (w *Worker) handleOptimizeMessage(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("optimizer: message %s missing payload", msg.ID)
		w.ackOptimize(ctx, msg.ID)
		return
	}

	var payload OptimizeJobPayload
	if err := json.Unmarshal([]byte(raw.(string)), &payload); err != nil {
		log.Printf("optimizer: unmarshal job %s: %v", msg.ID, err)
		w.ackOptimize(ctx, msg.ID)
		return
	}

	log.Printf("optimizer: processing job %s signal=%s mode=%s", payload.JobID, payload.SignalID, payload.Mode)

	if err := w.runOptimizeJob(ctx, payload); err != nil {
		log.Printf("optimizer: job %s failed: %v", payload.JobID, err)
	}
	w.ackOptimize(ctx, msg.ID)
}

func (w *Worker) runOptimizeJob(ctx context.Context, payload OptimizeJobPayload) error {
	from, err := time.Parse(time.RFC3339, payload.PeriodFrom)
	if err != nil {
		return fmt.Errorf("parse period_from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, payload.PeriodTo)
	if err != nil {
		return fmt.Errorf("parse period_to: %w", err)
	}

	topN := payload.TopN
	if topN <= 0 {
		topN = 10
	}
	folds := payload.WFFolds
	if folds <= 0 {
		folds = 4
	}
	scoreBy := payload.ScoreBy
	if scoreBy == "" {
		scoreBy = "profit_factor"
	}
	tps := payload.TakeProfits
	if len(tps) == 0 {
		tps = []float64{2.0}
	}
	sls := payload.StopLosses
	if len(sls) == 0 {
		sls = []float64{1.0}
	}

	job := OptimizeJob{
		SignalID:           payload.SignalID,
		Symbol:             payload.Symbol,
		Market:             models.Market(payload.Market),
		Timeframe:          models.Timeframe(payload.Timeframe),
		Exchange:           models.Exchange(payload.Exchange),
		Direction:          payload.Direction,
		PeriodFrom:         from,
		PeriodTo:           to,
		Mode:               payload.Mode,
		ScoreBy:            scoreBy,
		TopN:               topN,
		TakeProfits:        tps,
		StopLosses:         sls,
		ConditionsTemplate: payload.ConditionsTemplate,
		ParamSpace:         payload.ParamSpace,
		WFFolds:            folds,
	}

	progressKey := fmt.Sprintf(optimizeProgressFmt, payload.JobID)
	progress := func(pct int) {
		w.rdb.HSet(ctx, progressKey, "pct", pct, "updated_at", time.Now().Unix())
	}
	progress(0)

	var result OptimizeResult
	switch payload.Mode {
	case "walk_forward":
		result, err = RunWalkForward(ctx, w.pool, job, progress)
	default:
		result, err = RunGridSearch(ctx, w.pool, job, progress)
	}
	if err != nil {
		return fmt.Errorf("run optimize: %w", err)
	}

	if err := w.saveOptimizeResult(ctx, payload, result); err != nil {
		return fmt.Errorf("save optimize result: %w", err)
	}

	w.rdb.HSet(ctx, progressKey, "pct", 100, "status", "done", "updated_at", time.Now().Unix())
	log.Printf("optimizer: job %s done — %d top combinations", payload.JobID, len(result.TopCombinations))
	return nil
}

func (w *Worker) saveOptimizeResult(ctx context.Context, payload OptimizeJobPayload, r OptimizeResult) error {
	topJSON, _ := json.Marshal(r.TopCombinations)
	bestJSON, _ := json.Marshal(r.BestParams)
	jobParamsJSON, _ := json.Marshal(payload)

	mode := payload.Mode
	if mode == "" {
		mode = "fast"
	}

	_, err := w.pool.Exec(ctx, `
		INSERT INTO optimization_results
			(signal_id, job_params, mode, top_combinations, best_params)
		VALUES ($1, $2, $3, $4, $5)`,
		payload.SignalID,
		jobParamsJSON,
		mode,
		topJSON,
		bestJSON,
	)
	return err
}

func (w *Worker) ackOptimize(ctx context.Context, msgID string) {
	if err := w.rdb.XAck(ctx, streamOptimize, optimizeConsumerGroup, msgID).Err(); err != nil {
		log.Printf("optimizer: ack error %s: %v", msgID, err)
	}
}
