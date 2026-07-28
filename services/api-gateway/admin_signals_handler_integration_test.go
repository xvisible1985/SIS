//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestToggleIndicatorType_StatusOnlySyncsToSignalTypes is a regression for a bug found
// live (2026-07-16): 'price-change' was dragged into the "Сигналы" admin panel (panel
// synced to signal_types correctly), then its status was toggled 'disabled' → 'enabled'
// on the same card afterwards — but that second PATCH only carries {status}, no {panel},
// so the old sync block (gated on body.Panel != nil) silently skipped signal_types,
// leaving it stuck on 'disabled' while indicator_types moved on to 'enabled'. The
// user-facing SignalPickerField reads signal_types (not indicator_types), so the
// indicator never actually became selectable despite showing as enabled in the admin UI.
func TestToggleIndicatorType_StatusOnlySyncsToSignalTypes(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	const id = "test-sync-ind"

	s.pool.Exec(ctx, `DELETE FROM indicator_types WHERE id=$1`, id) //nolint:errcheck
	s.pool.Exec(ctx, `DELETE FROM signal_types WHERE id=$1`, id)    //nolint:errcheck
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM indicator_types WHERE id=$1`, id) //nolint:errcheck
		s.pool.Exec(ctx, `DELETE FROM signal_types WHERE id=$1`, id)    //nolint:errcheck
	})

	// Seed as if the card was already dragged into the signal panel at 'disabled' status.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO indicator_types (id, name, status, panel) VALUES ($1,'Test Sync','disabled','signal')`, id,
	); err != nil {
		t.Fatalf("seed indicator_types: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO signal_types (id, name, status, panel) VALUES ($1,'Test Sync','disabled','signal')`, id,
	); err != nil {
		t.Fatalf("seed signal_types: %v", err)
	}

	// A status-only PATCH — no panel field — mirrors what the admin UI's toggle sends.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/indicator-types/"+id, bytes.NewBufferString(`{"status":"enabled"}`))
	req = withChiParams(req, map[string]string{"id": id})
	s.ToggleIndicatorType(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ToggleIndicatorType: %d %s", rec.Code, rec.Body.String())
	}

	var gotStatus string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM signal_types WHERE id=$1`, id).Scan(&gotStatus); err != nil {
		t.Fatalf("query signal_types: %v", err)
	}
	if gotStatus != "enabled" {
		t.Fatalf("signal_types.status = %q, want %q (status-only toggle must propagate while panel='signal')", gotStatus, "enabled")
	}
}
