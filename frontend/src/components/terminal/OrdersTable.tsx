import type { ActiveOrder } from '../../types'
import { cancelOrder } from '../../api/trader'
import { useState } from 'react'

interface Props {
  accountId: string
  orders: ActiveOrder[]
  loading: boolean
  onSelect?: (symbol: string) => void
  onRemoveOrder?: (orderId: string) => void
}

function orderLabel(ord: ActiveOrder): string {
  const id = ord.orderLinkId ?? ''
  const type = ord.orderType?.toLowerCase() ?? ''
  if (/^(SIS_STR|STP)-[a-f0-9]+-tp(-\d+)+$/.test(id))
    return ord.side === 'Sell' ? 'TP_LONG' : ord.side === 'Buy' ? 'TP_SHORT' : 'TP'
  if (/^(SIS_STR|STP)-[a-f0-9]+-sl(-\d+)+$/.test(id))
    return ord.side === 'Sell' ? 'SL_LONG' : ord.side === 'Buy' ? 'SL_SHORT' : 'SL'
  const lvl = id.match(/^(SIS_STR|STR)-[a-f0-9]+-\d+-(\d+)$/)
  if (lvl) return `L${lvl[2]}_${type}`
  return type || '—'
}

function orderSource(linkId: string): 'str' | 'trm' | null {
  if (linkId.startsWith('SIS_STR-') || linkId.startsWith('STR-') || linkId.startsWith('STP-')) return 'str'
  if (linkId.startsWith('SIS_TRM-') || linkId.startsWith('SiS-')) return 'trm'
  return null
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function OrdersTable({ accountId, orders, loading, onSelect, onRemoveOrder }: Props) {
  const [cancelling, setCancelling] = useState<string | null>(null)

  async function handleCancel(ord: ActiveOrder) {
    if (cancelling === ord.orderId) return
    setCancelling(ord.orderId)
    try {
      await cancelOrder({ account_id: accountId, symbol: ord.symbol, category: ord.category, order_id: ord.orderId, order_filter: ord.orderFilter })
      onRemoveOrder?.(ord.orderId)
    } catch (e: any) {
      // 110001 = order not exists on exchange — remove from state regardless
      if (String(e?.response?.data?.message ?? e?.message ?? '').includes('110001')) {
        onRemoveOrder?.(ord.orderId)
      }
    }
    setCancelling(null)
  }

  const visible = orders

  if (loading) return <div className="flex items-center justify-center h-full text-gray-500 dark:text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!visible.length) return <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">Активных ордеров нет</p></div>

  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
        <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
          <th className="text-left px-3 py-2">Символ</th>
          <th className="text-left px-3 py-2">Сторона</th>
          <th className="text-left px-3 py-2">Тип</th>
          <th className="text-right px-3 py-2">Цена</th>
          <th className="text-right px-3 py-2">Кол-во</th>
          <th className="text-left px-3 py-2">Размер</th>
          <th className="text-right px-3 py-2">Исполнено</th>
          <th className="text-left px-3 py-2">Статус</th>
          <th className="text-left px-3 py-2">Маркер</th>
          <th className="px-3 py-2" />
        </tr>
      </thead>
      <tbody>
        {visible.map(ord => (
          <tr key={ord.orderId} onClick={() => onSelect?.(ord.symbol)} className={`border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-100 dark:hover:bg-gray-800/60 ${onSelect ? 'cursor-pointer' : ''}`}>
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
            <td className="px-3 py-2 text-gray-500 dark:text-gray-400 font-mono">{orderLabel(ord)}</td>
            <td className="px-3 py-2 text-right font-mono">
              {ord.triggerPrice ? `~${parseFloat(ord.triggerPrice).toFixed(2)}` : parseFloat(ord.price) > 0 ? parseFloat(ord.price).toFixed(2) : '—'}
            </td>
            <td className="px-3 py-2 text-right font-mono">{ord.qty}</td>
            <td className="px-3 py-2 text-left font-mono">
              {(() => {
                const p = ord.triggerPrice && parseFloat(ord.triggerPrice) > 0 ? parseFloat(ord.triggerPrice) : parseFloat(ord.price)
                const usdt = p > 0 ? (parseFloat(ord.qty) * p).toFixed(2) : null
                return usdt
                  ? <>{usdt} <span className="text-gray-500 dark:text-gray-400 text-[10px]">USDT</span></>
                  : <span className="text-gray-500 dark:text-gray-400">—</span>
              })()}
            </td>
            <td className="px-3 py-2 text-right font-mono text-gray-500 dark:text-gray-400">{ord.cumExecQty}</td>
            <td className="px-3 py-2">
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-yellow-500/20 text-yellow-400">{ord.orderStatus}</span>
            </td>
            <td className="px-3 py-2">
              {orderSource(ord.orderLinkId ?? '') === 'str' && (
                <span className="text-[10px] px-1 py-0.5 rounded bg-purple-500/20 text-purple-400">STR</span>
              )}
              {orderSource(ord.orderLinkId ?? '') === 'trm' && (
                <span className="text-[10px] px-1 py-0.5 rounded bg-blue-500/20 text-blue-400">TRM</span>
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
