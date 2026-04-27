package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/auth"
)

func TestRequireAuth_NoHeader(t *testing.T) {
	s := &Server{jwtSecret: []byte("secret")}
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestRequireAuth_ValidToken(t *testing.T) {
	secret := "secret"
	tok, _ := auth.GenerateToken("user-1", secret, time.Hour)
	s := &Server{jwtSecret: []byte(secret)}
	var gotID string
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = UserIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	if gotID != "user-1" {
		t.Errorf("got userID=%q, want user-1", gotID)
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	s := &Server{jwtSecret: []byte("secret")}
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad.token.here")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestUserIDFromCtx_Empty(t *testing.T) {
	id := UserIDFromCtx(context.Background())
	if id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}

// withUserID injects a user ID into the request context (simulates RequireAuth middleware).
func withUserID(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserID, userID))
}
