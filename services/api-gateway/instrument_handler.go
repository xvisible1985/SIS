package main

import (
	"net/http"

	"sis/pkg/trader"
)

// GetInstrumentConstraints returns leverage and lot-size limits for a symbol.
// GET /instrument-info?symbol=BTCUSDT&category=linear
func (s *Server) GetInstrumentConstraints(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, "symbol required")
		return
	}
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "linear"
	}

	info, err := trader.GetPublicInstrumentInfo(r.Context(), category, symbol)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}
