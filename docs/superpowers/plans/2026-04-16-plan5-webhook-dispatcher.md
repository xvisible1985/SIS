# Webhook Dispatcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Webhook Dispatcher service that consumes fired signals from Redis Streams and reliably POSTs them to user-registered URLs with retry logic, plus CRUD endpoints in the API Gateway to manage webhooks.

**Architecture:** A new `services/webhook` binary subscribes to the `signals:fired` Redis Stream, fetches active webhooks for each fired signal from Postgres, and delivers the payload via HTTP POST with 3 attempts (backoff 1s → 5s → 30s). Each delivery attempt is logged in `webhook_logs`. The API Gateway gains five new endpoints for webhook CRUD, protected by the existing JWT middleware.

**Tech Stack:** Go 1.25, `pgx/v5` (DB), `go-redis/v9` (Redis Streams), `net/http` (delivery), `chi/v5` (gateway routes), `godotenv` (env), `httptest` (unit test server)

---

## File Structure

**Create:**
- `migrations/004_webhooks.sql` — `webhooks` and `webhook_logs` tables
- `services/api-gateway/webhooks_handler.go` — `ListWebhooks`, `CreateWebhook`, `GetWebhook`, `UpdateWebhook`, `DeleteWebhook`
- `services/api-gateway/webhooks_handler_test.go` — integration tests (`//go:build integration`)
- `services/webhook/dispatcher.go` — `FiredSignal`, `WebhookTarget`, `DeliveryResult` types; `parseSignalPayload`, `retryDelays`, `deliverOnce` (pure functions); `Dispatcher` struct with `Run`, `handleMessage`, `fetchWebhooks`, `deliverWithRetry`, `saveLog`
- `services/webhook/dispatcher_test.go` — unit tests for all pure functions (no DB/Redis needed)
- `services/webhook/main.go` — entry point, env wiring, graceful shutdown

**Modify:**
- `services/api-gateway/main.go:57-70` — add webhook routes to the protected group

---

## Task 1: DB migration — webhooks + webhook_logs

**Files:**
- Create: `migrations/004_webhooks.sql`

- [ ] **Step 1: Create migrations/004_webhooks.sql**

```sql
-- migrations/004_webhooks.sql

CREATE TABLE IF NOT EXISTS webhooks (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    signal_id  UUID        NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
    url        TEXT        NOT NULL,
    platform   TEXT        NOT NULL DEFAULT 'custom',
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS webhooks_owner  ON webhooks (owner_id);
CREATE INDEX IF NOT EXISTS webhooks_signal ON webhooks (signal_id) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS webhook_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id  UUID        NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status_code INT         NOT NULL DEFAULT 0,
    response_ms INT         NOT NULL DEFAULT 0,
    success     BOOLEAN     NOT NULL DEFAULT FALSE,
    error       TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS webhook_logs_webhook ON webhook_logs (webhook_id, sent_at DESC);
```

- [ ] **Step 2: Apply migration to running DB**

```bash
docker exec -i sis-timescaledb-1 psql -U sis -d sis < migrations/004_webhooks.sql
```

Expected: `CREATE TABLE`, `CREATE INDEX` (×3) with no errors.

- [ ] **Step 3: Commit**

```bash
git add migrations/004_webhooks.sql
git commit -m "feat: migration 004 — webhooks and webhook_logs tables"
```

---

## Task 2: Webhook CRUD in api-gateway

**Files:**
- Create: `services/api-gateway/webhooks_handler.go`
- Create: `services/api-gateway/webhooks_handler_test.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Write failing integration tests**

Create `services/api-gateway/webhooks_handler_test.go`:

```go
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
	req = withUserID(req, userB) // userB tries to hook userA's signal
	s.CreateWebhook(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Verify tests fail to compile**

```bash
go test -tags integration ./services/api-gateway/... -run "TestCreateAndGetWebhook|TestListWebhooks|TestUpdateWebhook|TestDeleteWebhook|TestCreateWebhook_SignalNotOwned" -v 2>&1 | head -10
```

Expected: FAIL — `s.CreateWebhook undefined`.

- [ ] **Step 3: Implement webhooks_handler.go**

Create `services/api-gateway/webhooks_handler.go`:

```go
// services/api-gateway/webhooks_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type webhookRow struct {
	ID        string    `json:"id"`
	SignalID  string    `json:"signal_id"`
	URL       string    `json:"url"`
	Platform  string    `json:"platform"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// ListWebhooks returns all webhooks owned by the authenticated user.
// GET /webhooks
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, url, platform, is_active, created_at
		 FROM webhooks WHERE owner_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]webhookRow, 0)
	for rows.Next() {
		var row webhookRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, row)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateWebhook creates a new webhook for a signal owned by the caller.
// POST /webhooks
func (s *Server) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		SignalID string `json:"signal_id"`
		URL      string `json:"url"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.SignalID == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "signal_id and url are required")
		return
	}
	if req.Platform == "" {
		req.Platform = "custom"
	}

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		req.SignalID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var row webhookRow
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO webhooks (owner_id, signal_id, url, platform)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, signal_id, url, platform, is_active, created_at`,
		userID, req.SignalID, req.URL, req.Platform,
	).Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetWebhook returns a single webhook by ID (must be owned by caller).
// GET /webhooks/:id
func (s *Server) GetWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var row webhookRow
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, signal_id, url, platform, is_active, created_at
		 FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	).Scan(&row.ID, &row.SignalID, &row.URL, &row.Platform, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// UpdateWebhook updates url, platform, is_active.
// PUT /webhooks/:id
func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	var req struct {
		URL      string `json:"url"`
		Platform string `json:"platform"`
		IsActive *bool  `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE webhooks SET
			url       = COALESCE(NULLIF($3,''), url),
			platform  = COALESCE(NULLIF($4,''), platform),
			is_active = COALESCE($5, is_active)
		 WHERE id=$1 AND owner_id=$2`,
		whID, userID, req.URL, req.Platform, req.IsActive,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	s.GetWebhook(w, r)
}

// DeleteWebhook deletes a webhook owned by the caller.
// DELETE /webhooks/:id
func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	whID := chi.URLParam(r, "id")
	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM webhooks WHERE id=$1 AND owner_id=$2`,
		whID, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Add webhook routes to services/api-gateway/main.go**

In the protected `r.Group` block (after the optimization routes, before the closing `}`), add:

```go
		r.Get("/webhooks", s.ListWebhooks)
		r.Post("/webhooks", s.CreateWebhook)
		r.Get("/webhooks/{id}", s.GetWebhook)
		r.Put("/webhooks/{id}", s.UpdateWebhook)
		r.Delete("/webhooks/{id}", s.DeleteWebhook)
```

The protected group in `main.go` should now look like:

```go
	r.Group(func(r chi.Router) {
		r.Use(s.RequireAuth)

		r.Get("/signals", s.ListSignals)
		r.Post("/signals", s.CreateSignal)
		r.Get("/signals/{id}", s.GetSignal)
		r.Put("/signals/{id}", s.UpdateSignal)
		r.Delete("/signals/{id}", s.DeleteSignal)

		r.Post("/signals/{id}/backtest", s.SubmitBacktest)
		r.Post("/signals/{id}/optimize", s.SubmitOptimize)
		r.Get("/signals/{id}/backtest-results", s.GetBacktestResults)
		r.Get("/signals/{id}/optimization-results", s.GetOptimizationResults)

		r.Get("/webhooks", s.ListWebhooks)
		r.Post("/webhooks", s.CreateWebhook)
		r.Get("/webhooks/{id}", s.GetWebhook)
		r.Put("/webhooks/{id}", s.UpdateWebhook)
		r.Delete("/webhooks/{id}", s.DeleteWebhook)
	})
```

- [ ] **Step 5: Run integration tests**

```bash
go test -tags integration ./services/api-gateway/... -run "TestCreateAndGetWebhook|TestListWebhooks|TestUpdateWebhook|TestDeleteWebhook|TestCreateWebhook_SignalNotOwned" -v
```

Expected: 5/5 PASS.

- [ ] **Step 6: Verify api-gateway still builds**

```bash
go build ./services/api-gateway/...
```

Expected: exits 0.

- [ ] **Step 7: Commit**

```bash
git add services/api-gateway/webhooks_handler.go services/api-gateway/webhooks_handler_test.go services/api-gateway/main.go
git commit -m "feat: api-gateway webhook CRUD handlers"
```

---

## Task 3: Dispatcher — pure functions + unit tests

**Files:**
- Create: `services/webhook/dispatcher.go`
- Create: `services/webhook/dispatcher_test.go`

- [ ] **Step 1: Write failing unit tests**

Create `services/webhook/dispatcher_test.go`:

```go
// services/webhook/dispatcher_test.go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseSignalPayload_Valid(t *testing.T) {
	raw := `{"signal_id":"uuid-123","signal_name":"RSI cross","symbol":"BTCUSDT","exchange":"binance","market":"spot","direction":"LONG","price":"67420.50","timestamp":"2026-04-16T12:00:00Z"}`
	s, err := parseSignalPayload(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.SignalID != "uuid-123" {
		t.Errorf("got SignalID=%q, want uuid-123", s.SignalID)
	}
	if s.Symbol != "BTCUSDT" {
		t.Errorf("got Symbol=%q, want BTCUSDT", s.Symbol)
	}
	if s.Direction != "LONG" {
		t.Errorf("got Direction=%q, want LONG", s.Direction)
	}
	if s.Price != "67420.50" {
		t.Errorf("got Price=%q, want 67420.50", s.Price)
	}
}

func TestParseSignalPayload_InvalidJSON(t *testing.T) {
	_, err := parseSignalPayload("not json {")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRetryDelays(t *testing.T) {
	delays := retryDelays()
	if len(delays) != 3 {
		t.Fatalf("expected 3 delays, got %d", len(delays))
	}
	expected := []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}
	for i, want := range expected {
		if delays[i] != want {
			t.Errorf("delay[%d]: got %v, want %v", i, delays[i], want)
		}
	}
}

func TestDeliverOnce_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1", Symbol: "BTCUSDT", Direction: "LONG", Price: "50000"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, payload)
	if !result.Success {
		t.Errorf("expected Success=true, got false, err=%s", result.Error)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("got StatusCode=%d, want 200", result.StatusCode)
	}
	if result.ResponseMs < 0 {
		t.Errorf("ResponseMs should be non-negative")
	}
}

func TestDeliverOnce_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, payload)
	if result.Success {
		t.Error("expected Success=false for 500 response")
	}
	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("got StatusCode=%d, want 500", result.StatusCode)
	}
}

func TestDeliverOnce_NetworkError(t *testing.T) {
	// Port 1 is guaranteed to refuse connections
	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 1 * time.Second}, "http://127.0.0.1:1", payload)
	if result.Success {
		t.Error("expected Success=false for network error")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for network error")
	}
}

func TestDeliverOnce_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := FiredSignal{SignalID: "s1"}
	result := deliverOnce(context.Background(), &http.Client{Timeout: 50 * time.Millisecond}, srv.URL, payload)
	if result.Success {
		t.Error("expected Success=false for timeout")
	}
	if result.Error == "" {
		t.Error("expected non-empty error for timeout")
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./services/webhook/... -v 2>&1 | head -15
```

Expected: FAIL — package `sis/services/webhook` not found (or build error).

- [ ] **Step 3: Implement dispatcher.go**

Create `services/webhook/dispatcher.go`:

```go
// services/webhook/dispatcher.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	streamFired    = "signals:fired"
	consumerGroup  = "webhook-dispatcher"
	consumerName   = "dispatcher-1"
	deliverTimeout = 10 * time.Second
	maxAttempts    = 3
)

// FiredSignal is the payload published to the signals:fired Redis Stream.
type FiredSignal struct {
	SignalID   string `json:"signal_id"`
	SignalName string `json:"signal_name"`
	Symbol     string `json:"symbol"`
	Exchange   string `json:"exchange"`
	Market     string `json:"market"`
	Direction  string `json:"direction"`
	Price      string `json:"price"`
	Timestamp  string `json:"timestamp"`
}

// WebhookTarget holds the data needed to deliver to one webhook endpoint.
type WebhookTarget struct {
	ID  string
	URL string
}

// DeliveryResult records the outcome of a single HTTP POST attempt.
type DeliveryResult struct {
	StatusCode int
	ResponseMs int64
	Success    bool
	Error      string
}

// Dispatcher reads fired signals from Redis and delivers webhooks.
type Dispatcher struct {
	pool   *pgxpool.Pool
	rdb    *redis.Client
	client *http.Client
}

// NewDispatcher creates a Dispatcher with a configurable HTTP timeout.
func NewDispatcher(pool *pgxpool.Pool, rdb *redis.Client) *Dispatcher {
	return &Dispatcher{
		pool:   pool,
		rdb:    rdb,
		client: &http.Client{Timeout: deliverTimeout},
	}
}

// retryDelays returns the wait durations between delivery attempts (3 attempts total).
func retryDelays() []time.Duration {
	return []time.Duration{1 * time.Second, 5 * time.Second, 30 * time.Second}
}

// parseSignalPayload unmarshals a JSON string from a Redis stream message.
func parseSignalPayload(raw string) (FiredSignal, error) {
	var s FiredSignal
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return FiredSignal{}, fmt.Errorf("parseSignalPayload: %w", err)
	}
	return s, nil
}

// deliverOnce sends a single HTTP POST with the FiredSignal payload to url.
// Returns a DeliveryResult with timing and HTTP status information.
func deliverOnce(ctx context.Context, client *http.Client, url string, payload FiredSignal) DeliveryResult {
	body, _ := json.Marshal(payload)
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return DeliveryResult{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return DeliveryResult{ResponseMs: elapsed, Error: err.Error()}
	}
	defer resp.Body.Close()

	return DeliveryResult{
		StatusCode: resp.StatusCode,
		ResponseMs: elapsed,
		Success:    resp.StatusCode >= 200 && resp.StatusCode < 300,
	}
}

// Run starts the consumer loop. Blocks until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.rdb.XGroupCreateMkStream(ctx, streamFired, consumerGroup, "0")
	log.Printf("webhook-dispatcher: listening on stream %s", streamFired)
	for {
		if ctx.Err() != nil {
			return
		}
		msgs, err := d.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    consumerGroup,
			Consumer: consumerName,
			Streams:  []string{streamFired, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("webhook-dispatcher: xreadgroup error: %v", err)
			continue
		}
		for _, stream := range msgs {
			for _, msg := range stream.Messages {
				d.handleMessage(ctx, msg)
			}
		}
	}
}

func (d *Dispatcher) handleMessage(ctx context.Context, msg redis.XMessage) {
	raw, ok := msg.Values["payload"]
	if !ok {
		log.Printf("webhook-dispatcher: message %s missing payload field", msg.ID)
		d.ack(ctx, msg.ID)
		return
	}
	signal, err := parseSignalPayload(raw.(string))
	if err != nil {
		log.Printf("webhook-dispatcher: parse error message %s: %v", msg.ID, err)
		d.ack(ctx, msg.ID)
		return
	}
	targets, err := d.fetchWebhooks(ctx, signal.SignalID)
	if err != nil {
		log.Printf("webhook-dispatcher: fetch webhooks signal %s: %v", signal.SignalID, err)
		d.ack(ctx, msg.ID)
		return
	}
	for _, target := range targets {
		d.deliverWithRetry(ctx, target, signal)
	}
	d.ack(ctx, msg.ID)
}

func (d *Dispatcher) fetchWebhooks(ctx context.Context, signalID string) ([]WebhookTarget, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT id, url FROM webhooks WHERE signal_id=$1 AND is_active=TRUE`,
		signalID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []WebhookTarget
	for rows.Next() {
		var t WebhookTarget
		if err := rows.Scan(&t.ID, &t.URL); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

func (d *Dispatcher) deliverWithRetry(ctx context.Context, target WebhookTarget, payload FiredSignal) {
	delays := retryDelays()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		result := deliverOnce(ctx, d.client, target.URL, payload)
		d.saveLog(ctx, target.ID, result)
		if result.Success {
			log.Printf("webhook-dispatcher: delivered webhook %s (attempt %d)", target.ID, attempt+1)
			return
		}
		log.Printf("webhook-dispatcher: failed webhook %s attempt %d status=%d err=%s",
			target.ID, attempt+1, result.StatusCode, result.Error)
		if attempt < len(delays) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delays[attempt]):
			}
		}
	}
	log.Printf("webhook-dispatcher: giving up on webhook %s after %d attempts", target.ID, maxAttempts)
}

func (d *Dispatcher) saveLog(ctx context.Context, webhookID string, r DeliveryResult) {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO webhook_logs (webhook_id, status_code, response_ms, success, error)
		 VALUES ($1, $2, $3, $4, $5)`,
		webhookID, r.StatusCode, r.ResponseMs, r.Success, r.Error,
	)
	if err != nil {
		log.Printf("webhook-dispatcher: saveLog webhook %s: %v", webhookID, err)
	}
}

func (d *Dispatcher) ack(ctx context.Context, msgID string) {
	if err := d.rdb.XAck(ctx, streamFired, consumerGroup, msgID).Err(); err != nil {
		log.Printf("webhook-dispatcher: ack error %s: %v", msgID, err)
	}
}
```

- [ ] **Step 4: Run unit tests**

```bash
go test ./services/webhook/... -v
```

Expected: 7/7 PASS (TestParseSignalPayload_Valid, TestParseSignalPayload_InvalidJSON, TestRetryDelays, TestDeliverOnce_Success, TestDeliverOnce_ServerError, TestDeliverOnce_NetworkError, TestDeliverOnce_Timeout).

- [ ] **Step 5: Commit**

```bash
git add services/webhook/dispatcher.go services/webhook/dispatcher_test.go
git commit -m "feat: webhook dispatcher core — delivery, retry, pure functions + unit tests"
```

---

## Task 4: Dispatcher main.go + build + full test run

**Files:**
- Create: `services/webhook/main.go`

- [ ] **Step 1: Create services/webhook/main.go**

```go
// services/webhook/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"sis/pkg/cache"
	"sis/pkg/db"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	d := NewDispatcher(pool, rdb)
	log.Println("webhook-dispatcher: starting")
	d.Run(ctx)
	log.Println("webhook-dispatcher: stopped")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
```

- [ ] **Step 2: Build the binary**

```bash
go build ./services/webhook/...
```

Expected: exits 0, no errors.

- [ ] **Step 3: Run all unit tests**

```bash
go test ./...
```

Expected: all packages PASS, no build errors.

- [ ] **Step 4: Commit**

```bash
git add services/webhook/main.go
git commit -m "feat: webhook dispatcher main.go entry point"
```

---

## Self-Review

**Spec coverage:**
- ✅ Subscribe to `signals:fired` Redis Stream — Task 3 (`Run`, `handleMessage`)
- ✅ POST to user-registered URL — Task 3 (`deliverOnce`)
- ✅ Retry: 3 attempts, backoff 1s → 5s → 30s — Task 3 (`deliverWithRetry`, `retryDelays`)
- ✅ Log each delivery: status_code, response_ms, success, error — Task 3 (`saveLog`)
- ✅ Supported platforms field in `webhooks` table — Task 1 (schema) + Task 2 (CRUD)
- ✅ Webhook CRUD (list, create, get, update, delete) — Task 2
- ✅ Ownership enforcement (signal must belong to requesting user) — Task 2 (`CreateWebhook`)
- ✅ DB tables: `webhooks`, `webhook_logs` — Task 1

**Placeholder scan:** No TBD, TODO, or "similar to" references found.

**Type consistency:**
- `FiredSignal` defined in `dispatcher.go` — used in `dispatcher_test.go` ✅
- `WebhookTarget` defined in `dispatcher.go` — used in `deliverWithRetry`, `fetchWebhooks` ✅
- `DeliveryResult` defined in `dispatcher.go` — used in `deliverOnce`, `deliverWithRetry`, `saveLog` ✅
- `NewDispatcher` defined in `dispatcher.go` — used in `main.go` ✅
- `webhookRow` defined in `webhooks_handler.go` — used consistently across all handlers ✅
- `withUserID`, `withChiParams`, `newTestServer`, `createTestSignal` — all defined in existing test files in same `package main` (integration build tag) ✅
- `createWHUser` defined in `webhooks_handler_test.go` — used only in that file ✅
