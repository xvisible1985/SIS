# Strategies Page — Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** React page `/strategies` with expandable strategy cards, a 3-tab create/edit modal (Entry / Averaging / Exit), and template load/save functionality.

**Architecture:** StrategiesPage renders StrategyCard list. Each card shows a collapsed header (symbol, direction, grid ratio, volume, P&L, status) and an expanded body (signal mini-cards, levels + TP/SL, control buttons). StrategyModal has 3 tabs and a TemplateSelector bar at the top. Follows exact same patterns as AccountsPage (useState for card state, async fetch on expand, inline button handlers).

**Tech Stack:** React 18, TypeScript, Tailwind CSS, Vite. Working dir: `c:\Users\123\Projects\sis\frontend`.

---

### Task 1: Types + API clients

**Files:**
- Modify: `frontend/src/types.ts`
- Create: `frontend/src/api/strategies.ts`
- Create: `frontend/src/api/strategyTemplates.ts`

- [ ] **Step 1: Add types to frontend/src/types.ts**

Append to the end of `frontend/src/types.ts`:

```typescript
// Strategies
export interface SignalConfig {
  name: string
  params: Record<string, number>
}

export interface GridStep {
  price_move_pct: number
  lots: number
}

export interface Strategy {
  id: string
  account_id: string
  symbol: string
  category: string
  direction: 'long' | 'short' | 'both'
  status: 'active' | 'finishing' | 'stopped'
  grid_levels: number
  grid_active: number
  grid_step_pct: number
  grid_size_usdt: number
  tp_mode: 'total' | 'per_level'
  tp_pct: number
  sl_type: 'conditional' | 'programmatic'
  sl_pct: number
  signal_filter: boolean
  leverage: number
  margin_type: 'isolated' | 'cross'
  hedge_mode: boolean
  strategy_type: 'grid' | 'dca' | 'manual'
  signal_configs: SignalConfig[]
  steps: GridStep[] | null
  trailing_stop_enabled: boolean
  trailing_activation_pct: number | null
  trailing_callback_pct: number | null
  created_at: string
  updated_at: string
  // Computed by server
  volume_usdt: number
  active_levels: number
  last_pnl: number
}

export interface StrategyLevel {
  level_idx: number
  side: string
  target_price: number
  size_usdt: number
  status: 'pending' | 'placed' | 'filled' | 'cancelled'
  filled_price: number
}

export interface StrategyState {
  cycle_num: number
  start_price: number
  tp_order_id: string
  sl_order_id: string
  started_at: string
  levels: StrategyLevel[]
  volume_usdt: number
  avg_entry: number
}

export interface StrategyTemplate {
  id: string
  name: string
  config: Partial<Strategy>
  created_at: string
}

export interface StrategyFormData {
  account_id: string
  symbol: string
  category: string
  direction: 'long' | 'short'
  strategy_type: 'grid' | 'dca' | 'manual'
  leverage: number
  grid_size_usdt: number
  margin_type: 'isolated' | 'cross'
  hedge_mode: boolean
  steps: GridStep[]
  signal_configs: SignalConfig[]
  tp_pct: number
  tp_mode: 'total' | 'per_level'
  sl_pct: number
  sl_type: 'conditional' | 'programmatic'
  trailing_stop_enabled: boolean
  trailing_activation_pct: number
  trailing_callback_pct: number
}
```

- [ ] **Step 2: Create frontend/src/api/strategies.ts**

```typescript
import { apiClient } from './client'
import type { Strategy, StrategyState, StrategyFormData } from '../types'

export async function listStrategies(): Promise<Strategy[]> {
  const res = await apiClient.get<Strategy[]>('/strategies')
  return res.data
}

export async function createStrategy(data: StrategyFormData): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/strategies', data)
  return res.data
}

export async function updateStrategy(id: string, data: StrategyFormData): Promise<void> {
  await apiClient.put(`/strategies/${id}`, data)
}

export async function setStrategyStatus(
  id: string,
  status: 'active' | 'finishing' | 'stopped'
): Promise<void> {
  await apiClient.post(`/strategies/${id}/status`, { status })
}

export async function deleteStrategy(id: string): Promise<void> {
  await apiClient.delete(`/strategies/${id}`)
}

export async function getStrategyState(id: string): Promise<StrategyState> {
  const res = await apiClient.get<StrategyState>(`/strategies/${id}/state`)
  return res.data
}
```

- [ ] **Step 3: Create frontend/src/api/strategyTemplates.ts**

```typescript
import { apiClient } from './client'
import type { StrategyTemplate, StrategyFormData } from '../types'

export async function listTemplates(): Promise<StrategyTemplate[]> {
  const res = await apiClient.get<StrategyTemplate[]>('/strategy-templates')
  return res.data
}

export async function createTemplate(name: string, config: StrategyFormData): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/strategy-templates', { name, config })
  return res.data
}

export async function deleteTemplate(id: string): Promise<void> {
  await apiClient.delete(`/strategy-templates/${id}`)
}
```

- [ ] **Step 4: Build to verify types compile**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -20`

Expected: no errors (or only pre-existing errors unrelated to new types)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/api/strategies.ts frontend/src/api/strategyTemplates.ts
git commit -m "feat: strategy types and API client functions"
```

---

### Task 2: SignalMiniCard + TemplateSelector

**Files:**
- Create: `frontend/src/components/strategies/SignalMiniCard.tsx`
- Create: `frontend/src/components/strategies/TemplateSelector.tsx`

- [ ] **Step 1: Create frontend/src/components/strategies/SignalMiniCard.tsx**

```tsx
import type { SignalConfig } from '../../types'

const SIGNAL_CATEGORY: Record<string, { color: string; icon: string }> = {
  RSI:           { color: 'text-purple-400', icon: '〜' },
  MACD:          { color: 'text-purple-400', icon: '⬆' },
  EMA:           { color: 'text-blue-400',   icon: '—' },
  SMA:           { color: 'text-blue-400',   icon: '—' },
  Bollinger:     { color: 'text-yellow-400', icon: '↔' },
  Stochastic:    { color: 'text-purple-400', icon: '〜' },
  ADX:           { color: 'text-blue-400',   icon: '▲' },
  CCI:           { color: 'text-purple-400', icon: '〜' },
  ATR:           { color: 'text-yellow-400', icon: '↕' },
  OBV:           { color: 'text-cyan-400',   icon: '↑' },
  'Williams %R': { color: 'text-purple-400', icon: '%' },
  MFI:           { color: 'text-cyan-400',   icon: '$' },
  SAR:           { color: 'text-blue-400',   icon: '●' },
  Ichimoku:      { color: 'text-blue-400',   icon: '≡' },
  Supertrend:    { color: 'text-blue-400',   icon: '⬆' },
  'Volume Spike':{ color: 'text-orange-400', icon: '⚡' },
  'ATR Breakout':{ color: 'text-indigo-400', icon: '↕' },
  Divergence:    { color: 'text-green-400',  icon: '⇌' },
}

export function SignalMiniCard({ signal }: { signal: SignalConfig }) {
  const cat = SIGNAL_CATEGORY[signal.name] ?? { color: 'text-gray-400', icon: '◆' }
  const paramStr = Object.entries(signal.params ?? {})
    .map(([, v]) => v)
    .join(', ')

  return (
    <div className="relative bg-gray-800/60 border border-gray-700 rounded-lg px-3 pt-6 pb-2 min-w-[110px] flex-1">
      <div className="absolute top-1.5 left-1.5">
        <span className="bg-gray-700 text-gray-400 text-[9px] px-1.5 py-0.5 rounded">Норма</span>
      </div>
      <div className="flex items-center gap-1.5 mb-1">
        <span className={`text-sm ${cat.color}`}>{cat.icon}</span>
        <span className="text-gray-300 text-[11px] font-semibold truncate">{signal.name}</span>
        {paramStr && <span className="text-gray-600 text-[9px]">({paramStr})</span>}
      </div>
      <div className="text-gray-100 text-sm font-bold font-mono">—</div>
    </div>
  )
}
```

- [ ] **Step 2: Create frontend/src/components/strategies/TemplateSelector.tsx**

```tsx
import { useState, useEffect, useRef } from 'react'
import { listTemplates, createTemplate, deleteTemplate } from '../../api/strategyTemplates'
import type { StrategyTemplate, StrategyFormData } from '../../types'

interface Props {
  formData: StrategyFormData
  onLoad: (config: Partial<StrategyFormData>) => void
}

export function TemplateSelector({ formData, onLoad }: Props) {
  const [templates, setTemplates] = useState<StrategyTemplate[]>([])
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveName, setSaveName] = useState('')
  const [showSaveRow, setShowSaveRow] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    listTemplates().then(setTemplates).catch(() => {})
  }, [])

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  function handleLoad(tpl: StrategyTemplate) {
    setSelected(tpl.id)
    setOpen(false)
    onLoad(tpl.config as Partial<StrategyFormData>)
  }

  async function handleDelete(id: string, e: React.MouseEvent) {
    e.stopPropagation()
    await deleteTemplate(id)
    setTemplates(prev => prev.filter(t => t.id !== id))
    if (selected === id) setSelected(null)
  }

  async function handleSave() {
    if (!saveName.trim()) return
    setSaving(true)
    try {
      await createTemplate(saveName.trim(), formData)
      const fresh = await listTemplates()
      setTemplates(fresh)
      setShowSaveRow(false)
      setSaveName('')
    } finally {
      setSaving(false)
    }
  }

  const selectedName = templates.find(t => t.id === selected)?.name

  return (
    <div className="flex items-center gap-2 px-4 py-2 bg-gray-900/60 border-b border-gray-700">
      <span className="text-gray-500 text-[10px] shrink-0">Шаблон:</span>
      <div className="relative flex-1" ref={ref}>
        <button
          onClick={() => setOpen(v => !v)}
          className="w-full flex items-center justify-between bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-[11px] text-left"
        >
          <span className={selectedName ? 'text-gray-100' : 'text-gray-500'}>
            {selectedName ?? 'Без шаблона'}
          </span>
          <span className="text-gray-500 ml-2">▾</span>
        </button>
        {open && (
          <div className="absolute top-full mt-1 left-0 right-0 bg-gray-800 border border-gray-600 rounded-lg z-50 overflow-hidden shadow-xl">
            {templates.length === 0 ? (
              <div className="px-3 py-2 text-[11px] text-gray-500">Нет сохранённых шаблонов</div>
            ) : templates.map(tpl => (
              <div
                key={tpl.id}
                onClick={() => handleLoad(tpl)}
                className="flex items-center justify-between px-3 py-2 hover:bg-gray-700 cursor-pointer border-b border-gray-700/50 last:border-0"
              >
                <div>
                  <div className="text-[11px] text-gray-100">{tpl.name}</div>
                </div>
                <button
                  onClick={e => handleDelete(tpl.id, e)}
                  className="text-red-400 text-[10px] px-1.5 hover:text-red-300 opacity-60 hover:opacity-100"
                >
                  🗑
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
      {showSaveRow ? (
        <div className="flex items-center gap-2">
          <input
            value={saveName}
            onChange={e => setSaveName(e.target.value)}
            placeholder="Имя шаблона"
            className="bg-gray-800 border border-gray-600 rounded px-2 py-1 text-[11px] text-gray-100 w-36"
            onKeyDown={e => e.key === 'Enter' && handleSave()}
          />
          <button
            onClick={handleSave}
            disabled={saving}
            className="bg-green-900/50 text-green-400 border border-green-800 rounded px-2 py-1 text-[10px] shrink-0 disabled:opacity-50"
          >
            {saving ? '…' : 'Сохранить'}
          </button>
          <button onClick={() => setShowSaveRow(false)} className="text-gray-500 text-lg leading-none">✕</button>
        </div>
      ) : (
        <button
          onClick={() => setShowSaveRow(true)}
          className="shrink-0 bg-blue-900/40 text-blue-400 border border-blue-800 rounded px-2 py-1 text-[10px]"
        >
          💾 Сохранить
        </button>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Build to verify**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -20`

Expected: no new errors

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/strategies/
git commit -m "feat: SignalMiniCard and TemplateSelector components"
```

---

### Task 3: StrategyModal

**Files:**
- Create: `frontend/src/components/strategies/StrategyModal.tsx`

- [ ] **Step 1: Create frontend/src/components/strategies/StrategyModal.tsx**

```tsx
import { useState, useEffect } from 'react'
import { TemplateSelector } from './TemplateSelector'
import { createStrategy, updateStrategy } from '../../api/strategies'
import { listAccounts } from '../../api/accounts'
import type { Strategy, StrategyFormData, GridStep, SignalConfig, ExchangeAccount } from '../../types'

const AVAILABLE_SIGNALS = [
  'RSI', 'MACD', 'EMA', 'SMA', 'Bollinger', 'Stochastic', 'ADX',
  'CCI', 'ATR', 'OBV', 'Williams %R', 'MFI', 'SAR', 'Ichimoku',
  'Supertrend', 'Volume Spike', 'ATR Breakout', 'Divergence',
]

function defaultForm(): StrategyFormData {
  return {
    account_id: '',
    symbol: '',
    category: 'linear',
    direction: 'long',
    strategy_type: 'grid',
    leverage: 5,
    grid_size_usdt: 100,
    margin_type: 'isolated',
    hedge_mode: false,
    steps: [
      { price_move_pct: 1.0, lots: 1 },
      { price_move_pct: 1.5, lots: 2 },
      { price_move_pct: 2.0, lots: 2 },
    ],
    signal_configs: [],
    tp_pct: 2.0,
    tp_mode: 'total',
    sl_pct: 5.0,
    sl_type: 'conditional',
    trailing_stop_enabled: false,
    trailing_activation_pct: 1.5,
    trailing_callback_pct: 0.5,
  }
}

function strategyToForm(s: Strategy): StrategyFormData {
  return {
    account_id: s.account_id,
    symbol: s.symbol,
    category: s.category,
    direction: s.direction === 'short' ? 'short' : 'long',
    strategy_type: s.strategy_type,
    leverage: s.leverage,
    grid_size_usdt: s.grid_size_usdt,
    margin_type: s.margin_type,
    hedge_mode: s.hedge_mode,
    steps: s.steps ?? [{ price_move_pct: s.grid_step_pct, lots: 1 }],
    signal_configs: s.signal_configs ?? [],
    tp_pct: s.tp_pct,
    tp_mode: s.tp_mode,
    sl_pct: s.sl_pct,
    sl_type: s.sl_type,
    trailing_stop_enabled: s.trailing_stop_enabled,
    trailing_activation_pct: s.trailing_activation_pct ?? 1.5,
    trailing_callback_pct: s.trailing_callback_pct ?? 0.5,
  }
}

interface Props {
  strategy?: Strategy
  onClose: () => void
  onSaved: () => void
}

export function StrategyModal({ strategy, onClose, onSaved }: Props) {
  const [tab, setTab] = useState(0)
  const [form, setForm] = useState<StrategyFormData>(
    strategy ? strategyToForm(strategy) : defaultForm()
  )
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [signalPickerOpen, setSignalPickerOpen] = useState(false)

  useEffect(() => {
    listAccounts().then(setAccounts).catch(() => {})
  }, [])

  function patch(partial: Partial<StrategyFormData>) {
    setForm(f => ({ ...f, ...partial }))
  }

  function addStep() {
    patch({ steps: [...form.steps, { price_move_pct: 1.0, lots: 1 }] })
  }

  function removeStep(i: number) {
    patch({ steps: form.steps.filter((_, idx) => idx !== i) })
  }

  function updateStep(i: number, field: keyof GridStep, value: number) {
    const steps = form.steps.map((s, idx) =>
      idx === i ? { ...s, [field]: value } : s
    )
    patch({ steps })
  }

  function addSignal(name: string) {
    if (form.signal_configs.find(s => s.name === name)) return
    patch({ signal_configs: [...form.signal_configs, { name, params: {} }] })
    setSignalPickerOpen(false)
  }

  function removeSignal(name: string) {
    patch({ signal_configs: form.signal_configs.filter(s => s.name !== name) })
  }

  function loadTemplate(config: Partial<StrategyFormData>) {
    setForm(f => ({ ...f, ...config }))
  }

  async function handleSubmit() {
    if (!form.account_id || !form.symbol) {
      setError('Заполните символ и аккаунт')
      return
    }
    setSaving(true)
    setError(null)
    try {
      if (strategy) {
        await updateStrategy(strategy.id, form)
      } else {
        await createStrategy(form)
      }
      onSaved()
    } catch (e: any) {
      setError(e?.response?.data?.error ?? 'Ошибка сохранения')
    } finally {
      setSaving(false)
    }
  }

  const tabLabels = ['1. Вход', '2. Усреднение', '3. Выход']
  const inputCls = 'w-full bg-gray-800 border border-gray-700 rounded-md px-3 py-1.5 text-sm text-gray-100 focus:outline-none focus:border-blue-500'
  const labelCls = 'block text-xs text-gray-500 mb-1'

  function Toggle({ options, value, onChange }: {
    options: { label: string; value: string }[]
    value: string
    onChange: (v: string) => void
  }) {
    return (
      <div className="flex gap-1">
        {options.map(o => (
          <button
            key={o.value}
            onClick={() => onChange(o.value)}
            className={`flex-1 text-center rounded-md py-1.5 text-xs transition-colors ${
              value === o.value
                ? 'bg-blue-700 text-white'
                : 'bg-gray-800 text-gray-400 hover:bg-gray-700'
            }`}
          >
            {o.label}
          </button>
        ))}
      </div>
    )
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="bg-gray-900 border border-gray-700 rounded-xl w-[500px] shadow-2xl flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-gray-700">
          <span className="text-gray-100 font-semibold">
            {strategy ? 'Редактировать стратегию' : 'Новая стратегия'}
          </span>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 text-xl leading-none">✕</button>
        </div>

        {/* Template bar */}
        <TemplateSelector formData={form} onLoad={loadTemplate} />

        {/* Tabs */}
        <div className="flex border-b border-gray-700">
          {tabLabels.map((label, i) => (
            <button
              key={i}
              onClick={() => setTab(i)}
              className={`flex-1 py-2.5 text-xs font-medium border-b-2 transition-colors ${
                tab === i
                  ? 'border-blue-500 text-blue-400'
                  : 'border-transparent text-gray-500 hover:text-gray-300'
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">

          {/* Tab 1: Entry */}
          {tab === 0 && (
            <>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>Символ</label>
                  <input
                    value={form.symbol}
                    onChange={e => patch({ symbol: e.target.value.toUpperCase() })}
                    placeholder="BTCUSDT"
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>Аккаунт</label>
                  <select
                    value={form.account_id}
                    onChange={e => patch({ account_id: e.target.value })}
                    className={inputCls}
                  >
                    <option value="">Выбрать…</option>
                    {accounts.map(a => (
                      <option key={a.id} value={a.id}>{a.label}</option>
                    ))}
                  </select>
                </div>
              </div>

              <div>
                <label className={labelCls}>Направление</label>
                <Toggle
                  options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }]}
                  value={form.direction}
                  onChange={v => patch({ direction: v as 'long' | 'short' })}
                />
              </div>

              <div>
                <label className={labelCls}>Тип стратегии</label>
                <Toggle
                  options={[
                    { label: 'Grid', value: 'grid' },
                    { label: 'DCA', value: 'dca' },
                    { label: 'Manual', value: 'manual' },
                  ]}
                  value={form.strategy_type}
                  onChange={v => patch({ strategy_type: v as StrategyFormData['strategy_type'] })}
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>Плечо (×)</label>
                  <input
                    type="number" min={1} max={100}
                    value={form.leverage}
                    onChange={e => patch({ leverage: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>Объём 1 лота (USDT)</label>
                  <input
                    type="number" min={1}
                    value={form.grid_size_usdt}
                    onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
              </div>

              <div>
                <label className={labelCls}>Тип маржи</label>
                <Toggle
                  options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
                  value={form.margin_type}
                  onChange={v => patch({ margin_type: v as 'isolated' | 'cross' })}
                />
              </div>

              <div>
                <label className={labelCls}>Хедж режим</label>
                <Toggle
                  options={[{ label: 'Нет', value: 'false' }, { label: 'Да (Hedge)', value: 'true' }]}
                  value={String(form.hedge_mode)}
                  onChange={v => patch({ hedge_mode: v === 'true' })}
                />
              </div>
            </>
          )}

          {/* Tab 2: Averaging */}
          {tab === 1 && (
            <>
              <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800">
                Шаги усреднения
              </div>
              <div>
                <div className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 text-[10px] text-gray-600 mb-1 px-0.5">
                  <div className="text-center">#</div>
                  <div className="text-center">Движение %</div>
                  <div className="text-center">Лотов</div>
                  <div className="text-center">USDT</div>
                  <div />
                </div>
                {form.steps.map((step, i) => (
                  <div
                    key={i}
                    className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 items-center py-1.5 border-b border-gray-800/60 last:border-0"
                  >
                    <div className="text-center text-[10px] text-gray-600">{i + 1}</div>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={step.price_move_pct}
                      onChange={e => updateStep(i, 'price_move_pct', Number(e.target.value))}
                      className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                    />
                    <input
                      type="number" step="1" min="1"
                      value={step.lots}
                      onChange={e => updateStep(i, 'lots', Number(e.target.value))}
                      className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                    />
                    <div className="text-center text-[11px] text-gray-500">
                      {(step.lots * form.grid_size_usdt).toFixed(0)}
                    </div>
                    <button
                      onClick={() => removeStep(i)}
                      className="text-red-500 hover:text-red-400 text-center text-sm"
                    >
                      ✕
                    </button>
                  </div>
                ))}
                <button
                  onClick={addStep}
                  className="mt-2 w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
                >
                  + Добавить шаг
                </button>
              </div>

              <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800 mt-2">
                Сигналы для усреднения
              </div>
              <div className="flex flex-wrap gap-2">
                {form.signal_configs.map(sc => (
                  <div
                    key={sc.name}
                    className="flex items-center gap-1.5 bg-blue-900/20 border border-blue-800/50 rounded-md px-2.5 py-1"
                  >
                    <span className="text-blue-300 text-[11px]">{sc.name}</span>
                    <button
                      onClick={() => removeSignal(sc.name)}
                      className="text-gray-600 hover:text-gray-400 text-xs"
                    >
                      ✕
                    </button>
                  </div>
                ))}
                <div className="relative">
                  <button
                    onClick={() => setSignalPickerOpen(v => !v)}
                    className="border border-dashed border-blue-800 text-blue-500 rounded-md px-2.5 py-1 text-[11px] hover:border-blue-600 transition-colors"
                  >
                    + Добавить
                  </button>
                  {signalPickerOpen && (
                    <div className="absolute top-full mt-1 left-0 bg-gray-800 border border-gray-600 rounded-lg z-10 w-48 shadow-xl max-h-48 overflow-y-auto">
                      {AVAILABLE_SIGNALS
                        .filter(name => !form.signal_configs.find(s => s.name === name))
                        .map(name => (
                          <button
                            key={name}
                            onClick={() => addSignal(name)}
                            className="w-full text-left px-3 py-1.5 text-[11px] text-gray-300 hover:bg-gray-700 border-b border-gray-700/50 last:border-0"
                          >
                            {name}
                          </button>
                        ))}
                    </div>
                  )}
                </div>
              </div>
            </>
          )}

          {/* Tab 3: Exit */}
          {tab === 2 && (
            <>
              {/* TP */}
              <div className="border border-green-900/50 bg-green-950/20 rounded-lg p-3 space-y-3">
                <div className="text-xs font-semibold text-green-400">Take Profit</div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>TP %</label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.tp_pct}
                      onChange={e => patch({ tp_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Режим</label>
                    <Toggle
                      options={[{ label: 'total', value: 'total' }, { label: 'per level', value: 'per_level' }]}
                      value={form.tp_mode}
                      onChange={v => patch({ tp_mode: v as 'total' | 'per_level' })}
                    />
                  </div>
                </div>
              </div>

              {/* SL */}
              <div className="border border-red-900/50 bg-red-950/20 rounded-lg p-3 space-y-3">
                <div className="text-xs font-semibold text-red-400">Stop Loss</div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>SL %</label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.sl_pct}
                      onChange={e => patch({ sl_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Тип</label>
                    <Toggle
                      options={[
                        { label: 'conditional', value: 'conditional' },
                        { label: 'market', value: 'programmatic' },
                      ]}
                      value={form.sl_type}
                      onChange={v => patch({ sl_type: v as 'conditional' | 'programmatic' })}
                    />
                  </div>
                </div>
              </div>

              {/* Trailing stop */}
              <div className="space-y-3">
                <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800">
                  Трейлинг-стоп
                </div>
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => patch({ trailing_stop_enabled: !form.trailing_stop_enabled })}
                    className={`w-10 h-5 rounded-full relative transition-colors ${
                      form.trailing_stop_enabled ? 'bg-green-600' : 'bg-gray-700'
                    }`}
                  >
                    <span className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                      form.trailing_stop_enabled ? 'translate-x-5' : 'translate-x-0.5'
                    }`} />
                  </button>
                  <span className={`text-xs ${form.trailing_stop_enabled ? 'text-green-400' : 'text-gray-500'}`}>
                    {form.trailing_stop_enabled ? 'Включён' : 'Выключен'}
                  </span>
                </div>
                {form.trailing_stop_enabled && (
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>Активация %</label>
                      <input
                        type="number" step="0.1" min="0.1"
                        value={form.trailing_activation_pct}
                        onChange={e => patch({ trailing_activation_pct: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>Callback %</label>
                      <input
                        type="number" step="0.1" min="0.1"
                        value={form.trailing_callback_pct}
                        onChange={e => patch({ trailing_callback_pct: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                  </div>
                )}
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        {error && (
          <div className="px-5 py-2 text-sm text-red-400 border-t border-gray-700">✗ {error}</div>
        )}
        <div className="flex justify-end gap-2 px-5 py-3 border-t border-gray-700">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm bg-gray-800 text-gray-300 rounded-lg hover:bg-gray-700 transition-colors"
          >
            Отмена
          </button>
          <button
            onClick={handleSubmit}
            disabled={saving}
            className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
          >
            {saving ? 'Сохранение…' : strategy ? 'Сохранить' : 'Создать стратегию'}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build to verify**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -20`

Expected: no new errors

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/strategies/StrategyModal.tsx
git commit -m "feat: StrategyModal — 3-tab form with template selector"
```

---

### Task 4: StrategyCard

**Files:**
- Create: `frontend/src/components/strategies/StrategyCard.tsx`

- [ ] **Step 1: Create frontend/src/components/strategies/StrategyCard.tsx**

```tsx
import { useState } from 'react'
import { getStrategyState, setStrategyStatus, deleteStrategy } from '../../api/strategies'
import { SignalMiniCard } from './SignalMiniCard'
import type { Strategy, StrategyState, ExchangeAccount } from '../../types'

interface Props {
  strategy: Strategy
  accounts: ExchangeAccount[]
  onEdit: (s: Strategy) => void
  onChanged: () => void
}

interface CardState {
  open: boolean
  state: StrategyState | null
  loading: boolean
  acting: boolean
}

function statusBadge(status: string) {
  if (status === 'active')
    return <span className="text-[10px] px-2 py-0.5 rounded-full bg-green-900/40 text-green-400">Active</span>
  if (status === 'finishing')
    return <span className="text-[10px] px-2 py-0.5 rounded-full bg-yellow-900/40 text-yellow-400">Finishing</span>
  return <span className="text-[10px] px-2 py-0.5 rounded-full bg-gray-800 text-gray-500">Stopped</span>
}

function pnlColor(v: number) {
  if (v > 0) return 'text-green-400'
  if (v < 0) return 'text-red-400'
  return 'text-gray-500'
}

export function StrategyCard({ strategy: s, accounts, onEdit, onChanged }: Props) {
  const [cs, setCs] = useState<CardState>({ open: false, state: null, loading: false, acting: false })

  const accountName = accounts.find(a => a.id === s.account_id)?.label ?? s.account_id.slice(0, 8)

  async function toggle() {
    if (!cs.open && !cs.state) {
      setCs(p => ({ ...p, open: true, loading: true }))
      try {
        const data = await getStrategyState(s.id)
        setCs(p => ({ ...p, state: data, loading: false }))
      } catch {
        setCs(p => ({ ...p, loading: false }))
      }
    } else {
      setCs(p => ({ ...p, open: !p.open }))
    }
  }

  async function handleStatus(status: 'active' | 'finishing' | 'stopped') {
    setCs(p => ({ ...p, acting: true }))
    try {
      await setStrategyStatus(s.id, status)
      onChanged()
    } finally {
      setCs(p => ({ ...p, acting: false }))
    }
  }

  async function handleDelete() {
    if (!window.confirm('Удалить стратегию?')) return
    setCs(p => ({ ...p, acting: true }))
    try {
      await deleteStrategy(s.id)
      onChanged()
    } finally {
      setCs(p => ({ ...p, acting: false }))
    }
  }

  // Compute TP/SL display prices from strategy settings + state
  function tpPrice() {
    if (!cs.state || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.avg_entry * (1 - s.tp_pct / 100)
      : cs.state.avg_entry * (1 + s.tp_pct / 100)
  }

  function slPrice() {
    if (!cs.state || cs.state.start_price === 0) return null
    return s.direction === 'short'
      ? cs.state.start_price * (1 + s.sl_pct / 100)
      : cs.state.start_price * (1 - s.sl_pct / 100)
  }

  const btnBase = 'px-3 py-1.5 text-xs rounded-lg transition-colors disabled:opacity-50'

  return (
    <li className="border border-gray-700 rounded-xl overflow-hidden">
      {/* Collapsed header */}
      <button
        onClick={toggle}
        className="w-full flex items-center gap-3 px-5 py-3.5 hover:bg-gray-800/40 transition-colors text-left"
      >
        <div className="min-w-0 shrink-0">
          <div className="font-semibold text-gray-100 text-sm">{s.symbol}</div>
          <div className="text-[11px] text-gray-500 capitalize">
            {s.direction} · {accountName}
          </div>
        </div>
        <div className="flex items-center gap-4 ml-auto shrink-0 text-right">
          <div>
            <div className="text-[10px] text-gray-600">Grid</div>
            <div className="text-[11px] text-gray-300">{s.active_levels}/{s.grid_levels}</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-600">Объём</div>
            <div className="text-[11px] text-gray-300">{s.volume_usdt.toFixed(0)} USDT</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-600">P&L</div>
            <div className={`text-[11px] font-mono ${pnlColor(s.last_pnl)}`}>
              {s.last_pnl === 0 ? '—' : `${s.last_pnl > 0 ? '+' : ''}${s.last_pnl.toFixed(2)}`}
            </div>
          </div>
          {statusBadge(s.status)}
          <svg
            className={`w-4 h-4 text-gray-500 transition-transform ${cs.open ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {/* Expanded body */}
      {cs.open && (
        <div className="border-t border-gray-700 bg-gray-900/40">

          {cs.loading && (
            <div className="px-5 py-6 text-sm text-gray-500 text-center">Загрузка…</div>
          )}

          {!cs.loading && (
            <>
              {/* Signals */}
              {s.signal_configs && s.signal_configs.length > 0 && (
                <div className="px-5 py-3 border-b border-gray-800">
                  <div className="text-[10px] text-gray-600 uppercase tracking-wider mb-2">Сигналы</div>
                  <div className="flex gap-2 flex-wrap">
                    {s.signal_configs.map(sc => (
                      <SignalMiniCard key={sc.name} signal={sc} />
                    ))}
                  </div>
                </div>
              )}

              {/* Ждёт */}
              <div className="px-5 py-3 border-b border-gray-800">
                <div className="text-[10px] text-gray-600 uppercase tracking-wider mb-2">Ждёт</div>
                <div className="flex gap-4">
                  {/* Left: levels */}
                  <div className="flex-1 space-y-1.5">
                    <div className="text-[9px] text-gray-600 uppercase mb-1">Уровни</div>
                    {cs.state && cs.state.levels.length > 0 ? (
                      cs.state.levels.map(l => (
                        <div key={l.level_idx} className="flex items-center gap-2 text-[11px]">
                          <span className={
                            l.status === 'filled' ? 'text-green-400' :
                            l.status === 'placed' ? 'text-yellow-400' : 'text-gray-600'
                          }>
                            {l.status === 'filled' ? '✓' : l.status === 'placed' ? '◉' : '○'}
                          </span>
                          <span className="text-gray-400">
                            L{l.level_idx + 1} @ <span className="text-gray-200">{l.target_price.toFixed(2)}</span>
                          </span>
                          <span className={`ml-auto text-[9px] ${
                            l.status === 'filled' ? 'text-green-500' :
                            l.status === 'placed' ? 'text-yellow-500' : 'text-gray-600'
                          }`}>
                            {l.status === 'filled' ? 'заполнен' : l.status === 'placed' ? 'ожидает' : 'pending'}
                          </span>
                        </div>
                      ))
                    ) : (
                      <div className="text-[11px] text-gray-600">Нет активного цикла</div>
                    )}
                  </div>

                  {/* Divider */}
                  <div className="w-px bg-gray-800 shrink-0" />

                  {/* Right: TP/SL */}
                  <div className="w-44 space-y-2">
                    <div className="text-[9px] text-gray-600 uppercase mb-1">Выход</div>
                    <div className="border border-green-900/40 bg-green-950/20 rounded-lg px-3 py-2">
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-[10px] text-green-400 font-semibold">Take Profit</span>
                        {cs.state?.tp_order_id
                          ? <span className="text-[9px] bg-green-900/40 text-green-500 px-1.5 py-0.5 rounded">выставлен</span>
                          : <span className="text-[9px] text-gray-600">—</span>
                        }
                      </div>
                      <div className="font-mono text-gray-100 text-sm">
                        {tpPrice() ? tpPrice()!.toFixed(2) : '—'}
                      </div>
                      <div className="text-[10px] text-green-400">+{s.tp_pct}% от avg entry</div>
                    </div>
                    <div className="border border-red-900/40 bg-red-950/20 rounded-lg px-3 py-2">
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-[10px] text-red-400 font-semibold">Stop Loss</span>
                        <span className="text-[9px] text-gray-600">{s.sl_type}</span>
                      </div>
                      <div className="font-mono text-gray-100 text-sm">
                        {slPrice() ? slPrice()!.toFixed(2) : '—'}
                      </div>
                      <div className="text-[10px] text-red-400">−{s.sl_pct}% от start</div>
                    </div>
                  </div>
                </div>
              </div>

              {/* Control buttons */}
              <div className="px-5 py-3 flex gap-2">
                <button
                  onClick={() => onEdit(s)}
                  className={`${btnBase} bg-blue-900/30 text-blue-300 border border-blue-800/50 hover:bg-blue-900/50`}
                >
                  ✎ Редактировать
                </button>
                {s.status === 'active' || s.status === 'finishing' ? (
                  <>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('stopped')}
                      className={`${btnBase} bg-red-900/30 text-red-300 border border-red-800/50 hover:bg-red-900/50`}
                    >
                      ■ Остановить
                    </button>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('finishing')}
                      className={`${btnBase} bg-gray-800 text-gray-400 border border-gray-700 hover:bg-gray-700`}
                    >
                      ⊠ Завершить
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('active')}
                      className={`${btnBase} bg-green-900/30 text-green-300 border border-green-800/50 hover:bg-green-900/50`}
                    >
                      ▶ Включить
                    </button>
                    <button
                      disabled={cs.acting}
                      onClick={handleDelete}
                      className={`${btnBase} ml-auto text-red-500 hover:text-red-400 hover:bg-red-900/20`}
                    >
                      Удалить
                    </button>
                  </>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </li>
  )
}
```

- [ ] **Step 2: Build to verify**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -20`

Expected: no new errors

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/strategies/StrategyCard.tsx
git commit -m "feat: StrategyCard — expandable card with signals, levels, TP/SL, controls"
```

---

### Task 5: StrategiesPage + nav + routing

**Files:**
- Create: `frontend/src/pages/StrategiesPage.tsx`
- Modify: `frontend/src/components/Layout.tsx`
- Modify: `frontend/src/App.tsx`

- [ ] **Step 1: Create frontend/src/pages/StrategiesPage.tsx**

```tsx
import { useState, useEffect, useCallback } from 'react'
import { listStrategies } from '../api/strategies'
import { listAccounts } from '../api/accounts'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import type { Strategy, ExchangeAccount } from '../types'

export function StrategiesPage() {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [strats, accs] = await Promise.all([listStrategies(), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch {
      setStrategies([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  function openCreate() {
    setEditTarget(undefined)
    setModalOpen(true)
  }

  function openEdit(s: Strategy) {
    setEditTarget(s)
    setModalOpen(true)
  }

  function handleSaved() {
    setModalOpen(false)
    load()
  }

  return (
    <div className="max-w-2xl mx-auto space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Стратегии</h1>
        <button
          onClick={openCreate}
          className="px-4 py-2 bg-blue-600 text-white text-sm rounded-lg hover:bg-blue-700 transition-colors"
        >
          + Новая стратегия
        </button>
      </div>

      <div className="bg-white dark:bg-gray-800 rounded-xl shadow overflow-hidden">
        {loading ? (
          <div className="p-10 text-center text-gray-400">Загрузка…</div>
        ) : strategies.length === 0 ? (
          <div className="p-10 text-center text-gray-400">
            Нет стратегий. Нажмите «+ Новая стратегия».
          </div>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {strategies.map(s => (
              <StrategyCard
                key={s.id}
                strategy={s}
                accounts={accounts}
                onEdit={openEdit}
                onChanged={load}
              />
            ))}
          </ul>
        )}
      </div>

      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          onClose={() => setModalOpen(false)}
          onSaved={handleSaved}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 2: Add Стратегии to nav in frontend/src/components/Layout.tsx**

Find the `navItems` array (line 6) and add the strategies entry:

```typescript
const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Terminal', to: '/terminal' },
  { label: 'Webhooks', to: '/webhooks' },
  { label: 'Аккаунты', to: '/accounts' },
  { label: 'Стратегии', to: '/strategies' },
]
```

- [ ] **Step 3: Add route in frontend/src/App.tsx**

Add import at the top with other page imports:

```typescript
import { StrategiesPage } from './pages/StrategiesPage'
```

Add route inside the protected `<Routes>` block (after the `accounts` route):

```tsx
<Route path="strategies" element={<StrategiesPage />} />
```

- [ ] **Step 4: Build to verify**

Run: `cd frontend && npx tsc --noEmit 2>&1 | head -30`

Expected: no errors

Run: `cd frontend && npm run build 2>&1 | tail -10`

Expected: `✓ built in ...` with no errors

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/StrategiesPage.tsx \
        frontend/src/components/Layout.tsx \
        frontend/src/App.tsx
git commit -m "feat: StrategiesPage + nav item + route /strategies"
```
