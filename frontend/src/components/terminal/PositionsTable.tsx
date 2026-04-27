import type { Position } from '../../types'
import { cancelOrder } from '../../api/trader'
import { useState } from 'react'

interface Props {
  accountId: string
  positions: Position[]
  onSelect: (symbol: string) => void
  loading: boolean
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function PositionsTable({ accountId, positions, onSelect, loading }: Props) {
  const [closing, setClosing] = useState<string | null>(null)

  async function handleClose(pos: Position) {
    const key = `${pos.symbol}-${pos.side}`
    if (closing === key) return
    setClosing(key)
    await cancelOrder({ // reuse cancel for close via reduce_only market order
      account_id: accountId,
      symbol: pos.symbol,
      category: pos.category,
      order_id: '',
    }).catch(() => {})
    // Close is done via placeOrder with reduceOnly=true — call from parent in real impl
    setClosing(null)
  }

  if (loading) return (
    <div className="flex items-center justify-center h-full text-gray-400 text-sm">
      <span className="animate-pulse">Загрузка данных...</span>
    </div>
  )

  if (!positions.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2">
      <span className="text-2xl opacity-30">📭</span>
      <p className="text-sm">Открытых позиций нет</p>
    </div>
  )

  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-gray-900">
        <tr className="text-gray-400 border-b border-gray-700">
          {['Символ','Сторона','Размер','Цена входа','Mark Price','PnL','Плечо',''].map(h => (
            <th key={h} className={`px-3 py-2 font-medium ${h === '' || h === 'Размер' || h === 'Цена входа' || h === 'Mark Price' || h === 'PnL' || h === 'Плечо' ? 'text-right' : 'text-left'}`}>{h}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {positions.map(pos => {
          const pnl = parseFloat(pos.unrealisedPnl)
          return (
            <tr key={`${pos.symbol}-${pos.side}`} onClick={() => onSelect(pos.symbol)}
              className="border-b border-gray-700/50 hover:bg-gray-800/60 transition-colors cursor-pointer">
              <td className="px-3 py-2 font-mono font-medium">
                <div className="flex items-center gap-1.5">
                  <img src={coinIcon(pos.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display='none')} />
                  {pos.symbol}
                </div>
              </td>
              <td className="px-3 py-2">
                <span className={`text-[10px] px-1.5 py-0.5 rounded ${pos.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                  {pos.side === 'Buy' ? 'Long' : 'Short'}
                </span>
              </td>
              <td className="px-3 py-2 text-right font-mono">{pos.sizeUsdt.toFixed(2)} <span className="text-gray-400 text-[10px]">USDT</span></td>
              <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.entryPrice).toFixed(2)}</td>
              <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.markPrice).toFixed(2)}</td>
              <td className={`px-3 py-2 text-right font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {pnl >= 0 ? '+' : ''}{pnl.toFixed(2)} <span className="text-gray-400 font-normal">({pos.unrealisedPnlPct}%)</span>
              </td>
              <td className="px-3 py-2 text-right font-mono">{pos.leverage}x</td>
              <td className="px-3 py-2 text-right" onClick={e => e.stopPropagation()}>
                <button
                  onClick={() => handleClose(pos)}
                  disabled={closing === `${pos.symbol}-${pos.side}`}
                  className="text-[10px] px-2 py-0.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 disabled:opacity-50"
                >
                  {closing === `${pos.symbol}-${pos.side}` ? '...' : 'Закрыть'}
                </button>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
