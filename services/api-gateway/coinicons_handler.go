package main

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// GetCoinIcon serves a cached coin icon PNG.
// GET /coin-icon/{symbol}  (no auth — public images)
func (s *Server) GetCoinIcon(w http.ResponseWriter, r *http.Request) {
	raw := chi.URLParam(r, "symbol")
	base := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(raw, "usdt"), "usdc"), "usd"))
	if base == "" {
		serveCoinPlaceholder(w, "?")
		return
	}

	data, ct, err := s.coinIcons.Get(r.Context(), base)
	if err != nil || len(data) == 0 {
		serveCoinPlaceholder(w, base)
		return
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

// serveCoinPlaceholder renders a simple SVG circle with the first letter of the symbol.
func serveCoinPlaceholder(w http.ResponseWriter, base string) {
	letter := "?"
	if base != "" {
		r, _ := utf8.DecodeRuneInString(base)
		letter = strings.ToUpper(string(r))
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32">
  <circle cx="16" cy="16" r="16" fill="#374151"/>
  <text x="16" y="21" text-anchor="middle" font-family="sans-serif" font-size="14" font-weight="bold" fill="#9CA3AF">%s</text>
</svg>`, letter)

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, svg)
}
