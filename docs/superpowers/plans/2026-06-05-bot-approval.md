# Bot Approval Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить систему согласования публикации ботов: накопление активного времени → заявка → одобрение/отклонение админом → публикация.

**Architecture:** Три новых колонки на таблице `bots` (счётчик активных секунд, момент начала текущей сессии, статус согласования) + `min_publish_days` в `coin_filter_settings`. `StartBot`/`StopBot` обновляют таймер; `PatchBot` блокирует изменение `strategy_config` у активного бота и сбрасывает таймер при изменении стратегии у остановленного. Отдельный обработчик управляет заявками и решениями админа.

**Tech Stack:** Go (pgx/v5, chi), PostgreSQL, React + TypeScript, Tailwind CSS, integration tests (`//go:build integration`)

---

## File Map

| Action | Path |
|--------|------|
| Create | `migrations/064_bot_approval.sql` |
| Create | `services/api-gateway/bot_approval_handler.go` |
| Modify | `services/api-gateway/bots_handler.go` |
| Modify | `services/api-gateway/coin_filter_handler.go` |
| Modify | `services/api-gateway/bots_handler_test.go` |
| Modify | `services/api-gateway/main.go` |
| Modify | `frontend/src/features/bots/types.ts` |
| Modify | `frontend/src/features/bots/ui-types.ts` |
| Modify | `frontend/src/features/bots/api.ts` |
| Modify | `frontend/src/pages/BotsPage.tsx` |
| Modify | `frontend/src/features/admin-defaults/types.ts` |
| Modify | `frontend/src/features/admin-defaults/api.ts` |
| Modify | `frontend/src/features/admin-defaults/AdminDefaultsTab.tsx` |
| Modify | `frontend/src/features/bots/sections/MyBotsSection.tsx` |
| Modify | `frontend/src/features/bots/components/MyBotCard.tsx` |
| Modify | `frontend/src/features/bots/components/BotForm.tsx` |
| Modify | `frontend/src/features/admin-bots/api.ts` |
| Modify | `frontend/src/features/admin-bots/AdminBotCard.tsx` |
| Modify | `frontend/src/features/admin-bots/AdminBotsTab.tsx` |

---

## Task 1: DB migration

**Files:**
- Create: `migrations/064_bot_approval.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/064_bot_approval.sql

-- Timer fields on bots
ALTER TABLE bots
  ADD COLUMN IF NOT EXISTS active_seconds_acc BIGINT      NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS active_since       TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS approval_status    TEXT
    CHECK (approval_status IN ('pending', 'approved', 'rejected'));

-- Configurable threshold (days) for submitting for approval
ALTER TABLE coin_filter_settings
  ADD COLUMN IF NOT EXISTS min_publish_days INTEGER NOT NULL DEFAULT 15;
```

- [ ] **Step 2: Apply migration locally**

```
psql "$DATABASE_URL" -f migrations/064_bot_approval.sql
```

Expected: no errors. Verify:
```sql
SELECT active_seconds_acc, active_since, approval_status FROM bots LIMIT 1;
SELECT min_publish_days FROM coin_filter_settings WHERE id = 1;
```

- [ ] **Step 3: Commit**

```
git add migrations/064_bot_approval.sql
git commit -m "feat(db): bot approval timer fields + min_publish_days"
```

---

## Task 2: Go — botResp, botCols, coinFilterSettings

**Files:**
- Modify: `services/api-gateway/bots_handler.go`
- Modify: `services/api-gateway/coin_filter_handler.go`

### 2a: botResp struct

- [ ] **Step 1: Add three fields to `botResp` (after `IgnoreCoinFilter`)**

```go
	IgnoreCoinFilter      bool            `json:"ignoreCoinFilter"`
	ActiveSecondsAcc      int64           `json:"activeSecondsAcc"`
	ActiveSince           *time.Time      `json:"activeSince"`
	ApprovalStatus        *string         `json:"approvalStatus"`
```

### 2b: botCols constant

- [ ] **Step 2: Add new columns to `botCols`**

Current last line:
```go
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs,
	b.ignore_coin_filter`
```

Replace with:
```go
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs,
	b.ignore_coin_filter,
	b.active_seconds_acc, b.active_since, b.approval_status`
```

### 2c: collectBots scan

- [ ] **Step 3: Add three scan targets at the end of `rows.Scan(...)` in `collectBots`**

Current last two lines of Scan:
```go
			&b.AccountID, &b.AutoMode, &b.MaxLongStrategies, &b.MaxShortStrategies, &b.MaxSymConsecutiveRuns,
			&b.IgnoreCoinFilter,
```

Replace with:
```go
			&b.AccountID, &b.AutoMode, &b.MaxLongStrategies, &b.MaxShortStrategies, &b.MaxSymConsecutiveRuns,
			&b.IgnoreCoinFilter,
			&b.ActiveSecondsAcc, &b.ActiveSince, &b.ApprovalStatus,
```

### 2d: coinFilterSettings struct and handlers

- [ ] **Step 4: Add `MinPublishDays` to `coinFilterSettings` struct in `coin_filter_handler.go`**

```go
type coinFilterSettings struct {
	MinTurnoverUsdt float64  `json:"min_turnover_usdt"`
	Blacklist       []string `json:"blacklist"`
	MinPublishDays  int      `json:"min_publish_days"`
}
```

- [ ] **Step 5: Update `GetCoinFilter` SELECT and Scan**

Replace:
```go
	err := s.pool.QueryRow(r.Context(),
		`SELECT min_turnover_usdt, blacklist FROM coin_filter_settings WHERE id = 1`,
	).Scan(&cfg.MinTurnoverUsdt, &cfg.Blacklist)
```

With:
```go
	err := s.pool.QueryRow(r.Context(),
		`SELECT min_turnover_usdt, blacklist, min_publish_days FROM coin_filter_settings WHERE id = 1`,
	).Scan(&cfg.MinTurnoverUsdt, &cfg.Blacklist, &cfg.MinPublishDays)
```

- [ ] **Step 6: Update `UpdateCoinFilter` to save `min_publish_days`**

Replace:
```go
	_, err := s.pool.Exec(r.Context(),
		`UPDATE coin_filter_settings
		 SET min_turnover_usdt = $1, blacklist = $2, updated_at = NOW()
		 WHERE id = 1`,
		body.MinTurnoverUsdt, body.Blacklist,
	)
```

With:
```go
	_, err := s.pool.Exec(r.Context(),
		`UPDATE coin_filter_settings
		 SET min_turnover_usdt = $1, blacklist = $2, min_publish_days = $3, updated_at = NOW()
		 WHERE id = 1`,
		body.MinTurnoverUsdt, body.Blacklist, body.MinPublishDays,
	)
```

- [ ] **Step 7: Build**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```
git add services/api-gateway/bots_handler.go services/api-gateway/coin_filter_handler.go
git commit -m "feat(bots): add approval timer fields to botResp + min_publish_days to coinFilter"
```

---

## Task 3: Go — StartBot/StopBot timer

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

### 3a: StartBot sets active_since

- [ ] **Step 1: Update `StartBot` to set `active_since` before calling `setBotStatus`**

Find the end of `StartBot`, just before `s.setBotStatus(w, r, "active")`:
```go
	s.setBotStatus(w, r, "active")
```

Replace with:
```go
	// Start the activity timer for the approval flow.
	// Only set if not already active (active_since IS NULL) to avoid reset on double-click.
	_, _ = s.pool.Exec(ctx,
		`UPDATE bots SET active_since = NOW(), updated_at = NOW()
		 WHERE id = $1 AND owner_id = $2 AND active_since IS NULL`,
		botID, callerID,
	)
	s.setBotStatus(w, r, "active")
```

### 3b: StopBot accumulates time

- [ ] **Step 2: Replace `StopBot` with a version that accumulates `active_seconds_acc`**

Replace:
```go
// POST /bots/{id}/stop
func (s *Server) StopBot(w http.ResponseWriter, r *http.Request) {
	s.setBotStatus(w, r, "stopped")
}
```

With:
```go
// POST /bots/{id}/stop
func (s *Server) StopBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Accumulate active seconds into the counter and clear the session start marker.
	_, _ = s.pool.Exec(ctx,
		`UPDATE bots
		 SET active_seconds_acc = active_seconds_acc
		       + EXTRACT(EPOCH FROM NOW() - active_since)::BIGINT,
		     active_since  = NULL,
		     updated_at    = NOW()
		 WHERE id = $1 AND owner_id = $2 AND active_since IS NOT NULL`,
		botID, callerID,
	)
	s.setBotStatus(w, r, "stopped")
}
```

- [ ] **Step 3: Build**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```
git add services/api-gateway/bots_handler.go
git commit -m "feat(bots): StartBot/StopBot track active_seconds_acc for approval timer"
```

---

## Task 4: Go — PatchBot strategy lock + PublishBot gate

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

### 4a: PatchBot — extend initial SELECT + strategy lock

- [ ] **Step 1: Extend the initial SELECT in `PatchBot` to also fetch `status` and `is_official`**

Find in `PatchBot`:
```go
	var ownerID string
	var isFork bool
	var sourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, is_fork, source_bot_id FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &isFork, &sourceID); err != nil {
```

Replace with:
```go
	var ownerID, botStatus string
	var isFork, isOfficial bool
	var sourceID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT owner_id, is_fork, source_bot_id, status, is_official FROM bots WHERE id = $1`, botID,
	).Scan(&ownerID, &isFork, &sourceID, &botStatus, &isOfficial); err != nil {
```

- [ ] **Step 2: Add strategy lock check after body decode in `PatchBot`**

Find the line after body decode (right after `if err := json.NewDecoder(r.Body).Decode(&body); err != nil {`):
```go
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	args := []interface{}{botID}
```

Replace with:
```go
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	// Block strategy_config changes on active non-official bots.
	if _, changingStrategy := body["strategyConfig"]; changingStrategy && !isOfficial {
		if botStatus == "active" {
			writeError(w, http.StatusUnprocessableEntity,
				"Нельзя изменить стратегию активного бота. Сначала остановите бота.")
			return
		}
	}

	args := []interface{}{botID}
```

- [ ] **Step 3: Add timer reset to the SET list when `strategyConfig` changes**

Find the section after all `addStr` / `addBool` / `addRaw` calls (just before `if len(sets) == 0 {`):
```go
	addInt("maxSymConsecutiveRuns", "max_sym_consecutive_runs")

	if len(sets) == 0 {
```

Replace with:
```go
	addInt("maxSymConsecutiveRuns", "max_sym_consecutive_runs")

	// Reset approval timer when strategy_config changes on a non-official user bot.
	if _, changingStrategy := body["strategyConfig"]; changingStrategy && !isOfficial {
		sets = append(sets, "active_seconds_acc = 0", "active_since = NULL")
	}

	if len(sets) == 0 {
```

### 4b: PublishBot — require approval

- [ ] **Step 4: Add approval check to `PublishBot`**

Find `PublishBot`:
```go
func (s *Server) PublishBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET is_public = true, updated_at = NOW() WHERE id = $1 AND owner_id = $2`,
		botID, callerID)
```

Replace with:
```go
func (s *Server) PublishBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Non-official bots require admin approval before publishing.
	var isOfficial bool
	var approvalStatus *string
	if err := s.pool.QueryRow(ctx,
		`SELECT is_official, approval_status FROM bots WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	).Scan(&isOfficial, &approvalStatus); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	if !isOfficial {
		if approvalStatus == nil || *approvalStatus != "approved" {
			writeError(w, http.StatusUnprocessableEntity,
				"Бот не прошёл согласование. Отправьте заявку и дождитесь одобрения администратора.")
			return
		}
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE bots SET is_public = true, updated_at = NOW() WHERE id = $1 AND owner_id = $2`,
		botID, callerID)
```

- [ ] **Step 5: Build**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```
git add services/api-gateway/bots_handler.go
git commit -m "feat(bots): PatchBot strategy lock + PublishBot requires approval"
```

---

## Task 5: Go — bot_approval_handler.go + routes + tests

**Files:**
- Create: `services/api-gateway/bot_approval_handler.go`
- Modify: `services/api-gateway/main.go`
- Modify: `services/api-gateway/bots_handler_test.go`

- [ ] **Step 1: Write the test first**

Add to `services/api-gateway/bots_handler_test.go`:

```go
func TestBotApprovalFlow(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "approval_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	adminID := createAdminTestUser(t, s, "approval_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	botID := createTestBot(t, s, userID, "Approval Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// ── Case 1: insufficient time → 422 ──────────────────────────────────────
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/request-approval", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.RequestBotApproval(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("case 1: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// ── Case 2: sufficient time → 204, status = pending ──────────────────────
	s.pool.Exec(context.Background(),
		`UPDATE bots SET active_seconds_acc = 16*86400 WHERE id = $1`, botID)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/request-approval", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.RequestBotApproval(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("case 2: expected 204, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var gotStatus string
	s.pool.QueryRow(context.Background(),
		`SELECT approval_status FROM bots WHERE id = $1`, botID).Scan(&gotStatus)
	if gotStatus != "pending" {
		t.Errorf("case 2: expected approval_status=pending, got %q", gotStatus)
	}

	// ── Case 3: admin approves → 204, status = approved ──────────────────────
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/admin/bots/"+botID+"/approve", nil)
	req3 = withUserID(req3, adminID)
	req3 = addChiParams(req3, map[string]string{"id": botID})
	s.ApproveBotPublication(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("case 3: expected 204, got %d", rec3.Code)
	}

	// ── Case 4: publish now succeeds ─────────────────────────────────────────
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
	req4 = withUserID(req4, userID)
	req4 = addChiParams(req4, map[string]string{"id": botID})
	s.PublishBot(rec4, req4)
	if rec4.Code != http.StatusNoContent {
		t.Errorf("case 4: expected 204, got %d", rec4.Code)
	}
}

func TestPublishBot_RequiresApproval(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "pub_gate@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Publish Gate", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PublishBot(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 without approval, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRejectBot(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "reject_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	adminID := createAdminTestUser(t, s, "reject_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	botID := createTestBot(t, s, userID, "Reject Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Set pending state directly
	s.pool.Exec(context.Background(),
		`UPDATE bots SET approval_status = 'pending' WHERE id = $1`, botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/bots/"+botID+"/reject", nil)
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.RejectBotPublication(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}

	var gotStatus string
	s.pool.QueryRow(context.Background(),
		`SELECT approval_status FROM bots WHERE id = $1`, botID).Scan(&gotStatus)
	if gotStatus != "rejected" {
		t.Errorf("expected rejected, got %q", gotStatus)
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL (handlers don't exist yet)**

```
go test -v -tags integration -run "TestBotApprovalFlow|TestPublishBot_RequiresApproval|TestAdminRejectBot" ./services/api-gateway/
```

Expected: compile error — `s.RequestBotApproval undefined`.

- [ ] **Step 3: Create `bot_approval_handler.go`**

```go
// services/api-gateway/bot_approval_handler.go
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// POST /bots/{id}/request-approval (RequireAuth)
// Submits a user bot for admin review.
// Requires: is_official = false, accumulated active time >= min_publish_days.
func (s *Server) RequestBotApproval(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	var accSecs int64
	var activeSince *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT active_seconds_acc, active_since
		 FROM bots WHERE id = $1 AND owner_id = $2 AND is_official = false`,
		botID, callerID,
	).Scan(&accSecs, &activeSince); err != nil {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}

	// Include current active session in effective total.
	effectiveSecs := accSecs
	if activeSince != nil {
		effectiveSecs += int64(time.Since(*activeSince).Seconds())
	}

	// Load threshold from platform settings.
	var minDays int
	if err := s.pool.QueryRow(ctx,
		`SELECT min_publish_days FROM coin_filter_settings WHERE id = 1`,
	).Scan(&minDays); err != nil {
		minDays = 15
	}

	thresholdSecs := int64(minDays) * 86400
	if effectiveSecs < thresholdSecs {
		daysActive := effectiveSecs / 86400
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("Недостаточно активных дней: %d из %d", daysActive, minDays))
		return
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE bots SET approval_status = 'pending', updated_at = NOW()
		 WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/bots/{id}/approve (RequireAdmin)
func (s *Server) ApproveBotPublication(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET approval_status = 'approved', updated_at = NOW() WHERE id = $1`,
		botID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /admin/bots/{id}/reject (RequireAdmin)
func (s *Server) RejectBotPublication(w http.ResponseWriter, r *http.Request) {
	botID := chi.URLParam(r, "id")
	tag, err := s.pool.Exec(r.Context(),
		`UPDATE bots SET approval_status = 'rejected', updated_at = NOW() WHERE id = $1`,
		botID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "bot not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Register routes in `main.go`**

In the RequireAuth group, after `r.Post("/bots/{id}/publish", s.PublishBot)`:
```go
		r.Post("/bots/{id}/request-approval", s.RequestBotApproval)
```

In the RequireAdmin group, after `r.Post("/admin/bots/{id}/reject",  s.RejectBotPublication)`:
```go
		r.Post("/admin/bots/{id}/approve", s.ApproveBotPublication)
		r.Post("/admin/bots/{id}/reject",  s.RejectBotPublication)
```

- [ ] **Step 5: Build**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Step 6: Run tests**

```
go test -v -tags integration -run "TestBotApprovalFlow|TestPublishBot_RequiresApproval|TestAdminRejectBot" ./services/api-gateway/
```

Expected: all three PASS.

- [ ] **Step 7: Run full integration suite**

```
go test -v -tags integration ./services/api-gateway/ 2>&1 | Select-String -Pattern "PASS|FAIL|---"
```

Expected: no FAIL lines.

- [ ] **Step 8: Commit**

```
git add services/api-gateway/bot_approval_handler.go services/api-gateway/main.go services/api-gateway/bots_handler_test.go
git commit -m "feat(bots): request-approval, approve, reject handlers + integration tests"
```

---

## Task 6: Frontend types, parseBot, toMyBot, BotAction

**Files:**
- Modify: `frontend/src/features/bots/types.ts`
- Modify: `frontend/src/features/bots/ui-types.ts`
- Modify: `frontend/src/features/bots/api.ts`
- Modify: `frontend/src/pages/BotsPage.tsx`

### 6a: Bot type

- [ ] **Step 1: Add three fields to `Bot` in `types.ts` (after `ignoreCoinFilter`)**

```typescript
  ignoreCoinFilter?: boolean;
  activeSecondsAcc: number;
  activeSince: string | null;   // ISO 8601 timestamp or null
  approvalStatus: 'pending' | 'approved' | 'rejected' | null;
```

- [ ] **Step 2: Add `request-approval` to `BotAction` union in `types.ts`**

After `| { type: 'publish'; botId: string }`:
```typescript
  | { type: 'request-approval'; botId: string }
```

### 6b: MyBot type

- [ ] **Step 3: Add three fields to `MyBot` in `ui-types.ts` (after `custom?`)**

```typescript
  approvalStatus: 'pending' | 'approved' | 'rejected' | null;
  activeSecondsAcc: number;
  activeSince: string | null;
```

### 6c: parseBot

- [ ] **Step 4: Add three fields to `parseBot` in `bots/api.ts` (after `autoMode`)**

```typescript
    autoMode:              (raw.autoMode as boolean) ?? false,
    activeSecondsAcc:      (raw.activeSecondsAcc as number) ?? 0,
    activeSince:           (raw.activeSince as string) ?? null,
    approvalStatus:        (raw.approvalStatus as Bot['approvalStatus']) ?? null,
```

- [ ] **Step 5: Add `request-approval` case to `useBots.action` in `bots/api.ts`**

After `case 'publish': await apiClient.post(...); break;`:
```typescript
        case 'request-approval': await apiClient.post(`/bots/${a.botId}/request-approval`); break;
```

### 6d: toMyBot

- [ ] **Step 6: Add three fields to `toMyBot` in `BotsPage.tsx` (after `custom:`)**

```typescript
    custom:            !b.sourceBotId,
    approvalStatus:    b.approvalStatus,
    activeSecondsAcc:  b.activeSecondsAcc,
    activeSince:       b.activeSince,
```

- [ ] **Step 7: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 8: Commit**

```
git add frontend/src/features/bots/types.ts frontend/src/features/bots/ui-types.ts frontend/src/features/bots/api.ts frontend/src/pages/BotsPage.tsx
git commit -m "feat(bots): approval flow types, parseBot, toMyBot, BotAction"
```

---

## Task 7: Frontend admin-defaults — min_publish_days

**Files:**
- Modify: `frontend/src/features/admin-defaults/types.ts`
- Modify: `frontend/src/features/admin-defaults/api.ts`
- Modify: `frontend/src/features/admin-defaults/AdminDefaultsTab.tsx`

### 7a: Type

- [ ] **Step 1: Add `min_publish_days` to `CoinFilterSettings` in `types.ts`**

```typescript
export interface CoinFilterSettings {
  min_turnover_usdt: number
  blacklist: string[]
  min_publish_days: number
}
```

### 7b: API cache default

- [ ] **Step 2: Update default value in `getCoinFilter` in `api.ts`**

Find:
```typescript
  _coinFilterCache = { ...d, blacklist: d.blacklist ?? [] }
```

Replace with:
```typescript
  _coinFilterCache = { ...d, blacklist: d.blacklist ?? [], min_publish_days: d.min_publish_days ?? 15 }
```

### 7c: Admin UI section

- [ ] **Step 3: Add `PublicationSection` component to `AdminDefaultsTab.tsx`**

Add after `CoinFilterSection` and before `AdminDefaultsTab` export:

```tsx
// ── Publication section ───────────────────────────────────────────────────────

function PublicationSection({
  initial,
  onSaved,
}: {
  initial: CoinFilterSettings
  onSaved: () => void
}) {
  const [minDays, setMinDays] = useState(initial.min_publish_days)
  const [saving, setSaving]   = useState(false)
  const [saved, setSaved]     = useState(false)
  const [error, setError]     = useState<string | null>(null)

  useEffect(() => { setMinDays(initial.min_publish_days) }, [initial])

  async function handleSave() {
    setSaving(true); setError(null)
    try {
      await updateCoinFilter({ ...initial, min_publish_days: minDays })
      setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally { setSaving(false) }
  }

  return (
    <div className="rounded-lg border border-slate-700 bg-slate-900/50 p-5">
      <h3 className="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-300">
        Публикация ботов
      </h3>
      <Field label="Мин. активных дней">
        <NumInput
          value={minDays}
          onChange={v => setMinDays(Math.max(1, Math.round(v)))}
        />
      </Field>
      {error && (
        <div className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
          {error}
        </div>
      )}
      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          {saving ? 'Сохранение…' : 'Сохранить'}
        </button>
        {saved && <span className="text-sm text-emerald-400">Сохранено ✓</span>}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Render `PublicationSection` in `AdminDefaultsTab` JSX**

Find the grid in the `AdminDefaultsTab` return, after `<CoinFilterSection initial={coinFilter} onSaved={handleSaved} />`:
```tsx
          {coinFilter && (
            <CoinFilterSection initial={coinFilter} onSaved={handleSaved} />
          )}
```

Replace with:
```tsx
          {coinFilter && (
            <>
              <CoinFilterSection initial={coinFilter} onSaved={handleSaved} />
              <PublicationSection initial={coinFilter} onSaved={handleSaved} />
            </>
          )}
```

- [ ] **Step 5: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```
git add frontend/src/features/admin-defaults/types.ts frontend/src/features/admin-defaults/api.ts frontend/src/features/admin-defaults/AdminDefaultsTab.tsx
git commit -m "feat(admin-defaults): min_publish_days field in PublicationSection"
```

---

## Task 8: Frontend MyBotsSection — wire approval

**Files:**
- Modify: `frontend/src/features/bots/sections/MyBotsSection.tsx`
- Modify: `frontend/src/pages/BotsPage.tsx`

- [ ] **Step 1: Add `minPublishDays` state + load to `MyBotsSection`**

Add import at top of `MyBotsSection.tsx`:
```typescript
import { useState, useEffect } from 'react'
import { getCoinFilter } from '../../admin-defaults/api'
```

Add to `Props`:
```typescript
  onRequestApproval: (botId: string) => void;
```

Add at the start of the component body:
```typescript
  const [minPublishDays, setMinPublishDays] = useState(15)
  useEffect(() => {
    getCoinFilter().then(s => setMinPublishDays(s.min_publish_days ?? 15)).catch(() => {})
  }, [])
```

- [ ] **Step 2: Pass `minPublishDays` and `onRequestApproval` to each `MyBotCard`**

Find the `<MyBotCard ... />` call and add two props:
```tsx
            <MyBotCard
              key={b.id}
              bot={b}
              minPublishDays={minPublishDays}
              onToggle={(next: RunStatus | 'paused') => onToggle(b.id, next as 'running' | 'paused')}
              onEdit={() => onEdit(b.id)}
              onDelete={() => onDelete(b.id)}
              onRequestApproval={() => onRequestApproval(b.id)}
            />
```

- [ ] **Step 3: Wire `onRequestApproval` in `BotsPage.tsx`**

In the `<BotsPageUI ... />` call, add:
```tsx
        onRequestApproval={(id) => action({ type: 'request-approval', botId: id })}
```

- [ ] **Step 4: Add `onRequestApproval` to `BotsPageUI` props in `BotsPage.tsx` (the feature component)**

Find `frontend/src/features/bots/BotsPage.tsx` and check if it passes `onRequestApproval` to `MyBotsSection`. If `BotsPageUI` accepts all props and passes them down, add `onRequestApproval` to its props type and forward it to `MyBotsSection`.

Open `frontend/src/features/bots/BotsPage.tsx` (the feature version, not the pages version) and find the props type for the default export. Add:
```typescript
  onRequestApproval: (botId: string) => void;
```

Then forward it to `<MyBotsSection onRequestApproval={onRequestApproval} ... />`.

- [ ] **Step 5: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors. Fix any missing prop forwarding as needed.

- [ ] **Step 6: Commit**

```
git add frontend/src/features/bots/sections/MyBotsSection.tsx frontend/src/pages/BotsPage.tsx
git commit -m "feat(MyBotsSection): load minPublishDays, wire onRequestApproval"
```

---

## Task 9: Frontend MyBotCard — approval UI

**Files:**
- Modify: `frontend/src/features/bots/components/MyBotCard.tsx`

- [ ] **Step 1: Add new props to `MyBotCard`**

Replace current `Props` type:
```typescript
type Props = {
  bot: MyBot;
  onToggle: (next: 'running' | 'paused') => void;
  onEdit:  () => void;
  onDelete: () => void;
};
```

With:
```typescript
type Props = {
  bot: MyBot;
  minPublishDays: number;
  onToggle: (next: 'running' | 'paused') => void;
  onEdit:  () => void;
  onDelete: () => void;
  onRequestApproval: () => void;
};
```

- [ ] **Step 2: Update function signature**

```typescript
export function MyBotCard({ bot, minPublishDays, onToggle, onEdit, onDelete, onRequestApproval }: Props) {
```

- [ ] **Step 3: Compute approval progress values inside the component body**

Add after the `const running = optimisticRunning` line:
```typescript
  const effectiveSecs = bot.activeSecondsAcc
    + (bot.activeSince
        ? (Date.now() - new Date(bot.activeSince).getTime()) / 1000
        : 0)
  const thresholdSecs = minPublishDays * 86400
  const progressPct   = Math.min(100, (effectiveSecs / thresholdSecs) * 100)
  const daysActive    = Math.floor(effectiveSecs / 86400)
  const thresholdReached = effectiveSecs >= thresholdSecs
  const isApproved    = bot.approvalStatus === 'approved'
```

- [ ] **Step 4: Add approval block to the card body**

Find the `{/* actions */}` block (the div with the three buttons). Just before it, add the approval section:
```tsx
        {/* ── Approval progress ──────────────────────────────────────────── */}
        {!isApproved && (
          <div className="mb-3 border-t border-white/[.05] pt-2.5">
            <div className="mb-1 flex items-center justify-between text-[10px] text-slate-400">
              <span>Активность</span>
              <span className="font-mono">{daysActive} / {minPublishDays} дн.</span>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-white/[.06]">
              <div
                className="h-1 rounded-full bg-blue-500 transition-all duration-500"
                style={{ width: `${progressPct}%` }}
              />
            </div>
            <div className="mt-2">
              {bot.approvalStatus === null && thresholdReached && (
                <button
                  type="button"
                  onClick={onRequestApproval}
                  className="w-full rounded-lg border border-blue-400/30 bg-blue-400/[.12] px-3 py-1.5 text-xs font-semibold text-blue-300 hover:bg-blue-400/[.18]"
                >
                  Отправить на согласование
                </button>
              )}
              {bot.approvalStatus === 'pending' && (
                <div className="flex items-center gap-1.5 rounded-lg bg-amber-400/[.08] px-3 py-1.5 text-xs text-amber-300">
                  <span>🕐</span> На рассмотрении
                </div>
              )}
              {bot.approvalStatus === 'rejected' && (
                <button
                  type="button"
                  onClick={onRequestApproval}
                  className="w-full rounded-lg border border-rose-400/30 bg-rose-400/[.10] px-3 py-1.5 text-xs font-semibold text-rose-300 hover:bg-rose-400/[.18]"
                >
                  ✕ Отклонён — переотправить
                </button>
              )}
            </div>
          </div>
        )}
        {isApproved && (
          <div className="mb-3 flex items-center gap-1.5 border-t border-white/[.05] pt-2.5 text-xs text-emerald-400">
            <span>✓</span> Одобрен — можно опубликовать
          </div>
        )}
```

- [ ] **Step 5: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```
git add frontend/src/features/bots/components/MyBotCard.tsx
git commit -m "feat(MyBotCard): approval progress bar, submit/pending/rejected/approved UI"
```

---

## Task 10: Frontend BotForm — strategy lock

**Files:**
- Modify: `frontend/src/features/bots/components/BotForm.tsx`

The `BotForm` has three outer tabs: `basic`, `activation`, `strategy`. When `bot?.status === 'active'` and `!bot?.isOfficial`, the `strategy` tab needs a lock overlay — users cannot edit strategy config of a running bot.

- [ ] **Step 1: Add `strategyLocked` computed variable**

Find in `BotForm.tsx` near the state declarations (after `const [outerTab, setOuterTab]`):
```typescript
  const [outerTab, setOuterTab]   = useState<OuterTab>('basic');
  const [stratTab, setStratTab]   = useState<StrategySubTab>('entry');
```

Add after these lines:
```typescript
  // Editing strategy_config is blocked while the bot is active (server enforces this too).
  const strategyLocked = bot?.status === 'active' && !bot?.isOfficial
```

- [ ] **Step 2: Add lock overlay around the strategy tab content**

Find the JSX block:
```tsx
          {outerTab === 'strategy' && (
```

The content starts after this condition. Wrap the entire `{outerTab === 'strategy' && (...)}` block's inner content with a relative container and overlay. The structure should be:

```tsx
          {outerTab === 'strategy' && (
            <div className="relative">
              {strategyLocked && (
                <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 rounded-lg bg-slate-900/85 backdrop-blur-sm">
                  <span className="text-3xl">🔒</span>
                  <span className="text-sm font-semibold text-slate-200">Стратегия заблокирована</span>
                  <span className="text-xs text-slate-400">Остановите бота, чтобы изменить стратегию</span>
                  <span className="mt-1 text-[10px] text-slate-500">Изменение стратегии сбросит таймер активности</span>
                </div>
              )}
              {/* EXISTING strategy tab content — do NOT change anything inside here */}
              ... (keep all existing strategy tab JSX unchanged) ...
            </div>
          )}
```

To implement: find where `{outerTab === 'strategy' && (` starts its inner div and add the relative wrapper + overlay immediately after the opening condition check. Keep all existing content inside untouched.

- [ ] **Step 3: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```
git add frontend/src/features/bots/components/BotForm.tsx
git commit -m "feat(BotForm): lock strategy tab when bot is active"
```

---

## Task 11: Frontend admin-bots — approve/reject + pending section

**Files:**
- Modify: `frontend/src/features/admin-bots/api.ts`
- Modify: `frontend/src/features/admin-bots/AdminBotCard.tsx`
- Modify: `frontend/src/features/admin-bots/AdminBotsTab.tsx`

### 11a: api.ts

- [ ] **Step 1: Add `approvalStatus` to `parseBot` in `admin-bots/api.ts`**

Find `parseBot`. After `autoMode: (raw.autoMode as boolean) ?? false,`:
```typescript
    autoMode:        (raw.autoMode as boolean) ?? false,
    approvalStatus:  (raw.approvalStatus as Bot['approvalStatus']) ?? null,
```

- [ ] **Step 2: Add `approve` and `reject` callbacks to `useAdminBots`**

After `const update = ...`:
```typescript
  const approve = useCallback(async (botId: string) => {
    await apiClient.post(`/admin/bots/${botId}/approve`);
    await load();
  }, [load]);

  const reject = useCallback(async (botId: string) => {
    await apiClient.post(`/admin/bots/${botId}/reject`);
    await load();
  }, [load]);
```

Update return value:
```typescript
  return { bots, loading, create, remove, togglePublic, update, approve, reject, refresh: load };
```

### 11b: AdminBotCard — approve/reject props

- [ ] **Step 3: Add optional `onApprove`/`onReject` to `AdminBotCard` Props type**

```typescript
type Props = {
  bot: BotType;
  onEdit?: () => void;
  onTogglePublic?: () => void;
  onDelete?: () => void;
  onApprove?: () => void;
  onReject?: () => void;
};
```

- [ ] **Step 4: Destructure new props in `AdminBotCard`**

```typescript
export function AdminBotCard({ bot, onEdit, onTogglePublic, onDelete, onApprove, onReject }: Props) {
```

- [ ] **Step 5: Add approve/reject buttons to the footer actions in `AdminBotCard`**

Find the footer actions div (the one with Edit / Eye / Trash2 buttons). Add before the delete button:
```tsx
        {onApprove && (
          <button
            type="button"
            onClick={onApprove}
            className="flex items-center gap-1 rounded-md border border-emerald-400/25 bg-emerald-400/[.10] px-2.5 py-1.5 text-xs font-semibold text-emerald-300 hover:bg-emerald-400/[.18]"
            title="Одобрить публикацию"
          >
            ✓ Одобрить
          </button>
        )}
        {onReject && (
          <button
            type="button"
            onClick={onReject}
            className="flex items-center gap-1 rounded-md border border-rose-400/20 bg-rose-400/[.08] px-2.5 py-1.5 text-xs font-semibold text-rose-300 hover:bg-rose-400/[.15]"
            title="Отклонить заявку"
          >
            ✕ Отклонить
          </button>
        )}
```

- [ ] **Step 6: Show `approvalStatus` badge in `AdminBotCard` header area**

In the top-right corner area (after the existing NOVABOT badge and status badge), add:
```tsx
        {bot.approvalStatus === 'pending' && (
          <span className="rounded-full border border-amber-400/30 bg-amber-400/[.12] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-amber-300">
            На согласовании
          </span>
        )}
        {bot.approvalStatus === 'approved' && (
          <span className="rounded-full border border-emerald-400/25 bg-emerald-400/[.10] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-emerald-300">
            Одобрен
          </span>
        )}
        {bot.approvalStatus === 'rejected' && (
          <span className="rounded-full border border-rose-400/25 bg-rose-400/[.10] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-rose-300">
            Отклонён
          </span>
        )}
```

### 11c: AdminBotsTab — pending section

- [ ] **Step 7: Add pending section and wire approve/reject to `AdminBotsTab`**

Replace the entire `AdminBotsTab` function body with:

```tsx
export function AdminBotsTab() {
  const { bots, loading, create, remove, togglePublic, update, approve, reject, refresh } = useAdminBots();
  const [creating, setCreating] = useState(false);
  const [editingBot, setEditingBot] = useState<BotType | null>(null);

  const pendingBots  = bots.filter(b => b.approvalStatus === 'pending');
  const officialBots = bots.filter(b => b.isOfficial && b.approvalStatus !== 'pending');
  const userBots     = bots.filter(b => !b.isOfficial && b.approvalStatus !== 'pending');

  async function handleCreate(data: CreateBotInput) {
    await create(data);
    setCreating(false);
  }

  async function handleEdit(botId: string, data: CreateBotInput) {
    await update(botId, data);
    setEditingBot(null);
  }

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-3">
        <div className="flex items-baseline gap-3">
          <h2 className="m-0 text-sm font-semibold text-slate-100">Библиотека ботов</h2>
          <span className="text-[11px] text-slate-400">
            {officialBots.length} NovaBot · {userBots.length} пользовательских
            {pendingBots.length > 0 && (
              <span className="ml-2 rounded-full bg-amber-400/20 px-1.5 py-0.5 text-[10px] font-bold text-amber-300">
                {pendingBots.length} на согласовании
              </span>
            )}
          </span>
        </div>
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3 py-1.5 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
        >
          <Plus size={12} strokeWidth={2.4} />
          Новый бот NovaBot
        </button>
      </div>

      {/* Create / Edit modal */}
      {creating && (
        <BotForm mode="admin" onSubmit={handleCreate} onClose={() => setCreating(false)} />
      )}
      {editingBot && (
        <BotForm
          mode="admin"
          bot={editingBot}
          onSubmit={(data) => handleEdit(editingBot.id, data)}
          onClose={() => setEditingBot(null)}
        />
      )}

      {/* Cards grid */}
      <div className="flex-1 overflow-auto px-5 py-3 space-y-6">
        {loading ? (
          <div className="py-12 text-center text-sm text-slate-500">Загрузка…</div>
        ) : (
          <>
            {/* ── Pending approval section ──────────────────────────────────────── */}
            {pendingBots.length > 0 && (
              <div>
                <h3 className="mb-3 text-xs font-bold uppercase tracking-wider text-amber-400">
                  На согласование ({pendingBots.length})
                </h3>
                <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-3">
                  {pendingBots.map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onApprove={() => approve(bot.id)}
                      onReject={() => { if (window.confirm(`Отклонить заявку бота «${bot.name}»?`)) reject(bot.id); }}
                      onDelete={() => { if (window.confirm('Удалить бота?')) remove(bot.id); }}
                    />
                  ))}
                </div>
              </div>
            )}

            {/* ── All other bots ────────────────────────────────────────────────── */}
            {(officialBots.length > 0 || userBots.length > 0) && (
              <div>
                {pendingBots.length > 0 && (
                  <h3 className="mb-3 text-xs font-bold uppercase tracking-wider text-slate-400">
                    Все боты
                  </h3>
                )}
                <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-3">
                  {[...officialBots, ...userBots].map(bot => (
                    <AdminBotCard
                      key={bot.id}
                      bot={bot}
                      onEdit={bot.isOfficial ? () => setEditingBot(bot) : undefined}
                      onTogglePublic={() => togglePublic(bot.id, !bot.isPublic)}
                      onDelete={() => { if (window.confirm('Удалить бота?')) remove(bot.id); }}
                    />
                  ))}
                </div>
              </div>
            )}

            {bots.length === 0 && (
              <div className="py-12 text-center text-sm text-slate-500">Нет ботов</div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 8: TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 9: Commit**

```
git add frontend/src/features/admin-bots/api.ts frontend/src/features/admin-bots/AdminBotCard.tsx frontend/src/features/admin-bots/AdminBotsTab.tsx
git commit -m "feat(admin-bots): approve/reject actions, pending section, approval badges"
```

---

## Final verification

- [ ] **Build Go binary**

```
go build ./services/api-gateway/...
```

Expected: no errors.

- [ ] **Run full integration tests**

```
go test -v -tags integration ./services/api-gateway/ 2>&1 | Select-String "PASS|FAIL|---"
```

Expected: all PASS.

- [ ] **TypeScript check**

```
cd frontend && npx tsc --noEmit
```

Expected: no errors.
