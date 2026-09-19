//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDeleteStrategy_ClearsDanglingHedgedStrategyRef reproduces the bug found live
// 2026-09-18: strategies.hedged_strategy_id REFERENCES strategies(id) with no ON DELETE
// clause (migration 056), so Postgres rejects the DELETE with a foreign key violation
// whenever another strategy's hedged_strategy_id still points at the one being removed.
// DeleteStrategy surfaced that as a raw "db error" to the user with no way to resolve it
// from the UI. It must instead clear the dangling reference first, then delete cleanly.
func TestDeleteStrategy_ClearsDanglingHedgedStrategyRef(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "delhedgeref")
	accID := createTestAccount(t, s, userID)

	var mainID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, strategy_type, status)
		 VALUES ($1,$2,'ADAUSDT','linear','long','grid','finishing') RETURNING id`,
		userID, accID,
	).Scan(&mainID); err != nil {
		t.Fatalf("seed main strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", mainID) })

	var hedgeID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, category, direction, strategy_type, status, hedged_strategy_id)
		 VALUES ($1,$2,'ADAUSDT','linear','short','grid','stopped',$3) RETURNING id`,
		userID, accID, mainID,
	).Scan(&hedgeID); err != nil {
		t.Fatalf("seed hedge strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", hedgeID) })

	rec := httptest.NewRequest(http.MethodDelete, "/strategies/"+mainID, nil)
	rec = withUserID(rec, userID)
	rec = withChiParams(rec, map[string]string{"id": mainID})
	w := httptest.NewRecorder()
	s.DeleteStrategy(w, rec)

	if w.Code != http.StatusOK {
		t.Fatalf("DeleteStrategy: got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM strategies WHERE id=$1`, mainID).Scan(&count); err != nil {
		t.Fatalf("query main strategy: %v", err)
	}
	if count != 0 {
		t.Errorf("main strategy count = %d, want 0 — must actually be deleted", count)
	}

	var hedgeRefCleared bool
	if err := s.pool.QueryRow(ctx,
		`SELECT hedged_strategy_id IS NULL FROM strategies WHERE id=$1`, hedgeID,
	).Scan(&hedgeRefCleared); err != nil {
		t.Fatalf("query hedge strategy: %v", err)
	}
	if !hedgeRefCleared {
		t.Error("hedge strategy's hedged_strategy_id was not cleared — the row itself must survive with the dangling reference nulled out")
	}
}
