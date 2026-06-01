# Log Visualizer — Слои графика + Мини-карточка стратегии: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить попап управления слоями графика (тогглы + фильтр лога) и анимированную мини-карточку стратегии (filled/volume/lastPnl) в Log Visualizer.

**Architecture:** Новое состояние `layerSettings` в `LogVisualizerTab` передаётся в `LogVisualizerChart` (фильтрация маркеров + ценовые линии) и в новый компонент `LogVisualizerLayersPopup` (UI попапа). Параллельно `LogVisualizerStrategyCard` получает `visibleEvents` и читает статистику из них.

**Tech Stack:** React 18, TypeScript, lightweight-charts v4, Tailwind CSS, Go (pgx), Vitest

---

## File Map

| Статус   | Файл | Что делает |
|----------|------|------------|
| ИЗМЕНИТЬ | `services/api-gateway/admin_log_visualizer_handler.go` | `lvStrategy` + `lvLevel` расширяются; запросы обновляются |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/types.ts` | `LayerSettings`, `DEFAULT_LAYER_SETTINGS`, расширение `LVStrategy` и `LVLevel` |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/utils.ts` | `filterEvents`, `computeCardStats`, `formatPnl` |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/LogVisualizerChart.tsx` | prop `layerSettings`, фильтрация маркеров, ценовые линии |
| НОВЫЙ    | `frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx` | Кнопка + попап управления слоями |
| НОВЫЙ    | `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx` | Мини-карточка стратегии (absolute overlay) |
| ИЗМЕНИТЬ | `frontend/src/features/log-visualizer/LogVisualizerTab.tsx` | `layerSettings` state, новые компоненты, `relative` на chart-div |
| ИЗМЕНИТЬ | `frontend/src/__tests__/LogVisualizerTab.test.tsx` | Тесты для `filterEvents`, `computeCardStats`, `formatPnl` |

---

## Task 1: Backend — расширить lvStrategy и lvLevel

**Files:**
- Modify: `services/api-gateway/admin_log_visualizer_handler.go:25-46`

Два изменения: (1) `lvStrategy` получает `GridLevels` и `LastPnl`; (2) `lvLevel` получает `SizeUsdt`; (3) обновляются запросы и Scan-вызовы.

- [ ] **Step 1: Обновить structs**

В файле `services/api-gateway/admin_log_visualizer_handler.go` заменить:

```go
type lvStrategy struct {
	ID           string `json:"id"`
	Symbol       string `json:"symbol"`
	Direction    string `json:"direction"`
	StrategyType string `json:"strategyType"`
	Status       string `json:"status"`
}
```

на:

```go
type lvStrategy struct {
	ID           string   `json:"id"`
	Symbol       string   `json:"symbol"`
	Direction    string   `json:"direction"`
	StrategyType string   `json:"strategyType"`
	Status       string   `json:"status"`
	GridLevels   int      `json:"gridLevels"`
	LastPnl      *float64 `json:"lastPnl"`
}
```

И заменить:

```go
type lvLevel struct {
	LevelIdx    int     `json:"levelIdx"`
	Side        string  `json:"side"`
	FilledPrice float64 `json:"filledPrice"`
	Qty         string  `json:"qty"`
	Status      string  `json:"status"`
	TsMs        float64 `json:"tsMs"`
}
```

на:

```go
type lvLevel struct {
	LevelIdx    int     `json:"levelIdx"`
	Side        string  `json:"side"`
	FilledPrice float64 `json:"filledPrice"`
	Qty         string  `json:"qty"`
	SizeUsdt    float64 `json:"sizeUsdt"`
	Status      string  `json:"status"`
	TsMs        float64 `json:"tsMs"`
}
```

- [ ] **Step 2: Обновить запрос LVGetStrategies**

Найти в `LVGetStrategies` (≈строка 99) и заменить:

```go
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, direction, strategy_type, status
		FROM strategies
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
```

на:

```go
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, symbol, direction, strategy_type, status, grid_levels,
		  (SELECT realized_pnl FROM strategy_cycles
		   WHERE strategy_id = s.id AND ended_at IS NOT NULL
		   ORDER BY cycle_num DESC LIMIT 1) AS last_pnl
		FROM strategies s
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
```

- [ ] **Step 3: Обновить Scan в LVGetStrategies**

Найти (≈строка 114):
```go
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Direction, &s.StrategyType, &s.Status); err != nil {
```

Заменить на:
```go
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Direction, &s.StrategyType, &s.Status, &s.GridLevels, &s.LastPnl); err != nil {
```

- [ ] **Step 4: Обновить запрос LVGetLevels**

Найти в `LVGetLevels` (≈строка 182) и заменить:

```go
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
```

на:

```go
	rows, err := s.pool.Query(r.Context(), `
		SELECT level_idx, side,
		       COALESCE(filled_price, 0),
		       qty, COALESCE(size_usdt, 0), status,
		       EXTRACT(EPOCH FROM filled_at) * 1000 AS ts_ms
		FROM strategy_levels
		WHERE strategy_id = $1
		  AND filled_at IS NOT NULL
		  AND filled_at >= to_timestamp($2::bigint / 1000.0)
		  AND filled_at <  to_timestamp($3::bigint / 1000.0)
		ORDER BY filled_at ASC
	`, stratID, fromMs, toMs)
```

- [ ] **Step 5: Обновить Scan в LVGetLevels**

Найти (≈строка 203):
```go
		if err := rows.Scan(&l.LevelIdx, &l.Side, &l.FilledPrice, &l.Qty, &l.Status, &l.TsMs); err != nil {
```

Заменить на:
```go
		if err := rows.Scan(&l.LevelIdx, &l.Side, &l.FilledPrice, &l.Qty, &l.SizeUsdt, &l.Status, &l.TsMs); err != nil {
```

- [ ] **Step 6: Проверить компиляцию Go**

```
cd C:\Users\123\Projects\sis\services\api-gateway
go build ./...
```

Ожидаемый результат: никаких ошибок, тишина.

- [ ] **Step 7: Commit**

```
git add services/api-gateway/admin_log_visualizer_handler.go
git commit -m "feat(lv): extend lvStrategy (gridLevels, lastPnl) and lvLevel (sizeUsdt)"
```

---

## Task 2: Frontend — обновить types.ts

**Files:**
- Modify: `frontend/src/features/log-visualizer/types.ts`

- [ ] **Step 1: Расширить LVStrategy и LVLevel**

Полностью заменить содержимое `frontend/src/features/log-visualizer/types.ts`:

```typescript
// frontend/src/features/log-visualizer/types.ts

export interface LVAccount {
  id: string
  label: string
  ownerUsername: string
}

export interface LVStrategy {
  id:           string
  symbol:       string
  direction:    string   // 'long' | 'short' | 'both'
  strategyType: string   // 'grid' | 'matrix'
  status:       string
  gridLevels:   number
  lastPnl:      number | null
}

export interface LVEvent {
  message: string
  level: 'info' | 'warn' | 'error'
  tsMs: number
}

export interface LVLevel {
  levelIdx:    number
  side:        'Buy' | 'Sell'
  filledPrice: number
  qty:         string    // base asset amount, e.g. "0.001234"
  sizeUsdt:    number    // position size in USDT
  status:      'filled' | 'sl_closed'
  tsMs:        number
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
  label:  string   // display text, e.g. "▲ L3 0.4225 · 0.001234" or "TP placed"
}

export interface LayerSettings {
  showOrderMarkers: boolean   // arrowUp/arrowDown markers for level events
  showLogMarkers:   boolean   // circle markers for log events
  showPriceLines:   boolean   // horizontal dashed price lines at each filled level
  showInfo:         boolean   // show log events with level 'info'
  showWarn:         boolean   // show log events with level 'warn'
  showError:        boolean   // show log events with level 'error'
}

export const DEFAULT_LAYER_SETTINGS: LayerSettings = {
  showOrderMarkers: true,
  showLogMarkers:   true,
  showPriceLines:   false,
  showInfo:         true,
  showWarn:         true,
  showError:        true,
}

export const INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D'] as const
export type Interval = typeof INTERVALS[number]
```

- [ ] **Step 2: Обновить существующие LVLevel fixtures в тестах**

В файле `frontend/src/__tests__/LogVisualizerTab.test.tsx` добавить поле `sizeUsdt: 0` в оба существующих объекта `LVLevel` (строки ≈7 и ≈12). `LVLevel` теперь требует это поле.

```typescript
// строка ≈7 — было:
const level: LVLevel = { levelIdx: 3, side: 'Buy', filledPrice: 0.4225, qty: '47 USDT', status: 'filled', tsMs: 0 }
// стало:
const level: LVLevel = { levelIdx: 3, side: 'Buy', filledPrice: 0.4225, qty: '47 USDT', sizeUsdt: 0, status: 'filled', tsMs: 0 }

// строка ≈12 — было:
const level: LVLevel = { levelIdx: 5, side: 'Sell', filledPrice: 1.2345, qty: '100 USDT', status: 'sl_closed', tsMs: 0 }
// стало:
const level: LVLevel = { levelIdx: 5, side: 'Sell', filledPrice: 1.2345, qty: '100 USDT', sizeUsdt: 0, status: 'sl_closed', tsMs: 0 }
```

- [ ] **Step 3: Проверить что TypeScript не ругается**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Ожидаемый результат: 0 ошибок (или ошибки только в несвязанных файлах).

- [ ] **Step 4: Commit**

```
git add frontend/src/features/log-visualizer/types.ts frontend/src/__tests__/LogVisualizerTab.test.tsx
git commit -m "feat(lv): add LayerSettings type, extend LVStrategy + LVLevel (add sizeUsdt)"
```

---

## Task 3: Utility functions + тесты (TDD)

**Files:**
- Modify: `frontend/src/features/log-visualizer/utils.ts`
- Modify: `frontend/src/__tests__/LogVisualizerTab.test.tsx`

Три чистых функции для тестирования бизнес-логики без рендеринга компонентов.

- [ ] **Step 1: Написать падающие тесты**

В `frontend/src/__tests__/LogVisualizerTab.test.tsx`:

**1а.** Добавить импорты сразу после существующих строк `import` (первые строки файла):

```typescript
import { filterEvents, computeCardStats, formatPnl } from '../features/log-visualizer/utils'
import type { MergedEvent, LayerSettings } from '../features/log-visualizer/types'
import { DEFAULT_LAYER_SETTINGS } from '../features/log-visualizer/types'
```

**1б.** В конец файла добавить тест-хелперы и test suites:

```typescript
// ── Test helpers ──────────────────────────────────────────────────────────────

function makeLevelEvent(side: 'Buy' | 'Sell' = 'Buy', sizeUsdt = 100): MergedEvent {
  return {
    tsMs: 0, kind: 'level',
    level: { levelIdx: 1, side, filledPrice: 50000, qty: '0.002', sizeUsdt, status: 'filled', tsMs: 0 },
    label: '',
  }
}

function makeLogEvent(level: 'info' | 'warn' | 'error' = 'info'): MergedEvent {
  return {
    tsMs: 0, kind: 'log',
    log: { message: 'test', level, tsMs: 0 },
    label: '',
  }
}

// ── filterEvents ──────────────────────────────────────────────────────────────

describe('filterEvents', () => {
  const all: MergedEvent[] = [
    makeLevelEvent('Buy'),
    makeLogEvent('info'),
    makeLogEvent('warn'),
    makeLogEvent('error'),
  ]

  it('default settings keeps all 4 events', () => {
    expect(filterEvents(all, DEFAULT_LAYER_SETTINGS)).toHaveLength(4)
  })

  it('showOrderMarkers=false removes the level event', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showOrderMarkers: false })
    expect(result).toHaveLength(3)
    expect(result.every(e => e.kind === 'log')).toBe(true)
  })

  it('showLogMarkers=false removes all 3 log events', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showLogMarkers: false })
    expect(result).toHaveLength(1)
    expect(result[0].kind).toBe('level')
  })

  it('showWarn=false removes only warn event', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showWarn: false })
    expect(result).toHaveLength(3)
    expect(result.find(e => e.log?.level === 'warn')).toBeUndefined()
  })

  it('showLogMarkers=false overrides individual level flags', () => {
    const s: LayerSettings = { ...DEFAULT_LAYER_SETTINGS, showLogMarkers: false, showInfo: true }
    expect(filterEvents(all, s).filter(e => e.kind === 'log')).toHaveLength(0)
  })
})

// ── computeCardStats ──────────────────────────────────────────────────────────

describe('computeCardStats', () => {
  it('empty events → 0 count, 0 volume', () => {
    expect(computeCardStats([])).toEqual({ filledCount: 0, volumeUsdt: 0 })
  })

  it('counts only level events', () => {
    const events = [makeLevelEvent('Buy', 100), makeLogEvent('info')]
    expect(computeCardStats(events).filledCount).toBe(1)
  })

  it('sums sizeUsdt of all level events', () => {
    const events = [makeLevelEvent('Buy', 100), makeLevelEvent('Sell', 150)]
    expect(computeCardStats(events).volumeUsdt).toBe(250)
  })
})

// ── formatPnl ─────────────────────────────────────────────────────────────────

describe('formatPnl', () => {
  it('positive number gets + prefix', () => {
    expect(formatPnl(124.5)).toBe('+124.50 $')
  })

  it('negative number has no extra prefix', () => {
    expect(formatPnl(-30)).toBe('-30.00 $')
  })

  it('zero is treated as positive', () => {
    expect(formatPnl(0)).toBe('+0.00 $')
  })
})
```

- [ ] **Step 2: Запустить тесты — убедиться что падают**

```
cd C:\Users\123\Projects\sis\frontend
npx vitest run src/__tests__/LogVisualizerTab.test.tsx
```

Ожидаемый результат: FAIL — `filterEvents`, `computeCardStats`, `formatPnl` not exported from utils.

- [ ] **Step 3: Реализовать функции в utils.ts**

Полностью заменить `frontend/src/features/log-visualizer/utils.ts`:

```typescript
// frontend/src/features/log-visualizer/utils.ts

import type { LVEvent, LVLevel, MergedEvent, LayerSettings } from './types'

/** Returns label for display in events list and info panel. */
export function makeMergedEventLabel(
  kind: 'log' | 'level',
  log?: LVEvent,
  level?: LVLevel,
): string {
  if (kind === 'level' && level) {
    const dir = level.side === 'Buy' ? '▲' : '▼'
    const status = level.status === 'sl_closed' ? ' [SL]' : ''
    return `${dir} L${level.levelIdx} ${level.filledPrice.toFixed(4)} · ${level.qty}${status}`
  }
  return log?.message ?? '—'
}

/** Filter merged events according to current layer settings. */
export function filterEvents(events: MergedEvent[], settings: LayerSettings): MergedEvent[] {
  return events.filter(ev => {
    if (ev.kind === 'level') return settings.showOrderMarkers
    if (ev.kind === 'log') {
      if (!settings.showLogMarkers) return false
      const lvl = ev.log?.level
      if (lvl === 'info'  && !settings.showInfo)  return false
      if (lvl === 'warn'  && !settings.showWarn)  return false
      if (lvl === 'error' && !settings.showError) return false
      return true
    }
    return true
  })
}

/** Compute filled-order statistics from the currently visible events. */
export function computeCardStats(visibleEvents: MergedEvent[]): {
  filledCount: number
  volumeUsdt:  number
} {
  const filledLevels = visibleEvents.filter(ev => ev.kind === 'level')
  return {
    filledCount: filledLevels.length,
    volumeUsdt:  filledLevels.reduce((sum, ev) => sum + (ev.level?.sizeUsdt ?? 0), 0),
  }
}

/** Format a PnL number with sign and 2 decimal places, e.g. "+124.50 $" */
export function formatPnl(pnl: number): string {
  return (pnl >= 0 ? '+' : '') + pnl.toFixed(2) + ' $'
}
```

- [ ] **Step 4: Запустить тесты — убедиться что проходят**

```
cd C:\Users\123\Projects\sis\frontend
npx vitest run src/__tests__/LogVisualizerTab.test.tsx
```

Ожидаемый результат: все 12 тестов PASS (4 старых + 5 filterEvents + 3 computeCardStats + 3 formatPnl).

- [ ] **Step 5: Commit**

```
git add frontend/src/features/log-visualizer/utils.ts frontend/src/__tests__/LogVisualizerTab.test.tsx
git commit -m "feat(lv): add filterEvents, computeCardStats, formatPnl utilities + tests"
```

---

## Task 4: Компонент LogVisualizerLayersPopup

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx`

- [ ] **Step 1: Создать компонент**

Создать `frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx`:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx

import { useEffect, useRef, useState } from 'react'
import type { LayerSettings } from './types'

interface Props {
  settings: LayerSettings
  onChange: (s: LayerSettings) => void
}

function Toggle({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      className="flex-shrink-0 relative focus:outline-none"
      style={{ width: 28, height: 15 }}
    >
      <span
        style={{
          display: 'block',
          width: 28,
          height: 15,
          borderRadius: 8,
          background: on ? '#4a7dff' : 'rgba(255,255,255,.15)',
          transition: 'background .15s',
        }}
      />
      <span
        style={{
          position: 'absolute',
          top: 2,
          ...(on ? { right: 2 } : { left: 2 }),
          width: 11,
          height: 11,
          borderRadius: '50%',
          background: 'white',
          transition: 'left .15s, right .15s',
        }}
      />
    </button>
  )
}

const CHIP_COLORS = {
  info:  { text: '#94a3b8', bg: 'rgba(148,163,184,.15)', border: 'rgba(148,163,184,.3)' },
  warn:  { text: '#fbbf24', bg: 'rgba(251,191,36,.12)',  border: 'rgba(245,158,11,.3)'  },
  error: { text: '#f87171', bg: 'rgba(248,113,113,.12)', border: 'rgba(239,68,68,.3)'   },
}

const TOGGLE_ROWS: Array<{ key: keyof LayerSettings; label: string }> = [
  { key: 'showOrderMarkers', label: 'Маркеры ордеров' },
  { key: 'showLogMarkers',   label: 'Маркеры событий' },
  { key: 'showPriceLines',   label: 'Ценовые линии'   },
]

const LOG_LEVELS = [
  { lvl: 'info',  key: 'showInfo'  as const },
  { lvl: 'warn',  key: 'showWarn'  as const },
  { lvl: 'error', key: 'showError' as const },
]

export function LogVisualizerLayersPopup({ settings, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  // Close on outside click
  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const patch = (update: Partial<LayerSettings>) => onChange({ ...settings, ...update })

  // Button is highlighted when any layer is hidden
  const isModified =
    !settings.showOrderMarkers || !settings.showLogMarkers || !settings.showPriceLines ||
    !settings.showInfo || !settings.showWarn || !settings.showError

  return (
    <div ref={containerRef} className="relative">
      {/* Toolbar button */}
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        title="Слои графика"
        className={`flex items-center gap-1 rounded px-2 py-1 text-xs border transition-colors focus:outline-none ${
          isModified
            ? 'border-[#4a7dff]/40 bg-[#4a7dff]/15 text-[#b8c8ff]'
            : 'border-white/[.08] bg-white/[.04] text-slate-400 hover:bg-white/[.07] hover:text-slate-200'
        }`}
      >
        {/* Layers icon */}
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <line x1="4" y1="6"  x2="20" y2="6"/>
          <line x1="4" y1="12" x2="20" y2="12"/>
          <line x1="4" y1="18" x2="20" y2="18"/>
          <circle cx="9"  cy="6"  r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="15" cy="12" r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="7"  cy="18" r="2.5" fill="currentColor" stroke="none"/>
        </svg>
        Слои
      </button>

      {/* Dropdown popup */}
      {open && (
        <div
          className="absolute top-full mt-1 z-50"
          style={{
            background:   '#0d1220',
            border:       '1px solid rgba(255,255,255,.10)',
            borderRadius: 12,
            width:        220,
            overflow:     'hidden',
          }}
        >
          {/* Section: layer toggles */}
          <div style={{ padding: '10px 12px 4px', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 1, color: '#475569' }}>
            Слои графика
          </div>
          <div style={{ padding: '0 6px 6px' }}>
            {TOGGLE_ROWS.map(({ key, label }) => (
              <div key={key} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '5px 8px', borderRadius: 6 }}>
                <span style={{ fontSize: 12, color: '#e2e8f0' }}>{label}</span>
                <Toggle
                  on={settings[key] as boolean}
                  onToggle={() => patch({ [key]: !settings[key] })}
                />
              </div>
            ))}
          </div>

          {/* Divider */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,.07)', margin: '0 12px' }} />

          {/* Section: log level chips */}
          <div style={{ padding: '4px 12px 4px', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 1, color: '#475569' }}>
            Уровень лога
          </div>
          <div style={{ display: 'flex', gap: 4, padding: '6px 14px 10px' }}>
            {LOG_LEVELS.map(({ lvl, key }) => {
              const active   = settings[key]
              const disabled = !settings.showLogMarkers
              const c        = CHIP_COLORS[lvl as keyof typeof CHIP_COLORS]
              return (
                <button
                  key={lvl}
                  type="button"
                  disabled={disabled}
                  onClick={() => patch({ [key]: !active })}
                  style={{
                    padding:    '2px 8px',
                    borderRadius: 5,
                    fontSize:   11,
                    fontWeight: 600,
                    cursor:     disabled ? 'default' : 'pointer',
                    opacity:    disabled ? 0.35 : 1,
                    background: active && !disabled ? c.bg   : 'rgba(255,255,255,.04)',
                    color:      active && !disabled ? c.text : '#475569',
                    border:     `1px solid ${active && !disabled ? c.border : 'transparent'}`,
                    transition: 'all .15s',
                  }}
                >
                  {lvl}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Проверить что TypeScript не ругается**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Ожидаемый результат: 0 новых ошибок.

- [ ] **Step 3: Commit**

```
git add frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx
git commit -m "feat(lv): add LogVisualizerLayersPopup component"
```

---

## Task 5: Обновить LogVisualizerChart

**Files:**
- Modify: `frontend/src/features/log-visualizer/LogVisualizerChart.tsx`

Добавить `layerSettings` prop, заменить `useEffect([events])` маркеров, добавить `useEffect` для price lines.

- [ ] **Step 1: Полностью заменить файл**

Заменить содержимое `frontend/src/features/log-visualizer/LogVisualizerChart.tsx`:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerChart.tsx

import { useEffect, useRef } from 'react'
import {
  createChart, CandlestickSeries, createSeriesMarkers,
  ColorType, type IChartApi, type ISeriesApi, type IPriceLine,
} from 'lightweight-charts'
import type { LVCandle, MergedEvent, LayerSettings } from './types'
import { filterEvents } from './utils'

interface Props {
  candles:       LVCandle[]
  events:        MergedEvent[]
  layerSettings: LayerSettings
}

export function LogVisualizerChart({ candles, events, layerSettings }: Props) {
  const containerRef  = useRef<HTMLDivElement>(null)
  const chartRef      = useRef<IChartApi | null>(null)
  const seriesRef     = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersRef    = useRef<any>(null)
  const priceLinesRef = useRef<IPriceLine[]>([])

  // Initialize chart once on mount
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
      chartRef.current      = null
      seriesRef.current     = null
      markersRef.current    = null
      priceLinesRef.current = []
    }
  }, [])

  // Update candle data when slice changes
  useEffect(() => {
    if (!seriesRef.current || candles.length === 0) return
    const data = candles.map(c => ({
      time:  Math.floor(c.t / 1000) as import('lightweight-charts').Time,
      open:  c.o, high: c.h, low: c.l, close: c.c,
    }))
    seriesRef.current.setData(data)
  }, [candles])

  // Update event markers when list or layer settings change
  useEffect(() => {
    if (!markersRef.current) return
    const visible = filterEvents(events, layerSettings)
    const markers = visible.map(ev => {
      type MarkerPos   = 'aboveBar' | 'belowBar' | 'inBar'
      type MarkerShape = 'arrowUp' | 'arrowDown' | 'circle'

      let position: MarkerPos
      let shape: MarkerShape
      let color: string
      let text: string | undefined

      if (ev.kind === 'level' && ev.level) {
        const isBuy = ev.level.side === 'Buy'
        position = isBuy ? 'belowBar' : 'aboveBar'
        shape    = isBuy ? 'arrowUp'  : 'arrowDown'
        color    = isBuy ? '#34d399'  : '#f87171'
        text     = `L${ev.level.levelIdx}`
      } else {
        position = 'inBar'
        shape    = 'circle'
        color    = ev.log?.level === 'error' ? '#f87171'
                 : ev.log?.level === 'warn'  ? '#fbbf24' : '#94a3b8'
        text     = undefined
      }

      return {
        time: Math.floor(ev.tsMs / 1000) as import('lightweight-charts').Time,
        position,
        color,
        shape,
        text,
        size: 1,
      }
    })
    markersRef.current.setMarkers(markers)
  }, [events, layerSettings])

  // Rebuild price lines when events change or showPriceLines is toggled
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return

    // Remove all existing price lines
    priceLinesRef.current.forEach(pl => series.removePriceLine(pl))
    priceLinesRef.current = []

    if (!layerSettings.showPriceLines) return

    priceLinesRef.current = events
      .filter(ev => ev.kind === 'level' && ev.level)
      .map(ev =>
        series.createPriceLine({
          price:            ev.level!.filledPrice,
          color:            ev.level!.side === 'Buy' ? '#34d399' : '#f87171',
          lineWidth:        1,
          lineStyle:        2,    // LineStyle.Dashed
          axisLabelVisible: true,
          title:            `L${ev.level!.levelIdx}`,
        })
      )
  }, [events, layerSettings.showPriceLines])

  return (
    <div ref={containerRef} className="w-full h-full" />
  )
}
```

- [ ] **Step 2: Проверить TypeScript**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Ожидаемый результат: 0 новых ошибок.

- [ ] **Step 3: Commit**

```
git add frontend/src/features/log-visualizer/LogVisualizerChart.tsx
git commit -m "feat(lv): add layerSettings prop to LogVisualizerChart, add price lines support"
```

---

## Task 6: Компонент LogVisualizerStrategyCard

**Files:**
- Create: `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx`

- [ ] **Step 1: Создать компонент**

Создать `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx`:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx

import type { LVStrategy, MergedEvent } from './types'
import { computeCardStats, formatPnl } from './utils'

interface Props {
  strategy:      LVStrategy
  visibleEvents: MergedEvent[]
}

export function LogVisualizerStrategyCard({ strategy, visibleEvents }: Props) {
  const { filledCount, volumeUsdt } = computeCardStats(visibleEvents)
  const pnl    = strategy.lastPnl
  const isLong = strategy.direction.toLowerCase() === 'long'

  return (
    <div
      className="absolute bottom-4 right-4 backdrop-blur-sm pointer-events-none select-none"
      style={{
        background:   'rgba(6,6,12,0.85)',
        border:       '1px solid rgba(255,255,255,.10)',
        borderRadius: 10,
        padding:      '10px 12px',
        width:        200,
      }}
    >
      {/* Header row: symbol + direction badge */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 3 }}>
        <span style={{ fontSize: 12, fontWeight: 700, color: '#e2e8f0' }}>
          {strategy.symbol}
        </span>
        <span style={{
          fontSize:   9,
          fontWeight: 700,
          padding:    '1px 6px',
          borderRadius: 4,
          color:      isLong ? '#34d399' : '#f87171',
          background: isLong ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.12)',
        }}>
          {strategy.direction.toUpperCase()}
        </span>
      </div>

      {/* Subheader: type · status */}
      <div style={{ fontSize: 10, color: '#64748b', marginBottom: 8 }}>
        {strategy.strategyType} · {strategy.status}
      </div>

      {/* Stats grid */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '4px 8px', marginBottom: pnl !== null ? 8 : 0 }}>
        <div>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase' }}>Взято ордеров</div>
          <div style={{ fontSize: 13, color: '#e2e8f0', fontWeight: 700, fontFamily: 'monospace' }}>
            {filledCount}&nbsp;/&nbsp;{strategy.gridLevels}
          </div>
        </div>
        <div>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase' }}>Объём</div>
          <div style={{ fontSize: 13, color: '#e2e8f0', fontWeight: 700, fontFamily: 'monospace' }}>
            {volumeUsdt.toFixed(2)}&nbsp;$
          </div>
        </div>
      </div>

      {/* Last PnL — only shown when available */}
      {pnl !== null && (
        <div style={{ borderTop: '1px solid rgba(255,255,255,.06)', paddingTop: 7 }}>
          <div style={{ fontSize: 9, color: '#475569', textTransform: 'uppercase', marginBottom: 2 }}>
            Last PnL стратегии
          </div>
          <div style={{
            fontSize:   14,
            fontWeight: 700,
            fontFamily: 'monospace',
            color:      pnl > 0 ? '#34d399' : pnl < 0 ? '#f87171' : '#94a3b8',
          }}>
            {formatPnl(pnl)}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Проверить TypeScript**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Ожидаемый результат: 0 новых ошибок.

- [ ] **Step 3: Commit**

```
git add frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx
git commit -m "feat(lv): add LogVisualizerStrategyCard component"
```

---

## Task 7: Wiring в LogVisualizerTab + финальная проверка

**Files:**
- Modify: `frontend/src/features/log-visualizer/LogVisualizerTab.tsx`

Добавить `layerSettings` state, вставить попап в тулбар, добавить `strategy` derived var, сделать chart-wrapper `relative`, добавить `LogVisualizerStrategyCard`.

- [ ] **Step 1: Полностью заменить файл**

Заменить содержимое `frontend/src/features/log-visualizer/LogVisualizerTab.tsx`:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerTab.tsx

import { useState, useEffect, useRef, useCallback } from 'react'
import { LogVisualizerChart }      from './LogVisualizerChart'
import { LogVisualizerEventsList } from './LogVisualizerEventsList'
import { LogVisualizerControls }   from './LogVisualizerControls'
import { LogVisualizerLayersPopup } from './LogVisualizerLayersPopup'
import { LogVisualizerStrategyCard } from './LogVisualizerStrategyCard'
import { lvGetAccounts, lvGetStrategies, lvGetEvents, lvGetLevels, lvGetKlines } from './api'
import { makeMergedEventLabel } from './utils'
import type { LVAccount, LVStrategy, LVCandle, MergedEvent, Interval } from './types'
import { INTERVALS, DEFAULT_LAYER_SETTINGS } from './types'
import type { LayerSettings } from './types'

// Speed: candles per second = speed * CANDLES_PER_SEC_BASE
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
  const [interval,   setIntervalVal] = useState<Interval>('1m')
  const [speed,      setSpeed]      = useState(8)
  const [isMax,      setIsMax]      = useState(false)

  // ── Layer settings ────────────────────────────────────────────────────
  const [layerSettings, setLayerSettings] = useState<LayerSettings>(DEFAULT_LAYER_SETTINGS)

  // ── Loaded data ───────────────────────────────────────────────────────
  const [candles,   setCandles]   = useState<LVCandle[]>([])
  const [events,    setEvents]    = useState<MergedEvent[]>([])
  const [loading,   setLoading]   = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  // ── Animation state ───────────────────────────────────────────────────
  const [candleIdx,  setCandleIdx]  = useState(-1)
  const [eventIdx,   setEventIdx]   = useState(-1)
  const [isPlaying,  setIsPlaying]  = useState(false)

  // Stable refs for animation loop (avoid stale closures)
  const candleIdxRef = useRef(candleIdx)
  const eventIdxRef  = useRef(eventIdx)
  const candlesRef   = useRef(candles)
  const eventsRef    = useRef(events)
  // Generation counter: prevents a slow first load from overwriting a faster second load
  const loadGenRef   = useRef(0)
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
    const gen = ++loadGenRef.current
    setLoading(true)
    setLoadError(null)
    setCandles([]); setEvents([])
    setCandleIdx(-1); setEventIdx(-1); setIsPlaying(false)

    try {
      const fromMs = new Date(fromDate).getTime()
      const toMs   = new Date(toDate).getTime() + 86_400_000

      const strat  = strategies.find(s => s.id === strategyId)
      const symbol = strat?.symbol ?? ''

      const [eventsRaw, levelsRaw, candlesRaw] = await Promise.all([
        lvGetEvents(strategyId, fromMs, toMs),
        lvGetLevels(strategyId, fromMs, toMs),
        lvGetKlines(symbol, interval, fromMs, toMs),
      ])

      if (gen !== loadGenRef.current) return

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
      if (gen !== loadGenRef.current) return
      setLoadError(e instanceof Error ? e.message : 'Ошибка загрузки')
    } finally {
      if (gen === loadGenRef.current) setLoading(false)
    }
  }, [strategyId, fromDate, toDate, interval, strategies])

  // ── Animation tick ────────────────────────────────────────────────────
  useEffect(() => {
    if (!isPlaying) return

    if (isMax) {
      const nextEvIdx = eventIdxRef.current + 1
      if (nextEvIdx >= eventsRef.current.length) {
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
      const ci     = candleIdxRef.current
      const ei     = eventIdxRef.current
      const cs     = candlesRef.current
      const evs    = eventsRef.current
      const nextCi = ci + 1

      if (nextCi >= cs.length) {
        setIsPlaying(false)
        return
      }

      const nextCandle = cs[nextCi]
      const nextEvent  = evs[ei + 1]

      if (nextEvent && nextCandle.t >= nextEvent.tsMs) {
        setCandleIdx(nextCi)
        setEventIdx(ei + 1)
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

  const handlePrev  = useCallback(() => jumpToEvent(eventIdx - 1),      [jumpToEvent, eventIdx])
  const handleNext  = useCallback(() => jumpToEvent(eventIdx + 1),      [jumpToEvent, eventIdx])
  const handleFirst = useCallback(() => jumpToEvent(0),                 [jumpToEvent])
  const handleLast  = useCallback(() => jumpToEvent(events.length - 1), [jumpToEvent, events.length])

  // ── Derived data ──────────────────────────────────────────────────────
  const visibleCandles = candleIdx >= 0 ? candles.slice(0, candleIdx + 1) : []
  const visibleEvents  = eventIdx  >= 0 ? events.slice(0, eventIdx + 1)  : []
  const currentEvent   = eventIdx >= 0 ? events[eventIdx] : null
  const hasData        = candles.length > 0
  const strategy       = strategies.find(s => s.id === strategyId) ?? null

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
          onChange={e => setIntervalVal(e.target.value as Interval)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-1 text-xs text-slate-200 focus:outline-none"
        >
          {INTERVALS.map(iv => <option key={iv} value={iv}>{iv}</option>)}
        </select>

        {/* Layers popup */}
        <LogVisualizerLayersPopup
          settings={layerSettings}
          onChange={setLayerSettings}
        />

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

      {/* Large range warning */}
      {!loading && candles.length > 30_000 && (
        <div className="flex-shrink-0 mx-4 mt-1 text-[10px] text-amber-400/70">
          ⚠ Загружено {candles.length.toLocaleString('ru-RU')} свечей — большой диапазон, анимация может быть медленной
        </div>
      )}

      {/* Main area: chart + sidebar */}
      <div className="flex-1 flex overflow-hidden min-h-0">
        {/* Chart wrapper — relative позволяет StrategyCard позиционироваться absolute */}
        <div className="flex-1 relative min-w-0">
          <LogVisualizerChart
            candles={visibleCandles}
            events={visibleEvents}
            layerSettings={layerSettings}
          />
          {hasData && strategy && (
            <LogVisualizerStrategyCard
              strategy={strategy}
              visibleEvents={visibleEvents}
            />
          )}
        </div>
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

- [ ] **Step 2: Запустить все тесты**

```
cd C:\Users\123\Projects\sis\frontend
npx vitest run
```

Ожидаемый результат: все тесты PASS (те же 12 из LogVisualizerTab.test.tsx + прочие).

- [ ] **Step 3: Проверить TypeScript**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Ожидаемый результат: 0 новых ошибок.

- [ ] **Step 4: Проверить production build**

```
cd C:\Users\123\Projects\sis\frontend
npm run build
```

Ожидаемый результат: build успешен, ~seconds, без ошибок TypeScript.

- [ ] **Step 5: Commit**

```
git add frontend/src/features/log-visualizer/LogVisualizerTab.tsx
git commit -m "feat(lv): wire layerSettings popup + strategy mini-card into LogVisualizerTab"
```

---

## Контрольный список (самопроверка)

После выполнения всех задач убедиться что:

- [ ] `go build ./...` в `services/api-gateway` — без ошибок
- [ ] `npx vitest run` во `frontend` — все тесты PASS
- [ ] `npm run build` во `frontend` — без ошибок
- [ ] Кнопка «Слои» появляется в тулбаре между интервалом и «▶ Загрузить»
- [ ] Попап открывается/закрывается кликом на кнопку и кликом вне
- [ ] Тоггл «Маркеры ордеров» скрывает/показывает стрелки на графике
- [ ] Тоггл «Маркеры событий» скрывает/показывает кружки; чипы уровня становятся disabled
- [ ] Тоггл «Ценовые линии» рисует горизонтальные линии по filledPrice каждого уровня
- [ ] Мини-карточка появляется в правом нижнем углу графика после загрузки данных
- [ ] Счётчик «Взято ордеров» увеличивается при каждом level-событии в анимации
- [ ] Объём увеличивается вместе с счётчиком
- [ ] Last PnL отображается если `strategy.lastPnl !== null`, скрыт иначе
