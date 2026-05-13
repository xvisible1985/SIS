import type { Position } from '../../types'

export const TAKER_FEE = 0.00055 // Bybit linear taker fee

export interface CloseConfirm {
  pos: Position
  accountId: string
  fee: number
  netPnl: number
}

export function makeCloseConfirm(pos: Position, accountId: string): CloseConfirm {
  const fee = pos.sizeUsdt * TAKER_FEE
  const netPnl = parseFloat(pos.unrealisedPnl) - fee
  return { pos, accountId, fee, netPnl }
}

export function ClosePositionModal({ confirm, onConfirm, onCancel, closing }: {
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
