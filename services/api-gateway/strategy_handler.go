package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type strategyPayload struct {
	AccountID    string  `json:"account_id"`
	Symbol       string  `json:"symbol"`
	Category     string  `json:"category"`
	Direction    string  `json:"direction"`
	GridLevels   int     `json:"grid_levels"`
	GridActive   int     `json:"grid_active"`
	GridStepPct  float64 `json:"grid_step_pct"`
	GridSizeUSDT float64 `json:"grid_size_usdt"`
	TPMode       string  `json:"tp_mode"`
	TPPct        float64 `json:"tp_pct"`
	SLType       string  `json:"sl_type"`
	SLPct        float64 `json:"sl_pct"`
	SignalFilter bool    `json:"signal_filter"`
}

// ListStrategies returns all strategies for the authenticated user.
// GET /strategies
func (s *Server) ListStrategies(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, account_id, symbol, category, direction, status,
		        grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		        tp_mode, tp_pct, sl_type, sl_pct, signal_filter, created_at, updated_at
		 FROM strategies WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		ID           string    `json:"id"`
		AccountID    string    `json:"account_id"`
		Symbol       string    `json:"symbol"`
		Category     string    `json:"category"`
		Direction    string    `json:"direction"`
		Status       string    `json:"status"`
		GridLevels   int       `json:"grid_levels"`
		GridActive   int       `json:"grid_active"`
		GridStepPct  float64   `json:"grid_step_pct"`
		GridSizeUSDT float64   `json:"grid_size_usdt"`
		TPMode       string    `json:"tp_mode"`
		TPPct        float64   `json:"tp_pct"`
		SLType       string    `json:"sl_type"`
		SLPct        float64   `json:"sl_pct"`
		SignalFilter bool      `json:"signal_filter"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	var result []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Symbol, &r.Category, &r.Direction, &r.Status,
			&r.GridLevels, &r.GridActive, &r.GridStepPct, &r.GridSizeUSDT,
			&r.TPMode, &r.TPPct, &r.SLType, &r.SLPct, &r.SignalFilter, &r.CreatedAt, &r.UpdatedAt,
		); err == nil {
			result = append(result, r)
		}
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateStrategy creates a new strategy (status=stopped by default).
// POST /strategies
func (s *Server) CreateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Symbol == "" {
		writeError(w, http.StatusBadRequest, "account_id and symbol are required")
		return
	}
	if req.Category == "" {
		req.Category = "linear"
	}
	if req.Direction == "" {
		req.Direction = "long"
	}
	if req.GridLevels == 0 {
		req.GridLevels = 5
	}
	if req.GridActive == 0 {
		req.GridActive = 3
	}
	if req.TPMode == "" {
		req.TPMode = "total"
	}
	if req.SLType == "" {
		req.SLType = "conditional"
	}
	var id string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO strategies
		 (owner_id, account_id, symbol, category, direction,
		  grid_levels, grid_active, grid_step_pct, grid_size_usdt,
		  tp_mode, tp_pct, sl_type, sl_pct, signal_filter)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 RETURNING id`,
		userID, req.AccountID, req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
	).Scan(&id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// UpdateStrategy updates strategy parameters (not status).
// PUT /strategies/{id}
func (s *Server) UpdateStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req strategyPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET
		  symbol=$1, category=$2, direction=$3,
		  grid_levels=$4, grid_active=$5, grid_step_pct=$6, grid_size_usdt=$7,
		  tp_mode=$8, tp_pct=$9, sl_type=$10, sl_pct=$11, signal_filter=$12,
		  updated_at=NOW()
		 WHERE id=$13 AND owner_id=$14`,
		req.Symbol, req.Category, req.Direction,
		req.GridLevels, req.GridActive, req.GridStepPct, req.GridSizeUSDT,
		req.TPMode, req.TPPct, req.SLType, req.SLPct, req.SignalFilter,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SetStrategyStatus changes strategy status: active | finishing | stopped.
// POST /strategies/{id}/status
func (s *Server) SetStrategyStatus(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Status != "active" && req.Status != "finishing" && req.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "status must be active | finishing | stopped")
		return
	}
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE strategies SET status=$1, updated_at=NOW() WHERE id=$2 AND owner_id=$3`,
		req.Status, id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	s.engine.Notify(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteStrategy deletes a strategy (only if stopped).
// DELETE /strategies/{id}
func (s *Server) DeleteStrategy(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	id := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM strategies WHERE id=$1 AND owner_id=$2 AND status='stopped'`,
		id, userID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusBadRequest, "strategy not found or not stopped")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
