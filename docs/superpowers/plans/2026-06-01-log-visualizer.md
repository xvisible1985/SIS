# Log Visualizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить вкладку «Визуализатор» в AdminPage — пошаговый проигрыш истории стратегии на свечном графике с навигацией по событиям.

**Architecture:** Пять Go-хендлеров в новом файле отдают данные (аккаунты, стратегии, события, уровни-ордера, свечи с Bybit). Фронтенд грузит всё за один раз, хранит в памяти и анимирует через `setInterval` по массиву свечей; при достижении временной метки события — пауза и отображение инфо.

**Tech Stack:** Go (api-gateway), React + TypeScript, lightweight-charts v4 (уже в проекте), Tailwind CSS, Vitest.

---

## Файловая структура

**Создать:**
- `services/api-gateway/admin_log_visualizer_handler.go` — все 5 Go-хендлеров + Bybit kline-fetch
- `services/api-gateway/admin_log_visualizer_handler_test.go` — интеграционный тест
- `frontend/src/features/log-visualizer/types.ts` — TypeScript-типы
- `frontend/src/features/log-visualizer/api.ts` — API-функции
- `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx` — боковая панель событий
- `frontend/src/features/log-visualizer/LogVisualizerControls.tsx` — панель управления (prev/play/next)
- `frontend/src/features/log-visualizer/LogVisualizerChart.tsx` — lightweight-charts обёртка
- `frontend/src/features/log-visualizer/LogVisualizerTab.tsx` — главный компонент + стейт-машина
- `frontend/src/__tests__/LogVisualizerTab.test.tsx` — unit-тест логики слияния событий

**Изменить:**
- `services/api-gateway/main.go` — добавить 5 маршрутов в admin-группу
- `frontend/src/pages/AdminPage.tsx` — добавить таб «Визуализатор»

---

## Task 1: Go-хендлеры и маршруты

**Files:**
- Create: `services/api-gateway/admin_log_visualizer_handler.go`
- Modify: `services/api-gateway/main.go`

- [ ] **Step 1: Написать тест хендлера accounts**

Создай `services/api-gateway/admin_log_visualizer_handler_test.go`:

```go
//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLVGetAccounts_ReturnsJSON(t *testing.T) {
	s := newTestServer(t)
	adminID := createAdminTestUser(t, s, "lv_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	// Insert an exchange account for a non-admin user
	ownerID := createAdminTestUser(t, s, "lv_owner@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerID)

	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO exchange_accounts (owner_id, exchange, label, api_key_enc, secret_enc)
		 VALUES ($1, 'bybit', 'test-acc', 'enc_key', 'enc_sec')`, ownerID)
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/log-visualizer/accounts", nil)
	req = withUserID(req, adminID)
	s.LVGetAccounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("expected at least one account")
	}
	first := resp[0]
	if first["id"] == nil || first["label"] == nil || first["ownerUsername"] == nil {
		t.Errorf("missing fields in response: %v", first)
	}
}
```

- [ ] **Step 2: Запустить тест — убедиться что FAIL**

```
cd services/api-gateway
go test -tags integration -run TestLVGetAccounts_ReturnsJSON .
```
Ожидаем: `FAIL` — `s.LVGetAccounts undefined`

- [ ] **Step 3: Создать файл хендлеров**

Создай `services/api-gateway/admin_log_visualizer_handler.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"sis/pkg/proxy"
)

// ── Response types ─────────────────────────────────────────────────────────

type lvAccount struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	OwnerUsername string `json:"ownerUsername"`
}

type lvStrategy struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Direction    string `json:"direction"`
	StrategyType string `json:"strategyType"`
	Status       string `json:"status"`
}

type lvEvent struct {
	Message string  `json:"message"`
	Level   string  `json:"level"`
	TsMs    float64 `json:"tsMs"`
}

type lvLevel struct {
	LevelIdx    int     `json:"levelIdx"`
	Side        string  `json:"side"`
	FilledPrice float64 `json:"filledPrice"`
	Qty         string  `json:"qty"`
	Status      string  `json:"status"`
	TsMs        float64 `json:"tsMs"`
}

type lvCandle struct {
	T int64   `json:"t"`
	O float64 `json:"o"`
	H float64 `json:"h"`
	L float64 `json:"l"`
	C float64 `json:"c"`
	V float64 `json:"v"`
}

// ── GET /admin/log-visualizer/accounts ─────────────────────────────────────

func (s *Server) LVGetAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT a.id, a.label, COALESCE(u.username, u.email) AS owner_username
		FROM exchange_accounts a
		JOIN users u ON u.id = a.owner_id
		ORDER BY u.username NULLS LAST, u.email, a.label
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvAccount
	for rows.Next() {
		var a lvAccount
		if err := rows.Scan(&a.ID, &a.Label, &a.OwnerUsername); err == nil {
			out = append(out, a)
		}
	}
	if out == nil {
		out = []lvAccount{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/strategies?account_id= ───────────────────────

func (s *Server) LVGetStrategies(w http.ResponseWriter, r *http.Request) {
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "account_id required")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, direction, strategy_type, status
		FROM strategies
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvStrategy
	for rows.Next() {
		var s lvStrategy
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Direction, &s.StrategyType, &s.Status); err == nil {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []lvStrategy{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/events?strategy_id=&from=&to= ───────────────

func (s *Server) LVGetEvents(w http.ResponseWriter, r *http.Request) {
	stratID, fromMs, toMs, err := lvParseParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT message, level,
		       EXTRACT(EPOCH FROM created_at) * 1000 AS ts_ms
		FROM strategy_events
		WHERE strategy_id = $1
		  AND created_at >= to_timestamp($2::bigint / 1000.0)
		  AND created_at <  to_timestamp($3::bigint / 1000.0)
		ORDER BY created_at ASC
	`, stratID, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvEvent
	for rows.Next() {
		var e lvEvent
		if err := rows.Scan(&e.Message, &e.Level, &e.TsMs); err == nil {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []lvEvent{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/levels?strategy_id=&from=&to= ───────────────

func (s *Server) LVGetLevels(w http.ResponseWriter, r *http.Request) {
	stratID, fromMs, toMs, err := lvParseParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT level_idx, side,
		       COALESCE(filled_price, 0),
		       qty, status,
		       EXTRACT(EPOCH FROM filled_at) * 1000 AS ts_ms
		FROM strategy_levels
		WHERE strategy_id = $1
		  AND filled_at IS NOT NULL
		  AND filled_at >= to_timestamp($2::bigint / 1000.0)
		  AND filled_at <  to_timestamp($3::bigint / 1000.0)
		ORDER BY filled_at ASC
	`, stratID, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var out []lvLevel
	for rows.Next() {
		var l lvLevel
		if err := rows.Scan(&l.LevelIdx, &l.Side, &l.FilledPrice, &l.Qty, &l.Status, &l.TsMs); err == nil {
			out = append(out, l)
		}
	}
	if out == nil {
		out = []lvLevel{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /admin/log-visualizer/klines?symbol=&interval=&from=&to= ──────────

func (s *Server) LVGetKlines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	symbol   := q.Get("symbol")
	interval := q.Get("interval") // "1m","5m","15m","30m","1h","4h","1D"
	fromStr  := q.Get("from")
	toStr    := q.Get("to")

	if symbol == "" || interval == "" || fromStr == "" || toStr == "" {
		writeError(w, http.StatusBadRequest, "symbol, interval, from, to required")
		return
	}
	fromMs, err1 := strconv.ParseInt(fromStr, 10, 64)
	toMs, err2   := strconv.ParseInt(toStr, 10, 64)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "from and to must be unix milliseconds")
		return
	}

	bybitIv := bybitChartTF(interval) // reuse from signal_chart_handler.go
	candles, err := lvFetchBybitKlines(r.Context(), symbol, bybitIv, fromMs, toMs)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch klines: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, candles)
}

// lvFetchBybitKlines fetches OHLCV candles from Bybit for the given ms range.
// Bybit returns max 1000 per request (newest-first), so we paginate backwards.
func lvFetchBybitKlines(ctx context.Context, symbol, interval string, fromMs, toMs int64) ([]lvCandle, error) {
	const bybitURL = "https://api.bybit.com/v5/market/kline"
	var all []lvCandle
	cursor := toMs

	for {
		url := fmt.Sprintf("%s?category=linear&symbol=%s&interval=%s&start=%d&end=%d&limit=1000",
			bybitURL, symbol, interval, fromMs, cursor)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := proxy.HTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			RetCode int `json:"retCode"`
			Result  struct {
				List [][]string `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse bybit response: %w", err)
		}
		if result.RetCode != 0 {
			return nil, fmt.Errorf("bybit retCode %d: %s", result.RetCode, string(body))
		}

		list := result.Result.List
		if len(list) == 0 {
			break
		}

		// Parse batch (list is newest-first → reverse to oldest-first)
		batch := make([]lvCandle, 0, len(list))
		for i := len(list) - 1; i >= 0; i-- {
			row := list[i]
			if len(row) < 6 {
				continue
			}
			ts, _  := strconv.ParseInt(row[0], 10, 64)
			batch = append(batch, lvCandle{
				T: ts,
				O: lvParseF(row[1]),
				H: lvParseF(row[2]),
				L: lvParseF(row[3]),
				C: lvParseF(row[4]),
				V: lvParseF(row[5]),
			})
		}

		// Prepend batch to all (batch is oldest-first, all grows forward in time)
		all = append(batch, all...)

		if len(list) < 1000 {
			break // fetched everything
		}
		// Oldest timestamp in this batch = list[len-1][0] (newest-first list)
		oldestTs, _ := strconv.ParseInt(list[len(list)-1][0], 10, 64)
		if oldestTs <= fromMs {
			break
		}
		cursor = oldestTs - 1 // move cursor back
	}

	// Deduplicate and sort ascending by T (safeguard)
	seen := map[int64]bool{}
	deduped := make([]lvCandle, 0, len(all))
	for _, c := range all {
		if !seen[c.T] {
			seen[c.T] = true
			deduped = append(deduped, c)
		}
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].T < deduped[j].T })

	return deduped, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func lvParseParams(r *http.Request) (stratID string, fromMs, toMs int64, err error) {
	q := r.URL.Query()
	stratID = q.Get("strategy_id")
	if stratID == "" {
		err = fmt.Errorf("strategy_id required")
		return
	}
	fromMs, err = strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil {
		err = fmt.Errorf("from must be unix milliseconds")
		return
	}
	toMs, err = strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil {
		err = fmt.Errorf("to must be unix milliseconds")
		return
	}
	return
}

func lvParseF(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// Ensure time import is used (for future handler extensions).
var _ = time.Now
```

- [ ] **Step 4: Добавить маршруты в main.go**

В файле `services/api-gateway/main.go` внутри блока `r.Group(func(r chi.Router) { r.Use(s.RequireAdmin) ... })` добавить после последнего bybit-news маршрута:

```go
// Admin: log visualizer
r.Get("/admin/log-visualizer/accounts",   s.LVGetAccounts)
r.Get("/admin/log-visualizer/strategies", s.LVGetStrategies)
r.Get("/admin/log-visualizer/events",     s.LVGetEvents)
r.Get("/admin/log-visualizer/levels",     s.LVGetLevels)
r.Get("/admin/log-visualizer/klines",     s.LVGetKlines)
```

- [ ] **Step 5: Собрать проект**

```
cd services/api-gateway
go build ./...
```
Ожидаем: 0 ошибок компиляции.

- [ ] **Step 6: Запустить тест — убедиться что PASS**

```
cd services/api-gateway
go test -tags integration -run TestLVGetAccounts_ReturnsJSON .
```
Ожидаем: `PASS`

- [ ] **Step 7: Коммит**

```bash
git add services/api-gateway/admin_log_visualizer_handler.go \
        services/api-gateway/admin_log_visualizer_handler_test.go \
        services/api-gateway/main.go
git commit -m "feat(api): add log visualizer admin endpoints"
```

---

## Task 2: TypeScript типы и API-клиент

**Files:**
- Create: `frontend/src/features/log-visualizer/types.ts`
- Create: `frontend/src/features/log-visualizer/api.ts`

- [ ] **Step 1: Создать types.ts**

```typescript
// frontend/src/features/log-visualizer/types.ts

export interface LVAccount {
  id: string
  label: string
  ownerUsername: string
}

export interface LVStrategy {
  id: string
  symbol: string
  direction: string   // 'long' | 'short' | 'both'
  strategyType: string // 'grid' | 'matrix'
  status: string
}

export interface LVEvent {
  message: string
  level: string       // 'info' | 'warn' | 'error'
  tsMs: number
}

export interface LVLevel {
  levelIdx: number
  side: 'Buy' | 'Sell'
  filledPrice: number
  qty: string
  status: string      // 'filled' | 'sl_closed'
  tsMs: number
}

export interface LVCandle {
  t: number  // unix ms
  o: number
  h: number
  l: number
  c: number
  v: number
}

/** Merged event from strategy_events or strategy_levels, sorted by time. */
export interface MergedEvent {
  tsMs:   number
  kind:   'log' | 'level'
  log?:   LVEvent
  level?: LVLevel
  label:  string   // display text, e.g. "L3 filled 0.4225" or "TP placed"
}

export const INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D'] as const
export type Interval = typeof INTERVALS[number]
```

- [ ] **Step 2: Создать api.ts**

```typescript
// frontend/src/features/log-visualizer/api.ts

import { apiClient } from '../../api/client'
import type { LVAccount, LVStrategy, LVEvent, LVLevel, LVCandle, Interval } from './types'

export async function lvGetAccounts(): Promise<LVAccount[]> {
  const res = await apiClient.get<LVAccount[]>('/admin/log-visualizer/accounts')
  return res.data
}

export async function lvGetStrategies(accountId: string): Promise<LVStrategy[]> {
  const res = await apiClient.get<LVStrategy[]>('/admin/log-visualizer/strategies', {
    params: { account_id: accountId },
  })
  return res.data
}

export async function lvGetEvents(
  strategyId: string, fromMs: number, toMs: number,
): Promise<LVEvent[]> {
  const res = await apiClient.get<LVEvent[]>('/admin/log-visualizer/events', {
    params: { strategy_id: strategyId, from: fromMs, to: toMs },
  })
  return res.data
}

export async function lvGetLevels(
  strategyId: string, fromMs: number, toMs: number,
): Promise<LVLevel[]> {
  const res = await apiClient.get<LVLevel[]>('/admin/log-visualizer/levels', {
    params: { strategy_id: strategyId, from: fromMs, to: toMs },
  })
  return res.data
}

export async function lvGetKlines(
  symbol: string, interval: Interval, fromMs: number, toMs: number,
): Promise<LVCandle[]> {
  const res = await apiClient.get<LVCandle[]>('/admin/log-visualizer/klines', {
    params: { symbol, interval, from: fromMs, to: toMs },
  })
  return res.data
}

/** Returns label for display in events list and info panel. */
export function makeMergedEventLabel(
  kind: 'log' | 'level',
  log?: import('./types').LVEvent,
  level?: import('./types').LVLevel,
): string {
  if (kind === 'level' && level) {
    const dir = level.side === 'Buy' ? '▲' : '▼'
    const status = level.status === 'sl_closed' ? ' [SL]' : ''
    return `${dir} L${level.levelIdx} ${level.filledPrice.toFixed(4)} · ${level.qty}${status}`
  }
  return log?.message ?? '—'
}
```

- [ ] **Step 3: Написать тест для makeMergedEventLabel**

Создай `frontend/src/__tests__/LogVisualizerTab.test.tsx`:

```typescript
import { describe, it, expect } from 'vitest'
import { makeMergedEventLabel } from '../features/log-visualizer/api'
import type { LVLevel, LVEvent } from '../features/log-visualizer/types'

describe('makeMergedEventLabel', () => {
  it('formats filled Buy level', () => {
    const level: LVLevel = { levelIdx: 3, side: 'Buy', filledPrice: 0.4225, qty: '47 USDT', status: 'filled', tsMs: 0 }
    expect(makeMergedEventLabel('level', undefined, level)).toBe('▲ L3 0.4225 · 47 USDT')
  })

  it('formats sl_closed Sell level', () => {
    const level: LVLevel = { levelIdx: 5, side: 'Sell', filledPrice: 1.2345, qty: '100 USDT', status: 'sl_closed', tsMs: 0 }
    expect(makeMergedEventLabel('level', undefined, level)).toBe('▼ L5 1.2345 · 100 USDT [SL]')
  })

  it('formats log event', () => {
    const log: LVEvent = { message: 'TP placed', level: 'info', tsMs: 0 }
    expect(makeMergedEventLabel('log', log, undefined)).toBe('TP placed')
  })
})
```

- [ ] **Step 4: Запустить тест — убедиться что PASS**

```
cd frontend
npx vitest run src/__tests__/LogVisualizerTab.test.tsx
```
Ожидаем: `3 passed`

- [ ] **Step 5: Коммит**

```bash
git add frontend/src/features/log-visualizer/types.ts \
        frontend/src/features/log-visualizer/api.ts \
        frontend/src/__tests__/LogVisualizerTab.test.tsx
git commit -m "feat(lv): types, api client, event label util"
```

---

## Task 3: LogVisualizerEventsList

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx`

- [ ] **Step 1: Создать компонент**

```typescript
// frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx

import { useEffect, useRef } from 'react'
import type { MergedEvent } from './types'

interface Props {
  events:       MergedEvent[]
  currentIndex: number           // -1 = до первого события
  onJump:       (idx: number) => void
}

export function LogVisualizerEventsList({ events, currentIndex, onJump }: Props) {
  const listRef    = useRef<HTMLDivElement>(null)
  const activeRef  = useRef<HTMLButtonElement>(null)

  // Автопрокрутка к текущему событию
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [currentIndex])

  function fmtTime(tsMs: number) {
    return new Date(tsMs).toLocaleTimeString('ru-RU', {
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  }

  return (
    <div className="flex flex-col h-full overflow-hidden border-l border-white/[.06]">
      {/* Header */}
      <div className="flex-shrink-0 px-3 py-2 border-b border-white/[.06]">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-slate-400">
          События
          {events.length > 0 && (
            <span className="ml-2 font-mono text-slate-500">{currentIndex + 1}/{events.length}</span>
          )}
        </span>
      </div>

      {/* List */}
      <div ref={listRef} className="flex-1 overflow-y-auto">
        {events.length === 0 ? (
          <div className="flex h-full items-center justify-center text-[11px] text-slate-600">
            Нет событий
          </div>
        ) : (
          events.map((ev, idx) => {
            const isCurrent = idx === currentIndex
            const isPast    = idx < currentIndex
            return (
              <button
                key={idx}
                ref={isCurrent ? activeRef : undefined}
                onClick={() => onJump(idx)}
                className={`w-full text-left px-3 py-1.5 border-b border-white/[.03] transition-colors hover:bg-white/[.03] ${
                  isCurrent
                    ? 'bg-amber-400/[.08] border-l-2 border-l-amber-400'
                    : isPast
                    ? 'opacity-60'
                    : 'opacity-40'
                }`}
              >
                <div className="flex items-start gap-1.5">
                  {/* Dot */}
                  <span className={`mt-[3px] shrink-0 h-1.5 w-1.5 rounded-full ${
                    isCurrent ? 'bg-amber-400'
                    : ev.kind === 'level'
                      ? ev.level?.side === 'Buy' ? 'bg-emerald-500' : 'bg-rose-500'
                      : ev.log?.level === 'error' ? 'bg-rose-500'
                        : ev.log?.level === 'warn' ? 'bg-amber-500' : 'bg-slate-500'
                  }`} />
                  <div className="min-w-0 flex-1">
                    <div className="text-[9px] text-slate-500 font-mono">{fmtTime(ev.tsMs)}</div>
                    <div className={`text-[10px] leading-tight truncate ${
                      isCurrent ? 'text-amber-200' : 'text-slate-400'
                    }`}>
                      {ev.label}
                    </div>
                  </div>
                </div>
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Коммит**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx
git commit -m "feat(lv): events sidebar component"
```

---

## Task 4: LogVisualizerControls

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerControls.tsx`

- [ ] **Step 1: Создать компонент**

```typescript
// frontend/src/features/log-visualizer/LogVisualizerControls.tsx

import type { MergedEvent } from './types'

interface Props {
  isPlaying:    boolean
  speed:        number       // 1–48
  isMax:        boolean      // true = MAX режим (без анимации)
  currentEvent: MergedEvent | null
  hasData:      boolean
  canGoPrev:    boolean
  canGoNext:    boolean
  onPlay:       () => void
  onPause:      () => void
  onPrev:       () => void
  onNext:       () => void
  onFirst:      () => void
  onLast:       () => void
  onSpeedChange: (v: number) => void
  onMaxChange:   (v: boolean) => void
}

export function LogVisualizerControls({
  isPlaying, speed, isMax, currentEvent, hasData,
  canGoPrev, canGoNext,
  onPlay, onPause, onPrev, onNext, onFirst, onLast,
  onSpeedChange, onMaxChange,
}: Props) {
  function fmtTime(tsMs: number) {
    return new Date(tsMs).toLocaleString('ru-RU', {
      day: 'numeric', month: 'numeric',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  }

  const btnBase = 'rounded px-3 py-1 text-xs font-medium transition-colors disabled:opacity-30 disabled:cursor-not-allowed'
  const btnSecondary = `${btnBase} bg-white/[.05] text-slate-300 hover:bg-white/[.09]`
  const btnPrimary   = `${btnBase} bg-[#5b8cff]/20 text-[#b8c8ff] hover:bg-[#5b8cff]/30`

  return (
    <div className="flex-shrink-0 border-t border-white/[.06] bg-[#0a0d14] px-4 py-2 flex flex-col gap-2">
      {/* Buttons row */}
      <div className="flex items-center gap-2">
        <button className={btnSecondary} disabled={!hasData || !canGoPrev} onClick={onFirst} title="В начало">⏮</button>
        <button className={btnSecondary} disabled={!hasData || !canGoPrev} onClick={onPrev} title="Предыдущее событие">◀ Пред.</button>

        {isPlaying ? (
          <button className={btnPrimary} onClick={onPause} title="Пауза">⏸ Пауза</button>
        ) : (
          <button className={btnPrimary} disabled={!hasData || !canGoNext} onClick={onPlay} title="Играть">▶ Играть</button>
        )}

        <button className={btnSecondary} disabled={!hasData || !canGoNext} onClick={onNext} title="Следующее событие">След. ▶</button>
        <button className={btnSecondary} disabled={!hasData || !canGoNext} onClick={onLast} title="В конец">⏭</button>

        {/* Speed */}
        <div className="ml-auto flex items-center gap-2">
          <label className="flex items-center gap-1.5 cursor-pointer select-none text-[11px] text-slate-400">
            <input
              type="checkbox"
              checked={isMax}
              onChange={e => onMaxChange(e.target.checked)}
              className="accent-amber-400"
            />
            MAX
          </label>
          {!isMax && (
            <>
              <span className="text-[10px] text-slate-500 font-mono">{speed}×</span>
              <input
                type="range" min={1} max={48} step={1}
                value={speed}
                onChange={e => onSpeedChange(Number(e.target.value))}
                className="w-24 h-1 accent-[#5b8cff] cursor-pointer"
              />
            </>
          )}
        </div>
      </div>

      {/* Info line */}
      <div className="min-h-[16px]">
        {currentEvent ? (
          <div className="flex items-center gap-2">
            <span className="text-[10px] font-mono text-slate-500">{fmtTime(currentEvent.tsMs)}</span>
            <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${
              currentEvent.kind === 'level'
                ? currentEvent.level?.side === 'Buy' ? 'bg-emerald-400' : 'bg-rose-400'
                : currentEvent.log?.level === 'error' ? 'bg-rose-400'
                  : currentEvent.log?.level === 'warn' ? 'bg-amber-400' : 'bg-slate-500'
            }`} />
            <span className="text-[11px] text-amber-200/90">{currentEvent.label}</span>
          </div>
        ) : (
          <span className="text-[10px] text-slate-600">
            {hasData ? 'Нажми ▶ Играть' : 'Загрузи данные'}
          </span>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Коммит**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerControls.tsx
git commit -m "feat(lv): playback controls component"
```

---

## Task 5: LogVisualizerChart

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerChart.tsx`

Изучи паттерн из `frontend/src/components/terminal/Chart.tsx` (строки 150–220) — там инициализация lightweight-charts.

- [ ] **Step 1: Создать компонент**

```typescript
// frontend/src/features/log-visualizer/LogVisualizerChart.tsx

import { useEffect, useRef } from 'react'
import {
  createChart, CandlestickSeries, createSeriesMarkers,
  ColorType, type IChartApi, type ISeriesApi,
} from 'lightweight-charts'
import type { LVCandle, MergedEvent } from './types'

interface Props {
  candles: LVCandle[]      // slice отображаемых свечей (растёт при анимации)
  events:  MergedEvent[]   // события до текущего момента (для маркеров)
}

export function LogVisualizerChart({ candles, events }: Props) {
  const containerRef   = useRef<HTMLDivElement>(null)
  const chartRef       = useRef<IChartApi | null>(null)
  const seriesRef      = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersRef     = useRef<any>(null)

  // Инициализация графика (один раз)
  useEffect(() => {
    if (!containerRef.current) return

    const chart = createChart(containerRef.current, {
      width:  containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
      layout: {
        background: { type: ColorType.Solid, color: '#0a0a0f' },
        textColor: '#9ca3af',
      },
      grid: {
        vertLines: { color: '#1a1a2e' },
        horzLines: { color: '#1a1a2e' },
      },
      rightPriceScale: { borderColor: '#2d2d3d' },
      timeScale: {
        borderColor: '#2d2d3d',
        timeVisible: true,
        tickMarkFormatter: (time: number, type: number) => {
          const d = new Date(time * 1000)
          if (type === 0) return String(d.getFullYear())
          if (type === 1) return d.toLocaleDateString('ru-RU', { month: 'short' })
          if (type === 2) return d.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })
          if (type === 3) return d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
          return d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
        },
      },
      localization: {
        locale: 'ru-RU',
        timeFormatter: (time: number) => {
          const d = new Date(time * 1000)
          return d.toLocaleString('ru-RU', { day: 'numeric', month: 'numeric', hour: '2-digit', minute: '2-digit' })
        },
      },
      crosshair: { mode: 1 },
    })
    chartRef.current = chart

    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#00DC82', downColor: '#ef4444',
      borderUpColor: '#00DC82', borderDownColor: '#ef4444',
      wickUpColor: '#00DC82', wickDownColor: '#ef4444',
      lastValueVisible: false,
      priceLineVisible: false,
    })
    seriesRef.current = series
    markersRef.current = createSeriesMarkers(series)

    // Resize observer
    const ro = new ResizeObserver(() => {
      if (containerRef.current) {
        chart.applyOptions({
          width:  containerRef.current.clientWidth,
          height: containerRef.current.clientHeight,
        })
      }
    })
    ro.observe(containerRef.current)

    return () => {
      ro.disconnect()
      chart.remove()
      chartRef.current  = null
      seriesRef.current = null
      markersRef.current = null
    }
  }, [])

  // Обновить данные свечей при изменении среза
  useEffect(() => {
    if (!seriesRef.current || candles.length === 0) return
    const data = candles.map(c => ({
      time:  Math.floor(c.t / 1000) as import('lightweight-charts').Time,
      open:  c.o, high: c.h, low: c.l, close: c.c,
    }))
    seriesRef.current.setData(data)
    // Автопрокрутка к последней свече
    chartRef.current?.timeScale().scrollToRealTime()
  }, [candles])

  // Обновить маркеры событий при изменении списка
  useEffect(() => {
    if (!markersRef.current) return
    const markers = events.map(ev => ({
      time: Math.floor(ev.tsMs / 1000) as import('lightweight-charts').Time,
      position: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? 'belowBar' : 'aboveBar')
        : 'inBar' as 'aboveBar' | 'belowBar' | 'inBar',
      color: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? '#34d399' : '#f87171')
        : ev.log?.level === 'error' ? '#f87171'
          : ev.log?.level === 'warn' ? '#fbbf24' : '#94a3b8',
      shape: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? 'arrowUp' : 'arrowDown')
        : 'circle' as 'arrowUp' | 'arrowDown' | 'circle',
      text: ev.kind === 'level' ? `L${ev.level?.levelIdx}` : undefined,
      size: 1,
    }))
    markersRef.current.setMarkers(markers)
  }, [events])

  return (
    <div ref={containerRef} className="w-full h-full" />
  )
}
```

- [ ] **Step 2: Коммит**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerChart.tsx
git commit -m "feat(lv): chart component with candle animation and event markers"
```

---

## Task 6: LogVisualizerTab — главный компонент

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerTab.tsx`

- [ ] **Step 1: Создать компонент**

```typescript
// frontend/src/features/log-visualizer/LogVisualizerTab.tsx

import { useState, useEffect, useRef, useCallback } from 'react'
import { LogVisualizerChart }      from './LogVisualizerChart'
import { LogVisualizerEventsList } from './LogVisualizerEventsList'
import { LogVisualizerControls }   from './LogVisualizerControls'
import { lvGetAccounts, lvGetStrategies, lvGetEvents, lvGetLevels, lvGetKlines, makeMergedEventLabel } from './api'
import type { LVAccount, LVStrategy, LVCandle, MergedEvent, Interval } from './types'
import { INTERVALS } from './types'

// Скорость анимации: candles per second = speed * CANDLES_PER_SEC_BASE
const CANDLES_PER_SEC_BASE = 20

export function LogVisualizerTab() {
  // ── Picker state ──────────────────────────────────────────────────────
  const [accounts,   setAccounts]   = useState<LVAccount[]>([])
  const [strategies, setStrategies] = useState<LVStrategy[]>([])
  const [accountId,  setAccountId]  = useState('')
  const [strategyId, setStrategyId] = useState('')
  const [fromDate,   setFromDate]   = useState(() => {
    const d = new Date(); d.setDate(d.getDate() - 7); return d.toISOString().slice(0, 10)
  })
  const [toDate,     setToDate]     = useState(() => new Date().toISOString().slice(0, 10))
  const [interval,   setInterval]   = useState<Interval>('1m')
  const [speed,      setSpeed]      = useState(8)
  const [isMax,      setIsMax]      = useState(false)

  // ── Loaded data ───────────────────────────────────────────────────────
  const [candles,   setCandles]   = useState<LVCandle[]>([])
  const [events,    setEvents]    = useState<MergedEvent[]>([])
  const [loading,   setLoading]   = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  // ── Animation state ───────────────────────────────────────────────────
  const [candleIdx,  setCandleIdx]  = useState(-1)  // index of last visible candle
  const [eventIdx,   setEventIdx]   = useState(-1)  // index of current event (-1 = before first)
  const [isPlaying,  setIsPlaying]  = useState(false)

  // Stable refs for animation loop
  const candleIdxRef = useRef(candleIdx)
  const eventIdxRef  = useRef(eventIdx)
  const candlesRef   = useRef(candles)
  const eventsRef    = useRef(events)
  useEffect(() => { candleIdxRef.current = candleIdx }, [candleIdx])
  useEffect(() => { eventIdxRef.current  = eventIdx  }, [eventIdx])
  useEffect(() => { candlesRef.current   = candles   }, [candles])
  useEffect(() => { eventsRef.current    = events    }, [events])

  // ── Load accounts on mount ────────────────────────────────────────────
  useEffect(() => {
    lvGetAccounts()
      .then(setAccounts)
      .catch(e => console.error('LV accounts:', e))
  }, [])

  // ── Load strategies when account changes ──────────────────────────────
  useEffect(() => {
    if (!accountId) { setStrategies([]); setStrategyId(''); return }
    lvGetStrategies(accountId)
      .then(list => { setStrategies(list); setStrategyId(list[0]?.id ?? '') })
      .catch(e => console.error('LV strategies:', e))
  }, [accountId])

  // ── Load data ─────────────────────────────────────────────────────────
  const handleLoad = useCallback(async () => {
    if (!strategyId || !fromDate || !toDate) return
    setLoading(true)
    setLoadError(null)
    setCandles([]); setEvents([])
    setCandleIdx(-1); setEventIdx(-1); setIsPlaying(false)

    try {
      const fromMs = new Date(fromDate).getTime()
      const toMs   = new Date(toDate).getTime() + 86_400_000 // включить конец дня

      const strat = strategies.find(s => s.id === strategyId)
      const symbol = strat?.symbol ?? ''

      const [eventsRaw, levelsRaw, candlesRaw] = await Promise.all([
        lvGetEvents(strategyId, fromMs, toMs),
        lvGetLevels(strategyId, fromMs, toMs),
        lvGetKlines(symbol, interval, fromMs, toMs),
      ])

      // Слияние и сортировка событий по времени
      const merged: MergedEvent[] = [
        ...eventsRaw.map(e => ({
          tsMs:  e.tsMs,
          kind:  'log' as const,
          log:   e,
          label: makeMergedEventLabel('log', e, undefined),
        })),
        ...levelsRaw.map(l => ({
          tsMs:   l.tsMs,
          kind:   'level' as const,
          level:  l,
          label:  makeMergedEventLabel('level', undefined, l),
        })),
      ].sort((a, b) => a.tsMs - b.tsMs)

      setCandles(candlesRaw)
      setEvents(merged)
      setCandleIdx(candlesRaw.length > 0 ? 0 : -1)
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [strategyId, fromDate, toDate, interval, strategies])

  // ── Animation tick ────────────────────────────────────────────────────
  useEffect(() => {
    if (!isPlaying) return

    if (isMax) {
      // MAX: прыжок к следующему событию без анимации
      const nextEvIdx = eventIdxRef.current + 1
      if (nextEvIdx >= eventsRef.current.length) {
        // Нет следующего события — анимируем до конца
        setCandleIdx(candlesRef.current.length - 1)
        setIsPlaying(false)
        return
      }
      const targetTs = eventsRef.current[nextEvIdx].tsMs
      const ci = candlesRef.current.findIndex(c => c.t >= targetTs)
      setCandleIdx(ci >= 0 ? ci : candlesRef.current.length - 1)
      setEventIdx(nextEvIdx)
      setIsPlaying(false)
      return
    }

    const intervalMs = Math.max(16, Math.round(1000 / (speed * CANDLES_PER_SEC_BASE)))
    const id = setInterval(() => {
      const ci    = candleIdxRef.current
      const ei    = eventIdxRef.current
      const cs    = candlesRef.current
      const evs   = eventsRef.current
      const nextCi = ci + 1

      if (nextCi >= cs.length) {
        setIsPlaying(false)
        return
      }

      const nextCandle    = cs[nextCi]
      const nextEvIdx     = ei + 1
      const nextEvent     = evs[nextEvIdx]

      if (nextEvent && nextCandle.t >= nextEvent.tsMs) {
        // Достигли события
        setCandleIdx(nextCi)
        setEventIdx(nextEvIdx)
        setIsPlaying(false)
      } else {
        setCandleIdx(nextCi)
      }
    }, intervalMs)

    return () => clearInterval(id)
  }, [isPlaying, speed, isMax])

  // ── Navigation helpers ────────────────────────────────────────────────
  const jumpToEvent = useCallback((idx: number) => {
    setIsPlaying(false)
    if (idx < 0 || idx >= events.length) return
    const targetTs = events[idx].tsMs
    const ci = candles.findIndex(c => c.t >= targetTs)
    setCandleIdx(ci >= 0 ? ci : candles.length - 1)
    setEventIdx(idx)
  }, [candles, events])

  const handlePrev  = useCallback(() => jumpToEvent(eventIdx - 1),     [jumpToEvent, eventIdx])
  const handleNext  = useCallback(() => jumpToEvent(eventIdx + 1),     [jumpToEvent, eventIdx])
  const handleFirst = useCallback(() => jumpToEvent(0),                [jumpToEvent])
  const handleLast  = useCallback(() => jumpToEvent(events.length - 1),[jumpToEvent, events.length])

  // ── Derived display data ──────────────────────────────────────────────
  const visibleCandles = candleIdx >= 0 ? candles.slice(0, candleIdx + 1) : []
  const visibleEvents  = eventIdx  >= 0 ? events.slice(0, eventIdx + 1)  : []
  const currentEvent   = eventIdx >= 0 ? events[eventIdx] : null
  const hasData        = candles.length > 0

  // ── Strategy display label ────────────────────────────────────────────
  function stratLabel(s: LVStrategy) {
    return `${s.symbol} · ${s.direction} · ${s.strategyType}`
  }

  // ── Render ────────────────────────────────────────────────────────────
  return (
    <div className="flex h-full flex-col overflow-hidden bg-[#0a0d14] text-slate-200">

      {/* Toolbar */}
      <div className="flex-shrink-0 flex flex-wrap items-center gap-2 px-4 py-2 border-b border-white/[.06]">
        {/* Account */}
        <select
          value={accountId}
          onChange={e => setAccountId(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        >
          <option value="">— Аккаунт —</option>
          {accounts.map(a => (
            <option key={a.id} value={a.id}>{a.ownerUsername} / {a.label}</option>
          ))}
        </select>

        {/* Strategy */}
        <select
          value={strategyId}
          onChange={e => setStrategyId(e.target.value)}
          disabled={strategies.length === 0}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none disabled:opacity-40"
        >
          <option value="">— Стратегия —</option>
          {strategies.map(s => (
            <option key={s.id} value={s.id}>{stratLabel(s)}</option>
          ))}
        </select>

        {/* Date range */}
        <input
          type="date" value={fromDate} onChange={e => setFromDate(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        />
        <span className="text-slate-600 text-xs">→</span>
        <input
          type="date" value={toDate} onChange={e => setToDate(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        />

        {/* Interval */}
        <select
          value={interval}
          onChange={e => setInterval(e.target.value as Interval)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        >
          {INTERVALS.map(iv => <option key={iv} value={iv}>{iv}</option>)}
        </select>

        {/* Load button */}
        <button
          onClick={handleLoad}
          disabled={!strategyId || loading}
          className="ml-auto rounded px-3 py-1 text-xs font-semibold bg-[#5b8cff]/20 text-[#b8c8ff] hover:bg-[#5b8cff]/30 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          {loading ? 'Загрузка…' : '▶ Загрузить'}
        </button>
      </div>

      {/* Error */}
      {loadError && (
        <div className="flex-shrink-0 mx-4 mt-2 rounded border border-rose-400/20 bg-rose-400/[.06] px-3 py-2 text-xs text-rose-300">
          {loadError}
        </div>
      )}

      {/* Warning for large ranges */}
      {!loading && candles.length > 30_000 && (
        <div className="flex-shrink-0 mx-4 mt-1 text-[10px] text-amber-400/70">
          ⚠ Загружено {candles.length.toLocaleString('ru-RU')} свечей — большой диапазон, анимация может быть медленной
        </div>
      )}

      {/* Main area */}
      <div className="flex-1 flex overflow-hidden min-h-0">
        {/* Chart */}
        <div className="flex-1 min-w-0">
          <LogVisualizerChart candles={visibleCandles} events={visibleEvents} />
        </div>

        {/* Events sidebar */}
        <div className="w-[220px] flex-shrink-0">
          <LogVisualizerEventsList
            events={events}
            currentIndex={eventIdx}
            onJump={jumpToEvent}
          />
        </div>
      </div>

      {/* Controls */}
      <LogVisualizerControls
        isPlaying={isPlaying}
        speed={speed}
        isMax={isMax}
        currentEvent={currentEvent}
        hasData={hasData}
        canGoPrev={eventIdx > 0}
        canGoNext={eventIdx < events.length - 1}
        onPlay={() => setIsPlaying(true)}
        onPause={() => setIsPlaying(false)}
        onPrev={handlePrev}
        onNext={handleNext}
        onFirst={handleFirst}
        onLast={handleLast}
        onSpeedChange={setSpeed}
        onMaxChange={setIsMax}
      />
    </div>
  )
}
```

- [ ] **Step 2: Запустить тесты**

```
cd frontend
npx vitest run
```
Ожидаем: все существующие тесты проходят, новый `LogVisualizerTab.test.tsx` тоже PASS.

- [ ] **Step 3: Коммит**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerTab.tsx
git commit -m "feat(lv): main tab component with animation state machine"
```

---

## Task 7: Интеграция в AdminPage

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

Открой `frontend/src/pages/AdminPage.tsx`. Найди константу `TABS` (строка ~1176):

```typescript
const TABS = [
  { id: 'users',      label: 'Пользователи' },
  { id: 'bots',       label: 'Боты'         },
  { id: 'monitoring', label: 'Мониторинг'   },
  { id: 'signals',    label: 'Сигналы'      },
  { id: 'proxies',    label: 'Прокси'       },
  { id: 'bybit-news', label: 'Bybit News'   },
] as const
```

- [ ] **Step 1: Добавить импорт**

В начало файла после существующих импортов добавить:

```typescript
import { LogVisualizerTab } from '../features/log-visualizer/LogVisualizerTab'
```

- [ ] **Step 2: Добавить таб в TABS**

```typescript
const TABS = [
  { id: 'users',          label: 'Пользователи' },
  { id: 'bots',           label: 'Боты'         },
  { id: 'monitoring',     label: 'Мониторинг'   },
  { id: 'signals',        label: 'Сигналы'      },
  { id: 'proxies',        label: 'Прокси'       },
  { id: 'bybit-news',     label: 'Bybit News'   },
  { id: 'log-visualizer', label: 'Визуализатор' },
] as const
```

- [ ] **Step 3: Добавить рендер таба**

В JSX-блоке `AdminPage`, после блока `bybit-news`:

```tsx
{tab === 'log-visualizer' && (
  <div className="flex flex-1 flex-col overflow-hidden">
    <LogVisualizerTab />
  </div>
)}
```

- [ ] **Step 4: Проверить сборку**

```
cd frontend
npm run build
```
Ожидаем: Build succeeded, 0 TypeScript-ошибок.

- [ ] **Step 5: Запустить dev и проверить вручную**

```
cd frontend
npm run dev
```

Открыть `http://localhost:5173/admin` → кликнуть таб «Визуализатор» → убедиться что страница рендерится без ошибок в консоли.

- [ ] **Step 6: Коммит**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(lv): add Визуализатор tab to admin page"
```

---

## Task 8: Финальная проверка

- [ ] **Step 1: Запустить все фронтенд-тесты**

```
cd frontend
npx vitest run
```
Ожидаем: все тесты PASS.

- [ ] **Step 2: Собрать Go**

```
cd services/api-gateway
go build ./...
```
Ожидаем: 0 ошибок.

- [ ] **Step 3: Проверить сценарий вручную**

1. Открыть `/admin` → таб «Визуализатор»
2. Выбрать аккаунт → стратегию (например, GENIUS short)
3. Установить диапазон: последние 3 дня, интервал `1m`
4. Нажать «Загрузить» → ждём спиннер → данные появились
5. Нажать «▶ Играть» при скорости 8× → свечи анимируются
6. При достижении события — пауза, инфо-строка показывает детали
7. Нажать «◀ Пред.» — граф откатывается к предыдущему событию
8. Кликнуть на событие в правой панели — граф прыгает туда
9. Поставить галочку MAX → нажать «▶ Играть» → мгновенный прыжок к следующему событию
10. Нажать «⏮ Начало» → граф возвращается к первой свече

- [ ] **Step 4: Финальный коммит**

```bash
git add .
git commit -m "feat: log visualizer complete"
```
