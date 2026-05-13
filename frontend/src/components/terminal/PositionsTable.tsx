import type { Position } from '../../types'
import { placeOrder } from '../../api/trader'
import { useState } from 'react'
import { ClosePositionModal, makeCloseConfirm, type CloseConfirm } from '../common/ClosePositionModal'

interface Props {
  accountId: string
  positions: Position[]
  onSelect: (symbol: string) => void
  loading: boolean
  tickerPrices?: Map<string, number>
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function PositionsTable({ accountId, positions, onSelect, loading, tickerPrices }: Props) {
  const [closing, setClosing] = useState(false)
  const [confirm, setConfirm] = useState<CloseConfirm | null>(null)

  function handleCloseClick(pos: Position) {
    setConfirm(makeCloseConfirm(pos, accountId))
  }

  async function handleConfirm() {
    if (!confirm) return
    setClosing(true)
    try {
      await placeOrder({
        account_id: confirm.accountId,
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
        <ClosePositionModal
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
              <th key={h} className="px-3 py-2 font-medium text-left">{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {positions.map(pos => {
            const entry = parseFloat(pos.entryPrice)
            const size = parseFloat(pos.size)
            const liveMarkPrice = tickerPrices?.get(pos.symbol)
            const mark = liveMarkPrice ?? parseFloat(pos.markPrice)
            const pnl = liveMarkPrice != null
              ? (pos.side === 'Buy' ? (mark - entry) : (entry - mark)) * size
              : parseFloat(pos.unrealisedPnl)
            const pnlPct = entry > 0 && size > 0 ? (pnl / (size * entry)) * 100 : 0
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
                <td className="px-3 py-2 text-left font-mono">{pos.sizeUsdt.toFixed(2)} <span className="text-gray-500 dark:text-gray-400 text-[10px]">USDT</span></td>
                <td className="px-3 py-2 text-left font-mono">{entry.toFixed(2)}</td>
                <td className="px-3 py-2 text-left font-mono">{mark.toFixed(2)}</td>
                <td className={`px-3 py-2 text-left font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                  {pnl >= 0 ? '+' : ''}{pnl.toFixed(4)} <span className="text-gray-500 dark:text-gray-400 font-normal">({pnlPct.toFixed(2)}%)</span>
                </td>
                <td className="px-3 py-2 text-left font-mono">{pos.leverage}x</td>
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
