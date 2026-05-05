import { useEffect, useState } from 'react'
import { listOrders } from '../../api/trader'
import type { TraderOrder } from '../../types'

interface Props { accountId?: string; symbol?: string }

export function HistoryTable({ accountId, symbol }: Props) {
  const [orders, setOrders] = useState<TraderOrder[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listOrders({ account_id: accountId, status: 'closed', symbol, page, limit: 50 })
      .then(r => { setOrders(r.orders); setTotal(r.total) })
      .finally(() => setLoading(false))
  }, [accountId, symbol, page])

  if (loading) return <div className="flex items-center justify-center h-full text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!orders.length) return <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">История пуста</p></div>

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-auto">
        <table className="w-full text-xs">
          <thead className="sticky top-0 z-10 bg-gray-900">
            <tr className="text-gray-400 border-b border-gray-700">
              <th className="text-left px-3 py-2">Дата</th>
              <th className="text-left px-3 py-2">Символ</th>
              <th className="text-left px-3 py-2">Сторона</th>
              <th className="text-left px-3 py-2">Тип</th>
              <th className="text-right px-3 py-2">Цена</th>
              <th className="text-right px-3 py-2">Кол-во</th>
              <th className="text-right px-3 py-2">Комиссия</th>
              <th className="text-left px-3 py-2">Статус</th>
              <th className="px-3 py-2">Маркер</th>
            </tr>
          </thead>
          <tbody>
            {orders.map(o => (
              <tr key={o.id} className="border-b border-gray-700/50 hover:bg-gray-800/60">
                <td className="px-3 py-2 font-mono text-gray-400 whitespace-nowrap">{new Date(o.created_at).toLocaleString('ru-RU')}</td>
                <td className="px-3 py-2 font-mono font-medium">{o.symbol}</td>
                <td className="px-3 py-2">
                  <span className={`text-[10px] px-1.5 py-0.5 rounded ${o.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>{o.side}</span>
                </td>
                <td className="px-3 py-2 text-gray-400">{o.order_type}</td>
                <td className="px-3 py-2 text-right font-mono">{o.price ? parseFloat(o.price).toFixed(2) : '—'}</td>
                <td className="px-3 py-2 text-right font-mono">{o.cum_exec_qty}</td>
                <td className="px-3 py-2 text-right font-mono text-gray-400">{o.cum_exec_fee}</td>
                <td className="px-3 py-2">
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">{o.status}</span>
                </td>
                <td className="px-3 py-2">
                  {o.order_link_id?.startsWith('sis') && <span className="text-[10px] px-1 py-0.5 rounded bg-blue-500/20 text-blue-400">SIS</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {total > 50 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-gray-700 flex-shrink-0 text-xs text-gray-400">
          <span>Всего: {total}</span>
          <div className="flex gap-1">
            <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">←</button>
            <span className="px-2 py-1">{page}</span>
            <button onClick={() => setPage(p => p + 1)} disabled={page * 50 >= total} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">→</button>
          </div>
        </div>
      )}
    </div>
  )
}
