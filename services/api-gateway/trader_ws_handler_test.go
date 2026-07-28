//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/auth"
)

func TestPositionsStream_MissingToken(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws/trader/positions?account_id=00000000-0000-0000-0000-000000000001", nil)
	s.PositionsStream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestPositionsStream_AccountNotFound checks that a valid token with a well-formed
// UUID that doesn't exist in the DB returns 404 (ErrNoRows path, not a DB error).
func TestPositionsStream_AccountNotFound_Returns404(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "ps_notfound")
	tok, err := auth.GenerateToken(userID, "test-secret", time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/ws/trader/positions?token="+tok+"&account_id=00000000-0000-0000-0000-000000000001",
		nil)
	s.PositionsStream(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestPositionsStream_DBError_Returns500 checks that a PostgreSQL syntax error
// (malformed UUID → invalid input syntax, NOT ErrNoRows) returns 500, not 404.
// Before the fix both paths mapped to 404, making DB outages look like missing accounts.
func TestPositionsStream_DBError_Returns500(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "ps_dberr")
	tok, err := auth.GenerateToken(userID, "test-secret", time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/ws/trader/positions?token="+tok+"&account_id=not-a-valid-uuid",
		nil)
	s.PositionsStream(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on DB syntax error, got %d: %s", rec.Code, rec.Body.String())
	}
}
