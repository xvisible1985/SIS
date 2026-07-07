//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetStrategyState_EndedCycleReturnsNoLevels verifies that once a cycle has ended,
// GetStrategyState returns no levels — so the chart stops drawing stale "taken order"
// bars for a stopped strategy.
func TestGetStrategyState_EndedCycleReturnsNoLevels(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "endedlvl")
	accID := createTestAccount(t, s, userID)

	var stratID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, status)
		 VALUES ($1,$2,'ENDUSDT','short','grid','stopped') RETURNING id`, userID, accID).Scan(&stratID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID string
	s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at, ended_at, result)
		 VALUES ($1,1,NOW()-INTERVAL '1 hour', NOW()-INTERVAL '5 minutes','tp') RETURNING id`, stratID).Scan(&cycleID)
	// A filled level that must NOT be returned once the cycle is ended.
	s.pool.Exec(ctx,
		`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot, filled_price)
		 VALUES ($1,$2,1,'Sell',0.05,10,'200','filled',0,0.05)`, stratID, cycleID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/state", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	s.GetStrategyState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetStrategyState: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Levels []json.RawMessage `json:"levels"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Levels) != 0 {
		t.Errorf("ended cycle returned %d levels, want 0 (bars must clear)", len(resp.Levels))
	}
}
