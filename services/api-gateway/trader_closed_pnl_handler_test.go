//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// TestLoadOrderLinkIDMap_ReturnsOwnOrdersWithinWindow_ExcludesOlderAndOtherAccounts is
// the regression test for the P&L tab's slow-load fix (2026-10-17): instead of one REST
// round-trip to Binance per closing order, GetClosedPnl now preloads this account's own
// order_id -> order_link_id mapping in one bulk query. This pins that the query: (a)
// only returns rows for the requested account, (b) only returns rows created at or
// after `since`, and (c) correctly maps each order_id (stored as TEXT) to its
// order_link_id.
func TestLoadOrderLinkIDMap_ReturnsOwnOrdersWithinWindow_ExcludesOlderAndOtherAccounts(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	var ownerID, accID, otherAccID string
	if err := s.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1,'x') RETURNING id`,
		"pnlcache-"+t.Name()+"@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", ownerID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','x','','') RETURNING id`,
		ownerID).Scan(&accID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", accID) })
	if err := s.pool.QueryRow(ctx, `INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc) VALUES ($1,'binance','other','','') RETURNING id`,
		ownerID).Scan(&otherAccID); err != nil {
		t.Fatalf("create other account: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM exchange_accounts WHERE id=$1", otherAccID) })

	since := time.Now().Add(-24 * time.Hour)
	insertOrder := func(accountID, orderID, orderLinkID string, createdAt time.Time) {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO trader_orders (owner_id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type, qty, status, created_at, updated_at)
			VALUES ($1,$2,$3,$4,'binance','BTCUSDT','linear','Buy','Limit',1,'Filled',$5,$5)`,
			ownerID, accountID, orderLinkID, orderID, createdAt); err != nil {
			t.Fatalf("insert order %s: %v", orderID, err)
		}
	}
	insertOrder(accID, "100", "SIS_STR-in-window", since.Add(time.Hour))     // within window, right account
	insertOrder(accID, "200", "SIS_STR-too-old", since.Add(-time.Hour))      // before `since` — must be excluded
	insertOrder(otherAccID, "300", "SIS_STR-other-account", since.Add(time.Hour)) // different account — must be excluded
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trader_orders WHERE account_id IN ($1,$2)", accID, otherAccID) })

	got, err := s.loadOrderLinkIDMap(ctx, accID, since)
	if err != nil {
		t.Fatalf("loadOrderLinkIDMap: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(got), got)
	}
	if got[100] != "SIS_STR-in-window" {
		t.Errorf("got[100] = %q, want SIS_STR-in-window", got[100])
	}
	if _, ok := got[200]; ok {
		t.Error("order 200 (before `since`) must be excluded")
	}
	if _, ok := got[300]; ok {
		t.Error("order 300 (different account) must be excluded")
	}
}
