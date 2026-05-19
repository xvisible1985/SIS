# Admin Users Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить вкладку «Пользователи» в AdminPage — split-панель со списком пользователей, поиском и полной формой управления каждым пользователем.

**Architecture:** Одна миграция добавляет новые колонки в `users` и таблицу `novabot_transactions`. Новый `admin_users_handler.go` реализует все 9 эндпоинтов управления. Фронтенд использует готовые компоненты из Claude Design handoff, подключённые через хук `useAdminUsers`.

**Tech Stack:** Go 1.25, pgx/v5, chi v5, bcrypt, AES-256-GCM; React 18, TypeScript 5.7, Tailwind v3, Vite 6, Axios (через существующий apiClient)

---

## Файловая карта

| Действие | Путь |
|----------|------|
| Создать | `migrations/021_user_management.sql` |
| Изменить | `services/api-gateway/middleware.go` |
| Изменить | `services/api-gateway/auth_handler.go` |
| Создать | `services/api-gateway/admin_users_handler.go` |
| Создать | `services/api-gateway/admin_users_handler_test.go` |
| Изменить | `services/api-gateway/main.go` |
| Создать | `frontend/src/features/admin-users/types.ts` |
| Создать | `frontend/src/features/admin-users/utils.ts` |
| Создать | `frontend/src/features/admin-users/index.ts` |
| Создать | `frontend/src/features/admin-users/AdminUsersPage.tsx` |
| Создать | `frontend/src/features/admin-users/api.ts` |
| Создать | `frontend/src/features/admin-users/components/Avatar.tsx` |
| Создать | `frontend/src/features/admin-users/components/MaskedKey.tsx` |
| Создать | `frontend/src/features/admin-users/components/RefererPicker.tsx` |
| Создать | `frontend/src/features/admin-users/components/RoleTag.tsx` |
| Создать | `frontend/src/features/admin-users/components/Segmented.tsx` |
| Создать | `frontend/src/features/admin-users/components/StatusPill.tsx` |
| Создать | `frontend/src/features/admin-users/components/Toggle.tsx` |
| Создать | `frontend/src/features/admin-users/components/UserDetailPanel.tsx` |
| Создать | `frontend/src/features/admin-users/components/UserList.tsx` |
| Изменить | `frontend/src/pages/AdminPage.tsx` |

---

## Task 1: Database migration

**Files:**
- Create: `migrations/021_user_management.sql`

- [ ] **Step 1: Создать файл миграции**

```sql
-- migrations/021_user_management.sql

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role           TEXT         NOT NULL DEFAULT 'user'
        CHECK (role IN ('user', 'admin')),
    ADD COLUMN IF NOT EXISTS is_curator     BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS is_blocked     BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS email_verified BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS block_reason   TEXT,
    ADD COLUMN IF NOT EXISTS referrer_id    UUID         REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS novabot_balance NUMERIC(18,8) NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS users_role      ON users (role);
CREATE INDEX IF NOT EXISTS users_referrer  ON users (referrer_id) WHERE referrer_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS novabot_transactions (
    id         UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    admin_id   UUID          REFERENCES users(id) ON DELETE SET NULL,
    amount     NUMERIC(18,8) NOT NULL,
    note       TEXT          NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS novabot_transactions_user
    ON novabot_transactions (user_id, created_at DESC);
```

- [ ] **Step 2: Commit**

```bash
git add migrations/021_user_management.sql
git commit -m "feat: migration 021 — user management fields + novabot_transactions"
```

---

## Task 2: Update RequireAdmin — DB role check

**Files:**
- Modify: `services/api-gateway/middleware.go`

Старый `RequireAdmin` проверял email через `adminEmails` map. Новый проверяет колонку `role` в БД. Это breaking change: удаляем `adminEmails` из Server (он больше нужен только для bootstrap в main.go).

- [ ] **Step 1: Заменить тело RequireAdmin в middleware.go**

Найти функцию `RequireAdmin` (строки ~41-63) и заменить целиком:

```go
// RequireAdmin checks that the authenticated user has role='admin' in the DB.
// Must be used after RequireAuth.
func (s *Server) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromCtx(r.Context())
		if userID == "" {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		var role string
		if err := s.pool.QueryRow(r.Context(),
			`SELECT role FROM users WHERE id = $1`, userID,
		).Scan(&role); err != nil {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 2: Убедиться что сборка проходит**

```bash
cd services/api-gateway && go build ./...
```

Ожидаем: успешная сборка без ошибок.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/middleware.go
git commit -m "feat: RequireAdmin checks DB role column instead of env ADMIN_EMAILS"
```

---

## Task 3: Update Login — blocked user returns 403

**Files:**
- Modify: `services/api-gateway/auth_handler.go`
- Modify: `services/api-gateway/auth_handler_test.go`

- [ ] **Step 1: Написать падающий тест в auth_handler_test.go**

Добавить в конец файла `auth_handler_test.go`:

```go
func TestLogin_BlockedUser(t *testing.T) {
	s := newTestServer(t)
	email := "blocked_user@example.com"

	// Register
	regBody := `{"email":"` + email + `","password":"pass1234"}`
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	// Block the user directly in DB
	s.pool.Exec(context.Background(),
		`UPDATE users SET is_blocked=true WHERE email=$1`, email)

	// Login should fail with 403
	loginBody := `{"email":"` + email + `","password":"pass1234"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}

	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}
```

- [ ] **Step 2: Запустить тест — убедиться что падает**

```bash
cd services/api-gateway && go test -tags integration -run TestLogin_BlockedUser -v
```

Ожидаем: `FAIL — got 200, want 403` (или что-то похожее).

- [ ] **Step 3: Обновить Login в auth_handler.go**

В функции `Login` изменить SQL-запрос и добавить проверку блокировки. Найти строку:
```go
var userID, hash string
err := s.pool.QueryRow(r.Context(),
    `SELECT id, password_hash FROM users WHERE email = $1`,
    req.Email,
).Scan(&userID, &hash)
```
Заменить на:
```go
var userID, hash string
var isBlocked bool
err := s.pool.QueryRow(r.Context(),
    `SELECT id, password_hash, is_blocked FROM users WHERE email = $1`,
    req.Email,
).Scan(&userID, &hash, &isBlocked)
```

После проверки `CheckPassword` добавить:
```go
if isBlocked {
    writeError(w, http.StatusForbidden, "account blocked")
    return
}
```

Итоговый вид функции Login после изменений (секция с DB + проверками):
```go
var userID, hash string
var isBlocked bool
err := s.pool.QueryRow(r.Context(),
    `SELECT id, password_hash, is_blocked FROM users WHERE email = $1`,
    req.Email,
).Scan(&userID, &hash, &isBlocked)
if err != nil {
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}

if !auth.CheckPassword(hash, req.Password) {
    writeError(w, http.StatusUnauthorized, "invalid credentials")
    return
}

if isBlocked {
    writeError(w, http.StatusForbidden, "account blocked")
    return
}
```

- [ ] **Step 4: Запустить тест — убедиться что проходит**

```bash
cd services/api-gateway && go test -tags integration -run TestLogin_BlockedUser -v
```

Ожидаем: `PASS`.

- [ ] **Step 5: Запустить все auth тесты**

```bash
cd services/api-gateway && go test -tags integration -run TestLogin -v
cd services/api-gateway && go test -tags integration -run TestRegister -v
```

Ожидаем: все `PASS`.

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/auth_handler.go services/api-gateway/auth_handler_test.go
git commit -m "feat: block user login when is_blocked=true"
```

---

## Task 4: Create admin_users_handler.go

**Files:**
- Create: `services/api-gateway/admin_users_handler.go`
- Create: `services/api-gateway/admin_users_handler_test.go`

- [ ] **Step 1: Написать тесты в admin_users_handler_test.go**

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

	"github.com/go-chi/chi/v5"
)

// createAdminTestUser registers a user and optionally upgrades to admin.
// Returns userID. Caller is responsible for cleanup.
func createAdminTestUser(t *testing.T, s *Server, email, password string, makeAdmin bool) string {
	t.Helper()
	body := `{"email":"` + email + `","password":"` + password + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register failed: %s", rec.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	userID, _ := resp["user_id"].(string)
	if makeAdmin {
		s.pool.Exec(context.Background(), `UPDATE users SET role='admin' WHERE id=$1`, userID)
	}
	return userID
}

// addChiParams injects chi URL params into the request context without replacing existing context values.
func addChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestListAdminUsers(t *testing.T) {
	s := newTestServer(t)
	email := "listusers_admin@example.com"
	adminID := createAdminTestUser(t, s, email, "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = withUserID(req, adminID)
	s.ListAdminUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var users []map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&users)
	if len(users) == 0 {
		t.Error("expected at least one user")
	}
	// Check structure
	u := users[0]
	for _, field := range []string{"id", "email", "role", "status", "accounts"} {
		if _, ok := u[field]; !ok {
			t.Errorf("missing field %q in response", field)
		}
	}
}

func TestPatchAdminUser_Role(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "patch_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "patch_target@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	body := `{"role":"admin"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/"+targetID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.PatchAdminUser(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var role string
	s.pool.QueryRow(context.Background(), `SELECT role FROM users WHERE id=$1`, targetID).Scan(&role)
	if role != "admin" {
		t.Errorf("role not updated, got %q", role)
	}
}

func TestBlockUnblockUser(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "blocker_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "blockee@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	// Block
	blockBody := `{"reason":"test block"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/block", bytes.NewBufferString(blockBody))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.BlockAdminUser(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("block got %d: %s", rec.Code, rec.Body.String())
	}

	var isBlocked bool
	s.pool.QueryRow(context.Background(), `SELECT is_blocked FROM users WHERE id=$1`, targetID).Scan(&isBlocked)
	if !isBlocked {
		t.Error("user should be blocked")
	}

	// Unblock
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/unblock", nil)
	req2 = withUserID(req2, adminID)
	req2 = addChiParams(req2, map[string]string{"id": targetID})
	s.UnblockAdminUser(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unblock got %d: %s", rec2.Code, rec2.Body.String())
	}

	s.pool.QueryRow(context.Background(), `SELECT is_blocked FROM users WHERE id=$1`, targetID).Scan(&isBlocked)
	if isBlocked {
		t.Error("user should be unblocked")
	}
}

func TestBalanceAdjust(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "baladmin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "baluser@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	body := `{"amount":100.5,"note":"test bonus"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/balance/adjust",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": targetID})
	s.AdjustNovabotBalance(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var balance float64
	s.pool.QueryRow(context.Background(), `SELECT novabot_balance FROM users WHERE id=$1`, targetID).Scan(&balance)
	if balance != 100.5 {
		t.Errorf("balance = %v, want 100.5", balance)
	}

	var txCount int
	s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM novabot_transactions WHERE user_id=$1`, targetID).Scan(&txCount)
	if txCount != 1 {
		t.Errorf("expected 1 transaction, got %d", txCount)
	}
}

func TestDeleteAdminAccount(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "delacc_admin@example.com", "pass1234", true)
	targetID := createAdminTestUser(t, s, "delacc_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", targetID)

	// Insert a fake exchange account
	var accID string
	s.pool.QueryRow(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1,'bybit','test','enc','enc') RETURNING id`,
		targetID).Scan(&accID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/"+targetID+"/accounts/"+accID, nil)
	req = withUserID(req, adminID)
	req = req.WithContext(newChiCtx(map[string]string{"id": targetID, "aid": accID}))
	s.DeleteAdminAccount(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}

	var cnt int
	s.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM exchange_accounts WHERE id=$1`, accID).Scan(&cnt)
	if cnt != 0 {
		t.Error("account should have been deleted")
	}
}

```

- [ ] **Step 2: Запустить тесты — убедиться что не компилируются (методы не существуют)**

```bash
cd services/api-gateway && go test -tags integration -run TestListAdminUsers -v 2>&1 | head -20
```

Ожидаем: ошибка компиляции `s.ListAdminUsers undefined`.

- [ ] **Step 3: Создать admin_users_handler.go**

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"sis/pkg/auth"
	"sis/pkg/crypto"
)

// ── Response types ──────────────────────────────────────────────────────────

type adminAccountResp struct {
	ID       string    `json:"id"`
	Exchange string    `json:"exchange"`
	Label    string    `json:"label"`
	APIKey   string    `json:"apiKey"`
	Perms    []string  `json:"perms"`
	Added    time.Time `json:"added"`
}

type adminUserResp struct {
	ID            string             `json:"id"`
	Email         string             `json:"email"`
	Name          string             `json:"name"`
	Role          string             `json:"role"`
	Curator       bool               `json:"curator"`
	Status        string             `json:"status"`
	Balance       float64            `json:"balance"`
	Joined        time.Time          `json:"joined"`
	LastActive    time.Time          `json:"lastActive"`
	EmailVerified bool               `json:"emailVerified"`
	ReferrerID    *string            `json:"refererId"`
	BlockReason   *string            `json:"blockReason"`
	Accounts      []adminAccountResp `json:"accounts"`
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func userStatus(isBlocked, emailVerified bool) string {
	if isBlocked {
		return "blocked"
	}
	if !emailVerified {
		return "pending"
	}
	return "active"
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// ListAdminUsers returns all users with decrypted exchange accounts.
// GET /admin/users
func (s *Server) ListAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := s.pool.Query(ctx, `
		SELECT id, email, role, is_curator, is_blocked, email_verified,
		       referrer_id, novabot_balance, block_reason, created_at
		FROM users
		ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	users := make([]adminUserResp, 0)
	idx := make(map[string]int)

	for rows.Next() {
		var u adminUserResp
		var isBlocked, curator, emailVerified bool
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Role, &curator, &isBlocked, &emailVerified,
			&u.ReferrerID, &u.Balance, &u.BlockReason, &u.Joined,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		u.Name = u.Email
		u.Curator = curator
		u.Status = userStatus(isBlocked, emailVerified)
		u.EmailVerified = emailVerified
		u.LastActive = u.Joined
		u.Accounts = []adminAccountResp{}
		idx[u.ID] = len(users)
		users = append(users, u)
	}

	accRows, err := s.pool.Query(ctx, `
		SELECT id, owner_id, exchange, label, api_key_enc, created_at
		FROM exchange_accounts
		ORDER BY created_at DESC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer accRows.Close()

	for accRows.Next() {
		var id, ownerID, exchange, label, keyEnc string
		var added time.Time
		if err := accRows.Scan(&id, &ownerID, &exchange, &label, &keyEnc, &added); err != nil {
			continue
		}
		apiKey, err := crypto.Decrypt(keyEnc, s.encKey)
		if err != nil {
			apiKey = "***"
		}
		i, ok := idx[ownerID]
		if !ok {
			continue
		}
		users[i].Accounts = append(users[i].Accounts, adminAccountResp{
			ID:       id,
			Exchange: exchange,
			Label:    label,
			APIKey:   apiKey,
			Perms:    []string{},
			Added:    added,
		})
	}

	writeJSON(w, http.StatusOK, users)
}

// PatchAdminUser updates role, curator flag, and/or referrer for a user.
// PATCH /admin/users/{id}
func (s *Server) PatchAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	// Use raw map so we can distinguish absent keys from explicit null (for refererId).
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if roleRaw, ok := raw["role"]; ok {
		var role string
		if err := json.Unmarshal(roleRaw, &role); err != nil {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if role != "user" && role != "admin" {
			writeError(w, http.StatusBadRequest, "role must be user or admin")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET role=$1 WHERE id=$2`, role, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	if curatorRaw, ok := raw["curator"]; ok {
		var curator bool
		if err := json.Unmarshal(curatorRaw, &curator); err != nil {
			writeError(w, http.StatusBadRequest, "invalid curator")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET is_curator=$1 WHERE id=$2`, curator, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	if refRaw, ok := raw["refererId"]; ok {
		var refID *string
		if err := json.Unmarshal(refRaw, &refID); err != nil {
			writeError(w, http.StatusBadRequest, "invalid refererId")
			return
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET referrer_id=$1 WHERE id=$2`, refID, id); err != nil {
			writeError(w, http.StatusInternalServerError, "db error")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// VerifyEmail manually sets email_verified=true.
// POST /admin/users/{id}/email/verify
func (s *Server) AdminVerifyEmail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET email_verified=true WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ResetEmail sets email_verified=false.
// POST /admin/users/{id}/email/reset
func (s *Server) AdminResetEmail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET email_verified=false WHERE id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ResendEmail is a stub — email service not implemented.
// POST /admin/users/{id}/email/resend
func (s *Server) AdminResendEmail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdminSetPassword changes a user's password.
// POST /admin/users/{id}/password
func (s *Server) AdminSetPassword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash error")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2`, hash, id); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AdjustNovabotBalance atomically adjusts a user's novabot balance and records the transaction.
// POST /admin/users/{id}/balance/adjust
func (s *Server) AdjustNovabotBalance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	adminID := UserIDFromCtx(r.Context())

	var req struct {
		Amount float64 `json:"amount"`
		Note   string  `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Amount == 0 {
		writeError(w, http.StatusBadRequest, "amount must be non-zero")
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tx error")
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET novabot_balance = novabot_balance + $1 WHERE id = $2`,
		req.Amount, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO novabot_transactions (user_id, admin_id, amount, note) VALUES ($1, $2, $3, $4)`,
		id, adminID, req.Amount, req.Note,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "commit error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// BlockAdminUser sets is_blocked=true with a reason.
// POST /admin/users/{id}/block
func (s *Server) BlockAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET is_blocked=true, block_reason=$1 WHERE id=$2`,
		req.Reason, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// UnblockAdminUser sets is_blocked=false and clears block_reason.
// POST /admin/users/{id}/unblock
func (s *Server) UnblockAdminUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.pool.Exec(r.Context(),
		`UPDATE users SET is_blocked=false, block_reason=NULL WHERE id=$1`, id,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DeleteAdminAccount deletes an exchange account belonging to a user.
// DELETE /admin/users/{id}/accounts/{aid}
func (s *Server) DeleteAdminAccount(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	accID := chi.URLParam(r, "aid")
	tag, err := s.pool.Exec(r.Context(),
		`DELETE FROM exchange_accounts WHERE id=$1 AND owner_id=$2`, accID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

```

- [ ] **Step 4: Запустить все тесты handler-а**

```bash
cd services/api-gateway && go test -tags integration -run "TestListAdminUsers|TestPatchAdminUser|TestBlockUnblockUser|TestBalanceAdjust|TestDeleteAdminAccount" -v
```

Ожидаем: все `PASS`.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/admin_users_handler.go services/api-gateway/admin_users_handler_test.go
git commit -m "feat: admin users handler — list, patch, block, balance, delete account"
```

---

## Task 5: Register routes + bootstrap in main.go

**Files:**
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Добавить функцию bootstrapAdmins перед main()**

В конец файла `main.go` (после `getEnv`) добавить:

```go
// bootstrapAdmins upgrades users from ADMIN_EMAILS env to role='admin' in the DB.
// Allows a smooth migration from env-based to DB-based admin check.
func bootstrapAdmins(ctx context.Context, pool *pgxpool.Pool, adminEmails map[string]bool) {
	for email := range adminEmails {
		pool.Exec(ctx, `UPDATE users SET role='admin' WHERE email=$1 AND role!='admin'`, email)
	}
}
```

- [ ] **Step 2: Вызвать bootstrapAdmins после создания сервера**

В `main()`, сразу после строки `s := NewServer(...)`:

```go
bootstrapAdmins(ctx, pool, adminEmails)
```

- [ ] **Step 3: Добавить роуты в admin-группу**

Найти блок:
```go
r.Group(func(r chi.Router) {
    r.Use(s.RequireAdmin)
    r.Get("/admin/signal-types", ...
```

Добавить **перед** существующими admin-роутами:

```go
// Admin: user management
r.Get("/admin/users", s.ListAdminUsers)
r.Patch("/admin/users/{id}", s.PatchAdminUser)
r.Post("/admin/users/{id}/email/verify", s.AdminVerifyEmail)
r.Post("/admin/users/{id}/email/reset", s.AdminResetEmail)
r.Post("/admin/users/{id}/email/resend", s.AdminResendEmail)
r.Post("/admin/users/{id}/password", s.AdminSetPassword)
r.Post("/admin/users/{id}/balance/adjust", s.AdjustNovabotBalance)
r.Post("/admin/users/{id}/block", s.BlockAdminUser)
r.Post("/admin/users/{id}/unblock", s.UnblockAdminUser)
r.Delete("/admin/users/{id}/accounts/{aid}", s.DeleteAdminAccount)
```

- [ ] **Step 4: Проверить сборку**

```bash
cd services/api-gateway && go build ./...
```

Ожидаем: успешная сборка.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/main.go
git commit -m "feat: register admin user routes and bootstrap admin emails on startup"
```

---

## Task 6: Copy frontend handoff files

**Files:**
- Create: `frontend/src/features/admin-users/` (все файлы из handoff)

Файлы из `C:\Users\123\Downloads\handoff-admin-users\` копируются в `frontend/src/features/admin-users/` **без изменений**.

- [ ] **Step 1: Создать директорию и скопировать файлы**

```powershell
$src = "C:\Users\123\Downloads\handoff-admin-users"
$dst = "C:\Users\123\Projects\sis\frontend\src\features\admin-users"
New-Item -ItemType Directory -Force $dst
New-Item -ItemType Directory -Force "$dst\components"
Copy-Item "$src\AdminUsersPage.tsx" "$dst\"
Copy-Item "$src\types.ts"          "$dst\"
Copy-Item "$src\utils.ts"          "$dst\"
Copy-Item "$src\index.ts"          "$dst\"
Copy-Item "$src\components\*"      "$dst\components\"
```

- [ ] **Step 2: Проверить что файлы на месте**

```powershell
Get-ChildItem "C:\Users\123\Projects\sis\frontend\src\features\admin-users" -Recurse -Name
```

Ожидаем: 10 файлов + папка `components` с 9 файлами.

- [ ] **Step 3: Commit**

```bash
cd C:\Users\123\Projects\sis
git add frontend/src/features/admin-users/
git commit -m "feat: add admin-users feature from Claude Design handoff"
```

---

## Task 7: Create frontend api.ts hook

**Files:**
- Create: `frontend/src/features/admin-users/api.ts`

- [ ] **Step 1: Создать api.ts**

```typescript
import { useState, useCallback, useEffect } from 'react'
import { apiClient } from '../../api/client'
import type { AdminAction, AdminUser } from './types'

function toAdminUser(raw: Record<string, unknown>): AdminUser {
  return {
    ...(raw as unknown as AdminUser),
    joined: new Date(raw.joined as string),
    lastActive: new Date((raw.lastActive as string) ?? (raw.joined as string)),
    accounts: ((raw.accounts ?? []) as Record<string, unknown>[]).map(a => ({
      ...(a as unknown as AdminUser['accounts'][number]),
      added: new Date(a.added as string),
    })),
  }
}

export function useAdminUsers() {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      const res = await apiClient.get<Record<string, unknown>[]>('/admin/users')
      setUsers(res.data.map(toAdminUser))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Ошибка загрузки пользователей')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const action = useCallback(async (a: AdminAction) => {
    const base = `/admin/users/${a.userId}`
    switch (a.type) {
      case 'role/set':
        await apiClient.patch(base, { role: a.role })
        break
      case 'curator/set':
        await apiClient.patch(base, { curator: a.curator })
        break
      case 'referer/set':
        await apiClient.patch(base, { refererId: a.refererId })
        break
      case 'email/verify':
        await apiClient.post(`${base}/email/verify`)
        break
      case 'email/resend':
        await apiClient.post(`${base}/email/resend`)
        break
      case 'email/reset':
        await apiClient.post(`${base}/email/reset`)
        break
      case 'password/set':
        await apiClient.post(`${base}/password`, {
          password: a.password,
          requireChange: a.requireChange,
        })
        break
      case 'balance/adjust':
        await apiClient.post(`${base}/balance/adjust`, { amount: a.amount, note: a.note })
        break
      case 'block':
        await apiClient.post(`${base}/block`, { reason: a.reason })
        break
      case 'unblock':
        await apiClient.post(`${base}/unblock`)
        break
      case 'account/remove':
        await apiClient.delete(`${base}/accounts/${a.accountId}`)
        break
    }
    await load()
  }, [load])

  return { users, loading, error, action, refresh: load }
}
```

- [ ] **Step 2: Проверить TypeScript компиляцию**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -30
```

Ожидаем: нет ошибок связанных с `admin-users/api.ts`.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/admin-users/api.ts
git commit -m "feat: useAdminUsers hook connecting frontend to backend"
```

---

## Task 8: Integrate users tab into AdminPage

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Добавить импорт в начало AdminPage.tsx**

После существующих импортов добавить:

```typescript
import { AdminUsersPage } from '../features/admin-users'
import { useAdminUsers } from '../features/admin-users/api'
```

- [ ] **Step 2: Добавить UsersTab компонент перед функцией AdminPage**

```typescript
function UsersTab() {
  const { users, loading, error, action, refresh } = useAdminUsers()

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-[12px] text-slate-600">
        Загрузка...
      </div>
    )
  }
  if (error) {
    return (
      <div className="m-5 rounded-xl border border-amber-400/20 bg-amber-400/[.06] px-4 py-3 text-[12px] text-amber-300">
        {error}
      </div>
    )
  }
  return (
    <AdminUsersPage
      users={users}
      onAction={action}
      onRefresh={refresh}
    />
  )
}
```

- [ ] **Step 3: Добавить вкладку 'users' первой в массив TABS**

Найти:
```typescript
const TABS = [
  { id: 'monitoring', label: 'Мониторинг' },
  { id: 'signals',    label: 'Сигналы'    },
] as const
```

Заменить на:
```typescript
const TABS = [
  { id: 'users',      label: 'Пользователи' },
  { id: 'monitoring', label: 'Мониторинг'   },
  { id: 'signals',    label: 'Сигналы'      },
] as const
```

- [ ] **Step 4: Добавить рендер вкладки users в JSX**

Найти секцию `{/* Content */}` и добавить **перед** `{tab === 'monitoring' && ...}`:

```tsx
{tab === 'users' && (
  <div className="flex flex-1 overflow-hidden">
    <UsersTab />
  </div>
)}
```

- [ ] **Step 5: Проверить TypeScript**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -40
```

Ожидаем: нет ошибок.

- [ ] **Step 6: Запустить dev-сервер и проверить вкладку**

```bash
cd frontend && npm run dev
```

Открыть `http://localhost:5173/admin` (под admin-аккаунтом). Убедиться:
- Вкладка «Пользователи» первая и выбрана по умолчанию
- Список пользователей загружается
- Поиск фильтрует список
- Клик на пользователя открывает панель справа

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat: add Users tab to AdminPage with full user management"
```

---

## Проверка сборки бэкенда

- [ ] **Собрать бинарник api-gateway**

```bash
cd services/api-gateway && go build -o api-gateway.exe .
```

Ожидаем: `api-gateway.exe` создан без ошибок.

- [ ] **Финальный коммит**

```bash
git add services/api-gateway/api-gateway.exe
git commit -m "build: rebuild api-gateway with admin users feature"
```
