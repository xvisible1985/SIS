import { useState, useEffect } from 'react'
import { Chart } from '../components/terminal/Chart'
import { Orderbook } from '../components/terminal/Orderbook'
import { OrderForm } from '../components/terminal/OrderForm'
import { PositionsTable } from '../components/terminal/PositionsTable'
import { OrdersTable } from '../components/terminal/OrdersTable'
import { HistoryTable } from '../components/terminal/HistoryTable'
import { ExecutionsTable } from '../components/terminal/ExecutionsTable'
import { TradeLog } from '../components/terminal/TradeLog'
import { PnlTable } from '../components/terminal/PnlTable'
import { GridForm } from '../components/terminal/GridForm'
import { CoinPicker } from '../components/common/CoinPicker'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { listAccounts } from '../api/accounts'
import { listStrategies } from '../api/strategies'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import type { Strategy, ExchangeAccount, ActiveOrder, Position } from '../types'

let _cachedSymbol = 'BTCUSDT'
let _cachedTf = '60'

const TIMEFRAMES = [
  { label: '1м', value: '1' },
  { label: '5м', value: '5' },
  { label: '15м', value: '15' },
  { label: '1ч', value: '60' },
  { label: '4ч', value: '240' },
  { label: '1д', value: 'D' },
]

type BottomTab = 'positions' | 'orders' | 'history' | 'executions' | 'log' | 'pnl'
type RightTab = 'manual' | 'strategies' | 'bots'

// ── Strategies tab ───────────────────────────────────────────────────────────
function TerminalStrategiesTab({ onSymbolChange, orders, positions }: { onSymbolChange: (sym: string) => void; orders: ActiveOrder[]; positions: Position[] }) {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [modalOpen, setModalOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    try {
      const [strats, accs] = await Promise.all([listStrategies(), listAccounts()])
      setStrategies(strats)
      setAccounts(accs)
    } catch {
      setStrategies([])
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  function handleSelect(s: Strategy) {
    setSelectedId(s.id)
    onSymbolChange(s.symbol)
  }

  return (
    <div className="flex flex-col h-full">
      <div className="px-3 py-2 flex items-center justify-between border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
        <span className="text-xs text-gray-500 dark:text-gray-400">{strategies.length} стратегий</span>
        <button
          onClick={() => { setEditTarget(undefined); setModalOpen(true) }}
          className="text-xs px-2.5 py-1 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
        >+ Новая</button>
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-2">
        {loading && strategies.length === 0 && <div className="p-8 text-center text-sm text-gray-400">Загрузка…</div>}
        {!loading && strategies.length === 0 && (
          <div className="p-8 text-center text-sm text-gray-400">Нет стратегий</div>
        )}
        {strategies.map(s => (
          <StrategyCard
            key={s.id}
            strategy={s}
            accounts={accounts}
            orders={orders}
            positions={positions}
            onEdit={s => { setEditTarget(s); setModalOpen(true) }}
            onChanged={load}
            selected={s.id === selectedId}
            onSelect={handleSelect}
            isOpen={s.id === expandedId}
            onToggleOpen={() => setExpandedId(prev => prev === s.id ? null : s.id)}
          />
        ))}
      </div>
      {modalOpen && (
        <StrategyModal
          strategy={editTarget}
          onClose={() => setModalOpen(false)}
          onSaved={() => { setModalOpen(false); load() }}
        />
      )}
    </div>
  )
}

// ── Main page ────────────────────────────────────────────────────────────────
export function TerminalPage() {
  const [accountId, setAccountId] = useState<string | null>(null)
  const [symbol, setSymbol] = useState(_cachedSymbol)
  const [tf, setTf] = useState(_cachedTf)
  const [bottomTab, setBottomTab] = useState<BottomTab>('positions')
  const [rightTab, setRightTab] = useState<RightTab>('manual')

  useEffect(() => {
    listAccounts().then(accs => {
      if (accs.length > 0 && accs[0]) setAccountId(accs[0].id)
    }).catch(() => {})
  }, [])

  useEffect(() => { _cachedSymbol = symbol }, [symbol])
  useEffect(() => { _cachedTf = tf }, [tf])

  const { positions, orders, log, status, accountName, loading, reconnect, removeOrder } = usePositionsWs(accountId)
  const { candles, candleSymbol, lastPrice, priceChange, loadMore } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  // Hedge mode (shared across right panel)
  const hedgeModeFromPositions = positions.some(p => p.symbol === symbol && p.positionIdx !== 0)
  const [hedgeModeOverride, setHedgeModeOverride] = useState<boolean | null>(null)
  const hedgeStorageKey = accountId ? `sis_hedge_${accountId}_${symbol}` : null

  useEffect(() => {
    if (!hedgeStorageKey) return
    const stored = localStorage.getItem(hedgeStorageKey)
    setHedgeModeOverride(stored === 'true' ? true : stored === 'false' ? false : null)
  }, [hedgeStorageKey])

  useEffect(() => {
    if (!hedgeStorageKey || hedgeModeOverride === null) return
    localStorage.setItem(hedgeStorageKey, String(hedgeModeOverride))
  }, [hedgeStorageKey, hedgeModeOverride])

  useEffect(() => {
    if (hedgeModeFromPositions) setHedgeModeOverride(true)
  }, [hedgeModeFromPositions])

  const hedgeMode = hedgeModeOverride !== null ? hedgeModeOverride : hedgeModeFromPositions


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
    { key: 'pnl', label: 'P&L' },
  ]

  return (
    <div className="bg-gray-100 dark:bg-[#07070f]" style={{ display: 'grid', gridTemplateColumns: '75% 25%', gridTemplateRows: '65% 35%', height: 'calc(100vh - 65px)', gap: '10px', padding: '10px' }}>

      {/* Chart area */}
      <div style={{ gridColumn: 1, gridRow: 1 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
          <CoinPicker value={symbol} onChange={setSymbol} />
          <div className="flex gap-1">
            {TIMEFRAMES.map(t => (
              <button key={t.value} onClick={() => setTf(t.value)}
                className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${tf === t.value ? 'bg-blue-500 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
                {t.label}
              </button>
            ))}
          </div>
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
          <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} />
        </div>
      </div>

      {/* Bottom: positions/orders/history */}
      <div style={{ gridColumn: 1, gridRow: 2 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        <div className="flex items-center border-b border-gray-200 dark:border-gray-700 flex-shrink-0 px-2">
          {bottomTabs.map(t => (
            <button key={t.key} onClick={() => setBottomTab(t.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${bottomTab === t.key ? 'border-blue-500 text-blue-600 dark:text-white' : 'border-transparent text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white'}`}>
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
          {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} onSelect={setSymbol} onRemoveOrder={removeOrder} />}
          {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
          {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
          {bottomTab === 'log' && <TradeLog log={log} />}
          {bottomTab === 'pnl' && <PnlTable accountId={accountId ?? undefined} />}
        </div>
      </div>

      {/* Right panel */}
      <div style={{ gridColumn: 2, gridRow: '1 / 3' }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">

        {/* Tab bar */}
        <div className="flex gap-1.5 p-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0 bg-gray-50 dark:bg-gray-800/60">
          {([
            { key: 'manual', label: 'Торговля' },
            { key: 'strategies', label: 'Стратегии' },
            { key: 'bots', label: 'Боты' },
          ] as { key: RightTab; label: string }[]).map(t => (
            <button
              key={t.key}
              onClick={() => setRightTab(t.key)}
              className={`flex-1 py-1.5 rounded-lg text-xs font-semibold transition-all ${
                rightTab === t.key
                  ? 'bg-white dark:bg-gray-700 text-gray-900 dark:text-white shadow-sm'
                  : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div className="flex-1 overflow-y-auto flex flex-col">
          {rightTab === 'manual' && (
            <>
              {accountId ? (
                <OrderForm
                  accountId={accountId}
                  symbol={symbol}
                  onSymbolChange={setSymbol}
                  lastPrice={lastPrice}
                  orders={orders}
                  hedgeMode={hedgeMode}
                />
              ) : (
                <div className="flex flex-col items-center justify-center p-6 text-center text-gray-500 dark:text-gray-400 gap-3 border-b border-gray-200 dark:border-gray-700">
                  <span className="text-3xl opacity-30">🔑</span>
                  <p className="text-sm">Нет аккаунта Bybit. Добавьте API-ключи в настройках.</p>
                </div>
              )}
              <GridForm
                accountId={accountId ?? ''}
                symbol={symbol}
                onSymbolChange={setSymbol}
                hedgeMode={hedgeMode}
                lastPrice={lastPrice}
              />
              <div style={{ height: 280 }} className="flex-shrink-0">
                <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
              </div>
            </>
          )}
          {rightTab === 'strategies' && <TerminalStrategiesTab onSymbolChange={setSymbol} orders={orders} positions={positions} />}
          {rightTab === 'bots' && (
            <div className="flex-1 flex items-center justify-center text-gray-400 dark:text-gray-500 text-sm">
              В разработке
            </div>
          )}
        </div>
      </div>

    </div>
  )
}
