package main

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GetCoinIcon serves a cached coin icon PNG.
// GET /coin-icon/{symbol}  (no auth — public images)
func (s *Server) GetCoinIcon(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "symbol")
	base := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(raw, "usdt"), "usdc"), "usd"))
	if base == "" {
		http.NotFound(w, r)
		return
	}

	data, ct, err := s.coinIcons.Get(r.Context(), base)
	if err != nil || len(data) == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
