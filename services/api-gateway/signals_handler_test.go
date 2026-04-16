//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/auth"
)

func authHeader(t *testing.T, s *Server, userID string) string {
	t.Helper()
	tok, err := auth.GenerateToken(userID, string(s.jwtSecret), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok
}

func createTestUser(t *testing.T, s *Server) string {
	t.Helper()
	// Use a sanitised name to avoid special chars in email
	email := "sigtest_" + t.Name()[:8] + "@example.com"
	var userID string
	err := s.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, '') RETURNING id`, email,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})
	return userID
}

// withChiParams injects chi URL parameters into the request context.
func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// withUserID injects a user ID into the request context (simulates RequireAuth middleware).
func withUserID(r *http.Request, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserID, userID))
}

const testConditions = `{"type":"condition","indicator":"RSI","params":{"period":14},"operator":">","value":50}`

func TestCreateAndGetSignal(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)

	body := `{"name":"RSI cross","description":"test","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateSignal(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	sigID, _ := created["id"].(string)
	if sigID == "" {
		t.Fatal("expected signal id in response")
	}

	// GET /signals/:id
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/signals/"+sigID, nil)
	req2 = withUserID(req2, userID)
	req2 = withChiParams(req2, map[string]string{"id": sigID})
	s.GetSignal(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get: got %d: %s", rec2.Code, rec2.Body.String())
	}
	var got map[string]any
	json.NewDecoder(rec2.Body).Decode(&got)
	if got["id"] != sigID {
		t.Errorf("got id=%v, want %s", got["id"], sigID)
	}
}

func TestListSignals_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signals", nil)
	req = withUserID(req, userID)
	s.ListSignals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []any
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("expected 0 signals, got %d", len(resp))
	}
}

func TestDeleteSignal(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)

	body := `{"name":"del","description":"","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	recC := httptest.NewRecorder()
	reqC := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	reqC.Header.Set("Content-Type", "application/json")
	reqC = withUserID(reqC, userID)
	s.CreateSignal(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	sigID, _ := created["id"].(string)

	recD := httptest.NewRecorder()
	reqD := httptest.NewRequest(http.MethodDelete, "/signals/"+sigID, nil)
	reqD = withUserID(reqD, userID)
	reqD = withChiParams(reqD, map[string]string{"id": sigID})
	s.DeleteSignal(recD, reqD)
	if recD.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", recD.Code)
	}

	// Verify gone
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/signals/"+sigID, nil)
	req2 = withUserID(req2, userID)
	req2 = withChiParams(req2, map[string]string{"id": sigID})
	s.GetSignal(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec2.Code)
	}
}

func TestUpdateSignal(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)

	body := `{"name":"original","description":"","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	recC := httptest.NewRecorder()
	reqC := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	reqC.Header.Set("Content-Type", "application/json")
	reqC = withUserID(reqC, userID)
	s.CreateSignal(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	sigID, _ := created["id"].(string)

	updateBody := `{"name":"updated"}`
	recU := httptest.NewRecorder()
	reqU := httptest.NewRequest(http.MethodPut, "/signals/"+sigID, bytes.NewBufferString(updateBody))
	reqU.Header.Set("Content-Type", "application/json")
	reqU = withUserID(reqU, userID)
	reqU = withChiParams(reqU, map[string]string{"id": sigID})
	s.UpdateSignal(recU, reqU)
	if recU.Code != http.StatusOK {
		t.Fatalf("update: got %d: %s", recU.Code, recU.Body.String())
	}
	var updated map[string]any
	json.NewDecoder(recU.Body).Decode(&updated)
	if updated["name"] != "updated" {
		t.Errorf("name not updated: got %v", updated["name"])
	}
}
