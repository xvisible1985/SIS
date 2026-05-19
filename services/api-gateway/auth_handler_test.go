//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sis/pkg/cache"
	"sis/pkg/db"
	_ "embed"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, "postgres://sis:sis_secret@localhost:5432/sis")
	if err != nil {
		t.Skipf("timescaledb unavailable: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Run migrations
	if err := db.Migrate(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	rdb, err := cache.Connect(ctx, "redis://localhost:6379")
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { rdb.Close() })
	return NewServer(ctx, pool, rdb, "test-secret", "0000000000000000000000000000000000000000000000000000000000000000", map[string]bool{})
}

func TestRegister_Success(t *testing.T) {
	s := newTestServer(t)
	body := `{"email":"reg_plan4@example.com","password":"pass1234"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected token in response")
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", "reg_plan4@example.com")
}

func TestRegister_DuplicateEmail(t *testing.T) {
	s := newTestServer(t)
	email := "dup_plan4@example.com"
	body := `{"email":"` + email + `","password":"pass1234"}`

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	s.Register(rec1, req1)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	s.Register(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("got %d, want 409", rec2.Code)
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}

func TestLogin_Success(t *testing.T) {
	s := newTestServer(t)
	email := "login_plan4@example.com"
	body := `{"email":"` + email + `","password":"mypassword"}`

	// Register first
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	// Login
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected token")
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}

func TestLogin_WrongPassword(t *testing.T) {
	s := newTestServer(t)
	email := "wrongpw_plan4@example.com"

	regBody := `{"email":"` + email + `","password":"correct"}`
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	loginBody := `{"email":"` + email + `","password":"wrong"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}

func TestLogin_BlockedUser(t *testing.T) {
	s := newTestServer(t)
	email := "blocked_user@example.com"

	// Register
	regBody := `{"email":"` + email + `","password":"pass1234"}`
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	// Block the user directly in DB
	s.pool.Exec(context.Background(),
		`UPDATE users SET is_blocked=true WHERE email=$1`, email)

	// Login should fail with 403
	loginBody := `{"email":"` + email + `","password":"pass1234"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}

	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}
