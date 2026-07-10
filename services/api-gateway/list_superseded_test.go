//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// listStrategyStatuses calls ListStrategies for userID and returns id→status of the rows returned.
func listStrategyStatuses(t *testing.T, s *Server, userID string) map[string]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/strategies", nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.ListStrategies(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListStrategies: got %d: %s", rec.Code, rec.Body.String())
	}
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.ID] = r.Status
	}
	return out
}

// TestListStrategies_HidesSupersededStoppedCard: a 'stopped' strategy for a slot
// (account,symbol,direction) that ALSO has a NEWER active/finishing strategy is a
// duplicate "ghost card" left by bot recreation — it must be hidden from the list.
// A stopped strategy with no newer live sibling, and the newer active one, stay visible.
func TestListStrategies_HidesSupersededStoppedCard(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "supers")
	accID := createTestAccount(t, s, userID)

	ins := func(symbol, dir, status string, createdAt time.Time) string {
		var id string
		if err := s.pool.QueryRow(ctx,
			`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status, created_at)
			 VALUES ($1,$2,$3,$4,'matrix',$5,$6) RETURNING id`,
			userID, accID, symbol, dir, status, createdAt,
		).Scan(&id); err != nil {
			t.Fatalf("insert %s %s: %v", symbol, dir, err)
		}
		t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", id) })
		return id
	}

	now := time.Now()
	oldStopped := ins("SUPUSDT", "long", "stopped", now.Add(-24*time.Hour)) // superseded → hidden
	newActive := ins("SUPUSDT", "long", "active", now)                      // supersedes → shown
	loneStopped := ins("LONEUSDT", "short", "stopped", now)                 // no newer sibling → shown

	ids := listStrategyStatuses(t, s, userID)

	if _, ok := ids[oldStopped]; ok {
		t.Errorf("superseded stopped card %s should be hidden", oldStopped[:8])
	}
	if _, ok := ids[newActive]; !ok {
		t.Errorf("newer active card %s should be shown", newActive[:8])
	}
	if _, ok := ids[loneStopped]; !ok {
		t.Errorf("lone stopped card %s (no newer active) should be shown", loneStopped[:8])
	}
}
