import type { Position } from '../../types'
import { placeOrder } from '../../api/trader'
import { useState } from 'react'

const TAKER_FEE = 0.00055 // Bybit linear taker fee

interface Props {
  accountId: string
  positions: Position[]
  onSelect: (symbol: string) => void
  loading: boolean
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

interface CloseConfirm {
  pos: Position
  fee: number
  netPnl: number
}

function CloseModal({ confirm, onConfirm, onCancel, closing }: {
  confirm: CloseConfirm
  onConfirm: () => void
  onCancel: () => void
  closing: boolean
}) {
  const { pos, fee, netPnl } = confirm
  const pnl = parseFloat(pos.unrealisedPnl)
  const isLong = pos.side === 'Buy'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onCancel}>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl shadow-xl w-80 p-5" onClick={e => e.stopPropagation()}>
        <h3 className="text-sm font-semibold text-gray-900 dark:text-white mb-4">Закрыть позицию</h3>

        <div className="space-y-2 text-xs mb-4">
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Инструмент</span>
            <span className="font-mono font-medium text-gray-900 dark:text-white">{pos.symbol}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Сторона</span>
            <span className={`font-medium ${isLong ? 'text-green-500' : 'text-red-500'}`}>{isLong ? 'Long' : 'Short'}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Размер</span>
            <span className="font-mono text-gray-900 dark:text-white">{pos.size} ({pos.sizeUsdt.toFixed(2)} USDT)</span>
          </div>
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Цена входа</span>
            <span className="font-mono text-gray-900 dark:text-white">{parseFloat(pos.entryPrice).toFixed(4)}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-gray-500 dark:text-gray-400">Mark Price</span>
            <span className="font-mono text-gray-900 dark:text-white">{parseFloat(pos.markPrice).toFixed(4)}</span>
          </div>

          <div className="border-t border-gray-200 dark:border-gray-700 pt-2 mt-2 space-y-2">
            <div className="flex justify-between">
              <span className="text-gray-500 dark:text-gray-400">Нереализованный PnL</span>
              <span className={`font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {pnl >= 0 ? '+' : ''}{pnl.toFixed(4)} USDT
              </span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500 dark:text-gray-400">Комиссия закрытия (0.055%)</span>
              <span className="font-mono text-red-400">−{fee.toFixed(4)} USDT</span>
            </div>
            <div className="flex justify-between border-t border-gray-200 dark:border-gray-700 pt-2">
              <span className="text-gray-600 dark:text-gray-300 font-medium">Итого</span>
              <span className={`font-mono font-bold text-sm ${netPnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {netPnl >= 0 ? '+' : ''}{netPnl.toFixed(4)} USDT
              </span>
            </div>
          </div>
        </div>

        <div className="flex gap-2">
          <button onClick={onCancel} className="flex-1 px-3 py-2 text-xs border border-gray-300 dark:border-gray-600 rounded-lg text-gray-700 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors">
            Отмена
          </button>
          <button onClick={onConfirm} disabled={closing}
            className="flex-1 px-3 py-2 text-xs bg-red-600 text-white rounded-lg hover:bg-red-700 disabled:opacity-50 transition-colors font-medium">
            {closing ? 'Закрытие...' : 'Закрыть'}
          </button>
        </div>
      </div>
    </div>
  )
}

export function PositionsTable({ accountId, positions, onSelect, loading }: Props) {
  const [closing, setClosing] = useState(false)
  const [confirm, setConfirm] = useState<CloseConfirm | null>(null)

  function handleCloseClick(pos: Position) {
    const fee = pos.sizeUsdt * TAKER_FEE
    const netPnl = parseFloat(pos.unrealisedPnl) - fee
    setConfirm({ pos, fee, netPnl })
  }

  async function handleConfirm() {
    if (!confirm) return
    setClosing(true)
    try {
      await placeOrder({
        account_id: accountId,
        symbol: confirm.pos.symbol,
        category: confirm.pos.category,
        side: confirm.pos.side === 'Buy' ? 'Sell' : 'Buy',
        order_type: 'Market',
        qty: confirm.pos.size,
        reduce_only: true,
        position_idx: confirm.pos.positionIdx,
      })
    } catch { /* WS обновит позицию */ }
    setClosing(false)
    setConfirm(null)
  }

  if (loading) return (
    <div className="flex items-center justify-center h-full text-gray-500 dark:text-gray-400 text-sm">
      <span className="animate-pulse">Загрузка данных...</span>
    </div>
  )

  if (!positions.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-500 dark:text-gray-400 gap-2">
      <span className="text-2xl opacity-30">📭</span>
      <p className="text-sm">Открытых позиций нет</p>
    </div>
  )

  return (
    <>
      {confirm && (
        <CloseModal
          confirm={confirm}
          onConfirm={handleConfirm}
          onCancel={() => setConfirm(null)}
          closing={closing}
        />
      )}
      <table className="w-full text-xs">
        <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
          <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
            {['Символ', 'Сторона', 'Размер', 'Цена входа', 'Mark Price', 'PnL', 'Плечо', ''].map(h => (
              <th key={h} className={`px-3 py-2 font-medium ${h === '' || h === 'Размер' || h === 'Цена входа' || h === 'Mark Price' || h === 'PnL' || h === 'Плечо' ? 'text-right' : 'text-left'}`}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {positions.map(pos => {
            const pnl = parseFloat(pos.unrealisedPnl)
            return (
              <tr key={`${pos.symbol}-${pos.side}`} onClick={() => onSelect(pos.symbol)}
                className="border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-100 dark:hover:bg-gray-800/60 transition-colors cursor-pointer">
                <td className="px-3 py-2 font-mono font-medium">
                  <div className="flex items-center gap-1.5">
                    <img src={coinIcon(pos.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display = 'none')} />
                    {pos.symbol}
                  </div>
                </td>
                <td className="px-3 py-2">
                  <span className={`text-[10px] px-1.5 py-0.5 rounded ${pos.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                    {pos.side === 'Buy' ? 'Long' : 'Short'}
                  </span>
                </td>
                <td className="px-3 py-2 text-right font-mono">{pos.sizeUsdt.toFixed(2)} <span className="text-gray-500 dark:text-gray-400 text-[10px]">USDT</span></td>
                <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.entryPrice).toFixed(2)}</td>
                <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.markPrice).toFixed(2)}</td>
                <td className={`px-3 py-2 text-right font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                  {pnl >= 0 ? '+' : ''}{pnl.toFixed(2)} <span className="text-gray-500 dark:text-gray-400 font-normal">({pos.unrealisedPnlPct}%)</span>
                </td>
                <td className="px-3 py-2 text-right font-mono">{pos.leverage}x</td>
                <td className="px-3 py-2 text-right" onClick={e => e.stopPropagation()}>
                  <button
                    onClick={() => handleCloseClick(pos)}
                    className="text-[10px] px-2 py-0.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 transition-colors"
                  >
                    Закрыть
                  </button>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </>
  )
}
