# Log Visualizer UI Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three UI improvements to the Log Visualizer: date in event timestamps, filter chips in the events sidebar, and a draggable strategy card.

**Architecture:** All changes are pure frontend — no backend modifications. Task 1 adds the `EventListFilter` type and `filterEventsList` pure function (with tests). Task 2 wires the new filter into the events list component and changes the timestamp format. Task 3 adds mouse-drag capability to the strategy card overlay.

**Tech Stack:** React 18, TypeScript, Tailwind CSS, Vitest

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `frontend/src/features/log-visualizer/types.ts` | Modify | Add `EventListFilter` union type |
| `frontend/src/features/log-visualizer/utils.ts` | Modify | Add `filterEventsList` function |
| `frontend/src/__tests__/LogVisualizerTab.test.tsx` | Modify | Tests for `filterEventsList` |
| `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx` | Modify | Date format + filter chips |
| `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx` | Modify | Drag & drop |

---

## Task 1: `EventListFilter` type + `filterEventsList` function + tests

**Files:**
- Modify: `frontend/src/features/log-visualizer/types.ts`
- Modify: `frontend/src/features/log-visualizer/utils.ts`
- Modify: `frontend/src/__tests__/LogVisualizerTab.test.tsx`

- [ ] **Step 1: Write failing tests for `filterEventsList`**

Open `frontend/src/__tests__/LogVisualizerTab.test.tsx` and add after the existing `formatPnl` describe block (before the closing of the file):

```typescript
// ── filterEventsList ──────────────────────────────────────────────────────────

function makeSlClosedLevel(): MergedEvent {
  return {
    tsMs: 0, kind: 'level',
    level: { levelIdx: 2, side: 'Sell', filledPrice: 50000, qty: '0.002', sizeUsdt: 100, status: 'sl_closed', tsMs: 0 },
    label: '',
  }
}

function makeLogWithMsg(level: 'info' | 'warn' | 'error', message: string): MergedEvent {
  return {
    tsMs: 0, kind: 'log',
    log: { message, level, tsMs: 0 },
    label: '',
  }
}

describe('filterEventsList', () => {
  const order   = makeLevelEvent('Buy')
  const slLevel = makeSlClosedLevel()
  const tpLog   = makeLogWithMsg('info', 'TP исполнен @ 99100')
  const slLog   = makeLogWithMsg('info', 'SL сработал')
  const errLog  = makeLogEvent('error')
  const infoLog = makeLogEvent('info')
  const all     = [order, slLevel, tpLog, slLog, errLog, infoLog]

  it("'all' returns every event unchanged", () => {
    expect(filterEventsList(all, 'all')).toHaveLength(6)
    expect(filterEventsList(all, 'all')).toBe(all)
  })

  it("'orders' returns only kind==='level' events (both filled and sl_closed)", () => {
    const result = filterEventsList(all, 'orders')
    expect(result).toHaveLength(2)
    expect(result.every(e => e.kind === 'level')).toBe(true)
  })

  it("'closes' returns sl_closed levels + logs matching TP/SL regex", () => {
    const result = filterEventsList(all, 'closes')
    expect(result).toHaveLength(3)
    expect(result).toContain(slLevel)
    expect(result).toContain(tpLog)
    expect(result).toContain(slLog)
  })

  it("'errors' returns only error-level log events", () => {
    const result = filterEventsList(all, 'errors')
    expect(result).toHaveLength(1)
    expect(result[0]).toBe(errLog)
  })

  it('empty array returns empty for all filters', () => {
    expect(filterEventsList([], 'all')).toHaveLength(0)
    expect(filterEventsList([], 'orders')).toHaveLength(0)
    expect(filterEventsList([], 'closes')).toHaveLength(0)
    expect(filterEventsList([], 'errors')).toHaveLength(0)
  })
})
```

Also add `filterEventsList` and `EventListFilter` to the imports at the top:

```typescript
// Replace the existing import line:
import { makeMergedEventLabel, filterEvents, computeCardStats, formatPnl } from '../features/log-visualizer/utils'
import type { LVLevel, LVEvent, MergedEvent, LayerSettings, EventListFilter } from '../features/log-visualizer/types'
import { DEFAULT_LAYER_SETTINGS } from '../features/log-visualizer/types'

// Add to utils import:
import { makeMergedEventLabel, filterEvents, computeCardStats, formatPnl, filterEventsList } from '../features/log-visualizer/utils'
```

The final import block at the top of the test file should be:

```typescript
import { describe, it, expect } from 'vitest'
import { makeMergedEventLabel, filterEvents, computeCardStats, formatPnl, filterEventsList } from '../features/log-visualizer/utils'
import type { LVLevel, LVEvent, MergedEvent, LayerSettings, EventListFilter } from '../features/log-visualizer/types'
import { DEFAULT_LAYER_SETTINGS } from '../features/log-visualizer/types'
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd frontend && npx vitest run src/__tests__/LogVisualizerTab.test.tsx
```

Expected: tests for `filterEventsList` FAIL with something like `filterEventsList is not a function` or similar import error.

- [ ] **Step 3: Add `EventListFilter` type to `types.ts`**

Open `frontend/src/features/log-visualizer/types.ts`. Add at the end of the file (after the `Interval` type):

```typescript
export type EventListFilter = 'all' | 'orders' | 'closes' | 'errors'
```

The end of the file should look like:

```typescript
export const INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D'] as const
export type Interval = typeof INTERVALS[number]

export type EventListFilter = 'all' | 'orders' | 'closes' | 'errors'
```

- [ ] **Step 4: Add `filterEventsList` to `utils.ts`**

Open `frontend/src/features/log-visualizer/utils.ts`.

First, update the import on line 3 to include `EventListFilter`:

```typescript
import type { LVEvent, LVLevel, MergedEvent, LayerSettings, EventListFilter } from './types'
```

Then add the following function at the end of the file (after `formatPnl`):

```typescript
/** Filter sidebar events by category. */
export function filterEventsList(
  events: MergedEvent[],
  filter: EventListFilter,
): MergedEvent[] {
  if (filter === 'all') return events
  return events.filter(ev => {
    switch (filter) {
      case 'orders':
        return ev.kind === 'level'
      case 'closes':
        return (
          (ev.kind === 'level' && ev.level?.status === 'sl_closed') ||
          (ev.kind === 'log'   && /TP исполнен|SL сработал/i.test(ev.log?.message ?? ''))
        )
      case 'errors':
        return ev.kind === 'log' && ev.log?.level === 'error'
      default:
        return true
    }
  })
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd frontend && npx vitest run src/__tests__/LogVisualizerTab.test.tsx
```

Expected: All tests PASS. Output should include:
```
✓ filterEventsList > 'all' returns every event unchanged
✓ filterEventsList > 'orders' returns only kind==='level' events (both filled and sl_closed)
✓ filterEventsList > 'closes' returns sl_closed levels + logs matching TP/SL regex
✓ filterEventsList > 'errors' returns only error-level log events
✓ filterEventsList > empty array returns empty for all filters
```

- [ ] **Step 6: Commit**

```bash
cd frontend && git add src/features/log-visualizer/types.ts src/features/log-visualizer/utils.ts src/__tests__/LogVisualizerTab.test.tsx && git commit -m "feat(log-visualizer): add EventListFilter type and filterEventsList function"
```

(Run from the `sis` root if `cd frontend` doesn't apply — adjust path prefix as needed.)

---

## Task 2: Date format + filter chips in `LogVisualizerEventsList`

**Files:**
- Modify: `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx`

This task has no pure-logic unit tests (it's a UI component), but the visual and functional result must be verified manually. The filter logic itself is already tested in Task 1.

- [ ] **Step 1: Replace the entire file content**

Open `frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx` and replace everything with:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx

import { useEffect, useRef, useState } from 'react'
import type { MergedEvent, EventListFilter } from './types'
import { filterEventsList } from './utils'

interface Props {
  events:       MergedEvent[]
  currentIndex: number           // -1 = before first event
  onJump:       (idx: number) => void
}

const CHIPS: { label: string; value: EventListFilter }[] = [
  { label: 'Все',      value: 'all'    },
  { label: 'Ордера',   value: 'orders' },
  { label: 'Закрытия', value: 'closes' },
  { label: 'Ошибки',   value: 'errors' },
]

export function LogVisualizerEventsList({ events, currentIndex, onJump }: Props) {
  const activeRef = useRef<HTMLButtonElement>(null)
  const [filter, setFilter] = useState<EventListFilter>('all')

  // Auto-scroll to current event
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [currentIndex])

  function fmtTime(tsMs: number) {
    const d    = new Date(tsMs)
    const date = d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
    const time = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    return `${date} ${time}`
  }

  const filtered = filterEventsList(events, filter)

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

      {/* Filter chips */}
      <div className="flex-shrink-0 flex flex-wrap gap-1 px-3 py-1.5 border-b border-white/[.06]">
        {CHIPS.map(chip => {
          const active = chip.value === filter
          return (
            <button
              key={chip.value}
              onClick={() => setFilter(chip.value)}
              style={{
                fontSize:     10,
                padding:      '2px 8px',
                borderRadius: 5,
                border:       active ? '1px solid rgba(74,125,255,.3)' : '1px solid transparent',
                background:   active ? 'rgba(74,125,255,.15)' : 'rgba(255,255,255,.04)',
                color:        active ? '#b8c8ff' : '#475569',
                cursor:       'pointer',
              }}
            >
              {chip.label}
            </button>
          )
        })}
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="flex h-full items-center justify-center text-[11px] text-slate-600">
            Нет событий
          </div>
        ) : (
          filtered.map(ev => {
            const originalIdx = events.indexOf(ev)
            const isCurrent   = originalIdx === currentIndex
            const isPast      = originalIdx < currentIndex
            return (
              <button
                key={originalIdx}
                ref={isCurrent ? activeRef : undefined}
                onClick={() => onJump(originalIdx)}
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

- [ ] **Step 2: Verify TypeScript compiles without errors**

```bash
cd frontend && npx tsc --noEmit
```

Expected: No errors. If there are errors they will relate to import paths or missing exports — fix by re-checking Task 1 steps.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx
git commit -m "feat(log-visualizer): add date to timestamps and filter chips in events list"
```

---

## Task 3: Draggable strategy card

**Files:**
- Modify: `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx`

- [ ] **Step 1: Replace the entire file content**

Open `frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx` and replace everything with:

```tsx
// frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx

import { useRef, useState } from 'react'
import type { LVStrategy, MergedEvent } from './types'
import { computeCardStats, formatPnl } from './utils'

interface Props {
  strategy:      LVStrategy
  visibleEvents: MergedEvent[]
}

export function LogVisualizerStrategyCard({ strategy, visibleEvents }: Props) {
  const cardRef = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  // pos === null means "use default CSS position (bottom-4 right-4)"

  const { filledCount, volumeUsdt } = computeCardStats(visibleEvents)
  const pnl    = strategy.lastPnl
  const isLong = strategy.direction.toLowerCase() === 'long'

  function handleMouseDown(e: React.MouseEvent) {
    e.preventDefault()
    const card = cardRef.current
    if (!card) return
    const container = card.parentElement
    if (!container) return

    const cardRect      = card.getBoundingClientRect()
    const containerRect = container.getBoundingClientRect()

    // On first drag, compute starting position from the rendered layout
    const startX = pos?.x ?? containerRect.width  - cardRect.width  - 16
    const startY = pos?.y ?? containerRect.height - cardRect.height - 16

    const offsetX = e.clientX - containerRect.left - startX
    const offsetY = e.clientY - containerRect.top  - startY

    const onMove = (me: MouseEvent) => {
      if (!card.parentElement) return
      const cr = card.parentElement.getBoundingClientRect()
      const x = Math.max(0, Math.min(cr.width  - card.offsetWidth,  me.clientX - cr.left - offsetX))
      const y = Math.max(0, Math.min(cr.height - card.offsetHeight, me.clientY - cr.top  - offsetY))
      setPos({ x, y })
    }

    const onUp = () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup',   onUp)
    }

    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup',   onUp)
  }

  return (
    <div
      ref={cardRef}
      onMouseDown={handleMouseDown}
      className="absolute z-10 backdrop-blur-sm select-none"
      style={{
        ...(pos ? { left: pos.x, top: pos.y } : { bottom: 16, right: 16 }),
        cursor:       'grab',
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
          fontSize:     9,
          fontWeight:   700,
          padding:      '1px 6px',
          borderRadius: 4,
          color:        isLong ? '#34d399' : '#f87171',
          background:   isLong ? 'rgba(52,211,153,.12)' : 'rgba(248,113,113,.12)',
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

- [ ] **Step 2: Verify TypeScript compiles without errors**

```bash
cd frontend && npx tsc --noEmit
```

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/log-visualizer/LogVisualizerStrategyCard.tsx
git commit -m "feat(log-visualizer): make strategy card draggable with mouse"
```

---

## Self-Review Checklist

- Spec Фича 1 (дата в сайдбаре) → Task 2: `fmtTime` переписан с `DD.MM HH:MM:SS` ✅
- Spec Фича 2 (фильтр событий):
  - `EventListFilter` тип → Task 1 ✅
  - `filterEventsList` функция → Task 1 ✅
  - Тесты функции → Task 1 ✅
  - Чипы в компоненте → Task 2 ✅
  - `onJump(originalIdx)` через `events.indexOf(ev)` → Task 2 ✅
- Spec Фича 3 (перетаскиваемая карточка) → Task 3 ✅
- Удалён `pointer-events-none` → Task 3 ✅
- `cursor: grab` → Task 3 ✅
- Зажим в границах `card.parentElement` → Task 3 ✅
