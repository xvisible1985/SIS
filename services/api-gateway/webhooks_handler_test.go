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

func createTestWebhook(t *testing.T, s *Server, userID, catalogSignalID, symbol string) webhookRow {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"catalog_signal_id": catalogSignalID,
		"symbol":            symbol,
		"timeframe":         "15m",
		"params":            map[string]any{},
		"platform":          "custom",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateWebhook: got %d: %s", rec.Code, rec.Body.String())
	}
	var row webhookRow
	if err := json.NewDecoder(rec.Body).Decode(&row); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		s.signalEngine.Unsubscribe(row.ID)
		s.pool.Exec(context.Background(), "DELETE FROM webhooks WHERE id=$1", row.ID)
	})
	return row
}

// TestCreateWebhook_RejectsNonComputableCatalogSignal is the regression for the
// signal.Build validation in CreateWebhook: a catalog_signal_id that isn't a real
// pkg/signal registry entry (e.g. it exists only as an admin-catalog row, or doesn't
// exist at all) must be rejected at creation time, not silently accepted and then never
// actually fire because the engine can't build it.
func TestCreateWebhook_RejectsNonComputableCatalogSignal(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_reject1")

	body, _ := json.Marshal(map[string]any{
		"catalog_signal_id": "not-a-real-signal",
		"symbol":            "BTCUSDT",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateWebhook_GeneratesRelayURL pins that SIS itself generates webhooks.url (never
// user-supplied) — an internal /webhooks/relay/{token} endpoint, one per alert.
func TestCreateWebhook_GeneratesRelayURL(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_create1")

	row := createTestWebhook(t, s, userID, "rsi-os", "BTCUSDT")
	if row.URL == "" {
		t.Fatal("expected a generated URL, got empty string")
	}
	if want := "/webhooks/relay/"; !bytes.Contains([]byte(row.URL), []byte(want)) {
		t.Errorf("URL = %q, want it to contain %q", row.URL, want)
	}
	if row.CatalogSignalName == "" {
		t.Error("expected CatalogSignalName resolved from signal_types, got empty")
	}
}

// TestDeleteWebhook_RemovesRow verifies delete actually removes the alert (and, by not
// panicking, that Unsubscribe on a since-deleted alert is safe).
func TestDeleteWebhook_RemovesRow(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_delete1")
	row := createTestWebhook(t, s, userID, "rsi-os", "ETHUSDT")

	req := httptest.NewRequest(http.MethodDelete, "/webhooks/"+row.ID, nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": row.ID})
	rec := httptest.NewRecorder()
	s.DeleteWebhook(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteWebhook: got %d: %s", rec.Code, rec.Body.String())
	}

	var exists bool
	s.pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM webhooks WHERE id=$1)", row.ID).Scan(&exists)
	if exists {
		t.Error("webhook row still exists after delete")
	}
}

// TestWebhookRelay_UnknownToken404s ensures a bad/stale token doesn't panic and reports
// not-found rather than silently succeeding.
func TestWebhookRelay_UnknownToken404s(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/relay/does-not-exist", bytes.NewReader([]byte(`{}`)))
	req = withChiParams(req, map[string]string{"token": "does-not-exist"})
	rec := httptest.NewRecorder()
	s.WebhookRelay(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestWebhookRelay_KnownToken200s exercises the full path a real delivery takes: look up
// the alert by its token (not owner-scoped — the dispatcher has no user session) and
// respond 200 so the dispatcher records a successful delivery in webhook_logs. No Telegram
// connection is set up for this user, so the forward step is a no-op — this only pins that
// absence doesn't turn into an error response.
func TestWebhookRelay_KnownToken200s(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_relay1")
	row := createTestWebhook(t, s, userID, "rsi-os", "SOLUSDT")

	var token string
	if err := s.pool.QueryRow(context.Background(), "SELECT token FROM webhooks WHERE id=$1", row.ID).Scan(&token); err != nil {
		t.Fatalf("read back token: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"direction": "buy", "symbol": "SOLUSDT"})
	req := httptest.NewRequest(http.MethodPost, "/webhooks/relay/"+token, bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"token": token})
	rec := httptest.NewRecorder()
	s.WebhookRelay(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// createWHUser mirrors the other _test.go files' per-suite user helper, scoped to this
// file's tests specifically (webhooks_*) to avoid cross-test collisions on unique fields.
func createWHUser(t *testing.T, s *Server, label string) string {
	t.Helper()
	email := label + "@example.com"
	var id string
	if err := s.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatalf("createWHUser: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", id) })
	return id
}

var _ = chi.URLParam // keep chi imported if unused directly in this file
