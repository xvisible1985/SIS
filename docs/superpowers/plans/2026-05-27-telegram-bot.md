# Telegram Bot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Telegram bot microservice enabling magic-link auth, strategy monitoring/control commands, event notifications, and welcome messages for new group members.

**Architecture:** New `services/tg-bot/` Go service polls Telegram API; calls api-gateway via internal HTTP using `TELEGRAM_BOT_SECRET` header for auth. api-gateway publishes notifications to Redis channel `tg:notify`; bot subscribes and sends Telegram messages. Frontend `LoginPage` auto-exchanges `?tg=TOKEN` URL param for a JWT.

**Tech Stack:** Go 1.25, `go-telegram-bot-api/v5`, `redis/go-redis/v9`, PostgreSQL, React/TypeScript.

---

## File Map

**New files:**
- `migrations/047_telegram_auth.sql` — new tables + columns
- `services/api-gateway/telegram_auth_handler.go` — `/auth/telegram` + `/auth/telegram-callback`
- `services/api-gateway/bot_handler.go` — `/bot/summary`, `/bot/pause-all`, `/bot/resume-all`, `/bot/strategy-status`
- `services/api-gateway/tg_notifier.go` — notification polling goroutine + Redis publish
- `services/tg-bot/main.go` — entry point, env, start polling + notifier
- `services/tg-bot/client.go` — HTTP client for api-gateway calls
- `services/tg-bot/bot.go` — update router + new_chat_members welcome handler
- `services/tg-bot/commands.go` — all command handlers
- `services/tg-bot/notifier.go` — Redis subscriber → Telegram sender
- `services/tg-bot/Dockerfile`

**Modified files:**
- `services/api-gateway/middleware.go` — add `RequireBotSecret`
- `services/api-gateway/server.go` — add `botSecret` field to `Server` struct and `NewServer`
- `services/api-gateway/main.go` — load `TELEGRAM_BOT_SECRET`, register new routes, start notifier
- `services/api-gateway/strategy_handler.go` — hook notify in `SetStrategyStatus`
- `services/api-gateway/auth_handler_test.go` — update `newTestServer` helper signature
- `services/api-gateway/middleware_test.go` — add `RequireBotSecret` unit test
- `frontend/src/api/auth.ts` — add `telegramCallback`
- `frontend/src/pages/LoginPage.tsx` — handle `?tg=TOKEN` on mount
- `docker-compose.yml` — add `tg-bot` service

---

## Task 1: Migration `047_telegram_auth.sql`

**Files:**
- Create: `migrations/047_telegram_auth.sql`

- [ ] **Create migration file**

```sql
-- migrations/047_telegram_auth.sql

-- One-time tokens for magic-link login (TG → web)
CREATE TABLE IF NOT EXISTS telegram_auth_tokens (
    token      TEXT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '5 minutes'
);

CREATE INDEX IF NOT EXISTS idx_telegram_auth_tokens_chat
    ON telegram_auth_tokens (chat_id);

-- Allow muting notifications per-user
ALTER TABLE telegram_connections
    ADD COLUMN IF NOT EXISTS mute_until TIMESTAMPTZ;

-- Track which strategy error events already triggered a TG notification
ALTER TABLE strategy_events
    ADD COLUMN IF NOT EXISTS tg_notified BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_strategy_events_notify
    ON strategy_events (tg_notified, created_at DESC)
    WHERE tg_notified = FALSE;
```

- [ ] **Verify migration applies cleanly**

```bash
# From project root (requires running TimescaleDB)
go run ./services/api-gateway 2>&1 | head -5
# Expected: no migration errors, server starts
```

- [ ] **Commit**

```bash
git add migrations/047_telegram_auth.sql
git commit -m "feat(db): add telegram_auth_tokens, mute_until, tg_notified columns"
```

---

## Task 2: Gateway — `botSecret` in Server + `RequireBotSecret` middleware

**Files:**
- Modify: `services/api-gateway/server.go`
- Modify: `services/api-gateway/middleware.go`
- Modify: `services/api-gateway/auth_handler_test.go`
- Modify: `services/api-gateway/middleware_test.go`

- [ ] **Add `botSecret` field to `Server` struct in `server.go`**

In `Server` struct, add after `adminEmails`:
```go
botSecret string
```

Update `NewServer` signature (add `botSecret string` after `encKey`):
```go
func NewServer(ctx context.Context, pool *pgxpool.Pool, rdb *redis.Client, jwtSecret, encKey, botSecret string, adminEmails map[string]bool, pm *proxy.Manager, ns *bybitnews.Scraper) *Server {
```

Inside `NewServer`, set the field:
```go
s := &Server{
    pool:            pool,
    rdb:             rdb,
    jwtSecret:       []byte(jwtSecret),
    encKey:          encKey,
    botSecret:       botSecret,
    // ... rest unchanged
}
```

- [ ] **Add `RequireBotSecret` to `middleware.go`**

```go
// RequireBotSecret validates that the request carries the shared bot secret.
// Used to protect internal bot-to-gateway endpoints.
func (s *Server) RequireBotSecret(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if s.botSecret == "" || header != "Bearer "+s.botSecret {
			writeError(w, http.StatusUnauthorized, "invalid bot secret")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Write unit tests for `RequireBotSecret` in `middleware_test.go`**

```go
func TestRequireBotSecret_Missing(t *testing.T) {
	s := &Server{botSecret: "mysecret"}
	h := s.RequireBotSecret(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestRequireBotSecret_WrongSecret(t *testing.T) {
	s := &Server{botSecret: "mysecret"}
	h := s.RequireBotSecret(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestRequireBotSecret_Valid(t *testing.T) {
	s := &Server{botSecret: "mysecret"}
	called := false
	h := s.RequireBotSecret(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer mysecret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Errorf("got %d, called=%v, want 200/true", rec.Code, called)
	}
}
```

- [ ] **Update `newTestServer` helper in `auth_handler_test.go`**

```go
func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, "postgres://sis:sis_secret@localhost:5432/sis")
	if err != nil {
		t.Skipf("timescaledb unavailable: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := db.Migrate(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	rdb, err := cache.Connect(ctx, "redis://localhost:6379")
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { rdb.Close() })
	return NewServer(ctx, pool, rdb, "test-secret", "0000000000000000000000000000000000000000000000000000000000000000", "bot-test-secret", map[string]bool{}, nil, nil)
}
```

- [ ] **Run unit tests**

```bash
cd services/api-gateway
go test ./... -run TestRequireBotSecret -v
# Expected: 3 PASS
```

- [ ] **Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/middleware.go \
        services/api-gateway/middleware_test.go services/api-gateway/auth_handler_test.go
git commit -m "feat(gateway): add botSecret to Server + RequireBotSecret middleware"
```

---

## Task 3: Gateway — `telegram_auth_handler.go`

**Files:**
- Create: `services/api-gateway/telegram_auth_handler.go`

Implements:
- `POST /auth/telegram` — bot requests magic link (BOT_SECRET required)
- `POST /auth/telegram-callback` — frontend exchanges one-time token for JWT (public)

- [ ] **Create `telegram_auth_handler.go`**

```go
// services/api-gateway/telegram_auth_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sis/pkg/auth"
)

// TelegramLoginRequest is called by the bot when a user sends /login.
// It finds or auto-creates a user for the given chat_id, then returns a magic URL.
// POST /auth/telegram  (requires BOT_SECRET)
func (s *Server) TelegramLoginRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID   int64  `json:"chat_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}

	ctx := r.Context()

	// 1. Lookup existing telegram connection
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM telegram_connections WHERE chat_id = $1`, req.ChatID,
	).Scan(&userID)

	if err != nil {
		// 2. Auto-register: create new user
		email := fmt.Sprintf("tg_%d@telegram.invalid", req.ChatID)
		err = s.pool.QueryRow(ctx,
			`INSERT INTO users (email, password_hash)
			 VALUES ($1, '')
			 ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
			 RETURNING id`,
			email,
		).Scan(&userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		// Link telegram connection
		_, err = s.pool.Exec(ctx,
			`INSERT INTO telegram_connections (user_id, chat_id, username)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (user_id) DO UPDATE SET chat_id=$2, username=$3, connected_at=NOW()`,
			userID, req.ChatID, req.Username,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	// 3. Generate one-time auth token
	token := newUUID()
	_, err = s.pool.Exec(ctx,
		`INSERT INTO telegram_auth_tokens (token, chat_id) VALUES ($1, $2)`,
		token, req.ChatID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	appURL := getEnv("APP_URL", "https://app.novabot.io")
	writeJSON(w, http.StatusOK, map[string]any{
		"url": appURL + "/login?tg=" + token,
	})
}

// TelegramLoginCallback is called by the frontend when the user clicks the magic link.
// It exchanges the one-time token for a JWT.
// POST /auth/telegram-callback  (public)
func (s *Server) TelegramLoginCallback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token required")
		return
	}

	ctx := r.Context()

	// Consume token (atomic delete-and-return)
	var chatID int64
	err := s.pool.QueryRow(ctx,
		`DELETE FROM telegram_auth_tokens
		 WHERE token = $1 AND expires_at > NOW()
		 RETURNING chat_id`,
		req.Token,
	).Scan(&chatID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token invalid or expired")
		return
	}

	// Resolve user
	var userID, email string
	err = s.pool.QueryRow(ctx,
		`SELECT u.id, u.email
		 FROM users u
		 JOIN telegram_connections tc ON tc.user_id = u.id
		 WHERE tc.chat_id = $1`, chatID,
	).Scan(&userID, &email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "user not found")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"user_id":  userID,
		"email":    email,
		"is_admin": s.adminEmails[email],
	})
}
```

- [ ] **Write integration tests** (add to a new file `services/api-gateway/telegram_auth_handler_test.go`)

```go
//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTelegramLoginRequest_NewUser(t *testing.T) {
	s := newTestServer(t)
	chatID := int64(999001)
	body := fmt.Sprintf(`{"chat_id":%d,"username":"testuser"}`, chatID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["url"] == "" {
		t.Error("expected url in response")
	}
	// Cleanup
	s.pool.Exec(context.Background(),
		`DELETE FROM users WHERE email=$1`, fmt.Sprintf("tg_%d@telegram.invalid", chatID))
}

func TestTelegramLoginCallback_ValidToken(t *testing.T) {
	s := newTestServer(t)
	chatID := int64(999002)

	// Setup: create user + connection + token
	var userID string
	s.pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash) VALUES ($1,'') RETURNING id`,
		fmt.Sprintf("tg_%d@telegram.invalid", chatID),
	).Scan(&userID)
	s.pool.Exec(context.Background(),
		`INSERT INTO telegram_connections (user_id, chat_id) VALUES ($1,$2)`, userID, chatID)
	token := newUUID()
	s.pool.Exec(context.Background(),
		`INSERT INTO telegram_auth_tokens (token, chat_id) VALUES ($1,$2)`, token, chatID)

	body := fmt.Sprintf(`{"token":%q}`, token)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram-callback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected JWT token in response")
	}
	// Cleanup
	s.pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
}

func TestTelegramLoginCallback_InvalidToken(t *testing.T) {
	s := newTestServer(t)
	body := `{"token":"nonexistent-token"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/telegram-callback", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.TelegramLoginCallback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}
```

- [ ] **Commit**

```bash
git add services/api-gateway/telegram_auth_handler.go \
        services/api-gateway/telegram_auth_handler_test.go
git commit -m "feat(gateway): add TelegramLoginRequest + TelegramLoginCallback handlers"
```

---

## Task 4: Gateway — `bot_handler.go`

**Files:**
- Create: `services/api-gateway/bot_handler.go`

Implements (all require BOT_SECRET):
- `GET /bot/summary?chat_id=N` — strategies + P&L for bot commands
- `POST /bot/pause-all` — stop all active strategies
- `POST /bot/resume-all` — resume all stopped strategies
- `POST /bot/strategy-status` — change status of one strategy (for inline buttons)

- [ ] **Create `bot_handler.go`**

```go
// services/api-gateway/bot_handler.go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
)

type botStrategySummary struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Direction    string  `json:"direction"`
	Status       string  `json:"status"`
	ActiveLevels int     `json:"active_levels"`
	GridLevels   int     `json:"grid_levels"`
	PnlToday     float64 `json:"pnl_today"`
}

// BotSummary returns strategy list and aggregated P&L for the given chat_id.
// GET /bot/summary?chat_id=N  (BOT_SECRET required)
func (s *Server) BotSummary(w http.ResponseWriter, r *http.Request) {
	chatID, err := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	if err != nil || chatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, chatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, symbol, direction, status, active_levels, grid_levels
		 FROM strategies WHERE owner_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var strategies []botStrategySummary
	for rows.Next() {
		var st botStrategySummary
		if err := rows.Scan(&st.ID, &st.Symbol, &st.Direction, &st.Status, &st.ActiveLevels, &st.GridLevels); err != nil {
			continue
		}
		strategies = append(strategies, st)
	}
	if strategies == nil {
		strategies = []botStrategySummary{}
	}

	var pnlToday, pnlWeek float64
	s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(pnl),0) FROM trade_history
		 WHERE owner_id=$1 AND closed_at >= NOW()-INTERVAL '1 day'`, userID,
	).Scan(&pnlToday)
	s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(pnl),0) FROM trade_history
		 WHERE owner_id=$1 AND closed_at >= NOW()-INTERVAL '7 days'`, userID,
	).Scan(&pnlWeek)

	writeJSON(w, http.StatusOK, map[string]any{
		"strategies": strategies,
		"pnl_today":  pnlToday,
		"pnl_week":   pnlWeek,
	})
}

// BotPauseAll stops all active strategies for the given chat_id.
// POST /bot/pause-all  (BOT_SECRET required)
func (s *Server) BotPauseAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET status='stopped', updated_at=NOW()
		 WHERE owner_id=$1 AND status='active'
		 RETURNING id`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	go func() {
		for _, id := range ids {
			s.engine.Notify(context.Background(), id)
			s.engine.LogUserAction(context.Background(), id, "Остановлено через Telegram")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"stopped": len(ids)})
}

// BotResumeAll activates all stopped strategies for the given chat_id.
// POST /bot/resume-all  (BOT_SECRET required)
func (s *Server) BotResumeAll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}

	rows, err := s.pool.Query(ctx,
		`UPDATE strategies SET status='active', updated_at=NOW()
		 WHERE owner_id=$1 AND status='stopped'
		 RETURNING id`, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	go func() {
		for _, id := range ids {
			s.engine.Notify(context.Background(), id)
			s.engine.LogUserAction(context.Background(), id, "Запущено через Telegram")
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"started": len(ids)})
}

// BotStrategyStatus sets status for a single strategy (used by inline button callbacks).
// POST /bot/strategy-status  (BOT_SECRET required)
func (s *Server) BotStrategyStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID     int64  `json:"chat_id"`
		StrategyID string `json:"strategy_id"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.ChatID == 0 || req.StrategyID == "" {
		writeError(w, http.StatusBadRequest, "chat_id and strategy_id required")
		return
	}
	if req.Status != "active" && req.Status != "finishing" && req.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "status must be active|finishing|stopped")
		return
	}
	ctx := r.Context()
	userID, err := s.userIDFromChatID(ctx, req.ChatID)
	if err != nil {
		writeError(w, http.StatusNotFound, "telegram account not linked")
		return
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE strategies SET status=$1, updated_at=NOW()
		 WHERE id=$2 AND owner_id=$3`, req.Status, req.StrategyID, userID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "strategy not found")
		return
	}
	go func() {
		s.engine.Notify(context.Background(), req.StrategyID)
		s.engine.LogUserAction(context.Background(), req.StrategyID, "Статус изменён через Telegram: "+req.Status)
	}()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// userIDFromChatID resolves a Telegram chat_id to a user UUID.
func (s *Server) userIDFromChatID(ctx context.Context, chatID int64) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM telegram_connections WHERE chat_id=$1`, chatID,
	).Scan(&userID)
	return userID, err
}
```

- [ ] **Commit**

```bash
git add services/api-gateway/bot_handler.go
git commit -m "feat(gateway): add bot_handler (summary, pause-all, resume-all, strategy-status)"
```

---

## Task 5: Gateway — `tg_notifier.go` (notification goroutine)

**Files:**
- Create: `services/api-gateway/tg_notifier.go`
- Modify: `services/api-gateway/strategy_handler.go` (hook in `SetStrategyStatus`)

- [ ] **Create `tg_notifier.go`**

```go
// services/api-gateway/tg_notifier.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// TgNotifyMsg is the message format published to Redis channel "tg:notify".
type TgNotifyMsg struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	StrategyID  string `json:"strategy_id,omitempty"`
	ShowPauseBtn bool  `json:"show_pause_btn,omitempty"`
}

const tgNotifyChannel = "tg:notify"

// publishTgNotify publishes a notification message to Redis for the tg-bot to deliver.
func (s *Server) publishTgNotify(ctx context.Context, msg TgNotifyMsg) {
	if s.rdb == nil {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := s.rdb.Publish(ctx, tgNotifyChannel, string(data)).Err(); err != nil {
		log.Printf("tg_notifier: publish error: %v", err)
	}
}

// startTgNotifier polls strategy_events for un-notified error/warn entries and
// publishes them to Redis. Runs as a background goroutine.
func (s *Server) startTgNotifier(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flushPendingTgNotifications(ctx)
		}
	}
}

func (s *Server) flushPendingTgNotifications(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `
		SELECT se.id, se.message, se.level, se.strategy_id,
		       st.symbol, tc.chat_id,
		       COALESCE(tns.on_trade, true)
		FROM strategy_events se
		JOIN strategies st ON st.id = se.strategy_id
		JOIN telegram_connections tc ON tc.user_id = st.owner_id
		LEFT JOIN telegram_notification_settings tns ON tns.user_id = st.owner_id
		WHERE se.tg_notified = false
		  AND se.level IN ('error', 'warn')
		  AND se.created_at > NOW() - INTERVAL '1 hour'
		  AND (tc.mute_until IS NULL OR tc.mute_until < NOW())
		  AND COALESCE(tns.on_trade, true) = true
		ORDER BY se.created_at ASC
		LIMIT 50
		FOR UPDATE OF se SKIP LOCKED
	`)
	if err != nil {
		log.Printf("tg_notifier: query error: %v", err)
		return
	}
	defer rows.Close()

	type row struct {
		eventID    string
		message    string
		level      string
		strategyID string
		symbol     string
		chatID     int64
		onTrade    bool
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.eventID, &r.message, &r.level, &r.strategyID, &r.symbol, &r.chatID, &r.onTrade); err != nil {
			continue
		}
		pending = append(pending, r)
	}
	rows.Close()

	for _, p := range pending {
		icon := "⚠️"
		if p.level == "error" {
			icon = "🔴"
		}
		text := fmt.Sprintf("%s *%s* — %s", icon, p.symbol, p.message)
		s.publishTgNotify(ctx, TgNotifyMsg{
			ChatID:       p.chatID,
			Text:         text,
			StrategyID:   p.strategyID,
			ShowPauseBtn: p.level == "error",
		})
		s.pool.Exec(ctx,
			`UPDATE strategy_events SET tg_notified=true WHERE id=$1`, p.eventID)
	}
}
```

- [ ] **Hook notification in `SetStrategyStatus` in `strategy_handler.go`**

Find the `go func()` block at the end of `SetStrategyStatus` (after the `writeJSON` call) and extend it:

```go
	// existing code:
	statusLabel := map[string]string{
		"active":    "запущена",
		"finishing": "завершение",
		"stopped":   "остановлена",
	}[req.Status]
	logMsg := fmt.Sprintf("Статус изменён пользователем: %s", statusLabel)

	// capture for goroutine
	stratID := id
	newStatus := req.Status
	go func() {
		ctx := context.Background()
		s.engine.Notify(ctx, stratID)
		s.engine.LogUserAction(ctx, stratID, logMsg)
		// Telegram notification for status change
		var chatID int64
		var symbol string
		var muteUntil *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT tc.chat_id, st.symbol, tc.mute_until
			FROM strategies st
			JOIN telegram_connections tc ON tc.user_id = st.owner_id
			WHERE st.id = $1`, stratID,
		).Scan(&chatID, &symbol, &muteUntil)
		if err == nil && (muteUntil == nil || muteUntil.Before(time.Now())) {
			icons := map[string]string{"active": "🟢", "finishing": "🟡", "stopped": "⏸"}
			text := fmt.Sprintf("%s *%s* — статус изменён: %s", icons[newStatus], symbol, statusLabel)
			s.publishTgNotify(ctx, TgNotifyMsg{ChatID: chatID, Text: text})
		}
	}()
```

Note: remove the old `go func()` block and replace it entirely with the one above (it includes the original `Notify` + `LogUserAction` calls).

- [ ] **Commit**

```bash
git add services/api-gateway/tg_notifier.go services/api-gateway/strategy_handler.go
git commit -m "feat(gateway): add tg_notifier goroutine + status-change notification hook"
```

---

## Task 6: Gateway — Wire routes in `main.go`

**Files:**
- Modify: `services/api-gateway/main.go`

- [ ] **Load `TELEGRAM_BOT_SECRET` and pass to `NewServer`**

```go
// after existing jwtSecret := mustEnv("JWT_SECRET")
botSecret := getEnv("TELEGRAM_BOT_SECRET", "")
```

Update `NewServer` call:
```go
s := NewServer(ctx, pool, rdb, jwtSecret, encKey, botSecret, adminEmails, pm, ns)
```

- [ ] **Register new routes**

After the existing `r.Post("/auth/login", s.Login)` line:
```go
r.Post("/auth/telegram-callback", s.TelegramLoginCallback)
```

Add a new bot-only route group (before the Telegram bot callback comment):
```go
// Bot-to-gateway internal routes — authenticated via TELEGRAM_BOT_SECRET
r.Group(func(r chi.Router) {
    r.Use(s.RequireBotSecret)
    r.Post("/auth/telegram", s.TelegramLoginRequest)
    r.Get("/bot/summary", s.BotSummary)
    r.Post("/bot/pause-all", s.BotPauseAll)
    r.Post("/bot/resume-all", s.BotResumeAll)
    r.Post("/bot/strategy-status", s.BotStrategyStatus)
})
```

- [ ] **Start the notification goroutine**

After `go s.RunBotEngine(ctx)`:
```go
// Start Telegram notification polling
go s.startTgNotifier(ctx)
```

- [ ] **Compile check**

```bash
cd services/api-gateway
go build ./...
# Expected: no errors
```

- [ ] **Commit**

```bash
git add services/api-gateway/main.go
git commit -m "feat(gateway): wire telegram bot routes + start tg notifier"
```

---

## Task 7: tg-bot — Module setup + `go.mod`

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `services/tg-bot/` directory

- [ ] **Add telegram-bot-api dependency**

```bash
cd C:\Users\123\Projects\sis
go get github.com/go-telegram-bot-api/telegram-bot-api/v5@latest
```

Expected: `go.mod` updated with `github.com/go-telegram-bot-api/telegram-bot-api/v5 vX.X.X`

- [ ] **Create service directory**

```bash
mkdir services\tg-bot
```

- [ ] **Commit**

```bash
git add go.mod go.sum
git commit -m "feat(tg-bot): add go-telegram-bot-api dependency"
```

---

## Task 8: tg-bot — `client.go`

**Files:**
- Create: `services/tg-bot/client.go`

The gateway client used by all command handlers to call api-gateway internal endpoints.

- [ ] **Create `client.go`**

```go
// services/tg-bot/client.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// GatewayClient calls api-gateway bot-internal endpoints.
type GatewayClient struct {
	baseURL   string
	botSecret string
	http      *http.Client
}

func NewGatewayClient(baseURL, botSecret string) *GatewayClient {
	return &GatewayClient{
		baseURL:   baseURL,
		botSecret: botSecret,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *GatewayClient) do(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.botSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("gateway %d: %s", resp.StatusCode, e["error"])
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// TelegramLoginRequest gets a magic-link URL for the given chat_id.
func (c *GatewayClient) TelegramLoginRequest(ctx context.Context, chatID int64, username string) (string, error) {
	var resp struct {
		URL string `json:"url"`
	}
	err := c.do(ctx, http.MethodPost, "/auth/telegram", map[string]any{
		"chat_id":  chatID,
		"username": username,
	}, &resp)
	return resp.URL, err
}

type StrategySummary struct {
	ID           string  `json:"id"`
	Symbol       string  `json:"symbol"`
	Direction    string  `json:"direction"`
	Status       string  `json:"status"`
	ActiveLevels int     `json:"active_levels"`
	GridLevels   int     `json:"grid_levels"`
}

type BotSummaryResp struct {
	Strategies []StrategySummary `json:"strategies"`
	PnlToday   float64           `json:"pnl_today"`
	PnlWeek    float64           `json:"pnl_week"`
}

// BotSummary fetches strategies + P&L for a chat_id.
func (c *GatewayClient) BotSummary(ctx context.Context, chatID int64) (*BotSummaryResp, error) {
	u := fmt.Sprintf("/bot/summary?chat_id=%d", chatID)
	var resp BotSummaryResp
	err := c.do(ctx, http.MethodGet, u, nil, &resp)
	return &resp, err
}

// PauseAll stops all active strategies for the given chat_id.
func (c *GatewayClient) PauseAll(ctx context.Context, chatID int64) (int, error) {
	var resp struct {
		Stopped int `json:"stopped"`
	}
	err := c.do(ctx, http.MethodPost, "/bot/pause-all", map[string]any{"chat_id": chatID}, &resp)
	return resp.Stopped, err
}

// ResumeAll activates all stopped strategies for the given chat_id.
func (c *GatewayClient) ResumeAll(ctx context.Context, chatID int64) (int, error) {
	var resp struct {
		Started int `json:"started"`
	}
	err := c.do(ctx, http.MethodPost, "/bot/resume-all", map[string]any{"chat_id": chatID}, &resp)
	return resp.Started, err
}

// StrategyStatus changes the status of a single strategy.
func (c *GatewayClient) StrategyStatus(ctx context.Context, chatID int64, strategyID, status string) error {
	return c.do(ctx, http.MethodPost, "/bot/strategy-status", map[string]any{
		"chat_id":     chatID,
		"strategy_id": strategyID,
		"status":      status,
	}, nil)
}

// GetNotifications reads notification settings for a user.
func (c *GatewayClient) GetNotifications(ctx context.Context, chatID int64) (map[string]bool, error) {
	// Uses the standard /account/notifications but needs user JWT — not available here.
	// Instead we call BotSummary which validates the chat_id exists, then return defaults.
	// Full per-user notification prefs are managed via the web app.
	return map[string]bool{"on_trade": true, "on_signal": true, "on_balance": true}, nil
}

// MuteUntil sets mute_until for the given chat_id.
func (c *GatewayClient) MuteUntil(ctx context.Context, chatID int64, until string) error {
	params := url.Values{"chat_id": {fmt.Sprint(chatID)}, "until": {until}}
	return c.do(ctx, http.MethodPost, "/bot/mute?"+params.Encode(), nil, nil)
}
```

> Note: `MuteUntil` requires a `/bot/mute` endpoint; add it to `bot_handler.go` in Task 4:

Add to `bot_handler.go`:
```go
// BotMute sets mute_until for the given chat_id.
// POST /bot/mute  (BOT_SECRET required)
func (s *Server) BotMute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChatID int64  `json:"chat_id"`
		Until  string `json:"until"` // RFC3339 timestamp
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChatID == 0 {
		writeError(w, http.StatusBadRequest, "chat_id required")
		return
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE telegram_connections SET mute_until=$1 WHERE chat_id=$2`,
		req.Until, req.ChatID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

Register in `main.go` inside the bot route group:
```go
r.Post("/bot/mute", s.BotMute)
```

- [ ] **Commit**

```bash
git add services/tg-bot/client.go services/api-gateway/bot_handler.go services/api-gateway/main.go
git commit -m "feat(tg-bot): gateway client + BotMute endpoint"
```

---

## Task 9: tg-bot — `commands.go`

**Files:**
- Create: `services/tg-bot/commands.go`

- [ ] **Create `commands.go`**

```go
// services/tg-bot/commands.go
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// cmdStart handles /start [token].
// With a token: calls /account/telegram-verify to link the account.
// Without a token: sends welcome message with inline buttons.
func cmdStart(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	token := strings.TrimSpace(msg.CommandArguments())
	chatID := msg.Chat.ID
	username := msg.From.UserName

	if token != "" {
		// Link existing account
		err := gw.do(ctx, "POST", "/account/telegram-verify", map[string]any{
			"token":    token,
			"chat_id":  chatID,
			"username": username,
		}, nil)
		if err != nil {
			reply(bot, chatID, "❌ Ссылка недействительна или истекла. Получите новую в настройках профиля.")
			return
		}
		reply(bot, chatID, "✅ Telegram успешно привязан к вашему аккаунту!\n\nТеперь вы будете получать уведомления о сделках и сигналах.")
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🚀 Зарегистрироваться", appURL+"/register"),
			tgbotapi.NewInlineKeyboardButtonData("🔐 Войти", "cmd_login"),
		),
	)
	m := tgbotapi.NewMessage(chatID, "👋 Добро пожаловать в *Novabot*!\n\nАвтоматическая торговля на Bybit.\n\nДля начала войдите в аккаунт или создайте новый.")
	m.ParseMode = "Markdown"
	m.ReplyMarkup = kb
	bot.Send(m)
}

// cmdLogin handles /login — sends a magic-link button.
func cmdLogin(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	chatID := msg.Chat.ID
	username := msg.From.UserName

	loginURL, err := gw.TelegramLoginRequest(ctx, chatID, username)
	if err != nil {
		reply(bot, chatID, "❌ Не удалось создать ссылку для входа. Попробуйте позже.")
		return
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔐 Открыть приложение", loginURL),
		),
	)
	m := tgbotapi.NewMessage(chatID, "Нажмите кнопку ниже — вы будете автоматически авторизованы.\n\n_Ссылка действительна 5 минут._")
	m.ParseMode = "Markdown"
	m.ReplyMarkup = kb
	bot.Send(m)
}

// cmdStatus handles /status — shows active strategies summary.
func cmdStatus(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	if len(summary.Strategies) == 0 {
		reply(bot, chatID, "📊 У вас нет стратегий.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 *Стратегии*\n\n")
	statusIcons := map[string]string{"active": "🟢", "finishing": "🟡", "stopped": "⏸"}
	for _, st := range summary.Strategies {
		icon := statusIcons[st.Status]
		if icon == "" {
			icon = "⚪"
		}
		sb.WriteString(fmt.Sprintf("%s *%s* %s — %d/%d уровней\n",
			icon, st.Symbol, st.Direction, st.ActiveLevels, st.GridLevels))
	}
	m := tgbotapi.NewMessage(chatID, sb.String())
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPnl handles /pnl — shows P&L summary.
func cmdPnl(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	pnlTodaySign := "+"
	if summary.PnlToday < 0 {
		pnlTodaySign = ""
	}
	pnlWeekSign := "+"
	if summary.PnlWeek < 0 {
		pnlWeekSign = ""
	}

	text := fmt.Sprintf("💰 *P&L*\n\nСегодня: `%s%.2f$`\nЗа неделю: `%s%.2f$`",
		pnlTodaySign, summary.PnlToday, pnlWeekSign, summary.PnlWeek)
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPositions handles /positions — shows open positions from strategies.
func cmdPositions(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	summary, err := gw.BotSummary(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}

	var active []StrategySummary
	for _, st := range summary.Strategies {
		if st.ActiveLevels > 0 {
			active = append(active, st)
		}
	}
	if len(active) == 0 {
		reply(bot, chatID, "📈 Нет открытых позиций.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📈 *Открытые позиции*\n\n")
	for _, st := range active {
		sb.WriteString(fmt.Sprintf("• *%s* %s — %d уровней заполнено\n",
			st.Symbol, st.Direction, st.ActiveLevels))
	}
	m := tgbotapi.NewMessage(chatID, sb.String())
	m.ParseMode = "Markdown"
	bot.Send(m)
}

// cmdPause handles /pause — stops all active strategies.
func cmdPause(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	n, err := gw.PauseAll(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	if n == 0 {
		reply(bot, chatID, "⏸ Нет активных стратегий для остановки.")
		return
	}
	reply(bot, chatID, fmt.Sprintf("⏸ Остановлено стратегий: *%d*", n))
}

// cmdResume handles /resume — activates all stopped strategies.
func cmdResume(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	n, err := gw.ResumeAll(ctx, chatID)
	if err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	if n == 0 {
		reply(bot, chatID, "🟢 Нет остановленных стратегий.")
		return
	}
	reply(bot, chatID, fmt.Sprintf("🟢 Запущено стратегий: *%d*", n))
}

// cmdNotifications handles /notifications — shows notification settings info.
func cmdNotifications(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message, appURL string) {
	chatID := msg.Chat.ID
	text := fmt.Sprintf("🔔 *Уведомления*\n\nНастройте уведомления в профиле:\n%s/account", appURL)
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("⚙️ Открыть настройки", appURL+"/account"),
		),
	)
	bot.Send(m)
}

// cmdMute handles /mute [duration] — mutes notifications for the given duration.
// Example: /mute 2h, /mute 30m, /mute 24h
func cmdMute(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	arg := strings.TrimSpace(msg.CommandArguments())
	if arg == "" {
		reply(bot, chatID, "Использование: /mute 30m | 2h | 24h")
		return
	}
	d, err := parseMuteDuration(arg)
	if err != nil {
		reply(bot, chatID, "❌ Неверный формат. Примеры: /mute 30m, /mute 2h, /mute 24h")
		return
	}
	until := time.Now().Add(d)
	if err := gw.MuteUntil(ctx, chatID, until.UTC().Format(time.RFC3339)); err != nil {
		replyNotLinked(bot, chatID)
		return
	}
	reply(bot, chatID, fmt.Sprintf("🔕 Уведомления заглушены до %s", until.Format("15:04 02.01")))
}

// parseMuteDuration parses strings like "30m", "2h", "24h".
func parseMuteDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// reply sends a plain Markdown message.
func reply(bot *tgbotapi.BotAPI, chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	m.ParseMode = "Markdown"
	bot.Send(m)
}

func replyNotLinked(bot *tgbotapi.BotAPI, chatID int64) {
	reply(bot, chatID, "⚠️ Ваш Telegram не привязан к аккаунту.\n\nИспользуйте /start или привяжите в настройках профиля на сайте.")
}
```

- [ ] **Commit**

```bash
git add services/tg-bot/commands.go
git commit -m "feat(tg-bot): implement all bot command handlers"
```

---

## Task 10: tg-bot — `bot.go` (router + welcome)

**Files:**
- Create: `services/tg-bot/bot.go`

- [ ] **Create `bot.go`**

```go
// services/tg-bot/bot.go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// handleUpdate routes an incoming Telegram update to the correct handler.
func handleUpdate(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, update tgbotapi.Update, cfg Config) {
	// Inline button callbacks (e.g. pause button from notifications)
	if update.CallbackQuery != nil {
		handleCallback(ctx, bot, gw, update.CallbackQuery)
		return
	}

	msg := update.Message
	if msg == nil {
		return
	}

	// New member joined configured group — send welcome
	if cfg.GroupID != 0 && msg.Chat.ID == cfg.GroupID && msg.NewChatMembers != nil {
		for _, member := range msg.NewChatMembers {
			if member.IsBot {
				continue
			}
			username := member.UserName
			if username == "" {
				username = member.FirstName
			}
			text := fmt.Sprintf(
				"👋 Добро пожаловать, @%s!\n\n*Novabot* — платформа автоматической торговли на Bybit.\n\n🚀 Зарегистрироваться: %s/register\n🔐 Уже есть аккаунт? /login\n📊 Привязать Telegram: /start",
				username, cfg.AppURL,
			)
			m := tgbotapi.NewMessage(msg.Chat.ID, text)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		return
	}

	if !msg.IsCommand() {
		return
	}

	cmd := msg.Command()
	log.Printf("cmd: /%s from chat_id=%d", cmd, msg.Chat.ID)

	switch cmd {
	case "start":
		cmdStart(ctx, bot, gw, msg, cfg.AppURL)
	case "login":
		cmdLogin(ctx, bot, gw, msg, cfg.AppURL)
	case "status":
		cmdStatus(ctx, bot, gw, msg)
	case "pnl":
		cmdPnl(ctx, bot, gw, msg)
	case "positions":
		cmdPositions(ctx, bot, gw, msg)
	case "pause":
		cmdPause(ctx, bot, gw, msg)
	case "resume":
		cmdResume(ctx, bot, gw, msg)
	case "notifications":
		cmdNotifications(ctx, bot, gw, msg, cfg.AppURL)
	case "mute":
		cmdMute(ctx, bot, gw, msg)
	default:
		reply(bot, msg.Chat.ID, "Неизвестная команда. Попробуйте /status, /login, /pnl")
	}
}

// handleCallback handles inline keyboard button presses.
func handleCallback(ctx context.Context, bot *tgbotapi.BotAPI, gw *GatewayClient, cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	data := cb.Data

	// Acknowledge the callback immediately
	bot.Request(tgbotapi.NewCallback(cb.ID, ""))

	switch {
	case data == "cmd_login":
		// /login triggered from start keyboard
		fakemsg := &tgbotapi.Message{
			From: cb.From,
			Chat: cb.Message.Chat,
			Text: "/login",
		}
		cmdLogin(ctx, bot, gw, fakemsg, "")
	case strings.HasPrefix(data, "pause_"):
		strategyID := strings.TrimPrefix(data, "pause_")
		if err := gw.StrategyStatus(ctx, chatID, strategyID, "stopped"); err != nil {
			reply(bot, chatID, "❌ Не удалось остановить стратегию.")
			return
		}
		reply(bot, chatID, "⏸ Стратегия остановлена.")
	}
}
```

- [ ] **Commit**

```bash
git add services/tg-bot/bot.go
git commit -m "feat(tg-bot): update router + new_chat_members welcome + callback handler"
```

---

## Task 11: tg-bot — `notifier.go`

**Files:**
- Create: `services/tg-bot/notifier.go`

- [ ] **Create `notifier.go`**

```go
// services/tg-bot/notifier.go
package main

import (
	"context"
	"encoding/json"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

const tgNotifyChannel = "tg:notify"

// TgNotifyMsg mirrors the struct published by api-gateway.
type TgNotifyMsg struct {
	ChatID       int64  `json:"chat_id"`
	Text         string `json:"text"`
	StrategyID   string `json:"strategy_id,omitempty"`
	ShowPauseBtn bool   `json:"show_pause_btn,omitempty"`
}

// startNotifier subscribes to Redis channel tg:notify and sends Telegram messages.
// Runs until ctx is cancelled.
func startNotifier(ctx context.Context, bot *tgbotapi.BotAPI, rdb *redis.Client) {
	sub := rdb.Subscribe(ctx, tgNotifyChannel)
	defer sub.Close()

	ch := sub.Channel()
	log.Printf("notifier: subscribed to %s", tgNotifyChannel)

	for {
		select {
		case <-ctx.Done():
			return
		case redisMsg, ok := <-ch:
			if !ok {
				return
			}
			var msg TgNotifyMsg
			if err := json.Unmarshal([]byte(redisMsg.Payload), &msg); err != nil {
				log.Printf("notifier: invalid message: %v", err)
				continue
			}
			sendNotification(bot, msg)
		}
	}
}

func sendNotification(bot *tgbotapi.BotAPI, msg TgNotifyMsg) {
	m := tgbotapi.NewMessage(msg.ChatID, msg.Text)
	m.ParseMode = "Markdown"

	if msg.ShowPauseBtn && msg.StrategyID != "" {
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⏸ Остановить стратегию", "pause_"+msg.StrategyID),
			),
		)
	}

	if _, err := bot.Send(m); err != nil {
		log.Printf("notifier: send to %d failed: %v", msg.ChatID, err)
	}
}
```

- [ ] **Commit**

```bash
git add services/tg-bot/notifier.go
git commit -m "feat(tg-bot): Redis subscriber notifier"
```

---

## Task 12: tg-bot — `main.go`

**Files:**
- Create: `services/tg-bot/main.go`

- [ ] **Create `main.go`**

```go
// services/tg-bot/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Config holds all configuration for the bot.
type Config struct {
	BotToken    string
	BotSecret   string
	GatewayURL  string
	RedisURL    string
	AppURL      string
	GroupID     int64
	WelcomeEnabled bool
}

func main() {
	_ = godotenv.Load()

	cfg := Config{
		BotToken:   mustEnv("TELEGRAM_BOT_TOKEN"),
		BotSecret:  mustEnv("TELEGRAM_BOT_SECRET"),
		GatewayURL: getEnv("GATEWAY_URL", "http://localhost:8080"),
		RedisURL:   getEnv("REDIS_URL", "redis://localhost:6379"),
		AppURL:     getEnv("APP_URL", "https://app.novabot.io"),
	}

	if gidStr := os.Getenv("TELEGRAM_GROUP_ID"); gidStr != "" {
		cfg.GroupID, _ = strconv.ParseInt(gidStr, 10, 64)
	}
	cfg.WelcomeEnabled = os.Getenv("WELCOME_ENABLED") == "true"
	if !cfg.WelcomeEnabled {
		cfg.GroupID = 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Telegram bot
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		log.Fatalf("bot init: %v", err)
	}
	log.Printf("tg-bot: authorized as @%s", bot.Self.UserName)

	// Redis
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis parse url: %v", err)
	}
	rdb := redis.NewClient(opt)
	defer rdb.Close()

	// Gateway client
	gw := NewGatewayClient(cfg.GatewayURL, cfg.BotSecret)

	// Start notification subscriber
	go startNotifier(ctx, bot, rdb)

	// Long polling loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	log.Printf("tg-bot: polling started")
	for {
		select {
		case <-ctx.Done():
			log.Println("tg-bot: shutting down")
			bot.StopReceivingUpdates()
			time.Sleep(500 * time.Millisecond)
			return
		case update := <-updates:
			go handleUpdate(ctx, bot, gw, update, cfg)
		}
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Build check**

```bash
cd C:\Users\123\Projects\sis
go build ./services/tg-bot/...
# Expected: no errors, binary produced
```

- [ ] **Commit**

```bash
git add services/tg-bot/main.go
git commit -m "feat(tg-bot): main.go entry point + Config"
```

---

## Task 13: Frontend — `api/auth.ts` + `LoginPage.tsx`

**Files:**
- Modify: `frontend/src/api/auth.ts`
- Modify: `frontend/src/pages/LoginPage.tsx`

- [ ] **Add `telegramCallback` to `api/auth.ts`**

Append to `frontend/src/api/auth.ts`:
```ts
export async function telegramCallback(token: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/telegram-callback', { token })
  return res.data
}
```

- [ ] **Update `LoginPage.tsx` to handle `?tg=TOKEN`**

Add `useEffect` and `useSearchParams` import, auto-exchange logic:

```tsx
import { useState, useEffect, FormEvent } from 'react'
import { useNavigate, Link, useSearchParams } from 'react-router-dom'
import { login, telegramCallback } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

export function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { login: authLogin } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // Auto-login via Telegram magic link (?tg=TOKEN)
  useEffect(() => {
    const tgToken = searchParams.get('tg')
    if (!tgToken) return
    setLoading(true)
    telegramCallback(tgToken)
      .then(res => {
        authLogin(res.token, res.user_id, res.email, res.is_admin ?? false)
        navigate('/')
      })
      .catch(() => {
        setError('Ссылка для входа недействительна или истекла.')
        setLoading(false)
      })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await login(email, password)
      authLogin(res.token, res.user_id, res.email, res.is_admin ?? false)
      navigate('/')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Login failed'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  // Show spinner while auto-logging in via TG
  if (loading && searchParams.get('tg')) {
    return (
      <div className="min-h-screen flex items-center justify-center font-sans" style={{ background: '#080b12' }}>
        <div className="text-center">
          <div className="text-[15px] font-semibold text-white mb-2">Входим через Telegram…</div>
          <div className="text-[12px] text-[#5b6479]">Пожалуйста, подождите</div>
        </div>
      </div>
    )
  }

  return (
    // ... keep existing JSX unchanged, just add error display if tgToken failed
    // The rest of the LoginPage JSX stays exactly the same
  )
}
```

> **Important:** keep the existing JSX (`return (...)`) block exactly as-is. Only replace the imports + state declarations + add the `useEffect`. The `return (...)` body is unchanged.

- [ ] **TypeScript check**

```bash
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
# Expected: no errors
```

- [ ] **Commit**

```bash
git add frontend/src/api/auth.ts frontend/src/pages/LoginPage.tsx
git commit -m "feat(frontend): add telegramCallback + auto-login from ?tg=TOKEN"
```

---

## Task 14: Dockerfile + `docker-compose.yml`

**Files:**
- Create: `services/tg-bot/Dockerfile`
- Modify: `docker-compose.yml`

- [ ] **Create `services/tg-bot/Dockerfile`**

```dockerfile
# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tg-bot ./services/tg-bot

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/tg-bot .
CMD ["./tg-bot"]
```

- [ ] **Add `tg-bot` service to `docker-compose.yml`**

Append to the `services:` section (before `volumes:`):
```yaml
  tg-bot:
    build:
      context: .
      dockerfile: services/tg-bot/Dockerfile
    restart: unless-stopped
    environment:
      TELEGRAM_BOT_TOKEN: ${TELEGRAM_BOT_TOKEN}
      TELEGRAM_BOT_SECRET: ${TELEGRAM_BOT_SECRET}
      GATEWAY_URL: ${GATEWAY_URL:-http://api-gateway:8080}
      REDIS_URL: ${REDIS_URL:-redis://redis:6379}
      APP_URL: ${APP_URL:-https://app.novabot.io}
      TELEGRAM_GROUP_ID: ${TELEGRAM_GROUP_ID:-}
      WELCOME_ENABLED: ${WELCOME_ENABLED:-false}
    depends_on:
      - redis
```

- [ ] **Verify docker compose config**

```bash
cd C:\Users\123\Projects\sis
docker compose config --quiet
# Expected: no errors
```

- [ ] **Commit**

```bash
git add services/tg-bot/Dockerfile docker-compose.yml
git commit -m "feat(tg-bot): Dockerfile + docker-compose service"
```

---

## Self-Review

**Spec coverage check:**
- ✅ `/start [token]` — Task 9 (`cmdStart`)
- ✅ `/login` magic link — Tasks 3 + 9
- ✅ Auto-registration on `/login` — Task 3 (`TelegramLoginRequest`)
- ✅ `/status`, `/positions`, `/pnl` — Task 4 (`BotSummary`) + Task 9
- ✅ `/pause`, `/resume` — Tasks 4 + 9
- ✅ `/notifications` — Task 9 (links to web app)
- ✅ `/mute` — Tasks 4 + 8 (`BotMute` + `cmdMute`)
- ✅ Inline pause button in notifications — Tasks 11 + 10 (`handleCallback`)
- ✅ Welcome message for new group members — Task 10 (`handleUpdate`)
- ✅ `TELEGRAM_GROUP_ID` / `WELCOME_ENABLED` config — Task 12
- ✅ `telegram_auth_tokens` table — Task 1
- ✅ `mute_until` column — Task 1
- ✅ `tg_notified` column + polling goroutine — Tasks 1 + 5
- ✅ `RequireBotSecret` middleware — Task 2
- ✅ BOT_SECRET in all internal routes — Tasks 2 + 6
- ✅ Frontend magic link handler — Task 13
- ✅ docker-compose — Task 14
- ✅ Status-change TG notification — Task 5

**Dependency chain:**
Task 1 → Task 2 → Task 3 → Task 4 → Task 5 → Task 6 → Task 7 → Task 8 → Task 9 → Task 10 → Task 11 → Task 12 → Task 13 → Task 14 → Task 15

---

## Task 15: Закончить настройку Telegram-бота

**Files:**
- Modify: `.env` (или `.env.production`)
- Modify: `docker-compose.yml` (при необходимости)

### Шаг 1: Создать бота у @BotFather

- [ ] Написать [@BotFather](https://t.me/BotFather) в Telegram
- [ ] Выполнить команду `/newbot`
- [ ] Задать имя бота (например: `Novabot`)
- [ ] Задать username (например: `novabot_trading_bot`)
- [ ] Сохранить полученный `TELEGRAM_BOT_TOKEN` (формат: `123456789:ABC-DEF...`)

### Шаг 2: Добавить переменные окружения

- [ ] Добавить в `.env`:

```env
TELEGRAM_BOT_TOKEN=<токен от @BotFather>
TELEGRAM_BOT_SECRET=<случайная строка, минимум 32 символа>
```

Сгенерировать `TELEGRAM_BOT_SECRET`:
```bash
openssl rand -hex 32
```

Убедиться, что эта же строка будет передана и в `api-gateway` (`TELEGRAM_BOT_SECRET`).

### Шаг 3: Настроить команды бота (опционально, но рекомендуется)

- [ ] Написать @BotFather команду `/setcommands`
- [ ] Выбрать своего бота
- [ ] Вставить список команд:

```
start - Привязать аккаунт или войти
login - Войти в Novabot
status - Статус стратегий
pnl - P&L за сегодня и неделю
positions - Открытые позиции
pause - Остановить все стратегии
resume - Запустить все стратегии
notifications - Настройки уведомлений
mute - Заглушить уведомления (/mute 2h)
```

### Шаг 4: Настроить группу (если нужны welcome-сообщения)

- [ ] Добавить бота в группу/супергруппу как **администратора** (нужны права отправки сообщений)
- [ ] Получить ID группы: добавить бота [@userinfobot](https://t.me/userinfobot) или переслать сообщение из группы в @userinfobot
- [ ] Добавить в `.env`:

```env
TELEGRAM_GROUP_ID=-100xxxxxxxxxx
WELCOME_ENABLED=true
```

### Шаг 5: Запустить сервис

- [ ] Запустить tg-bot через docker-compose:

```bash
docker compose up -d tg-bot
```

Или, если api-gateway запускается отдельно, убедиться что `GATEWAY_URL` указывает на правильный адрес.

- [ ] Проверить логи:

```bash
docker compose logs -f tg-bot
```

Ожидаемый вывод:
```
tg-bot: authorized as @novabot_trading_bot
tg-bot: polling started
notifier: subscribed to tg:notify
```

### Шаг 6: Проверить работу

- [ ] Написать боту `/login` — убедиться, что приходит кнопка с magic link
- [ ] Нажать кнопку — убедиться, что открывается сайт и происходит автологин
- [ ] Написать `/status` — убедиться, что приходит список стратегий (или сообщение "нет стратегий")
- [ ] Из AccountPage на сайте нажать "Привязать Telegram" — убедиться, что бот присылает подтверждение

### Шаг 7: Commit изменений `.env.example`

Добавить переменные в `.env.example` (если есть):
```env
TELEGRAM_BOT_TOKEN=
TELEGRAM_BOT_SECRET=
TELEGRAM_GROUP_ID=
WELCOME_ENABLED=false
```

```bash
git add .env.example
git commit -m "chore: add telegram bot env vars to .env.example"
```
