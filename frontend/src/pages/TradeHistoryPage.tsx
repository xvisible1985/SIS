import { useState, useEffect, useCallback, useRef } from 'react'
import { getTradeHistory, type TradeHistoryRow, type TradeHistoryStats, type TradeHistoryParams } from '../api/tradeHistory'
import { listStrategies } from '../api/strategies'
import { apiClient } from '../api/client'
import type { Bot } from '../features/bots/types'

const LIMIT = 50
const POLL_MS = 30_000

// ── helpers ───────────────────────────────────────────────────────────────────

function fmt(n: number | null | undefined, decimals = 2): string {
  if (n == null) return '—'
  return n.toFixed(decimals)
}

function pnlClass(n: number | null | undefined): string {
  if (n == null) return ''
  return n >= 0 ? 'text-green-400' : 'text-red-400'
}

function fmtDateTime(iso: string): string {
  const d = new Date(iso)
  const day   = d.getDate().toString().padStart(2, '0')
  const month = (d.getMonth() + 1).toString().padStart(2, '0')
  const hh    = d.getHours().toString().padStart(2, '0')
  const mm    = d.getMinutes().toString().padStart(2, '0')
  return `${day}.${month} ${hh}:${mm}`
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

function durationMs(t: TradeHistoryRow): number {
  return new Date(t.closed_at).getTime() - new Date(t.opened_at).getTime()
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
  return (
    <div className="flex gap-3 mb-5 overflow-x-auto">
      {[
        { label: 'Всего сделок', value: String(stats.total) },
        { label: 'Win Rate', value: `${fmt(stats.win_rate, 1)}%`, sub: `${stats.wins}W / ${stats.losses}L` },
        { label: 'Суммарный PnL', value: `${stats.total_pnl >= 0 ? '+' : ''}${fmt(stats.total_pnl)} $`, color: pnlClass(stats.total_pnl) },
        { label: 'Средний PnL',   value: `${stats.avg_pnl >= 0 ? '+' : ''}${fmt(stats.avg_pnl)} $`,   color: pnlClass(stats.avg_pnl)   },
        { label: 'Лучшая сделка', value: stats.best_trade  != null ? `+${fmt(stats.best_trade)} $`  : '—', color: 'text-green-400' },
        { label: 'Худшая сделка', value: stats.worst_trade != null ? `${fmt(stats.worst_trade)} $` : '—', color: 'text-red-400'   },
      ].map(c => (
        <div key={c.label} className="shrink-0 bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl px-4 py-3">
          <div className="text-xs text-gray-500 dark:text-gray-400 mb-1 whitespace-nowrap">{c.label}</div>
          <div className={`text-base font-semibold whitespace-nowrap ${c.color ?? ''}`}>{c.value}</div>
          {c.sub && <div className="text-xs text-gray-500 mt-0.5">{c.sub}</div>}
        </div>
      ))}
    </div>
  )
}

// ── Sortable table ────────────────────────────────────────────────────────────

type SortDir = 'asc' | 'desc'
type SortCol = 'symbol' | 'direction' | 'result' | 'bot_name' | 'volume_usdt' | 'pnl' | 'closed_at' | 'duration'

function SortIcon({ col, sortCol, sortDir }: { col: string; sortCol: string; sortDir: SortDir }) {
  if (col !== sortCol) return <span className="ml-0.5 opacity-25">↕</span>
  return <span className="ml-0.5 text-[#5b8cff]">{sortDir === 'asc' ? '↑' : '↓'}</span>
}

// ── TradeRow ──────────────────────────────────────────────────────────────────

function TradeRow({ t }: { t: TradeHistoryRow }) {
  const isTP = t.result === 'tp'
  return (
    <tr className="border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/30">
      <td className="py-2.5 px-3 text-sm font-medium">{t.symbol}</td>
      <td className="py-2.5 px-3 text-xs">
        <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${
          t.direction === 'long'
            ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
            : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
        }`}>
          {t.direction === 'long' ? 'LONG' : 'SHORT'}
        </span>
      </td>
      <td className="py-2.5 px-3 text-xs">
        <span className={`px-1.5 py-0.5 rounded font-medium ${
          isTP
            ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
            : 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
        }`}>
          {isTP ? 'TP' : 'SL'}
        </span>
      </td>
      <td className="py-2.5 px-3 text-xs text-gray-500">{t.bot_name ?? '—'}</td>
      <td className="py-2.5 px-3 text-xs text-right">{fmt(t.volume_usdt, 1)}$</td>
      <td className={`py-2.5 px-3 text-sm text-right font-medium ${pnlClass(t.pnl)}`}>
        {t.pnl != null ? `${t.pnl >= 0 ? '+' : ''}${fmt(t.pnl)}$` : '—'}
        {t.pnl_pct != null && (
          <span className="text-xs ml-1 opacity-70">({t.pnl_pct >= 0 ? '+' : ''}{fmt(t.pnl_pct, 1)}%)</span>
        )}
      </td>
      <td className="py-2.5 px-3 text-xs text-gray-400 text-right whitespace-nowrap">{fmtDateTime(t.closed_at)}</td>
      <td className="py-2.5 px-3 text-xs text-gray-400 text-right">{fmtDuration(t.opened_at, t.closed_at)}</td>
    </tr>
  )
}

// ── TradeHistoryPage ──────────────────────────────────────────────────────────

type FilterItem = { id: string; label: string; kind: 'bot' | 'strategy' }

export function TradeHistoryPage() {
  const [filterItems, setFilterItems] = useState<FilterItem[]>([])
  const [stats, setStats]   = useState<TradeHistoryStats | null>(null)
  const [trades, setTrades] = useState<TradeHistoryRow[]>([])
  const [total, setTotal]   = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)

  const [filterItem,   setFilterItem]   = useState('')
  const [filterResult, setFilterResult] = useState<'' | 'tp' | 'sl'>('')
  const [dateFrom,     setDateFrom]     = useState('')
  const [dateTo,       setDateTo]       = useState('')
  const [datePreset,   setDatePreset]   = useState<Preset>('all')

  const [sortCol, setSortCol] = useState<SortCol>('closed_at')
  const [sortDir, setSortDir] = useState<SortDir>('desc')

  useEffect(() => {
    Promise.all([
      apiClient.get<{ catalog: Bot[]; mine: Bot[] }>('/bots').then(r => r.data.mine ?? []).catch(() => [] as Bot[]),
      listStrategies().catch(() => []),
    ]).then(([bots, strategies]) => {
      const items: FilterItem[] = []
      for (const b of bots) items.push({ id: `bot:${b.id}`, label: b.name, kind: 'bot' })
      for (const s of strategies) {
        if (!s.bot_id) {
          items.push({ id: `strategy:${s.id}`, label: `${s.symbol} · ${s.direction.toUpperCase()}`, kind: 'strategy' })
        }
      }
      setFilterItems(items)
    })
  }, [])

  const load = useCallback(async (off: number, silent = false) => {
    if (!silent) setLoading(true)
    const params: TradeHistoryParams = { limit: LIMIT, offset: off }
    if (filterItem.startsWith('bot:'))      params.bot_id      = filterItem.slice(4)
    if (filterItem.startsWith('strategy:')) params.strategy_id = filterItem.slice(9)
    if (filterResult) params.result = filterResult
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
  }, [filterItem, filterResult, dateFrom, dateTo])

  useEffect(() => { load(0) }, [load])

  useEffect(() => {
    const id = setInterval(() => { load(offset, true) }, POLL_MS)
    return () => clearInterval(id)
  }, [load, offset])

  function handleSort(col: SortCol) {
    if (col === sortCol) setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setSortCol(col); setSortDir('desc') }
  }

  const sortedTrades = [...trades].sort((a, b) => {
    let av: number | string = 0
    let bv: number | string = 0
    switch (sortCol) {
      case 'symbol':     av = a.symbol;      bv = b.symbol;      break
      case 'direction':  av = a.direction;   bv = b.direction;   break
      case 'result':     av = a.result;      bv = b.result;      break
      case 'bot_name':   av = a.bot_name ?? ''; bv = b.bot_name ?? ''; break
      case 'volume_usdt': av = a.volume_usdt ?? 0; bv = b.volume_usdt ?? 0; break
      case 'pnl':        av = a.pnl ?? 0;   bv = b.pnl ?? 0;   break
      case 'closed_at':  av = a.closed_at;  bv = b.closed_at;  break
      case 'duration':   av = durationMs(a); bv = durationMs(b); break
    }
    if (av < bv) return sortDir === 'asc' ? -1 : 1
    if (av > bv) return sortDir === 'asc' ?  1 : -1
    return 0
  })

  const hasFilter = !!(filterItem || filterResult || dateFrom || dateTo)

  const COLS: [SortCol, string, string][] = [
    ['symbol',     'Символ',      'left'],
    ['direction',  'Направление', 'left'],
    ['result',     'Результат',   'left'],
    ['bot_name',   'Стратегия',   'left'],
    ['volume_usdt','Объём',       'right'],
    ['pnl',        'PnL',         'right'],
    ['closed_at',  'Закрыта',     'right'],
    ['duration',   'Длительность','right'],
  ]

  return (
    <div className="max-w-7xl mx-auto px-4 py-6">
      <div className="flex items-center justify-between mb-5">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-100">История сделок</h1>
        <span className="text-sm text-gray-500">{total} сделок</span>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-3 mb-5">
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

        <select
          value={filterResult}
          onChange={e => setFilterResult(e.target.value as '' | 'tp' | 'sl')}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        >
          <option value="">TP + SL</option>
          <option value="tp">Только TP</option>
          <option value="sl">Только SL</option>
        </select>

        <DateRangePicker
          from={dateFrom}
          to={dateTo}
          preset={datePreset}
          onChange={(f, t, p) => { setDateFrom(f); setDateTo(t); setDatePreset(p) }}
        />

        {hasFilter && (
          <button
            onClick={() => { setFilterItem(''); setFilterResult(''); setDateFrom(''); setDateTo(''); setDatePreset('all') }}
            className="text-sm text-gray-500 hover:text-gray-700 dark:hover:text-gray-300 underline"
          >
            Сбросить
          </button>
        )}
      </div>

      {/* Stats */}
      {stats && <StatsCards stats={stats} />}

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
                {sortedTrades.map(t => <TradeRow key={t.id} t={t} />)}
              </tbody>
            </table>
          </div>
        )}

        {total > LIMIT && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-gray-100 dark:border-gray-800">
            <span className="text-xs text-gray-500">
              {offset + 1}–{Math.min(offset + LIMIT, total)} из {total}
            </span>
            <div className="flex gap-2">
              <button
                disabled={offset === 0}
                onClick={() => load(Math.max(0, offset - LIMIT))}
                className="px-3 py-1 text-xs border border-gray-200 dark:border-gray-700 rounded-lg disabled:opacity-40 hover:bg-gray-50 dark:hover:bg-gray-800"
              >
                ← Назад
              </button>
              <button
                disabled={offset + LIMIT >= total}
                onClick={() => load(offset + LIMIT)}
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
