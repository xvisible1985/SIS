//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetClosedPnl_UnknownAccount_Returns404 locks in the one part of GetClosedPnl's
// exchange dispatch (Plan #4b/#4c) that's cheaply testable without live network access:
// both branches (bybit's cursor-paginated endpoint, binance's FetchRecentClosedPnl) sit
// behind the SAME account/exchange lookup, so an account that doesn't belong to the
// caller must 404 before either branch is ever reached — regardless of what exchange
// column that (nonexistent, for this owner) row would have had.
//
// The dispatch's two live branches themselves aren't unit-tested here: both
// pkg/trader's bybitBase and pkg/trader/binance's binanceBase (the vars that let their
// own packages' tests point at an httptest mock) are package-private, so this handler's
// test can't redirect either exchange's real network calls — a pre-existing testability
// gap this change doesn't introduce. The underlying logic each branch delegates to
// (binance.BinanceExchange.FetchRecentClosedPnl) has its own direct, network-mocked
// test coverage in pkg/trader/binance/exchange_test.go.
func TestGetClosedPnl_UnknownAccount_Returns404(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "pnltab")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/trader/pnl?account_id=00000000-0000-0000-0000-000000000000&category=linear", nil)
	req = withUserID(req, userID)
	s.GetClosedPnl(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d: %s, want 404", rec.Code, rec.Body.String())
	}
}
