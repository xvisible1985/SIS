package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GetStrategyDefaults returns all strategy defaults.
// GET /strategy-defaults (all auth users) and GET /admin/strategy-defaults (admin)
func (s *Server) GetStrategyDefaults(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(),
		`SELECT strategy_type, config FROM strategy_defaults ORDER BY strategy_type`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	result := map[string]json.RawMessage{}
	for rows.Next() {
		var stratType string
		var config json.RawMessage
		if err := rows.Scan(&stratType, &config); err != nil {
			continue
		}
		result[stratType] = config
	}
	writeJSON(w, http.StatusOK, result)
}

// UpdateStrategyDefaults sets defaults for a specific strategy type.
// PUT /admin/strategy-defaults/{type}
func (s *Server) UpdateStrategyDefaults(w http.ResponseWriter, r *http.Request) {
	stratType := chi.URLParam(r, "type")
	if stratType != "grid" && stratType != "matrix" {
		writeError(w, http.StatusBadRequest, "invalid strategy type: must be 'grid' or 'matrix'")
		return
	}
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO strategy_defaults (strategy_type, config, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (strategy_type) DO UPDATE SET config = $2, updated_at = NOW()`,
		stratType, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
