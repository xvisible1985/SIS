//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRelativeSlotLabels seeds a matrix strategy with L(0), L(-1), L(-2) filled and
// asserts GetStrategyState reports relative_slot -1 for the closest accumulation slot
// to entry and -2 for the next (Novabot relative_slots renumbering).
func TestRelativeSlotLabels(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "relslots")
	accID := createTestAccount(t, s, userID)

	var stratID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategies (owner_id, account_id, symbol, direction, strategy_type, relative_slots)
		 VALUES ($1,$2,'RSUSDT','long','matrix',true) RETURNING id`, userID, accID,
	).Scan(&stratID); err != nil {
		t.Fatalf("insert strategy: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", stratID) })

	var cycleID string
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at) VALUES ($1,1,NOW()) RETURNING id`, stratID,
	).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}

	ins := func(slot int, price float64) {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status, slot, filled_price)
			 VALUES ($1,$2,$3,'Buy',$4,10,'1','filled',$5,$4)`,
			stratID, cycleID, slot+10, price, slot); err != nil {
			t.Fatalf("insert level slot=%d: %v", slot, err)
		}
	}
	ins(0, 100) // entry
	ins(-1, 98) // closest accumulation → relative -1
	ins(-2, 95) // next → relative -2

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/strategies/"+stratID+"/state", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": stratID})
	s.GetStrategyState(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetStrategyState: %d %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Levels []struct {
			Slot         *int `json:"slot"`
			RelativeSlot int  `json:"relative_slot"`
		} `json:"levels"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[int]int{}
	for _, l := range resp.Levels {
		if l.Slot != nil {
			got[*l.Slot] = l.RelativeSlot
		}
	}
	if got[-1] != -1 {
		t.Errorf("slot -1 relative_slot = %d, want -1 (closest to entry)", got[-1])
	}
	if got[-2] != -2 {
		t.Errorf("slot -2 relative_slot = %d, want -2", got[-2])
	}
	// The entry anchor L(0) must not be renumbered.
	if got[0] != 0 {
		t.Errorf("slot 0 relative_slot = %d, want 0 (anchor, not renumbered)", got[0])
	}
}
