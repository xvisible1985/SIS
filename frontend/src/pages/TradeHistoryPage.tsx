import { useState, useEffect, useCallback } from 'react'
import { getTradeHistory, type TradeHistoryRow, type TradeHistoryStats, type TradeHistoryParams } from '../api/tradeHistory'
import { apiClient } from '../api/client'
import type { Bot } from '../features/bots/types'

const LIMIT = 50

function fmt(n: number | null | undefined, decimals = 2): string {
  if (n == null) return '—'
  return n.toFixed(decimals)
}

function pnlClass(n: number | null | undefined): string {
  if (n == null) return ''
  return n >= 0 ? 'text-green-400' : 'text-red-400'
}

function relTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}с назад`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}м назад`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}ч назад`
  const d = Math.floor(h / 24)
  return `${d}д назад`
}

function StatsCards({ stats }: { stats: TradeHistoryStats }) {
  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mb-5">
      {[
        { label: 'Всего сделок', value: String(stats.total) },
        { label: 'Win Rate', value: `${fmt(stats.win_rate, 1)}%`, sub: `${stats.wins}W / ${stats.losses}L` },
        {
          label: 'Суммарный PnL',
          value: `${stats.total_pnl >= 0 ? '+' : ''}${fmt(stats.total_pnl)} $`,
          color: pnlClass(stats.total_pnl),
        },
        {
          label: 'Средний PnL',
          value: `${stats.avg_pnl >= 0 ? '+' : ''}${fmt(stats.avg_pnl)} $`,
          color: pnlClass(stats.avg_pnl),
        },
        {
          label: 'Лучшая сделка',
          value: stats.best_trade != null ? `+${fmt(stats.best_trade)} $` : '—',
          color: 'text-green-400',
        },
        {
          label: 'Худшая сделка',
          value: stats.worst_trade != null ? `${fmt(stats.worst_trade)} $` : '—',
          color: 'text-red-400',
        },
      ].map(c => (
        <div key={c.label} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl p-4">
          <div className="text-xs text-gray-500 dark:text-gray-400 mb-1">{c.label}</div>
          <div className={`text-lg font-semibold ${c.color ?? ''}`}>{c.value}</div>
          {c.sub && <div className="text-xs text-gray-500 mt-0.5">{c.sub}</div>}
        </div>
      ))}
    </div>
  )
}

function TradeRow({ t }: { t: TradeHistoryRow }) {
  const isTP = t.result === 'tp'
  return (
    <tr className="border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/30">
      <td className="py-2.5 px-3 text-sm font-medium">{t.symbol}</td>
      <td className="py-2.5 px-3 text-xs text-gray-500">
        <span className={`px-1.5 py-0.5 rounded text-xs font-medium ${t.direction === 'long' ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400' : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'}`}>
          {t.direction === 'long' ? 'LONG' : 'SHORT'}
        </span>
      </td>
      <td className="py-2.5 px-3 text-xs">
        <span className={`px-1.5 py-0.5 rounded font-medium ${isTP ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' : 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'}`}>
          {isTP ? 'TP' : 'SL'}
        </span>
      </td>
      <td className="py-2.5 px-3 text-xs text-gray-500">{t.bot_name ?? '—'}</td>
      <td className="py-2.5 px-3 text-xs text-right">{fmt(t.avg_entry, 4)}</td>
      <td className="py-2.5 px-3 text-xs text-right">{fmt(t.exit_price, 4)}</td>
      <td className="py-2.5 px-3 text-xs text-right">{fmt(t.volume_usdt, 1)}$</td>
      <td className={`py-2.5 px-3 text-sm text-right font-medium ${pnlClass(t.pnl)}`}>
        {t.pnl != null ? `${t.pnl >= 0 ? '+' : ''}${fmt(t.pnl)}$` : '—'}
        {t.pnl_pct != null && (
          <span className="text-xs ml-1 opacity-70">({t.pnl_pct >= 0 ? '+' : ''}{fmt(t.pnl_pct, 1)}%)</span>
        )}
      </td>
      <td className="py-2.5 px-3 text-xs text-gray-400 text-right">{relTime(t.closed_at)}</td>
    </tr>
  )
}

export function TradeHistoryPage() {
  const [bots, setBots] = useState<Bot[]>([])
  const [stats, setStats] = useState<TradeHistoryStats | null>(null)
  const [trades, setTrades] = useState<TradeHistoryRow[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)

  const [filterBot, setFilterBot] = useState('')
  const [filterResult, setFilterResult] = useState<'' | 'tp' | 'sl'>('')
  const [filterFrom, setFilterFrom] = useState('')
  const [filterTo, setFilterTo] = useState('')

  useEffect(() => {
    apiClient.get<{ catalog: Bot[]; mine: Bot[] }>('/bots')
      .then(r => setBots(r.data.mine ?? []))
      .catch(() => {})
  }, [])

  const load = useCallback(async (off = 0) => {
    setLoading(true)
    const params: TradeHistoryParams = { limit: LIMIT, offset: off }
    if (filterBot)    params.bot_id = filterBot
    if (filterResult) params.result = filterResult
    if (filterFrom)   params.from = new Date(filterFrom).toISOString()
    if (filterTo)     params.to   = new Date(filterTo + 'T23:59:59').toISOString()
    try {
      const res = await getTradeHistory(params)
      setStats(res.stats ?? null)
      setTrades(res.trades ?? [])
      setTotal(res.total ?? 0)
      setOffset(off)
    } catch {
      setTrades([])
    } finally {
      setLoading(false)
    }
  }, [filterBot, filterResult, filterFrom, filterTo])

  useEffect(() => { load(0) }, [load])

  return (
    <div className="max-w-7xl mx-auto px-4 py-6">
      <div className="flex items-center justify-between mb-5">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-gray-100">История сделок</h1>
        <span className="text-sm text-gray-500">{total} сделок</span>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-3 mb-5">
        <select
          value={filterBot}
          onChange={e => setFilterBot(e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        >
          <option value="">Все боты</option>
          {bots.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}
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
        <input
          type="date"
          value={filterFrom}
          onChange={e => setFilterFrom(e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        />
        <span className="self-center text-gray-400 text-sm">—</span>
        <input
          type="date"
          value={filterTo}
          onChange={e => setFilterTo(e.target.value)}
          className="text-sm border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-1.5 bg-white dark:bg-gray-900 text-gray-800 dark:text-gray-200"
        />
        {(filterBot || filterResult || filterFrom || filterTo) && (
          <button
            onClick={() => { setFilterBot(''); setFilterResult(''); setFilterFrom(''); setFilterTo('') }}
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
                  <th className="py-2.5 px-3 text-left font-medium">Символ</th>
                  <th className="py-2.5 px-3 text-left font-medium">Направление</th>
                  <th className="py-2.5 px-3 text-left font-medium">Результат</th>
                  <th className="py-2.5 px-3 text-left font-medium">Бот</th>
                  <th className="py-2.5 px-3 text-right font-medium">Вход avg</th>
                  <th className="py-2.5 px-3 text-right font-medium">Выход</th>
                  <th className="py-2.5 px-3 text-right font-medium">Объём</th>
                  <th className="py-2.5 px-3 text-right font-medium">PnL</th>
                  <th className="py-2.5 px-3 text-right font-medium">Закрыта</th>
                </tr>
              </thead>
              <tbody>
                {trades.map(t => <TradeRow key={t.id} t={t} />)}
              </tbody>
            </table>
          </div>
        )}

        {/* Pagination */}
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
