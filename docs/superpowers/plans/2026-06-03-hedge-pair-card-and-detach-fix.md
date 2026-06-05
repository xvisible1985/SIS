# Hedge Pair Card & Detach Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the bot-detach ordering bug (show blacklist question first, then call API), and add a `HedgePairCard` component that renders both the main (long) and hedge (short) strategies as a single combined card when a hedge bot is actively hedging.

**Architecture:** Two independent changes. Task 1 is a small logic reorder inside `BotBadge`. Tasks 2–4 create `HedgePairCard` and wire it into both list pages by grouping strategies into pairs before rendering.

**Tech Stack:** React 18, TypeScript, Tailwind CSS (className), existing api helpers (`setStrategyStatus`, `detachFromBot`, `addBotBlacklist`)

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `frontend/src/components/strategies/StrategyCard.tsx` | Modify (lines 231–259) | Move `detachFromBot` call out of `handleDetach` into both answer handlers |
| `frontend/src/components/strategies/HedgePairCard.tsx` | Create | Combined card showing main + hedge strategy with net PnL and per-side controls |
| `frontend/src/pages/TerminalPage.tsx` | Modify (lines 795–824) | Import HedgePairCard, add `renderItems` memo, replace plain `.map()` |
| `frontend/src/pages/StrategiesPage.tsx` | Modify (lines 1–9, 20–47, 156–175) | Same as above; also add `useBots` import for hedge bot ID set |

---

## Task 1: Fix BotBadge — show blacklist question BEFORE calling detachFromBot

**Files:**
- Modify: `frontend/src/components/strategies/StrategyCard.tsx:231-259`

### Context

`handleDetach` currently calls `await detachFromBot(strategyId)` (sets `status='stopped'` in DB), then shows the blacklist confirmation dialog. This means the strategy is already stopped before the user answers. The fix: move the API call out of `handleDetach` and into both answer handlers.

- [ ] **Step 1: Open StrategyCard.tsx and locate BotBadge handlers (lines 231–259)**

The target block (read with `offset=231, limit=29`):

```tsx
const handleDetach = async () => {
  setActing(true)
  try {
    await detachFromBot(strategyId)
    // Show blacklist confirmation step before calling onDetached
    setStep('confirm-blacklist')
  } catch {
    setOpen(false)
    setStep('menu')
  } finally {
    setActing(false)
  }
}

const handleBlacklistYes = async () => {
  try {
    await addBotBlacklist(botId, symbol)
    window.dispatchEvent(new CustomEvent('bot-updated'))
  } catch { /* non-fatal */ }
  setOpen(false)
  setStep('menu')
  onDetached()
}

const handleBlacklistNo = () => {
  setOpen(false)
  setStep('menu')
  onDetached()
}
```

- [ ] **Step 2: Replace the three handlers with the fixed version**

Replace the block above with:

```tsx
const handleDetach = () => {
  // Show blacklist question first — API call happens in the answer handlers
  setStep('confirm-blacklist')
}

const handleBlacklistYes = async () => {
  setActing(true)
  try {
    await addBotBlacklist(botId, symbol)
    window.dispatchEvent(new CustomEvent('bot-updated'))
  } catch { /* non-fatal */ }
  try {
    await detachFromBot(strategyId)
  } catch { /* non-fatal */ }
  setActing(false)
  setOpen(false)
  setStep('menu')
  onDetached()
}

const handleBlacklistNo = async () => {
  setActing(true)
  try {
    await detachFromBot(strategyId)
  } catch { /* non-fatal */ }
  setActing(false)
  setOpen(false)
  setStep('menu')
  onDetached()
}
```

- [ ] **Step 3: Also disable "Нет" button while acting (confirm-blacklist view, lines ~315–320)**

Find this button (it currently lacks `disabled`):

```tsx
<button
  type="button"
  onClick={handleBlacklistNo}
  className="flex-1 py-[5px] text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors hover:bg-white/[.06] text-white/50 border border-white/10"
>
  Нет
</button>
```

Replace with:

```tsx
<button
  type="button"
  onClick={handleBlacklistNo}
  disabled={acting}
  className="flex-1 py-[5px] text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors hover:bg-white/[.06] text-white/50 border border-white/10 disabled:opacity-40"
>
  {acting ? '…' : 'Нет'}
</button>
```

Also update the "Да" button text to show loading state. Find:

```tsx
<button
  type="button"
  onClick={handleBlacklistYes}
  className="flex-1 py-[5px] text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors"
  style={{ background: accent + '22', color: accent, border: `1px solid ${accent}44` }}
>
  Да
</button>
```

Replace with:

```tsx
<button
  type="button"
  onClick={handleBlacklistYes}
  disabled={acting}
  className="flex-1 py-[5px] text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors disabled:opacity-40"
  style={{ background: accent + '22', color: accent, border: `1px solid ${accent}44` }}
>
  {acting ? '…' : 'Да'}
</button>
```

- [ ] **Step 4: Run TypeScript check**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Expected: no errors

- [ ] **Step 5: Commit**

```
cd C:\Users\123\Projects\sis\frontend
git add src/components/strategies/StrategyCard.tsx
git commit -m "fix: show blacklist question before calling detachFromBot API"
```

---

## Task 2: Create HedgePairCard component

**Files:**
- Create: `frontend/src/components/strategies/HedgePairCard.tsx`

### Context

When a hedge bot is actively hedging a strategy, we want to show both the main (long) and hedge (short) strategy on a single combined card. The card should:
- Collapsed header: coin icon + symbol, "hedge" label, both status badges (↑ LIVE / ↓ LIVE), net PnL, chevron
- Expanded body: 2-column layout (main left, hedge right) with per-strategy controls
- Footer: net PnL summary, total margin, ratio

The component is self-contained — it uses `setStrategyStatus` directly for status toggles and calls `onEdit` to open the existing `StrategyModal` for full configuration.

- [ ] **Step 1: Create the file with full implementation**

Create `frontend/src/components/strategies/HedgePairCard.tsx`:

```tsx
import { useState } from 'react'
import { setStrategyStatus } from '../../api/strategies'
import { CoinIcon } from '../common/CoinIcon'
import type { Strategy, ExchangeAccount, ActiveOrder, Position } from '../../types'

interface HedgePairCardProps {
  main: Strategy          // long strategy (has active hedge)
  hedge: Strategy         // short hedge strategy (isHedgeItself=true)
  accounts: ExchangeAccount[]
  orders: ActiveOrder[]
  positions?: Position[]
  tickerPrices?: Map<string, number>
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
  isOpen?: boolean
  onToggleOpen?: () => void
}

// ── helpers ───────────────────────────────────────────────────────────────────

function statusColor(status: string): string {
  if (status === 'active')    return '#5be0a0'
  if (status === 'finishing') return '#fbbf24'
  return '#6b7280'
}

function statusLabel(status: string): string {
  if (status === 'active')    return 'LIVE'
  if (status === 'finishing') return 'DONE'
  return 'STOP'
}

function pnlColor(v: number): string {
  if (v > 0) return '#5be0a0'
  if (v < 0) return '#f87171'
  return '#9ca3af'
}

function fmtPnl(v: number): string {
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toFixed(2)}$`
}

// ── per-strategy panel ────────────────────────────────────────────────────────

function StrategyPanel({
  strategy,
  isMain,
  onEdit,
  onChanged,
}: {
  strategy: Strategy
  isMain: boolean
  onEdit: (s: Strategy, filledCount: number) => void
  onChanged: () => void
}) {
  const [acting, setActing] = useState(false)
  const accent = isMain ? '#818cf8' : '#f59e0b'
  const pnl = strategy.last_pnl ?? 0

  const handleStop = async () => {
    setActing(true)
    try {
      await setStrategyStatus(strategy.id, 'stopped')
      onChanged()
    } catch { /* ignore */ }
    setActing(false)
  }

  const handleStart = async () => {
    setActing(true)
    try {
      await setStrategyStatus(strategy.id, 'active')
      onChanged()
    } catch { /* ignore */ }
    setActing(false)
  }

  return (
    <div className="flex-1 min-w-0 p-3 space-y-2">
      {/* Direction header */}
      <div className="flex items-center gap-1.5">
        <span className="text-[10px] font-bold uppercase tracking-widest" style={{ color: accent }}>
          {isMain ? '↑ Long' : '↓ Short'}
        </span>
        <span
          className="ml-auto text-[10px] font-bold uppercase px-1.5 py-0.5 rounded-[4px]"
          style={{
            color: statusColor(strategy.status),
            background: statusColor(strategy.status) + '22',
          }}
        >
          {statusLabel(strategy.status)}
        </span>
      </div>

      {/* Bot name */}
      {strategy.bot_name && (
        <div className="text-[11px] text-white/50 truncate">
          🤖 <span className="font-semibold" style={{ color: accent }}>{strategy.bot_name}</span>
        </div>
      )}

      {/* PnL */}
      <div className="text-[13px] font-bold tabular-nums" style={{ color: pnlColor(pnl) }}>
        {fmtPnl(pnl)}
      </div>

      {/* Volume */}
      <div className="text-[11px] text-white/40">
        Объём: <span className="text-white/60">{(strategy.volume_usdt ?? 0).toFixed(0)}$</span>
      </div>

      {/* Actions */}
      <div className="flex gap-1.5 pt-1">
        <button
          type="button"
          onClick={() => onEdit(strategy, strategy.active_levels)}
          className="flex-1 py-1 text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors"
          style={{
            border: `1px solid ${accent}44`,
            color: accent,
            background: accent + '11',
          }}
        >
          Настроить
        </button>
        {strategy.status !== 'stopped' ? (
          <button
            type="button"
            disabled={acting}
            onClick={handleStop}
            className="px-2.5 py-1 text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors hover:bg-red-500/10 text-red-400/70 border border-red-500/20 disabled:opacity-40"
          >
            {acting ? '…' : 'Стоп'}
          </button>
        ) : (
          <button
            type="button"
            disabled={acting}
            onClick={handleStart}
            className="px-2.5 py-1 text-[11px] font-semibold rounded-[5px] cursor-pointer transition-colors hover:bg-emerald-500/10 text-emerald-400/70 border border-emerald-500/20 disabled:opacity-40"
          >
            {acting ? '…' : 'Старт'}
          </button>
        )}
      </div>
    </div>
  )
}

// ── main component ────────────────────────────────────────────────────────────

export function HedgePairCard({
  main,
  hedge,
  onEdit,
  onChanged,
  isOpen,
  onToggleOpen,
}: HedgePairCardProps) {
  const netPnl = (main.last_pnl ?? 0) + (hedge.last_pnl ?? 0)
  const symbol = main.symbol
  const totalMargin = (main.volume_usdt ?? 0) + (hedge.volume_usdt ?? 0)

  return (
    <div
      className="rounded-[10px] overflow-hidden"
      style={{
        background: '#0f1119',
        border: '1px solid rgba(255,255,255,.10)',
      }}
    >
      {/* ── Collapsed header ── */}
      <button
        type="button"
        onClick={onToggleOpen}
        className="w-full flex items-center gap-2 px-3 py-2.5 hover:bg-white/[.03] transition-colors text-left"
      >
        {/* Hedge indicator icon */}
        <div
          className="shrink-0 flex items-center justify-center w-[18px] h-[18px] rounded-full text-[10px]"
          style={{ background: '#f59e0b22', border: '1px solid #f59e0b44', color: '#f59e0b' }}
        >
          ⟷
        </div>

        {/* Coin icon + symbol */}
        <CoinIcon symbol={symbol} size={18} />
        <span className="text-[13px] font-bold text-white">{symbol}</span>
        <span className="text-[9px] font-bold uppercase tracking-widest text-white/25 ml-0.5">hedge</span>

        {/* Both status badges */}
        <div className="flex items-center gap-1 ml-1 shrink-0">
          <span
            className="text-[10px] font-bold px-1.5 py-0.5 rounded-[4px]"
            style={{
              color: statusColor(main.status),
              background: statusColor(main.status) + '22',
            }}
          >
            ↑ {statusLabel(main.status)}
          </span>
          <span
            className="text-[10px] font-bold px-1.5 py-0.5 rounded-[4px]"
            style={{
              color: statusColor(hedge.status),
              background: statusColor(hedge.status) + '22',
            }}
          >
            ↓ {statusLabel(hedge.status)}
          </span>
        </div>

        {/* Net PnL */}
        <span
          className="ml-auto text-[13px] font-bold tabular-nums"
          style={{ color: pnlColor(netPnl) }}
        >
          {fmtPnl(netPnl)}
        </span>

        {/* Chevron */}
        <svg
          width={14} height={14} viewBox="0 0 24 24" fill="none"
          stroke="currentColor" strokeWidth={2.5}
          strokeLinecap="round" strokeLinejoin="round"
          className={`shrink-0 text-white/30 transition-transform duration-200 ${isOpen ? 'rotate-180' : ''}`}
          style={{ flex: '0 0 14px' }}
        >
          <path d="M6 9l6 6 6-6"/>
        </svg>
      </button>

      {/* ── Expanded body ── */}
      {isOpen && (
        <div>
          <div style={{ height: 1, background: 'rgba(255,255,255,.07)' }} />

          {/* 2-column layout */}
          <div className="flex" style={{ borderBottom: '1px solid rgba(255,255,255,.07)' }}>
            <StrategyPanel
              strategy={main}
              isMain={true}
              onEdit={onEdit}
              onChanged={onChanged}
            />
            <div style={{ width: 1, background: 'rgba(255,255,255,.07)', flexShrink: 0 }} />
            <StrategyPanel
              strategy={hedge}
              isMain={false}
              onEdit={onEdit}
              onChanged={onChanged}
            />
          </div>

          {/* Footer */}
          <div
            className="px-3 py-2 flex items-center gap-4 text-[11px] flex-wrap"
            style={{ color: 'rgba(255,255,255,.35)' }}
          >
            <span>
              Нетто P&L:{' '}
              <span className="font-bold" style={{ color: pnlColor(netPnl) }}>
                {fmtPnl(netPnl)}
              </span>
            </span>
            <span>
              Маржа:{' '}
              <span className="text-white/55">{totalMargin.toFixed(0)}$</span>
            </span>
            {main.volume_usdt > 0 && hedge.volume_usdt > 0 && (
              <span>
                Ratio: 1:
                <span className="text-white/55">
                  {(hedge.volume_usdt / main.volume_usdt).toFixed(2)}
                </span>
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Run TypeScript check**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Expected: no errors (all types come from existing `../../types` and `../../api/strategies`)

- [ ] **Step 3: Commit**

```
cd C:\Users\123\Projects\sis\frontend
git add src/components/strategies/HedgePairCard.tsx
git commit -m "feat: add HedgePairCard component for hedge pair display"
```

---

## Task 3: Wire HedgePairCard into TerminalPage

**Files:**
- Modify: `frontend/src/pages/TerminalPage.tsx`

### Context

TerminalPage already has `hedgeInfoMap` (Map<strategyId, {hasActiveHedge, isHedgeItself}>). We need to:
1. Import `HedgePairCard`
2. Add a `renderItems` memo that groups strategies into pairs and singles
3. Replace the `sortStrategies(visibleStrategies).map(s => ...)` block (lines ~795–824) with a `renderItems.map(item => ...)`

- [ ] **Step 1: Add import for HedgePairCard**

Find the existing StrategyCard import:

```tsx
import { StrategyCard } from '../components/strategies/StrategyCard'
```

Replace with:

```tsx
import { StrategyCard } from '../components/strategies/StrategyCard'
import { HedgePairCard } from '../components/strategies/HedgePairCard'
```

- [ ] **Step 2: Add renderItems memo after hedgeInfoMap**

Find the closing `return map` + closing brace of `hedgeInfoMap` useMemo:

```tsx
    return map
  }, [visibleStrategies, strategies, hedgeBotIds])

  return (
```

Replace with:

```tsx
    return map
  }, [visibleStrategies, strategies, hedgeBotIds])

  // Group strategies into hedge pairs and standalone singles
  const renderItems = useMemo(() => {
    const sorted = sortStrategies(visibleStrategies)
    const usedIds = new Set<string>()
    const items: Array<
      | { type: 'single'; strategy: typeof sorted[0] }
      | { type: 'pair'; main: typeof sorted[0]; hedge: typeof sorted[0] }
    > = []

    for (const s of sorted) {
      if (usedIds.has(s.id)) continue
      const info = hedgeInfoMap.get(s.id)
      if (info?.hasActiveHedge) {
        const partner = sorted.find(h =>
          !usedIds.has(h.id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction !== s.direction &&
          hedgeInfoMap.get(h.id)?.isHedgeItself,
        )
        if (partner) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          items.push({ type: 'pair', main: s, hedge: partner })
          continue
        }
      }
      usedIds.add(s.id)
      items.push({ type: 'single', strategy: s })
    }
    return items
  }, [visibleStrategies, hedgeInfoMap])

  return (
```

- [ ] **Step 3: Replace the strategy map in the JSX**

Find (the existing render loop):

```tsx
        {sortStrategies(visibleStrategies).map(s => (
          <div key={s.id} className="origin-top-left scale-[0.96]">
            <StrategyCard
              strategy={s}
              accounts={accounts}
              orders={orders}
              positions={positions}
              tickerPrices={tickerPrices}
              onEdit={s => { setEditTarget(s); setModalOpen(true) }}
              onChanged={load}
              selected={s.id === selectedId}
              onSelect={handleSelect}
              isOpen={s.id === expandedId}
              liveSignal={signalStates[s.id]}
              hedgeWatcherCount={countHedgeWatchers(s, hedgeBots)}
              hasActiveHedge={hedgeInfoMap.get(s.id)?.hasActiveHedge}
              isHedgeItself={hedgeInfoMap.get(s.id)?.isHedgeItself}
              onToggleOpen={() => {
                const isExpanding = expandedId !== s.id
                const newExp = isExpanding ? s.id : null
                setExpandedId(newExp)
                if (isExpanding) {
                  setSelectedId(s.id)
                  localStorage.setItem('t_sel', s.id)
                  onSymbolChange(s.symbol)
                }
              }}
            />
          </div>
        ))}
```

Replace with:

```tsx
        {renderItems.map(item => {
          if (item.type === 'pair') {
            return (
              <div key={`pair-${item.main.id}`} className="origin-top-left scale-[0.96]">
                <HedgePairCard
                  main={item.main}
                  hedge={item.hedge}
                  accounts={accounts}
                  orders={orders}
                  positions={positions}
                  tickerPrices={tickerPrices}
                  onEdit={s => { setEditTarget(s); setModalOpen(true) }}
                  onChanged={load}
                  isOpen={expandedId === item.main.id || expandedId === item.hedge.id}
                  onToggleOpen={() => {
                    const pairKey = item.main.id
                    const isExpanding = expandedId !== pairKey
                    setExpandedId(isExpanding ? pairKey : null)
                    if (isExpanding) {
                      setSelectedId(item.main.id)
                      localStorage.setItem('t_sel', item.main.id)
                      onSymbolChange(item.main.symbol)
                    }
                  }}
                />
              </div>
            )
          }
          const s = item.strategy
          return (
            <div key={s.id} className="origin-top-left scale-[0.96]">
              <StrategyCard
                strategy={s}
                accounts={accounts}
                orders={orders}
                positions={positions}
                tickerPrices={tickerPrices}
                onEdit={s => { setEditTarget(s); setModalOpen(true) }}
                onChanged={load}
                selected={s.id === selectedId}
                onSelect={handleSelect}
                isOpen={s.id === expandedId}
                liveSignal={signalStates[s.id]}
                hedgeWatcherCount={countHedgeWatchers(s, hedgeBots)}
                hasActiveHedge={hedgeInfoMap.get(s.id)?.hasActiveHedge}
                isHedgeItself={hedgeInfoMap.get(s.id)?.isHedgeItself}
                onToggleOpen={() => {
                  const isExpanding = expandedId !== s.id
                  const newExp = isExpanding ? s.id : null
                  setExpandedId(newExp)
                  if (isExpanding) {
                    setSelectedId(s.id)
                    localStorage.setItem('t_sel', s.id)
                    onSymbolChange(s.symbol)
                  }
                }}
              />
            </div>
          )
        })}
```

- [ ] **Step 4: Run TypeScript check**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Expected: no errors

- [ ] **Step 5: Commit**

```
cd C:\Users\123\Projects\sis\frontend
git add src/pages/TerminalPage.tsx
git commit -m "feat: render HedgePairCard for hedge pairs in TerminalPage"
```

---

## Task 4: Wire HedgePairCard into StrategiesPage

**Files:**
- Modify: `frontend/src/pages/StrategiesPage.tsx`

### Context

StrategiesPage currently renders strategies as a flat list without any hedge grouping. It doesn't load bots, so we need to add `useBots` to identify hedge bot IDs. Then add the same grouping logic as Task 3.

- [ ] **Step 1: Add imports**

Find the existing imports block at the top of StrategiesPage.tsx:

```tsx
import { useState, useEffect, useCallback } from 'react'
import { listStrategies } from '../api/strategies'
import { listAccounts } from '../api/accounts'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import { MatrixDebugOverlay } from '../components/strategies/MatrixDebugOverlay'
import { useSelectedAccount } from '../contexts/AccountContext'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import type { Strategy, ExchangeAccount } from '../types'
```

Replace with:

```tsx
import { useState, useEffect, useCallback, useMemo } from 'react'
import { listStrategies } from '../api/strategies'
import { listAccounts } from '../api/accounts'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { HedgePairCard } from '../components/strategies/HedgePairCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import { MatrixDebugOverlay } from '../components/strategies/MatrixDebugOverlay'
import { useSelectedAccount } from '../contexts/AccountContext'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useBots } from '../features/bots/api'
import type { Strategy, ExchangeAccount } from '../types'
```

- [ ] **Step 2: Add useBots call inside StrategiesPage function body**

Find the existing state declarations near the top of `StrategiesPage()`:

```tsx
  const { selectedAccountId } = useSelectedAccount()
  const { positions, orders } = usePositionsWs(selectedAccountId || null)

  const [strategies, setStrategies] = useState<Strategy[]>([])
```

Replace with:

```tsx
  const { selectedAccountId } = useSelectedAccount()
  const { positions, orders } = usePositionsWs(selectedAccountId || null)
  const { mine: hedgeBots } = useBots()

  const [strategies, setStrategies] = useState<Strategy[]>([])
```

- [ ] **Step 3: Add hedgeBotIds + hedgeInfoMap + renderItems memos**

Find the `load` callback and the `useEffect` that calls it:

```tsx
  const load = useCallback(async () => {
```

Insert the three memos just before the `return (` statement. Find:

```tsx
  function handleSelect(s: Strategy) {
```

Insert before it:

```tsx
  const hedgeBotIds = useMemo(
    () => new Set(hedgeBots.filter(b => b.strategyConfig.bot_kind === 'hedge').map(b => b.id)),
    [hedgeBots],
  )

  const hedgeInfoMap = useMemo(() => {
    const map = new Map<string, { hasActiveHedge: boolean; isHedgeItself: boolean }>()
    for (const s of strategies) {
      const isHedgeItself = !!s.bot_id && hedgeBotIds.has(s.bot_id)
      let hasActiveHedge = false
      if (!isHedgeItself) {
        const oppDir = s.direction === 'long' ? 'short' : 'long'
        hasActiveHedge = strategies.some(h =>
          !!h.bot_id &&
          hedgeBotIds.has(h.bot_id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction === oppDir &&
          (h.status === 'active' || h.status === 'finishing'),
        )
      }
      map.set(s.id, { hasActiveHedge, isHedgeItself })
    }
    return map
  }, [strategies, hedgeBotIds])

  const renderItems = useMemo(() => {
    const sorted = sortStrategies(strategies)
    const usedIds = new Set<string>()
    const items: Array<
      | { type: 'single'; strategy: Strategy }
      | { type: 'pair'; main: Strategy; hedge: Strategy }
    > = []

    for (const s of sorted) {
      if (usedIds.has(s.id)) continue
      const info = hedgeInfoMap.get(s.id)
      if (info?.hasActiveHedge) {
        const partner = sorted.find(h =>
          !usedIds.has(h.id) &&
          h.symbol === s.symbol &&
          h.account_id === s.account_id &&
          h.direction !== s.direction &&
          hedgeInfoMap.get(h.id)?.isHedgeItself,
        )
        if (partner) {
          usedIds.add(s.id)
          usedIds.add(partner.id)
          items.push({ type: 'pair', main: s, hedge: partner })
          continue
        }
      }
      usedIds.add(s.id)
      items.push({ type: 'single', strategy: s })
    }
    return items
  }, [strategies, hedgeInfoMap])

  function handleSelect(s: Strategy) {
```

- [ ] **Step 4: Replace the strategy list render in JSX**

Find:

```tsx
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {sortStrategies(strategies).map(s => (
              <StrategyCard
                key={s.id}
                strategy={s}
                accounts={accounts}
                orders={orders}
                positions={positions}
                onEdit={openEdit}
                onChanged={load}
                selected={s.id === selectedId}
                onSelect={handleSelect}
                isOpen={s.id === expandedId}
                onToggleOpen={() => {
                  const isExpanding = expandedId !== s.id
                  setExpandedId(isExpanding ? s.id : null)
                  if (isExpanding) setSelectedId(s.id)
                }}
              />
            ))}
          </ul>
```

Replace with:

```tsx
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {renderItems.map(item => {
              if (item.type === 'pair') {
                return (
                  <li key={`pair-${item.main.id}`}>
                    <HedgePairCard
                      main={item.main}
                      hedge={item.hedge}
                      accounts={accounts}
                      orders={orders}
                      positions={positions}
                      onEdit={openEdit}
                      onChanged={load}
                      isOpen={expandedId === item.main.id || expandedId === item.hedge.id}
                      onToggleOpen={() => {
                        const pairKey = item.main.id
                        const isExpanding = expandedId !== pairKey
                        setExpandedId(isExpanding ? pairKey : null)
                        if (isExpanding) setSelectedId(item.main.id)
                      }}
                    />
                  </li>
                )
              }
              const s = item.strategy
              return (
                <StrategyCard
                  key={s.id}
                  strategy={s}
                  accounts={accounts}
                  orders={orders}
                  positions={positions}
                  onEdit={openEdit}
                  onChanged={load}
                  selected={s.id === selectedId}
                  onSelect={handleSelect}
                  isOpen={s.id === expandedId}
                  onToggleOpen={() => {
                    const isExpanding = expandedId !== s.id
                    setExpandedId(isExpanding ? s.id : null)
                    if (isExpanding) setSelectedId(s.id)
                  }}
                />
              )
            })}
          </ul>
```

- [ ] **Step 5: Run TypeScript check**

```
cd C:\Users\123\Projects\sis\frontend
npx tsc --noEmit
```

Expected: no errors

- [ ] **Step 6: Build to verify no runtime errors**

```
cd C:\Users\123\Projects\sis\frontend
npm run build
```

Expected: `✓ built in X.XXs` (pre-existing chunk size warning is unrelated)

- [ ] **Step 7: Commit**

```
cd C:\Users\123\Projects\sis\frontend
git add src/pages/StrategiesPage.tsx
git commit -m "feat: render HedgePairCard for hedge pairs in StrategiesPage"
```

---

## Self-Review Checklist

### Spec coverage

| Requirement | Covered by |
|---|---|
| Show blacklist question BEFORE calling detach API | Task 1 |
| Both Yes/No answers call detachFromBot | Task 1 (handleBlacklistYes + handleBlacklistNo) |
| Combined card for active hedge pairs | Task 2 (HedgePairCard) |
| Both directions (↑ Long + ↓ Short) on one card | Task 2 header badges |
| Combined net PnL in header | Task 2 netPnl in header |
| Separate configure buttons for each strategy | Task 2 StrategyPanel "Настроить" button |
| Stop/Start per strategy | Task 2 StrategyPanel stop/start buttons |
| Footer: net PnL, total margin, ratio | Task 2 expanded footer |
| Wire into TerminalPage | Task 3 |
| Wire into StrategiesPage | Task 4 |

### Type consistency

- `HedgePairCard` props use `Strategy` from `../../types` — consistent throughout
- `onEdit: (s: Strategy, filledCount: number) => void` matches `StrategyCard.Props.onEdit` signature and `openEdit` in StrategiesPage
- `setStrategyStatus` is called with `(strategy.id, 'stopped' | 'active')` — matches `api/strategies` signature
- `hedgeInfoMap` key is `strategy.id: string` — consistent between TerminalPage (where it was originally) and StrategiesPage (where it's added)
- `renderItems` uses same `Strategy` type in both pages

### No placeholder scan

- All code blocks are complete
- No "TBD" or "TODO" in any step
- All imports are concrete paths
- All method calls use actual function signatures
