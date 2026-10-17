package main

import (
	"context"
	"net/http"

	"sis/pkg/trader"
	"sis/pkg/trader/binance"
)

// bybitGetPublicInstrumentInfo/binanceGetPublicInstrumentInfo are vars (not direct calls)
// so getPublicInstrumentInfoFor_test.go can stub them out and assert on dispatch alone,
// without reaching the real network — the same pattern pkg/trader/bybit.go's bybitBase
// and pkg/trader/binance/client.go's binanceBase use for httptest overrides, one level
// up since trader.GetPublicInstrumentInfo/binance.GetPublicInstrumentInfo are functions,
// not a base-URL string, to point at a mock server.
var (
	bybitGetPublicInstrumentInfo   = trader.GetPublicInstrumentInfo
	binanceGetPublicInstrumentInfo = binance.GetPublicInstrumentInfo
)

// getPublicInstrumentInfoFor dispatches to the right exchange's public instrument-info
// lookup — see trader.Exchange's GetInstrumentInfo doc comment for why this dispatch
// matters: a Binance account's qty/price must be rounded against Binance's own
// precision rules, not Bybit's, or the exchange rejects the order outright.
//
// This is the one place pkg/trader (Bybit) and pkg/trader/binance's public,
// account-agnostic instrument-info functions are chosen between — kept in
// services/api-gateway (rather than pkg/trader) because pkg/trader/binance already
// imports pkg/trader, so pkg/trader can't import pkg/trader/binance back without a
// cycle. Used by both the /instrument-info HTTP handler and bot_engine.go's
// grid_size_usdt auto-raise logic.
func getPublicInstrumentInfoFor(ctx context.Context, exchangeName, category, symbol string) (trader.PubInstrumentInfo, error) {
	if exchangeName == "binance" {
		return binanceGetPublicInstrumentInfo(ctx, category, symbol)
	}
	return bybitGetPublicInstrumentInfo(ctx, category, symbol)
}

// GetInstrumentConstraints returns leverage and lot-size limits for a symbol.
// GET /instrument-info?symbol=BTCUSDT&category=linear&exchange=bybit|binance
//
// exchange defaults to "bybit" for backward compatibility with callers that don't know
// or care about a specific account (e.g. a generic symbol picker) — pass it explicitly
// whenever the lookup is for a specific account's order, so precision is rounded
// against the exchange that will actually receive the order.
//
// max_leverage is served from the DB cache (refreshed every 10 min by RunLeverageRefresher) —
// that cache is Bybit-only (see leverage_cache.go), so it's skipped for Binance, whose
// leverage is a per-notional-bracket schedule rather than a single scalar.
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
	exchangeName := r.URL.Query().Get("exchange")
	if exchangeName == "" {
		exchangeName = "bybit"
	}

	info, err := getPublicInstrumentInfoFor(r.Context(), exchangeName, category, symbol)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if exchangeName == "bybit" {
		// Prefer the DB-cached max_leverage (kept fresh by the background refresher).
		// Fall back to what Bybit returned if the DB has no row yet.
		if dbLev := getMaxLeverageFromDB(r.Context(), s.pool, symbol, category); dbLev > 0 {
			info.MaxLeverage = float64(dbLev)
		} else {
			// First request for this symbol: persist immediately so future calls use DB.
			go upsertMaxLeverage(r.Context(), s.pool, symbol, category, int(info.MaxLeverage))
		}
	}

	writeJSON(w, http.StatusOK, info)
}
