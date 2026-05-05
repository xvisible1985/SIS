import { useEffect, useState } from 'react'
import { listExecutions } from '../../api/trader'
import type { TraderExecution } from '../../types'

interface Props { accountId?: string }

export function ExecutionsTable({ accountId }: Props) {
  const [execs, setExecs] = useState<TraderExecution[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [type, setType] = useState('all')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listExecutions({ account_id: accountId, type: type === 'all' ? undefined : type, page, limit: 100 })
      .then(r => { setExecs(r.executions); setTotal(r.total) })
      .finally(() => setLoading(false))
  }, [accountId, type, page])

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-700 flex-shrink-0">
        {['all', 'Trade', 'Funding', 'Fee'].map(t => (
          <button key={t} onClick={() => { setType(t); setPage(1) }}
            className={`text-xs px-2 py-0.5 rounded transition-colors ${type === t ? 'bg-blue-500/20 text-blue-400' : 'text-gray-400 hover:text-white'}`}>
            {t === 'all' ? 'Все' : t}
          </button>
        ))}
      </div>
      {loading
        ? <div className="flex items-center justify-center flex-1 text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
        : !execs.length
          ? <div className="flex items-center justify-center flex-1 text-gray-400 text-sm">Нет данных</div>
          : (
            <div className="flex-1 overflow-auto">
              <table className="w-full text-xs">
                <thead className="sticky top-0 z-10 bg-gray-900">
                  <tr className="text-gray-400 border-b border-gray-700">
                    <th className="text-left px-3 py-2">Время</th>
                    <th className="text-left px-3 py-2">Символ</th>
                    <th className="text-left px-3 py-2">Тип</th>
                    <th className="text-left px-3 py-2">Сторона</th>
                    <th className="text-right px-3 py-2">Цена</th>
                    <th className="text-right px-3 py-2">Кол-во</th>
                    <th className="text-right px-3 py-2">Значение</th>
                    <th className="text-right px-3 py-2">Комиссия</th>
                    <th className="text-right px-3 py-2">Мейкер</th>
                  </tr>
                </thead>
                <tbody>
                  {execs.map(e => (
                    <tr key={e.id} className="border-b border-gray-700/50 hover:bg-gray-800/60">
                      <td className="px-3 py-2 font-mono text-gray-400 whitespace-nowrap">{new Date(e.exec_time).toLocaleString('ru-RU')}</td>
                      <td className="px-3 py-2 font-mono font-medium">{e.symbol}</td>
                      <td className="px-3 py-2">
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">{e.exec_type}</span>
                      </td>
                      <td className="px-3 py-2">
                        {e.side && <span className={`text-[10px] px-1.5 py-0.5 rounded ${e.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>{e.side}</span>}
                      </td>
                      <td className="px-3 py-2 text-right font-mono">{e.price ? parseFloat(e.price).toFixed(2) : '—'}</td>
                      <td className="px-3 py-2 text-right font-mono">{e.qty ?? '—'}</td>
                      <td className="px-3 py-2 text-right font-mono">{e.exec_value ? parseFloat(e.exec_value).toFixed(4) : '—'}</td>
                      <td className="px-3 py-2 text-right font-mono text-gray-400">{e.exec_fee ? parseFloat(e.exec_fee).toFixed(6) : '—'}</td>
                      <td className="px-3 py-2 text-right">{e.is_maker != null ? (e.is_maker ? '✓' : '✗') : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
      }
      {total > 100 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-gray-700 flex-shrink-0 text-xs text-gray-400">
          <span>Всего: {total}</span>
          <div className="flex gap-1">
            <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">←</button>
            <span className="px-2 py-1">{page}</span>
            <button onClick={() => setPage(p => p + 1)} disabled={page * 100 >= total} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">→</button>
          </div>
        </div>
      )}
    </div>
  )
}
