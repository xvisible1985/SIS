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

// createWHUser creates a test user with an explicit suffix to avoid email collisions.
func createWHUser(t *testing.T, s *Server, suffix string) string {
	t.Helper()
	email := "wh_" + suffix + "@example.com"
	var userID string
	err := s.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, '') RETURNING id`, email,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("createWHUser %s: %v", suffix, err)
	}
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})
	return userID
}

func TestCreateAndGetWebhook(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "cagwh")
	sigID := createTestSignal(t, s, userID)

	body := `{"signal_id":"` + sigID + `","url":"https://example.com/hook","platform":"custom"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	whID, _ := created["id"].(string)
	if whID == "" {
		t.Fatal("expected webhook id in response")
	}
	if created["url"] != "https://example.com/hook" {
		t.Errorf("unexpected url: %v", created["url"])
	}

	// GET /webhooks/:id
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/webhooks/"+whID, nil)
	req2 = withUserID(req2, userID)
	req2 = withChiParams(req2, map[string]string{"id": whID})
	s.GetWebhook(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("get: got %d: %s", rec2.Code, rec2.Body.String())
	}
	var got map[string]any
	json.NewDecoder(rec2.Body).Decode(&got)
	if got["id"] != whID {
		t.Errorf("got id=%v, want %s", got["id"], whID)
	}
}

func TestListWebhooks_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "listwh")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/webhooks", nil)
	req = withUserID(req, userID)
	s.ListWebhooks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []any
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp) != 0 {
		t.Errorf("expected 0 webhooks, got %d", len(resp))
	}
}

func TestUpdateWebhook(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "updwh")
	sigID := createTestSignal(t, s, userID)

	body := `{"signal_id":"` + sigID + `","url":"https://old.example.com/hook","platform":"custom"}`
	recC := httptest.NewRecorder()
	reqC := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewBufferString(body))
	reqC.Header.Set("Content-Type", "application/json")
	reqC = withUserID(reqC, userID)
	s.CreateWebhook(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	whID, _ := created["id"].(string)

	updateBody := `{"url":"https://new.example.com/hook"}`
	recU := httptest.NewRecorder()
	reqU := httptest.NewRequest(http.MethodPut, "/webhooks/"+whID, bytes.NewBufferString(updateBody))
	reqU.Header.Set("Content-Type", "application/json")
	reqU = withUserID(reqU, userID)
	reqU = withChiParams(reqU, map[string]string{"id": whID})
	s.UpdateWebhook(recU, reqU)
	if recU.Code != http.StatusOK {
		t.Fatalf("update: got %d: %s", recU.Code, recU.Body.String())
	}
	var updated map[string]any
	json.NewDecoder(recU.Body).Decode(&updated)
	if updated["url"] != "https://new.example.com/hook" {
		t.Errorf("url not updated: got %v", updated["url"])
	}
}

func TestDeleteWebhook(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "delwh")
	sigID := createTestSignal(t, s, userID)

	body := `{"signal_id":"` + sigID + `","url":"https://example.com/hook"}`
	recC := httptest.NewRecorder()
	reqC := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewBufferString(body))
	reqC.Header.Set("Content-Type", "application/json")
	reqC = withUserID(reqC, userID)
	s.CreateWebhook(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	whID, _ := created["id"].(string)

	recD := httptest.NewRecorder()
	reqD := httptest.NewRequest(http.MethodDelete, "/webhooks/"+whID, nil)
	reqD = withUserID(reqD, userID)
	reqD = withChiParams(reqD, map[string]string{"id": whID})
	s.DeleteWebhook(recD, reqD)
	if recD.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", recD.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/webhooks/"+whID, nil)
	req2 = withUserID(req2, userID)
	req2 = withChiParams(req2, map[string]string{"id": whID})
	s.GetWebhook(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", rec2.Code)
	}
}

func TestCreateWebhook_SignalNotOwned(t *testing.T) {
	s := newTestServer(t)
	userA := createWHUser(t, s, "ownA")
	userB := createWHUser(t, s, "ownB")
	sigID := createTestSignal(t, s, userA)

	body := `{"signal_id":"` + sigID + `","url":"https://example.com/hook"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userB)
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}
