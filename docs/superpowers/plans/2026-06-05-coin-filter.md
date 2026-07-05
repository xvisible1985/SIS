# Coin Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a coin filter system that warns users about low-liquidity / blacklisted coins in pickers and blocks signal bots from starting on blacklisted symbols.

**Architecture:** Global `coin_filter_settings` table (single row) stores a turnover threshold and manual blacklist. The backend blocks `StartBot` for signal bots whose symbol is blacklisted (unless `ignore_coin_filter=true` on the bot). The frontend loads filter settings (module-level cache) to flag coins in CoinPicker/CoinMultiPicker with ⚠️ indicators and a reason tooltip. BotForm adds an "Игнорировать фильтр монет" toggle (signal bots only) and a confirm dialog when submitting with a flagged symbol.

**Tech Stack:** Go (pgx/v5, chi), React + TypeScript, Tailwind CSS, Vitest (unit), integration tests with `//go:build integration`

---

## File Map

| Action | Path |
|--------|------|
| Create | `migrations/063_coin_filter.sql` |
| Create | `services/api-gateway/coin_filter_handler.go` |
| Modify | `services/api-gateway/main.go` |
| Modify | `services/api-gateway/bots_handler.go` |
| Modify | `services/api-gateway/bots_handler_test.go` |
| Modify | `frontend/src/features/admin-defaults/types.ts` |
| Modify | `frontend/src/features/admin-defaults/api.ts` |
| Modify | `frontend/src/features/admin-defaults/AdminDefaultsTab.tsx` |
| Create | `frontend/src/components/common/coinFilter.ts` |
| Modify | `frontend/src/components/common/CoinPicker.tsx` |
| Modify | `frontend/src/components/common/CoinMultiPicker.tsx` |
| Modify | `frontend/src/features/bots/types.ts` |
| Modify | `frontend/src/features/bots/components/BotForm.tsx` |

---

## Task 1: DB migration

**Files:**
- Create: `migrations/063_coin_filter.sql`

- [ ] **Step 1: Write migration**

```sql
-- migrations/063_coin_filter.sql

-- Single-row table for coin filter settings
CREATE TABLE IF NOT EXISTS coin_filter_settings (
  id                INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  min_turnover_usdt NUMERIC  NOT NULL DEFAULT 500000,
  blacklist         TEXT[]   NOT NULL DEFAULT '{}',
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed default row
INSERT INTO coin_filter_settings DEFAULT VALUES ON CONFLICT DO NOTHING;

-- Per-bot override flag
ALTER TABLE bots
  ADD COLUMN IF NOT EXISTS ignore_coin_filter BOOLEAN NOT NULL DEFAULT FALSE;
```

- [ ] **Step 2: Apply migration locally**

Run (the project uses `db.Migrate` in tests which picks up all `migrations/*.sql`; to apply manually):
```
psql "$DATABASE_URL" -f migrations/063_coin_filter.sql
```
Expected: no errors. `SELECT * FROM coin_filter_settings;` returns one row with `id=1, min_turnover_usdt=500000, blacklist={}`.

- [ ] **Step 3: Commit**

```
git add migrations/063_coin_filter.sql
git commit -m "feat(db): coin_filter_settings table + ignore_coin_filter on bots"
```

---

## Task 2: Go handler + routes

**Files:**
- Create: `services/api-gateway/coin_filter_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Write the handler file**

`services/api-gateway/coin_filter_handler.go`:
```go
package main

import (
	"encoding/json"
	"net/http"
)

type coinFilterSettings struct {
	MinTurnoverUsdt float64  `json:"min_turnover_usdt"`
	Blacklist        []string `json:"blacklist"`
}

// GetCoinFilter returns coin filter settings.
// GET /coin-filter (all authenticated users)
func (s *Server) GetCoinFilter(w http.ResponseWriter, r *http.Request) {
	var cfg coinFilterSettings
	err := s.pool.QueryRow(r.Context(),
		`SELECT min_turnover_usdt, blacklist FROM coin_filter_settings WHERE id = 1`,
	).Scan(&cfg.MinTurnoverUsdt, &cfg.Blacklist)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.Blacklist == nil {
		cfg.Blacklist = []string{}
	}
	writeJSON(w, http.StatusOK, cfg)
}

// UpdateCoinFilter replaces coin filter settings.
// PUT /admin/coin-filter (admin only)
func (s *Server) UpdateCoinFilter(w http.ResponseWriter, r *http.Request) {
	var body coinFilterSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Blacklist == nil {
		body.Blacklist = []string{}
	}
	_, err := s.pool.Exec(r.Context(),
		`UPDATE coin_filter_settings
		 SET min_turnover_usdt = $1, blacklist = $2, updated_at = NOW()
		 WHERE id = 1`,
		body.MinTurnoverUsdt, body.Blacklist,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 2: Register routes in `main.go`**

In `main.go`, find the block that has the strategy-defaults routes:
```go
		// Strategy defaults (all authenticated users — read-only)
		r.Get("/strategy-defaults", s.GetStrategyDefaults)
```
Add after it:
```go
		// Coin filter settings (all authenticated users — read-only)
		r.Get("/coin-filter", s.GetCoinFilter)
```

Then find the admin strategy-defaults block:
```go
			// Admin: strategy defaults management
			r.Get("/admin/strategy-defaults", s.GetStrategyDefaults)
			r.Put("/admin/strategy-defaults/{type}", s.UpdateStrategyDefaults)
```
Add after it:
```go
			// Admin: coin filter management
			r.Put("/admin/coin-filter", s.UpdateCoinFilter)
```

- [ ] **Step 3: Build and verify**

```
go build ./services/api-gateway/...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```
git add services/api-gateway/coin_filter_handler.go services/api-gateway/main.go
git commit -m "feat(api): GET /coin-filter and PUT /admin/coin-filter endpoints"
```

---

## Task 3: Go bots_handler — expose + check `ignore_coin_filter`

**Files:**
- Modify: `services/api-gateway/bots_handler.go`

### 3a: Add `IgnoreCoinFilter` to `botResp`

- [ ] **Step 1: Add field to `botResp` struct**

Find the `botResp` struct (around line 24). Add `IgnoreCoinFilter` at the end:
```go
	IgnoreCoinFilter      bool            `json:"ignoreCoinFilter"`
```

- [ ] **Step 2: Add column to `botCols`**

Find `const botCols = ...`. Replace the current last line of the string:
```go
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs`
```
With:
```go
	b.account_id, b.auto_mode, b.max_long_strategies, b.max_short_strategies, b.max_sym_consecutive_runs,
	b.ignore_coin_filter`
```

- [ ] **Step 3: Add to `collectBots` Scan**

Find the `rows.Scan(...)` call in `collectBots`. It currently ends with:
```go
			&b.AccountID, &b.AutoMode, &b.MaxLongStrategies, &b.MaxShortStrategies, &b.MaxSymConsecutiveRuns,
```
Add `&b.IgnoreCoinFilter` at the end:
```go
			&b.AccountID, &b.AutoMode, &b.MaxLongStrategies, &b.MaxShortStrategies, &b.MaxSymConsecutiveRuns,
			&b.IgnoreCoinFilter,
```

### 3b: Add to `CreateBot`

- [ ] **Step 4: Add to request struct in `CreateBot`**

Find the `var req struct { ... }` in `CreateBot`. After `AutoMode bool`:
```go
		AutoMode             bool            `json:"autoMode"`
		IgnoreCoinFilter      bool            `json:"ignoreCoinFilter"`
```

- [ ] **Step 5: Add to INSERT in `CreateBot`**

Find the `INSERT INTO bots (...)` query. The current columns end with `auto_mode`. Add `ignore_coin_filter`:
```go
		INSERT INTO bots (owner_id, name, description, full_description, avatar_url, is_public,
		                  account_id, symbol_whitelist, symbol_blacklist, triggers, strategy_config,
		                  max_strategies, max_long_strategies, max_short_strategies, max_margin_usdt, max_sym_consecutive_runs, auto_mode, ignore_coin_filter)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING id
```
And add `req.IgnoreCoinFilter` at the end of the args list:
```go
		callerID, req.Name, req.Description, req.FullDescription, req.AvatarURL, req.IsPublic,
		req.AccountID, req.SymbolWhitelist, req.SymbolBlacklist,
		[]byte(req.Triggers), []byte(req.StrategyConfig),
		req.MaxStrategies, req.MaxLongStrategies, req.MaxShortStrategies, req.MaxMarginUsdt, req.MaxSymConsecutiveRuns, req.AutoMode, req.IgnoreCoinFilter,
```

### 3c: Add to `PatchBot`

- [ ] **Step 6: Add `addBool` call for `ignoreCoinFilter` in `PatchBot`**

Find the block of `addBool(...)` calls in `PatchBot`. After `addBool("autoMode", "auto_mode")`:
```go
	addBool("ignoreCoinFilter", "ignore_coin_filter")
```

### 3d: Modify `StartBot`

- [ ] **Step 7: Replace `StartBot` with coin-filter-aware version**

Replace:
```go
// POST /bots/{id}/start
func (s *Server) StartBot(w http.ResponseWriter, r *http.Request) {
	s.setBotStatus(w, r, "active")
}
```
With:
```go
// POST /bots/{id}/start
func (s *Server) StartBot(w http.ResponseWriter, r *http.Request) {
	callerID := UserIDFromCtx(r.Context())
	botID := chi.URLParam(r, "id")
	ctx := r.Context()

	// Fetch bot kind, symbol, and override flag to check coin filter.
	var botKind, symbol string
	var ignoreCoinFilter bool
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(strategy_config->>'bot_kind', ''),
		        COALESCE(strategy_config->>'symbol', ''),
		        COALESCE(ignore_coin_filter, false)
		 FROM bots WHERE id = $1 AND owner_id = $2`,
		botID, callerID,
	).Scan(&botKind, &symbol, &ignoreCoinFilter)
	if err != nil {
		// bot not found — let setBotStatus return 404
		s.setBotStatus(w, r, "active")
		return
	}

	// For signal bots without the override, check the global blacklist.
	if botKind == "signal" && !ignoreCoinFilter && symbol != "" {
		var blacklist []string
		if dbErr := s.pool.QueryRow(ctx,
			`SELECT blacklist FROM coin_filter_settings WHERE id = 1`,
		).Scan(&blacklist); dbErr == nil {
			for _, b := range blacklist {
				if b == symbol {
					writeError(w, http.StatusUnprocessableEntity,
						"Монета «"+symbol+"» находится в чёрном списке фильтра монет. "+
							"Включите «Игнорировать фильтр монет» в настройках бота, чтобы продолжить.")
					return
				}
			}
		}
	}

	s.setBotStatus(w, r, "active")
}
```

- [ ] **Step 8: Build**

```
go build ./services/api-gateway/...
```
Expected: no errors.

- [ ] **Step 9: Commit**

```
git add services/api-gateway/bots_handler.go
git commit -m "feat(bots): ignore_coin_filter field + StartBot blacklist check"
```

---

## Task 4: Go integration test

**Files:**
- Modify: `services/api-gateway/bots_handler_test.go`

- [ ] **Step 1: Write the test**

Add to the end of `bots_handler_test.go`:
```go
func TestStartBot_CoinFilterBlocked(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "coinfilter@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Filter Test", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Make it a signal bot with a symbol we will blacklist
	s.pool.Exec(context.Background(),
		`UPDATE bots SET strategy_config = $1 WHERE id = $2`,
		`{"bot_kind":"signal","symbol":"TRASHUSDT"}`, botID)

	// Add to blacklist
	s.pool.Exec(context.Background(),
		`UPDATE coin_filter_settings SET blacklist = ARRAY['TRASHUSDT'] WHERE id = 1`)
	defer s.pool.Exec(context.Background(),
		`UPDATE coin_filter_settings SET blacklist = '{}' WHERE id = 1`)

	// Should be blocked — expect 422
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/start", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.StartBot(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (blocked by blacklist), got %d: %s", rec.Code, rec.Body.String())
	}

	// Enable override — should succeed (204)
	s.pool.Exec(context.Background(),
		`UPDATE bots SET ignore_coin_filter = true WHERE id = $1`, botID)
	defer s.pool.Exec(context.Background(),
		`UPDATE bots SET status = 'stopped' WHERE id = $1`, botID)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/start", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.StartBot(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("expected 204 (override enabled), got %d: %s", rec2.Code, rec2.Body.String())
	}
}
```

- [ ] **Step 2: Run the integration test**

```
go test -v -tags integration -run TestStartBot_CoinFilterBlocked ./services/api-gateway/
```
Expected: `--- PASS: TestStartBot_CoinFilterBlocked`

- [ ] **Step 3: Run full integration test suite to check no regressions**

```
go test -v -tags integration -run TestStartStopBot ./services/api-gateway/
```
Expected: PASS (existing test creates a bot with no bot_kind, so coin filter check is skipped).

- [ ] **Step 4: Commit**

```
git add services/api-gateway/bots_handler_test.go
git commit -m "test(bots): StartBot blocked by coin filter blacklist"
```

---

## Task 5: Frontend types + API

**Files:**
- Modify: `frontend/src/features/admin-defaults/types.ts`
- Modify: `frontend/src/features/admin-defaults/api.ts`

- [ ] **Step 1: Add `CoinFilterSettings` type**

In `frontend/src/features/admin-defaults/types.ts`, append:
```typescript
export interface CoinFilterSettings {
  min_turnover_usdt: number
  blacklist: string[]
}
```

- [ ] **Step 2: Add coin filter API functions to `api.ts`**

In `frontend/src/features/admin-defaults/api.ts`, add after the existing imports:
```typescript
import type { AllStrategyDefaults, GridDefaults, MatrixDefaults, CoinFilterSettings } from './types'
```
(Replace the existing import line that doesn't include `CoinFilterSettings`.)

Then append at the end of the file:
```typescript
let _coinFilterCache: CoinFilterSettings | null = null

export async function getCoinFilter(): Promise<CoinFilterSettings> {
  if (_coinFilterCache) return _coinFilterCache
  const res = await apiClient.get<CoinFilterSettings>('/coin-filter')
  _coinFilterCache = res.data ?? { min_turnover_usdt: 500000, blacklist: [] }
  return _coinFilterCache
}

export function invalidateCoinFilterCache(): void {
  _coinFilterCache = null
}

export async function updateCoinFilter(settings: CoinFilterSettings): Promise<void> {
  await apiClient.put('/admin/coin-filter', settings)
  invalidateCoinFilterCache()
}
```

- [ ] **Step 3: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 4: Commit**

```
git add frontend/src/features/admin-defaults/types.ts frontend/src/features/admin-defaults/api.ts
git commit -m "feat(admin-defaults): CoinFilterSettings type + getCoinFilter/updateCoinFilter API"
```

---

## Task 6: Frontend coin filter utility

**Files:**
- Create: `frontend/src/components/common/coinFilter.ts`

This module is imported by `CoinPicker`, `CoinMultiPicker`, and `BotForm`. It re-exports `getCoinFilter` and provides a synchronous `checkCoinFlagged` helper.

- [ ] **Step 1: Write `coinFilter.ts`**

```typescript
// frontend/src/components/common/coinFilter.ts
import { getCoinFilter } from '../../features/admin-defaults/api'
import type { CoinFilterSettings } from '../../features/admin-defaults/types'

export { getCoinFilter }
export type { CoinFilterSettings }

function fmtVol(v: number): string {
  if (v >= 1e9) return (v / 1e9).toFixed(1) + 'B'
  if (v >= 1e6) return (v / 1e6).toFixed(0) + 'M'
  if (v >= 1e3) return (v / 1e3).toFixed(0) + 'K'
  return v.toFixed(0)
}

/**
 * Given a symbol, its 24-hour turnover in USDT, and the current filter settings,
 * returns whether the coin is flagged and a human-readable reason.
 *
 * @param symbol   e.g. "PEPEUSDT"
 * @param turnover 24h USDT volume
 * @param settings loaded from getCoinFilter()
 */
export function checkCoinFlagged(
  symbol: string,
  turnover: number,
  settings: CoinFilterSettings,
): { flagged: boolean; reason: string } {
  if (settings.blacklist.includes(symbol)) {
    return { flagged: true, reason: 'В чёрном списке' }
  }
  if (settings.min_turnover_usdt > 0 && turnover < settings.min_turnover_usdt) {
    return {
      flagged: true,
      reason: `Объём ${fmtVol(turnover)} < ${fmtVol(settings.min_turnover_usdt)}`,
    }
  }
  return { flagged: false, reason: '' }
}
```

- [ ] **Step 2: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 3: Commit**

```
git add frontend/src/components/common/coinFilter.ts
git commit -m "feat(common): coinFilter utility — checkCoinFlagged helper"
```

---

## Task 7: Admin UI — CoinFilterSection

**Files:**
- Modify: `frontend/src/features/admin-defaults/AdminDefaultsTab.tsx`

- [ ] **Step 1: Add imports**

At the top of `AdminDefaultsTab.tsx`, add to the existing import from `./api`:
```typescript
import { updateStrategyDefaults, invalidateStrategyDefaultsCache, updateCoinFilter, getCoinFilter } from './api'
```
And add to the type imports:
```typescript
import type { GridDefaults, MatrixDefaults, GridStep, AllStrategyDefaults, CoinFilterSettings } from './types'
```

- [ ] **Step 2: Add `CoinFilterSection` component**

Add after the `MatrixSection` component and before the `AdminDefaultsTab` export:
```tsx
// ── Coin filter section ───────────────────────────────────────────────────────

function CoinFilterSection({
  initial,
  onSaved,
}: {
  initial: CoinFilterSettings
  onSaved: () => void
}) {
  const [d, setD]                     = useState<CoinFilterSettings>(initial)
  const [blacklistInput, setInput]    = useState('')
  const [saving, setSaving]           = useState(false)
  const [saved, setSaved]             = useState(false)
  const [error, setError]             = useState<string | null>(null)

  useEffect(() => { setD(initial) }, [initial])

  function addToBlacklist() {
    const sym = blacklistInput.trim().toUpperCase()
    if (!sym || d.blacklist.includes(sym)) { setInput(''); return }
    setD(p => ({ ...p, blacklist: [...p.blacklist, sym] }))
    setInput('')
  }

  function removeFromBlacklist(sym: string) {
    setD(p => ({ ...p, blacklist: p.blacklist.filter(s => s !== sym) }))
  }

  async function handleSave() {
    setSaving(true); setError(null)
    try {
      await updateCoinFilter(d)
      setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally { setSaving(false) }
  }

  return (
    <div className="rounded-lg border border-slate-700 bg-slate-900/50 p-5 lg:col-span-2">
      <h3 className="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-300">
        Фильтр монет
      </h3>

      <div className="space-y-4">
        <Field label="Мин. объём 24ч (USDT)">
          <NumInput
            value={d.min_turnover_usdt}
            onChange={v => setD(p => ({ ...p, min_turnover_usdt: v }))}
          />
        </Field>

        <div>
          <label className="mb-2 block text-sm text-slate-400">Чёрный список</label>
          <div className="flex gap-2 mb-2">
            <input
              type="text"
              value={blacklistInput}
              onChange={e => setInput(e.target.value.toUpperCase())}
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addToBlacklist() } }}
              placeholder="PEPEUSDT"
              className="flex-1 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
            />
            <button
              type="button"
              onClick={addToBlacklist}
              className="rounded bg-slate-700 px-3 py-1.5 text-sm text-slate-200 hover:bg-slate-600"
            >
              + Добавить
            </button>
          </div>
          <div className="flex flex-wrap gap-1.5 min-h-[1.75rem]">
            {d.blacklist.map(sym => (
              <span
                key={sym}
                className="inline-flex items-center gap-1 rounded border border-rose-500/30 bg-rose-500/10 px-2 py-0.5 text-xs font-mono text-rose-300"
              >
                {sym}
                <button
                  type="button"
                  onClick={() => removeFromBlacklist(sym)}
                  className="text-rose-400 hover:text-rose-200 leading-none"
                >
                  ✕
                </button>
              </span>
            ))}
            {d.blacklist.length === 0 && (
              <span className="text-xs text-slate-600 leading-[1.75rem]">Пусто</span>
            )}
          </div>
        </div>
      </div>

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

- [ ] **Step 3: Update `AdminDefaultsTab` to load coin filter and render section**

In `AdminDefaultsTab`, add state:
```typescript
  const [coinFilter, setCoinFilter] = useState<CoinFilterSettings | null>(null)
```

Update the `load` function body — after setting `defaults`, also load coin filter:
```typescript
  async function load() {
    setLoading(true)
    setError(null)
    try {
      const [defaultsRes, filterRes] = await Promise.all([
        apiClient.get<AllStrategyDefaults>('/admin/strategy-defaults'),
        apiClient.get<CoinFilterSettings>('/coin-filter'),
      ])
      setDefaults(defaultsRes.data ?? {})
      setCoinFilter(filterRes.data ?? { min_turnover_usdt: 500000, blacklist: [] })
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg)
    } finally {
      setLoading(false)
    }
  }
```

In the JSX, inside the `{defaults && (...)}` block, after the `</MatrixSection>` and before the closing `</div>` of the grid, add:
```tsx
          {coinFilter && (
            <CoinFilterSection initial={coinFilter} onSaved={handleSaved} />
          )}
```

- [ ] **Step 4: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 5: Commit**

```
git add frontend/src/features/admin-defaults/AdminDefaultsTab.tsx
git commit -m "feat(admin): CoinFilterSection in Дефолты tab"
```

---

## Task 8: Frontend CoinPicker — flag coins with ⚠️

**Files:**
- Modify: `frontend/src/components/common/CoinPicker.tsx`

The goal: when the dropdown opens, load coin filter settings. Mark flagged coins with ⚠️ in the list. Show ⚠️ on the trigger button when the selected coin is flagged.

- [ ] **Step 1: Add imports**

At the top of `CoinPicker.tsx`, add:
```typescript
import { getCoinFilter, checkCoinFlagged } from './coinFilter'
import type { CoinFilterSettings } from './coinFilter'
```

- [ ] **Step 2: Add `coinFilterSettings` state to the component**

In the `CoinPicker` component, after the `savedLists` state:
```typescript
  const [coinFilterSettings, setCoinFilterSettings] = useState<CoinFilterSettings | null>(null)
```

- [ ] **Step 3: Load coin filter on open**

In the `useEffect` that runs when `open` changes (around line 127 in the original), add a `getCoinFilter` call:
```typescript
  useEffect(() => {
    if (!open) return
    loadTickers().then(() => setRows([..._tickers]))
    setSavedLists(getSavedLists())
    getCoinFilter().then(setCoinFilterSettings).catch(() => {})
    setTimeout(() => inputRef.current?.focus(), 30)
  }, [open])
```

- [ ] **Step 4: Compute `flaggedMap` and `selectedFlagReason`**

Add these two computed values inside the component body, after the `filtered` / `allLists` lines:
```typescript
  // Map of symbol → reason string for flagged coins
  const flaggedMap = useMemo((): Map<string, string> => {
    if (!coinFilterSettings) return new Map()
    const map = new Map<string, string>()
    for (const r of rows) {
      const { flagged, reason } = checkCoinFlagged(r.symbol, r.turnover, coinFilterSettings)
      if (flagged) map.set(r.symbol, reason)
    }
    return map
  }, [rows, coinFilterSettings])

  // Reason if the currently selected value is flagged (empty string = not flagged)
  const selectedFlagReason = useMemo((): string => {
    if (!coinFilterSettings) return ''
    const row = rows.find(r => r.symbol === value)
    if (!row) return ''
    const { flagged, reason } = checkCoinFlagged(value, row.turnover, coinFilterSettings)
    return flagged ? reason : ''
  }, [rows, value, coinFilterSettings])
```

Note: `useMemo` is already imported. If not, add it to the React import at the top.

- [ ] **Step 5: Update the trigger button JSX to show ⚠️ when selected coin is flagged**

Find the trigger button JSX. After the `<span>` that shows the symbol name:
```tsx
        <span className={`font-mono font-semibold text-gray-900 dark:text-white flex-1 text-left ${isSm ? 'text-[11px]' : 'text-base'}`}>{value}</span>
```
Add immediately after it (still inside the button):
```tsx
        {selectedFlagReason && (
          <span title={selectedFlagReason} className="text-amber-400 text-xs leading-none">⚠️</span>
        )}
```

- [ ] **Step 6: Update coin rows in the dropdown to show ⚠️**

Find the coin list row inside `filtered.map(r => { ... })`. The inner content structure is:
```tsx
                      <div className="flex-1 min-w-0">
                        <div className="flex items-baseline gap-0.5">
                          <span className="font-semibold text-sm text-gray-900 dark:text-white">{base}</span>
                          <span className="text-[10px] text-gray-400">/USDT</span>
                        </div>
                        <div className="text-[10px] text-gray-400">{fmtVol(r.turnover)} USDT</div>
                      </div>
```
Change to:
```tsx
                      <div className="flex-1 min-w-0">
                        <div className="flex items-baseline gap-0.5">
                          <span className="font-semibold text-sm text-gray-900 dark:text-white">{base}</span>
                          <span className="text-[10px] text-gray-400">/USDT</span>
                          {flaggedMap.has(r.symbol) && (
                            <span title={flaggedMap.get(r.symbol)} className="text-amber-400 text-[10px] leading-none cursor-help">⚠️</span>
                          )}
                        </div>
                        <div className="text-[10px] text-gray-400">{fmtVol(r.turnover)} USDT</div>
                      </div>
```

- [ ] **Step 7: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors. Fix any `useMemo` import if missing.

- [ ] **Step 8: Commit**

```
git add frontend/src/components/common/CoinPicker.tsx
git commit -m "feat(CoinPicker): flag low-volume/blacklisted coins with ⚠️"
```

---

## Task 9: Frontend CoinMultiPicker — flag coins with ⚠️

**Files:**
- Modify: `frontend/src/components/common/CoinMultiPicker.tsx`

Same pattern as CoinPicker: load coin filter on open, show ⚠️ in dropdown rows AND on existing coin tags.

- [ ] **Step 1: Add imports**

At the top of `CoinMultiPicker.tsx`, add:
```typescript
import { getCoinFilter, checkCoinFlagged } from './coinFilter'
import type { CoinFilterSettings } from './coinFilter'
```

- [ ] **Step 2: Add `coinFilterSettings` state**

In the component body, near other state declarations:
```typescript
  const [coinFilterSettings, setCoinFilterSettings] = useState<CoinFilterSettings | null>(null)
```

- [ ] **Step 3: Load coin filter on open**

Find the `useEffect` that fires when `open` changes and calls `loadTickers`. Add `getCoinFilter` call:
```typescript
  useEffect(() => {
    if (!open) return
    loadTickers().then(() => setRows([..._tickers]))
    getCoinFilter().then(setCoinFilterSettings).catch(() => {})
    // ... rest of existing effect
  }, [open])
```

- [ ] **Step 4: Compute `flaggedMap`**

After the `dynamicPresets` useMemo, add:
```typescript
  const flaggedMap = useMemo((): Map<string, string> => {
    if (!coinFilterSettings) return new Map()
    const map = new Map<string, string>()
    for (const r of rows) {
      const { flagged, reason } = checkCoinFlagged(r.symbol, r.turnover, coinFilterSettings)
      if (flagged) map.set(r.symbol, reason)
    }
    return map
  }, [rows, coinFilterSettings])
```

- [ ] **Step 5: Show ⚠️ on coin tags (chips) for flagged coins in the selected list**

Find the JSX that renders selected coin tags (chips). It looks like:
```tsx
              <span key={val}
                className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ${isPattern(val) ? patternTagCls : coinTagCls}`}
                onClick={e => e.stopPropagation()}>
                {isPattern(val) ? <Asterisk size={9} className="opacity-70" /> : <CoinIcon symbol={val} className="w-3 h-3" />}
                {val.replace(/USDT$/i, '')}
```
After the symbol text, add:
```tsx
                {flaggedMap.has(val) && (
                  <span title={flaggedMap.get(val)} className="text-amber-400 text-[9px] cursor-help">⚠️</span>
                )}
```

- [ ] **Step 6: Show ⚠️ in dropdown coin rows**

Find the filtered coin rows rendered inside the multi-picker dropdown. Locate the symbol name span:
```tsx
                      <span className="flex-1 text-left">
                        <span className="text-[12px] font-semibold text-slate-200">{base}</span>
                        <span className="ml-1 text-[10px] text-slate-500">USDT</span>
                      </span>
```
Add ⚠️ after the USDT span:
```tsx
                      <span className="flex-1 text-left">
                        <span className="text-[12px] font-semibold text-slate-200">{base}</span>
                        <span className="ml-1 text-[10px] text-slate-500">USDT</span>
                        {flaggedMap.has(row.symbol) && (
                          <span title={flaggedMap.get(row.symbol)} className="ml-1 text-amber-400 text-[10px] cursor-help">⚠️</span>
                        )}
                      </span>
```

- [ ] **Step 7: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 8: Commit**

```
git add frontend/src/components/common/CoinMultiPicker.tsx
git commit -m "feat(CoinMultiPicker): flag low-volume/blacklisted coins with ⚠️"
```

---

## Task 10: Frontend BotForm — `ignoreCoinFilter` toggle + confirm dialog

**Files:**
- Modify: `frontend/src/features/bots/types.ts`
- Modify: `frontend/src/features/bots/components/BotForm.tsx`

### 10a: Add to types

- [ ] **Step 1: Add `ignoreCoinFilter` to `Bot` and `CreateBotInput`**

In `frontend/src/features/bots/types.ts`:

In `Bot` type, after `autoMode: boolean;`:
```typescript
  ignoreCoinFilter: boolean;
```

In `CreateBotInput` type, after `autoMode?: boolean;`:
```typescript
  ignoreCoinFilter?: boolean;
```

### 10b: Update BotForm

- [ ] **Step 2: Add import for coin filter**

At the top of `BotForm.tsx`, add to the existing imports:
```typescript
import { getCoinFilter, checkCoinFlagged } from '../../../components/common/coinFilter'
import type { CoinFilterSettings } from '../../../components/common/coinFilter'
```

- [ ] **Step 3: Add state**

In the component body, after the `sizeAsMain` state line:
```typescript
  const [ignoreCoinFilter, setIgnoreCoinFilter] = useState<boolean>(bot?.ignoreCoinFilter ?? false)
  const [coinFilterSettings, setCoinFilterSettings] = useState<CoinFilterSettings | null>(null)
  const [showCoinFilterConfirm, setShowCoinFilterConfirm] = useState(false)
```

- [ ] **Step 4: Load coin filter once on mount**

Add a new `useEffect` after the existing "Apply admin-configured defaults" effect:
```typescript
  // Load coin filter settings for the confirm dialog check
  useEffect(() => {
    getCoinFilter().then(setCoinFilterSettings).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
```

- [ ] **Step 5: Add coin filter check to `handleSubmit`**

The current `handleSubmit` starts with:
```typescript
  const handleSubmit = async () => {
    if (!name.trim()) return;
    setSubmitting(true);
```
Replace with:
```typescript
  const handleSubmit = async () => {
    if (!name.trim()) return;

    // For signal bots: if the symbol is flagged and the user hasn't opted out,
    // show a confirmation dialog instead of submitting immediately.
    const isSignalBot = (config.bot_kind ?? initialKind ?? 'signal') === 'signal'
    if (isSignalBot && !ignoreCoinFilter && coinFilterSettings) {
      const symbol = config.symbol ?? ''
      // Look up turnover from CoinMultiPicker / CoinPicker shared cache isn't
      // accessible here, so we flag based on blacklist only (turnover requires
      // the ticker cache). Pass turnover=Infinity so only blacklist check fires.
      const { flagged, reason } = checkCoinFlagged(symbol, Infinity, coinFilterSettings)
      if (flagged && !showCoinFilterConfirm) {
        setShowCoinFilterConfirm(true)
        return
      }
    }
    setShowCoinFilterConfirm(false)

    setSubmitting(true);
```

- [ ] **Step 6: Add confirm dialog JSX**

In the BotForm JSX, just before the final closing `</div>` of the modal (near the Cancel/Submit buttons at the bottom), add:
```tsx
      {/* Coin filter confirm dialog */}
      {showCoinFilterConfirm && (
        <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/60">
          <div className="w-80 rounded-xl border border-amber-500/30 bg-slate-900 p-5 shadow-2xl">
            <div className="mb-3 flex items-center gap-2 text-amber-400">
              <span className="text-xl">⚠️</span>
              <span className="text-sm font-semibold">Монета в чёрном списке</span>
            </div>
            <p className="mb-4 text-sm text-slate-300">
              Монета <span className="font-mono font-bold text-slate-100">{config.symbol}</span> помечена фильтром монет.
              Создать бота на этой монете?
            </p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => { setShowCoinFilterConfirm(false); void handleSubmit() }}
                className="flex-1 rounded bg-amber-600 px-3 py-2 text-sm font-medium text-white hover:bg-amber-500"
              >
                Создать всё равно
              </button>
              <button
                type="button"
                onClick={() => setShowCoinFilterConfirm(false)}
                className="flex-1 rounded border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-300 hover:bg-slate-700"
              >
                Отмена
              </button>
            </div>
          </div>
        </div>
      )}
```

- [ ] **Step 7: Add `ignoreCoinFilter` toggle in Basic tab (signal bots only)**

Find the Basic tab section in the BotForm JSX. Locate the `autoMode` toggle or any toggle near the end of the basic fields. After the autoMode toggle block, add:
```tsx
                {/* Ignore coin filter — only relevant for signal bots */}
                {(config.bot_kind ?? initialKind ?? 'signal') === 'signal' && (
                  <div className="flex items-center justify-between">
                    <span className="text-sm text-slate-400">Игнорировать фильтр монет</span>
                    <button
                      type="button"
                      onClick={() => setIgnoreCoinFilter(v => !v)}
                      className="text-slate-400 hover:text-slate-200"
                    >
                      {ignoreCoinFilter ? <ToggleRight size={20} className="text-amber-400" /> : <ToggleLeft size={20} />}
                    </button>
                  </div>
                )}
```

- [ ] **Step 8: Pass `ignoreCoinFilter` in `handleSubmit`'s `onSubmit` call**

In `handleSubmit`, find the `await onSubmit({ ... })` call. Add `ignoreCoinFilter`:
```typescript
      await onSubmit({
        name: name.trim(),
        // ... existing fields ...
        autoMode,
        ignoreCoinFilter,
      });
```

- [ ] **Step 9: TypeScript check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.

If there are errors related to `Bot.ignoreCoinFilter` being required but not present in existing `fetchBot` mappings on the backend — check: the backend sends `ignoreCoinFilter` via `botResp`. The `Bot` TS type maps 1:1 from the JSON. If the backend was not yet returning `ignoreCoinFilter` for existing bots, it defaults to `false` via the DB default. This is fine.

If the `Bot` type has `ignoreCoinFilter: boolean` (required), existing mocked bots in tests may break. To be safe, make it optional in types.ts:
```typescript
  ignoreCoinFilter?: boolean;
```
And in `BotForm.tsx` use `bot?.ignoreCoinFilter ?? false`.

- [ ] **Step 10: Commit**

```
git add frontend/src/features/bots/types.ts frontend/src/features/bots/components/BotForm.tsx
git commit -m "feat(BotForm): ignoreCoinFilter toggle + confirm dialog for blacklisted symbols"
```

---

## Final verification

- [ ] **Build Go binary**

```
go build ./services/api-gateway/...
```
Expected: no errors.

- [ ] **Build frontend**

```
cd frontend && npm run build
```
Expected: no errors.

- [ ] **Run Go integration tests**

```
go test -v -tags integration ./services/api-gateway/ 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Run frontend type check**

```
cd frontend && npx tsc --noEmit
```
Expected: no errors.
