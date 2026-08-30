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

func getTradeHistoryResp(t *testing.T, s *Server, userID string, params url.Values) tradeHistoryResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/trade-history?"+params.Encode(), nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.GetTradeHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetTradeHistory: got %d: %s", rec.Code, rec.Body.String())
	}
	var resp tradeHistoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// TestClearAccountStats_HidesOlderTradesFromHistoryButNotNewerOnes is the regression for
// extending the non-destructive "Очистить статистику" clamp (already covered on the
// Dashboard, see dashboard_handler_test.go) to the Trade History page: trades before the
// exchange_accounts.stats_cleared_at marker must disappear from GetTradeHistory's stats,
// count and rows alike, while trades after it — and the underlying trade_history rows
// themselves — stay untouched.
func TestClearAccountStats_HidesOlderTradesFromHistoryButNotNewerOnes(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "thclear1")
	accID := createTestAccount(t, s, userID)

	seedTradeHistory(t, s, userID, accID, "BEFOREUSDT", 10.0, time.Now().Add(-2*time.Hour))

	req := httptest.NewRequest(http.MethodPatch, "/accounts/"+accID+"/clear-stats", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": accID})
	rec := httptest.NewRecorder()
	s.ClearAccountStats(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ClearAccountStats: got %d: %s", rec.Code, rec.Body.String())
	}

	var clearedAt time.Time
	if err := s.pool.QueryRow(context.Background(),
		`SELECT stats_cleared_at FROM exchange_accounts WHERE id=$1`, accID,
	).Scan(&clearedAt); err != nil {
		t.Fatalf("read back stats_cleared_at: %v", err)
	}
	seedTradeHistory(t, s, userID, accID, "AFTERUSDT", 20.0, clearedAt.Add(time.Minute))

	resp := getTradeHistoryResp(t, s, userID, url.Values{"account_id": {accID}})
	if resp.Total != 1 {
		t.Fatalf("Total = %d, want 1 (only the trade after the clear marker)", resp.Total)
	}
	if len(resp.Trades) != 1 || resp.Trades[0].Symbol != "AFTERUSDT" {
		t.Errorf("Trades = %+v, want exactly [AFTERUSDT]", resp.Trades)
	}
	if resp.Stats.Total != 1 {
		t.Errorf("Stats.Total = %d, want 1", resp.Stats.Total)
	}

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

// TestGetTradeHistory_WithoutClearMarker_ShowsAllTrades documents the neighboring scenario
// that must stay unaffected by the clamp: an account that never had its stats cleared keeps
// showing its full trade history.
func TestGetTradeHistory_WithoutClearMarker_ShowsAllTrades(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "thclear2")
	accID := createTestAccount(t, s, userID)

	seedTradeHistory(t, s, userID, accID, "OLDUSDT", 1.0, time.Now().Add(-48*time.Hour))
	seedTradeHistory(t, s, userID, accID, "NEWUSDT", 2.0, time.Now().Add(-1*time.Hour))

	resp := getTradeHistoryResp(t, s, userID, url.Values{"account_id": {accID}})
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2 (no clear marker set)", resp.Total)
	}
}

// TestGetTradeHistorySymbols_ScopesToAccountID is the regression for adding an optional
// account_id filter to the symbols endpoint, which backs the Trade History page's symbol
// dropdown — now that the page is pinned to the sidebar-selected account, the dropdown must
// only offer symbols traded on that account, not every account the user owns.
func TestGetTradeHistorySymbols_ScopesToAccountID(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "thsymbols1")
	accA := createTestAccountLabeled(t, s, userID, "a")
	accB := createTestAccountLabeled(t, s, userID, "b")

	seedTradeHistory(t, s, userID, accA, "AAAUSDT", 1.0, time.Now().Add(-1*time.Hour))
	seedTradeHistory(t, s, userID, accB, "BBBUSDT", 2.0, time.Now().Add(-1*time.Hour))

	getSymbols := func(params url.Values) []string {
		req := httptest.NewRequest(http.MethodGet, "/trade-history/symbols?"+params.Encode(), nil)
		req = withUserID(req, userID)
		rec := httptest.NewRecorder()
		s.GetTradeHistorySymbols(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GetTradeHistorySymbols: got %d: %s", rec.Code, rec.Body.String())
		}
		var symbols []string
		if err := json.NewDecoder(rec.Body).Decode(&symbols); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return symbols
	}

	scoped := getSymbols(url.Values{"account_id": {accA}})
	if len(scoped) != 1 || scoped[0] != "AAAUSDT" {
		t.Errorf("scoped symbols = %v, want exactly [AAAUSDT]", scoped)
	}

	all := getSymbols(url.Values{})
	if len(all) != 2 {
		t.Errorf("unscoped symbols = %v, want both AAAUSDT and BBBUSDT (unchanged fallback behavior)", all)
	}
}
