package main

import (
	"encoding/json"
	"net/http"
)

type coinFilterSettings struct {
	MinTurnoverUsdt float64  `json:"min_turnover_usdt"`
	Blacklist       []string `json:"blacklist"`
}

// GetCoinFilter returns coin filter settings.
// GET /coin-filter (all authenticated users)
func (s *Server) GetCoinFilter(w http.ResponseWriter, r *http.Request) {
	var cfg coinFilterSettings
	err := s.pool.QueryRow(r.Context(),
		`SELECT min_turnover_usdt, blacklist FROM coin_filter_settings WHERE id = 1`,
	).Scan(&cfg.MinTurnoverUsdt, &cfg.Blacklist)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.Blacklist == nil {
		cfg.Blacklist = []string{}
	}
	writeJSON(w, http.StatusOK, cfg)
}

// UpdateCoinFilter replaces coin filter settings.
// PUT /admin/coin-filter (admin only)
func (s *Server) UpdateCoinFilter(w http.ResponseWriter, r *http.Request) {
	var body coinFilterSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Blacklist == nil {
		body.Blacklist = []string{}
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE coin_filter_settings
		 SET min_turnover_usdt = $1, blacklist = $2, updated_at = NOW()
		 WHERE id = 1`,
		body.MinTurnoverUsdt, body.Blacklist,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
