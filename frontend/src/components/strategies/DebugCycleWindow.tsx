import { useState, useEffect, useRef, useCallback } from 'react'
import { getStrategyState } from '../../api/strategies'
import type { Strategy, StrategyState, ActiveOrder } from '../../types'

interface Props {
  strategy: Strategy
  orders: ActiveOrder[]   // live WS orders — already filtered to this strategy by parent
  onClose: () => void
}

function shortId(id?: string | null) { return id ? id.slice(0, 8) : '—' }

function parseLinkLabel(linkId?: string | null): string {
  if (!linkId) return '?'
  if (linkId.includes('-tp-')) return 'TP'
  if (linkId.includes('-sl-')) return 'SL'
  const m = linkId.match(/-\d+-(\d+)(?:-\d+)?$/)
  return m ? `L${m[1]}` : linkId.slice(-8)
}

function dbStatusColor(s: string) {
  if (s === 'filled')    return 'text-emerald-400'
  if (s === 'placed')    return 'text-blue-400'
  if (s === 'cancelled') return 'text-slate-500'
  if (s === 'pending')   return 'text-yellow-400'
  return 'text-slate-300'
}

function exchStatusColor(s: string) {
  if (s === 'Filled')                                             return 'text-emerald-400'
  if (s === 'New' || s === 'PartiallyFilled')                    return 'text-blue-400'
  if (s === 'Cancelled' || s === 'Rejected' || s === 'Deactivated') return 'text-slate-500'
  return 'text-slate-300'
}

export function DebugCycleWindow({ strategy, orders, onClose }: Props) {
  const [state, setState] = useState<StrategyState | null>(null)
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null)
  const [fetchError, setFetchError] = useState<string | null>(null)

  // Dragging
  const [pos, setPos] = useState({ left: Math.max(40, window.innerWidth / 2 - 480), top: 80 })
  const drag = useRef<{ mx: number; my: number; left: number; top: number } | null>(null)

  const refresh = useCallback(async () => {
    try {
      const st = await getStrategyState(strategy.id)
      setState(st)
      setLastRefresh(new Date())
      setFetchError(null)
    } catch (e: any) {
      setFetchError(e?.message ?? 'ошибка')
    }
  }, [strategy.id])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [refresh])

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!drag.current) return
      setPos({ left: drag.current.left + e.clientX - drag.current.mx, top: drag.current.top + e.clientY - drag.current.my })
    }
    function onUp() { drag.current = null }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [])

  function onHeaderMouseDown(e: React.MouseEvent) {
    if ((e.target as HTMLElement).closest('button')) return
    drag.current = { mx: e.clientX, my: e.clientY, left: pos.left, top: pos.top }
    e.preventDefault()
  }

  // Build exchange order lookup by orderId and linkId
  const exchById: Record<string, ActiveOrder> = {}
  const exchByLink: Record<string, ActiveOrder> = {}
  for (const o of orders) {
    if (o.orderId)     exchById[o.orderId]     = o
    if (o.orderLinkId) exchByLink[o.orderLinkId] = o
  }

  // Narrow to current cycle only
  const cyclePrefix = state ? `SIS_STR-${strategy.id.slice(0, 8)}-${state.cycle_num}-` : null
  const tpPrefix    = state ? `SIS_STR-${strategy.id.slice(0, 8)}-tp-${state.cycle_num}` : null
  const slPrefix    = state ? `SIS_STR-${strategy.id.slice(0, 8)}-sl-${state.cycle_num}` : null

  const cycleOrders = cyclePrefix
    ? orders.filter(o => {
        const lk = o.orderLinkId ?? ''
        return lk.startsWith(cyclePrefix) || (tpPrefix && lk.startsWith(tpPrefix)) || (slPrefix && lk.startsWith(slPrefix))
      })
    : orders

  return (
    <div
      className="fixed z-[9999] flex flex-col rounded-xl shadow-2xl overflow-hidden"
      style={{
        left: pos.left, top: pos.top, width: 980, maxHeight: '78vh',
        background: '#1a1a2e', border: '1px solid #3a3a5c',
      }}
    >
      {/* Header */}
      <div
        className="flex items-center justify-between px-4 py-2 border-b cursor-move select-none shrink-0"
        style={{ background: '#1e1e32', borderColor: '#2e2e48' }}
        onMouseDown={onHeaderMouseDown}
      >
        <div className="flex items-center gap-3">
          <span className="text-xs font-mono font-semibold text-slate-200">
            DEBUG · {strategy.symbol} · цикл&nbsp;
            <span className="text-blue-400">{state?.cycle_num ?? '…'}</span>
          </span>
          {fetchError
            ? <span className="text-[10px] text-red-400">{fetchError}</span>
            : <span className="text-[10px] text-slate-500">
                {lastRefresh ? `↻ ${lastRefresh.toLocaleTimeString()}` : 'загрузка…'}
              </span>
          }
        </div>
        <button
          onClick={onClose}
          className="w-5 h-5 flex items-center justify-center text-slate-400 hover:text-slate-100 text-xs rounded"
        >✕</button>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-auto">
        <div className="grid grid-cols-2 h-full" style={{ gridTemplateColumns: '1fr 1fr' }}>

          {/* ── LEFT: DB ── */}
          <div style={{ borderRight: '1px solid #2e2e48' }}>
            <div className="sticky top-0 px-3 py-1 text-[10px] text-slate-400 uppercase tracking-wider border-b"
              style={{ background: '#202035', borderColor: '#2e2e48' }}>
              БД — strategy_levels&nbsp;
              <span className="text-slate-500 normal-case">({state?.levels?.length ?? 0} строк)</span>
            </div>
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="text-[10px] text-slate-500 border-b" style={{ borderColor: '#272740' }}>
                  <th className="px-2 py-1 text-left font-normal">ур.</th>
                  <th className="px-2 py-1 text-left font-normal">статус</th>
                  <th className="px-2 py-1 text-right font-normal">цена/fill</th>
                  <th className="px-2 py-1 text-right font-normal">USDT</th>
                  <th className="px-2 py-1 text-left font-normal">orderId на бирже</th>
                </tr>
              </thead>
              <tbody>
                {(state?.levels ?? []).map((l, i) => {
                  const onExch = l.exchange_order_id
                    ? !!(exchById[l.exchange_order_id] || exchByLink[l.exchange_order_id])
                    : null
                  return (
                    <tr key={i} className="border-b hover:bg-white/[.03]" style={{ borderColor: '#1e1e30' }}>
                      <td className="px-2 py-1 text-slate-300">L{l.level_idx} <span className="text-slate-500">{l.side[0]}</span></td>
                      <td className={`px-2 py-1 ${dbStatusColor(l.status)}`}>{l.status}</td>
                      <td className="px-2 py-1 text-right">
                        {l.status === 'filled' && l.filled_price
                          ? <span className="text-emerald-400">{l.filled_price.toFixed(2)}</span>
                          : <span className="text-slate-300">{l.target_price.toFixed(2)}</span>
                        }
                      </td>
                      <td className="px-2 py-1 text-right text-slate-400">{l.size_usdt}$</td>
                      <td className="px-2 py-1">
                        {l.exchange_order_id
                          ? <span
                              className={onExch ? 'text-emerald-400' : 'text-amber-400'}
                              title={l.exchange_order_id}
                            >
                              {shortId(l.exchange_order_id)}
                              {!onExch && <span className="ml-1 text-amber-500 text-[9px]">⚠нет</span>}
                            </span>
                          : <span className="text-slate-600">—</span>
                        }
                      </td>
                    </tr>
                  )
                })}

                {/* TP */}
                {state?.tp_order_id && (() => {
                  const onExch = !!exchById[state.tp_order_id]
                  return (
                    <tr className="border-b hover:bg-white/[.03]" style={{ borderColor: '#1e1e30' }}>
                      <td className="px-2 py-1 text-blue-400">TP</td>
                      <td className="px-2 py-1 text-blue-400">placed</td>
                      <td className="px-2 py-1 text-right text-slate-500">—</td>
                      <td className="px-2 py-1 text-right text-slate-500">—</td>
                      <td className="px-2 py-1">
                        <span className={onExch ? 'text-emerald-400' : 'text-amber-400'} title={state.tp_order_id}>
                          {shortId(state.tp_order_id)}
                          {!onExch && <span className="ml-1 text-amber-500 text-[9px]">⚠нет</span>}
                        </span>
                      </td>
                    </tr>
                  )
                })()}

                {/* SL */}
                {state?.sl_order_id && (() => {
                  const onExch = !!exchById[state.sl_order_id]
                  return (
                    <tr className="border-b hover:bg-white/[.03]" style={{ borderColor: '#1e1e30' }}>
                      <td className="px-2 py-1 text-yellow-400">SL</td>
                      <td className="px-2 py-1 text-yellow-400">placed</td>
                      <td className="px-2 py-1 text-right text-slate-500">—</td>
                      <td className="px-2 py-1 text-right text-slate-500">—</td>
                      <td className="px-2 py-1">
                        <span className={onExch ? 'text-emerald-400' : 'text-amber-400'} title={state.sl_order_id}>
                          {shortId(state.sl_order_id)}
                          {!onExch && <span className="ml-1 text-amber-500 text-[9px]">⚠нет</span>}
                        </span>
                      </td>
                    </tr>
                  )
                })()}

                {!state && (
                  <tr><td colSpan={5} className="px-3 py-4 text-center text-slate-500 text-[10px]">нет активного цикла</td></tr>
                )}
              </tbody>
            </table>
          </div>

          {/* ── RIGHT: Exchange ── */}
          <div>
            <div className="sticky top-0 px-3 py-1 text-[10px] text-slate-400 uppercase tracking-wider border-b"
              style={{ background: '#202035', borderColor: '#2e2e48' }}>
              Биржа — open orders (цикл {state?.cycle_num ?? '?'})&nbsp;
              <span className="text-slate-500 normal-case">({cycleOrders.length} / {orders.length} стратегии)</span>
            </div>
            <table className="w-full text-[11px] font-mono">
              <thead>
                <tr className="text-[10px] text-slate-500 border-b" style={{ borderColor: '#272740' }}>
                  <th className="px-2 py-1 text-left font-normal">label</th>
                  <th className="px-2 py-1 text-left font-normal">статус</th>
                  <th className="px-2 py-1 text-right font-normal">цена</th>
                  <th className="px-2 py-1 text-right font-normal">qty</th>
                  <th className="px-2 py-1 text-left font-normal">orderId / linkId</th>
                </tr>
              </thead>
              <tbody>
                {cycleOrders.map((o, i) => {
                  const label = parseLinkLabel(o.orderLinkId)
                  const labelColor = label === 'TP' ? 'text-blue-400' : label === 'SL' ? 'text-yellow-400' : 'text-slate-200'
                  const price = o.triggerPrice && parseFloat(o.triggerPrice) > 0
                    ? o.triggerPrice : o.price
                  const inDbById = o.orderId ? !!(
                    state?.levels.some(l => l.exchange_order_id === o.orderId) ||
                    state?.tp_order_id === o.orderId ||
                    state?.sl_order_id === o.orderId
                  ) : false
                  return (
                    <tr key={i} className="border-b hover:bg-white/[.03]" style={{ borderColor: '#1e1e30' }}>
                      <td className={`px-2 py-1 ${labelColor}`}>
                        {label} <span className="text-slate-500">{o.side?.[0]}</span>
                      </td>
                      <td className={`px-2 py-1 ${exchStatusColor(o.orderStatus)}`}>{o.orderStatus}</td>
                      <td className="px-2 py-1 text-right text-slate-200">
                        {price ? parseFloat(price).toFixed(2) : '—'}
                      </td>
                      <td className="px-2 py-1 text-right text-slate-300">{o.qty}</td>
                      <td className="px-2 py-1">
                        <div className="flex flex-col gap-0">
                          <span
                            className={inDbById ? 'text-emerald-400' : 'text-amber-400'}
                            title={o.orderId ?? ''}
                          >
                            {shortId(o.orderId)}
                            {!inDbById && <span className="ml-1 text-amber-500 text-[9px]">⚠нет в БД</span>}
                          </span>
                          <span className="text-slate-500 text-[9px]" title={o.orderLinkId ?? ''}>
                            {o.orderLinkId ? o.orderLinkId.slice(-16) : ''}
                          </span>
                        </div>
                      </td>
                    </tr>
                  )
                })}
                {!cycleOrders.length && (
                  <tr><td colSpan={5} className="px-3 py-4 text-center text-slate-500 text-[10px]">нет ордеров текущего цикла</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  )
}
