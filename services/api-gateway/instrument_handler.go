package main

import (
	"net/http"

	"sis/pkg/trader"
)

// GetInstrumentConstraints returns leverage and lot-size limits for a symbol.
// GET /instrument-info?symbol=BTCUSDT&category=linear
//
// max_leverage is served from the DB cache (refreshed every 10 min by RunLeverageRefresher).
// On a cache miss it falls through to a live Bybit fetch and persists the result.
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

	// Prefer the DB-cached max_leverage (kept fresh by the background refresher).
	// Fall back to what Bybit returned if the DB has no row yet.
	if dbLev := getMaxLeverageFromDB(r.Context(), s.pool, symbol, category); dbLev > 0 {
		info.MaxLeverage = float64(dbLev)
	} else {
		// First request for this symbol: persist immediately so future calls use DB.
		go upsertMaxLeverage(r.Context(), s.pool, symbol, category, int(info.MaxLeverage))
	}

	writeJSON(w, http.StatusOK, info)
}
