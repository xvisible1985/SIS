import { useState, useEffect, useCallback, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { setSignalChartIntent } from '../stores/signalChartStore'
import { ToggleLeft, ToggleRight, FlaskConical, X } from 'lucide-react'
import { apiClient } from '../api/client'
import { SIGNALS, setRsiTestOverride } from '../features/indicators/signals'
import { INDICATORS } from '../features/indicators/indicators'
import { useMarketIndicators } from '../hooks/useMarketIndicators'
import { IndicatorCard } from '../features/indicators/components/IndicatorCard'
import { SignalCard }    from '../features/indicators/components/SignalCard'
import { FlyingCardOverlay } from '../features/indicators/components/FlyingCardOverlay'
import type { IndicatorDef, BaseParams, SignalDef } from '../features/indicators/types'
import { AdminUsersPage } from '../features/admin-users'
import { useAdminUsers } from '../features/admin-users/api'
import { AdminBotsTab } from '../features/admin-bots/AdminBotsTab'
import { AdminProxiesTab } from '../features/admin-proxies/AdminProxiesTab'
import { AdminDefaultsTab } from '../features/admin-defaults/AdminDefaultsTab'
import { BybitNewsTab } from '../features/bybit-news/BybitNewsTab'
import { BybitNewsCard } from '../features/bybit-news/BybitNewsCard'
import { LogVisualizerTab } from '../features/log-visualizer/LogVisualizerTab'

// ── Types ──────────────────────────────────────────────────────────────────

interface ComputeUnitStat {
  hash: string
  symbol: string
  interval: string
  signals: string       // human-readable: "RSI(14) + EMA(20)"
  subscribers: number
  avgComputeMs: number
  computeCount: number
  lastState: 'buy' | 'sell' | 'neutral'
  lastComputedSec: number
}

interface StrategyWorkerStat {
  strategy_id: string
  account_id: string
  account_label: string
  owner_id: string
  owner_username: string
  symbol: string
  category: string
  status: string
  queue_depth: number
  is_processing: boolean
  panic_count: number
  tasks_dropped: number
  last_task_at: string // RFC3339 or empty
}

interface AdminMetrics {
  signal: {
    activeUnits: number
    activeSubs: number
    wsConnections: number
    computesPerSec: number
    cpuTimeMs: number
    bufferMemMB: number
    units: ComputeUnitStat[]
  }
  strategy: {
    activeStrategies: number
    activeCycles: number
    ordersToday: number
    fillsToday: number
    accounts: { id: string; name: string; strategies: number; cycles: number }[]
  }
  strategy_workers?: StrategyWorkerStat[]
  global_warmer?: {
    symbols: number
    intervals: string[]
    warm_count: number
    total_slots: number
    warm_pct: number
    prefetch_ms: number
  }
  ticker_hub?: {
    symbols: number
    warm_symbols: number
    ws_connections: number
  }
  bot_engine?: {
    last_tick_at: string
    last_tick_ms: number
    bots_active: number
    groups_computed: number
    opportunities: number
  }
}

// ── Hooks ──────────────────────────────────────────────────────────────────

function useAdminMetrics() {
  const [metrics, setMetrics] = useState<AdminMetrics | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    async function fetch_() {
      try {
        const res = await apiClient.get<AdminMetrics>('/admin/metrics')
        if (!cancelled) { setMetrics(res.data); setError(null) }
      } catch {
        if (!cancelled) setError('Бэкенд недоступен — данные ещё не подключены')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }

    fetch_()
    const iv = setInterval(fetch_, 5000)
    return () => { cancelled = true; clearInterval(iv) }
  }, [])

  return { metrics, loading, error }
}

// ── UI primitives ──────────────────────────────────────────────────────────

const COLOR_MAP = {
  blue:   'border-[#5b8cff]/25 bg-[#5b8cff]/[.07] text-[#a0b8ff]',
  green:  'border-emerald-400/25 bg-emerald-400/[.07] text-emerald-300',
  violet: 'border-violet-400/25 bg-violet-400/[.07] text-violet-300',
  amber:  'border-amber-400/25 bg-amber-400/[.07] text-amber-300',
  rose:   'border-rose-400/25 bg-rose-400/[.07] text-rose-300',
} as const

function StatCard({
  label, value, sub, color = 'blue',
}: {
  label: string; value: string | number; sub?: string; color?: keyof typeof COLOR_MAP
}) {
  return (
    <div className={`rounded-xl border p-4 ${COLOR_MAP[color]}`}>
      <div className="text-[10px] font-semibold uppercase tracking-wider text-slate-400">{label}</div>
      <div className="mt-1.5 text-[22px] font-bold leading-none text-slate-50">{value}</div>
      {sub && <div className="mt-1 text-[11px] text-slate-500">{sub}</div>}
    </div>
  )
}

function SectionHeader({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-4 flex items-center gap-3">
      <span className="whitespace-nowrap text-[11px] font-semibold uppercase tracking-[1.4px] text-slate-400">
        {children}
      </span>
      <div className="h-px flex-1 bg-white/[.05]" />
    </div>
  )
}

function StateChip({ state }: { state: 'buy' | 'sell' | 'neutral' }) {
  if (state === 'buy')
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-emerald-400/15 px-2 py-0.5 text-[10px] font-bold text-emerald-300">
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />buy
      </span>
    )
  if (state === 'sell')
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-rose-400/15 px-2 py-0.5 text-[10px] font-bold text-rose-300">
        <span className="h-1.5 w-1.5 rounded-full bg-rose-400" />sell
      </span>
    )
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-white/[.05] px-2 py-0.5 text-[10px] text-slate-500">
      —
    </span>
  )
}

// ── Strategy Workers section ───────────────────────────────────────────────

function relativeTime(isoStr: string): string {
  if (!isoStr) return '—'
  const diffSec = Math.floor((Date.now() - new Date(isoStr).getTime()) / 1000)
  if (diffSec < 60) return `${diffSec}s ago`
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`
  return `${Math.floor(diffSec / 3600)}h ago`
}

type WorkerFilter = 'all' | 'active' | 'stopped' | 'panics' | 'dropped' | 'problems'

function WorkersSection({ workers }: { workers?: StrategyWorkerStat[] }) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState<WorkerFilter>('all')
  if (!workers || workers.length === 0) return null

  const totalPanics  = workers.reduce((s, w) => s + w.panic_count, 0)
  const totalDropped = workers.reduce((s, w) => s + w.tasks_dropped, 0)
  const processing   = workers.filter(w => w.is_processing).length
  const hasAlerts    = totalPanics > 0 || totalDropped > 0

  const filtered = workers.filter(w => {
    switch (filter) {
      case 'active':   return w.status === 'active'
      case 'stopped':  return w.status !== 'active'
      case 'panics':   return w.panic_count > 0
      case 'dropped':  return w.tasks_dropped > 0
      case 'problems': return w.panic_count > 0 || w.tasks_dropped > 0
      default:         return true
    }
  })

  const short = (id: string) => id.length > 8 ? id.slice(0, 8) : id

  const filterBtns: { key: WorkerFilter; label: string; count?: number; alert?: boolean }[] = [
    { key: 'all',      label: 'Все',         count: workers.length },
    { key: 'active',   label: 'Активные',    count: workers.filter(w => w.status === 'active').length },
    { key: 'stopped',  label: 'Остановленные', count: workers.filter(w => w.status !== 'active').length },
    { key: 'panics',   label: 'Паники',      count: workers.filter(w => w.panic_count > 0).length,   alert: totalPanics > 0 },
    { key: 'dropped',  label: 'Dropped',     count: workers.filter(w => w.tasks_dropped > 0).length, alert: totalDropped > 0 },
    { key: 'problems', label: 'Проблемы',    count: workers.filter(w => w.panic_count > 0 || w.tasks_dropped > 0).length, alert: hasAlerts },
  ]

  return (
    <section>
      <SectionHeader>Strategy Workers</SectionHeader>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Воркеров"         value={workers.length}    color="blue"   />
        <StatCard label="Выполняют задачу" value={processing}        color={processing > 0 ? 'green' : 'blue'} />
        <StatCard label="Паник"            value={totalPanics}       color={totalPanics > 0 ? 'rose' : 'blue'} />
        <StatCard label="Dropped задач"    value={totalDropped}      color={totalDropped > 0 ? 'amber' : 'blue'} />
      </div>

      <div className="mt-4 overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
        {/* Header row with toggle + alert badge */}
        <button
          className="flex w-full cursor-pointer items-center gap-3 px-4 py-2.5 text-left hover:bg-white/[.02]"
          onClick={() => setOpen(v => !v)}
        >
          <svg
            className={`h-3 w-3 shrink-0 text-slate-500 transition-transform ${open ? 'rotate-90' : ''}`}
            viewBox="0 0 6 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"
          >
            <polyline points="1,1 5,5 1,9" />
          </svg>
          <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-400">
            Детали воркеров
          </span>
          {hasAlerts && (
            <span className="rounded-full bg-rose-400/20 px-2 py-0.5 text-[10px] font-bold text-rose-300">
              ⚠ требует внимания
            </span>
          )}
        </button>

        {open && (
          <>
            {/* Filter bar */}
            <div className="flex flex-wrap gap-1.5 border-t border-white/[.06] px-4 py-2.5">
              {filterBtns.map(btn => (
                <button
                  key={btn.key}
                  onClick={() => setFilter(btn.key)}
                  className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-[11px] font-semibold transition-colors ${
                    filter === btn.key
                      ? btn.alert
                        ? 'bg-rose-400/25 text-rose-200'
                        : 'bg-[#5b8cff]/20 text-[#a0b8ff]'
                      : 'bg-white/[.05] text-slate-400 hover:bg-white/[.08]'
                  }`}
                >
                  {btn.label}
                  {btn.count !== undefined && (
                    <span className={`rounded-full px-1.5 py-0 text-[10px] ${
                      filter === btn.key ? 'bg-white/20' : 'bg-white/[.07]'
                    }`}>{btn.count}</span>
                  )}
                </button>
              ))}
            </div>

            {/* Table */}
            <div className="overflow-x-auto border-t border-white/[.06]">
              <table className="w-full text-[12px]">
                <thead>
                  <tr className="border-b border-white/[.05] text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                    <th className="px-4 py-2 text-left">Пользователь</th>
                    <th className="px-4 py-2 text-left">Аккаунт</th>
                    <th className="px-4 py-2 text-left">Символ</th>
                    <th className="px-4 py-2 text-left">Статус</th>
                    <th className="px-4 py-2 text-right">Очередь</th>
                    <th className="px-4 py-2 text-center">Активен</th>
                    <th className="px-4 py-2 text-right">Паник</th>
                    <th className="px-4 py-2 text-right">Dropped</th>
                    <th className="px-4 py-2 text-right">Последняя задача</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.length === 0 ? (
                    <tr>
                      <td colSpan={9} className="px-4 py-8 text-center text-[12px] text-slate-600">
                        Нет воркеров по выбранному фильтру
                      </td>
                    </tr>
                  ) : filtered.map(w => {
                    const rowAlert = w.panic_count > 0 || w.tasks_dropped > 0
                    const lastSec = w.last_task_at
                      ? Math.floor((Date.now() - new Date(w.last_task_at).getTime()) / 1000)
                      : null
                    const stale = w.status === 'active' && lastSec !== null && lastSec > 60

                    return (
                      <tr
                        key={w.strategy_id}
                        className={`border-b border-white/[.03] transition-colors hover:bg-white/[.02] ${
                          rowAlert ? 'bg-rose-500/[.04]' : ''
                        }`}
                      >
                        <td className="px-4 py-2.5 text-slate-300" title={w.owner_id}>{w.owner_username || <span className="font-mono text-slate-500">{short(w.owner_id)}</span>}</td>
                        <td className="px-4 py-2.5 text-slate-300" title={w.account_id}>{w.account_label || <span className="font-mono text-slate-500">{short(w.account_id)}</span>}</td>
                        <td className="px-4 py-2.5 font-mono font-semibold text-slate-200">{w.symbol}</td>
                        <td className="px-4 py-2.5">
                          <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-bold ${
                            w.status === 'active'
                              ? 'bg-emerald-400/15 text-emerald-300'
                              : 'bg-white/[.06] text-slate-500'
                          }`}>
                            <span className={`h-1.5 w-1.5 rounded-full ${w.status === 'active' ? 'bg-emerald-400' : 'bg-slate-600'}`} />
                            {w.status}
                          </span>
                        </td>
                        <td className={`px-4 py-2.5 text-right font-mono ${
                          w.queue_depth > 32 ? 'text-rose-300' : w.queue_depth > 10 ? 'text-amber-300' : 'text-slate-400'
                        }`}>{w.queue_depth}</td>
                        <td className="px-4 py-2.5 text-center">
                          {w.is_processing
                            ? <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-blue-400" />
                            : <span className="text-slate-600">—</span>
                          }
                        </td>
                        <td className={`px-4 py-2.5 text-right font-mono ${w.panic_count > 0 ? 'font-bold text-rose-300' : 'text-slate-500'}`}>
                          {w.panic_count}
                        </td>
                        <td className={`px-4 py-2.5 text-right font-mono ${w.tasks_dropped > 0 ? 'font-bold text-amber-300' : 'text-slate-500'}`}>
                          {w.tasks_dropped}
                        </td>
                        <td className={`px-4 py-2.5 text-right text-slate-500 ${stale ? 'text-rose-400' : ''}`}>
                          {relativeTime(w.last_task_at)}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </section>
  )
}

// ── Monitoring tab ─────────────────────────────────────────────────────────

function MonitoringTab({ metrics, error }: { metrics: AdminMetrics | null; error: string | null }) {
  const [unitsOpen, setUnitsOpen] = useState(false)

  const dash = (v: number | undefined, decimals = 0, suffix = ''): string => {
    if (v == null) return '—'
    const s = decimals ? v.toFixed(decimals) : v.toLocaleString('ru-RU')
    return s + suffix
  }

  const sig = metrics?.signal
  const str = metrics?.strategy

  return (
    <div className="flex flex-col gap-9">

      {error && (
        <div className="rounded-xl border border-amber-400/20 bg-amber-400/[.06] px-4 py-3 text-[12px] leading-relaxed text-amber-300">
          {error}
        </div>
      )}

      {/* ── Signal Engine ── */}
      <section>
        <SectionHeader>Signal Engine</SectionHeader>

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          <StatCard label="Compute Units"  value={dash(sig?.activeUnits)}                color="blue"   />
          <StatCard label="Подписок"       value={dash(sig?.activeSubs)}                 color="violet" />
          <StatCard label="WS соединений"  value={dash(sig?.wsConnections)}              color="blue"   />
          <StatCard label="Compute / сек"  value={dash(sig?.computesPerSec, 1)}          color="green"  />
          <StatCard label="CPU (накоплено)" value={dash(sig?.cpuTimeMs, 0, ' ms')}       color="amber"  />
          <StatCard label="Буферы (RAM)"   value={dash(sig?.bufferMemMB, 1, ' MB')}      color="violet" />
        </div>

        <div className="mt-4 overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
          <button
            className="flex w-full cursor-pointer items-center gap-3 px-4 py-2.5 text-left hover:bg-white/[.02]"
            onClick={() => setUnitsOpen(v => !v)}
          >
            <svg
              className={`h-3 w-3 shrink-0 text-slate-500 transition-transform ${unitsOpen ? 'rotate-90' : ''}`}
              viewBox="0 0 6 10" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"
            >
              <polyline points="1,1 5,5 1,9" />
            </svg>
            <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-400">
              Compute Units
            </span>
            {sig?.units.length != null && (
              <span className="rounded-full bg-white/[.06] px-2 py-0.5 font-mono text-[10px] text-slate-400">
                {sig.units.length}
              </span>
            )}
          </button>

          {unitsOpen && <div className="overflow-x-auto border-t border-white/[.06]">
            <table className="w-full text-[12px]">
              <thead>
                <tr className="border-b border-white/[.05] text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-2 text-left">Symbol</th>
                  <th className="px-4 py-2 text-left">TF</th>
                  <th className="px-4 py-2 text-left">Сигналы</th>
                  <th className="px-4 py-2 text-right">Подписок</th>
                  <th className="px-4 py-2 text-right">Avg ms</th>
                  <th className="px-4 py-2 text-right">Обсчётов</th>
                  <th className="px-4 py-2 text-right">Последний</th>
                  <th className="px-4 py-2 text-center">Состояние</th>
                </tr>
              </thead>
              <tbody>
                {!sig?.units.length ? (
                  <tr>
                    <td colSpan={8} className="px-4 py-10 text-center text-[12px] text-slate-600">
                      Нет активных compute units
                    </td>
                  </tr>
                ) : sig.units
                    .slice()
                    .sort((a, b) => b.subscribers - a.subscribers)
                    .map(u => (
                  <tr key={u.hash} className="border-b border-white/[.03] transition-colors hover:bg-white/[.02]">
                    <td className="px-4 py-2.5 font-mono font-semibold text-slate-200">{u.symbol}</td>
                    <td className="px-4 py-2.5 font-mono text-slate-400">{u.interval}</td>
                    <td className="max-w-[240px] truncate px-4 py-2.5 text-slate-300">{u.signals}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-slate-300">{u.subscribers}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-slate-400">{u.avgComputeMs.toFixed(3)}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-slate-500">
                      {u.computeCount.toLocaleString('ru-RU')}
                    </td>
                    <td className="px-4 py-2.5 text-right text-slate-500">{u.lastComputedSec}s ago</td>
                    <td className="px-4 py-2.5 text-center">
                      <StateChip state={u.lastState} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>}
        </div>
      </section>

      {/* ── Strategy Engine ── */}
      <section>
        <SectionHeader>Strategy Engine</SectionHeader>

        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <StatCard label="Активных стратегий" value={dash(str?.activeStrategies)} color="blue"   />
          <StatCard label="Активных циклов"    value={dash(str?.activeCycles)}     color="green"  />
          <StatCard label="Ордеров сегодня"    value={dash(str?.ordersToday)}      color="violet" />
          <StatCard label="Исполнено сегодня"  value={dash(str?.fillsToday)}       color="amber"  />
        </div>

        {str?.accounts && str.accounts.length > 0 && (
          <div className="mt-4 overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
            <div className="border-b border-white/[.06] px-4 py-2.5">
              <span className="text-[11px] font-semibold uppercase tracking-wider text-slate-400">
                По аккаунтам
              </span>
            </div>
            <table className="w-full text-[12px]">
              <thead>
                <tr className="border-b border-white/[.05] text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-2 text-left">Аккаунт</th>
                  <th className="px-4 py-2 text-right">Стратегий</th>
                  <th className="px-4 py-2 text-right">Циклов</th>
                </tr>
              </thead>
              <tbody>
                {str.accounts.map(a => (
                  <tr key={a.id} className="border-b border-white/[.03] hover:bg-white/[.02]">
                    <td className="px-4 py-2.5 font-medium text-slate-200">{a.name}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-slate-300">{a.strategies}</td>
                    <td className="px-4 py-2.5 text-right font-mono text-slate-400">{a.cycles}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* ── Strategy Workers ── */}
      <WorkersSection workers={metrics?.strategy_workers} />

      {/* ── Global Warmer ── */}
      <section>
        <SectionHeader>Global Warmer</SectionHeader>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          <StatCard label="Символов"      value={dash(metrics?.global_warmer?.symbols)}                     color="blue"   />
          <StatCard label="Интервалов"    value={dash(metrics?.global_warmer?.intervals?.length)}            color="violet" />
          <StatCard label="Тёплых слотов" value={dash(metrics?.global_warmer?.warm_count)}                  color="green"  />
          <StatCard label="Всего слотов"  value={dash(metrics?.global_warmer?.total_slots)}                  color="blue"   />
          <StatCard label="Прогрев %"     value={dash(metrics?.global_warmer?.warm_pct, 1, '%')}             color="amber"  sub={`${dash(metrics?.global_warmer?.prefetch_ms)} ms prefetch`} />
          <StatCard label="Интервалы"     value={metrics?.global_warmer?.intervals?.join(', ') ?? '—'}       color="violet" />
        </div>
      </section>

      {/* ── Ticker Hub ── */}
      <section>
        <SectionHeader>Ticker Hub</SectionHeader>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <StatCard label="Символов"        value={dash(metrics?.ticker_hub?.symbols)}      color="blue"   />
          <StatCard
            label="С ценой"
            value={metrics?.ticker_hub != null
              ? `${dash(metrics.ticker_hub.warm_symbols)} / ${dash(metrics.ticker_hub.symbols)}`
              : '—'}
            color="green"
            sub={metrics?.ticker_hub != null && metrics.ticker_hub.symbols > 0
              ? `${((metrics.ticker_hub.warm_symbols / metrics.ticker_hub.symbols) * 100).toFixed(1)}%`
              : undefined}
          />
          <StatCard label="WS соединений"   value={dash(metrics?.ticker_hub?.ws_connections)} color="violet" />
        </div>
      </section>

      {/* ── Bot Engine ── */}
      <section>
        <SectionHeader>Bot Engine</SectionHeader>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <StatCard label="Активных ботов"  value={dash(metrics?.bot_engine?.bots_active)}     color="blue"   />
          <StatCard label="Групп"           value={dash(metrics?.bot_engine?.groups_computed)}  color="violet" />
          <StatCard label="Возможностей"    value={dash(metrics?.bot_engine?.opportunities)}    color="green"  />
          <StatCard label="Тик (мс)"        value={dash(metrics?.bot_engine?.last_tick_ms)}     color="amber"  />
          <StatCard
            label="Последний тик"
            value={metrics?.bot_engine?.last_tick_at
              ? new Date(metrics.bot_engine.last_tick_at).toLocaleTimeString('ru-RU')
              : '—'}
            color="blue"
          />
        </div>
      </section>

    </div>
  )
}

// ── Signal/indicator types tab ─────────────────────────────────────────────

type ContentStatus = 'enabled' | 'test' | 'disabled'

interface ContentTypeItem {
  id: string
  name: string
  status: ContentStatus
  panel: string
}

const STATUS_CYCLE: Record<ContentStatus, ContentStatus> = {
  disabled: 'test',
  test:     'enabled',
  enabled:  'disabled',
}

function useContentTypes(endpoint: string, patchEndpoint: string) {
  const [items, setItems] = useState<ContentTypeItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      const res = await apiClient.get<ContentTypeItem[]>(endpoint)
      setItems(res.data)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg.includes('403') || msg.toLowerCase().includes('forbidden')
        ? 'Нет доступа — убедитесь что ваш email добавлен в ADMIN_EMAILS на сервере'
        : `Ошибка загрузки: ${msg}`)
    } finally {
      setLoading(false)
    }
  }, [endpoint])

  useEffect(() => { load() }, [load])

  const toggle = useCallback(async (id: string) => {
    const prev = items.find(x => x.id === id)
    if (!prev) return
    const next = STATUS_CYCLE[prev.status]
    setItems(cur => cur.map(x => x.id === id ? { ...x, status: next } : x))
    try {
      await apiClient.patch(`${patchEndpoint}/${id}`, { status: next })
    } catch {
      setItems(cur => cur.map(x => x.id === id ? { ...x, status: prev.status } : x))
    }
  }, [items, patchEndpoint])

  const updatePanel = useCallback(async (id: string, panel: string) => {
    const prev = items.find(x => x.id === id)
    if (!prev || prev.panel === panel) return
    setItems(cur => cur.map(x => x.id === id ? { ...x, panel } : x))
    try {
      await apiClient.patch(`${patchEndpoint}/${id}`, { panel })
    } catch {
      setItems(cur => cur.map(x => x.id === id ? { ...x, panel: prev.panel } : x))
    }
  }, [items, patchEndpoint])

  return { items, loading, error, toggle, updatePanel }
}

// ── Shared layout helpers ──────────────────────────────────────────────────

const COINS_ADMIN = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'ADAUSDT', 'DOGEUSDT', 'AVAXUSDT', 'DOTUSDT', 'MATICUSDT',
  'LTCUSDT', 'LINKUSDT', 'TRXUSDT', 'TONUSDT', 'SUIUSDT',
]

const TIMEFRAMES_ADMIN = [
  { label: '1м', value: '1m' }, { label: '5м', value: '5m' },
  { label: '15м', value: '15m' }, { label: '30м', value: '30m' },
  { label: '1ч', value: '1h' }, { label: '4ч', value: '4h' },
  { label: '1д', value: '1d' },
]

const DRAG_KEY_ADMIN = 'application/x-sis-item-admin'

function VSplitAdmin({ onMouseDown }: { onMouseDown: (e: React.MouseEvent) => void }) {
  return (
    <div onMouseDown={onMouseDown}
      className="group relative z-10 flex w-3 flex-shrink-0 cursor-col-resize items-center justify-center select-none">
      <div className="flex flex-col gap-[3px] opacity-0 transition-opacity group-hover:opacity-100">
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
      </div>
    </div>
  )
}

function HSplitAdmin({ onMouseDown }: { onMouseDown: (e: React.MouseEvent) => void }) {
  return (
    <div onMouseDown={onMouseDown}
      className="group relative z-10 flex h-3 flex-shrink-0 cursor-row-resize items-center justify-center select-none">
      <div className="flex flex-row gap-[3px] opacity-0 transition-opacity group-hover:opacity-100">
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
        <span className="h-[3px] w-[3px] rounded-full bg-white/30" />
      </div>
    </div>
  )
}

const STATUS_STYLE: Record<ContentStatus, { cls: string; label: string; icon: React.ReactNode }> = {
  enabled:  { cls: 'bg-emerald-400/15 border-emerald-400/30 text-emerald-300 hover:bg-emerald-400/25', label: 'вкл',  icon: <ToggleRight size={11} /> },
  test:     { cls: 'bg-amber-400/15 border-amber-400/30 text-amber-300 hover:bg-amber-400/25',         label: 'тест', icon: <FlaskConical size={11} /> },
  disabled: { cls: 'bg-white/[.04] border-white/[.10] text-slate-500 hover:bg-white/[.08]',            label: 'выкл', icon: <ToggleLeft size={11} /> },
}

function EnableToggle({ status, onToggle }: { status: ContentStatus; onToggle: () => void }) {
  const s = STATUS_STYLE[status]
  return (
    <button
      type="button"
      onClick={e => { e.stopPropagation(); onToggle() }}
      title="Сменить статус"
      className={`absolute right-2 top-2 z-20 flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] font-semibold border transition-all ${s.cls}`}
    >
      {s.icon}{s.label}
    </button>
  )
}

interface AdminConstructorItem { type: 'indicator' | 'signal'; id: string }
interface AdminFlyingCard {
  item: { type: 'indicator'; def: IndicatorDef<BaseParams> } | { type: 'signal'; def: SignalDef<Record<string, unknown>> }
  sourceRect: DOMRect
}
interface AdminDragData { itemType: 'indicator' | 'signal'; id: string }

type AdminPanelItem =
  | { itemType: 'indicator'; def: IndicatorDef<BaseParams>;              adminItem: ContentTypeItem | undefined }
  | { itemType: 'signal';    def: SignalDef<Record<string, unknown>>;    adminItem: ContentTypeItem | undefined }

// ── RSI Test override control ──────────────────────────────────────────────

function RsiTestOverride() {
  const [active, setActive]   = useState(false)
  const [value,  setValue]    = useState(50)
  const [saving, setSaving]   = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    apiClient.get<{ active: boolean; value: number }>('/admin/signal-override/rsi-test')
      .then(r => {
        setActive(r.data.active)
        if (r.data.active) {
          setValue(r.data.value)
          setRsiTestOverride(r.data.value)
        }
      })
      .catch(() => {})
    return () => { setRsiTestOverride(null) }
  }, [])

  async function sendValue(v: number | null) {
    setSaving(true)
    try {
      await apiClient.put('/admin/signal-override/rsi-test', { value: v })
    } finally {
      setSaving(false)
    }
  }

  function handleSlider(e: React.ChangeEvent<HTMLInputElement>) {
    const v = Number(e.target.value)
    setValue(v)
    setRsiTestOverride(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => sendValue(v), 200)
  }

  async function handleToggle() {
    const next = !active
    setActive(next)
    setRsiTestOverride(next ? value : null)
    await sendValue(next ? value : null)
  }

  const stateLabel = value <= 30 ? { label: 'Buy', color: '#5be0a0' }
    : value >= 70 ? { label: 'Sell', color: '#fca5a5' }
    : { label: 'Neutral', color: '#8babff' }

  return (
    <div
      className="mt-2 rounded-[9px] px-3 py-2.5 flex flex-col gap-2"
      style={{ background: 'rgba(0,0,0,.25)', border: '1px solid rgba(255,255,255,.07)' }}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-semibold text-[#8b9ab8] uppercase tracking-[.6px]">RSI override</span>
        <button
          type="button"
          onClick={handleToggle}
          className={`text-[10px] font-bold px-2 py-0.5 rounded-[5px] transition-colors ${
            active
              ? 'bg-amber-400/20 border border-amber-400/40 text-amber-300'
              : 'bg-white/[.05] border border-white/[.10] text-slate-500'
          }`}
        >
          {active ? 'ON' : 'OFF'}
        </button>
      </div>
      {active && (
        <>
          <div className="flex items-center gap-2">
            <input
              type="range" min={0} max={100} step={0.5}
              value={value}
              onChange={handleSlider}
              className="flex-1 h-1.5 rounded-full appearance-none cursor-pointer"
              style={{ accentColor: stateLabel.color }}
            />
            <span className="font-mono text-[13px] font-semibold w-9 text-right" style={{ color: stateLabel.color }}>
              {value.toFixed(1)}
            </span>
          </div>
          <div className="flex items-center justify-between text-[10px]">
            <span className="text-[#5b6479]">0 — Buy zone</span>
            <span className="font-semibold" style={{ color: stateLabel.color }}>{stateLabel.label}</span>
            <span className="text-[#5b6479]">Sell zone — 100</span>
          </div>
          {saving && <span className="text-[9px] text-[#5b6479]">сохранение…</span>}
        </>
      )}
    </div>
  )
}

function SignalTypesTab() {
  const signals    = useContentTypes('/admin/signal-types',    '/admin/signal-types')
  const indicators = useContentTypes('/admin/indicator-types', '/admin/indicator-types')

  // Distribute items across panels based on their `panel` DB field
  const indicatorPanelItems: AdminPanelItem[] = [
    ...INDICATORS.map(ind => ({
      itemType: 'indicator' as const,
      def: ind as IndicatorDef<BaseParams>,
      adminItem: indicators.items.find(x => x.id === ind.id),
    })).filter(x => (x.adminItem?.panel ?? 'indicator') === 'indicator'),
    ...SIGNALS.map(sig => ({
      itemType: 'signal' as const,
      def: sig as SignalDef<Record<string, unknown>>,
      adminItem: signals.items.find(x => x.id === sig.id),
    })).filter(x => x.adminItem?.panel === 'indicator'),
  ]

  const signalPanelItems: AdminPanelItem[] = [
    ...SIGNALS.map(sig => ({
      itemType: 'signal' as const,
      def: sig as SignalDef<Record<string, unknown>>,
      adminItem: signals.items.find(x => x.id === sig.id),
    })).filter(x => (x.adminItem?.panel ?? 'signal') === 'signal'),
    ...INDICATORS.map(ind => ({
      itemType: 'indicator' as const,
      def: ind as IndicatorDef<BaseParams>,
      adminItem: indicators.items.find(x => x.id === ind.id),
    })).filter(x => x.adminItem?.panel === 'signal'),
  ]

  const [selectedCoin, setSelectedCoin] = useState('BTCUSDT')
  const [selectedTf,   setSelectedTf]   = useState('1h')
  const { connected, candles } = useMarketIndicators(selectedCoin, selectedTf)

  const [constructorItems,      setConstructorItems]      = useState<AdminConstructorItem[]>([])
  const [isDragOverConstructor, setIsDragOverConstructor] = useState(false)
  const [isDragOverIndicators,  setIsDragOverIndicators]  = useState(false)
  const [isDragOverSignals,     setIsDragOverSignals]     = useState(false)
  const [flyingCard,            setFlyingCard]            = useState<AdminFlyingCard | null>(null)
  const [paramsMap,             setParamsMap]             = useState<Record<string, Record<string, unknown>>>({})

  function getP(id: string, defaults: Record<string, unknown>) { return paramsMap[id] ?? defaults }
  function setP(id: string, next: Record<string, unknown>)     { setParamsMap(p => ({ ...p, [id]: next })) }

  const navigate = useNavigate()

  function openSignalChart(sig: SignalDef<Record<string, unknown>>, params?: Record<string, unknown>) {
    setSignalChartIntent({
      signalId: sig.id,
      signalName: sig.name,
      params: params ?? (sig.defaults as Record<string, unknown>),
      symbol: selectedCoin,
      tf: selectedTf,
    })
    navigate('/signal-chart')
  }

  const containerRef = useRef<HTMLDivElement>(null)
  const [topLeftPct, setTopLeftPct] = useState(50)
  const [bottomPx,   setBottomPx]   = useState(280)
  const [botLeftPct, setBotLeftPct] = useState(66)

  const onTopSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX; const startPct = topLeftPct
    const mv = (ev: MouseEvent) => {
      const w = containerRef.current?.clientWidth ?? 1
      setTopLeftPct(Math.min(80, Math.max(20, startPct + ((ev.clientX - startX) / w) * 100)))
    }
    const up = () => { window.removeEventListener('mousemove', mv); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', mv); window.addEventListener('mouseup', up)
  }, [topLeftPct])

  const onHSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startY = e.clientY; const startPx = bottomPx
    const mv = (ev: MouseEvent) => setBottomPx(Math.min(520, Math.max(120, startPx - (ev.clientY - startY))))
    const up = () => { window.removeEventListener('mousemove', mv); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', mv); window.addEventListener('mouseup', up)
  }, [bottomPx])

  const onBotSplit = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX; const startPct = botLeftPct
    const mv = (ev: MouseEvent) => {
      const w = containerRef.current?.clientWidth ?? 1
      setBotLeftPct(Math.min(85, Math.max(15, startPct + ((ev.clientX - startX) / w) * 100)))
    }
    const up = () => { window.removeEventListener('mousemove', mv); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', mv); window.addEventListener('mouseup', up)
  }, [botLeftPct])

  function startDrag(e: React.DragEvent, itemType: 'indicator' | 'signal', id: string) {
    const data: AdminDragData = { itemType, id }
    e.dataTransfer.setData(DRAG_KEY_ADMIN, JSON.stringify(data))
    e.dataTransfer.effectAllowed = 'copy'
  }

  function parseDrag(e: React.DragEvent): AdminDragData | null {
    const raw = e.dataTransfer.getData(DRAG_KEY_ADMIN)
    try { return raw ? JSON.parse(raw) : null } catch { return null }
  }

  function handlePanelDrop(e: React.DragEvent, targetPanel: 'indicator' | 'signal') {
    e.preventDefault()
    setIsDragOverIndicators(false); setIsDragOverSignals(false)
    const data = parseDrag(e)
    if (!data) return
    const currentPanel = data.itemType === 'indicator'
      ? (indicators.items.find(x => x.id === data.id)?.panel ?? 'indicator')
      : (signals.items.find(x => x.id === data.id)?.panel ?? 'signal')
    if (currentPanel !== targetPanel) {
      if (data.itemType === 'indicator') indicators.updatePanel(data.id, targetPanel)
      else signals.updatePanel(data.id, targetPanel)
    }
  }

  function panelDragOver(e: React.DragEvent, setter: (v: boolean) => void) {
    if (!e.dataTransfer.types.includes(DRAG_KEY_ADMIN)) return
    e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setter(true)
  }
  function panelDragLeave(e: React.DragEvent, setter: (v: boolean) => void) {
    if ((e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) return
    setter(false)
  }

  function onConstructorDrop(e: React.DragEvent) {
    e.preventDefault(); setIsDragOverConstructor(false)
    const data = parseDrag(e)
    if (!data) return
    setConstructorItems(prev => prev.some(i => i.id === data.id) ? prev : [...prev, { type: data.itemType, id: data.id }])
  }
  function removeItem(id: string) { setConstructorItems(prev => prev.filter(i => i.id !== id)) }

  function renderPanelCard(item: AdminPanelItem) {
    const status = item.adminItem?.status ?? 'enabled'
    const onToggle = () => item.itemType === 'indicator' ? indicators.toggle(item.def.id) : signals.toggle(item.def.id)
    return (
      <div key={item.def.id} className="relative cursor-grab active:cursor-grabbing" draggable
        onDragStart={e => startDrag(e, item.itemType, item.def.id)}>
        <EnableToggle status={status} onToggle={onToggle} />
        <div className={`transition-opacity ${status !== 'disabled' ? 'opacity-100' : 'opacity-40'}`}>
          {item.itemType === 'indicator' ? (
            <IndicatorCard
              indicator={item.def}
              candles={candles}
              value={getP(item.def.id, item.def.defaults as Record<string, unknown>) as BaseParams}
              onChange={p => setP(item.def.id, p)}
              onSettingsClick={rect => setFlyingCard({ item: { type: 'indicator', def: item.def }, sourceRect: rect })}
              hideBadge
            />
          ) : (
            <>
              <SignalCard
                signal={item.def}
                candles={candles}
                value={getP(item.def.id, item.def.defaults)}
                onChange={p => setP(item.def.id, p)}
                onSettingsClick={rect => setFlyingCard({ item: { type: 'signal', def: item.def }, sourceRect: rect })}
                hideBadge
                onChartClick={() => openSignalChart(item.def as SignalDef<Record<string,unknown>>, getP(item.def.id, item.def.defaults))}
              />
              {item.def.id === 'rsi-test' && <RsiTestOverride />}
            </>
          )}
        </div>
      </div>
    )
  }

  const loading = signals.loading || indicators.loading
  const errorMsg = signals.error || indicators.error

  return (
    <div className="flex h-full flex-col overflow-hidden">

      {/* Toolbar */}
      <div className="flex h-10 flex-shrink-0 items-center gap-3 mb-2">
        <select value={selectedCoin} onChange={e => setSelectedCoin(e.target.value)}
          className="rounded border border-white/[.08] bg-white/[.04] px-2 py-0.5 text-xs text-slate-200 focus:outline-none">
          {COINS_ADMIN.map(c => <option key={c} value={c}>{c}</option>)}
        </select>
        <div className="flex gap-0.5">
          {TIMEFRAMES_ADMIN.map(tf => (
            <button key={tf.value} onClick={() => setSelectedTf(tf.value)}
              className={`rounded px-2 py-0.5 text-xs transition-colors ${selectedTf === tf.value ? 'bg-[#5b8cff]/30 text-[#b8c8ff]' : 'text-slate-500 hover:text-slate-300'}`}>
              {tf.label}
            </button>
          ))}
        </div>
        <span className={`ml-auto flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${connected ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/[.04] text-slate-500'}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : 'animate-pulse bg-slate-500'}`} />
          {connected ? 'Live' : 'Подключение...'}
        </span>
      </div>

      {errorMsg && (
        <div className="mb-2 rounded-xl border border-amber-400/20 bg-amber-400/[.06] px-4 py-2.5 text-[12px] text-amber-300">
          {errorMsg}
        </div>
      )}

      {/* Panel layout */}
      <div ref={containerRef} className="flex flex-1 flex-col overflow-hidden">

        {/* Top row */}
        <div className="flex flex-1 overflow-hidden" style={{ minHeight: 0 }}>

          {/* Indicators panel */}
          <div
            className={`flex flex-col overflow-hidden rounded-xl border bg-[#0d1018] transition-colors ${isDragOverIndicators ? 'border-blue-400/40 bg-blue-500/[.04]' : 'border-white/[.07]'}`}
            style={{ width: `${topLeftPct}%` }}
            onDragOver={e => panelDragOver(e, setIsDragOverIndicators)}
            onDragLeave={e => panelDragLeave(e, setIsDragOverIndicators)}
            onDrop={e => handlePanelDrop(e, 'indicator')}
          >
            <div className="rounded-t-xl border-b border-white/[.06] bg-blue-500/[.07] px-5 py-2.5 flex items-center gap-2">
              <span className="text-xs font-semibold uppercase tracking-wider text-blue-300/70">
                Индикаторы <span className="ml-1.5 font-mono text-blue-400/50">{indicatorPanelItems.length}</span>
              </span>
              <span className="ml-auto text-[10px] text-blue-400/40">
                {indicatorPanelItems.filter(x => x.adminItem?.status !== 'disabled').length} активно
              </span>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {loading ? (
                <div className="text-center text-[12px] text-slate-600 py-8">Загрузка...</div>
              ) : (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
                  {indicatorPanelItems.map(renderPanelCard)}
                </div>
              )}
            </div>
          </div>

          <VSplitAdmin onMouseDown={onTopSplit} />

          {/* Signals panel */}
          <div
            className={`flex flex-1 flex-col overflow-hidden rounded-xl border bg-[#0d1018] transition-colors ${isDragOverSignals ? 'border-violet-400/40 bg-violet-500/[.04]' : 'border-white/[.07]'}`}
            onDragOver={e => panelDragOver(e, setIsDragOverSignals)}
            onDragLeave={e => panelDragLeave(e, setIsDragOverSignals)}
            onDrop={e => handlePanelDrop(e, 'signal')}
          >
            <div className="rounded-t-xl border-b border-white/[.06] bg-violet-500/[.07] px-5 py-2.5 flex items-center gap-2">
              <span className="text-xs font-semibold uppercase tracking-wider text-violet-300/70">
                Сигналы <span className="ml-1.5 font-mono text-violet-400/50">{signalPanelItems.length}</span>
              </span>
              <span className="ml-auto text-[10px] text-violet-400/40">
                {signalPanelItems.filter(x => x.adminItem?.status !== 'disabled').length} активно
              </span>
            </div>
            <div className="flex-1 overflow-auto px-5 py-4">
              {loading ? (
                <div className="text-center text-[12px] text-slate-600 py-8">Загрузка...</div>
              ) : (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3">
                  <BybitNewsCard />
                  {signalPanelItems.map(renderPanelCard)}
                </div>
              )}
            </div>
          </div>

        </div>

        <HSplitAdmin onMouseDown={onHSplit} />

        {/* Bottom row */}
        <div className="flex flex-shrink-0 overflow-hidden" style={{ height: `${bottomPx}px` }}>

          {/* Constructor panel */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ width: `${botLeftPct}%` }}>
            <div className="rounded-t-xl border-b border-white/[.06] bg-emerald-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-emerald-300/70">
                Конструктор сигналов
                {constructorItems.length > 0 && (
                  <span className="ml-2 font-mono text-emerald-400/50">{constructorItems.length}</span>
                )}
              </span>
            </div>
            <div
              className={`relative flex-1 overflow-auto px-4 py-3 transition-colors ${isDragOverConstructor ? 'bg-[#5b8cff]/[.05]' : ''}`}
              onDragOver={e => panelDragOver(e, setIsDragOverConstructor)}
              onDragLeave={e => panelDragLeave(e, setIsDragOverConstructor)}
              onDrop={onConstructorDrop}
            >
              {isDragOverConstructor && (
                <div className="pointer-events-none absolute inset-2 rounded-lg border-2 border-dashed border-[#5b8cff]/40" />
              )}
              {constructorItems.length === 0 ? (
                <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
                  <span className="text-[28px] opacity-20">⊕</span>
                  <span className="text-[11px] text-slate-500">Перетащите индикаторы или сигналы сюда</span>
                </div>
              ) : (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
                  {constructorItems.map(item => {
                    const ind = item.type === 'indicator' ? INDICATORS.find(i => i.id === item.id) : null
                    const sig = item.type === 'signal'    ? SIGNALS.find(s => s.id === item.id)    : null
                    return (
                      <div key={item.id} className="relative">
                        {ind && (
                          <IndicatorCard
                            indicator={ind}
                            candles={candles}
                            value={getP(ind.id, ind.defaults as Record<string, unknown>) as BaseParams}
                            onChange={p => setP(ind.id, p)}
                            onSettingsClick={rect => setFlyingCard({ item: { type: 'indicator', def: ind as IndicatorDef<BaseParams> }, sourceRect: rect })}
                            hideBadge
                          />
                        )}
                        {sig && (
                          <SignalCard
                            signal={sig}
                            candles={candles}
                            value={getP(sig.id, sig.defaults)}
                            onChange={p => setP(sig.id, p)}
                            onSettingsClick={rect => setFlyingCard({ item: { type: 'signal', def: sig as SignalDef<Record<string, unknown>> }, sourceRect: rect })}
                            hideBadge
                            onChartClick={() => openSignalChart(sig as SignalDef<Record<string,unknown>>, getP(sig.id, sig.defaults))}
                          />
                        )}
                        <button type="button" onClick={() => removeItem(item.id)}
                          className="absolute right-2 top-2 z-10 flex h-5 w-5 items-center justify-center rounded-full bg-black/60 text-slate-400 hover:bg-rose-500/80 hover:text-white">
                          <X size={10} />
                        </button>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </div>

          <VSplitAdmin onMouseDown={onBotSplit} />

          {/* My Signal panel */}
          <div className="flex flex-1 flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]">
            <div className="rounded-t-xl border-b border-white/[.06] bg-sky-500/[.07] px-5 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-sky-300/70">Мой сигнал</span>
            </div>
            <div className="flex-1 overflow-auto px-4 py-3" />
          </div>

        </div>
      </div>

      {flyingCard && (
        <FlyingCardOverlay
          item={flyingCard.item}
          sourceRect={flyingCard.sourceRect}
          candles={candles}
          value={getP(flyingCard.item.def.id, flyingCard.item.def.defaults as Record<string, unknown>)}
          onChange={p => setP(flyingCard.item.def.id, p)}
          onClose={() => setFlyingCard(null)}
        />
      )}
    </div>
  )
}

// ── Users tab ──────────────────────────────────────────────────────────────

function UsersTab() {
  const { users, loading, action, refresh, fetchTransactions } = useAdminUsers()
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-slate-500">
        Загрузка...
      </div>
    )
  }
  return (
    <AdminUsersPage
      users={users}
      onAction={action}
      onRefresh={refresh}
      fetchTransactions={fetchTransactions}
    />
  )
}

// ── Page ───────────────────────────────────────────────────────────────────

const TABS = [
  { id: 'users',      label: 'Пользователи' },
  { id: 'bots',       label: 'Боты'         },
  { id: 'monitoring', label: 'Мониторинг'   },
  { id: 'signals',    label: 'Сигналы'      },
  { id: 'proxies',    label: 'Прокси'       },
  { id: 'defaults',   label: 'Дефолты'      },
  { id: 'bybit-news', label: 'Bybit News'   },
  { id: 'log-visualizer', label: 'Визуализатор' },
] as const

type TabId = typeof TABS[number]['id']

export function AdminPage() {
  const [tab, setTab] = useState<TabId>('users')
  const { metrics, error } = useAdminMetrics()

  return (
    <div className="flex h-full flex-col overflow-hidden bg-[#0a0d14] text-slate-200">

      {/* Header */}
      <div className="flex h-11 flex-shrink-0 items-center gap-4 border-b border-white/[.06] px-5">
        <span className="text-sm font-semibold text-slate-100">Админка</span>
        <div className="flex gap-0.5">
          {TABS.map(t => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={`rounded px-3 py-1 text-xs font-medium transition-colors ${
                tab === t.id
                  ? 'bg-[#5b8cff]/20 text-[#b8c8ff]'
                  : 'text-slate-500 hover:text-slate-300'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      {tab === 'users' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <UsersTab />
        </div>
      )}
      {tab === 'monitoring' && (
        <div className="flex-1 overflow-auto p-5">
          <MonitoringTab metrics={metrics} error={error} />
        </div>
      )}
      {tab === 'bots' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <AdminBotsTab />
        </div>
      )}
      {tab === 'signals' && (
        <div className="flex flex-1 flex-col overflow-hidden p-2.5">
          <SignalTypesTab />
        </div>
      )}
      {tab === 'proxies' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <AdminProxiesTab />
        </div>
      )}
      {tab === 'defaults' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <AdminDefaultsTab />
        </div>
      )}
      {tab === 'bybit-news' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <BybitNewsTab />
        </div>
      )}
      {tab === 'log-visualizer' && (
        <div className="flex flex-1 flex-col overflow-hidden">
          <LogVisualizerTab />
        </div>
      )}

    </div>
  )
}
