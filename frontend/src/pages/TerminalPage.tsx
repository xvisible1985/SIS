import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
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
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { listAccounts } from '../api/accounts'
import { switchPositionMode } from '../api/trader'
import { listStrategies, setStrategyStatus } from '../api/strategies'
import type { Strategy } from '../types'
import type { BookRow } from '../hooks/terminal/useOrderbook'

const COINS = ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT', 'XRPUSDT', 'DOGEUSDT']

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

function coinIcon(sym: string) {
  const base = sym.replace(/USDT|USDC|USD$/, '').toLowerCase()
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${base}.png`
}

// ── Strategies panel ────────────────────────────────────────────────────────
function StrategiesPanel({ symbol, accountId }: { symbol: string; accountId: string | null }) {
  const [open, setOpen] = useState(false)
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [loaded, setLoaded] = useState(false)
  const [acting, setActing] = useState(false)

  useEffect(() => {
    if (!open) return
    setLoaded(false)
    listStrategies()
      .then(all => setStrategies(all.filter(s => s.symbol === symbol)))
      .catch(() => setStrategies([]))
      .finally(() => setLoaded(true))
  }, [open, symbol])

  async function handleStatus(id: string, status: 'active' | 'stopped') {
    setActing(true)
    try {
      await setStrategyStatus(id, status)
      const all = await listStrategies()
      setStrategies(all.filter(s => s.symbol === symbol))
    } finally {
      setActing(false)
    }
  }

  const activeCount = strategies.filter(s => s.status === 'active').length

  return (
    <div className="border-b border-gray-200 dark:border-gray-700">
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white transition-colors"
      >
        <span className="flex items-center gap-1.5">
          <span className="text-[10px] opacity-60">⠿</span>
          Стратегии
          {activeCount > 0 && !open && (
            <span className="bg-green-500/20 text-green-400 text-[10px] px-1.5 rounded-full">{activeCount}</span>
          )}
        </span>
        <span className={`text-[10px] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>

      {open && (
        <div className="border-t border-gray-200 dark:border-gray-700 px-3 py-2 space-y-2 text-xs">
          {!loaded && <div className="py-4 text-center text-gray-500">Загрузка…</div>}
          {loaded && strategies.length === 0 && (
            <div className="py-4 text-center text-gray-500">Нет стратегий для {symbol}</div>
          )}
          {loaded && strategies.map(s => (
            <div key={s.id} className="rounded-lg border border-gray-200 dark:border-gray-700 px-2.5 py-2 space-y-1.5">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-1.5">
                  <span className={`px-1.5 py-0.5 rounded text-[10px] font-semibold ${
                    s.direction === 'long' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'
                  }`}>
                    {s.direction.toUpperCase()}
                  </span>
                  <span className="text-gray-400">{s.strategy_type}</span>
                </div>
                <div className="flex items-center gap-1.5">
                  {s.status === 'active' || s.status === 'finishing' ? (
                    <button
                      disabled={acting}
                      onClick={() => handleStatus(s.id, 'stopped')}
                      className="text-[10px] px-2 py-0.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 disabled:opacity-50 transition-colors"
                    >■</button>
                  ) : (
                    <button
                      disabled={acting}
                      onClick={() => handleStatus(s.id, 'active')}
                      className="text-[10px] px-2 py-0.5 rounded bg-green-500/20 text-green-400 hover:bg-green-500/30 disabled:opacity-50 transition-colors"
                    >▶</button>
                  )}
                  <span className={`text-[10px] px-1.5 py-0.5 rounded-full ${
                    s.status === 'active' ? 'bg-green-900/40 text-green-400' :
                    s.status === 'finishing' ? 'bg-yellow-900/40 text-yellow-400' :
                    'bg-gray-800 text-gray-500'
                  }`}>{s.status}</span>
                </div>
              </div>
              <div className="flex items-center justify-between text-gray-500 text-[10px]">
                <span>Grid {s.active_levels}/{s.grid_levels}</span>
                <span>{s.volume_usdt.toFixed(0)} USDT</span>
                <span className={s.last_pnl > 0 ? 'text-green-400' : s.last_pnl < 0 ? 'text-red-400' : ''}>
                  {s.last_pnl === 0 ? '—' : `${s.last_pnl > 0 ? '+' : ''}${s.last_pnl.toFixed(2)}`}
                </span>
              </div>
            </div>
          ))}
          <Link
            to="/strategies"
            className="block text-center text-[11px] text-blue-400 hover:text-blue-300 py-1 transition-colors"
          >
            Все стратегии →
          </Link>
        </div>
      )}
    </div>
  )
}

// ── Orderbook panel ──────────────────────────────────────────────────────────
function OrderbookPanel({ bids, asks, spread, symbol }: {
  bids: BookRow[]
  asks: BookRow[]
  spread: string | null
  symbol: string
}) {
  const [open, setOpen] = useState(true)
  return (
    <div>
      <button
        onClick={() => setOpen(v => !v)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white transition-colors border-b border-gray-200 dark:border-gray-700"
      >
        <span className="flex items-center gap-1.5">
          <span className="text-[10px] opacity-60">⠿</span>
          Стакан
        </span>
        <span className={`text-[10px] transition-transform duration-200 ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>
      {open && (
        <div style={{ height: 260 }}>
          <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
        </div>
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

  useEffect(() => {
    listAccounts().then(accs => {
      if (accs.length > 0 && accs[0]) setAccountId(accs[0].id)
    }).catch(() => {})
  }, [])

  useEffect(() => { _cachedSymbol = symbol }, [symbol])
  useEffect(() => { _cachedTf = tf }, [tf])

  const { positions, orders, log, status, accountName, loading, reconnect } = usePositionsWs(accountId)
  const { candles, candleSymbol, lastPrice, priceChange, loadMore } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  // Hedge mode (shared across right panel)
  const hedgeModeFromPositions = positions.some(p => p.symbol === symbol && p.positionIdx !== 0)
  const [hedgeModeOverride, setHedgeModeOverride] = useState<boolean | null>(null)
  const [modeLoading, setModeLoading] = useState(false)
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

  async function handleSwitchMode() {
    if (!accountId) return
    const cat = symbol.endsWith('USDT') || symbol.endsWith('USDC') ? 'linear' : 'inverse'
    setModeLoading(true)
    const nextMode = hedgeMode ? 0 : 3
    const res = await switchPositionMode({ account_id: accountId, symbol, category: cat, mode: nextMode })
    if (res.ok) setHedgeModeOverride(nextMode === 3)
    setModeLoading(false)
  }

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
    <div className="bg-gray-100 dark:bg-[#07070f]" style={{ display: 'grid', gridTemplateColumns: '78% 22%', gridTemplateRows: '65% 35%', height: 'calc(100vh - 65px)', gap: '10px', padding: '10px' }}>

      {/* Chart area */}
      <div style={{ gridColumn: 1, gridRow: 1 }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
          <select value={symbol} onChange={e => setSymbol(e.target.value)}
            className="bg-gray-100 dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded px-2 py-1 text-sm text-gray-900 dark:text-white w-36 focus:outline-none">
            {COINS.map(c => <option key={c} value={c}>{c}</option>)}
          </select>
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
          <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} symbol={symbol} onLoadMore={loadMore} />
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
          {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} />}
          {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
          {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
          {bottomTab === 'log' && <TradeLog log={log} />}
          {bottomTab === 'pnl' && <PnlTable accountId={accountId ?? undefined} />}
        </div>
      </div>

      {/* Right panel: shared header + collapsible blocks */}
      <div style={{ gridColumn: 2, gridRow: '1 / 3' }} className="bg-white dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700/50 rounded-xl flex flex-col overflow-hidden">

        {/* Panel header: symbol + hedge mode toggle */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-200 dark:border-gray-700 flex-shrink-0">
          <img
            src={coinIcon(symbol)}
            className="w-5 h-5 rounded-full"
            onError={e => (e.currentTarget.style.display = 'none')}
          />
          <span className="font-bold font-mono text-sm text-gray-900 dark:text-white">{symbol}</span>
          <button
            onClick={handleSwitchMode}
            disabled={!accountId || modeLoading}
            className={`ml-auto flex items-center gap-2 px-2.5 py-1 rounded-lg text-xs font-medium transition-all disabled:opacity-50 ${
              hedgeMode
                ? 'bg-blue-500 text-white hover:bg-blue-600'
                : 'bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400 hover:bg-gray-300 dark:hover:bg-gray-600'
            }`}
          >
            <span className="select-none">Хедж режим</span>
            <span className={`relative inline-flex w-7 h-4 rounded-full transition-colors flex-shrink-0 ${hedgeMode ? 'bg-white/30' : 'bg-black/20'}`}>
              <span className={`absolute top-0.5 w-3 h-3 rounded-full bg-white shadow transition-all ${hedgeMode ? 'left-3.5' : 'left-0.5'}`} />
            </span>
          </button>
        </div>

        {/* Scrollable blocks */}
        <div className="flex-1 overflow-y-auto">
          {accountId ? (
            <OrderForm
              accountId={accountId}
              symbol={symbol}
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
          <GridForm accountId={accountId ?? ''} symbol={symbol} lastPrice={lastPrice} />
          <StrategiesPanel symbol={symbol} accountId={accountId} />
          <OrderbookPanel bids={bids} asks={asks} spread={spread} symbol={symbol} />
        </div>
      </div>

    </div>
  )
}
