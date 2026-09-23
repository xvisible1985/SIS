//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func getDashboardRecentTrades(t *testing.T, s *Server, userID string, params url.Values) dashboardRecentTradesResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/recent-trades?"+params.Encode(), nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.GetDashboardRecentTrades(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetDashboardRecentTrades: got %d: %s", rec.Code, rec.Body.String())
	}
	var resp dashboardRecentTradesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// TestGetDashboardRecentTrades_PaginatesBeyondTen is the regression this endpoint exists for:
// the dashboard widget's own recent-trades query is hard-capped at LIMIT 10 (see
// GetDashboard's "4. Recent trades" section) — this endpoint must expose everything beyond
// that cap via limit/offset, ordered the same way (closed_at DESC), for the "Все последние
// сделки" page's infinite scroll.
func TestGetDashboardRecentTrades_PaginatesBeyondTen(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dashrt1")
	accID := createTestAccount(t, s, userID)

	// Seed 15 trades, oldest first, each a minute apart, so DESC order is deterministic and
	// distinguishable by symbol suffix.
	base := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 15; i++ {
		seedTradeHistory(t, s, userID, accID, "SYM"+string(rune('A'+i))+"USDT", float64(i), base.Add(time.Duration(i)*time.Minute))
	}

	page1 := getDashboardRecentTrades(t, s, userID, url.Values{"period": {"all"}, "account_id": {accID}, "limit": {"10"}, "offset": {"0"}})
	if len(page1.Trades) != 10 {
		t.Fatalf("page1 len = %d, want 10", len(page1.Trades))
	}
	if !page1.HasMore {
		t.Error("page1.HasMore = false, want true — 5 more trades remain")
	}
	// Newest first: index 14 (SYMOUSDT, closed last) must come before index 0.
	if page1.Trades[0].Symbol != "SYM"+string(rune('A'+14))+"USDT" {
		t.Errorf("page1.Trades[0].Symbol = %q, want the most recently closed trade first", page1.Trades[0].Symbol)
	}

	page2 := getDashboardRecentTrades(t, s, userID, url.Values{"period": {"all"}, "account_id": {accID}, "limit": {"10"}, "offset": {"10"}})
	if len(page2.Trades) != 5 {
		t.Fatalf("page2 len = %d, want 5 (the remaining trades)", len(page2.Trades))
	}
	if page2.HasMore {
		t.Error("page2.HasMore = true, want false — no more trades beyond page2")
	}

	// No overlap between the two pages.
	seen := map[string]bool{}
	for _, tr := range page1.Trades {
		seen[tr.ID] = true
	}
	for _, tr := range page2.Trades {
		if seen[tr.ID] {
			t.Errorf("trade %s appears on both page1 and page2 — offset pagination is broken", tr.ID)
		}
	}
}

// TestGetDashboardRecentTrades_ScopesToAccountAndPeriod confirms this endpoint applies the
// exact same account_id + period(since) scoping as the dashboard widget it expands
// (buildDashboardBaseFilter/dashboardPeriodSince are shared with GetDashboard) — a trade
// outside the selected account or before the period cutoff must not appear.
func TestGetDashboardRecentTrades_ScopesToAccountAndPeriod(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "dashrt2")
	accIn := createTestAccountLabeled(t, s, userID, "in")
	accOut := createTestAccountLabeled(t, s, userID, "out")

	seedTradeHistory(t, s, userID, accIn, "INSIDEUSDT", 1.0, time.Now().Add(-1*time.Hour))
	seedTradeHistory(t, s, userID, accIn, "TOOOLDUSDT", 2.0, time.Now().AddDate(0, 0, -40))
	seedTradeHistory(t, s, userID, accOut, "OTHERACCUSDT", 3.0, time.Now().Add(-1*time.Hour))

	resp := getDashboardRecentTrades(t, s, userID, url.Values{"period": {"30d"}, "account_id": {accIn}, "limit": {"10"}})
	if len(resp.Trades) != 1 || resp.Trades[0].Symbol != "INSIDEUSDT" {
		t.Errorf("Trades = %+v, want exactly [INSIDEUSDT] (accOut and the 40-day-old trade must be excluded)", resp.Trades)
	}
}
