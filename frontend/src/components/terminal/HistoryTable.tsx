import { useEffect, useState } from 'react'
import { listExecutions } from '../../api/trader'
import type { TraderExecution } from '../../types'

interface Props { accountId?: string; symbol?: string }

function cycleFromLinkId(linkId: string | null | undefined): string | null {
  if (!linkId) return null
  const m = linkId.match(/^(SIS_STR|STR)-[a-f0-9]+-(\d+)-\d+-\d+$/)
  if (m) return m[2]
  const m2 = linkId.match(/^(SIS_STR|STP)-[a-f0-9]+-(tp|sl)-(\d+)-\d+$/)
  if (m2) return m2[3]
  return null
}

function labelFromLinkId(linkId: string | null | undefined): string {
  if (!linkId) return '—'
  if (/^(SIS_STR|STP)-[a-f0-9]+-tp-\d+-\d+$/.test(linkId)) return 'TP'
  if (/^(SIS_STR|STP)-[a-f0-9]+-sl-\d+-\d+$/.test(linkId)) return 'SL'
  const lvl = linkId.match(/^(SIS_STR|STR)-[a-f0-9]+-\d+-(\d+)-\d+$/)
  if (lvl) return `L${lvl[2]}`
  return '—'
}

export function HistoryTable({ accountId, symbol }: Props) {
  const [execs, setExecs] = useState<TraderExecution[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listExecutions({ account_id: accountId, type: 'Trade', symbol, page, limit: 50 })
      .then(r => { setExecs(r.executions); setTotal(r.total) })
      .finally(() => setLoading(false))
  }, [accountId, symbol, page])

  if (loading) return <div className="flex items-center justify-center h-full text-gray-500 dark:text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!execs.length) return <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">История пуста</p></div>

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-auto">
        <table className="w-full text-xs">
          <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
            <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
              <th className="text-left px-3 py-2">Дата</th>
              <th className="text-left px-3 py-2">Символ</th>
              <th className="text-left px-3 py-2">Сторона</th>
              <th className="text-left px-3 py-2">Тип</th>
              <th className="text-left px-3 py-2">Цикл</th>
              <th className="text-right px-3 py-2">Цена</th>
              <th className="text-right px-3 py-2">Кол-во</th>
              <th className="text-right px-3 py-2">Объём</th>
              <th className="text-right px-3 py-2">Комиссия</th>
            </tr>
          </thead>
          <tbody>
            {execs.map(e => (
              <tr key={e.id} className="border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-100 dark:hover:bg-gray-800/60">
                <td className="px-3 py-2 font-mono text-gray-500 dark:text-gray-400 whitespace-nowrap">
                  {new Date(e.exec_time).toLocaleString('ru-RU')}
                </td>
                <td className="px-3 py-2 font-mono font-medium">{e.symbol}</td>
                <td className="px-3 py-2">
                  {e.side && (
                    <span className={`text-[10px] px-1.5 py-0.5 rounded ${e.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                      {e.side}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2 font-mono text-slate-400 text-[10px]">
                  {labelFromLinkId(e.order_link_id)}
                </td>
                <td className="px-3 py-2 font-mono text-slate-400 text-[10px]">
                  {cycleFromLinkId(e.order_link_id) ?? <span className="text-slate-600">—</span>}
                </td>
                <td className="px-3 py-2 text-right font-mono">
                  {e.price ? parseFloat(e.price).toFixed(2) : '—'}
                </td>
                <td className="px-3 py-2 text-right font-mono">{e.qty ?? '—'}</td>
                <td className="px-3 py-2 text-right font-mono text-slate-300">
                  {e.exec_value ? parseFloat(e.exec_value).toFixed(2) : '—'}
                </td>
                <td className="px-3 py-2 text-right font-mono text-gray-500 dark:text-gray-400">
                  {e.exec_fee ? parseFloat(e.exec_fee).toFixed(4) : '—'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {total > 50 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-gray-200 dark:border-gray-700 flex-shrink-0 text-xs text-gray-500 dark:text-gray-400">
          <span>Всего: {total}</span>
          <div className="flex gap-1">
            <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="px-2 py-1 rounded bg-gray-100 dark:bg-gray-800 disabled:opacity-40">←</button>
            <span className="px-2 py-1">{page}</span>
            <button onClick={() => setPage(p => p + 1)} disabled={page * 50 >= total} className="px-2 py-1 rounded bg-gray-100 dark:bg-gray-800 disabled:opacity-40">→</button>
          </div>
        </div>
      )}
    </div>
  )
}
