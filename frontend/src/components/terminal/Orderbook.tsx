import type { BookRow } from '../../hooks/terminal/useOrderbook'

interface Props {
  bids: BookRow[]
  asks: BookRow[]
  spread: string | null
  symbol: string
}

export function Orderbook({ bids, asks, spread, symbol }: Props) {
  const maxBid = Math.max(...bids.map(([, s]) => parseFloat(s)), 1)
  const maxAsk = Math.max(...asks.map(([, s]) => parseFloat(s)), 1)

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-gray-700 flex-shrink-0">
        <span className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Стакан</span>
        <span className="text-xs text-gray-400 font-mono">{symbol}</span>
        {spread && (
          <span className="text-xs font-mono text-gray-400">
            Спред: <span className="text-white">{spread}</span>
          </span>
        )}
      </div>
      <div className="flex flex-1 overflow-hidden min-h-0">
        <div className="flex-1 flex flex-col overflow-hidden border-r border-gray-700/30">
          <div className="flex justify-between px-2 py-0.5 border-b border-gray-700/20 flex-shrink-0">
            <span className="text-[9px] font-medium text-green-500 uppercase">Bid</span>
            <span className="text-[9px] text-gray-400">Объём</span>
          </div>
          <div className="flex-1 overflow-auto">
            {bids.map(([price, size]) => (
              <div key={price} className="relative flex justify-between px-2 py-[2px] hover:bg-green-500/5">
                <div
                  className="absolute inset-y-0 right-0 bg-green-500/10"
                  style={{ width: `${(parseFloat(size) / maxBid * 100).toFixed(1)}%` }}
                />
                <span className="relative text-[10px] font-mono text-green-400 z-10">{parseFloat(price).toFixed(2)}</span>
                <span className="relative text-[10px] font-mono text-gray-400 z-10">{parseFloat(size).toFixed(3)}</span>
              </div>
            ))}
            {!bids.length && <div className="text-center text-gray-500 text-[10px] py-4">Загрузка...</div>}
          </div>
        </div>
        <div className="flex-1 flex flex-col overflow-hidden">
          <div className="flex justify-between px-2 py-0.5 border-b border-gray-700/20 flex-shrink-0">
            <span className="text-[9px] font-medium text-red-500 uppercase">Ask</span>
            <span className="text-[9px] text-gray-400">Объём</span>
          </div>
          <div className="flex-1 overflow-auto">
            {asks.map(([price, size]) => (
              <div key={price} className="relative flex justify-between px-2 py-[2px] hover:bg-red-500/5">
                <div
                  className="absolute inset-y-0 left-0 bg-red-500/10"
                  style={{ width: `${(parseFloat(size) / maxAsk * 100).toFixed(1)}%` }}
                />
                <span className="relative text-[10px] font-mono text-red-400 z-10">{parseFloat(price).toFixed(2)}</span>
                <span className="relative text-[10px] font-mono text-gray-400 z-10">{parseFloat(size).toFixed(3)}</span>
              </div>
            ))}
            {!asks.length && <div className="text-center text-gray-500 text-[10px] py-4">Загрузка...</div>}
          </div>
        </div>
      </div>
    </div>
  )
}
