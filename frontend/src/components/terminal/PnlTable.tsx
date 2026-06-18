import { useEffect, useState, useCallback } from 'react'
import { getClosedPnl, type ClosedPnlItem } from '../../api/trader'

interface Props {
  accountId?: string
}

const CATEGORIES = ['linear', 'inverse', 'spot']

export function PnlTable({ accountId }: Props) {
  const [list, setList] = useState<ClosedPnlItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [category, setCategory] = useState('linear')
  const [nextCursor, setNextCursor] = useState('')

  const load = useCallback(async (cat: string, cur: string) => {
    if (!accountId) return
    setLoading(true)
    setError(null)
    try {
      const res = await getClosedPnl({ account_id: accountId, category: cat, cursor: cur })
      setList(prev => cur ? [...prev, ...res.list] : res.list)
      setNextCursor(res.nextCursor ?? '')
    } catch (e: any) {
      setError(e?.response?.data?.error ?? 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [accountId])

  useEffect(() => {
    setList([])
    setNextCursor('')
    load(category, '')
  }, [category, load])

  function handleMore() {
    load(category, nextCursor)
  }

  function formatTime(ms: string) {
    const n = parseInt(ms)
    if (!n) return '—'
    return new Date(n).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
  }

  function calcFee(item: ClosedPnlItem) {
    // item.side = closing order side: Buy = Short position closed, Sell = Long position closed
    const gross = item.side === 'Buy'
      ? (parseFloat(item.avgEntryPrice) - parseFloat(item.avgExitPrice)) * parseFloat(item.qty)  // Short: profit when entry > exit
      : (parseFloat(item.avgExitPrice) - parseFloat(item.avgEntryPrice)) * parseFloat(item.qty)  // Long:  profit when exit > entry
    return gross - parseFloat(item.closedPnl)
  }

  return (
    <div className="flex flex-col h-full">
      {/* Фильтр категории */}
      <div className="flex items-center gap-1 px-3 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
        {CATEGORIES.map(c => (
          <button key={c} onClick={() => setCategory(c)}
            className={`px-2.5 py-1 text-xs rounded font-medium transition-colors ${category === c ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
            {c}
          </button>
        ))}
      </div>

      {/* Таблица */}
      <div className="flex-1 overflow-auto">
        {loading && !list.length ? (
          <div className="flex items-center justify-center h-full text-gray-400 text-sm animate-pulse">Загрузка...</div>
        ) : error ? (
          <div className="flex items-center justify-center h-full text-red-500 text-sm">{error}</div>
        ) : !list.length ? (
          <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2">
            <span className="text-2xl opacity-30">📊</span>
            <p className="text-sm">Закрытых позиций нет</p>
          </div>
        ) : (
          <>
            <table className="w-full text-xs">
              <thead className="sticky top-0 z-10 bg-white dark:bg-gray-900">
                <tr className="text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
                  {['Время', 'Инструмент', 'Направление', 'Объём', 'Цена входа', 'Цена выхода'].map(h => (
                    <th key={h} className={`px-3 py-2 font-medium ${h === 'Время' || h === 'Инструмент' || h === 'Направление' ? 'text-left' : 'text-right'}`}>{h}</th>
                  ))}
                  <th className="px-3 py-2 font-medium text-right cursor-help" title="Торговые комиссии + фандинг за период удержания (= валовый P&L − чистый closedPnl)">Комиссии</th>
                  <th className="px-3 py-2 font-medium text-right">P&L</th>
                </tr>
              </thead>
              <tbody>
                {list.map((item, i) => {
                  const pnl = parseFloat(item.closedPnl)
                  // side = closing order side: Buy means a Short position was closed, Sell = Long was closed
                  const isLong = item.side === 'Sell'
                  const fee = calcFee(item)
                  return (
                    <tr key={i} className="border-b border-gray-200 dark:border-gray-700/50 hover:bg-gray-50 dark:hover:bg-gray-800/40">
                      <td className="px-3 py-2 text-gray-500 dark:text-gray-400 whitespace-nowrap">{formatTime(item.createdTime)}</td>
                      <td className="px-3 py-2 font-mono font-medium text-gray-900 dark:text-white">{item.symbol}</td>
                      <td className="px-3 py-2">
                        <span className={`text-[10px] px-1.5 py-0.5 rounded ${isLong ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                          {isLong ? 'Long' : 'Short'}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right font-mono text-gray-900 dark:text-white">{item.qty}</td>
                      <td className="px-3 py-2 text-right font-mono text-gray-900 dark:text-white">{parseFloat(item.avgEntryPrice).toFixed(4)}</td>
                      <td className="px-3 py-2 text-right font-mono text-gray-900 dark:text-white">{parseFloat(item.avgExitPrice).toFixed(4)}</td>
                      <td className="px-3 py-2 text-right font-mono text-white">−{Math.abs(fee).toFixed(4)}</td>
                      <td className={`px-3 py-2 text-right font-mono font-semibold ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                        {pnl >= 0 ? '+' : ''}{pnl.toFixed(4)}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>

            {nextCursor && (
              <div className="flex justify-center py-3">
                <button onClick={handleMore} disabled={loading}
                  className="px-4 py-1.5 text-xs border border-gray-300 dark:border-gray-600 rounded-lg text-gray-600 dark:text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50 transition-colors">
                  {loading ? 'Загрузка...' : 'Загрузить ещё'}
                </button>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
