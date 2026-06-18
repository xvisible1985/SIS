import { useState, useEffect, useCallback, useRef, type ReactNode } from 'react'
import { getTradeHistory, getTradeHistorySymbols, type TradeHistoryRow, type TradeHistoryStats, type TradeHistoryParams } from '../api/tradeHistory'
import { listStrategies } from '../api/strategies'
import { apiClient } from '../api/client'
import type { Bot } from '../features/bots/types'

interface AccountOption { id: string; label: string }

const LIMIT = 50
const POLL_MS = 30_000

// ── helpers ───────────────────────────────────────────────────────────────────

function fmt(n: number | null | undefined, decimals = 2): string {
  if (n == null) return '—'
  return n.toFixed(decimals)
}

/** Форматирование до значимых цифр: достаточно знаков, чтобы не показывать нули */
function fmtSig(n: number | null | undefined): string {
  if (n == null) return '—'
  if (n === 0) return '0.00'
  const abs = Math.abs(n)
  const dec = abs >= 1 ? 2 : abs >= 0.01 ? 4 : abs >= 0.001 ? 5 : 6
  return n.toFixed(dec)
}


function fmtDateTime(iso: string): string {
  const d = new Date(iso)
  const day   = d.getDate().toString().padStart(2, '0')
  const month = (d.getMonth() + 1).toString().padStart(2, '0')
  const year  = d.getFullYear()
  const hh    = d.getHours().toString().padStart(2, '0')
  const mm    = d.getMinutes().toString().padStart(2, '0')
  return `${day}.${month}.${year} ${hh}:${mm}`
}

function fmtDuration(openIso: string, closeIso: string): string {
  const ms = new Date(closeIso).getTime() - new Date(openIso).getTime()
  if (ms < 0) return '—'
  const totalMin = Math.floor(ms / 60000)
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  if (h === 0) return `${m}м`
  if (m === 0) return `${h}ч`
  return `${h}ч ${m}м`
}


function toLocalDateStr(d: Date): string {
  const y = d.getFullYear()
  const m = (d.getMonth() + 1).toString().padStart(2, '0')
  const day = d.getDate().toString().padStart(2, '0')
  return `${y}-${m}-${day}`
}

function presetRange(preset: string): { from: string; to: string } {
  const now = new Date()
  const to = toLocalDateStr(now)
  if (preset === '1d') return { from: to, to }
  if (preset === '1w') {
    const d = new Date(now); d.setDate(d.getDate() - 6)
    return { from: toLocalDateStr(d), to }
  }
  if (preset === '1m') {
    const d = new Date(now); d.setMonth(d.getMonth() - 1)
    return { from: toLocalDateStr(d), to }
  }
  if (preset === '1y') {
    const d = new Date(now); d.setFullYear(d.getFullYear() - 1)
    return { from: toLocalDateStr(d), to }
  }
  return { from: '', to: '' }
}

// ── DateRangePicker ───────────────────────────────────────────────────────────

type Preset = 'all' | '1d' | '1w' | '1m' | '1y' | 'custom'

const PRESETS: { id: Preset; label: string }[] = [
  { id: 'all', label: 'Всё' },
  { id: '1d',  label: '1Д'  },
  { id: '1w',  label: '1Нед' },
  { id: '1m',  label: '1Мес' },
  { id: '1y',  label: '1Год' },
]

const WEEK_DAYS = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс']
const MONTHS_RU = ['Январь','Февраль','Март','Апрель','Май','Июнь','Июль','Август','Сентябрь','Октябрь','Ноябрь','Декабрь']

interface DateRangePickerProps {
  from: string
  to: string
  preset: Preset
  onChange: (from: string, to: string, preset: Preset) => void
}

function DateRangePicker({ from, to, preset, onChange }: DateRangePickerProps) {
  const [open, setOpen]           = useState(false)
  const [viewYear, setViewYear]   = useState(() => (from ? new Date(from) : new Date()).getFullYear())
  const [viewMonth, setViewMonth] = useState(() => (from ? new Date(from) : new Date()).getMonth())
  const [pendingFrom, setPendingFrom] = useState<string | null>(null)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function onOut(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false); setPendingFrom(null)
      }
    }
    document.addEventListener('mousedown', onOut)
    return () => document.removeEventListener('mousedown', onOut)
  }, [])

  function handlePreset(p: Preset) {
    const r = presetRange(p)
    onChange(r.from, r.to, p)
    setOpen(false); setPendingFrom(null)
  }

  function handleDayClick(dateStr: string) {
    if (pendingFrom === null) {
      setPendingFrom(dateStr)
    } else {
      const [f, t] = dateStr < pendingFrom ? [dateStr, pendingFrom] : [pendingFrom, dateStr]
      onChange(f, t, 'custom')
      setPendingFrom(null)
      setOpen(false)
    }
  }

  function buildCalendarDays(): (string | null)[] {
    const firstDay = new Date(viewYear, viewMonth, 1)
    let startDow = firstDay.getDay() - 1
    if (startDow < 0) startDow = 6
    const daysInMonth = new Date(viewYear, viewMonth + 1, 0).getDate()
    const cells: (string | null)[] = []
    for (let i = 0; i < startDow; i++) cells.push(null)
    for (let d = 1; d <= daysInMonth; d++) {
      const m  = (viewMonth + 1).toString().padStart(2, '0')
      const dd = d.toString().padStart(2, '0')
      cells.push(`${viewYear}-${m}-${dd}`)
    }
    return cells
  }

  function prevMonth() {
    if (viewMonth === 0) { setViewMonth(11); setViewYear(y => y - 1) }
    else setViewMonth(m => m - 1)
  }
  function nextMonth() {
    if (viewMonth === 11) { setViewMonth(0); setViewYear(y => y + 1) }
    else setViewMonth(m => m + 1)
  }

  const days = buildCalendarDays()
  const effFrom = pendingFrom ?? from
  const effTo   = pendingFrom ? '' : to

  function dayClass(dateStr: string): string {
    const base = 'w-7 h-7 flex items-center justify-center text-[11px] cursor-pointer transition-colors select-none '
    if (dateStr === effFrom || (dateStr === effTo && effTo)) {
      return base + 'rounded-full bg-[#5b8cff] text-white font-semibold'
    }
    if (effFrom && effTo && dateStr > effFrom && dateStr < effTo) {
      return base + 'bg-[#5b8cff]/20 text-[#a0b8ff]'
    }
    return base + 'rounded-full hover:bg-white/[.08] text-slate-300'
  }

  function triggerLabel(): string {
    if (preset !== 'custom') return PRESETS.find(p => p.id === preset)?.label ?? 'Период'
    if (from && to) return `${from.slice(5).replace('-', '.')} — ${to.slice(5).replace('-', '.')}`
    if (from) return `с ${from.slice(5).replace('-', '.')}`
    return 'Период'
  }

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(v => !v)}
        className="flex items-center gap-1.5 text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200 hover:bg-gray-50 dark:hover:bg-gray-800 whitespace-nowrap"
      >
        <span>📅</span>
        {triggerLabel()}
      </button>

      {open && (
        <div className="absolute top-full mt-1 left-0 z-50 bg-gray-900 border border-gray-700 rounded-xl shadow-2xl p-3 w-72">
          {/* Presets */}
          <div className="flex gap-1 mb-3">
            {PRESETS.map(p => (
              <button
                key={p.id}
                onClick={() => handlePreset(p.id)}
                className={`flex-1 py-1 rounded text-[11px] font-semibold transition-colors ${
                  preset === p.id
                    ? 'bg-[#5b8cff]/20 text-[#a0b8ff] border border-[#5b8cff]/40'
                    : 'bg-white/[.04] text-slate-400 border border-transparent hover:bg-white/[.08]'
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>

          {/* Calendar header */}
          <div className="flex items-center justify-between mb-2">
            <button onClick={prevMonth} className="px-2 py-0.5 text-slate-400 hover:text-slate-200 text-lg leading-none">‹</button>
            <span className="text-[12px] font-semibold text-slate-200">
              {MONTHS_RU[viewMonth]} {viewYear}
            </span>
            <button onClick={nextMonth} className="px-2 py-0.5 text-slate-400 hover:text-slate-200 text-lg leading-none">›</button>
          </div>

          {/* Week headers */}
          <div className="grid grid-cols-7 mb-1">
            {WEEK_DAYS.map(d => (
              <div key={d} className="text-center text-[10px] text-slate-500 font-medium">{d}</div>
            ))}
          </div>

          {/* Days */}
          <div className="grid grid-cols-7 gap-y-0.5">
            {days.map((dateStr, i) => (
              <div key={i} className="flex justify-center">
                {dateStr ? (
                  <div className={dayClass(dateStr)} onClick={() => handleDayClick(dateStr)}>
                    {parseInt(dateStr.slice(-2))}
                  </div>
                ) : (
                  <div className="w-7 h-7" />
                )}
              </div>
            ))}
          </div>

          {pendingFrom && (
            <div className="mt-2 text-center text-[10px] text-slate-500">
              Выберите конечную дату (начало: {pendingFrom.slice(5).replace('-', '.')})
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ── StatsCards ────────────────────────────────────────────────────────────────

function StatsCards({ stats }: { stats: TradeHistoryStats }) {
  const grossTotal = stats.total_pnl
  const netTotal   = stats.total_net_pnl
  const winPct    = stats.total > 0 ? (stats.wins / stats.total) * 100 : 0

  const card = 'shrink-0 bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl px-4 py-3 flex flex-col gap-1.5 min-w-[148px]'
  const lbl  = 'text-[12px] font-semibold uppercase tracking-[.6px] text-gray-400 dark:text-gray-400 whitespace-nowrap mb-0.5'
  const row  = 'flex items-baseline justify-between gap-3'

  return (
    <div className="flex gap-3 mb-5 overflow-x-auto pb-1">

      {/* ── Сделки ── */}
      <div className={card}>
        <div className={lbl}>Сделки</div>
        <div className="text-2xl font-bold text-gray-100 leading-none">{stats.total}</div>
        <div className="flex gap-2 text-xs font-semibold">
          <span className="text-green-500">↑ {stats.wins}</span>
          <span className="text-gray-600 dark:text-gray-600">/</span>
          <span className="text-red-500">↓ {stats.losses}</span>
        </div>
      </div>

      {/* ── Win Rate ── */}
      <div className={card} style={{ minWidth: 160 }}>
        <div className={lbl}>Win Rate</div>
        <div className={`text-2xl font-bold leading-none ${winPct >= 50 ? 'text-green-500' : 'text-red-500'}`}>
          {fmt(stats.win_rate, 1)}%
        </div>
        {/* progress bar */}
        <div className="h-1.5 rounded-full bg-gray-800 overflow-hidden">
          <div
            className={`h-full rounded-full transition-all ${winPct >= 50 ? 'bg-green-500' : 'bg-red-500'}`}
            style={{ width: `${Math.min(100, winPct)}%` }}
          />
        </div>
        <div className="text-[10px] text-gray-500">{stats.wins}W / {stats.losses}L</div>
      </div>

      {/* ── PnL gross + net ── */}
      <div className={card} style={{ minWidth: 180 }}>
        <div className={lbl}>PnL</div>
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-14 shrink-0">Gross</span>
          <span className="inline-flex items-center gap-1 text-sm font-bold font-mono text-gray-200">
            {grossTotal >= 0 ? '+' : ''}{fmtSig(grossTotal)}$
            <span className={grossTotal >= 0 ? 'text-green-500' : 'text-red-500'}>{grossTotal >= 0 ? '↑' : '↓'}</span>
          </span>
        </div>
        <div className="h-px bg-gray-800" />
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-14 shrink-0">Чистый</span>
          <span className="inline-flex items-center gap-1 text-base font-bold font-mono text-gray-200">
            {netTotal >= 0 ? '+' : ''}{fmtSig(netTotal)}$
            <span className={netTotal >= 0 ? 'text-green-500' : 'text-red-500'}>{netTotal >= 0 ? '↑' : '↓'}</span>
          </span>
        </div>
      </div>

      {/* ── Лучшая / Худшая ── */}
      <div className={card} style={{ minWidth: 164 }}>
        <div className={lbl}>Экстремумы</div>
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-14 shrink-0">Лучшая</span>
          <span className="text-sm font-bold font-mono text-green-500">
            {stats.best_trade != null ? `+${fmtSig(stats.best_trade)}$` : '—'}
          </span>
        </div>
        <div className="h-px bg-gray-800" />
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-14 shrink-0">Худшая</span>
          <span className="text-sm font-bold font-mono text-red-500">
            {stats.worst_trade != null ? `${fmtSig(stats.worst_trade)}$` : '—'}
          </span>
        </div>
      </div>

      {/* ── Комиссии / Фандинг ── */}
      <div className={card} style={{ minWidth: 164 }}>
        <div className={lbl}>Издержки</div>
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-20 shrink-0">Комиссии</span>
          <span className="text-sm font-bold font-mono text-orange-400">
            -{fmtSig(stats.total_fees)}$
          </span>
        </div>
        <div className="h-px bg-gray-800" />
        <div className={`${row}`}>
          <span className="text-[10px] text-gray-500 w-20 shrink-0">Фандинг</span>
          <span className={`text-sm font-bold font-mono ${stats.total_funding <= 0 ? 'text-green-500' : 'text-orange-400'}`}>
            {stats.total_funding <= 0 ? '+' : '-'}{fmtSig(Math.abs(stats.total_funding))}$
          </span>
        </div>
      </div>

    </div>
  )
}

// ── Sortable table ────────────────────────────────────────────────────────────

type SortDir = 'asc' | 'desc'
type SortCol = 'symbol' | 'direction' | 'result' | 'bot_name' | 'volume_usdt' | 'pnl' | 'pnl_gross' | 'fees' | 'funding' | 'closed_at' | 'duration'

function SortIcon({ col, sortCol, sortDir }: { col: string; sortCol: string; sortDir: SortDir }) {
  if (col !== sortCol) return <span className="ml-0.5 opacity-25">↕</span>
  return <span className="ml-0.5 text-[#5b8cff]">{sortDir === 'asc' ? '↑' : '↓'}</span>
}

// ── Pair detection ────────────────────────────────────────────────────────────

interface PairMeta { pairKey: string; combinedNetPnl: number; symbol: string }

function detectPairs(trades: TradeHistoryRow[]): Map<string, PairMeta> {
  const byKey = new Map<string, TradeHistoryRow[]>()
  for (const t of trades) {
    const key = `${t.symbol}::${t.opened_at}`
    const arr = byKey.get(key) ?? []
    arr.push(t)
    byKey.set(key, arr)
  }
  const result = new Map<string, PairMeta>()
  for (const [key, rows] of byKey) {
    if (rows.length === 2 && rows[0].direction !== rows[1].direction) {
      const combined = (rows[0].net_pnl ?? 0) + (rows[1].net_pnl ?? 0)
      const meta: PairMeta = { pairKey: key, combinedNetPnl: combined, symbol: rows[0].symbol }
      result.set(rows[0].id, meta)
      result.set(rows[1].id, meta)
    }
  }
  return result
}

// ── PairSummaryRow ────────────────────────────────────────────────────────────

function PairSummaryRow({ symbol, combinedNetPnl }: { symbol: string; combinedNetPnl: number }) {
  return (
    <tr className="border-b border-violet-500/20 bg-violet-500/[.04]">
      <td colSpan={11} className="py-1.5 px-3">
        <div className="flex items-center gap-2 text-[11px]">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-violet-500/40 bg-violet-500/15 px-2.5 py-0.5 font-semibold text-violet-300">
            ⇄ Парное закрытие
          </span>
          <span className="text-gray-500">{symbol}</span>
          <span className="text-gray-600">·</span>
          <span className="text-gray-400">Совокупный PnL:</span>
          <span className={`font-mono font-semibold ${combinedNetPnl >= 0 ? 'text-green-400' : 'text-red-400'}`}>
            {combinedNetPnl >= 0 ? '+' : ''}{fmtSig(combinedNetPnl)}$
          </span>
        </div>
      </td>
    </tr>
  )
}

// ── TradeRow ──────────────────────────────────────────────────────────────────

function ExitBadge({ result, source }: { result: string; source?: string }) {
  if (result === 'tp') {
    return <span className="px-1.5 py-0.5 rounded font-medium bg-green-500/15 text-green-400">TP</span>
  }
  if (result === 'sl') {
    return <span className="px-1.5 py-0.5 rounded font-medium bg-red-500/15 text-red-400">SL</span>
  }
  const isManualSource = source === 'manual'
  return (
    <span className={`px-1.5 py-0.5 rounded font-medium ${isManualSource ? 'bg-yellow-500/15 text-yellow-400' : 'bg-gray-800 text-gray-400'}`}>
      {isManualSource ? 'Ручная' : 'Ручной'}
    </span>
  )
}

function TradeRow({ t, isPaired }: { t: TradeHistoryRow; isPaired?: boolean }) {
  return (
    <tr className={`border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/30 ${isPaired ? 'bg-violet-500/[.02]' : ''}`}>
      <td className="py-2.5 px-3 text-sm font-medium">{t.symbol}</td>
      <td className="py-2.5 px-3 text-xs">
        <div className="flex items-center gap-1">
          <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${t.direction === 'long' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
            {t.direction === 'long' ? 'LONG' : 'SHORT'}
          </span>
          {isPaired && (
            <span className="text-[10px] font-bold text-violet-400" title="Парная сделка">⇄</span>
          )}
        </div>
      </td>
      <td className="py-2.5 px-3 text-xs">
        <ExitBadge result={t.result} source={t.source} />
      </td>
      <td className="py-2.5 px-3 text-xs text-gray-500">{t.bot_name ?? '—'}</td>
      <td className="py-2.5 px-3 text-xs text-right">{fmt(t.volume_usdt, 1)}$</td>

      {/* Net PnL */}
      <td className="py-2.5 px-3 text-xs text-right font-medium font-mono text-gray-300 dark:text-gray-300">
        {t.net_pnl == null ? (
          <span className="text-gray-500 dark:text-gray-600 text-xs">—</span>
        ) : (
          <span className="inline-flex items-center gap-0.5 justify-end">
            {`${t.net_pnl >= 0 ? '+' : ''}${fmtSig(t.net_pnl)}$`}
            {t.volume_usdt != null && t.volume_usdt > 0 && (
              <span className="text-xs opacity-50">
                ({t.net_pnl >= 0 ? '+' : ''}{fmt(t.net_pnl / t.volume_usdt * 100, 1)}%)
              </span>
            )}
            <span className={t.net_pnl >= 0 ? 'text-green-500' : 'text-red-500'}>
              {t.net_pnl >= 0 ? '↑' : '↓'}
            </span>
          </span>
        )}
      </td>

      {/* Gross PnL */}
      <td className="py-2.5 px-3 text-xs text-right font-mono text-gray-300 dark:text-gray-300">
        {t.pnl == null
          ? <span className="text-gray-600 dark:text-gray-600">—</span>
          : (
            <span className="inline-flex items-center gap-0.5 justify-end">
              {`${t.pnl >= 0 ? '+' : ''}${fmtSig(t.pnl)}$`}
              <span className={t.pnl >= 0 ? 'text-green-500' : 'text-red-500'}>
                {t.pnl >= 0 ? '↑' : '↓'}
              </span>
            </span>
          )
        }
      </td>

      {/* Комиссия */}
      <td className="py-2.5 px-3 text-xs text-right font-mono text-gray-400 dark:text-gray-500">
        {t.fees !== 0 ? `-${fmtSig(t.fees)}$` : <span className="text-gray-600 dark:text-gray-600">—</span>}
      </td>

      {/* Фандинг */}
      <td className="py-2.5 px-3 text-xs text-right font-mono text-gray-400 dark:text-gray-500">
        {t.funding !== 0
          ? `${t.funding <= 0 ? '+' : '-'}${fmtSig(Math.abs(t.funding))}$`
          : <span className="text-gray-600 dark:text-gray-600">—</span>
        }
      </td>

      <td className="py-2.5 px-3 text-xs text-gray-400 text-right whitespace-nowrap">{fmtDateTime(t.closed_at)}</td>
      <td className="py-2.5 px-3 text-xs text-gray-400 text-right">{fmtDuration(t.opened_at, t.closed_at)}</td>
    </tr>
  )
}

// ── TradeHistoryPage ──────────────────────────────────────────────────────────

type FilterItem = { id: string; label: string; kind: 'bot' | 'strategy' }

export function TradeHistoryPage() {
  const [accounts, setAccounts]         = useState<AccountOption[]>([])
  const [filterAccount, setFilterAccount] = useState('')
  const [filterItems, setFilterItems]   = useState<FilterItem[]>([])
  const [symbolOptions, setSymbolOptions] = useState<string[]>([])
  const [filterSymbol, setFilterSymbol] = useState('')
  const [stats, setStats]   = useState<TradeHistoryStats | null>(null)
  const [trades, setTrades] = useState<TradeHistoryRow[]>([])
  const [total, setTotal]   = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading]  = useState(false)

  const [filterItem,   setFilterItem]   = useState('')
  const [filterSource, setFilterSource] = useState<'' | 'strategy' | 'manual'>('')
  const [filterResult, setFilterResult] = useState<'' | 'tp' | 'sl' | 'manual'>('')
  const [dateFrom,     setDateFrom]     = useState('')
  const [dateTo,       setDateTo]       = useState('')
  const [datePreset,   setDatePreset]   = useState<Preset>('all')

  const [sortCol, setSortCol] = useState<SortCol>('closed_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  // ── Fetch accounts once (for account filter dropdown) ──
  useEffect(() => {
    apiClient.get<AccountOption[]>('/accounts')
      .then(r => setAccounts(r.data ?? []))
      .catch(() => {})
  }, [])

  useEffect(() => {
    getTradeHistorySymbols().then(setSymbolOptions).catch(() => {})
  }, [])

  useEffect(() => {
    Promise.all([
      apiClient.get<{ catalog: Bot[]; mine: Bot[] }>('/bots').then(r => r.data.mine ?? []).catch(() => [] as Bot[]),
      listStrategies().catch(() => []),
    ]).then(([bots, allStrategies]) => {
      // Hide stopped strategies with no open position, no realized PnL — they have no trade history worth showing.
      const strategies = allStrategies.filter(s =>
        s.status !== 'stopped' ||
        (s.active_levels ?? 0) > 0 ||
        (s.volume_usdt ?? 0) > 0 ||
        (s.last_pnl ?? 0) !== 0
      )

      const items: FilterItem[] = []
      for (const b of bots) items.push({ id: `bot:${b.id}`, label: b.name, kind: 'bot' })

      // Manual (non-bot) strategies — filter by strategy_id
      for (const s of strategies) {
        if (!s.bot_id) {
          items.push({ id: `strategy:${s.id}`, label: `${s.symbol} · ${s.direction.toUpperCase()}`, kind: 'strategy' })
        }
      }

      // Bot-managed strategies — deduplicated by bot+symbol, filter by bot_id+symbol
      const seenBotSymbol = new Set<string>()
      for (const s of strategies) {
        if (s.bot_id) {
          const key = `${s.bot_id}:${s.symbol}`
          if (!seenBotSymbol.has(key)) {
            seenBotSymbol.add(key)
            const botLabel = s.bot_name ? ` (${s.bot_name})` : ''
            items.push({
              id: `bot-symbol:${s.bot_id}:${s.symbol}`,
              label: `${s.symbol}${botLabel}`,
              kind: 'strategy',
            })
          }
        }
      }

      setFilterItems(items)
    })
  }, [])

  // ── Load trade history from DB ────────────────────────────────────────────
  const loadDB = useCallback(async (off: number, silent = false) => {
    if (!silent) setLoading(true)
    const params: TradeHistoryParams = { limit: LIMIT, offset: off, sort_by: sortCol, sort_dir: sortDir }
    if (filterAccount) params.account_id = filterAccount
    if (filterItem.startsWith('bot:'))      params.bot_id      = filterItem.slice(4)
    if (filterItem.startsWith('strategy:')) params.strategy_id = filterItem.slice(9)
    if (filterItem.startsWith('bot-symbol:')) {
      const rest = filterItem.slice('bot-symbol:'.length)
      const colon = rest.indexOf(':')
      if (colon !== -1) {
        params.bot_id = rest.slice(0, colon)
        params.symbol = rest.slice(colon + 1)
      }
    }
    if (filterSymbol)  params.symbol  = filterSymbol
    if (filterSource)  params.source  = filterSource
    if (filterResult)  params.result  = filterResult
    if (dateFrom) params.from = new Date(dateFrom).toISOString()
    if (dateTo)   params.to   = new Date(dateTo + 'T23:59:59').toISOString()
    try {
      const res = await getTradeHistory(params)
      setStats(res.stats ?? null)
      setTrades(res.trades ?? [])
      setTotal(res.total ?? 0)
      setOffset(off)
    } catch {
      if (!silent) setTrades([])
    } finally {
      if (!silent) setLoading(false)
    }
  }, [filterAccount, filterItem, filterSymbol, filterSource, filterResult, dateFrom, dateTo, sortCol, sortDir])

  const load = useCallback((off: number, silent = false) => {
    loadDB(off, silent)
  }, [loadDB])

  useEffect(() => { load(0) }, [load])

  useEffect(() => {
    const id = setInterval(() => { loadDB(offset, true) }, POLL_MS)
    return () => clearInterval(id)
  }, [loadDB, offset])

  const displayStats = stats

  function handleSort(col: SortCol) {
    if (col === sortCol) setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setSortCol(col); setSortDir('desc') }
    // offset reset happens via useEffect on load deps change
  }

  const hasFilter = !!(filterAccount || filterItem || filterSymbol || filterSource || filterResult || dateFrom || dateTo)

  const COLS: [SortCol, string, string][] = [
    ['symbol',     'Символ',          'left'],
    ['direction',  'Направление',     'left'],
    ['result',     'Выход',           'left'],
    ['bot_name',   'Бот',             'left'],
    ['volume_usdt','Объём',           'right'],
    ['pnl',        'PnL (чистый)',   'right'],
    ['pnl_gross',  'PnL (gross)',    'right'],
    ['fees',       'Комиссия',      'right'],
    ['funding',    'Фандинг',        'right'],
    ['closed_at',  'Закрыта',         'right'],
    ['duration',   'Длительность',    'right'],
  ]

  return (
    <div className="max-w-7xl mx-auto px-4 py-6">
      <div className="flex items-center justify-between mb-5">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-100">История сделок</h1>
        <span className="text-sm text-gray-500">{total} сделок</span>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-3 mb-5">

        {/* Account filter */}
        {accounts.length > 1 && (
          <select
            value={filterAccount}
            onChange={e => setFilterAccount(e.target.value)}
            className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200 font-medium"
          >
            <option value="">Все аккаунты</option>
            {accounts.map(a => (
              <option key={a.id} value={a.id}>{a.label}</option>
            ))}
          </select>
        )}

        {/* Strategy / bot filter */}
        <select
          value={filterItem}
          onChange={e => setFilterItem(e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        >
          <option value="">Все стратегии</option>
          {filterItems.some(i => i.kind === 'bot') && (
            <optgroup label="Боты">
              {filterItems.filter(i => i.kind === 'bot').map(i => (
                <option key={i.id} value={i.id}>{i.label}</option>
              ))}
            </optgroup>
          )}
          {filterItems.some(i => i.kind === 'strategy') && (
            <optgroup label="Стратегии">
              {filterItems.filter(i => i.kind === 'strategy').map(i => (
                <option key={i.id} value={i.id}>{i.label}</option>
              ))}
            </optgroup>
          )}
        </select>

        {/* Symbol filter */}
        {symbolOptions.length > 0 && (
          <select
            value={filterSymbol}
            onChange={e => setFilterSymbol(e.target.value)}
            className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
          >
            <option value="">Все монеты</option>
            {symbolOptions.map(s => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        )}

        {/* Source filter */}
        <select
          value={filterSource}
          onChange={e => setFilterSource(e.target.value as '' | 'strategy' | 'manual')}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        >
          <option value="">Все сделки</option>
          <option value="strategy">Стратегия</option>
          <option value="manual">Ручные</option>
        </select>

        {/* Result filter */}
        <select
          value={filterResult}
          onChange={e => setFilterResult(e.target.value as '' | 'tp' | 'sl' | 'manual')}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        >
          <option value="">Все выходы</option>
          <option value="tp">Только TP</option>
          <option value="sl">Только SL</option>
          <option value="manual">Только ручные</option>
        </select>

        <DateRangePicker
          from={dateFrom}
          to={dateTo}
          preset={datePreset}
          onChange={(f, t, p) => { setDateFrom(f); setDateTo(t); setDatePreset(p) }}
        />

        {hasFilter && (
          <button
            onClick={() => {
              setFilterAccount('')
              setFilterItem('')
              setFilterSymbol('')
              setFilterSource('')
              setFilterResult('')
              setDateFrom('')
              setDateTo('')
              setDatePreset('all')
              setTrades([])
            }}
            className="text-sm text-gray-500 hover:text-gray-700 dark:hover:text-gray-300 underline"
          >
            Сбросить
          </button>
        )}
      </div>

      {/* Stats */}
      {displayStats && <StatsCards stats={displayStats} />}

      {/* Table */}
      <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl overflow-hidden">
        {loading && (
          <div className="p-6 text-center text-sm text-gray-400">Загрузка…</div>
        )}
        {!loading && trades.length === 0 && (
          <div className="p-8 text-center text-sm text-gray-400">Сделок пока нет</div>
        )}
        {!loading && trades.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-gray-800 dark:text-gray-200">
              <thead>
                <tr className="text-xs text-gray-500 border-b border-gray-200 dark:border-gray-700">
                  {COLS.map(([col, label, align]) => (
                    <th
                      key={col}
                      onClick={() => handleSort(col)}
                      className={`py-2.5 px-3 text-${align} font-medium cursor-pointer hover:text-gray-700 dark:hover:text-gray-300 select-none whitespace-nowrap`}
                    >
                      {label}
                      <SortIcon col={col} sortCol={sortCol} sortDir={sortDir} />
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {(() => {
                  const pairMap = detectPairs(trades)
                  const emitted = new Set<string>()
                  const rows: ReactNode[] = []
                  for (const t of trades) {
                    const meta = pairMap.get(t.id)
                    rows.push(<TradeRow key={t.id} t={t} isPaired={!!meta} />)
                    if (meta) {
                      if (emitted.has(meta.pairKey)) {
                        rows.push(
                          <PairSummaryRow
                            key={`pair-${meta.pairKey}`}
                            symbol={meta.symbol}
                            combinedNetPnl={meta.combinedNetPnl}
                          />
                        )
                      } else {
                        emitted.add(meta.pairKey)
                      }
                    }
                  }
                  return rows
                })()}
              </tbody>
            </table>
          </div>
        )}

        {/* ── Pagination ── */}
        {total > LIMIT && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-gray-100 dark:border-gray-800">
            <span className="text-xs text-gray-500">
              {offset + 1}–{Math.min(offset + LIMIT, total)} из {total}
            </span>
            <div className="flex gap-2">
              <button
                disabled={offset === 0}
                onClick={() => loadDB(Math.max(0, offset - LIMIT))}
                className="px-3 py-1 text-xs border border-gray-200 dark:border-gray-700 rounded-lg disabled:opacity-40 hover:bg-gray-50 dark:hover:bg-gray-800"
              >
                ← Назад
              </button>
              <button
                disabled={offset + LIMIT >= total}
                onClick={() => loadDB(offset + LIMIT)}
                className="px-3 py-1 text-xs border border-gray-200 dark:border-gray-700 rounded-lg disabled:opacity-40 hover:bg-gray-50 dark:hover:bg-gray-800"
              >
                Вперёд →
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
