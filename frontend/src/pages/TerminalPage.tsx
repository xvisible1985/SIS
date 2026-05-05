import { useState, useEffect } from 'react'
import { Chart } from '../components/terminal/Chart'
import { DepthChart } from '../components/terminal/DepthChart'
import { Orderbook } from '../components/terminal/Orderbook'
import { OrderForm } from '../components/terminal/OrderForm'
import { PositionsTable } from '../components/terminal/PositionsTable'
import { OrdersTable } from '../components/terminal/OrdersTable'
import { HistoryTable } from '../components/terminal/HistoryTable'
import { ExecutionsTable } from '../components/terminal/ExecutionsTable'
import { TradeLog } from '../components/terminal/TradeLog'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { listAccounts } from '../api/accounts'

const COINS = ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT', 'XRPUSDT', 'DOGEUSDT']
const TIMEFRAMES = [
  { label: '1м', value: '1' },
  { label: '5м', value: '5' },
  { label: '15м', value: '15' },
  { label: '1ч', value: '60' },
  { label: '4ч', value: '240' },
  { label: '1д', value: 'D' },
]

type BottomTab = 'positions' | 'orders' | 'history' | 'executions' | 'log'
type ChartView = 'candles' | 'depth'

export function TerminalPage() {
  const [accountId, setAccountId] = useState<string | null>(null)
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [tf, setTf] = useState('60')
  const [bottomTab, setBottomTab] = useState<BottomTab>('positions')
  const [chartView, setChartView] = useState<ChartView>('candles')

  useEffect(() => {
    listAccounts().then(accs => {
      if (accs.length > 0 && accs[0]) setAccountId(accs[0].id)
    }).catch(() => {})
  }, [])

  const { positions, orders, log, status, accountName, loading, reconnect } = usePositionsWs(accountId)
  const { candles, lastPrice, priceChange } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  const statusColor = {
    connecting: 'bg-yellow-400 animate-pulse',
    connected: 'bg-green-500',
    error: 'bg-red-500',
    closed: 'bg-red-500',
  }[status]

  const bottomTabs: { key: BottomTab; label: string; count?: number }[] = [
    { key: 'positions', label: 'Позиции', count: positions.length },
    { key: 'orders', label: 'Ордера', count: orders.length },
    { key: 'history', label: 'История' },
    { key: 'executions', label: 'Сделки' },
    { key: 'log', label: 'Лог' },
  ]

  return (
    <div className="bg-gray-100 dark:bg-[#07070f]" style={{ display: 'grid', gridTemplateColumns: '65% 35%', gridTemplateRows: '65% 35%', height: 'calc(100vh - 65px)', gap: '10px', padding: '10px' }}>

      {/* Chart area */}
      <div style={{ gridColumn: 1, gridRow: 1 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        {/* Toolbar */}
        <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
          <select value={symbol} onChange={e => setSymbol(e.target.value)}
            className="bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 text-sm text-gray-900 dark:text-white w-36 focus:outline-none">
            {COINS.map(c => <option key={c} value={c}>{c}</option>)}
          </select>

          {/* Chart view toggle */}
          <div className="flex rounded overflow-hidden border border-gray-200 dark:border-gray-600">
            <button onClick={() => setChartView('candles')}
              className={`px-2 py-0.5 text-xs font-medium transition-colors ${chartView === 'candles' ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
              Свечи
            </button>
            <button onClick={() => setChartView('depth')}
              className={`px-2 py-0.5 text-xs font-medium transition-colors ${chartView === 'depth' ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
              Глубина
            </button>
          </div>

          {chartView === 'candles' && (
            <div className="flex gap-1">
              {TIMEFRAMES.map(t => (
                <button key={t.value} onClick={() => setTf(t.value)}
                  className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${tf === t.value ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                  {t.label}
                </button>
              ))}
            </div>
          )}

          {lastPrice && (
            <div className="ml-auto flex items-center gap-2">
              <span className="font-mono font-bold text-lg text-gray-900 dark:text-white">{lastPrice}</span>
              <span className={`text-sm font-medium ${priceChange >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {priceChange >= 0 ? '+' : ''}{priceChange.toFixed(2)}%
              </span>
            </div>
          )}
        </div>
        <div className="flex-1 min-h-0">
          {chartView === 'candles'
            ? <Chart candles={candles} positions={positions} orders={orders} symbol={symbol} />
            : <DepthChart bids={bids} asks={asks} />
          }
        </div>
      </div>

      {/* Bottom: positions/orders/history */}
      <div style={{ gridColumn: 1, gridRow: 2 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        <div className="flex items-center border-b border-gray-200 dark:border-gray-700 flex-shrink-0 px-2">
          {bottomTabs.map(t => (
            <button key={t.key} onClick={() => setBottomTab(t.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${bottomTab === t.key ? 'border-blue-500 text-white' : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
              {t.label}
              {t.count != null && t.count > 0 && (
                loading && (t.key === 'positions' || t.key === 'orders')
                  ? <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
                  : <span className="bg-gray-200 dark:bg-gray-700 text-xs px-1.5 py-0.5 rounded-full">{t.count}</span>
              )}
            </button>
          ))}
          {accountName && <span className="text-xs text-gray-500 dark:text-gray-400 ml-2">{accountName}</span>}
          <div className="ml-auto flex items-center gap-1.5">
            <span className={`w-2 h-2 rounded-full ${statusColor}`} />
            {(status === 'closed' || status === 'error') && (
              <button onClick={reconnect} className="text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white text-xs">↺</button>
            )}
          </div>
        </div>
        <div className="flex-1 overflow-auto">
          {bottomTab === 'positions' && <PositionsTable accountId={accountId ?? ''} positions={positions} onSelect={setSymbol} loading={loading} />}
          {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} />}
          {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
          {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
          {bottomTab === 'log' && <TradeLog log={log} />}
        </div>
      </div>

      {/* Right panel: OrderForm + Orderbook */}
      <div style={{ gridColumn: 2, gridRow: '1 / 3', display: 'flex', flexDirection: 'column', gap: '10px', overflow: 'hidden' }}>
        <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl overflow-hidden flex-shrink-0" style={{ maxHeight: '55%' }}>
          {accountId ? (
            <OrderForm accountId={accountId} symbol={symbol} lastPrice={lastPrice} orders={orders} positions={positions} />
          ) : (
            <div className="flex flex-col items-center justify-center h-full p-6 text-center text-gray-500 dark:text-gray-400 gap-3">
              <span className="text-3xl opacity-30">🔑</span>
              <p className="text-sm">Нет аккаунта Bybit. Добавьте API-ключи в настройках.</p>
            </div>
          )}
        </div>
        <div className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex-1 flex flex-col overflow-hidden min-h-0">
          <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
        </div>
      </div>

    </div>
  )
}
