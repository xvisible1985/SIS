//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLVGetAccounts_ReturnsJSON(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "lv_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	// Insert an exchange account for a non-admin user
	ownerID := createAdminTestUser(t, s, "lv_owner@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerID)

	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1, 'bybit', 'test-acc', 'enc_key', 'enc_sec')`, ownerID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/log-visualizer/accounts", nil)
	req = withUserID(req, adminID)
	s.LVGetAccounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("expected at least one account")
	}
	first := resp[0]
	if first["id"] == nil || first["label"] == nil || first["ownerUsername"] == nil {
		t.Errorf("missing fields in response: %v", first)
	}
}
