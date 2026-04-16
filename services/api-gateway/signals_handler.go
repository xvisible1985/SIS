// services/api-gateway/signals_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type signalRow struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Exchange    string          `json:"exchange"`
	Symbol      string          `json:"symbol"`
	Market      string          `json:"market"`
	Timeframe   string          `json:"timeframe"`
	Direction   string          `json:"direction"`
	Conditions  json.RawMessage `json:"conditions"`
	IsActive    bool            `json:"is_active"`
	CreatedAt   time.Time       `json:"created_at"`
}

type createSignalRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Exchange    string          `json:"exchange"`
	Symbol      string          `json:"symbol"`
	Market      string          `json:"market"`
	Timeframe   string          `json:"timeframe"`
	Direction   string          `json:"direction"`
	Conditions  json.RawMessage `json:"conditions"`
}

// ListSignals returns all signals owned by the authenticated user.
// GET /signals
func (s *Server) ListSignals(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at
		 FROM signals WHERE owner_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	result := make([]signalRow, 0)
	for rows.Next() {
		var row signalRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
			&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
			&row.Conditions, &row.IsActive, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, row)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateSignal creates a new signal for the authenticated user.
// POST /signals
func (s *Server) CreateSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req createSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Exchange == "" || req.Symbol == "" || req.Market == "" || req.Timeframe == "" {
		writeError(w, http.StatusBadRequest, "name, exchange, symbol, market, timeframe are required")
		return
	}
	if req.Direction == "" {
		req.Direction = "LONG"
	}

	var row signalRow
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO signals (owner_id, name, description, exchange, symbol, market, timeframe, direction, conditions)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at`,
		userID, req.Name, req.Description, req.Exchange, req.Symbol, req.Market, req.Timeframe, req.Direction, req.Conditions,
	).Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
		&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
		&row.Conditions, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetSignal returns a single signal by ID (must be owned by caller).
// GET /signals/:id
func (s *Server) GetSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var row signalRow
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at
		 FROM signals WHERE id = $1 AND owner_id = $2`,
		sigID, userID,
	).Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
		&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
		&row.Conditions, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// UpdateSignal updates name, description, direction, conditions, is_active.
// PUT /signals/:id
func (s *Server) UpdateSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Direction   string          `json:"direction"`
		Conditions  json.RawMessage `json:"conditions"`
		IsActive    *bool           `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	_, err := s.pool.Exec(r.Context(),
		`UPDATE signals SET
			name        = COALESCE(NULLIF($3,''), name),
			description = COALESCE(NULLIF($4,''), description),
			direction   = COALESCE(NULLIF($5,''), direction),
			conditions  = COALESCE($6, conditions),
			is_active   = COALESCE($7, is_active)
		 WHERE id = $1 AND owner_id = $2`,
		sigID, userID, req.Name, req.Description, req.Direction, req.Conditions, req.IsActive,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	s.GetSignal(w, r)
}

// DeleteSignal deletes a signal owned by the caller.
// DELETE /signals/:id
func (s *Server) DeleteSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM signals WHERE id = $1 AND owner_id = $2`,
		sigID, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
