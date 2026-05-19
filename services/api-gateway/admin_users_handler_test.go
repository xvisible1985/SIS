//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// createAdminTestUser registers a user and optionally upgrades to admin.
// Returns userID. Caller is responsible for cleanup.
func createAdminTestUser(t *testing.T, s *Server, email, password string, makeAdmin bool) string {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register failed: %s", rec.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	userID, _ := resp["user_id"].(string)
	if makeAdmin {
		s.pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, userID)
	}
	return userID
}

// addChiParams injects chi URL params into the request context without replacing existing context values.
func addChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestListAdminUsers(t *testing.T) {
	s := newTestServer(t)
	email := "listusers_admin@example.com"
	adminID := createAdminTestUser(t, s, email, "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = withUserID(req, adminID)
	s.ListAdminUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var users []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&users)
	if len(users) == 0 {
		t.Error("expected at least one user")
	}
	// Check structure
	u := users[0]
	for _, field := range []string{"id", "email", "role", "status", "accounts"} {
		if _, ok := u[field]; !ok {
			t.Errorf("missing field %q in response", field)
		}
	}
}

func TestPatchAdminUser_Role(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "patch_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "patch_target@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	body := `{"role":"admin"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/"+targetID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.PatchAdminUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var role string
	s.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE id=$1`, targetID).Scan(&role)
	if role != "admin" {
		t.Errorf("role not updated, got %q", role)
	}
}

func TestBlockUnblockUser(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "blocker_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "blockee@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	// Block
	blockBody := `{"reason":"test block"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/block", bytes.NewBufferString(blockBody))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.BlockAdminUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("block got %d: %s", rec.Code, rec.Body.String())
	}

	var isBlocked bool
	s.pool.QueryRow(context.Background(), `SELECT is_blocked FROM users WHERE id=$1`, targetID).Scan(&isBlocked)
	if !isBlocked {
		t.Error("user should be blocked")
	}

	// Unblock
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/unblock", nil)
	req2 = withUserID(req2, adminID)
	req2 = addChiParams(req2, map[string]string{"id": targetID})
	s.UnblockAdminUser(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unblock got %d: %s", rec2.Code, rec2.Body.String())
	}

	s.pool.QueryRow(context.Background(), `SELECT is_blocked FROM users WHERE id=$1`, targetID).Scan(&isBlocked)
	if isBlocked {
		t.Error("user should be unblocked")
	}
}

func TestBalanceAdjust(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "baladmin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "baluser@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	body := `{"amount":100.5,"note":"test bonus"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/balance/adjust",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.AdjustNovabotBalance(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var balance float64
	s.pool.QueryRow(context.Background(), `SELECT novabot_balance FROM users WHERE id=$1`, targetID).Scan(&balance)
	if balance != 100.5 {
		t.Errorf("balance = %v, want 100.5", balance)
	}

	var txCount int
	s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM novabot_transactions WHERE user_id=$1`, targetID).Scan(&txCount)
	if txCount != 1 {
		t.Errorf("expected 1 transaction, got %d", txCount)
	}
}

func TestDeleteAdminAccount(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "delacc_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "delacc_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	// Insert a fake exchange account
	var accID string
	s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,'bybit','test','enc','enc') RETURNING id`,
		targetID).Scan(&accID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/"+targetID+"/accounts/"+accID, nil)
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID, "aid": accID})
	s.DeleteAdminAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cnt int
	s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM exchange_accounts WHERE id=$1`, accID).Scan(&cnt)
	if cnt != 0 {
		t.Error("account should have been deleted")
	}
}
