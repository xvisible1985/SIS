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

func seedBalanceSnapshot(t *testing.T, s *Server, accountID string, equity float64, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO balance_snapshots (account_id, equity, created_at) VALUES ($1,$2,$3) RETURNING id`,
		accountID, equity, createdAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed balance_snapshots: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM balance_snapshots WHERE id=$1", id) })
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

// TestGetDashboard_EquitySeries_DoesNotLeakOtherUsersAccountData is the regression for a
// cross-tenant IDOR: balance_snapshots has no owner_id of its own — ownership only exists
// via exchange_accounts.owner_id — so the equity_series query must join against
// exchange_accounts and check owner_id, exactly like every other section of GetDashboard
// (trade_history queries AND th.owner_id = $1; the stats_cleared_at lookup does
// WHERE id=$1 AND owner_id=$2). Without that check, any authenticated user could pass
// another user's account_id and receive that other user's equity/balance history.
//
// The owner-side assertion below is not incidental: without it, this test would still pass
// "green" even if equity_series broke entirely for everyone (e.g. a regression that makes
// the join always return zero rows) — it must positively confirm the legitimate owner still
// gets their own data back, not just that the attacker gets nothing.
func TestGetDashboard_EquitySeries_DoesNotLeakOtherUsersAccountData(t *testing.T) {
	s := newTestServer(t)
	owner := createWHUser(t, s, "dasheq_owner")
	attacker := createWHUser(t, s, "dasheq_attacker")
	victimAcc := createTestAccountLabeled(t, s, owner, "victim")

	seedBalanceSnapshot(t, s, victimAcc, 1000.0, time.Now().Add(-1*time.Hour))

	resp := getDashboardStats(t, s, attacker, url.Values{"period": {"30d"}, "account_id": {victimAcc}})
	if len(resp.EquitySeries) != 0 {
		t.Errorf("EquitySeries = %+v, want empty — attacker must not see another user's account balance history", resp.EquitySeries)
	}

	ownerResp := getDashboardStats(t, s, owner, url.Values{"period": {"30d"}, "account_id": {victimAcc}})
	if len(ownerResp.EquitySeries) != 1 {
		t.Fatalf("EquitySeries = %+v, want exactly 1 point (the owner querying their own account)", ownerResp.EquitySeries)
	}
	if ownerResp.EquitySeries[0].Equity != 1000.0 {
		t.Errorf("EquitySeries[0].Equity = %v, want 1000.0 (the seeded snapshot)", ownerResp.EquitySeries[0].Equity)
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

// TestGetDashboard_EquitySeries_BucketsByDayUsingLastSnapshot is the regression for the
// equity_series aggregation: two snapshots on the same day must collapse into one bucket,
// keeping the LATEST snapshot in that bucket (equity "as of end of bucket"), not an
// average or the first one.
func TestGetDashboard_EquitySeries_BucketsByDayUsingLastSnapshot(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dasheq3")
	accID := createTestAccount(t, s, userID)

	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	seedBalanceSnapshot(t, s, accID, 100.0, day.Add(9*time.Hour))
	seedBalanceSnapshot(t, s, accID, 105.5, day.Add(15*time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}, "account_id": {accID}})
	if len(resp.EquitySeries) != 1 {
		t.Fatalf("EquitySeries = %+v, want exactly 1 bucket (both snapshots fall on the same day)", resp.EquitySeries)
	}
	if resp.EquitySeries[0].Equity != 105.5 {
		t.Errorf("Equity = %v, want 105.5 (the later of the two same-day snapshots)", resp.EquitySeries[0].Equity)
	}
}

// TestGetDashboard_EquitySeries_EmptyWithoutAccountID documents the intentional limitation:
// balance_snapshots is per-account, so there is no equity series to show in the "all
// accounts" aggregate view — the field must come back empty, not an error, so the frontend
// can fall back to the old cumulative P&L chart.
func TestGetDashboard_EquitySeries_EmptyWithoutAccountID(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dasheq4")
	accID := createTestAccount(t, s, userID)
	seedBalanceSnapshot(t, s, accID, 100.0, time.Now().Add(-time.Hour))

	resp := getDashboardStats(t, s, userID, url.Values{"period": {"30d"}})
	if len(resp.EquitySeries) != 0 {
		t.Errorf("EquitySeries = %+v, want empty when no account_id is given (aggregate view)", resp.EquitySeries)
	}
}
