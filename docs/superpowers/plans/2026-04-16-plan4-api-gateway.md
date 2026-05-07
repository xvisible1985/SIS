# API Gateway + Auth + WebSocket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an HTTP API service with JWT auth, signals CRUD, backtest/optimize job submission, result retrieval, and WebSocket progress streaming.

**Architecture:** Single `services/api-gateway` binary using `chi` router. Auth logic (bcrypt + JWT) lives in `pkg/auth` as pure functions. All handlers are methods on a `Server` struct holding DB pool, Redis client, and JWT secret. Job submission pushes JSON payloads onto the Redis Streams already consumed by `signal-engine`. WebSocket endpoint polls Redis hash progress keys set by `signal-engine`.

**Tech Stack:** `github.com/go-chi/chi/v5` (router), `github.com/golang-jwt/jwt/v5` (JWT), `golang.org/x/crypto/bcrypt` (password hashing), `github.com/gorilla/websocket` (already indirect → promote to direct), `github.com/jackc/pgx/v5`, `github.com/redis/go-redis/v9`

---

## File Structure

**Create:**
- `migrations/003_add_auth.sql` — add `password_hash` column to `users`
- `pkg/auth/auth.go` — `HashPassword`, `CheckPassword`, `GenerateToken`, `ValidateToken`
- `pkg/auth/auth_test.go` — unit tests (no DB)
- `services/api-gateway/server.go` — `Server` struct, `NewServer`, JSON helpers, `newUUID`
- `services/api-gateway/middleware.go` — `RequireAuth` middleware, `UserIDFromCtx`
- `services/api-gateway/middleware_test.go` — unit tests
- `services/api-gateway/auth_handler.go` — `Register`, `Login`
- `services/api-gateway/auth_handler_test.go` — integration tests (`//go:build integration`)
- `services/api-gateway/signals_handler.go` — `ListSignals`, `CreateSignal`, `GetSignal`, `UpdateSignal`, `DeleteSignal`
- `services/api-gateway/signals_handler_test.go` — integration tests
- `services/api-gateway/jobs_handler.go` — `SubmitBacktest`, `SubmitOptimize`, `GetBacktestResults`, `GetOptimizationResults`
- `services/api-gateway/jobs_handler_test.go` — integration tests
- `services/api-gateway/ws_handler.go` — `JobProgress` WebSocket handler
- `services/api-gateway/main.go` — entry point, router wiring

**Modify:**
- `go.mod` / `go.sum` — add chi, golang-jwt, x/crypto; promote gorilla/websocket to direct

---

## HTTP Routes

```
POST   /auth/register                       → Register
POST   /auth/login                          → Login

GET    /signals                             → ListSignals          (auth required)
POST   /signals                             → CreateSignal         (auth required)
GET    /signals/:id                         → GetSignal            (auth required)
PUT    /signals/:id                         → UpdateSignal         (auth required)
DELETE /signals/:id                         → DeleteSignal         (auth required)

POST   /signals/:id/backtest                → SubmitBacktest       (auth required)
POST   /signals/:id/optimize               → SubmitOptimize       (auth required)
GET    /signals/:id/backtest-results        → GetBacktestResults   (auth required)
GET    /signals/:id/optimization-results    → GetOptimizationResults (auth required)

GET    /ws/jobs/:id/progress?type=backtest|optimize  → JobProgress WebSocket (token via ?token=)
```

---

### Task 1: Add dependencies and DB migration

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `migrations/003_add_auth.sql`

- [ ] **Step 1: Add Go dependencies**

```bash
cd c:/Users/123/Projects/sis
go get github.com/go-chi/chi/v5@latest
go get github.com/golang-jwt/jwt/v5@latest
go get golang.org/x/crypto@latest
go get github.com/gorilla/websocket@latest
```

Expected: `go.mod` gains 3 new direct deps, gorilla/websocket moves to direct.

- [ ] **Step 2: Write migration**

Create `migrations/003_add_auth.sql`:

```sql
-- migrations/003_add_auth.sql

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum migrations/003_add_auth.sql
git commit -m "feat: add api-gateway deps (chi, jwt, bcrypt, websocket) and auth migration"
```

---

### Task 2: Auth helpers (pkg/auth)

**Files:**
- Create: `pkg/auth/auth.go`
- Create: `pkg/auth/auth_test.go`

- [ ] **Step 1: Write failing tests**

Create `pkg/auth/auth_test.go`:

```go
package auth_test

import (
	"testing"
	"time"

	"sis/pkg/auth"
)

func TestHashAndCheck_Valid(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !auth.CheckPassword(hash, "hunter2") {
		t.Error("expected CheckPassword true for correct password")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, _ := auth.HashPassword("correct")
	if auth.CheckPassword(hash, "wrong") {
		t.Error("expected CheckPassword false for wrong password")
	}
}

func TestGenerateAndValidate_Token(t *testing.T) {
	secret := "test-secret"
	userID := "user-uuid-123"
	tok, err := auth.GenerateToken(userID, secret, 24*time.Hour)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := auth.ValidateToken(tok, secret)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != userID {
		t.Errorf("got userID=%q, want %q", got, userID)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	tok, _ := auth.GenerateToken("uid", "secret1", time.Hour)
	_, err := auth.ValidateToken(tok, "secret2")
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	tok, _ := auth.GenerateToken("uid", "secret", -time.Second)
	_, err := auth.ValidateToken(tok, "secret")
	if err == nil {
		t.Error("expected error for expired token")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./pkg/auth/... -v
```

Expected: FAIL — package `sis/pkg/auth` not found.

- [ ] **Step 3: Implement pkg/auth/auth.go**

Create `pkg/auth/auth.go`:

```go
// pkg/auth/auth.go
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// HashPassword returns a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(b), nil
}

// CheckPassword reports whether password matches hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type claims struct {
	jwt.RegisteredClaims
}

// GenerateToken creates a signed HS256 JWT for userID valid for ttl.
func GenerateToken(userID, secret string, ttl time.Duration) (string, error) {
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return tok.SignedString([]byte(secret))
}

// ValidateToken parses and validates a JWT, returning the subject (userID).
func ValidateToken(tokenStr, secret string) (string, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}
	c, ok := tok.Claims.(*claims)
	if !ok || !tok.Valid {
		return "", errors.New("invalid token")
	}
	return c.Subject, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./pkg/auth/... -v
```

Expected: 5/5 PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/auth/
git commit -m "feat: add pkg/auth (bcrypt + JWT helpers)"
```

---

### Task 3: Server struct and JWT middleware

**Files:**
- Create: `services/api-gateway/server.go`
- Create: `services/api-gateway/middleware.go`
- Create: `services/api-gateway/middleware_test.go`

- [ ] **Step 1: Write failing middleware tests**

Create `services/api-gateway/middleware_test.go`:

```go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sis/pkg/auth"
)

func TestRequireAuth_NoHeader(t *testing.T) {
	s := &Server{jwtSecret: []byte("secret")}
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestRequireAuth_ValidToken(t *testing.T) {
	secret := "secret"
	tok, _ := auth.GenerateToken("user-1", secret, time.Hour)
	s := &Server{jwtSecret: []byte(secret)}
	var gotID string
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = UserIDFromCtx(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	if gotID != "user-1" {
		t.Errorf("got userID=%q, want user-1", gotID)
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	s := &Server{jwtSecret: []byte("secret")}
	h := s.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer bad.token.here")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestUserIDFromCtx_Empty(t *testing.T) {
	id := UserIDFromCtx(context.Background())
	if id != "" {
		t.Errorf("expected empty, got %q", id)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./services/api-gateway/... -run "TestRequireAuth|TestUserIDFromCtx" -v
```

Expected: FAIL — package cannot compile, types not defined.

- [ ] **Step 3: Create server.go**

Create `services/api-gateway/server.go`:

```go
// services/api-gateway/server.go
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	pool      *pgxpool.Pool
	rdb       *redis.Client
	jwtSecret []byte
}

// NewServer creates a Server.
func NewServer(pool *pgxpool.Pool, rdb *redis.Client, jwtSecret string) *Server {
	return &Server{pool: pool, rdb: rdb, jwtSecret: []byte(jwtSecret)}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// newUUID generates a random UUID v4.
func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
```

- [ ] **Step 4: Create middleware.go**

Create `services/api-gateway/middleware.go`:

```go
// services/api-gateway/middleware.go
package main

import (
	"context"
	"net/http"
	"strings"

	"sis/pkg/auth"
)

type contextKey string

const ctxUserID contextKey = "userID"

// RequireAuth validates a Bearer JWT and stores the userID in context.
func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")
		userID, err := auth.ValidateToken(tokenStr, string(s.jwtSecret))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromCtx extracts the authenticated user ID from context.
func UserIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./services/api-gateway/... -run "TestRequireAuth|TestUserIDFromCtx" -v
```

Expected: 4/4 PASS.

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/server.go services/api-gateway/middleware.go services/api-gateway/middleware_test.go
git commit -m "feat: api-gateway server struct and JWT auth middleware"
```

---

### Task 4: Register and Login handlers

**Files:**
- Create: `services/api-gateway/auth_handler.go`
- Create: `services/api-gateway/auth_handler_test.go`

- [ ] **Step 1: Write failing integration tests**

Create `services/api-gateway/auth_handler_test.go`:

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

	"sis/pkg/cache"
	"sis/pkg/db"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, "postgres://sis:sis_secret@localhost:5432/sis")
	if err != nil {
		t.Skipf("timescaledb unavailable: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	rdb, err := cache.Connect(ctx, "redis://localhost:6379")
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	t.Cleanup(func() { rdb.Close() })
	return NewServer(pool, rdb, "test-secret")
}

func TestRegister_Success(t *testing.T) {
	s := newTestServer(t)
	body := `{"email":"reg_plan4@example.com","password":"pass1234"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Register(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected token in response")
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", "reg_plan4@example.com")
}

func TestRegister_DuplicateEmail(t *testing.T) {
	s := newTestServer(t)
	email := "dup_plan4@example.com"
	body := `{"email":"` + email + `","password":"pass1234"}`

	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	s.Register(rec1, req1)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	s.Register(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("got %d, want 409", rec2.Code)
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}

func TestLogin_Success(t *testing.T) {
	s := newTestServer(t)
	email := "login_plan4@example.com"
	body := `{"email":"` + email + `","password":"mypassword"}`

	// Register first
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	// Login
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["token"] == "" {
		t.Error("expected token")
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}

func TestLogin_WrongPassword(t *testing.T) {
	s := newTestServer(t)
	email := "wrongpw_plan4@example.com"

	regBody := `{"email":"` + email + `","password":"correct"}`
	recR := httptest.NewRecorder()
	reqR := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(regBody))
	reqR.Header.Set("Content-Type", "application/json")
	s.Register(recR, reqR)

	loginBody := `{"email":"` + email + `","password":"wrong"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	s.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
	s.pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -tags integration ./services/api-gateway/... -run "TestRegister|TestLogin" -v
```

Expected: FAIL — `s.Register` undefined.

- [ ] **Step 3: Implement auth_handler.go**

Create `services/api-gateway/auth_handler.go`:

```go
// services/api-gateway/auth_handler.go
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"sis/pkg/auth"
)

type authRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates a new user account and returns a JWT.
// POST /auth/register
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "email required and password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var userID string
	err = s.pool.QueryRow(r.Context(),
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		req.Email, hash,
	).Scan(&userID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "user_id": userID})
}

// Login authenticates a user and returns a JWT.
// POST /auth/login
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	var userID, hash string
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, password_hash FROM users WHERE email = $1`,
		req.Email,
	).Scan(&userID, &hash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if !auth.CheckPassword(hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := auth.GenerateToken(userID, string(s.jwtSecret), 24*time.Hour)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "user_id": userID})
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -tags integration ./services/api-gateway/... -run "TestRegister|TestLogin" -v
```

Expected: 4/4 PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/auth_handler.go services/api-gateway/auth_handler_test.go
git commit -m "feat: api-gateway register and login handlers"
```

---

### Task 5: Signals CRUD handlers

**Files:**
- Create: `services/api-gateway/signals_handler.go`
- Create: `services/api-gateway/signals_handler_test.go`

- [ ] **Step 1: Write failing integration tests**

Create `services/api-gateway/signals_handler_test.go`:

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

const testConditions = `{"type":"condition","indicator":"RSI","params":{"period":14},"operator":">","value":50}`

func TestCreateAndGetSignal(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)

	body := `{"name":"RSI cross","description":"test","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, s, userID))
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
	req2.Header.Set("Authorization", authHeader(t, s, userID))
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
	req.Header.Set("Authorization", authHeader(t, s, userID))
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
	reqC.Header.Set("Authorization", authHeader(t, s, userID))
	s.CreateSignal(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	sigID, _ := created["id"].(string)

	recD := httptest.NewRecorder()
	reqD := httptest.NewRequest(http.MethodDelete, "/signals/"+sigID, nil)
	reqD.Header.Set("Authorization", authHeader(t, s, userID))
	reqD = withChiParams(reqD, map[string]string{"id": sigID})
	s.DeleteSignal(recD, reqD)
	if recD.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", recD.Code)
	}

	// Verify gone
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/signals/"+sigID, nil)
	req2.Header.Set("Authorization", authHeader(t, s, userID))
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
	reqC.Header.Set("Authorization", authHeader(t, s, userID))
	s.CreateSignal(recC, reqC)
	var created map[string]any
	json.NewDecoder(recC.Body).Decode(&created)
	sigID, _ := created["id"].(string)

	updateBody := `{"name":"updated"}`
	recU := httptest.NewRecorder()
	reqU := httptest.NewRequest(http.MethodPut, "/signals/"+sigID, bytes.NewBufferString(updateBody))
	reqU.Header.Set("Content-Type", "application/json")
	reqU.Header.Set("Authorization", authHeader(t, s, userID))
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
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -tags integration ./services/api-gateway/... -run "TestCreateAndGetSignal|TestListSignals|TestDeleteSignal|TestUpdateSignal" -v
```

Expected: FAIL — `s.CreateSignal` undefined.

- [ ] **Step 3: Implement signals_handler.go**

Create `services/api-gateway/signals_handler.go`:

```go
// services/api-gateway/signals_handler.go
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type signalRow struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Exchange    string          `json:"exchange"`
	Symbol      string          `json:"symbol"`
	Market      string          `json:"market"`
	Timeframe   string          `json:"timeframe"`
	Direction   string          `json:"direction"`
	Conditions  json.RawMessage `json:"conditions"`
	IsActive    bool            `json:"is_active"`
	CreatedAt   time.Time       `json:"created_at"`
}

type createSignalRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Exchange    string          `json:"exchange"`
	Symbol      string          `json:"symbol"`
	Market      string          `json:"market"`
	Timeframe   string          `json:"timeframe"`
	Direction   string          `json:"direction"`
	Conditions  json.RawMessage `json:"conditions"`
}

// ListSignals returns all signals owned by the authenticated user.
// GET /signals
func (s *Server) ListSignals(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at
		 FROM signals WHERE owner_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	result := make([]signalRow, 0)
	for rows.Next() {
		var row signalRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
			&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
			&row.Conditions, &row.IsActive, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, row)
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateSignal creates a new signal for the authenticated user.
// POST /signals
func (s *Server) CreateSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	var req createSignalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Exchange == "" || req.Symbol == "" || req.Market == "" || req.Timeframe == "" {
		writeError(w, http.StatusBadRequest, "name, exchange, symbol, market, timeframe are required")
		return
	}
	if req.Direction == "" {
		req.Direction = "LONG"
	}

	var row signalRow
	err := s.pool.QueryRow(r.Context(),
		`INSERT INTO signals (owner_id, name, description, exchange, symbol, market, timeframe, direction, conditions)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at`,
		userID, req.Name, req.Description, req.Exchange, req.Symbol, req.Market, req.Timeframe, req.Direction, req.Conditions,
	).Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
		&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
		&row.Conditions, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// GetSignal returns a single signal by ID (must be owned by caller).
// GET /signals/:id
func (s *Server) GetSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var row signalRow
	err := s.pool.QueryRow(r.Context(),
		`SELECT id, name, description, exchange, symbol, market, timeframe, direction, conditions, is_active, created_at
		 FROM signals WHERE id = $1 AND owner_id = $2`,
		sigID, userID,
	).Scan(&row.ID, &row.Name, &row.Description, &row.Exchange,
		&row.Symbol, &row.Market, &row.Timeframe, &row.Direction,
		&row.Conditions, &row.IsActive, &row.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// UpdateSignal updates name, description, direction, conditions, is_active.
// PUT /signals/:id
func (s *Server) UpdateSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var req struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Direction   string          `json:"direction"`
		Conditions  json.RawMessage `json:"conditions"`
		IsActive    *bool           `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	_, err := s.pool.Exec(r.Context(),
		`UPDATE signals SET
			name        = COALESCE(NULLIF($3,''), name),
			description = COALESCE(NULLIF($4,''), description),
			direction   = COALESCE(NULLIF($5,''), direction),
			conditions  = COALESCE($6, conditions),
			is_active   = COALESCE($7, is_active)
		 WHERE id = $1 AND owner_id = $2`,
		sigID, userID, req.Name, req.Description, req.Direction, req.Conditions, req.IsActive,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	s.GetSignal(w, r)
}

// DeleteSignal deletes a signal owned by the caller.
// DELETE /signals/:id
func (s *Server) DeleteSignal(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	_, err := s.pool.Exec(r.Context(),
		`DELETE FROM signals WHERE id = $1 AND owner_id = $2`,
		sigID, userID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -tags integration ./services/api-gateway/... -run "TestCreateAndGetSignal|TestListSignals|TestDeleteSignal|TestUpdateSignal" -v
```

Expected: 4/4 PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/signals_handler.go services/api-gateway/signals_handler_test.go
git commit -m "feat: api-gateway signals CRUD handlers"
```

---

### Task 6: Job submission and results handlers

**Files:**
- Create: `services/api-gateway/jobs_handler.go`
- Create: `services/api-gateway/jobs_handler_test.go`

- [ ] **Step 1: Write failing integration tests**

Create `services/api-gateway/jobs_handler_test.go`:

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

func createTestSignal(t *testing.T, s *Server, userID string) string {
	t.Helper()
	body := `{"name":"job_test","description":"","exchange":"binance","symbol":"BTCUSDT","market":"spot","timeframe":"1h","direction":"LONG","conditions":` + testConditions + `}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, s, userID))
	s.CreateSignal(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create signal for test: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.NewDecoder(rec.Body).Decode(&created)
	sigID, _ := created["id"].(string)
	t.Cleanup(func() {
		s.pool.Exec(context.Background(), "DELETE FROM signals WHERE id=$1", sigID)
	})
	return sigID
}

func TestSubmitBacktest_Success(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	btBody := `{"period_from":"2024-01-01T00:00:00Z","period_to":"2024-12-31T00:00:00Z","take_profit":2.0,"stop_loss":1.0}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals/"+sigID+"/backtest", bytes.NewBufferString(btBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, s, userID))
	req = withChiParams(req, map[string]string{"id": sigID})
	s.SubmitBacktest(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["job_id"] == "" {
		t.Error("expected job_id in response")
	}
}

func TestSubmitOptimize_Success(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	optBody := `{"period_from":"2024-01-01T00:00:00Z","period_to":"2024-12-31T00:00:00Z","mode":"fast","score_by":"profit_factor","top_n":5,"take_profits":[2.0],"stop_losses":[1.0],"param_space":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/signals/"+sigID+"/optimize", bytes.NewBufferString(optBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(t, s, userID))
	req = withChiParams(req, map[string]string{"id": sigID})
	s.SubmitOptimize(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["job_id"] == "" {
		t.Error("expected job_id in response")
	}
}

func TestGetBacktestResults_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signals/"+sigID+"/backtest-results", nil)
	req.Header.Set("Authorization", authHeader(t, s, userID))
	req = withChiParams(req, map[string]string{"id": sigID})
	s.GetBacktestResults(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var results []any
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestGetOptimizationResults_Empty(t *testing.T) {
	s := newTestServer(t)
	userID := createTestUser(t, s)
	sigID := createTestSignal(t, s, userID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/signals/"+sigID+"/optimization-results", nil)
	req.Header.Set("Authorization", authHeader(t, s, userID))
	req = withChiParams(req, map[string]string{"id": sigID})
	s.GetOptimizationResults(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var results []any
	json.NewDecoder(rec.Body).Decode(&results)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test -tags integration ./services/api-gateway/... -run "TestSubmitBacktest|TestSubmitOptimize|TestGetBacktest|TestGetOptimization" -v
```

Expected: FAIL — `s.SubmitBacktest` undefined.

- [ ] **Step 3: Implement jobs_handler.go**

Create `services/api-gateway/jobs_handler.go`:

```go
// services/api-gateway/jobs_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

type backtestRequest struct {
	PeriodFrom string  `json:"period_from"`
	PeriodTo   string  `json:"period_to"`
	TakeProfit float64 `json:"take_profit"`
	StopLoss   float64 `json:"stop_loss"`
}

type optimizeRequest struct {
	PeriodFrom  string               `json:"period_from"`
	PeriodTo    string               `json:"period_to"`
	Mode        string               `json:"mode"`
	ScoreBy     string               `json:"score_by"`
	TopN        int                  `json:"top_n"`
	TakeProfits []float64            `json:"take_profits"`
	StopLosses  []float64            `json:"stop_losses"`
	ParamSpace  map[string][]float64 `json:"param_space"`
	WFFolds     int                  `json:"wf_folds"`
}

// SubmitBacktest enqueues a backtest job onto the jobs:backtest Redis stream.
// POST /signals/:id/backtest
func (s *Server) SubmitBacktest(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exchange, symbol, market, timeframe, direction string
	var condJSON json.RawMessage
	err := s.pool.QueryRow(r.Context(),
		`SELECT exchange, symbol, market, timeframe, direction, conditions
		 FROM signals WHERE id=$1 AND owner_id=$2`,
		sigID, userID,
	).Scan(&exchange, &symbol, &market, &timeframe, &direction, &condJSON)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var req backtestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.TakeProfit <= 0 {
		req.TakeProfit = 2.0
	}
	if req.StopLoss <= 0 {
		req.StopLoss = 1.0
	}

	jobID := newUUID()
	payload := map[string]any{
		"job_id":      jobID,
		"signal_id":   sigID,
		"symbol":      symbol,
		"market":      market,
		"timeframe":   timeframe,
		"exchange":    exchange,
		"direction":   direction,
		"period_from": req.PeriodFrom,
		"period_to":   req.PeriodTo,
		"take_profit": req.TakeProfit,
		"stop_loss":   req.StopLoss,
		"conditions":  string(condJSON),
	}
	payloadJSON, _ := json.Marshal(payload)

	if err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{
		Stream: "jobs:backtest",
		Values: map[string]any{"payload": string(payloadJSON)},
	}).Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	progressKey := fmt.Sprintf("jobs:%s:progress", jobID)
	s.rdb.HSet(r.Context(), progressKey, "pct", 0, "status", "queued", "updated_at", time.Now().Unix())

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// SubmitOptimize enqueues an optimization job onto the jobs:optimize Redis stream.
// POST /signals/:id/optimize
func (s *Server) SubmitOptimize(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exchange, symbol, market, timeframe, direction string
	var condJSON json.RawMessage
	err := s.pool.QueryRow(r.Context(),
		`SELECT exchange, symbol, market, timeframe, direction, conditions
		 FROM signals WHERE id=$1 AND owner_id=$2`,
		sigID, userID,
	).Scan(&exchange, &symbol, &market, &timeframe, &direction, &condJSON)
	if err != nil {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	var req optimizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Mode == "" {
		req.Mode = "fast"
	}
	if req.ScoreBy == "" {
		req.ScoreBy = "profit_factor"
	}
	if req.TopN <= 0 {
		req.TopN = 10
	}

	jobID := newUUID()
	payload := map[string]any{
		"job_id":               jobID,
		"signal_id":            sigID,
		"symbol":               symbol,
		"market":               market,
		"timeframe":            timeframe,
		"exchange":             exchange,
		"direction":            direction,
		"period_from":          req.PeriodFrom,
		"period_to":            req.PeriodTo,
		"mode":                 req.Mode,
		"score_by":             req.ScoreBy,
		"top_n":                req.TopN,
		"take_profits":         req.TakeProfits,
		"stop_losses":          req.StopLosses,
		"param_space":          req.ParamSpace,
		"wf_folds":             req.WFFolds,
		"conditions_template":  string(condJSON),
	}
	payloadJSON, _ := json.Marshal(payload)

	if err := s.rdb.XAdd(r.Context(), &redis.XAddArgs{
		Stream: "jobs:optimize",
		Values: map[string]any{"payload": string(payloadJSON)},
	}).Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	progressKey := fmt.Sprintf("jobs:%s:optimize:progress", jobID)
	s.rdb.HSet(r.Context(), progressKey, "pct", 0, "status", "queued", "updated_at", time.Now().Unix())

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// GetBacktestResults returns all backtest results for a signal owned by the caller.
// GET /signals/:id/backtest-results
func (s *Server) GetBacktestResults(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		sigID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, symbol, timeframe, period_from, period_to, mode,
		        total_signals, win_count, loss_count, win_rate, avg_gain,
		        max_drawdown, profit_factor, patterns, created_at
		 FROM backtest_results WHERE signal_id=$1 ORDER BY created_at DESC LIMIT 50`,
		sigID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type resultRow struct {
		ID           string          `json:"id"`
		SignalID     string          `json:"signal_id"`
		Symbol       string          `json:"symbol"`
		Timeframe    string          `json:"timeframe"`
		PeriodFrom   time.Time       `json:"period_from"`
		PeriodTo     time.Time       `json:"period_to"`
		Mode         string          `json:"mode"`
		TotalSignals int             `json:"total_signals"`
		WinCount     int             `json:"win_count"`
		LossCount    int             `json:"loss_count"`
		WinRate      float64         `json:"win_rate"`
		AvgGain      float64         `json:"avg_gain"`
		MaxDrawdown  float64         `json:"max_drawdown"`
		ProfitFactor float64         `json:"profit_factor"`
		Patterns     json.RawMessage `json:"patterns"`
		CreatedAt    time.Time       `json:"created_at"`
	}

	results := make([]resultRow, 0)
	for rows.Next() {
		var row resultRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.Symbol, &row.Timeframe,
			&row.PeriodFrom, &row.PeriodTo, &row.Mode,
			&row.TotalSignals, &row.WinCount, &row.LossCount,
			&row.WinRate, &row.AvgGain, &row.MaxDrawdown, &row.ProfitFactor,
			&row.Patterns, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		results = append(results, row)
	}
	writeJSON(w, http.StatusOK, results)
}

// GetOptimizationResults returns all optimization results for a signal owned by the caller.
// GET /signals/:id/optimization-results
func (s *Server) GetOptimizationResults(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	sigID := chi.URLParam(r, "id")

	var exists bool
	s.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM signals WHERE id=$1 AND owner_id=$2)`,
		sigID, userID,
	).Scan(&exists)
	if !exists {
		writeError(w, http.StatusNotFound, "signal not found")
		return
	}

	rows, err := s.pool.Query(r.Context(),
		`SELECT id, signal_id, mode, top_combinations, best_params, created_at
		 FROM optimization_results WHERE signal_id=$1 ORDER BY created_at DESC LIMIT 50`,
		sigID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()

	type optRow struct {
		ID              string          `json:"id"`
		SignalID        string          `json:"signal_id"`
		Mode            string          `json:"mode"`
		TopCombinations json.RawMessage `json:"top_combinations"`
		BestParams      json.RawMessage `json:"best_params"`
		CreatedAt       time.Time       `json:"created_at"`
	}

	results := make([]optRow, 0)
	for rows.Next() {
		var row optRow
		if err := rows.Scan(&row.ID, &row.SignalID, &row.Mode,
			&row.TopCombinations, &row.BestParams, &row.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		results = append(results, row)
	}
	writeJSON(w, http.StatusOK, results)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -tags integration ./services/api-gateway/... -run "TestSubmitBacktest|TestSubmitOptimize|TestGetBacktest|TestGetOptimization" -v
```

Expected: 4/4 PASS.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/jobs_handler.go services/api-gateway/jobs_handler_test.go
git commit -m "feat: api-gateway job submission and results handlers"
```

---

### Task 7: WebSocket progress handler

**Files:**
- Create: `services/api-gateway/ws_handler.go`

The WebSocket endpoint authenticates via `?token=` query parameter (browsers cannot send custom headers on WebSocket upgrade requests). It polls the appropriate Redis hash key every second and streams progress JSON to the client. It closes the connection when `status` is `"done"` or the context is cancelled.

- [ ] **Step 1: Write the handler (no automated test — WS requires a live connection)**

Create `services/api-gateway/ws_handler.go`:

```go
// services/api-gateway/ws_handler.go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"sis/pkg/auth"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type progressMessage struct {
	Pct       int    `json:"pct"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updated_at"`
}

// JobProgress streams job progress over WebSocket.
// GET /ws/jobs/:id/progress?type=backtest|optimize&token=<jwt>
func (s *Server) JobProgress(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (browsers can't set headers on WS)
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	if _, err := auth.ValidateToken(tokenStr, string(s.jwtSecret)); err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	jobID := chi.URLParam(r, "id")
	jobType := r.URL.Query().Get("type")
	var progressKey string
	switch jobType {
	case "optimize":
		progressKey = fmt.Sprintf("jobs:%s:optimize:progress", jobID)
	default:
		progressKey = fmt.Sprintf("jobs:%s:progress", jobID)
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade error: %v", err)
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			vals, err := s.rdb.HGetAll(r.Context(), progressKey).Result()
			if err != nil || len(vals) == 0 {
				continue
			}

			var msg progressMessage
			if pct, ok := vals["pct"]; ok {
				fmt.Sscanf(pct, "%d", &msg.Pct)
			}
			msg.Status = vals["status"]
			if ts, ok := vals["updated_at"]; ok {
				fmt.Sscanf(ts, "%d", &msg.UpdatedAt)
			}

			data, _ := json.Marshal(msg)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

			if msg.Status == "done" {
				return
			}
		}
	}
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./services/api-gateway/...
```

Expected: exits 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add services/api-gateway/ws_handler.go
git commit -m "feat: api-gateway WebSocket job progress handler"
```

---

### Task 8: main.go — router wiring and service entry point

**Files:**
- Create: `services/api-gateway/main.go`

- [ ] **Step 1: Create main.go**

Create `services/api-gateway/main.go`:

```go
// services/api-gateway/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"sis/pkg/cache"
	"sis/pkg/db"
)

func main() {
	_ = godotenv.Load()

	dsn := mustEnv("DATABASE_URL")
	redisURL := mustEnv("REDIS_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	listenAddr := getEnv("LISTEN_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	rdb, err := cache.Connect(ctx, redisURL)
	if err != nil {
		log.Fatalf("redis connect: %v", err)
	}
	defer rdb.Close()

	s := NewServer(pool, rdb, jwtSecret)
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Auth routes — no JWT required
	r.Post("/auth/register", s.Register)
	r.Post("/auth/login", s.Login)

	// Protected routes
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
	})

	// WebSocket — auth via ?token= query param
	r.Get("/ws/jobs/{id}/progress", s.JobProgress)

	srv := &http.Server{Addr: listenAddr, Handler: r}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	log.Printf("api-gateway: listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
	log.Println("api-gateway: stopped")
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

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./services/api-gateway/...
```

Expected: exits 0.

- [ ] **Step 3: Run all unit tests**

```bash
go test ./pkg/auth/... ./services/api-gateway/... -v
```

Expected: all unit tests PASS (middleware + auth helpers). Integration tests are excluded (no `-tags integration`).

- [ ] **Step 4: Add JWT_SECRET and LISTEN_ADDR to .env**

Append to `.env` (create if missing):

```bash
JWT_SECRET=change-me-in-production
LISTEN_ADDR=:8080
```

- [ ] **Step 5: Smoke test — start the service**

```bash
go run ./services/api-gateway/. &
sleep 2

# Register
curl -s -X POST http://localhost:8080/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"smoke@example.com","password":"password123"}' | jq .

# Login and capture token
TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"smoke@example.com","password":"password123"}' | jq -r .token)

# List signals (should be empty array)
curl -s http://localhost:8080/signals \
  -H "Authorization: Bearer $TOKEN" | jq .

kill %1
```

Expected:
- Register → `{"token":"...", "user_id":"..."}`
- Login → `{"token":"...", "user_id":"..."}`
- List signals → `[]`

- [ ] **Step 6: Commit**

```bash
git add services/api-gateway/main.go .env
git commit -m "feat: api-gateway main.go router wiring and smoke test"
```

---

## Self-Review

**Spec coverage:**
- ✅ POST /auth/register — Task 4
- ✅ POST /auth/login — Task 4
- ✅ JWT middleware protecting all routes except auth — Task 3
- ✅ GET/POST /signals — Task 5
- ✅ GET/PUT/DELETE /signals/:id — Task 5
- ✅ POST /signals/:id/backtest — Task 6
- ✅ POST /signals/:id/optimize — Task 6
- ✅ GET /signals/:id/backtest-results — Task 6
- ✅ GET /signals/:id/optimization-results — Task 6
- ✅ WebSocket /ws/jobs/:id/progress — Task 7
- ✅ DB migration (password_hash) — Task 1
- ✅ Dependencies (chi, jwt, bcrypt, websocket) — Task 1

**Placeholder scan:** No TBD, TODO, or "similar to" references found.

**Type consistency:**
- `Server` struct defined in `server.go`, used consistently across all handlers
- `UserIDFromCtx` defined in `middleware.go`, used in all authenticated handlers
- `writeJSON`/`writeError` defined in `server.go`, used throughout
- `newUUID` defined in `server.go`, used in `jobs_handler.go`
- `chi.URLParam(r, "id")` used correctly in all handlers that need `:id`
- `withChiParams` test helper defined in `signals_handler_test.go`, imported in `jobs_handler_test.go` (same package `main`)
- `testConditions` constant defined in `signals_handler_test.go`, reused in `jobs_handler_test.go`
- `newTestServer`/`createTestUser`/`authHeader` helpers all defined in `auth_handler_test.go` (same package `main`, `integration` build tag)
