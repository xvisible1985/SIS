import { useState, useEffect, useRef, useMemo } from 'react'
import { getStrategyState, getStrategyEvents, setStrategyStatus, deleteStrategy } from '../../api/strategies'
import { SignalMiniCard } from './SignalMiniCard'
import type { Strategy, StrategyState, StrategyEvent, ExchangeAccount, ActiveOrder, Position } from '../../types'

interface Props {
  strategy: Strategy
  accounts: ExchangeAccount[]
  orders: ActiveOrder[]
  positions?: Position[]
  onEdit: (s: Strategy) => void
  onChanged: () => void
  selected?: boolean
  onSelect?: (s: Strategy) => void
  isOpen?: boolean
  onToggleOpen?: () => void
}

interface CardState {
  state: StrategyState | null
  events: StrategyEvent[]
  loading: boolean
  acting: boolean
}

function statusBadge(status: string) {
  if (status === 'active')
    return <span className="text-[10px] px-2 py-0.5 rounded-full bg-green-900/40 text-green-400">Active</span>
  if (status === 'finishing')
    return <span className="text-[10px] px-2 py-0.5 rounded-full bg-yellow-900/40 text-yellow-400">Finishing</span>
  return <span className="text-[10px] px-2 py-0.5 rounded-full bg-gray-800 text-gray-500">Stopped</span>
}

function pnlColor(v: number) {
  if (v > 0) return 'text-green-400'
  if (v < 0) return 'text-red-400'
  return 'text-gray-500'
}

export function StrategyCard({ strategy: s, accounts, orders, positions, onEdit, onChanged, selected, onSelect, isOpen, onToggleOpen }: Props) {
  const [cs, setCs] = useState<CardState>({ state: null, events: [], loading: false, acting: false })
  const [logOpen, setLogOpen] = useState(false)

  const accountName = accounts.find(a => a.id === s.account_id)?.label ?? s.account_id.slice(0, 8)

  // Initial load when card opens; reset log collapse when card closes
  useEffect(() => {
    if (isOpen && !cs.state && !cs.loading) {
      setCs(p => ({ ...p, loading: true }))
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, events]) => setCs(p => ({ ...p, state, events, loading: false })))
        .catch(() => setCs(p => ({ ...p, loading: false })))
    }
    if (!isOpen) setLogOpen(false)
  }, [isOpen])

  // Reload state when this strategy's orders change (supports both old and new link ID formats)
  const strategyOrders = useMemo(() => {
    const id8 = s.id.slice(0, 8)
    return orders.filter(o => {
      const lk = o.orderLinkId ?? ''
      return lk.startsWith('SIS_STR-' + id8) || lk.startsWith('STR-' + id8) || lk.startsWith('STP-' + id8)
    })
  }, [orders, s.id])

  const strategyOrdersKey = useMemo(
    () => strategyOrders.map(o => o.orderId + ':' + o.orderStatus).sort().join(','),
    [strategyOrders],
  )

  // Count only L-level orders (exclude TP/SL) for the Grid X/Y header display
  const activeLOrders = useMemo(
    () => strategyOrders.filter(o => /^(SIS_STR|STR)-[a-f0-9]+-\d+-\d+$/.test(o.orderLinkId ?? '')).length,
    [strategyOrders],
  )

  useEffect(() => {
    if (!isOpen || cs.acting) return
    Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
      .then(([state, events]) => setCs(p => ({ ...p, state, events })))
      .catch(() => {})
  }, [strategyOrdersKey])

  // Immediately reload when the position for this strategy closes via WS
  const hadPositionRef = useRef(false)
  useEffect(() => {
    if (!positions) return
    const expectedSide = s.direction === 'long' ? 'Buy' : 'Sell'
    const hasPos = positions.some(p => p.symbol === s.symbol && p.side === expectedSide && parseFloat(p.size) > 0)
    if (hadPositionRef.current && !hasPos) {
      // Position just closed — reload card state immediately
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, events]) => setCs(p => ({ ...p, state, events })))
        .catch(() => {})
      onChanged()
    }
    hadPositionRef.current = hasPos
  }, [positions])

  // Poll state every 5s while card is open so avg_entry / TP / SL stay fresh
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  useEffect(() => {
    if (!isOpen) {
      if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null }
      return
    }
    pollRef.current = setInterval(() => {
      if (cs.acting) return
      Promise.all([getStrategyState(s.id), getStrategyEvents(s.id)])
        .then(([state, events]) => setCs(p => ({ ...p, state, events })))
        .catch(() => {})
    }, 5000)
    return () => { if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null } }
  }, [isOpen, s.id])

  function expand(e?: React.MouseEvent) {
    e?.stopPropagation()
    if (onSelect) onSelect(s)
    onToggleOpen?.()
  }

  function handleHeaderClick() {
    if (onSelect) onSelect(s)
    else onToggleOpen?.()
  }

  async function handleStatus(status: 'active' | 'finishing' | 'stopped') {
    setCs(p => ({ ...p, acting: true }))
    try {
      await setStrategyStatus(s.id, status)
      onChanged()
      // One quick reload to sync DB state; WS events will trigger further updates
      await new Promise(r => setTimeout(r, 300))
      const [state, events] = await Promise.all([
        getStrategyState(s.id).catch(() => null),
        getStrategyEvents(s.id).catch(() => [] as StrategyEvent[]),
      ])
      setCs(p => ({ ...p, state, events, acting: false }))
    } catch {
      setCs(p => ({ ...p, acting: false }))
    }
  }

  async function handleDelete() {
    if (!window.confirm('Удалить стратегию?')) return
    setCs(p => ({ ...p, acting: true }))
    try {
      await deleteStrategy(s.id)
      onChanged()
    } finally {
      setCs(p => ({ ...p, acting: false }))
    }
  }

  function tpPrice() {
    if (!cs.state || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.avg_entry * (1 - s.tp_pct / 100)
      : cs.state.avg_entry * (1 + s.tp_pct / 100)
  }

  function slPrice() {
    if (!cs.state || cs.state.start_price === 0 || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.start_price * (1 + s.sl_pct / 100)
      : cs.state.start_price * (1 - s.sl_pct / 100)
  }

  const btnBase = 'px-3 py-1.5 text-xs rounded-lg transition-colors disabled:opacity-50'

  return (
    <li className={`rounded-xl overflow-hidden transition-colors ${
      selected
        ? s.direction === 'long'
          ? 'bg-green-900/40 border border-green-700/60'
          : 'bg-red-900/40 border border-red-700/60'
        : s.direction === 'long'
          ? 'bg-green-950/20 border border-gray-700'
          : 'bg-red-950/20 border border-gray-700'
    }`}>
      {/* Collapsed header */}
      <button
        onClick={handleHeaderClick}
        className="w-full flex items-center gap-3 px-5 py-3.5 hover:bg-gray-800/40 transition-colors text-left"
      >
        <div className="min-w-0 shrink-0">
          <div className="font-semibold text-gray-100 text-sm">{s.symbol}</div>
          <div className="text-[11px] text-gray-500 capitalize">
            {s.direction} · {accountName}
          </div>
        </div>
        <div className="flex items-center gap-4 ml-auto shrink-0 text-right">
          <div>
            <div className="text-[10px] text-gray-500">Grid</div>
            <div className="text-[11px] text-gray-300">{activeLOrders}/{s.grid_levels}</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-500">Объём</div>
            <div className="text-[11px] text-gray-300">{s.volume_usdt.toFixed(0)} USDT</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-500">P&L</div>
            <div className={`text-[11px] font-mono ${pnlColor(s.last_pnl)}`}>
              {s.last_pnl === 0 ? '—' : `${s.last_pnl > 0 ? '+' : ''}${s.last_pnl.toFixed(2)}`}
            </div>
          </div>
          {statusBadge(s.status)}
          <button
            onClick={expand}
            className="p-1 -mr-1 rounded hover:bg-gray-700/50 transition-colors"
          >
            <svg
              className={`w-4 h-4 text-gray-500 transition-transform ${isOpen ? 'rotate-180' : ''}`}
              fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
            </svg>
          </button>
        </div>
      </button>

      {/* Expanded body */}
      <div
        className="grid transition-[grid-template-rows] duration-200 ease-in-out"
        style={{ gridTemplateRows: isOpen ? '1fr' : '0fr' }}
      >
      <div className="overflow-hidden">
        <div className="border-t border-gray-700 bg-gray-900">

          {cs.loading && (
            <div className="px-5 py-6 text-sm text-gray-500 text-center">Загрузка…</div>
          )}

          {!cs.loading && (
            <>
              {/* Signals */}
              {s.signal_configs && s.signal_configs.length > 0 && (
                <div className="px-5 py-3 border-b border-gray-800">
                  <div className="text-[10px] text-gray-500 uppercase tracking-wider mb-2">Сигналы</div>
                  <div className="flex gap-2 flex-wrap">
                    {s.signal_configs.map(sc => (
                      <SignalMiniCard key={sc.name} signal={sc} />
                    ))}
                  </div>
                </div>
              )}

              {/* Ждёт */}
              <div className="px-5 py-3 border-b border-gray-800">
                <div className="text-[10px] text-gray-500 uppercase tracking-wider mb-2">Ждёт</div>
                <div className="flex gap-4">
                  {/* Left: levels */}
                  <div className="flex-[3] space-y-1.5">
                    <div className="text-[9px] text-gray-500 uppercase mb-1">Уровни</div>
                    {cs.state && cs.state.levels.length > 0 ? (
                      cs.state.levels.map(l => (
                        <div key={l.level_idx} className="flex items-center gap-2 text-[11px]">
                          <span className={
                            l.status === 'filled' ? 'text-green-400' :
                            l.status === 'placed' ? 'text-yellow-400' :
                            l.status === 'cancelled' ? 'text-gray-700' : 'text-gray-500'
                          }>
                            {l.status === 'filled' ? '✓' : l.status === 'placed' ? '◉' : l.status === 'cancelled' ? '✕' : '○'}
                          </span>
                          <span className={l.status === 'cancelled' ? 'text-gray-500' : 'text-gray-400'}>
                            L{l.level_idx} @ <span className={l.status === 'cancelled' ? 'text-gray-500' : 'text-gray-200'}>{l.size_usdt.toFixed(0)} USDT</span>
                          </span>
                          <span className={`ml-auto text-[9px] ${
                            l.status === 'filled' ? 'text-green-500' :
                            l.status === 'placed' ? 'text-yellow-500' :
                            l.status === 'cancelled' ? 'text-gray-700' : 'text-gray-500'
                          }`}>
                            {l.status === 'filled' ? 'заполнен' : l.status === 'placed' ? 'ожидает' : l.status === 'cancelled' ? 'отменён' : 'pending'}
                          </span>
                        </div>
                      ))
                    ) : (
                      <div className="text-[11px] text-gray-500">Нет активного цикла</div>
                    )}
                  </div>

                  {/* Divider */}
                  <div className="w-px bg-gray-800 shrink-0" />

                  {/* Right: TP/SL */}
                  <div className="w-36 space-y-2">
                    <div className="text-[9px] text-gray-500 uppercase mb-1">Выход</div>
                    <div className="border border-green-900/40 bg-green-950/20 rounded-lg px-3 py-2">
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-[10px] text-green-400 font-semibold">Take Profit</span>
                        {cs.state?.tp_order_id
                          ? <span className="text-[9px] bg-green-900/40 text-green-500 px-1.5 py-0.5 rounded">выставлен</span>
                          : <span className="text-[9px] text-gray-500">—</span>
                        }
                      </div>
                      <div className="font-mono text-gray-100 text-sm">
                        {tpPrice() ? tpPrice()!.toFixed(2) : '—'}
                      </div>
                      <div className="text-[10px] text-green-400">+{s.tp_pct}% от avg entry</div>
                    </div>
                    <div className="border border-red-900/40 bg-red-950/20 rounded-lg px-3 py-2">
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-[10px] text-red-400 font-semibold">Stop Loss</span>
                        <span className="text-[9px] text-gray-500">{s.sl_type}</span>
                      </div>
                      <div className="font-mono text-gray-100 text-sm">
                        {slPrice() ? slPrice()!.toFixed(2) : '—'}
                      </div>
                      <div className="text-[10px] text-red-400">−{s.sl_pct}% от start</div>
                    </div>
                  </div>
                </div>
              </div>

              {/* Events log */}
              {cs.events.length > 0 && (
                <div className="border-b border-gray-800">
                  <button
                    onClick={() => setLogOpen(p => !p)}
                    className="w-full flex items-center gap-2 px-5 py-3 hover:bg-gray-800/30 transition-colors"
                  >
                    <span className="text-[10px] text-gray-500 uppercase tracking-wider">Лог</span>
                    <span className="text-[10px] text-gray-600 ml-1">({cs.events.length})</span>
                    <svg
                      className={`w-3 h-3 text-gray-600 ml-auto transition-transform ${logOpen ? 'rotate-180' : ''}`}
                      fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
                    >
                      <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                    </svg>
                  </button>
                  <div
                    className="grid transition-[grid-template-rows] duration-200 ease-in-out"
                    style={{ gridTemplateRows: logOpen ? '1fr' : '0fr' }}
                  >
                    <div className="overflow-hidden">
                      <div className="px-5 pb-3 space-y-0.5 max-h-48 overflow-y-auto">
                        {cs.events.map((e, i) => (
                          <div key={i} className="flex items-start gap-2 text-[10px]">
                            <span className="text-gray-500 shrink-0 font-mono">
                              {new Date(e.created_at).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                            </span>
                            <span className={
                              e.level === 'error' ? 'text-red-400' :
                              e.level === 'warn' ? 'text-yellow-400' : 'text-gray-300'
                            }>{e.message}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>
                </div>
              )}

              {/* Control buttons */}
              <div className="px-5 py-3 flex gap-2">
                <button
                  onClick={() => onEdit(s)}
                  className={`${btnBase} bg-blue-900/30 text-blue-300 border border-blue-800/50 hover:bg-blue-900/50`}
                >
                  ✎ Редактировать
                </button>
                {s.status === 'active' || s.status === 'finishing' ? (
                  <>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('stopped')}
                      className={`${btnBase} bg-red-900/30 text-red-300 border border-red-800/50 hover:bg-red-900/50`}
                    >
                      ■ Остановить
                    </button>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('finishing')}
                      className={`${btnBase} bg-gray-800 text-gray-400 border border-gray-700 hover:bg-gray-700`}
                    >
                      ⊠ Завершить
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      disabled={cs.acting}
                      onClick={() => handleStatus('active')}
                      className={`${btnBase} bg-green-900/30 text-green-300 border border-green-800/50 hover:bg-green-900/50`}
                    >
                      ▶ Включить
                    </button>
                    <button
                      disabled={cs.acting}
                      onClick={handleDelete}
                      className={`${btnBase} ml-auto text-red-500 hover:text-red-400 hover:bg-red-900/20`}
                    >
                      Удалить
                    </button>
                  </>
                )}
              </div>
            </>
          )}
        </div>
      </div>
      </div>
    </li>
  )
}
