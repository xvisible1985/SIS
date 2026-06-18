import { useState, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'
import { RefreshCw } from 'lucide-react'
import { apiClient } from '../../api/client'
import { updateStrategyDefaults, invalidateStrategyDefaultsCache, updateCoinFilter, getCoinFilter, invalidateCoinFilterCache } from './api'
import type {
  GridDefaults, MatrixDefaults, HedgeBotDefaults, MatrixBotDefaults,
  GridStep, AllStrategyDefaults, CoinFilterSettings, MatrixLevel, MatrixEntryLevel,
} from './types'
import { CoinMultiPicker } from '../../components/common/CoinMultiPicker'
import { LeverageSlider } from '../../components/common/LeverageSlider'
import { Toggle, Tip } from '../../components/strategies/FormWidgets'

// ─── Constants ─────────────────────────────────────────────────────────────────

const ACT_TYPES       = ['От последнего ордера Main', 'Просадка Main-позиции', 'PNL Main-позиции', 'ROI Main-позиции']
const ACT_UNITS       = ['%', '%', '$', '%']
const CLOSE_TYPES     = ['По завершению цикла', 'Принять убыток не более, $', 'Приостановить и восстановить']
const DEACT_CLOSE_TYPES = ['PNL (общий), USDT', 'ROI (общий), %', 'В безубыток']
const DEACT_TYPES     = ['Просадка Main-позиции', 'PNL Main-позиции', 'ROI Main-позиции', 'От последнего ордера Main', 'Ожидать парного закрытия']
const DEACT_UNITS     = ['%', '$', '%', '%', '']

const DEFAULT_MATRIX_LEVELS: MatrixLevel[] = [
  { direction: 'below', price_step_pct: -1.5, size_pct: 5,  stop_pct: -3.0, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 2.5 },
  { direction: 'below', price_step_pct: -3.0, size_pct: 8,  stop_pct: -2.5, stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 3.5 },
  { direction: 'below', price_step_pct: -5.0, size_pct: 12, stop_pct: null,  stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 4.0 },
  { direction: 'above', price_step_pct: 2.0,  size_pct: 5,  stop_pct: -3.5, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 3.0 },
  { direction: 'above', price_step_pct: 4.0,  size_pct: 8,  stop_pct: -3.0, stop_cond_pct: -1.5, stop_replace_pct: -1.5, tp_pct: 4.5 },
]
const DEFAULT_MATRIX_ENTRY: MatrixEntryLevel = {
  price_step_pct: null, size_pct: 10, stop_pct: -5.0, stop_cond_pct: -2.0, stop_replace_pct: 1.0, tp_pct: 2.0,
}

// ─── Shared UI primitives ──────────────────────────────────────────────────────

const lbl      = 'block text-xs text-slate-400 mb-1'
const inputCls = 'w-full rounded border border-slate-700 bg-slate-800 px-2.5 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500'
const numCls   = () => 'bg-gray-900 border border-gray-700 rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-violet-500'

function NumInput({ value, onChange, className }: { value: number; onChange: (v: number) => void; className?: string }) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <input type="text" inputMode="decimal"
      value={draft !== null ? draft : value}
      onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n) }}
      onBlur={() => { if (draft !== null) { const n = parseFloat(draft); if (!isNaN(n)) onChange(n) } setDraft(null) }}
      onFocus={() => setDraft(String(value))}
      className={className ?? inputCls}
    />
  )
}

function MxNum({ value, onChange, cls }: { value: number; onChange: (v: number) => void; cls?: string }) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <input type="text" inputMode="decimal"
      value={draft !== null ? draft : value}
      onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n) }}
      onBlur={() => { if (draft !== null && draft !== '' && draft !== '-') { const n = parseFloat(draft); if (!isNaN(n)) onChange(n) } setDraft(null) }}
      onFocus={() => setDraft(String(value))}
      className={cls ?? numCls()}
    />
  )
}

function MxNull({ value, onChange }: { value: number | null; onChange: (v: number | null) => void }) {
  const [draft, setDraft] = useState<string | null>(null)
  const display = draft !== null ? draft : (value === null ? '' : String(value))
  return (
    <input type="text" inputMode="decimal" placeholder="—" value={display}
      onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); else if (e.target.value === '' || e.target.value === '-') onChange(null) }}
      onBlur={() => { if (draft !== null) { if (draft === '' || draft === '-') onChange(null); else { const n = parseFloat(draft); onChange(isNaN(n) ? null : n) } } setDraft(null) }}
      onFocus={() => setDraft(value === null ? '' : String(value))}
      className={`bg-gray-900 border rounded py-1 px-1 text-[11px] text-center w-full outline-none focus:border-violet-500 ${value === null && draft === null ? 'border-dashed border-gray-700 text-gray-500' : 'border-gray-700 text-gray-100'}`}
    />
  )
}

function SaveFooter({ onSave, saving, saved, error }: { onSave: () => void; saving: boolean; saved: boolean; error: string | null }) {
  return (
    <>
      {error && <div className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">{error}</div>}
      <div className="mt-5 flex items-center gap-3 border-t border-slate-800 pt-4">
        <button type="button" onClick={onSave} disabled={saving}
          className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50">
          {saving ? 'Сохранение…' : 'Сохранить'}
        </button>
        {saved && <span className="text-sm text-emerald-400">Сохранено ✓</span>}
      </div>
    </>
  )
}

function SubTabs<T extends string>({ tabs, active, onChange }: { tabs: { id: T; label: string }[]; active: T; onChange: (t: T) => void }) {
  return (
    <div className="flex gap-0 rounded-lg border border-white/[.06] bg-white/[.015] p-0.5">
      {tabs.map(t => (
        <button key={t.id} type="button" onClick={() => onChange(t.id)}
          className={`flex-1 rounded-md py-1.5 text-[11px] font-semibold transition-colors ${active === t.id ? 'bg-white/[.07] text-slate-100 shadow-sm' : 'text-slate-400 hover:text-slate-200'}`}>
          {t.label}
        </button>
      ))}
    </div>
  )
}

function ToggleSw({ enabled, onToggle }: { enabled: boolean; onToggle: () => void }) {
  return (
    <button type="button" onClick={onToggle}
      className={`w-9 h-5 rounded-full relative transition-colors shrink-0 ${enabled ? 'bg-violet-500' : 'bg-gray-700'}`}>
      <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${enabled ? 'translate-x-4' : 'translate-x-0'}`} />
    </button>
  )
}

function EnumPicker({ options, value, onChange }: { options: string[]; value: number; onChange: (v: number) => void }) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState({ top: 0, left: 0, width: 0 })
  const btnRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      if (!btnRef.current?.contains(e.target as Node) && !menuRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])
  const toggle = () => {
    if (!open && btnRef.current) { const r = btnRef.current.getBoundingClientRect(); setPos({ top: r.bottom + 4, left: r.left, width: r.width }) }
    setOpen(o => !o)
  }
  return (
    <div className="relative w-full">
      <button ref={btnRef} type="button" onClick={toggle}
        className="w-full flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-black/[.25] px-2.5 py-[9px] text-[12px] text-slate-200 outline-none hover:border-white/[.16]">
        <span className="flex-1 text-left truncate">{options[value] ?? '—'}</span>
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" className="shrink-0 opacity-40"><path d="M6 9l6 6 6-6"/></svg>
      </button>
      {open && createPortal(
        <div ref={menuRef} className="fixed z-[9999] rounded-[9px] p-1 flex flex-col gap-px"
          style={{ top: pos.top, left: pos.left, minWidth: Math.max(pos.width, 260), background: '#181b28', border: '1px solid rgba(255,255,255,.22)', boxShadow: '0 8px 32px rgba(0,0,0,.85)' }}>
          {options.map((label, i) => (
            <button key={i} type="button" onClick={() => { onChange(i); setOpen(false) }}
              className={`flex items-center gap-2 px-[9px] py-[7px] text-[12px] font-medium text-[#cfd5e1] rounded-[6px] w-full text-left hover:bg-white/[.06] ${value === i ? 'bg-white/[.05] text-white' : ''}`}>
              <span className={`w-[5px] h-[5px] rounded-full shrink-0 ${value === i ? 'bg-violet-400' : 'bg-transparent'}`} />
              {label}
            </button>
          ))}
        </div>,
        document.body
      )}
    </div>
  )
}

// ─── Matrix level table (shared by Matrix / HedgeBot / MatrixBot sections) ─────

function MatrixTable({
  levels, entry,
  onUpdateLevel, onUpdateEntry, onAddAbove, onAddBelow, onRemoveLevel,
}: {
  levels: MatrixLevel[]
  entry: MatrixEntryLevel
  onUpdateLevel: (dir: 'above' | 'below', idx: number, field: keyof MatrixLevel, v: number | string | null | boolean) => void
  onUpdateEntry: (field: keyof MatrixEntryLevel, v: number | string | null | boolean) => void
  onAddAbove: () => void
  onAddBelow: () => void
  onRemoveLevel: (dir: 'above' | 'below', idx: number) => void
}) {
  const above = levels.filter(l => l.direction === 'above')
  const below = levels.filter(l => l.direction === 'below')
  const col = 'grid grid-cols-[30px_1fr_1fr_6px_1fr_1fr_1fr_1fr_42px_18px] gap-[3px] items-center'
  const sep = <div className="flex items-center justify-center"><div className="w-px h-[20px] bg-gray-700/60" /></div>

  const hdrs = () => (
    <div className={`${col} px-1 pb-1`}>
      <span /><span className="text-[8px] text-gray-400 text-center">Шаг %</span><span className="text-[8px] text-gray-400 text-center">Объём %</span>
      <span />
      <span className="text-[8px] text-gray-400 text-center">Стоп %</span><span className="text-[8px] text-gray-400 text-center">Усл %</span>
      <span className="text-[8px] text-gray-400 text-center">Замена %</span><span className="text-[8px] text-gray-400 text-center">ТП %</span>
      <span className="text-[8px] text-gray-400 text-center">Тип</span><span />
    </div>
  )

  const typeBtn = (isV: boolean, onToggle: () => void) => (
    <button type="button" onClick={onToggle}
      className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${isV ? 'bg-violet-950/60 text-violet-300 border border-violet-800/50' : 'bg-gray-800 text-gray-500 border border-gray-700 hover:text-gray-300'}`}>
      {isV ? 'Virtual' : 'Broker'}
    </button>
  )

  const row = (direction: 'above' | 'below', lv: MatrixLevel, dirIdx: number, num: number, canDel: boolean) => {
    const isAbove = direction === 'above'
    const badge = isAbove
      ? 'bg-emerald-900 text-emerald-300 text-[8px] font-bold rounded text-center py-0.5'
      : 'bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5'
    const rowBg = isAbove ? 'bg-emerald-950/20' : 'bg-blue-950/30'
    return (
      <div key={`${direction}-${dirIdx}`} className={`${col} px-1 py-1 rounded mb-0.5 ${rowBg}`}>
        <div className={badge}>{isAbove ? `L(${num})` : `L(-${num})`}</div>
        <MxNum value={lv.price_step_pct} onChange={v => onUpdateLevel(direction, dirIdx, 'price_step_pct', v)} cls={numCls()} />
        <MxNum value={lv.size_pct}       onChange={v => onUpdateLevel(direction, dirIdx, 'size_pct', v)}       cls={numCls()} />
        {sep}
        <MxNull value={lv.stop_pct}         onChange={v => onUpdateLevel(direction, dirIdx, 'stop_pct', v)} />
        <MxNull value={lv.stop_cond_pct}    onChange={v => onUpdateLevel(direction, dirIdx, 'stop_cond_pct', v)} />
        <MxNull value={lv.stop_replace_pct} onChange={v => onUpdateLevel(direction, dirIdx, 'stop_replace_pct', v)} />
        <MxNull value={lv.tp_pct}           onChange={v => onUpdateLevel(direction, dirIdx, 'tp_pct', v)} />
        {typeBtn(lv.order_type === 'virtual', () => onUpdateLevel(direction, dirIdx, 'order_type', lv.order_type === 'virtual' ? 'exchange' : 'virtual'))}
        {canDel ? <button type="button" onClick={() => onRemoveLevel(direction, dirIdx)} className="text-center text-xs text-red-500 hover:text-red-400">✕</button> : <span />}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <div>
        <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pb-1 border-b border-gray-800 mb-1">
          <span>Выше точки входа</span>
          <span className="bg-emerald-900/60 text-emerald-400 rounded px-1.5 py-0.5 text-[8px]">{above.length} уровней</span>
        </div>
        <button type="button" onClick={onAddAbove}
          className="w-full border border-dashed border-emerald-900 text-emerald-700 rounded py-1 text-[10px] hover:border-emerald-700 hover:text-emerald-400 transition-colors mb-2">
          + Добавить уровень выше
        </button>
        {hdrs()}
        {[...above].reverse().map((lv, revIdx) => {
          const dirIdx = above.length - 1 - revIdx
          return row('above', lv, dirIdx, dirIdx + 1, above.length > 1)
        })}
      </div>

      <div className="border border-yellow-700/60 bg-yellow-950/10 rounded-lg p-2">
        <div className="text-[9px] text-yellow-600 uppercase tracking-wider mb-1.5">⬡ Нулевой уровень — точка входа L(0)</div>
        {hdrs()}
        <div className={`${col} px-1 py-1 rounded bg-yellow-950/20`}>
          <div className="text-[8px] font-bold rounded text-center py-0.5 bg-yellow-800/60 text-yellow-200">L(0)</div>
          <MxNull value={entry.price_step_pct ?? null} onChange={v => onUpdateEntry('price_step_pct', v)} />
          <MxNum  value={entry.size_pct}                onChange={v => onUpdateEntry('size_pct', v)} cls={numCls()} />
          {sep}
          <MxNull value={entry.stop_pct}         onChange={v => onUpdateEntry('stop_pct', v)} />
          <MxNull value={entry.stop_cond_pct}    onChange={v => onUpdateEntry('stop_cond_pct', v)} />
          <MxNull value={entry.stop_replace_pct} onChange={v => onUpdateEntry('stop_replace_pct', v)} />
          <MxNull value={entry.tp_pct}           onChange={v => onUpdateEntry('tp_pct', v)} />
          {typeBtn(entry.order_type === 'virtual', () => onUpdateEntry('order_type', entry.order_type === 'virtual' ? 'exchange' : 'virtual'))}
          <span />
        </div>
      </div>

      <div>
        {hdrs()}
        {below.map((lv, dirIdx) => row('below', lv, dirIdx, dirIdx + 1, below.length > 1))}
        <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pt-1 border-t border-gray-800 mt-1 mb-1">
          <span>Ниже точки входа</span>
          <span className="bg-blue-900/60 text-blue-400 rounded px-1.5 py-0.5 text-[8px]">{below.length} уровней</span>
        </div>
        <button type="button" onClick={onAddBelow}
          className="w-full border border-dashed border-blue-900 text-blue-600 rounded py-1 text-[10px] hover:border-blue-700 hover:text-blue-400 transition-colors">
          + Добавить уровень ниже
        </button>
      </div>
    </div>
  )
}

// ─── useMatrixState ────────────────────────────────────────────────────────────

function useMatrixState(initLevels: MatrixLevel[], initEntry: MatrixEntryLevel) {
  const [levels, setLevels] = useState<MatrixLevel[]>(initLevels)
  const [entry,  setEntry]  = useState<MatrixEntryLevel>(initEntry)

  const above = levels.filter(l => l.direction === 'above')
  const below = levels.filter(l => l.direction === 'below')

  function addAbove() {
    const last = above[above.length - 1]
    setLevels(ls => [...ls, { direction: 'above', price_step_pct: last ? +(last.price_step_pct + 2).toFixed(1) : 2.0, size_pct: last ? +(last.size_pct + 2).toFixed(1) : 5, stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null }])
  }
  function addBelow() {
    const last = below[below.length - 1]
    setLevels(ls => [...ls, { direction: 'below', price_step_pct: last ? +(last.price_step_pct - 1.5).toFixed(1) : -1.5, size_pct: last ? +(last.size_pct + 3).toFixed(1) : 5, stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null }])
  }
  function removeLevel(direction: 'above' | 'below', idx: number) {
    const dirLvs = levels.filter(l => l.direction === direction)
    if (dirLvs.length <= 1) return
    let removed = false
    setLevels(ls => ls.filter(l => {
      if (l.direction !== direction) return true
      const ti = ls.filter(x => x.direction === direction).indexOf(l)
      if (ti === idx && !removed) { removed = true; return false }
      return true
    }))
  }
  function updateLevel(direction: 'above' | 'below', idx: number, field: keyof MatrixLevel, value: number | string | boolean | null) {
    let cnt = 0
    setLevels(ls => ls.map(l => {
      if (l.direction !== direction) return l
      if (cnt++ === idx) return { ...l, [field]: value }
      return l
    }))
  }
  function updateEntry(field: keyof MatrixEntryLevel, value: number | string | boolean | null) {
    setEntry(e => ({ ...e, [field]: value }))
  }

  function reset(newLevels: MatrixLevel[], newEntry: MatrixEntryLevel) {
    setLevels(newLevels)
    setEntry(newEntry)
  }

  return { levels, entry, above, below, addAbove, addBelow, removeLevel, updateLevel, updateEntry, reset }
}

// ─── Matrix params panel (shared) ─────────────────────────────────────────────

function MatrixParamsPanel<T extends { safe_zone_pct?: number; protected_build?: boolean; matrix_rebuild_on_sl?: boolean; matrix_rebuild_from_entry?: boolean }>({
  d, setD,
}: { d: T; setD: (fn: (p: T) => T) => void }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={lbl}>Safe-Zone от SL %<Tip text="После SL — зона вокруг цены, в которой новые ордера матрицы не выставляются." /></label>
          <NumInput value={d.safe_zone_pct ?? 1.5} onChange={v => setD(p => ({ ...p, safe_zone_pct: v }))} />
        </div>
        <div>
          <label className={lbl}>🔒 Защищённое построение<Tip text="Следующий уровень выставляется только после того, как предыдущий прикрыт стопом." /></label>
          <Toggle
            options={[{ label: 'Выкл', value: 'false' }, { label: '🔒 Вкл', value: 'true' }]}
            value={String(d.protected_build ?? false)}
            onChange={v => setD(p => ({ ...p, protected_build: v === 'true' }))}
            optionColors={{ true: 'bg-amber-700 text-white' }}
          />
        </div>
        <div>
          <label className={lbl}>⟳ Перестройка от SZ<Tip text="После SL немедленно переставляет уровни от нижней границы SafeZone." /></label>
          <Toggle
            options={[{ label: 'Выкл', value: 'false' }, { label: '⟳ Вкл', value: 'true' }]}
            value={String(d.matrix_rebuild_on_sl ?? false)}
            onChange={v => setD(p => ({ ...p, matrix_rebuild_on_sl: v === 'true' }))}
            optionColors={{ true: 'bg-blue-700 text-white' }}
          />
        </div>
        <div>
          <label className={lbl}>⚓ Якорь на точку входа<Tip text="Все уровни строятся от цены заполнения L(0). После SL перезаходит маркетом из SafeZone." /></label>
          <Toggle
            options={[{ label: 'Выкл', value: 'false' }, { label: '⚓ Вкл', value: 'true' }]}
            value={String(d.matrix_rebuild_from_entry ?? false)}
            onChange={v => setD(p => ({ ...p, matrix_rebuild_from_entry: v === 'true' }))}
            optionColors={{ true: 'bg-violet-700 text-white' }}
          />
        </div>
      </div>
    </div>
  )
}

// ─── Common strategy base panel (для ввода leverage + deposit + basic toggles) ─

function StratBasicPanel<T extends {
  leverage?: number; grid_size_usdt?: number; entry_order_type?: string;
  margin_type?: string; grid_active?: number; max_stop_active?: number;
  after_stop_mode?: string; max_cycles?: number;
}>({ d, setD, showHedgeMode }: { d: T; setD: (fn: (p: T) => T) => void; showHedgeMode?: boolean }) {
  const leverage = d.leverage ?? 5
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={lbl}>Депозит (USDT)</label>
          <NumInput value={d.grid_size_usdt ?? 100} onChange={v => setD(p => ({ ...p, grid_size_usdt: v }))} />
        </div>
        <div>
          <label className={lbl}>Плечо&nbsp;<span className="font-bold text-white">×{leverage}</span></label>
          <LeverageSlider value={leverage} min={1} max={100} onChange={v => setD(p => ({ ...p, leverage: v }))} className="mt-2" />
          <div className="flex justify-between text-[10px] text-slate-600 mt-0.5"><span>×1</span><span>×100</span></div>
        </div>
      </div>

      <div>
        <label className={lbl}>Тип входного ордера<Tip text="Limit — мейкер, дешевле. Stop Market — тейкер, гарантирует исполнение." /></label>
        <Toggle
          options={[{ label: 'Limit', value: 'limit' }, { label: 'Stop Market', value: 'stop_market' }]}
          value={d.entry_order_type ?? 'limit'}
          onChange={v => setD(p => ({ ...p, entry_order_type: v }))}
        />
      </div>

      <div>
        <label className={lbl}>Тип маржи<Tip text="Isolated — убыток ограничен суммой под позицию. Cross — участвует вся свободная маржа." /></label>
        <Toggle
          options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
          value={d.margin_type ?? 'isolated'}
          onChange={v => setD(p => ({ ...p, margin_type: v }))}
        />
      </div>

      {showHedgeMode && (
        <div>
          <label className={lbl}>Hedge Mode<Tip text="Long и Short по одной монете в отдельных слотах." /></label>
          <Toggle
            options={[{ label: 'Нет', value: 'false' }, { label: 'Да (Hedge)', value: 'true' }]}
            value={String((d as { hedge_mode?: boolean }).hedge_mode ?? false)}
            onChange={v => setD(p => ({ ...p, hedge_mode: v === 'true' }))}
          />
        </div>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className={lbl}>Активных слотов<Tip text="0 = все сразу. Скользящее окно ордеров на бирже." /></label>
          <NumInput value={d.grid_active ?? 0} onChange={v => setD(p => ({ ...p, grid_active: Math.max(0, Math.round(v)) }))} />
        </div>
        <div>
          <label className={lbl}>Условных стопов<Tip text="0 = без лимита. Bybit ограничивает conditional-ордера на символ." /></label>
          <NumInput value={d.max_stop_active ?? 0} onChange={v => setD(p => ({ ...p, max_stop_active: Math.max(0, Math.round(v)) }))} />
        </div>
        <div>
          <label className={lbl}>После стопа</label>
          <Toggle
            options={[{ label: 'Рестарт', value: 'restart' }, { label: 'Удалить', value: 'delete' }]}
            value={d.after_stop_mode ?? 'restart'}
            onChange={v => setD(p => ({ ...p, after_stop_mode: v }))}
          />
        </div>
        <div>
          <label className={lbl}>Макс. циклов<Tip text="0 = не ограничено." /></label>
          <NumInput value={d.max_cycles ?? 0} onChange={v => setD(p => ({ ...p, max_cycles: Math.max(0, Math.round(v)) }))} />
        </div>
      </div>
    </div>
  )
}

// ─── GridSection ───────────────────────────────────────────────────────────────

type GridTab = 'entry' | 'grid' | 'exit'

function GridSection({ initial, onSaved }: { initial: GridDefaults; onSaved: () => void }) {
  const [d, setD]                     = useState<GridDefaults>(initial)
  const [tab, setTab]                 = useState<GridTab>('entry')
  const [trailingEnabled, setTrailingEnabled] = useState((initial.trailing_activation_pct ?? 0) > 0)
  const [slEnabled, setSlEnabled]     = useState((initial.sl_pct ?? 0) < 0)
  const [saving, setSaving]           = useState(false)
  const [saved, setSaved]             = useState(false)
  const [error, setError]             = useState<string | null>(null)

  useEffect(() => {
    setD(initial)
    setTrailingEnabled((initial.trailing_activation_pct ?? 0) > 0)
    setSlEnabled((initial.sl_pct ?? 0) < 0)
  }, [initial])

  function patchStep(i: number, field: keyof GridStep, value: number) {
    setD(p => { const steps = [...(p.steps ?? [])]; steps[i] = { ...steps[i], [field]: value }; return { ...p, steps } })
  }
  function addStep() {
    setD(p => ({ ...p, steps: [...(p.steps ?? []), { price_move_pct: -1.0, size_pct: 50 }] }))
  }
  function removeStep(i: number) {
    setD(p => ({ ...p, steps: (p.steps ?? []).filter((_, idx) => idx !== i) }))
  }

  async function handleSave() {
    const toSave: GridDefaults = {
      ...d,
      sl_pct: slEnabled ? (d.sl_pct ?? -5) : 0,
      trailing_activation_pct: trailingEnabled ? (d.trailing_activation_pct ?? 1.5) : 0,
      trailing_callback_pct:   trailingEnabled ? (d.trailing_callback_pct ?? 0.5)  : 0,
    }
    setSaving(true); setError(null)
    try {
      await updateStrategyDefaults('grid', toSave); setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setSaving(false) }
  }

  const deposit = d.grid_size_usdt ?? 100
  const tabs: { id: GridTab; label: string }[] = [
    { id: 'entry', label: '1. Базовые' },
    { id: 'grid',  label: '2. Сетка'   },
    { id: 'exit',  label: '3. Завершение' },
  ]

  return (
    <div className="flex flex-col gap-4">
      <SubTabs tabs={tabs} active={tab} onChange={setTab} />

      {tab === 'entry' && (
        <div className="flex flex-col gap-4">
          <div>
            <label className={lbl}>Направление<Tip text="Long — покупки. Short — продажи. Both — оба направления." /></label>
            <Toggle
              options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }, { label: 'Both', value: 'both' }]}
              value={d.direction ?? 'long'}
              onChange={v => setD(p => ({ ...p, direction: v as GridDefaults['direction'] }))}
              optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white', both: 'bg-blue-700 text-white' }}
            />
          </div>
          <div>
            <label className={lbl}>Тип входного ордера<Tip text="Limit — мейкер. Stop Market — тейкер, гарантирует исполнение." /></label>
            <Toggle
              options={[{ label: 'Limit', value: 'limit' }, { label: 'Stop Market', value: 'stop_market' }]}
              value={d.entry_order_type ?? 'limit'}
              onChange={v => setD(p => ({ ...p, entry_order_type: v as GridDefaults['entry_order_type'] }))}
            />
          </div>
          <div>
            <label className={lbl}>Тип маржи<Tip text="Isolated — убыток ограничен суммой под позицию. Cross — участвует вся свободная маржа." /></label>
            <Toggle
              options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
              value={d.margin_type ?? 'isolated'}
              onChange={v => setD(p => ({ ...p, margin_type: v as GridDefaults['margin_type'] }))}
            />
          </div>
          <div>
            <label className={lbl}>Hedge Mode<Tip text="Long и Short по одной монете в отдельных слотах." /></label>
            <Toggle
              options={[{ label: 'Нет', value: 'false' }, { label: 'Да', value: 'true' }]}
              value={String(d.hedge_mode ?? false)}
              onChange={v => setD(p => ({ ...p, hedge_mode: v === 'true' }))}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={lbl}>Активных ордеров<Tip text="0 = все сразу. Скользящее окно." /></label>
              <NumInput value={d.grid_active ?? 0} onChange={v => setD(p => ({ ...p, grid_active: Math.max(0, Math.round(v)) }))} />
            </div>
            <div>
              <label className={lbl}>Условных стопов<Tip text="0 = без лимита." /></label>
              <NumInput value={d.max_stop_active ?? 0} onChange={v => setD(p => ({ ...p, max_stop_active: Math.max(0, Math.round(v)) }))} />
            </div>
            <div>
              <label className={lbl}>После стопа</label>
              <Toggle
                options={[{ label: 'Рестарт', value: 'restart' }, { label: 'Удалить', value: 'delete' }]}
                value={d.after_stop_mode ?? 'restart'}
                onChange={v => setD(p => ({ ...p, after_stop_mode: v as GridDefaults['after_stop_mode'] }))}
              />
            </div>
            <div>
              <label className={lbl}>Макс. циклов<Tip text="0 = не ограничено." /></label>
              <NumInput value={d.max_cycles ?? 0} onChange={v => setD(p => ({ ...p, max_cycles: Math.max(0, Math.round(v)) }))} />
            </div>
          </div>
        </div>
      )}

      {tab === 'grid' && (
        <div className="flex flex-col gap-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={lbl}>Депозит (USDT)</label>
              <NumInput value={deposit} onChange={v => setD(p => ({ ...p, grid_size_usdt: v }))} />
            </div>
            <div>
              <label className={lbl}>Плечо&nbsp;<span className="font-bold text-white">×{d.leverage ?? 5}</span></label>
              <LeverageSlider value={d.leverage ?? 5} min={1} max={100} onChange={v => setD(p => ({ ...p, leverage: v }))} className="mt-2" />
              <div className="flex justify-between text-[10px] text-slate-600 mt-0.5"><span>×1</span><span>×100</span></div>
            </div>
          </div>

          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-slate-500 mb-1.5">Шаги усреднения</div>
            <div className="grid grid-cols-[24px_1fr_1fr_56px_18px] gap-1.5 text-[9px] text-slate-500 mb-1 px-0.5">
              <div className="text-center">#</div><div className="text-center">% от входа</div>
              <div className="text-center">% депоз.</div><div className="text-center">USDT</div><div />
            </div>
            <div className="space-y-0.5">
              {(d.steps ?? []).map((step, i) => (
                <div key={i} className="grid grid-cols-[24px_1fr_1fr_56px_18px] gap-1.5 items-center py-0.5 border-b border-slate-800/50 last:border-0">
                  <div className="text-center text-[9px] font-semibold text-indigo-400/70">L{i + 1}</div>
                  <MxNum value={step.price_move_pct} onChange={v => patchStep(i, 'price_move_pct', v)} cls="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-[11px] text-slate-200 text-center w-full focus:outline-none focus:border-indigo-500" />
                  <MxNum value={step.size_pct}       onChange={v => patchStep(i, 'size_pct', v)}       cls="bg-slate-800 border border-slate-700 rounded px-2 py-1 text-[11px] text-slate-200 text-center w-full focus:outline-none focus:border-indigo-500" />
                  <div className="rounded border border-slate-700/50 bg-slate-800/40 px-2 py-1 text-[11px] text-slate-500 text-center">
                    {+(step.size_pct / 100 * deposit).toFixed(1)}
                  </div>
                  <button type="button" onClick={() => removeStep(i)} className="text-center text-sm text-rose-600 hover:text-rose-400">✕</button>
                </div>
              ))}
              {(d.steps ?? []).length === 0 && <div className="py-3 text-center text-xs text-slate-600">Нет уровней</div>}
            </div>
            <button type="button" onClick={addStep}
              className="mt-2 w-full border border-dashed border-indigo-900 text-indigo-600 rounded py-1 text-[11px] hover:border-indigo-700 hover:text-indigo-400 transition-colors">
              + Добавить шаг
            </button>
          </div>
        </div>
      )}

      {tab === 'exit' && (
        <div className="flex flex-col gap-3">
          <div className="rounded-lg border border-emerald-900/50 bg-emerald-950/20 p-3 space-y-3">
            <span className="text-xs font-semibold text-emerald-400">Take Profit</span>
            <div>
              <label className={lbl}>TP %<Tip text="% прибыли от средней цены входа для закрывающего ордера." /></label>
              <NumInput value={d.tp_pct ?? 2.0} onChange={v => setD(p => ({ ...p, tp_pct: v }))} />
            </div>
            <div className="border-t border-emerald-900/40 pt-2 space-y-2">
              <div className="flex items-center gap-2">
                <ToggleSw enabled={trailingEnabled} onToggle={() => setTrailingEnabled(v => !v)} />
                <span className="text-[10px] text-slate-400 font-semibold uppercase tracking-wider">Трейлинг-стоп</span>
              </div>
              {trailingEnabled && (
                <div className="grid grid-cols-2 gap-2">
                  <div>
                    <label className={lbl}>Активация %</label>
                    <NumInput value={d.trailing_activation_pct ?? 1.5} onChange={v => setD(p => ({ ...p, trailing_activation_pct: v }))} />
                  </div>
                  <div>
                    <label className={lbl}>Callback %</label>
                    <NumInput value={d.trailing_callback_pct ?? 0.5} onChange={v => setD(p => ({ ...p, trailing_callback_pct: v }))} />
                  </div>
                </div>
              )}
            </div>
          </div>

          <div className="rounded-lg border border-rose-900/50 bg-rose-950/20 p-3 space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-rose-400">Stop Loss</span>
              <ToggleSw enabled={slEnabled} onToggle={() => { setSlEnabled(v => !v); if (slEnabled) setD(p => ({ ...p, sl_pct: 0 })); else setD(p => ({ ...p, sl_pct: -5.0 })) }} />
            </div>
            {slEnabled ? (
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={lbl}>SL %<Tip text="Отрицательное значение = ниже средней для Long (например, -5)." /></label>
                  <NumInput value={d.sl_pct ?? -5.0} onChange={v => setD(p => ({ ...p, sl_pct: v }))} />
                </div>
                <div>
                  <label className={lbl}>Тип<Tip text="На бирже — стоп-ордер Bybit. Программный — сервис следит через WS." /></label>
                  <Toggle
                    options={[{ label: 'На бирже', value: 'conditional' }, { label: 'Программный', value: 'programmatic' }]}
                    value={d.sl_type ?? 'conditional'}
                    onChange={v => setD(p => ({ ...p, sl_type: v as GridDefaults['sl_type'] }))}
                  />
                </div>
              </div>
            ) : (
              <div className="text-[11px] text-gray-600 italic">Без стоп-лосса — позиция закрывается только по TP или вручную</div>
            )}
          </div>
        </div>
      )}

      <SaveFooter onSave={handleSave} saving={saving} saved={saved} error={error} />
    </div>
  )
}

// ─── MatrixSection ─────────────────────────────────────────────────────────────

type MatrixTab = 'entry' | 'matrix' | 'params'

function MatrixSection({ initial, onSaved }: { initial: MatrixDefaults; onSaved: () => void }) {
  const [d, setD]       = useState<MatrixDefaults>(initial)
  const [tab, setTab]   = useState<MatrixTab>('entry')
  const [saving, setSaving] = useState(false)
  const [saved,  setSaved]  = useState(false)
  const [error,  setError]  = useState<string | null>(null)

  const mx = useMatrixState(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)

  useEffect(() => {
    setD(initial)
    mx.reset(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)
  }, [initial]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSave() {
    const toSave: MatrixDefaults = { ...d, matrix_levels: mx.levels, matrix_entry_level: mx.entry }
    setSaving(true); setError(null)
    try {
      await updateStrategyDefaults('matrix', toSave); setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setSaving(false) }
  }

  const tabs: { id: MatrixTab; label: string }[] = [
    { id: 'entry',  label: '1. Базовые'   },
    { id: 'matrix', label: '2. Матрица'   },
    { id: 'params', label: '3. Параметры' },
  ]

  return (
    <div className="flex flex-col gap-4">
      <SubTabs tabs={tabs} active={tab} onChange={setTab} />

      {tab === 'entry' && (
        <div className="flex flex-col gap-4">
          <div>
            <label className={lbl}>Направление<Tip text="Long — покупки. Short — продажи. Both — оба направления." /></label>
            <Toggle
              options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }, { label: 'Both', value: 'both' }]}
              value={d.direction ?? 'long'}
              onChange={v => setD(p => ({ ...p, direction: v as MatrixDefaults['direction'] }))}
              optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white', both: 'bg-blue-700 text-white' }}
            />
          </div>
          <div>
            <label className={lbl}>Тип входного ордера</label>
            <Toggle
              options={[{ label: 'Limit', value: 'limit' }, { label: 'Stop Market', value: 'stop_market' }]}
              value={d.entry_order_type ?? 'limit'}
              onChange={v => setD(p => ({ ...p, entry_order_type: v as MatrixDefaults['entry_order_type'] }))}
            />
          </div>
          <div>
            <label className={lbl}>Тип маржи</label>
            <Toggle
              options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
              value={d.margin_type ?? 'isolated'}
              onChange={v => setD(p => ({ ...p, margin_type: v as MatrixDefaults['margin_type'] }))}
            />
          </div>
          <div>
            <label className={lbl}>Hedge Mode</label>
            <Toggle
              options={[{ label: 'Нет', value: 'false' }, { label: 'Да', value: 'true' }]}
              value={String(d.hedge_mode ?? false)}
              onChange={v => setD(p => ({ ...p, hedge_mode: v === 'true' }))}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={lbl}>Депозит (USDT)</label>
              <NumInput value={d.grid_size_usdt ?? 100} onChange={v => setD(p => ({ ...p, grid_size_usdt: v }))} />
            </div>
            <div>
              <label className={lbl}>Плечо&nbsp;<span className="font-bold text-white">×{d.leverage ?? 5}</span></label>
              <LeverageSlider value={d.leverage ?? 5} min={1} max={100} onChange={v => setD(p => ({ ...p, leverage: v }))} className="mt-2" />
              <div className="flex justify-between text-[10px] text-slate-600 mt-0.5"><span>×1</span><span>×100</span></div>
            </div>
            <div>
              <label className={lbl}>Активных слотов<Tip text="0 = все сразу." /></label>
              <NumInput value={d.grid_active ?? 0} onChange={v => setD(p => ({ ...p, grid_active: Math.max(0, Math.round(v)) }))} />
            </div>
            <div>
              <label className={lbl}>Условных стопов<Tip text="0 = без лимита." /></label>
              <NumInput value={d.max_stop_active ?? 0} onChange={v => setD(p => ({ ...p, max_stop_active: Math.max(0, Math.round(v)) }))} />
            </div>
            <div>
              <label className={lbl}>После стопа</label>
              <Toggle
                options={[{ label: 'Рестарт', value: 'restart' }, { label: 'Удалить', value: 'delete' }]}
                value={d.after_stop_mode ?? 'restart'}
                onChange={v => setD(p => ({ ...p, after_stop_mode: v as MatrixDefaults['after_stop_mode'] }))}
              />
            </div>
            <div>
              <label className={lbl}>Макс. циклов<Tip text="0 = не ограничено." /></label>
              <NumInput value={d.max_cycles ?? 0} onChange={v => setD(p => ({ ...p, max_cycles: Math.max(0, Math.round(v)) }))} />
            </div>
          </div>
        </div>
      )}

      {tab === 'matrix' && (
        <MatrixTable
          levels={mx.levels} entry={mx.entry}
          onUpdateLevel={mx.updateLevel} onUpdateEntry={mx.updateEntry}
          onAddAbove={mx.addAbove} onAddBelow={mx.addBelow} onRemoveLevel={mx.removeLevel}
        />
      )}

      {tab === 'params' && <MatrixParamsPanel d={d} setD={setD} />}

      <SaveFooter onSave={handleSave} saving={saving} saved={saved} error={error} />
    </div>
  )
}

// ─── HedgeBotSection ───────────────────────────────────────────────────────────

type HedgeTab = 'strategy' | 'matrix' | 'params' | 'activation'

function HedgeBotSection({ initial, onSaved }: { initial: HedgeBotDefaults; onSaved: () => void }) {
  const [d, setD]     = useState<HedgeBotDefaults>(initial)
  const [tab, setTab] = useState<HedgeTab>('strategy')
  const [saving, setSaving] = useState(false)
  const [saved,  setSaved]  = useState(false)
  const [error,  setError]  = useState<string | null>(null)

  const mx = useMatrixState(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)

  useEffect(() => {
    setD(initial)
    mx.reset(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)
  }, [initial]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSave() {
    const toSave: HedgeBotDefaults = { ...d, matrix_levels: mx.levels, matrix_entry_level: mx.entry }
    setSaving(true); setError(null)
    try {
      await updateStrategyDefaults('bot_hedge', toSave); setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setSaving(false) }
  }

  const tabs: { id: HedgeTab; label: string }[] = [
    { id: 'strategy',   label: 'Стратегия' },
    { id: 'matrix',     label: 'Матрица'   },
    { id: 'params',     label: 'Параметры' },
    { id: 'activation', label: 'Активация' },
  ]

  const actType  = d.hedge_act_type  ?? 1
  const actValue = d.hedge_act_value ?? -4

  return (
    <div className="flex flex-col gap-4">
      <SubTabs tabs={tabs} active={tab} onChange={setTab} />

      {tab === 'strategy' && <StratBasicPanel d={d} setD={setD} showHedgeMode={false} />}

      {tab === 'matrix' && (
        <MatrixTable
          levels={mx.levels} entry={mx.entry}
          onUpdateLevel={mx.updateLevel} onUpdateEntry={mx.updateEntry}
          onAddAbove={mx.addAbove} onAddBelow={mx.addBelow} onRemoveLevel={mx.removeLevel}
        />
      )}

      {tab === 'params' && <MatrixParamsPanel d={d} setD={setD} />}

      {tab === 'activation' && (
        <div className="flex flex-col gap-5">
          <div>
            <label className={lbl}>Направление хеджа<Tip text="Long — хеджируем только лонги. Short — только шорты. Both — любую сторону." /></label>
            <Toggle
              options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }, { label: 'Both', value: 'both' }]}
              value={(d as { direction?: string }).direction ?? 'both'}
              onChange={v => setD(p => ({ ...p, direction: v }))}
              optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white' }}
            />
          </div>

          <div className="rounded-lg border border-amber-500/20 bg-amber-950/[.12] p-3 space-y-2">
            <div className="flex items-center gap-2.5">
              <ToggleSw enabled={d.hedge_force_activation ?? false} onToggle={() => setD(p => ({ ...p, hedge_force_activation: !(p.hedge_force_activation ?? false) }))} />
              <span className="text-[12px] font-semibold text-amber-300">
                Принудительная активация<Tip text="Хедж открывается немедленно для каждой подходящей позиции — условие активации ниже игнорируется." />
              </span>
            </div>
          </div>

          <div className={`grid grid-cols-2 gap-4${d.hedge_force_activation ? ' opacity-40 pointer-events-none select-none' : ''}`}>
            <div>
              <label className={lbl}>Активировать при<Tip text="Условие на Main-позиции." /></label>
              <EnumPicker options={ACT_TYPES} value={actType} onChange={v => setD(p => ({ ...p, hedge_act_type: v }))} />
              <div className="flex items-center gap-1.5 mt-2">
                {ACT_UNITS[actType] && <span className="text-[10px] text-slate-500">{ACT_UNITS[actType]}</span>}
                <NumInput value={actValue} onChange={v => setD(p => ({ ...p, hedge_act_value: v }))} />
              </div>
            </div>
            <div>
              <label className={lbl}>Если занят слот<Tip text="Что делать, если в направлении хеджа уже открыта стратегия." /></label>
              <EnumPicker options={CLOSE_TYPES} value={d.hedge_close_type ?? 0} onChange={v => setD(p => ({ ...p, hedge_close_type: v }))} />
              {(d.hedge_close_type ?? 0) === 1 && (
                <div className="flex items-center gap-1.5 mt-2">
                  <span className="text-[10px] text-slate-500">$</span>
                  <NumInput value={d.hedge_close_value ?? 10} onChange={v => setD(p => ({ ...p, hedge_close_value: v }))} />
                </div>
              )}
            </div>
          </div>

          <div className="text-[10px] font-semibold uppercase tracking-wider text-slate-500 pb-1 border-b border-white/[.05]">Деактивация</div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={lbl}>Закрыть обе позиции при<Tip text="Суммарный результат Main + хедж." /></label>
              <EnumPicker options={DEACT_CLOSE_TYPES} value={d.hedge_deact_close_type ?? 0} onChange={v => setD(p => ({ ...p, hedge_deact_close_type: v }))} />
              {(d.hedge_deact_close_type ?? 0) !== 2 && (
                <div className="flex items-center gap-1.5 mt-2">
                  <span className="text-[10px] text-slate-500">{(d.hedge_deact_close_type ?? 0) === 0 ? '$' : '%'}</span>
                  <NumInput value={d.hedge_deact_close_value ?? 50} onChange={v => setD(p => ({ ...p, hedge_deact_close_value: v }))} />
                </div>
              )}
            </div>
            <div>
              <label className={lbl}>Деактивировать хедж при<Tip text="Условие восстановления Main-позиции — закрывает только хедж." /></label>
              <EnumPicker options={DEACT_TYPES} value={d.hedge_deact_type ?? 0} onChange={v => setD(p => ({ ...p, hedge_deact_type: v }))} />
              {DEACT_UNITS[d.hedge_deact_type ?? 0] && (
                <div className="flex items-center gap-1.5 mt-2">
                  <span className="text-[10px] text-slate-500">{DEACT_UNITS[d.hedge_deact_type ?? 0]}</span>
                  <NumInput value={d.hedge_deact_value ?? 3} onChange={v => setD(p => ({ ...p, hedge_deact_value: v }))} />
                </div>
              )}
            </div>
          </div>

          <div className="rounded-lg border border-white/[.06] bg-white/[.02] p-3 space-y-3">
            <div className="flex items-center gap-2.5">
              <ToggleSw enabled={d.hedge_profit_lazy ?? false} onToggle={() => setD(p => ({ ...p, hedge_profit_lazy: !(p.hedge_profit_lazy ?? false) }))} />
              <span className="text-[12px] font-semibold text-slate-300">
                Отложенный профит<Tip text="Закрыть только хедж при достижении заданного % прибыли. Main-позиция продолжает работать." />
              </span>
            </div>
            {(d.hedge_profit_lazy ?? false) && (
              <div className="pl-11">
                <label className={lbl}>Шаг трейлинга, %</label>
                <NumInput value={d.hedge_profit_lazy_pct ?? 2} onChange={v => setD(p => ({ ...p, hedge_profit_lazy_pct: v }))} className="w-28 rounded border border-slate-700 bg-slate-800 px-2.5 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500" />
              </div>
            )}
          </div>
        </div>
      )}

      <SaveFooter onSave={handleSave} saving={saving} saved={saved} error={error} />
    </div>
  )
}

// ─── MatrixBotSection ──────────────────────────────────────────────────────────

type MxBotTab = 'strategy' | 'matrix' | 'params' | 'close'

function MatrixBotSection({ initial, onSaved }: { initial: MatrixBotDefaults; onSaved: () => void }) {
  const [d, setD]     = useState<MatrixBotDefaults>(initial)
  const [tab, setTab] = useState<MxBotTab>('strategy')
  const [saving, setSaving] = useState(false)
  const [saved,  setSaved]  = useState(false)
  const [error,  setError]  = useState<string | null>(null)

  const mx = useMatrixState(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)

  useEffect(() => {
    setD(initial)
    mx.reset(initial.matrix_levels ?? DEFAULT_MATRIX_LEVELS, initial.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY)
  }, [initial]) // eslint-disable-line react-hooks/exhaustive-deps

  async function handleSave() {
    const toSave: MatrixBotDefaults = { ...d, matrix_levels: mx.levels, matrix_entry_level: mx.entry }
    setSaving(true); setError(null)
    try {
      await updateStrategyDefaults('bot_matrix', toSave); setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setSaving(false) }
  }

  const tabs: { id: MxBotTab; label: string }[] = [
    { id: 'strategy', label: 'Стратегия' },
    { id: 'matrix',   label: 'Матрица'   },
    { id: 'params',   label: 'Параметры' },
    { id: 'close',    label: 'Закрытие'  },
  ]

  return (
    <div className="flex flex-col gap-4">
      <SubTabs tabs={tabs} active={tab} onChange={setTab} />

      {tab === 'strategy' && <StratBasicPanel d={d} setD={setD} showHedgeMode={true} />}

      {tab === 'matrix' && (
        <MatrixTable
          levels={mx.levels} entry={mx.entry}
          onUpdateLevel={mx.updateLevel} onUpdateEntry={mx.updateEntry}
          onAddAbove={mx.addAbove} onAddBelow={mx.addBelow} onRemoveLevel={mx.removeLevel}
        />
      )}

      {tab === 'params' && <MatrixParamsPanel d={d} setD={setD} />}

      {tab === 'close' && (
        <div className="flex flex-col gap-5">
          <div className="rounded-lg border border-violet-500/20 bg-violet-950/[.10] px-4 py-3 text-[12px] text-violet-300 leading-relaxed">
            MatrixBot открывает Long + Short на каждом символе. Закрывает пару при достижении суммарного PnL.
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={lbl}>Закрыть пару при<Tip text="Суммарный результат Long + Short. «В безубыток» — когда PnL ≥ 0." /></label>
              <EnumPicker
                options={DEACT_CLOSE_TYPES}
                value={d.hedge_deact_close_type ?? 0}
                onChange={v => setD(p => ({ ...p, hedge_deact_close_type: v }))}
              />
            </div>
            {(d.hedge_deact_close_type ?? 0) !== 2 && (
              <div>
                <label className={lbl}>{(d.hedge_deact_close_type ?? 0) === 0 ? 'PnL, USDT' : 'ROI, %'}</label>
                <NumInput value={d.hedge_deact_close_value ?? 50} onChange={v => setD(p => ({ ...p, hedge_deact_close_value: v }))} />
              </div>
            )}
          </div>

          <div className="rounded-lg border border-white/[.06] bg-white/[.02] p-3 space-y-3">
            <div className="flex items-center gap-2.5">
              <ToggleSw enabled={d.hedge_profit_lazy ?? false} onToggle={() => setD(p => ({ ...p, hedge_profit_lazy: !(p.hedge_profit_lazy ?? false) }))} />
              <span className="text-[12px] font-semibold text-slate-300">
                Закрывать если прибыль хеджа ≥<Tip text="Если ROI хедж-позиции достигает порога — закрыть, не дожидаясь парного условия." />
              </span>
            </div>
            {(d.hedge_profit_lazy ?? false) && (
              <div className="pl-11 flex items-center gap-2">
                <NumInput
                  value={d.hedge_profit_lazy_pct ?? 2}
                  onChange={v => setD(p => ({ ...p, hedge_profit_lazy_pct: v }))}
                  className="w-20 rounded border border-slate-700 bg-slate-800 px-2.5 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
                />
                <span className="text-[11px] text-slate-500">%</span>
              </div>
            )}
          </div>
        </div>
      )}

      <SaveFooter onSave={handleSave} saving={saving} saved={saved} error={error} />
    </div>
  )
}

// ─── CoinFilterSection ─────────────────────────────────────────────────────────

function CoinFilterSection({ initial, onSaved }: { initial: CoinFilterSettings; onSaved: () => void }) {
  const [d, setD]     = useState<CoinFilterSettings>(initial)
  const [saving, setSaving] = useState(false)
  const [saved,  setSaved]  = useState(false)
  const [error,  setError]  = useState<string | null>(null)

  useEffect(() => { setD(initial) }, [initial])

  async function handleSave() {
    setSaving(true); setError(null)
    try {
      await updateCoinFilter(d); setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) { setError(e instanceof Error ? e.message : String(e)) }
    finally { setSaving(false) }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className={lbl}>Мин. объём 24ч (USDT)</label>
          <NumInput value={d.min_turnover_usdt} onChange={v => setD(p => ({ ...p, min_turnover_usdt: v }))} />
        </div>
        <div>
          <label className={lbl}>Мин. активных дней для публикации</label>
          <NumInput value={d.min_publish_days} onChange={v => setD(p => ({ ...p, min_publish_days: Math.max(1, Math.round(v)) }))} />
        </div>
      </div>
      <div>
        <label className={lbl}>Чёрный список монет</label>
        <CoinMultiPicker values={d.blacklist} onChange={blacklist => setD(p => ({ ...p, blacklist }))} color="red" placeholder="Выбрать монеты для blacklist..." />
      </div>
      <SaveFooter onSave={handleSave} saving={saving} saved={saved} error={error} />
    </div>
  )
}

// ─── Main component ────────────────────────────────────────────────────────────

type OuterTab = 'grid' | 'matrix' | 'hedge_bot' | 'matrix_bot' | 'coin_filter'

const OUTER_TABS: { id: OuterTab; label: string }[] = [
  { id: 'grid',        label: 'Grid'        },
  { id: 'matrix',      label: 'Matrix'      },
  { id: 'hedge_bot',   label: 'HedgeBot'    },
  { id: 'matrix_bot',  label: 'MatrixBot'   },
  { id: 'coin_filter', label: 'Фильтр монет' },
]

export function AdminDefaultsTab() {
  const [outerTab, setOuterTab] = useState<OuterTab>('grid')
  const [defaults, setDefaults] = useState<AllStrategyDefaults | null>(null)
  const [loading,  setLoading]  = useState(true)
  const [error,    setError]    = useState<string | null>(null)
  const [coinFilter, setCoinFilter] = useState<CoinFilterSettings | null>(null)

  async function load() {
    setLoading(true); setError(null)
    try {
      const [defaultsRes, filterRes] = await Promise.all([
        apiClient.get<AllStrategyDefaults>('/admin/strategy-defaults'),
        getCoinFilter(),
      ])
      setDefaults(defaultsRes.data ?? {})
      setCoinFilter(filterRes)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally { setLoading(false) }
  }

  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  function handleSaved() {
    invalidateStrategyDefaultsCache()
    invalidateCoinFilterCache()
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center gap-3 px-4 pt-4 pb-2 border-b border-white/[.05]">
        <h2 className="text-sm font-semibold text-slate-200">Дефолтные значения</h2>
        <button type="button" onClick={load} disabled={loading}
          className="ml-auto flex items-center gap-1.5 rounded border border-slate-700 bg-slate-800 px-2.5 py-1 text-xs text-slate-300 hover:bg-slate-700 disabled:opacity-50">
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
          Обновить
        </button>
      </div>

      {/* Outer tab nav */}
      <div className="flex border-b border-white/[.05] px-4">
        {OUTER_TABS.map(t => (
          <button key={t.id} type="button" onClick={() => setOuterTab(t.id)}
            className={`relative mr-5 py-2.5 text-[12px] font-semibold transition-colors ${outerTab === t.id ? 'text-slate-50' : 'text-slate-400 hover:text-slate-200'}`}>
            {t.label}
            {outerTab === t.id && <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full bg-indigo-500" />}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto p-4">
        {error && (
          <div className="mb-4 rounded border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">{error}</div>
        )}
        {loading && !defaults && (
          <div className="py-10 text-center text-sm text-slate-500">Загрузка…</div>
        )}

        {defaults && (
          <>
            {outerTab === 'grid'        && <GridSection      initial={defaults.grid        ?? {}}  onSaved={handleSaved} />}
            {outerTab === 'matrix'      && <MatrixSection    initial={defaults.matrix      ?? {}}  onSaved={handleSaved} />}
            {outerTab === 'hedge_bot'   && <HedgeBotSection  initial={defaults.bot_hedge   ?? {}}  onSaved={handleSaved} />}
            {outerTab === 'matrix_bot'  && <MatrixBotSection initial={defaults.bot_matrix  ?? {}}  onSaved={handleSaved} />}
            {outerTab === 'coin_filter' && coinFilter && <CoinFilterSection initial={coinFilter} onSaved={handleSaved} />}
          </>
        )}
      </div>
    </div>
  )
}
