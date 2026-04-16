// services/api-gateway/jobs_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

type backtestRequest struct {
	PeriodFrom string  `json:"period_from"`
	PeriodTo   string  `json:"period_to"`
	TakeProfit float64 `json:"take_profit"`
	StopLoss   float64 `json:"stop_loss"`
}

type optimizeRequest struct {
	PeriodFrom  string               `json:"period_from"`
	PeriodTo    string               `json:"period_to"`
	Mode        string               `json:"mode"`
	ScoreBy     string               `json:"score_by"`
	TopN        int                  `json:"top_n"`
	TakeProfits []float64            `json:"take_profits"`
	StopLosses  []float64            `json:"stop_losses"`
	ParamSpace  map[string][]float64 `json:"param_space"`
	WFFolds     int                  `json:"wf_folds"`
}

// SubmitBacktest enqueues a backtest job onto the jobs:backtest Redis stream.
// POST /signals/:id/backtest
func (s *Server) SubmitBacktest(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exchange, symbol, market, timeframe, direction string
	var condJSON json.RawMessage
	err := s.pool.QueryRow(r.Context(),
		`SELECT exchange, symbol, market, timeframe, direction, conditions
		 FROM signals WHERE id=$1 AND owner_id=$2`,
		sigID, userID,
	).Scan(&exchange, &symbol, &market, &timeframe, &direction, &condJSON)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var req backtestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.TakeProfit <= 0 {
		req.TakeProfit = 2.0
	}
	if req.StopLoss <= 0 {
		req.StopLoss = 1.0
	}

	jobID := newUUID()
	payload := map[string]any{
		"job_id":      jobID,
		"signal_id":   sigID,
		"symbol":      symbol,
		"market":      market,
		"timeframe":   timeframe,
		"exchange":    exchange,
		"direction":   direction,
		"period_from": req.PeriodFrom,
		"period_to":   req.PeriodTo,
		"take_profit": req.TakeProfit,
		"stop_loss":   req.StopLoss,
		"conditions":  string(condJSON),
	}
	payloadJSON, _ := json.Marshal(payload)

	if err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{
		Stream: "jobs:backtest",
		Values: map[string]any{"payload": string(payloadJSON)},
	}).Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	progressKey := fmt.Sprintf("jobs:%s:progress", jobID)
	s.rdb.HSet(r.Context(), progressKey, "pct", 0, "status", "queued", "updated_at", time.Now().Unix())

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// SubmitOptimize enqueues an optimization job onto the jobs:optimize Redis stream.
// POST /signals/:id/optimize
func (s *Server) SubmitOptimize(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exchange, symbol, market, timeframe, direction string
	var condJSON json.RawMessage
	err := s.pool.QueryRow(r.Context(),
		`SELECT exchange, symbol, market, timeframe, direction, conditions
		 FROM signals WHERE id=$1 AND owner_id=$2`,
		sigID, userID,
	).Scan(&exchange, &symbol, &market, &timeframe, &direction, &condJSON)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var req optimizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Mode == "" {
		req.Mode = "fast"
	}
	if req.ScoreBy == "" {
		req.ScoreBy = "profit_factor"
	}
	if req.TopN <= 0 {
		req.TopN = 10
	}

	jobID := newUUID()
	payload := map[string]any{
		"job_id":              jobID,
		"signal_id":           sigID,
		"symbol":              symbol,
		"market":              market,
		"timeframe":           timeframe,
		"exchange":            exchange,
		"direction":           direction,
		"period_from":         req.PeriodFrom,
		"period_to":           req.PeriodTo,
		"mode":                req.Mode,
		"score_by":            req.ScoreBy,
		"top_n":               req.TopN,
		"take_profits":        req.TakeProfits,
		"stop_losses":         req.StopLosses,
		"param_space":         req.ParamSpace,
		"wf_folds":            req.WFFolds,
		"conditions_template": string(condJSON),
	}
	payloadJSON, _ := json.Marshal(payload)

	if err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{
		Stream: "jobs:optimize",
		Values: map[string]any{"payload": string(payloadJSON)},
	}).Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	progressKey := fmt.Sprintf("jobs:%s:optimize:progress", jobID)
	s.rdb.HSet(r.Context(), progressKey, "pct", 0, "status", "queued", "updated_at", time.Now().Unix())

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// GetBacktestResults returns all backtest results for a signal owned by the caller.
// GET /signals/:id/backtest-results
func (s *Server) GetBacktestResults(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		sigID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, symbol, timeframe, period_from, period_to, mode,
		        total_signals, win_count, loss_count, win_rate, avg_gain,
		        max_drawdown, profit_factor, patterns, created_at
		 FROM backtest_results WHERE signal_id=$1 ORDER BY created_at DESC LIMIT 50`,
		sigID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type resultRow struct {
		ID           string          `json:"id"`
		SignalID     string          `json:"signal_id"`
		Symbol       string          `json:"symbol"`
		Timeframe    string          `json:"timeframe"`
		PeriodFrom   time.Time       `json:"period_from"`
		PeriodTo     time.Time       `json:"period_to"`
		Mode         string          `json:"mode"`
		TotalSignals int             `json:"total_signals"`
		WinCount     int             `json:"win_count"`
		LossCount    int             `json:"loss_count"`
		WinRate      float64         `json:"win_rate"`
		AvgGain      float64         `json:"avg_gain"`
		MaxDrawdown  float64         `json:"max_drawdown"`
		ProfitFactor float64         `json:"profit_factor"`
		Patterns     json.RawMessage `json:"patterns"`
		CreatedAt    time.Time       `json:"created_at"`
	}

	results := make([]resultRow, 0)
	for rows.Next() {
		var row resultRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.Symbol, &row.Timeframe,
			&row.PeriodFrom, &row.PeriodTo, &row.Mode,
			&row.TotalSignals, &row.WinCount, &row.LossCount,
			&row.WinRate, &row.AvgGain, &row.MaxDrawdown, &row.ProfitFactor,
			&row.Patterns, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		results = append(results, row)
	}
	writeJSON(w, http.StatusOK, results)
}

// GetOptimizationResults returns all optimization results for a signal owned by the caller.
// GET /signals/:id/optimization-results
func (s *Server) GetOptimizationResults(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		sigID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, mode, top_combinations, best_params, created_at
		 FROM optimization_results WHERE signal_id=$1 ORDER BY created_at DESC LIMIT 50`,
		sigID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type optRow struct {
		ID              string          `json:"id"`
		SignalID        string          `json:"signal_id"`
		Mode            string          `json:"mode"`
		TopCombinations json.RawMessage `json:"top_combinations"`
		BestParams      json.RawMessage `json:"best_params"`
		CreatedAt       time.Time       `json:"created_at"`
	}

	results := make([]optRow, 0)
	for rows.Next() {
		var row optRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.Mode,
			&row.TopCombinations, &row.BestParams, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		results = append(results, row)
	}
	writeJSON(w, http.StatusOK, results)
}
