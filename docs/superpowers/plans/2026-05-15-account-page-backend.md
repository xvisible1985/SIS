# Account Page — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `/account/*` REST endpoints for profile management, Telegram integration, and referral system.

**Architecture:** New `account_handler.go` file on the existing `*Server` struct. New migration adds `username` to `users` and creates 5 new tables. All endpoints live under `r.Group` with `RequireAuth` middleware. `POST /account/telegram-verify` is unprotected (token IS the auth).

**Tech Stack:** Go 1.21, chi v5, pgx v5, `pkg/auth` for bcrypt/JWT, `newUUID()` helper already in `server.go`

---

## File Structure

- **Create:** `migrations/017_account_page.sql` — 5 new tables + username column on users
- **Create:** `services/api-gateway/account_handler.go` — all 9 account endpoints
- **Create:** `services/api-gateway/account_handler_test.go` — integration tests (build tag `integration`)
- **Modify:** `services/api-gateway/main.go` — register account routes

---

### Task 1: DB Migration

**Files:**
- Create: `migrations/017_account_page.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/017_account_page.sql

ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;

CREATE TABLE IF NOT EXISTS telegram_connections (
    user_id      UUID    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    chat_id      BIGINT  NOT NULL,
    username     TEXT,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS telegram_pending_tokens (
    token      TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '10 minutes'
);

CREATE TABLE IF NOT EXISTS telegram_notification_settings (
    user_id   UUID    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    on_trade  BOOLEAN NOT NULL DEFAULT TRUE,
    on_signal BOOLEAN NOT NULL DEFAULT TRUE,
    on_balance BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS referral_codes (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS referral_signups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    referee_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rewarded    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(referee_id)
);
```

- [ ] **Step 2: Verify migration applies cleanly**

The server auto-runs `db.Migrate` on startup. Restart the dev server and check logs for errors.

```
go run ./services/api-gateway
```

Expected: no migration errors in stdout.

- [ ] **Step 3: Commit**

```bash
git add migrations/017_account_page.sql
git commit -m "feat: account page DB migration (username, telegram, referrals)"
```

---

### Task 2: Profile Endpoints

**Files:**
- Create: `services/api-gateway/account_handler.go`

Implements:
- `GET /account/profile` → `{ email, username, plan, telegram_username }`
- `PATCH /account/profile` → update username
- `POST /account/change-password`

- [ ] **Step 1: Write the failing test**

Create `services/api-gateway/account_handler_test.go`:

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

func newAccTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := newTestServer(t)
	userID := createWHUser(t, s, "acct")
	return s, userID
}

func TestGetProfile(t *testing.T) {
	s, userID := newAccTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/profile", nil)
	req = withUserID(req, userID)
	s.GetProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["email"] == nil {
		t.Fatal("expected email in response")
	}
}

func TestUpdateUsername(t *testing.T) {
	s, userID := newAccTestServer(t)
	body := `{"username":"testuser"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/account/profile", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.UpdateProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["username"] != "testuser" {
		t.Errorf("expected username=testuser, got %v", resp["username"])
	}
}

func TestChangePassword(t *testing.T) {
	s := newTestServer(t)
	// Register a real user so we have a password hash
	regBody := `{"email":"pwchange_test@example.com","password":"oldpass123"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Skipf("register failed: %s", rec.Body.String())
	}
	var regResp map[string]any
	json.NewDecoder(rec.Body).Decode(&regResp)
	userID, _ := regResp["user_id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})

	cpBody := `{"current_password":"oldpass123","new_password":"newpass456"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/account/change-password", bytes.NewBufferString(cpBody))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	s.ChangePassword(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	s := newTestServer(t)
	regBody := `{"email":"pwchange_wrong@example.com","password":"correct123"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	var regResp map[string]any
	json.NewDecoder(rec.Body).Decode(&regResp)
	userID, _ := regResp["user_id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	})

	cpBody := `{"current_password":"wrong","new_password":"newpass456"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/account/change-password", bytes.NewBufferString(cpBody))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	s.ChangePassword(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec2.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetProfile|TestUpdateUsername|TestChangePassword" -v
```

Expected: FAIL with `s.GetProfile undefined` (or similar compile error)

- [ ] **Step 3: Create account_handler.go with profile endpoints**

```go
// services/api-gateway/account_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"sis/pkg/auth"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{3,30}$`)

// GetProfile returns the authenticated user's profile.
// GET /account/profile
func (s *Server) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var email, plan string
	var username, telegramUsername *string
	err := s.pool.QueryRow(r.Context(),
		`SELECT u.email, u.plan, u.username, tc.username
		 FROM users u
		 LEFT JOIN telegram_connections tc ON tc.user_id = u.id
		 WHERE u.id = $1`, userID,
	).Scan(&email, &plan, &username, &telegramUsername)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email":             email,
		"username":          username,
		"plan":              plan,
		"telegram_username": telegramUsername,
	})
}

// UpdateProfile updates the authenticated user's username.
// PATCH /account/profile
func (s *Server) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3–30 chars, letters/digits/underscore only")
		return
	}
	var email, plan string
	var telegramUsername *string
	err := s.pool.QueryRow(r.Context(),
		`UPDATE users SET username=$1 WHERE id=$2
		 RETURNING email, plan`,
		req.Username, userID,
	).Scan(&email, &plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// fetch telegram username separately
	s.pool.QueryRow(r.Context(),
		`SELECT username FROM telegram_connections WHERE user_id=$1`, userID,
	).Scan(&telegramUsername)
	writeJSON(w, http.StatusOK, map[string]any{
		"email":             email,
		"username":          &req.Username,
		"plan":              plan,
		"telegram_username": telegramUsername,
	})
}

// ChangePassword verifies the current password and replaces it.
// POST /account/change-password
func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	var hash string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT password_hash FROM users WHERE id=$1`, userID,
	).Scan(&hash); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !auth.CheckPassword(hash, req.CurrentPassword) {
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET password_hash=$1 WHERE id=$2`, newHash, userID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetProfile|TestUpdateUsername|TestChangePassword" -v
```

Expected: PASS (3 tests green, 1 for wrong password 400)

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/account_handler.go services/api-gateway/account_handler_test.go
git commit -m "feat: account profile and password endpoints"
```

---

### Task 3: Telegram Endpoints

**Files:**
- Modify: `services/api-gateway/account_handler.go` (append)

Implements:
- `GET /account/telegram-link` → generate token, return `{ url }`
- `POST /account/telegram-verify` → bot callback (no auth, token = secret)
- `DELETE /account/telegram` → remove connection
- `GET /account/notifications` → return settings
- `PATCH /account/notifications` → update settings

- [ ] **Step 1: Write the failing test** (append to `account_handler_test.go`)

```go
func TestGetTelegramLink(t *testing.T) {
	s, userID := newAccTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/telegram-link", nil)
	req = withUserID(req, userID)
	s.GetTelegramLink(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	url, _ := body["url"].(string)
	if !strings.Contains(url, "t.me/") {
		t.Errorf("expected t.me/ in url, got %q", url)
	}
}

func TestTelegramVerifyAndDisconnect(t *testing.T) {
	s, userID := newAccTestServer(t)

	// Generate a pending token
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/telegram-link", nil)
	req = withUserID(req, userID)
	s.GetTelegramLink(rec, req)
	var linkResp map[string]any
	json.NewDecoder(rec.Body).Decode(&linkResp)
	url, _ := linkResp["url"].(string)
	// extract token from url: last part after "start="
	parts := strings.Split(url, "start=")
	if len(parts) < 2 {
		t.Fatalf("token not found in url: %s", url)
	}
	token := parts[1]

	// Verify via bot callback
	verifyBody := `{"token":"` + token + `","chat_id":123456,"username":"tguser"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/account/telegram-verify", bytes.NewBufferString(verifyBody))
	req2.Header.Set("Content-Type", "application/json")
	s.TelegramVerify(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("verify got %d: %s", rec2.Code, rec2.Body.String())
	}

	// Disconnect
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/account/telegram", nil)
	req3 = withUserID(req3, userID)
	s.TelegramDisconnect(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("disconnect got %d: %s", rec3.Code, rec3.Body.String())
	}
}

func TestGetUpdateNotifications(t *testing.T) {
	s, userID := newAccTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/notifications", nil)
	req = withUserID(req, userID)
	s.GetNotifications(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["on_trade"] == nil {
		t.Fatal("expected on_trade in response")
	}

	patchBody := `{"on_trade":false}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPatch, "/account/notifications", bytes.NewBufferString(patchBody))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	s.UpdateNotifications(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("patch got %d: %s", rec2.Code, rec2.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetTelegramLink|TestTelegramVerify|TestGetUpdateNotifications" -v
```

Expected: FAIL with compile error (`GetTelegramLink` undefined)

- [ ] **Step 3: Append Telegram handlers to account_handler.go**

```go
// GetTelegramLink generates a one-time deep-link token.
// GET /account/telegram-link
func (s *Server) GetTelegramLink(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	token := newUUID() + newUUID() // 72-char token
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO telegram_pending_tokens (token, user_id)
		 VALUES ($1, $2)
		 ON CONFLICT (token) DO NOTHING`,
		token, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	botName := getEnv("TELEGRAM_BOT_NAME", "novabot")
	url := "https://t.me/" + botName + "?start=" + token
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// TelegramVerify is called by the Telegram bot after the user clicks the deep link.
// POST /account/telegram-verify  (no auth — token IS the secret)
func (s *Server) TelegramVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		ChatID   int64  `json:"chat_id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var userID string
	err := s.pool.QueryRow(r.Context(),
		`DELETE FROM telegram_pending_tokens
		 WHERE token=$1 AND expires_at > NOW()
		 RETURNING user_id`,
		req.Token,
	).Scan(&userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token invalid or expired")
		return
	}
	_, err = s.pool.Exec(r.Context(),
		`INSERT INTO telegram_connections (user_id, chat_id, username)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id) DO UPDATE SET chat_id=$2, username=$3, connected_at=NOW()`,
		userID, req.ChatID, req.Username,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TelegramDisconnect removes the Telegram connection.
// DELETE /account/telegram
func (s *Server) TelegramDisconnect(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	s.pool.Exec(r.Context(),
		`DELETE FROM telegram_connections WHERE user_id=$1`, userID)
	w.WriteHeader(http.StatusNoContent)
}

// GetNotifications returns notification settings (defaults TRUE if row not yet created).
// GET /account/notifications
func (s *Server) GetNotifications(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var onTrade, onSignal, onBalance bool
	err := s.pool.QueryRow(r.Context(),
		`SELECT on_trade, on_signal, on_balance
		 FROM telegram_notification_settings WHERE user_id=$1`, userID,
	).Scan(&onTrade, &onSignal, &onBalance)
	if err != nil {
		onTrade, onSignal, onBalance = true, true, true
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"on_trade": onTrade, "on_signal": onSignal, "on_balance": onBalance,
	})
}

// UpdateNotifications upserts notification settings.
// PATCH /account/notifications
func (s *Server) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req struct {
		OnTrade   *bool `json:"on_trade"`
		OnSignal  *bool `json:"on_signal"`
		OnBalance *bool `json:"on_balance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Read current values first
	var onTrade, onSignal, onBalance bool
	s.pool.QueryRow(r.Context(),
		`SELECT on_trade, on_signal, on_balance
		 FROM telegram_notification_settings WHERE user_id=$1`, userID,
	).Scan(&onTrade, &onSignal, &onBalance)
	if req.OnTrade != nil {
		onTrade = *req.OnTrade
	}
	if req.OnSignal != nil {
		onSignal = *req.OnSignal
	}
	if req.OnBalance != nil {
		onBalance = *req.OnBalance
	}
	_, err := s.pool.Exec(r.Context(),
		`INSERT INTO telegram_notification_settings (user_id, on_trade, on_signal, on_balance)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id) DO UPDATE SET on_trade=$2, on_signal=$3, on_balance=$4`,
		userID, onTrade, onSignal, onBalance,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetTelegramLink|TestTelegramVerify|TestGetUpdateNotifications" -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/account_handler.go services/api-gateway/account_handler_test.go
git commit -m "feat: telegram connection and notification settings endpoints"
```

---

### Task 4: Referral Endpoint

**Files:**
- Modify: `services/api-gateway/account_handler.go` (append)

Implements:
- `GET /account/referral` → get-or-create code, stats, signups list

- [ ] **Step 1: Write the failing test** (append to `account_handler_test.go`)

```go
func TestGetReferral(t *testing.T) {
	s, userID := newAccTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account/referral", nil)
	req = withUserID(req, userID)
	s.GetReferral(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	code, _ := body["code"].(string)
	if len(code) != 8 {
		t.Errorf("expected 8-char code, got %q", code)
	}
	link, _ := body["link"].(string)
	if !strings.Contains(link, code) {
		t.Errorf("link %q does not contain code %q", link, code)
	}
	// Second call returns same code
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/account/referral", nil)
	req2 = withUserID(req2, userID)
	s.GetReferral(rec2, req2)
	var body2 map[string]any
	json.NewDecoder(rec2.Body).Decode(&body2)
	if body2["code"] != body["code"] {
		t.Error("second call returned different code")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetReferral" -v
```

Expected: FAIL with compile error (`GetReferral` undefined)

- [ ] **Step 3: Append referral handler to account_handler.go**

```go
// GetReferral returns (creating if needed) the user's referral code, link, and stats.
// GET /account/referral
func (s *Server) GetReferral(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())

	// Get or create referral code atomically.
	// ON CONFLICT DO UPDATE SET code=existing keeps the original value,
	// and RETURNING always gives us the winning code.
	var code string
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO referral_codes (user_id, code) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET code = referral_codes.code
		 RETURNING code`,
		userID, generateReferralCode(),
	).Scan(&code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Stats
	var count int
	s.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM referral_signups WHERE referrer_id=$1`, userID,
	).Scan(&count)

	// Signups list
	type signup struct {
		Date        string `json:"date"`
		EmailMasked string `json:"email_masked"`
		Active      bool   `json:"active"`
	}
	rows, _ := s.pool.Query(r.Context(),
		`SELECT rs.created_at, u.email,
		        (SELECT COUNT(*) > 0 FROM exchange_accounts ea WHERE ea.owner_id = u.id) AS active
		 FROM referral_signups rs
		 JOIN users u ON u.id = rs.referee_id
		 WHERE rs.referrer_id=$1
		 ORDER BY rs.created_at DESC
		 LIMIT 100`, userID,
	)
	var signups []signup
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var s signup
			var email string
			rows.Scan(&s.Date, &email, &s.Active)
			s.EmailMasked = maskEmail(email)
			signups = append(signups, s)
		}
	}
	if signups == nil {
		signups = []signup{}
	}

	appURL := getEnv("APP_URL", "https://app.novabot.io")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":          code,
		"link":          appURL + "/r/" + code,
		"count":         count,
		"total_rewards": 0,
		"signups":       signups,
	})
}

// generateReferralCode returns a random 8-char uppercase alphanumeric string.
func generateReferralCode() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// maskEmail returns "ab***@domain.com" style masked email.
func maskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) > 2 {
		local = local[:2] + "***"
	}
	return local + "@" + parts[1]
}
```

Add `"crypto/rand"` to imports (it's already used via `newUUID` in `server.go`, but this file needs its own import).

The full import block for `account_handler.go` should be:
```go
import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"sis/pkg/auth"
)
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetReferral" -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/account_handler.go services/api-gateway/account_handler_test.go
git commit -m "feat: referral code and signups endpoint"
```

---

### Task 5: Register Routes

**Files:**
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Add routes to the protected group in main.go**

In `main.go`, inside `r.Group(func(r chi.Router) { r.Use(s.RequireAuth) ... })`, add after the existing routes:

```go
		// Account
		r.Get("/account/profile", s.GetProfile)
		r.Patch("/account/profile", s.UpdateProfile)
		r.Post("/account/change-password", s.ChangePassword)
		r.Get("/account/telegram-link", s.GetTelegramLink)
		r.Delete("/account/telegram", s.TelegramDisconnect)
		r.Get("/account/notifications", s.GetNotifications)
		r.Patch("/account/notifications", s.UpdateNotifications)
		r.Get("/account/referral", s.GetReferral)
```

And outside the auth group (public, for bot callback):

```go
	// Telegram bot callback — no auth, token is the secret
	r.Post("/account/telegram-verify", s.TelegramVerify)
```

Add this line just before the `// Coin icons — public` comment.

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./services/api-gateway
```

Expected: exits 0 with no output.

- [ ] **Step 3: Run all integration tests**

```bash
cd services/api-gateway && go test -tags integration -run "TestGetProfile|TestUpdateUsername|TestChangePassword|TestGetTelegramLink|TestTelegramVerify|TestGetUpdateNotifications|TestGetReferral" -v
```

Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add services/api-gateway/main.go
git commit -m "feat: register /account/* routes in api-gateway"
```
