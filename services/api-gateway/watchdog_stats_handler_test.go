package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetWatchdogStats_MissingFile_ReturnsEmptyMapNot500(t *testing.T) {
	t.Setenv("WATCHDOG_STATS_PATH", filepath.Join(t.TempDir(), "does-not-exist.json"))
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/watchdog-stats", nil)
	s.GetWatchdogStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]watchdogServiceStat
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0 for a missing file", len(got))
	}
}

func TestGetWatchdogStats_MalformedFile_ReturnsEmptyMapNot500(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("WATCHDOG_STATS_PATH", path)
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/watchdog-stats", nil)
	s.GetWatchdogStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]watchdogServiceStat
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0 for a malformed file", len(got))
	}
}

func TestGetWatchdogStats_ValidFile_ReturnsParsedStats(t *testing.T) {
	restartAt := time.Date(2026, 10, 14, 4, 11, 0, 0, time.UTC)
	checkAt := time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)
	fixture := map[string]watchdogServiceStat{
		"docker": {
			Restarts:    3,
			LastRestart: &restartAt,
			LastCheck:   &checkAt,
			Status:      "ok",
		},
		"sis-frontend": {
			Restarts:    0,
			LastRestart: nil,
			LastCheck:   &checkAt,
			Status:      "online",
		},
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("WATCHDOG_STATS_PATH", path)

	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/watchdog-stats", nil)
	s.GetWatchdogStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]watchdogServiceStat
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got["docker"].Restarts != 3 {
		t.Errorf("docker.Restarts = %d, want 3", got["docker"].Restarts)
	}
	if got["docker"].LastRestart == nil || !got["docker"].LastRestart.Equal(restartAt) {
		t.Errorf("docker.LastRestart = %v, want %v", got["docker"].LastRestart, restartAt)
	}
	if got["sis-frontend"].LastRestart != nil {
		t.Errorf("sis-frontend.LastRestart = %v, want nil (never restarted)", got["sis-frontend"].LastRestart)
	}
	if got["sis-frontend"].Status != "online" {
		t.Errorf("sis-frontend.Status = %q, want %q", got["sis-frontend"].Status, "online")
	}
}

// Regression: Set-Content -Encoding UTF8 on Windows PowerShell 5.1 writes a UTF-8 BOM,
// which json.Unmarshal doesn't skip — found live, the watchdog panel stayed empty on the
// frontend even though the stats file existed and had valid-looking content.
func TestGetWatchdogStats_FileWithUtf8Bom_StillParses(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	body := []byte(`{"docker":{"restarts":1,"last_restart":"2026-10-14T04:11:00Z","last_check":"2026-10-14T15:30:00Z","status":"ok"}}`)
	path := filepath.Join(t.TempDir(), "stats.json")
	if err := os.WriteFile(path, append(bom, body...), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Setenv("WATCHDOG_STATS_PATH", path)

	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/watchdog-stats", nil)
	s.GetWatchdogStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]watchdogServiceStat
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got["docker"].Restarts != 1 {
		t.Errorf("got %+v, want docker.restarts=1 (BOM must not make this look like an empty/malformed file)", got)
	}
}

func TestGetWatchdogStats_DefaultPath_WhenEnvUnset(t *testing.T) {
	// Default path is relative ("scripts/watchdog-stats.json") and almost certainly
	// doesn't resolve from the test binary's working directory — just pins that an
	// unset env var doesn't crash the handler, same graceful-empty-map behavior.
	t.Setenv("WATCHDOG_STATS_PATH", "")
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/watchdog-stats", nil)
	s.GetWatchdogStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
