//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// createTestAccountLabeled is createTestAccount but with a caller-chosen label — needed
// whenever a test creates more than one account for the SAME user, since createTestAccount
// always uses the fixed label "test" and (owner_id, exchange, label) is unique.
func createTestAccountLabeled(t *testing.T, s *Server, userID, label string) string {
	t.Helper()
	s.encKey = testEncKey
	var id string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,'bybit',$2,'enc_key','enc_secret') RETURNING id`, userID, label,
	).Scan(&id); err != nil {
		t.Fatalf("createTestAccountLabeled: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM exchange_accounts WHERE id=$1", id) })
	return id
}

func seedTradeHistory(t *testing.T, s *Server, userID, accountID, symbol string, netPnl float64, closedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO trade_history (account_id, owner_id, symbol, category, direction, cycle_num, result, opened_at, closed_at, net_pnl)
		 VALUES ($1,$2,$3,'linear','long',1,'tp',$4,$4,$5) RETURNING id`,
		accountID, userID, symbol, closedAt, netPnl,
	).Scan(&id); err != nil {
		t.Fatalf("seed trade_history: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM trade_history WHERE id=$1", id) })
}

func getDashboardStats(t *testing.T, s *Server, userID string, params url.Values) dashboardResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard?"+params.Encode(), nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.GetDashboard(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetDashboard: got %d: %s", rec.Code, rec.Body.String())
	}
	var resp dashboardResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// TestGetDashboard_ScopesToAccountID is the regression for the bug found live
// (2026-08-19): a freshly connected account showed the previous account's trade stats,
// because the frontend never sent account_id and GetDashboard silently aggregated across
// every account the user owns when it was absent. With account_id present, only that
// account's trades must appear.
func TestGetDashboard_ScopesToAccountID(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dashscope1")
	accOld := createTestAccountLabeled(t, s, userID, "old")
	accNew := createTestAccountLabeled(t, s, userID, "new")

	seedTradeHistory(t, s, userID, accOld, "OLDUSDT", 10.0, time.Now().Add(-1*time.Hour))
	seedTradeHistory(t, s, userID, accNew, "NEWUSDT", 5.0, time.Now().Add(-1*time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}, "account_id": {accNew}})
	if resp.Stats.Total != 1 {
		t.Errorf("Total = %d, want 1 (only accNew's trade)", resp.Stats.Total)
	}
	if len(resp.RecentTrades) != 1 || resp.RecentTrades[0].Symbol != "NEWUSDT" {
		t.Errorf("RecentTrades = %+v, want exactly [NEWUSDT]", resp.RecentTrades)
	}
}

// TestGetDashboard_WithoutAccountID_AggregatesAllAccounts documents the pre-existing (and
// still intentional) fallback behavior when no account_id is given — an aggregate view
// across every account the user owns.
func TestGetDashboard_WithoutAccountID_AggregatesAllAccounts(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dashscope2")
	acc1 := createTestAccountLabeled(t, s, userID, "acc1")
	acc2 := createTestAccountLabeled(t, s, userID, "acc2")

	seedTradeHistory(t, s, userID, acc1, "AAAUSDT", 1.0, time.Now().Add(-1*time.Hour))
	seedTradeHistory(t, s, userID, acc2, "BBBUSDT", 2.0, time.Now().Add(-1*time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}})
	if resp.Stats.Total != 2 {
		t.Errorf("Total = %d, want 2 (aggregated across both accounts)", resp.Stats.Total)
	}
}

// TestClearAccountStats_HidesOlderTradesButNotNewerOnes is the regression for the
// non-destructive "Очистить статистику" button: trades before the marker disappear from
// the dashboard; trades after it (and the underlying trade_history rows themselves) must
// be unaffected.
func TestClearAccountStats_HidesOlderTradesButNotNewerOnes(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dashclear1")
	accID := createTestAccount(t, s, userID)

	seedTradeHistory(t, s, userID, accID, "BEFOREUSDT", 10.0, time.Now().Add(-2*time.Hour))

	// Clear stats now.
	req := httptest.NewRequest(http.MethodPatch, "/accounts/"+accID+"/clear-stats", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": accID})
	rec := httptest.NewRecorder()
	s.ClearAccountStats(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ClearAccountStats: got %d: %s", rec.Code, rec.Body.String())
	}

	// Read the marker back from the DB (Postgres' own NOW(), not Go's time.Now()) and place
	// the "after" trade relative to THAT — comparing a Go-clock timestamp against a
	// Postgres-clock marker is flaky under any clock skew between the test host and the DB
	// container, however small (found live: passed in isolation, failed intermittently as
	// part of the full suite).
	var clearedAt time.Time
	if err := s.pool.QueryRow(context.Background(),
		`SELECT stats_cleared_at FROM exchange_accounts WHERE id=$1`, accID,
	).Scan(&clearedAt); err != nil {
		t.Fatalf("read back stats_cleared_at: %v", err)
	}
	seedTradeHistory(t, s, userID, accID, "AFTERUSDT", 20.0, clearedAt.Add(time.Minute))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"all"}, "account_id": {accID}})
	if resp.Stats.Total != 1 {
		t.Fatalf("Total = %d, want 1 (only the trade after the clear marker)", resp.Stats.Total)
	}
	if resp.RecentTrades[0].Symbol != "AFTERUSDT" {
		t.Errorf("RecentTrades[0].Symbol = %q, want AFTERUSDT — BEFOREUSDT must be hidden, not the wrong one", resp.RecentTrades[0].Symbol)
	}

	// The underlying row must still exist — non-destructive.
	var stillExists bool
	if err := s.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM trade_history WHERE account_id=$1 AND symbol='BEFOREUSDT')`, accID,
	).Scan(&stillExists); err != nil {
		t.Fatalf("check row exists: %v", err)
	}
	if !stillExists {
		t.Error("BEFOREUSDT row was deleted — clear-stats must be non-destructive")
	}
}
