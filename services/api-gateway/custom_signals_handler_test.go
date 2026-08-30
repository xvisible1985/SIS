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

func createTestCustomSignal(t *testing.T, s *Server, userID, name string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name": name,
		"components": []map[string]any{
			{"signal_id": "rsi-os", "params": map[string]any{"period": 14, "threshold": 30, "kind": "cross"}},
			{"signal_id": "macd-x", "params": map[string]any{"fast": 12, "slow": 26, "signal": 9, "dir": "up"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/custom-signals", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateCustomSignal(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateCustomSignal: got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() { s.pool.Exec(context.Background(), "DELETE FROM custom_signals WHERE id=$1", out.ID) })
	return out.ID
}

// TestCreateCustomSignal_RejectsFewerThanTwoComponents pins that a "combo" of a single
// signal is rejected — the whole point of custom_signals is combining more than one.
func TestCreateCustomSignal_RejectsFewerThanTwoComponents(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "cs_one1")

	body, _ := json.Marshal(map[string]any{
		"name":       "Just RSI",
		"components": []map[string]any{{"signal_id": "rsi-os", "params": map[string]any{}}},
	})
	req := httptest.NewRequest(http.MethodPost, "/custom-signals", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateCustomSignal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateCustomSignal_RejectsUncomputableComponent mirrors
// TestCreateWebhook_RejectsNonComputableCatalogSignal for combo legs — a component id that
// isn't a real pkg/signal registry entry must fail at combo-creation time, not silently
// save and only break later when something tries to subscribe to it.
func TestCreateCustomSignal_RejectsUncomputableComponent(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "cs_bad1")

	body, _ := json.Marshal(map[string]any{
		"name": "Bad combo",
		"components": []map[string]any{
			{"signal_id": "rsi-os", "params": map[string]any{}},
			{"signal_id": "not-a-real-signal", "params": map[string]any{}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/custom-signals", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateCustomSignal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateCustomSignal_SavesAndListsComponents: the round trip a saved combo must
// survive — components come back in the order they were saved, with their own params, and
// resolved names (not bare ids) via the signal_types join.
func TestCreateCustomSignal_SavesAndListsComponents(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "cs_list1")
	csID := createTestCustomSignal(t, s, userID, "RSI + MACD combo")

	req := httptest.NewRequest(http.MethodGet, "/custom-signals", nil)
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.ListCustomSignals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListCustomSignals: got %d: %s", rec.Code, rec.Body.String())
	}
	var list []customSignalRow
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found *customSignalRow
	for i := range list {
		if list[i].ID == csID {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("created custom signal %s not found in list: %+v", csID, list)
	}
	if found.Name != "RSI + MACD combo" {
		t.Errorf("Name = %q, want %q", found.Name, "RSI + MACD combo")
	}
	if len(found.Components) != 2 {
		t.Fatalf("len(Components) = %d, want 2", len(found.Components))
	}
	if found.Components[0].SignalID != "rsi-os" || found.Components[1].SignalID != "macd-x" {
		t.Errorf("component order/ids wrong: %+v", found.Components)
	}
	if found.Components[0].SignalName == "" || found.Components[0].SignalName == "rsi-os" {
		t.Errorf("expected a resolved signal_types name for rsi-os, got %q", found.Components[0].SignalName)
	}
}

// TestCreateWebhook_RejectsBothOrNeitherSignalSource pins the webhooks_signal_source_xor
// invariant at the API layer, not just the DB constraint.
func TestCreateWebhook_RejectsBothOrNeitherSignalSource(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_xor1")
	csID := createTestCustomSignal(t, s, userID, "combo for xor test")

	cases := []map[string]any{
		{"symbol": "BTCUSDT"}, // neither
		{"catalog_signal_id": "rsi-os", "custom_signal_id": csID, "symbol": "BTCUSDT"}, // both
	}
	for _, body := range cases {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(raw))
		req = withUserID(req, userID)
		rec := httptest.NewRecorder()
		s.CreateWebhook(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%v: got %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
}

// TestCreateWebhook_WithCustomSignalID_SubscribesAndResolvesName: a webhook built on a
// saved combo must come back with custom_signal_id set, catalog_signal_id nil, and its
// display name resolved from custom_signals (not empty/id).
func TestCreateWebhook_WithCustomSignalID_SubscribesAndResolvesName(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_combo1")
	csID := createTestCustomSignal(t, s, userID, "My RSI+MACD alert signal")

	body, _ := json.Marshal(map[string]any{
		"custom_signal_id": csID,
		"symbol":           "BTCUSDT",
		"timeframe":        "15m",
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
	if row.CustomSignalID == nil || *row.CustomSignalID != csID {
		t.Errorf("CustomSignalID = %v, want %s", row.CustomSignalID, csID)
	}
	if row.CatalogSignalID != nil {
		t.Errorf("CatalogSignalID = %v, want nil", row.CatalogSignalID)
	}
	if row.CatalogSignalName != "My RSI+MACD alert signal" {
		t.Errorf("CatalogSignalName = %q, want the custom signal's own name", row.CatalogSignalName)
	}
}

// TestDeleteCustomSignal_CascadesOwnedWebhooks: deleting a combo a webhook depends on must
// not leave a dangling webhook row (and must not panic unsubscribing it from the engine).
func TestDeleteCustomSignal_CascadesOwnedWebhooks(t *testing.T) {
	s := newTestServer(t)
	userID := createWHUser(t, s, "wh_cascade1")
	csID := createTestCustomSignal(t, s, userID, "combo to delete")

	body, _ := json.Marshal(map[string]any{
		"custom_signal_id": csID, "symbol": "ETHUSDT", "timeframe": "1h",
	})
	req := httptest.NewRequest(http.MethodPost, "/webhooks", bytes.NewReader(body))
	req = withUserID(req, userID)
	rec := httptest.NewRecorder()
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateWebhook: got %d: %s", rec.Code, rec.Body.String())
	}
	var row webhookRow
	json.NewDecoder(rec.Body).Decode(&row)

	delReq := httptest.NewRequest(http.MethodDelete, "/custom-signals/"+csID, nil)
	delReq = withUserID(delReq, userID)
	delReq = withChiParams(delReq, map[string]string{"id": csID})
	delRec := httptest.NewRecorder()
	s.DeleteCustomSignal(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DeleteCustomSignal: got %d: %s", delRec.Code, delRec.Body.String())
	}

	var exists bool
	s.pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM webhooks WHERE id=$1)", row.ID).Scan(&exists)
	if exists {
		t.Error("webhook row still exists after its custom signal was deleted — cascade did not fire")
	}
}

// TestSignalComboPreview_RequiresAtLeastTwoComponents mirrors the same rule at the
// preview endpoint — previewing a single signal already has /signals/chart-history.
func TestSignalComboPreview_RequiresAtLeastTwoComponents(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"symbol": "BTCUSDT", "interval": "1h",
		"components": []map[string]any{{"signal_id": "rsi-os", "params": map[string]any{}}},
	})
	req := httptest.NewRequest(http.MethodPost, "/signals/combo-preview", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.SignalComboPreview(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestSignalComboPreview_RejectsUncomputableComponent: same computability guard as create,
// applied before any Bybit call is made.
func TestSignalComboPreview_RejectsUncomputableComponent(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"symbol": "BTCUSDT", "interval": "1h",
		"components": []map[string]any{
			{"signal_id": "rsi-os", "params": map[string]any{}},
			{"signal_id": "not-a-real-signal", "params": map[string]any{}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/signals/combo-preview", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.SignalComboPreview(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
