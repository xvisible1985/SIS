import { useState, useEffect, useMemo } from 'react'
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
import { useTickerPrices } from '../hooks/terminal/useTickerPrices'
import { listAccounts } from '../api/accounts'
import { listStrategies } from '../api/strategies'
import { listExecutions } from '../api/trader'
import { StrategyCard } from '../components/strategies/StrategyCard'
import { StrategyModal } from '../components/strategies/StrategyModal'
import type { Strategy, ExchangeAccount, ActiveOrder, Position, ChartExecution } from '../types'

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

const STATUS_ORDER: Record<string, number> = { active: 0, finishing: 1, stopped: 2 }
function sortStrategies<T extends { status: string; symbol: string }>(list: T[]): T[] {
  return [...list].sort((a, b) => {
    const sd = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9)
    return sd !== 0 ? sd : a.symbol.localeCompare(b.symbol)
  })
}

// ── Strategies tab ───────────────────────────────────────────────────────────
type LiveSignal = { signal_state: string; signal_values: Record<string, number> }

function TerminalStrategiesTab({ onSymbolChange, orders, positions }: { onSymbolChange: (sym: string) => void; orders: ActiveOrder[]; positions: Position[] }) {
  const [strategies, setStrategies] = useState<Strategy[]>([])
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [editTarget, setEditTarget] = useState<Strategy | undefined>()
  const [modalOpen, setModalOpen] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [signalStates, setSignalStates] = useState<Record<string, LiveSignal>>({})

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

  useEffect(() => {
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    function connect() {
      const token = localStorage.getItem('token') ?? ''
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      ws = new WebSocket(`${proto}//${window.location.host}/ws/strategies/updates?token=${encodeURIComponent(token)}`)
      ws.onmessage = (evt) => {
        try {
          const updates: { id: string; status: string; active_levels: number; volume_usdt: number; signal_state?: string; signal_values?: Record<string, number> }[] = JSON.parse(evt.data as string)
          if (updates.length > 0) {
            setStrategies(prev => prev.map(s => {
              const upd = updates.find(u => u.id === s.id)
              return upd ? { ...s, status: upd.status as Strategy['status'], active_levels: upd.active_levels, volume_usdt: upd.volume_usdt } : s
            }))
            const sigUpdates = updates.filter(u => u.signal_state !== undefined)
            if (sigUpdates.length > 0) {
              setSignalStates(prev => {
                const next = { ...prev }
                for (const u of sigUpdates) {
                  const newVals = u.signal_values && Object.keys(u.signal_values).length > 0
                    ? u.signal_values
                    : prev[u.id]?.signal_values ?? {}
                  next[u.id] = { signal_state: u.signal_state!, signal_values: newVals }
                }
                return next
              })
            }
          }
        } catch { /* ignore */ }
      }
      ws.onclose = () => { reconnectTimer = setTimeout(connect, 5000) }
      ws.onerror = () => ws?.close()
    }
    connect()
    return () => {
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (ws) { ws.onclose = null; ws.close() }
    }
  }, [])

  function handleSelect(s: Strategy) {
    setSelectedId(s.id)
    onSymbolChange(s.symbol)
    setExpandedId(prev => (prev !== null && prev !== s.id ? null : prev))
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
        {sortStrategies(strategies).map(s => (
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
            liveSignal={signalStates[s.id]}
            onToggleOpen={() => {
              const isExpanding = expandedId !== s.id
              setExpandedId(isExpanding ? s.id : null)
              if (isExpanding) { setSelectedId(s.id); onSymbolChange(s.symbol) }
            }}
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

  const { positions, orders, executions, log, status, accountName, loading, reconnect, removeOrder } = usePositionsWs(accountId)

  const [historicalExecs, setHistoricalExecs] = useState<ChartExecution[]>([])
  useEffect(() => {
    if (!accountId) return
    listExecutions({ account_id: accountId, symbol, type: 'Trade', limit: 200 })
      .then(res => setHistoricalExecs(
        res.executions
          .filter(e => e.side && e.price && e.order_link_id?.startsWith('SIS_STR'))
          .map(e => ({
            execId: e.exec_id,
            symbol: e.symbol,
            side: e.side as 'Buy' | 'Sell',
            price: parseFloat(e.price!),
            qty: e.qty ?? '',
            timeMs: new Date(e.exec_time).getTime(),
            orderLinkId: e.order_link_id ?? undefined,
          }))
      ))
      .catch(() => {})
  }, [accountId, symbol])

  const allExecutions = useMemo<ChartExecution[]>(() => {
    const wsIds = new Set(executions.map(e => e.execId))
    return [...historicalExecs.filter(e => !wsIds.has(e.execId)), ...executions]
  }, [historicalExecs, executions])

  const { candles, candleSymbol, lastPrice, priceChange, loadMore } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  const positionSymbols = useMemo(() => [...new Set(positions.map(p => p.symbol))], [positions])
  const tickerPrices = useTickerPrices(positionSymbols)

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
    <div className="bg-gray-100 dark:bg-[#07070f]" style={{ display: 'grid', gridTemplateColumns: '72.5% 27.5%', gridTemplateRows: '65% 35%', height: 'calc(100vh - 65px)', gap: '10px', padding: '10px' }}>

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
          <Chart candles={candles} candleSymbol={candleSymbol} positions={positions} orders={orders} executions={allExecutions} symbol={symbol} lastPrice={lastPrice} onLoadMore={loadMore} />
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
          {bottomTab === 'positions' && <PositionsTable accountId={accountId ?? ''} positions={positions} onSelect={setSymbol} loading={loading} tickerPrices={tickerPrices} />}
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
        <div className="p-3 pb-2.5 border-b border-gray-200 dark:border-white/[.06] flex-shrink-0">
          <div className="grid grid-cols-3 gap-0.5 p-[3px] bg-white/[.04] border border-white/[.06] rounded-[11px]">
            {([
              { key: 'manual'    , label: 'Торговля',  icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><path d="M3 17l5-5 4 4 4-7 5 8"/><circle cx="21" cy="5" r="1.4" fill="currentColor" stroke="none"/></svg> },
              { key: 'strategies', label: 'Стратегии', icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg> },
              { key: 'bots'      , label: 'Боты',      icon: <svg width={14} height={14} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round"><rect x="4" y="7" width="16" height="12" rx="3"/><circle cx="9" cy="13" r="1.2" fill="currentColor" stroke="none"/><circle cx="15" cy="13" r="1.2" fill="currentColor" stroke="none"/><path d="M12 4v3M9 19v2M15 19v2"/></svg> },
            ] as { key: RightTab; label: string; icon: React.ReactNode }[]).map(t => (
              <button
                key={t.key}
                onClick={() => setRightTab(t.key)}
                className={`inline-flex items-center justify-center gap-1.5 py-2 px-1.5 rounded-[8px] text-[12.5px] font-semibold transition-all leading-none ${
                  rightTab === t.key
                    ? 'bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_10px_-6px_rgba(74,125,255,.6)]'
                    : 'text-slate-400 hover:text-slate-200'
                }`}
                style={{ strokeWidth: rightTab === t.key ? 2 : 1.8 }}
              >
                {t.icon}{t.label}
              </button>
            ))}
          </div>
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
