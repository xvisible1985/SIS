# ALS Hedge Strategy — Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin actually select, configure, and save an ALS hedge strategy from
`HedgeBotForm.tsx` — strategy-type selector becomes real, a new "2. Пороги" tab replaces
"2. Матрица" for ALS, and ALS-specific fields appear on "3. Параметры".

**Architecture:** `HedgeBotForm.tsx`'s `config.strategy_type` becomes a real,
user-settable value instead of the hardcoded `'matrix'` literal it is today. The existing
tab slot ids (`'entry' | 'matrix' | 'params'`) are kept as-is — only their *label* and
*rendered content* branch on `config.strategy_type`, avoiding a wider refactor of the
tab-switching logic. The new threshold table reuses the exact same local widgets
(`MxNum`, `Toggle`, `Field`) the Matrix table already uses, following that file's
established patterns rather than introducing new ones. `types.ts` gets a new
`AlsThreshold` type and eight new `StrategyConfig` fields, mirroring the Go field names
already committed in `pkg/strategy/types.go` (Foundations/Polish plans) and
`services/api-gateway/bot_engine.go` (the API Persistence plan, this same session).

**Tech Stack:** React, TypeScript, existing `HedgeBotForm.tsx`/`FormWidgets.tsx`
component library.

**⚠️ Prerequisite — do not execute before this is true:**

The user explicitly decided (2026-10-19, this session, after the admin-gate backend was
committed) to leave the ALS button disabled-for-everyone in the live app **for now**,
specifically because `pkg/strategy`'s real ALS engine doesn't exist yet — only the
Foundations/Core-Engine/Polish plan documents do. **This plan must not be executed until
`2026-10-18-als-hedge-foundations.md`, `2026-10-18-als-hedge-engine-core.md`, and
`2026-10-18-als-hedge-polish.md` are fully implemented**, and
`2026-10-18-als-hedge-api-persistence.md` (the sibling backend plan, also written this
session) is implemented and its two migrations applied. Before unlocking anything live,
re-confirm with the user that they still want the button unlocked at that point — their
earlier "not yet" was about timing, not a permanent no, but it should be re-asked rather
than assumed.

---

### Task 1: `types.ts` — `AlsThreshold` type and new `StrategyConfig` fields

**Files:**
- Modify: `frontend/src/features/bots/types.ts`

- [ ] **Step 1: Add the `AlsThreshold` type**

Add right after the existing `export type { MatrixLevel, MatrixEntryLevel, SignalConfig };`
(line 2):

```ts
// ALS (Anti-Liquidation System) hedge strategy — one row of the "2. Пороги" table.
// Mirrors pkg/strategy/types.go's AlsThreshold struct (Foundations plan) field-for-field.
export type AlsThreshold = {
  trigger_type: number;     // 0 = from last filled order, 1 = from main's avg entry (ТВХ)
  trigger_pct: number;      // negative = adverse move (against main), same sign convention as hedge_act_value
  target_hedge_pct: number; // cumulative target hedge volume, % of main's CURRENT live USDT volume
};
```

- [ ] **Step 2: Widen `strategy_type` and add the eight new fields**

Current (`types.ts:27`):

```ts
  strategy_type?: 'grid' | 'matrix';
```

Replace with:

```ts
  strategy_type?: 'grid' | 'matrix' | 'als';
```

Then, right after the existing Matrix-specific block ends (after `size_as_main?: boolean;`,
`types.ts:65`), add:

```ts
  // ALS-specific (strategy_type: 'als'). Mirrors pkg/strategy/types.go's Strategy
  // fields of the same name (Foundations/Polish plans) and
  // services/api-gateway/bot_engine.go's botCfgJSON (API Persistence plan).
  als_thresholds?: AlsThreshold[];
  unsafe_hedge_min_pct?: number;
  unsafe_hedge_max_pct?: number;
  stop_market_size_k?: number;
  tp_control_enabled?: boolean;
  initial_stop_be_pct?: number | null;
  als_stop_cond_pct?: number;
  als_stop_replace_pct?: number;
```

- [ ] **Step 3: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors (purely additive type changes).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/bots/types.ts
git commit -m "Add AlsThreshold type and ALS fields to StrategyConfig"
```

---

### Task 2: `HedgeBotForm.tsx` — real `strategy_type` state, admin-gated selector

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx`

- [ ] **Step 1: Import `useAuth` and `AlsThreshold`**

Current (`HedgeBotForm.tsx:1-16`, the import block):

```tsx
import { useState, useEffect, useRef, forwardRef, useImperativeHandle } from 'react';
import { createPortal } from 'react-dom';
import { X, Shield, ToggleLeft, ToggleRight, Camera, Trash2, Smile } from 'lucide-react';
import { BotIconPicker } from './BotIconPicker';
import { Toggle, Tip, SignalPickerField } from '../../../components/strategies/FormWidgets';
import { getStrategyDefaults } from '../../admin-defaults/api';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { getInstrumentConstraints, type InstrumentConstraints } from '../../../api/strategies';
import { apiClient } from '../../../api/client';
import { useSelectedAccount } from '../../../contexts/AccountContext';
import { listAccounts } from '../../../api/accounts';
import type { Bot as BotType, CreateBotInput, StrategyConfig, MatrixLevel, MatrixEntryLevel } from '../types';
import { BOT_KIND_META } from '../botKindMeta';
import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';
import { SettingsSyncConfirmModal } from './SettingsSyncConfirmModal';
import { hasSyncableDiff } from '../syncableSettingsFields';
import type { SignalConfig } from '../../../types';
```

Replace with:

```tsx
import { useState, useEffect, useRef, forwardRef, useImperativeHandle } from 'react';
import { createPortal } from 'react-dom';
import { X, Shield, ToggleLeft, ToggleRight, Camera, Trash2, Smile } from 'lucide-react';
import { BotIconPicker } from './BotIconPicker';
import { Toggle, Tip, SignalPickerField } from '../../../components/strategies/FormWidgets';
import { getStrategyDefaults } from '../../admin-defaults/api';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { getInstrumentConstraints, type InstrumentConstraints } from '../../../api/strategies';
import { apiClient } from '../../../api/client';
import { useSelectedAccount } from '../../../contexts/AccountContext';
import { listAccounts } from '../../../api/accounts';
import { useAuth } from '../../../hooks/useAuth';
import type { Bot as BotType, CreateBotInput, StrategyConfig, MatrixLevel, MatrixEntryLevel, AlsThreshold } from '../types';
import { BOT_KIND_META } from '../botKindMeta';
import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';
import { SettingsSyncConfirmModal } from './SettingsSyncConfirmModal';
import { hasSyncableDiff } from '../syncableSettingsFields';
import type { SignalConfig } from '../../../types';
```

- [ ] **Step 2: Add `ALS_TRIGGER_TYPES` and `DEFAULT_ALS_THRESHOLDS`**

Current (`HedgeBotForm.tsx:97-106`):

```tsx
const DEFAULT_MATRIX_LEVELS: MatrixLevel[] = [
  { direction: 'below', price_step_pct: -1.5, size_pct: 5,  stop_pct: -3.0, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 2.5 },
  { direction: 'below', price_step_pct: -3.0, size_pct: 8,  stop_pct: -2.5, stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 3.5 },
  { direction: 'below', price_step_pct: -5.0, size_pct: 12, stop_pct: null,  stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 4.0 },
  { direction: 'above', price_step_pct: 2.0,  size_pct: 5,  stop_pct: -3.5, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 3.0 },
  { direction: 'above', price_step_pct: 4.0,  size_pct: 8,  stop_pct: -3.0, stop_cond_pct: -1.5, stop_replace_pct: -1.5, tp_pct: 4.5 },
];
const DEFAULT_MATRIX_ENTRY: MatrixEntryLevel = {
  price_step_pct: null, size_pct: 10, stop_pct: -5.0, stop_cond_pct: -2.0, stop_replace_pct: 1.0, tp_pct: 2.0,
};
```

Add right after it:

```tsx
// ALS trigger_type: mirrors the first two ACT_TYPES entries (last-order% / avg-entry%)
// — ALS deliberately has no PNL$/ROI% trigger kinds (see design spec, "Price-distance
// thresholds, not ROI").
const ALS_TRIGGER_TYPES = ['От последнего ордера', 'От ТВХ Main'];

// Worked example straight from the design spec's "target-based hedge sizing" section:
// -10%→30%, -30%→50%, -60%→80% of main's current live volume.
const DEFAULT_ALS_THRESHOLDS: AlsThreshold[] = [
  { trigger_type: 1, trigger_pct: -10, target_hedge_pct: 30 },
  { trigger_type: 1, trigger_pct: -30, target_hedge_pct: 50 },
  { trigger_type: 1, trigger_pct: -60, target_hedge_pct: 80 },
];
```

- [ ] **Step 3: Seed ALS defaults in `defaultStratConfig`, stop hardcoding `strategy_type`**

Current (`HedgeBotForm.tsx:108-138`):

```tsx
function defaultStratConfig(bot?: BotType): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    bot_kind:                'hedge',
    symbol:                  s.symbol                  ?? 'BTCUSDT',
    category:                s.category                ?? 'linear',
    direction:               s.direction               ?? 'both',
    strategy_type:           'matrix',
    entry_order_type:        s.entry_order_type        ?? 'limit',
    leverage:                s.leverage                ?? 5,
    margin_type:             s.margin_type             ?? 'isolated',
    hedge_mode:              s.hedge_mode              ?? true,
    grid_size_usdt:          s.grid_size_usdt          ?? 100,
    grid_active:             s.grid_active             ?? 0,
    max_stop_active:         s.max_stop_active         ?? 0,
    signal_configs:          (s.signal_configs          as SignalConfig[]) ?? [],
    activation_signals:      (s.activation_signals      as SignalConfig[]) ?? [],
    after_stop_mode:         s.after_stop_mode         ?? 'restart',
    max_cycles:              s.max_cycles              ?? 0,
    // Matrix
    matrix_levels:           s.matrix_levels           ?? DEFAULT_MATRIX_LEVELS,
    matrix_entry_level:      s.matrix_entry_level      ?? DEFAULT_MATRIX_ENTRY,
    safe_zone_pct:           s.safe_zone_pct           ?? 1.5,
    protected_build:         s.protected_build         ?? false,
    matrix_rebuild_on_sl:    s.matrix_rebuild_on_sl    ?? false,
    matrix_rebuild_from_entry: s.matrix_rebuild_from_entry ?? false,
    relative_slots:          s.relative_slots          ?? false,
    size_as_main:            s.size_as_main            ?? false,
    hedge_tp_main_mode:      s.hedge_tp_main_mode      ?? 'cancel',
  };
}
```

Replace with:

```tsx
function defaultStratConfig(bot?: BotType): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    bot_kind:                'hedge',
    symbol:                  s.symbol                  ?? 'BTCUSDT',
    category:                s.category                ?? 'linear',
    direction:               s.direction               ?? 'both',
    strategy_type:           s.strategy_type           ?? 'matrix',
    entry_order_type:        s.entry_order_type        ?? 'limit',
    leverage:                s.leverage                ?? 5,
    margin_type:             s.margin_type             ?? 'isolated',
    hedge_mode:              s.hedge_mode              ?? true,
    grid_size_usdt:          s.grid_size_usdt          ?? 100,
    grid_active:             s.grid_active             ?? 0,
    max_stop_active:         s.max_stop_active         ?? 0,
    signal_configs:          (s.signal_configs          as SignalConfig[]) ?? [],
    activation_signals:      (s.activation_signals      as SignalConfig[]) ?? [],
    after_stop_mode:         s.after_stop_mode         ?? 'restart',
    max_cycles:              s.max_cycles              ?? 0,
    // Matrix
    matrix_levels:           s.matrix_levels           ?? DEFAULT_MATRIX_LEVELS,
    matrix_entry_level:      s.matrix_entry_level      ?? DEFAULT_MATRIX_ENTRY,
    safe_zone_pct:           s.safe_zone_pct           ?? 1.5,
    protected_build:         s.protected_build         ?? false,
    matrix_rebuild_on_sl:    s.matrix_rebuild_on_sl    ?? false,
    matrix_rebuild_from_entry: s.matrix_rebuild_from_entry ?? false,
    relative_slots:          s.relative_slots          ?? false,
    size_as_main:            s.size_as_main            ?? false,
    hedge_tp_main_mode:      s.hedge_tp_main_mode      ?? 'cancel',
    // ALS
    als_thresholds:          s.als_thresholds          ?? DEFAULT_ALS_THRESHOLDS,
    unsafe_hedge_min_pct:    s.unsafe_hedge_min_pct    ?? 0,
    unsafe_hedge_max_pct:    s.unsafe_hedge_max_pct    ?? 80,
    stop_market_size_k:      s.stop_market_size_k      ?? 1,
    tp_control_enabled:      s.tp_control_enabled      ?? false,
    initial_stop_be_pct:     s.initial_stop_be_pct     ?? null,
    als_stop_cond_pct:       s.als_stop_cond_pct       ?? 3,
    als_stop_replace_pct:    s.als_stop_replace_pct    ?? 1,
  };
}
```

- [ ] **Step 4: Read `isAdmin` inside the component**

Current (`HedgeBotForm.tsx:163-167`):

```tsx
export const HedgeBotForm = forwardRef<HedgeBotFormHandle, Props>(function HedgeBotForm(
  { bot, onSubmit, onClose, mode = 'user', takenSymbols, embedded = false, hideFilters = false }, ref
) {
  const [outerTab, setOuterTab] = useState<OuterTab>(embedded ? 'activation' : 'basic');
  const [stratTab, setStratTab] = useState<StratTab>('entry');
```

Replace with:

```tsx
export const HedgeBotForm = forwardRef<HedgeBotFormHandle, Props>(function HedgeBotForm(
  { bot, onSubmit, onClose, mode = 'user', takenSymbols, embedded = false, hideFilters = false }, ref
) {
  const { isAdmin } = useAuth();
  const [outerTab, setOuterTab] = useState<OuterTab>(embedded ? 'activation' : 'basic');
  const [stratTab, setStratTab] = useState<StratTab>('entry');
```

- [ ] **Step 5: Add ALS threshold-table helpers next to the matrix helpers**

Current (`HedgeBotForm.tsx:349-353`, right where the matrix helpers start):

```tsx
  // ── Matrix helpers ───────────────────────────────────────────────────────────
  const matrixLevels     = config.matrix_levels     ?? DEFAULT_MATRIX_LEVELS;
  const matrixEntryLevel = config.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY;
  const aboveLevels = matrixLevels.filter(l => l.direction === 'above');
  const belowLevels = matrixLevels.filter(l => l.direction === 'below');
```

Add right before it (so the ALS block comes first):

```tsx
  // ── ALS threshold-table helpers ──────────────────────────────────────────────
  const alsThresholds = config.als_thresholds ?? DEFAULT_ALS_THRESHOLDS;

  function addAlsThreshold() {
    const last = alsThresholds[alsThresholds.length - 1];
    const nv: AlsThreshold = {
      trigger_type: last?.trigger_type ?? 1,
      trigger_pct: last ? +(last.trigger_pct - 10).toFixed(1) : -10,
      target_hedge_pct: last ? +(last.target_hedge_pct + 20).toFixed(1) : 30,
    };
    patch({ als_thresholds: [...alsThresholds, nv] });
  }
  function removeAlsThreshold(idx: number) {
    if (alsThresholds.length <= 1) return;
    patch({ als_thresholds: alsThresholds.filter((_, i) => i !== idx) });
  }
  function updateAlsThreshold(idx: number, field: keyof AlsThreshold, value: number) {
    patch({ als_thresholds: alsThresholds.map((t, i) => i === idx ? { ...t, [field]: value } : t) });
  }

  // ── Matrix helpers ───────────────────────────────────────────────────────────
  const matrixLevels     = config.matrix_levels     ?? DEFAULT_MATRIX_LEVELS;
  const matrixEntryLevel = config.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY;
  const aboveLevels = matrixLevels.filter(l => l.direction === 'above');
  const belowLevels = matrixLevels.filter(l => l.direction === 'below');
```

- [ ] **Step 6: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors — `isAdmin`/`alsThresholds`/the three new functions are all
declared but not yet referenced by JSX (that's Tasks 3-5), so expect "declared but never
read" to be a non-error in this codebase's tsconfig (confirm by checking the existing
build is clean after this step; if the project's lint config treats unused locals as
build-breaking, wrap Step 6 with `// eslint-disable-next-line` ahead of each new function
temporarily — but this is unlikely to be needed since Task 3 immediately below wires
`isAdmin` into JSX, closing the gap within the same task sequence before any commit).

- [ ] **Step 7: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Wire ALS defaults and threshold-table helpers into HedgeBotForm"
```

---

### Task 3: Make the strategy-type selector real and admin-gated

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx:1446-1463`

- [ ] **Step 1: Replace the hardcoded selector**

Current:

```tsx
              {/* Strategy type selector */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-bold uppercase tracking-wider text-slate-400">Тип стратегии</span>
                <div className="flex gap-1">
                  <button
                    type="button"
                    className="rounded-lg border px-4 py-1.5 text-[12px] font-semibold border-amber-500/40 bg-amber-500/[.18] text-amber-300"
                  >
                    Matrix
                  </button>
                  <div
                    title="В разработке"
                    className="relative rounded-lg border border-white/[.07] bg-white/[.02] px-4 py-1.5 text-[12px] font-semibold text-slate-600 cursor-not-allowed opacity-50"
                  >
                    ALS
                    <span className="ml-1.5 text-[9px] font-bold uppercase tracking-wide text-slate-500">скоро</span>
                  </div>
                </div>
              </div>
```

Replace with:

```tsx
              {/* Strategy type selector */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-bold uppercase tracking-wider text-slate-400">Тип стратегии</span>
                <div className="flex gap-1">
                  <button
                    type="button"
                    onClick={() => patch({ strategy_type: 'matrix' })}
                    className={`rounded-lg border px-4 py-1.5 text-[12px] font-semibold transition-colors ${
                      (config.strategy_type ?? 'matrix') === 'matrix'
                        ? 'border-amber-500/40 bg-amber-500/[.18] text-amber-300'
                        : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
                    }`}
                  >
                    Matrix
                  </button>
                  {isAdmin ? (
                    <button
                      type="button"
                      onClick={() => patch({ strategy_type: 'als' })}
                      className={`rounded-lg border px-4 py-1.5 text-[12px] font-semibold transition-colors ${
                        config.strategy_type === 'als'
                          ? 'border-amber-500/40 bg-amber-500/[.18] text-amber-300'
                          : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
                      }`}
                    >
                      ALS
                    </button>
                  ) : (
                    <div
                      title="Доступно только администраторам"
                      className="relative rounded-lg border border-white/[.07] bg-white/[.02] px-4 py-1.5 text-[12px] font-semibold text-slate-600 cursor-not-allowed opacity-50"
                    >
                      ALS
                      <span className="ml-1.5 text-[9px] font-bold uppercase tracking-wide text-slate-500">админ</span>
                    </div>
                  )}
                </div>
              </div>
```

- [ ] **Step 2: Manual check**

Run the frontend dev server, open "Хедж позиция → Стратегия" as a non-admin user:
confirm ALS still shows the disabled "админ" badge and clicking it does nothing. Log in
as an admin: confirm clicking "ALS" highlights it and clicking back to "Matrix" works
too.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Make hedge strategy-type selector real, admin-gated for ALS"
```

---

### Task 4: Tab label swap and content dispatch for "2. Матрица" / "2. Пороги"

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx`

- [ ] **Step 1: Make the tab label dynamic**

Current (`HedgeBotForm.tsx:537-541`):

```tsx
  const stratTabs: { id: StratTab; label: string }[] = [
    { id: 'entry',  label: '1. Базовые'   },
    { id: 'matrix', label: '2. Матрица'   },
    { id: 'params', label: '3. Параметры' },
  ];
```

Replace with:

```tsx
  const isAls = config.strategy_type === 'als';
  const stratTabs: { id: StratTab; label: string }[] = [
    { id: 'entry',  label: '1. Базовые'   },
    { id: 'matrix', label: isAls ? '2. Пороги' : '2. Матрица' },
    { id: 'params', label: '3. Параметры' },
  ];
```

- [ ] **Step 2: Branch the tab's rendered content on `isAls`**

Current (`HedgeBotForm.tsx:1576-1577`, right where the Matrix tab content begins):

```tsx
              {/* 2. Матрица */}
              {stratTab === 'matrix' && (() => {
```

Replace with:

```tsx
              {/* 2. Пороги (ALS) */}
              {stratTab === 'matrix' && isAls && (
                <div className="flex flex-col gap-3">
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>
                        Депозит
                        {instrInfo && !sizeAsMain && (
                          <span className="ml-1.5 text-[10px] text-slate-500">
                            мин. {instrInfo.min_order_usdt.toFixed(2)}$
                          </span>
                        )}
                        <Tip text="Фиксированный — вводите USDT. Объём Main — размер от текущего объёма мейн-позиции." />
                      </label>
                      {sizeAsMain ? (
                        <div className="flex items-center h-[34px] px-3 rounded-lg border border-amber-500/20 bg-amber-950/20 text-[11px] text-amber-400">
                          = объём Main-позиции
                        </div>
                      ) : (
                        <div className="flex items-center gap-2">
                          <MxNum
                            value={config.grid_size_usdt ?? 100}
                            onChange={v => patch({ grid_size_usdt: v })}
                            disabled={minLotEnabled}
                            cls={`${inputCls} ${minLotEnabled ? 'cursor-not-allowed opacity-50' : ''}`}
                          />
                          <button
                            type="button"
                            onClick={() => setMinLotEnabled(v => !v)}
                            className={`shrink-0 rounded-lg border px-3 py-2 text-[11px] font-semibold transition-colors ${
                              minLotEnabled
                                ? 'border-amber-500/40 bg-amber-500/[.18] text-amber-300'
                                : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
                            }`}
                          >
                            min
                          </button>
                        </div>
                      )}
                    </div>
                    <div className="flex flex-col justify-end">
                      <label className={labelCls}>
                        Режим депозита
                        <Tip text="Фиксированный — задаёте USDT вручную. Объём Main — каждый порог рассчитывается от объёма мейн-позиции в реальном времени." />
                      </label>
                      <Toggle
                        options={[
                          { label: 'Фикс.',       value: 'fixed' },
                          { label: 'Объём Main',  value: 'main'  },
                        ]}
                        value={sizeAsMain ? 'main' : 'fixed'}
                        onChange={v => setSizeAsMain(v === 'main')}
                        btnClassName="!py-2"
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-2 gap-3 items-end">
                    <div>
                      <label className={labelCls}>
                        Плечо&nbsp;
                        <span className="font-bold text-white">×{config.leverage ?? 5}</span>
                        {instrInfo && <span className="ml-1.5 text-[10px] text-slate-500">макс. ×{instrInfo.max_leverage}</span>}
                        <Tip text="Кратность заёмных средств." />
                      </label>
                      <input
                        type="range"
                        min={1}
                        max={instrInfo?.max_leverage ?? 100}
                        step={1}
                        value={config.leverage ?? 5}
                        disabled={leverageMax}
                        onChange={e => { setLeverageMax(false); patch({ leverage: parseInt(e.target.value) }) }}
                        className="w-full h-1.5 mt-2 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                        style={{ accentColor: BOT_KIND_META['hedge'].color }}
                      />
                      <div className="flex justify-between text-[10px] text-slate-500 mt-0.5">
                        <span>×1</span>
                        <span>×{instrInfo?.max_leverage ?? 100}</span>
                      </div>
                    </div>
                    <div className="flex flex-col justify-end pb-[22px]">
                      <label className={labelCls}>Фикс. / Макс.</label>
                      <Toggle
                        options={[
                          { label: 'Фикс.', value: 'fixed' },
                          { label: 'Макс.', value: 'max'   },
                        ]}
                        value={leverageMax ? 'max' : 'fixed'}
                        onChange={v => {
                          const isMax = v === 'max'
                          setLeverageMax(isMax)
                          if (isMax) patch({ leverage: instrInfo?.max_leverage ?? 100 })
                        }}
                      />
                    </div>
                  </div>

                  {/* ── Пороги хеджа ── */}
                  <div>
                    <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pb-1 border-b border-gray-800 mb-1">
                      <span>Пороги добора хеджа <span className="normal-case text-gray-600 font-normal">(от ближнего к дальнему)</span></span>
                      <span className="bg-blue-900/60 text-blue-400 rounded px-1.5 py-0.5 text-[8px]">{alsThresholds.length} порогов</span>
                    </div>
                    <div className="grid grid-cols-[24px_1fr_1fr_1fr_18px] gap-[4px] px-1 pb-1">
                      <span />
                      <span className="text-[8px] text-gray-400 text-center">Откуда<Tip text="От последнего ордера main или от его ТВХ." /></span>
                      <span className="text-[8px] text-gray-400 text-center">Триггер %<Tip text="Расстояние цены от выбранной точки main. Отрицательное — против main." /></span>
                      <span className="text-[8px] text-gray-400 text-center">Цель хеджа %<Tip text="Итоговый (не добавочный) объём хеджа как % от ТЕКУЩЕГО объёма main. Бот добавит только разницу с уже открытым объёмом." /></span>
                      <span />
                    </div>
                    {alsThresholds.map((t, idx) => (
                      <div key={idx} className="grid grid-cols-[24px_1fr_1fr_1fr_18px] gap-[4px] px-1 py-1 rounded mb-0.5 bg-blue-950/20">
                        <div className="bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5">{idx + 1}</div>
                        <select
                          value={t.trigger_type}
                          onChange={e => updateAlsThreshold(idx, 'trigger_type', Number(e.target.value))}
                          className="bg-gray-900 border border-gray-700 rounded py-1 px-1 text-[10px] text-gray-100 w-full outline-none focus:border-amber-500"
                        >
                          {ALS_TRIGGER_TYPES.map((label, i) => <option key={i} value={i}>{label}</option>)}
                        </select>
                        <MxNum value={t.trigger_pct} onChange={v => updateAlsThreshold(idx, 'trigger_pct', v)} />
                        <MxNum value={t.target_hedge_pct} onChange={v => updateAlsThreshold(idx, 'target_hedge_pct', v)} />
                        {alsThresholds.length > 1
                          ? <button type="button" onClick={() => removeAlsThreshold(idx)} className="text-center text-xs text-red-500 hover:text-red-400">✕</button>
                          : <span />}
                      </div>
                    ))}
                    <button type="button" onClick={addAlsThreshold}
                      className="w-full border border-dashed border-blue-900 text-blue-600 rounded py-1 text-[10px] hover:border-blue-700 hover:text-blue-400 transition-colors mt-1">
                      + Добавить порог
                    </button>
                  </div>

                  {/* Сигналы */}
                  <div>
                    <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800">
                      Сигналы для входа<Tip text="AND-логика: все сигналы должны совпасть. Если не добавлены — ALS стартует сразу." />
                    </div>
                    <SignalPickerField
                      configs={(config.signal_configs as SignalConfig[]) ?? []}
                      onChange={v => patch({ signal_configs: v })}
                    />
                  </div>
                </div>
              )}

              {/* 2. Матрица */}
              {stratTab === 'matrix' && !isAls && (() => {
```

- [ ] **Step 3: Close the new conditional correctly**

The Matrix branch's own closing is unchanged (`})()}` a few hundred lines down,
`HedgeBotForm.tsx:1812` in the pre-edit file) — only its *opening* condition changed
(`stratTab === 'matrix'` → `stratTab === 'matrix' && !isAls`), so no further edit is
needed there; the new ALS block added in Step 2 is a sibling JSX expression with its own
complete, independently-closed `{...&& (...)}`.

- [ ] **Step 4: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors.

- [ ] **Step 5: Manual check**

Run the frontend dev server as an admin. Open a hedge bot, switch "Тип стратегии" to
ALS: "2. Матрица" tab becomes "2. Пороги" and shows the new threshold table with the
three default rows (-10%→30%, -30%→50%, -60%→80%). Switch back to Matrix: the original
level table reappears unchanged.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Add ALS thresholds tab, swapped in for Matrix's level table"
```

---

### Task 5: ALS-specific "3. Параметры" fields

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx:1818-1883`

- [ ] **Step 1: Branch the "Управление Хедж" section's content on `isAls`**

Current (`HedgeBotForm.tsx:1818-1883`, the matrix-only part of "3. Параметры" —
"Управление Мэйн" right after it, lines 1885-1961, is unchanged/shared and stays
exactly as-is):

```tsx
                  {/* ── Управление Хедж ── */}
                  <div>
                    <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                      Управление Хедж
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          Safe-Zone от SL %
                          <Tip text="После срабатывания SL — зона вокруг цены, в которой новые ордера матрицы не выставляются." />
                        </label>
                        <input type="number" step="0.1" min={0}
                          value={config.safe_zone_pct ?? 1.5}
                          onChange={e => patch({ safe_zone_pct: Number(e.target.value) })}
                          className={inputCls} />
                      </div>
                      <div>
                        <label className={labelCls}>
                          🔒 Защищённое построение
                          <Tip text="Следующий уровень выставляется только после того, как предыдущий прикрыт хотя бы одним стопом." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '🔒 Вкл', value: 'true' }]}
                          value={String(config.protected_build ?? false)}
                          onChange={v => patch({ protected_build: v === 'true' })}
                          optionColors={{ true: 'bg-amber-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          ⟳ Перестройка сетки от SZ
                          <Tip text="После SL немедленно переставляет все незаполненные уровни от нижней границы SafeZone." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '⟳ Вкл', value: 'true' }]}
                          value={String(config.matrix_rebuild_on_sl ?? false)}
                          onChange={v => patch(v === 'true' ? { matrix_rebuild_on_sl: true, relative_slots: false } : { matrix_rebuild_on_sl: false })}
                          optionColors={{ true: 'bg-blue-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          ⚓ Якорь на точку входа
                          <Tip text="Все уровни строятся от цены заполнения L(0). После SL перезаходит маркетом из SafeZone." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '⚓ Вкл', value: 'true' }]}
                          value={String(config.matrix_rebuild_from_entry ?? false)}
                          onChange={v => patch(v === 'true' ? { matrix_rebuild_from_entry: true, relative_slots: false } : { matrix_rebuild_from_entry: false })}
                          optionColors={{ true: 'bg-violet-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          ↻ Относительные слоты (Novabot)
                          <Tip text="После срабатывания SL внутреннего слота остальные перенумеровываются к входу, а следующий слот берётся по параметрам освободившейся глубины. Взаимоисключимо с перестройкой сетки." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '↻ Вкл', value: 'true' }]}
                          value={String(config.relative_slots ?? false)}
                          onChange={v => patch(v === 'true' ? { relative_slots: true, matrix_rebuild_on_sl: false, matrix_rebuild_from_entry: false } : { relative_slots: false })}
                          optionColors={{ true: 'bg-emerald-700 text-white' }}
                        />
                      </div>
                    </div>
                  </div>
```

Replace with:

```tsx
                  {/* ── Управление Хедж ── */}
                  {isAls ? (
                    <div>
                      <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                        Управление Хедж (ALS)
                      </div>
                      <div className="grid grid-cols-2 gap-3">
                        <div>
                          <label className={labelCls}>
                            Safe-Zone от SL %
                            <Tip text="После срабатывания стопа хеджа — зона вокруг цены стопа, в которой хедж не переоткрывается, пока цена не отойдёт в сторону main." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.safe_zone_pct ?? 1.5}
                            onChange={e => patch({ safe_zone_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Условных стопов ×K
                            <Tip text="Stop Market Size K — множитель объёма стоп-ордера хеджа относительно объёма хеджа, который он защищает." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.stop_market_size_k ?? 1}
                            onChange={e => patch({ stop_market_size_k: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Min ROI для стопа хеджа, %
                            <Tip text="Насколько цена должна пройти в пользу хеджа, прежде чем на него впервые выставляется безубыточный стоп." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.als_stop_cond_pct ?? 3}
                            onChange={e => patch({ als_stop_cond_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Stop Loss Break Even Hedge, %
                            <Tip text="Итоговое расстояние безубыточного стопа хеджа от цены, после того как условие выше выполнено." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.als_stop_replace_pct ?? 1}
                            onChange={e => patch({ als_stop_replace_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Unsafe hedge % мин.
                            <Tip text="Минимальный размер добора хеджа, % от текущего объёма main." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.unsafe_hedge_min_pct ?? 0}
                            onChange={e => patch({ unsafe_hedge_min_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Unsafe hedge % макс.
                            <Tip text="Максимальный размер добора хеджа без стопа, % от текущего объёма main. 0 = без ограничения." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.unsafe_hedge_max_pct ?? 80}
                            onChange={e => patch({ unsafe_hedge_max_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Контроль ТП на Main
                            <Tip text="Пока хедж открыт — ТП на main-позиции снимается, и восстанавливается после закрытия хеджа." />
                          </label>
                          <Toggle
                            options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                            value={String(config.tp_control_enabled ?? false)}
                            onChange={v => patch({ tp_control_enabled: v === 'true' })}
                            optionColors={{ true: 'bg-blue-700 text-white' }}
                          />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Initial Stop BE Hedge, %
                            <Tip text="Опционально: безубыточный стоп ставится сразу при открытии хеджа, ещё до того как сработает условие Min ROI выше. Пусто/выкл — стоп ждёт обычного условия." />
                          </label>
                          <div className="flex items-center gap-2">
                            <Toggle
                              options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                              value={String(config.initial_stop_be_pct !== null && config.initial_stop_be_pct !== undefined)}
                              onChange={v => patch({ initial_stop_be_pct: v === 'true' ? (config.als_stop_replace_pct ?? 1) : null })}
                              optionColors={{ true: 'bg-amber-700 text-white' }}
                            />
                            {config.initial_stop_be_pct !== null && config.initial_stop_be_pct !== undefined && (
                              <input type="number" step="0.1" min={0}
                                value={config.initial_stop_be_pct}
                                onChange={e => patch({ initial_stop_be_pct: Number(e.target.value) })}
                                className={`${inputCls} flex-1`} />
                            )}
                          </div>
                        </div>
                      </div>
                    </div>
                  ) : (
                    <div>
                      <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                        Управление Хедж
                      </div>
                      <div className="grid grid-cols-2 gap-3">
                        <div>
                          <label className={labelCls}>
                            Safe-Zone от SL %
                            <Tip text="После срабатывания SL — зона вокруг цены, в которой новые ордера матрицы не выставляются." />
                          </label>
                          <input type="number" step="0.1" min={0}
                            value={config.safe_zone_pct ?? 1.5}
                            onChange={e => patch({ safe_zone_pct: Number(e.target.value) })}
                            className={inputCls} />
                        </div>
                        <div>
                          <label className={labelCls}>
                            🔒 Защищённое построение
                            <Tip text="Следующий уровень выставляется только после того, как предыдущий прикрыт хотя бы одним стопом." />
                          </label>
                          <Toggle
                            options={[{ label: 'Выкл', value: 'false' }, { label: '🔒 Вкл', value: 'true' }]}
                            value={String(config.protected_build ?? false)}
                            onChange={v => patch({ protected_build: v === 'true' })}
                            optionColors={{ true: 'bg-amber-700 text-white' }}
                          />
                        </div>
                        <div>
                          <label className={labelCls}>
                            ⟳ Перестройка сетки от SZ
                            <Tip text="После SL немедленно переставляет все незаполненные уровни от нижней границы SafeZone." />
                          </label>
                          <Toggle
                            options={[{ label: 'Выкл', value: 'false' }, { label: '⟳ Вкл', value: 'true' }]}
                            value={String(config.matrix_rebuild_on_sl ?? false)}
                            onChange={v => patch(v === 'true' ? { matrix_rebuild_on_sl: true, relative_slots: false } : { matrix_rebuild_on_sl: false })}
                            optionColors={{ true: 'bg-blue-700 text-white' }}
                          />
                        </div>
                        <div>
                          <label className={labelCls}>
                            ⚓ Якорь на точку входа
                            <Tip text="Все уровни строятся от цены заполнения L(0). После SL перезаходит маркетом из SafeZone." />
                          </label>
                          <Toggle
                            options={[{ label: 'Выкл', value: 'false' }, { label: '⚓ Вкл', value: 'true' }]}
                            value={String(config.matrix_rebuild_from_entry ?? false)}
                            onChange={v => patch(v === 'true' ? { matrix_rebuild_from_entry: true, relative_slots: false } : { matrix_rebuild_from_entry: false })}
                            optionColors={{ true: 'bg-violet-700 text-white' }}
                          />
                        </div>
                        <div>
                          <label className={labelCls}>
                            ↻ Относительные слоты (Novabot)
                            <Tip text="После срабатывания SL внутреннего слота остальные перенумеровываются к входу, а следующий слот берётся по параметрам освободившейся глубины. Взаимоисключимо с перестройкой сетки." />
                          </label>
                          <Toggle
                            options={[{ label: 'Выкл', value: 'false' }, { label: '↻ Вкл', value: 'true' }]}
                            value={String(config.relative_slots ?? false)}
                            onChange={v => patch(v === 'true' ? { relative_slots: true, matrix_rebuild_on_sl: false, matrix_rebuild_from_entry: false } : { relative_slots: false })}
                            optionColors={{ true: 'bg-emerald-700 text-white' }}
                          />
                        </div>
                      </div>
                    </div>
                  )}
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors.

- [ ] **Step 3: Manual check**

As an admin, switch to ALS, open "3. Параметры": confirm the new ALS-specific fields
render with their defaults (Safe-Zone 1.5, ×K 1, Min ROI 3, BE 1, Unsafe% 0/80, TP
control off, Initial Stop BE off). Switch back to Matrix: confirm the original fields
reappear with their own values intact (not clobbered by the ALS branch).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Add ALS-specific fields to 3. Параметры tab"
```

---

### Task 6: Hide the single `act_type`/`act_value` activation condition for ALS

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx:910-986`

- [ ] **Step 1: Wrap the activation-condition cell, adjust the grid**

Current (`HedgeBotForm.tsx:910-912`, the opening of the two-column row):

```tsx
              {/* Условие активации + Закрыть Safe — одна строка */}
              <div className={`grid grid-cols-2 gap-3${ha.force_activation ? ' opacity-40 pointer-events-none select-none' : ''}`}>
                <div>
                  <label className={labelCls}>
                    Активировать при
```

Replace with:

```tsx
              {/* Условие активации + Закрыть Safe — одна строка.
                  For ALS, threshold row 1 (see "2. Пороги") replaces act_type/act_value's
                  role, so that cell is hidden and "Если занят слот" takes the full row. */}
              <div className={`grid gap-3 ${isAls ? 'grid-cols-1' : 'grid-cols-2'}${ha.force_activation ? ' opacity-40 pointer-events-none select-none' : ''}`}>
                {!isAls && (
                <div>
                  <label className={labelCls}>
                    Активировать при
```

Then, right before the matching closing `</div>` of that same first cell (current
`HedgeBotForm.tsx:986`, immediately before the `<div>` that starts "Если занят слот"):

Current:

```tsx
                  </p>
                </div>

                <div>
                  <label className={labelCls}>
                    Если занят слот
```

Replace with:

```tsx
                  </p>
                </div>
                )}

                <div>
                  <label className={labelCls}>
                    Если занят слот
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors — the added `{!isAls && ( ... )}` wraps a complete, independently
balanced JSX subtree (the whole "Активировать при" `<div>...</div>` block), so no other
line in between needs touching.

- [ ] **Step 3: Manual check**

As an admin, switch to ALS, open "Активация" tab: confirm "Активировать при" is gone and
"Если занят слот" now spans the full row. Switch back to Matrix: confirm "Активировать
при" reappears in its original two-column layout.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Hide single act_type/act_value activation condition when ALS is selected"
```

---

### Task 7: Stop hardcoding `strategy_type: 'matrix'` in the submit payload

**Files:**
- Modify: `frontend/src/features/bots/components/HedgeBotForm.tsx:427-431`

- [ ] **Step 1: Replace the hardcoded line**

Current:

```tsx
      strategyConfig: {
        ...config,
        bot_kind:      'hedge',
        strategy_type: 'matrix',
        grid_levels:   totalMatrixLevels,
        grid_step_pct: belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0,
        signal_filter: false,
```

Replace with:

```tsx
      strategyConfig: {
        ...config,
        bot_kind:      'hedge',
        strategy_type: config.strategy_type ?? 'matrix',
        // grid_levels/grid_step_pct are matrix-only derived values — harmless no-ops for
        // ALS (pkg/strategy's als.go seeds strategy_levels rows from als_thresholds, never
        // from grid_levels/grid_step_pct; see docs/superpowers/plans/2026-10-18-als-hedge-engine-core.md).
        grid_levels:   totalMatrixLevels,
        grid_step_pct: belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0,
        signal_filter: false,
```

- [ ] **Step 2: Build to confirm it compiles**

Run: `cd frontend && npx tsc --noEmit`
Expected: no new errors.

- [ ] **Step 3: Manual check**

As an admin: create a new bot with ALS selected and a couple of threshold rows edited
from the defaults, save, reopen it — confirm the strategy type stays "ALS" and the
edited threshold rows round-trip exactly (this exercises the whole chain down to the
API Persistence plan's backend columns, once that plan is also implemented — if it
isn't yet, this manual check instead confirms the *frontend* round-trip through
`bots.strategy_config`'s raw JSON, which works independently of the `strategies`-table
persistence Plan 4a adds).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/features/bots/components/HedgeBotForm.tsx
git commit -m "Send real strategy_type in hedge bot submit payload"
```

---

## Self-review notes (completed while writing this plan)

- **Spec coverage**: "Strategy type selector" (Task 3), "2. Пороги" tab (Task 4), "3.
  Параметры" ALS fields (Task 5), "Активация" act_type/act_value hiding (Task 6) — all
  four UI-change bullets from the design spec's "UI changes" section are covered. "1.
  Базовые" and "Завершение" are confirmed unchanged (no task needed — verified by reading
  the live file: "1. Базовые" has no matrix-specific fields to begin with, and
  "Завершение" is a separate outer tab never touched by any task here).
- **Placeholder scan**: no bare "add validation" steps; every step is a literal
  before/after block re-read from the live file (`HedgeBotForm.tsx`, `types.ts`) before
  writing it into this plan.
- **Type consistency**: `AlsThreshold`'s three field names (`trigger_type`,
  `trigger_pct`, `target_hedge_pct`) are used identically in `types.ts` (Task 1),
  `DEFAULT_ALS_THRESHOLDS`/the threshold-table helpers (Task 2), and the rendered table
  (Task 4) — and match `pkg/strategy/types.go`'s `AlsThreshold` struct's JSON tags
  exactly (`trigger_type`, `trigger_pct`, `target_hedge_pct`), confirmed by re-reading
  the Foundations plan's Task 2 before writing this plan's Task 1.
- **Deliberate non-DRY choice**: Task 4's new ALS tab duplicates the Депозит/Режим/Плечо
  JSX block (~55 lines) instead of extracting a shared helper used by both the Matrix and
  ALS branches. This trades a small amount of duplication for zero risk to the Matrix
  tab's already-working, live-traffic-serving JSX — extracting a shared component would
  touch code this plan has no other reason to modify. Accepted consciously, not an
  oversight.
- **Does NOT cover**: the actual sizing/threshold-evaluation math shown live in the UI
  (e.g., a preview of what volume a threshold would place) — out of scope per the design
  spec, which describes the thresholds table as plain editable config, not a live
  calculator.
