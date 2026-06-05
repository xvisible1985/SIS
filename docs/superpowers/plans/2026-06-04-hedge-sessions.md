# Hedge Sessions — Metrics Tracking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track hedge bot sessions in the DB and expose real-time metrics (gap at start, current gap, gap reduction, cumulative hedge P&L) via API + frontend polling, replacing the current localStorage-based approach.

**Architecture:** New `hedge_sessions` table records each activation of a hedge bot against a main strategy. The hedge engine writes to it on activation/deactivation. A new API endpoint returns session data plus a live cumulative P&L aggregated from `trade_history`. The frontend polls every 30 s and reads `gap_at_start` from the session instead of localStorage.

**Tech Stack:** Go (pgx v5, chi router), PostgreSQL/TimescaleDB, TypeScript, React

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `migrations/057_hedge_sessions.sql` | Create | New table |
| `services/api-gateway/hedge_engine.go` | Modify | Write/close sessions on activation/deactivation |
| `services/api-gateway/strategy_handler.go` | Modify | Add `GetHedgeSession` handler |
| `services/api-gateway/main.go` | Modify | Register new route |
| `frontend/src/types.ts` | Modify | Add `HedgeSession` interface |
| `frontend/src/api/strategies.ts` | Modify | Add `getHedgeSession` function |
| `frontend/src/components/strategies/HedgePairCard.tsx` | Modify | Use session, polling, updated display |

---

## Task 1: DB Migration — hedge_sessions table

**Files:**
- Create: `migrations/057_hedge_sessions.sql`

- [ ] **Step 1: Write the migration**

```sql
-- migrations/057_hedge_sessions.sql
-- Tracks each hedge bot activation session.
-- main_entry_at_start: exchange avg_entry of main position at hedge activation moment.
-- hedge_entry_at_start: exchange avg_entry of hedge position once first opened (set async).
-- gap_at_start: |main_entry - hedge_entry| — immutable reference, set when hedge first opens.
-- cumulative_hedge_pnl is computed on-the-fly in the API from trade_history.
CREATE TABLE hedge_sessions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id               UUID NOT NULL REFERENCES bots(id)       ON DELETE CASCADE,
    main_strategy_id     UUID           REFERENCES strategies(id) ON DELETE SET NULL,
    hedge_strategy_id    UUID NOT NULL   REFERENCES strategies(id) ON DELETE CASCADE,
    main_entry_at_start  NUMERIC(18, 8),
    hedge_entry_at_start NUMERIC(18, 8),
    gap_at_start         NUMERIC(18, 8),
    started_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at             TIMESTAMPTZ
);

CREATE INDEX idx_hedge_sessions_hedge ON hedge_sessions(hedge_strategy_id);
CREATE INDEX idx_hedge_sessions_main  ON hedge_sessions(main_strategy_id);
```

- [ ] **Step 2: Apply migration**

```bash
cd /opt/sis && docker compose exec api-gateway ./migrate
# or locally:
go run ./cmd/migrate
```

Expected: migration runs without error, table `hedge_sessions` visible in DB.

- [ ] **Step 3: Verify table exists**

```sql
\d hedge_sessions
```

Expected: columns id, bot_id, main_strategy_id, hedge_strategy_id, main_entry_at_start, hedge_entry_at_start, gap_at_start, started_at, ended_at.

- [ ] **Step 4: Commit**

```bash
git add migrations/057_hedge_sessions.sql
git commit -m "feat: add hedge_sessions table"
```

---

## Task 2: Backend — Write/close sessions in hedge_engine.go

**Files:**
- Modify: `services/api-gateway/hedge_engine.go`

Key locations:
- After `s.createBotStrategy(...)` call (~line 549) — INSERT session
- Inside `checkHedgeDeactivation` where `hedgePos` is available (~line 1063) — UPDATE hedge_entry_at_start
- Inside `stopHedgeStrategy` (~line 1112) — close session

- [ ] **Step 1: Insert session on hedge activation**

In `checkHedgeActivation`, after the block that logs the activation success (after `s.applyHedgeMainControls`), add:

```go
// ── Record hedge session ──────────────────────────────────────────────
if hedgeStrategyID != "" {
    var mainEntryAtStart *float64
    if pos.EntryPrice > 0 {
        v := pos.EntryPrice
        mainEntryAtStart = &v
    }
    var mainStratIDPtr *string
    if mainStrategyID != "" {
        mainStratIDPtr = &mainStrategyID
    }
    if _, sessErr := s.pool.Exec(ctx,
        `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, main_entry_at_start)
         VALUES ($1, $2, $3, $4)`,
        botID, mainStratIDPtr, hedgeStrategyID, mainEntryAtStart,
    ); sessErr != nil {
        s.logBotEvent(ctx, botID,
            fmt.Sprintf("Хедж: ошибка записи сессии для %s: %v", hedgeStrategyID[:8], sessErr),
            "warn", "hedge")
    }
}
```

This goes right after the `if hedgeStrategyID != ""` block that calls `applyHedgeMainControls` (around line 577). The full block becomes:

```go
if hedgeStrategyID != "" {
    s.applyHedgeMainControls(ctx, botID, mainStrategyID, hedgeStrategyID, cfg)

    // ── Record hedge session ──────────────────────────────────────────────
    var mainEntryAtStart *float64
    if pos.EntryPrice > 0 {
        v := pos.EntryPrice
        mainEntryAtStart = &v
    }
    var mainStratIDPtr *string
    if mainStrategyID != "" {
        mainStratIDPtr = &mainStrategyID
    }
    if _, sessErr := s.pool.Exec(ctx,
        `INSERT INTO hedge_sessions (bot_id, main_strategy_id, hedge_strategy_id, main_entry_at_start)
         VALUES ($1, $2, $3, $4)`,
        botID, mainStratIDPtr, hedgeStrategyID, mainEntryAtStart,
    ); sessErr != nil {
        s.logBotEvent(ctx, botID,
            fmt.Sprintf("Хедж: ошибка записи сессии для %s: %v", hedgeStrategyID[:8], sessErr),
            "warn", "hedge")
    }
}
```

- [ ] **Step 2: Update hedge_entry_at_start when hedge position first opens**

In `checkHedgeDeactivation`, inside the `for _, h := range hedges` loop, after the line `hedgePos, hasHedge := bySymbol[hedgeSide]` (~line 1063), add:

```go
// Fill hedge_entry_at_start once the hedge position is visible on the exchange.
// gap_at_start = |main_entry - hedge_entry| — computed here, immutable afterwards.
if hasHedge && hedgePos.EntryPrice > 0 {
    s.pool.Exec(ctx, //nolint:errcheck
        `UPDATE hedge_sessions
         SET hedge_entry_at_start = $1,
             gap_at_start = CASE
                 WHEN main_entry_at_start IS NOT NULL
                 THEN ABS(main_entry_at_start - $1)
                 ELSE NULL
             END
         WHERE hedge_strategy_id = $2
           AND ended_at IS NULL
           AND hedge_entry_at_start IS NULL`,
        hedgePos.EntryPrice, h.id)
}
```

- [ ] **Step 3: Close session on hedge deactivation**

In `stopHedgeStrategy`, after the UPDATE strategies statement (after error check, around line 1121), add:

```go
// Close the hedge session.
s.pool.Exec(ctx, //nolint:errcheck
    `UPDATE hedge_sessions SET ended_at = NOW()
     WHERE hedge_strategy_id = $1 AND ended_at IS NULL`,
    strategyID)
```

- [ ] **Step 4: Build and verify compilation**

```bash
cd services/api-gateway && go build ./...
```

Expected: compiles with no errors.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/hedge_engine.go
git commit -m "feat: write hedge sessions on activation/deactivation"
```

---

## Task 3: Backend — API endpoint GET /strategies/{id}/hedge-session

**Files:**
- Modify: `services/api-gateway/strategy_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Add GetHedgeSession handler to strategy_handler.go**

Add at the bottom of `strategy_handler.go`:

```go
// GetHedgeSession returns the most recent hedge session for a strategy.
// The strategy ID can be either the main_strategy_id or hedge_strategy_id.
// Cumulative hedge P&L is computed live from trade_history.
// GET /strategies/{id}/hedge-session
func (s *Server) GetHedgeSession(w http.ResponseWriter, r *http.Request) {
    userID := UserIDFromCtx(r.Context())
    stratID := chi.URLParam(r, "id")

    type sessionResp struct {
        ID                  string   `json:"id"`
        BotID               string   `json:"bot_id"`
        MainStrategyID      *string  `json:"main_strategy_id"`
        HedgeStrategyID     string   `json:"hedge_strategy_id"`
        MainEntryAtStart    *float64 `json:"main_entry_at_start"`
        HedgeEntryAtStart   *float64 `json:"hedge_entry_at_start"`
        GapAtStart          *float64 `json:"gap_at_start"`
        StartedAt           string   `json:"started_at"`
        EndedAt             *string  `json:"ended_at"`
        CumulativeHedgePnl  float64  `json:"cumulative_hedge_pnl"`
    }

    var resp sessionResp
    err := s.pool.QueryRow(r.Context(), `
        SELECT
            hs.id::text,
            hs.bot_id::text,
            hs.main_strategy_id::text,
            hs.hedge_strategy_id::text,
            hs.main_entry_at_start,
            hs.hedge_entry_at_start,
            hs.gap_at_start,
            hs.started_at::text,
            hs.ended_at::text,
            COALESCE((
                SELECT SUM(th.net_pnl)
                FROM trade_history th
                WHERE th.strategy_id = hs.hedge_strategy_id
                  AND th.closed_at >= hs.started_at
                  AND (hs.ended_at IS NULL OR th.closed_at <= hs.ended_at)
            ), 0)
        FROM hedge_sessions hs
        WHERE (hs.main_strategy_id = $1::uuid OR hs.hedge_strategy_id = $1::uuid)
          AND hs.bot_id IN (SELECT id FROM bots WHERE owner_id = $2::uuid)
        ORDER BY hs.started_at DESC
        LIMIT 1`,
        stratID, userID,
    ).Scan(
        &resp.ID, &resp.BotID, &resp.MainStrategyID, &resp.HedgeStrategyID,
        &resp.MainEntryAtStart, &resp.HedgeEntryAtStart, &resp.GapAtStart,
        &resp.StartedAt, &resp.EndedAt, &resp.CumulativeHedgePnl,
    )
    if err != nil {
        writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
        return
    }

    writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 2: Register route in main.go**

In `main.go`, after the line `r.Get("/strategies/{id}/state", s.GetStrategyState)`, add:

```go
r.Get("/strategies/{id}/hedge-session", s.GetHedgeSession)
```

- [ ] **Step 3: Build**

```bash
cd services/api-gateway && go build ./...
```

Expected: compiles with no errors.

- [ ] **Step 4: Manual smoke test (after deploy or local run)**

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/strategies/<known-hedge-strategy-id>/hedge-session | jq .
```

Expected: JSON with `id`, `bot_id`, `main_entry_at_start`, `gap_at_start`, `cumulative_hedge_pnl`.
If no session exists yet: `{"error":"session not found"}`.

- [ ] **Step 5: Commit**

```bash
git add services/api-gateway/strategy_handler.go services/api-gateway/main.go
git commit -m "feat: GET /strategies/{id}/hedge-session endpoint"
```

---

## Task 4: Frontend — HedgeSession type + API client

**Files:**
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/api/strategies.ts`

- [ ] **Step 1: Add HedgeSession to types.ts**

Add after `StrategyState` interface (around line 333):

```ts
export interface HedgeSession {
  id: string
  bot_id: string
  main_strategy_id: string | null
  hedge_strategy_id: string
  main_entry_at_start: number | null
  hedge_entry_at_start: number | null
  gap_at_start: number | null
  started_at: string
  ended_at: string | null
  cumulative_hedge_pnl: number
}
```

- [ ] **Step 2: Add getHedgeSession to api/strategies.ts**

Add at the top of `strategies.ts`, extend the import:

```ts
import type { Strategy, StrategyState, StrategyEvent, StrategyFormData, CycleAuditData, HedgeSession } from '../types'
```

Add function at the end of the file:

```ts
export async function getHedgeSession(strategyId: string): Promise<HedgeSession | null> {
  try {
    const res = await apiClient.get<HedgeSession>(`/strategies/${strategyId}/hedge-session`)
    return res.data
  } catch {
    return null
  }
}
```

- [ ] **Step 3: TypeScript check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/types.ts frontend/src/api/strategies.ts
git commit -m "feat: HedgeSession type and getHedgeSession API client"
```

---

## Task 5: Frontend — HedgePairCard: session data, polling, updated display

**Files:**
- Modify: `frontend/src/components/strategies/HedgePairCard.tsx`

**Context:** The current card uses `localStorage` for `initialGap` and loads data only once on expand. We replace localStorage with `hedgeSession?.gap_at_start`, add 30-second polling while expanded, and replace "P&L Main / P&L Hedge" rows with "Накоплено хеджем".

- [ ] **Step 1: Add import for getHedgeSession and HedgeSession**

In the imports section, update the strategies API import:

```ts
import {
  getStrategyState, getStrategyEvents,
  setStrategyStatus, detachFromBot, addBotBlacklist,
  getHedgeSession,
} from '../../api/strategies'
import type { HedgeSession } from '../../types'
```

- [ ] **Step 2: Add hedgeSession state, remove gapKey/initialGap localStorage state**

Replace these two lines (~line 337-345):

```tsx
// ── gap key (stable per pair) ─────────────────────────────────────────────
const gapKey = `hgap_${main.id}_${hedge.id}`
```

and

```tsx
const [initialGap, setInitialGap] = useState<number | null>(() => {
  const v = localStorage.getItem(gapKey)
  return v ? parseFloat(v) : null
})
useEffect(() => {
  if (!expanded || currentGap === null || initialGap !== null) return
  localStorage.setItem(gapKey, currentGap.toFixed(6))
  setInitialGap(currentGap)
}, [expanded, currentGap, initialGap, gapKey])

const gapReduced = initialGap !== null && currentGap !== null ? initialGap - currentGap : null
```

With:

```tsx
const [hedgeSession, setHedgeSession] = useState<HedgeSession | null>(null)

const gapAtStart  = hedgeSession?.gap_at_start ?? null
const gapReduced  = gapAtStart !== null && currentGap !== null ? gapAtStart - currentGap : null
```

- [ ] **Step 3: Update initial data-load effect to also fetch session**

Replace the existing `useEffect` for expanded data loading (~lines 315-329):

```tsx
useEffect(() => {
  if (!expanded) return
  setDataLoading(true)
  Promise.all([
    getStrategyState(main.id).catch(() => null),
    getStrategyState(hedge.id).catch(() => null),
    getStrategyEvents(main.id,  { limit: 60 }).catch(() => ({ total: 0, events: [] as StrategyEvent[] })),
    getStrategyEvents(hedge.id, { limit: 60 }).catch(() => ({ total: 0, events: [] as StrategyEvent[] })),
    getHedgeSession(hedge.id).catch(() => null),
  ]).then(([ms, hs, me, he, sess]) => {
    setMainState(ms)
    setHedgeState(hs)
    setMainEvents(me.events)
    setHedgeEvents(he.events)
    if (sess) setHedgeSession(sess)
  }).finally(() => setDataLoading(false))
}, [expanded, main.id, hedge.id])
```

- [ ] **Step 4: Add polling effect for states + session (30 s)**

Add a new `useEffect` after the one above:

```tsx
// Poll strategy states and hedge session every 30 s while card is expanded.
useEffect(() => {
  if (!expanded) return
  const refresh = () => {
    Promise.all([
      getStrategyState(main.id).catch(() => null),
      getStrategyState(hedge.id).catch(() => null),
      getHedgeSession(hedge.id).catch(() => null),
    ]).then(([ms, hs, sess]) => {
      if (ms) setMainState(ms)
      if (hs) setHedgeState(hs)
      if (sess) setHedgeSession(sess)
    })
  }
  const timer = setInterval(refresh, 30_000)
  return () => clearInterval(timer)
}, [expanded, main.id, hedge.id])
```

- [ ] **Step 5: Update stats display — gap rows and P&L**

In the left-column stats block, replace:

```tsx
<StatRow
  label="Разрыв на старте"
  value={initialGap !== null ? fmtPrice(initialGap, dec) : '—'}
/>
```

With:

```tsx
<StatRow
  label="Разрыв на старте"
  value={gapAtStart !== null ? fmtPrice(gapAtStart, dec) : '—'}
/>
```

Then replace the P&L Main + P&L Hedge rows (and their preceding divider):

```tsx
<div className="h-px bg-white/[.05] my-1.5" />

<StatRow
  label="P&L Main (тек.)"
  value={fmtPnl(mainPnl)}
  color={mainPnl !== null ? (mainPnl > 0 ? '#6ee7b7' : mainPnl < 0 ? '#fca5a5' : undefined) : undefined}
/>
<StatRow
  label="P&L Hedge (тек.)"
  value={fmtPnl(hedgePnl)}
  color={hedgePnl !== null ? (hedgePnl > 0 ? '#6ee7b7' : hedgePnl < 0 ? '#fca5a5' : undefined) : undefined}
/>
```

With:

```tsx
<div className="h-px bg-white/[.05] my-1.5" />

<StatRow
  label="Накоплено хеджем"
  value={hedgeSession != null ? fmtPnl(hedgeSession.cumulative_hedge_pnl) : '—'}
  color={
    hedgeSession != null && hedgeSession.cumulative_hedge_pnl !== 0
      ? hedgeSession.cumulative_hedge_pnl > 0 ? '#6ee7b7' : '#fca5a5'
      : undefined
  }
/>
<StatRow
  label="Сессия начата"
  value={hedgeSession != null
    ? new Date(hedgeSession.started_at).toLocaleString('ru-RU', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
    : '—'}
/>
```

- [ ] **Step 6: TypeScript check**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 7: Remove unused variables**

If TypeScript complains about `gapKey` or `initialGap` being unused (from the old code), make sure they are fully removed. Search for remaining references:

```bash
grep -n "gapKey\|initialGap\|localStorage" frontend/src/components/strategies/HedgePairCard.tsx
```

Expected: no matches.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/strategies/HedgePairCard.tsx
git commit -m "feat: hedge session metrics in HedgePairCard — polling, gap_at_start from DB, cumulative P&L"
```

---

## Self-Review

**Spec coverage:**

| Requirement | Task |
|---|---|
| Сохранять нач. параметры в БД при захвате | Task 1 (table) + Task 2 (insert on activation) |
| hedge_entry_at_start — когда хедж открыл позицию | Task 2 Step 2 |
| gap_at_start — не обнуляется при переоткрытии хеджа | Task 2: записывается один раз (WHERE hedge_entry_at_start IS NULL) |
| Поллинг и обновление текущего разрыва | Task 5 Steps 4-5 |
| Убрать localStorage | Task 5 Step 2 |
| Накопленный P&L хедж-бота | Task 3 (SUM net_pnl) + Task 5 Step 5 |
| Закрытие сессии при деактивации | Task 2 Step 3 |

**Placeholder scan:** ✅ нет TBD/TODO.

**Type consistency:**
- `HedgeSession.cumulative_hedge_pnl: number` → `fmtPnl(hedgeSession.cumulative_hedge_pnl)` — `fmtPnl` принимает `number | null`, но мы передаём `number` (всегда из COALESCE 0). ✅
- `gapAtStart` заменяет `initialGap` во всех местах: `fmtPrice(gapAtStart, dec)` и `gapReduced` computation. ✅
- `getHedgeSession` возвращает `HedgeSession | null`, обработан через `if (sess) setHedgeSession(sess)`. ✅
