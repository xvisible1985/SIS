//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateStrategy_PersistsRelativeSlots verifies that creating a standalone matrix
// strategy (no bot) with relative_slots=true persists the flag — guarding the
// CreateStrategy INSERT placeholder/column alignment.
func TestCreateStrategy_PersistsRelativeSlots(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "relcreate")
	accID := createTestAccount(t, s, userID)

	body, _ := json.Marshal(map[string]any{
		"account_id":     accID,
		"symbol":         "RELUSDT",
		"direction":      "long",
		"strategy_type":  "matrix",
		"relative_slots": true,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/strategies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateStrategy(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateStrategy: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(rec.Body).Decode(&created)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM strategies WHERE id=$1", created.ID) })

	var got bool
	if err := s.pool.QueryRow(ctx, `SELECT relative_slots FROM strategies WHERE id=$1`, created.ID).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !got {
		t.Error("relative_slots was not persisted by CreateStrategy")
	}
}
