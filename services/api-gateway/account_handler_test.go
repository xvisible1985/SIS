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

func newAccTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	userID := createWHUser(t, s, "acct")
	return s, userID
}

func TestGetProfile(t *testing.T) {
	s, userID := newAccTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/profile", nil)
	req = withUserID(req, userID)
	s.GetProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["email"] == nil {
		t.Fatal("expected email in response")
	}
}

func TestUpdateUsername(t *testing.T) {
	s, userID := newAccTestServer(t)
	body := `{"username":"testuser"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/account/profile", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.UpdateProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["username"] != "testuser" {
		t.Errorf("expected username=testuser, got %v", resp["username"])
	}
}

func TestChangePassword(t *testing.T) {
	s := newTestServer(t)
	// Register a real user so we have a password hash
	regBody := `{"email":"pwchange_test@example.com","password":"oldpass123"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Skipf("register failed: %s", rec.Body.String())
	}
	var regResp map[string]any
	json.NewDecoder(rec.Body).Decode(&regResp)
	userID, _ := regResp["user_id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})

	cpBody := `{"current_password":"oldpass123","new_password":"newpass456"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/account/change-password", bytes.NewBufferString(cpBody))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	s.ChangePassword(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	s := newTestServer(t)
	regBody := `{"email":"pwchange_wrong@example.com","password":"correct123"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	var regResp map[string]any
	json.NewDecoder(rec.Body).Decode(&regResp)
	userID, _ := regResp["user_id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})

	cpBody := `{"current_password":"wrong","new_password":"newpass456"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/account/change-password", bytes.NewBufferString(cpBody))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	s.ChangePassword(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec2.Code)
	}
}
