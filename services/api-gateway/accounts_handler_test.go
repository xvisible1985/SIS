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

const testEncKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestCreateListDeleteAccount(t *testing.T) {
	s := newTestServer(t)
	s.encKey = testEncKey
	userID := createWHUser(t, s, "acc1")

	body := `{"exchange":"bybit","label":"main","api_key":"TESTKEY","secret":"TESTSECRET"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateAccount(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	accID, _ := created["id"].(string)
	if accID == "" {
		t.Fatal("expected id in response")
	}
	if _, ok := created["api_key"]; ok {
		t.Error("response must not expose api_key")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	req2 = withUserID(req2, userID)
	s.ListAccounts(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("list: got %d", rec2.Code)
	}
	var list []map[string]any
	json.NewDecoder(rec2.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/accounts/"+accID, nil)
	req3 = withUserID(req3, userID)
	req3 = withChiParams(req3, map[string]string{"id": accID})
	s.DeleteAccount(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec3.Code)
	}

	var count int
	s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM exchange_accounts WHERE id=$1", accID).Scan(&count)
	if count != 0 {
		t.Error("account should be deleted")
	}
}

func TestCreateAccount_NoEncKey(t *testing.T) {
	s := newTestServer(t)
	// encKey is empty — should 500
	userID := createWHUser(t, s, "acc2")
	body := `{"exchange":"bybit","label":"x","api_key":"K","secret":"S"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateAccount(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}
