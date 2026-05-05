import { useState } from 'react'
import type { ActiveOrder } from '../../types'
import { cancelOrder } from '../../api/trader'

interface Props {
  accountId: string
  orders: ActiveOrder[]
  loading: boolean
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function OrdersTable({ accountId, orders, loading }: Props) {
  const [cancelling, setCancelling] = useState<string | null>(null)

  async function handleCancel(ord: ActiveOrder) {
    if (cancelling === ord.orderId) return
    setCancelling(ord.orderId)
    await cancelOrder({ account_id: accountId, symbol: ord.symbol, category: ord.category, order_id: ord.orderId, order_filter: ord.orderFilter }).catch(() => {})
    setCancelling(null)
  }

  if (loading) return <div className="flex items-center justify-center h-full text-gray-500 dark:text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!orders.length) return <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">Активных ордеров нет</p></div>

  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
        <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
          <th className="text-left px-3 py-2">Символ</th>
          <th className="text-left px-3 py-2">Сторона</th>
          <th className="text-left px-3 py-2">Тип</th>
          <th className="text-right px-3 py-2">Цена</th>
          <th className="text-right px-3 py-2">Кол-во</th>
          <th className="text-right px-3 py-2">Исполнено</th>
          <th className="text-left px-3 py-2">Статус</th>
          <th className="text-left px-3 py-2">Маркер</th>
          <th className="px-3 py-2" />
        </tr>
      </thead>
      <tbody>
        {orders.map(ord => (
          <tr key={ord.orderId} className="border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-100 dark:hover:bg-gray-800/60">
            <td className="px-3 py-2 font-mono font-medium">
              <div className="flex items-center gap-1.5">
                <img src={coinIcon(ord.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display='none')} />
                {ord.symbol}
              </div>
            </td>
            <td className="px-3 py-2">
              <span className={`text-[10px] px-1.5 py-0.5 rounded ${ord.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                {ord.side}
              </span>
            </td>
            <td className="px-3 py-2 text-gray-500 dark:text-gray-400">{ord.orderType}</td>
            <td className="px-3 py-2 text-right font-mono">
              {ord.triggerPrice ? `~${parseFloat(ord.triggerPrice).toFixed(2)}` : parseFloat(ord.price) > 0 ? parseFloat(ord.price).toFixed(2) : '—'}
            </td>
            <td className="px-3 py-2 text-right font-mono">{ord.qty}</td>
            <td className="px-3 py-2 text-right font-mono text-gray-500 dark:text-gray-400">{ord.cumExecQty}</td>
            <td className="px-3 py-2">
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-yellow-500/20 text-yellow-400">{ord.orderStatus}</span>
            </td>
            <td className="px-3 py-2">
              {ord.orderLinkId?.startsWith('sis') && (
                <span className="text-[10px] px-1 py-0.5 rounded bg-blue-500/20 text-blue-400">SIS</span>
              )}
            </td>
            <td className="px-3 py-2 text-right">
              <button onClick={() => handleCancel(ord)} disabled={cancelling === ord.orderId}
                className="text-gray-500 dark:text-gray-400 hover:text-red-400 transition-colors disabled:opacity-50">✕</button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
