import { useState, useEffect, useRef, useCallback } from 'react'
import { getStrategyState } from '../../api/strategies'
import { apiClient } from '../../api/client'
import { setRsiTestOverride } from '../../features/indicators/signals'
import type { Strategy, StrategyState, ActiveOrder } from '../../types'

interface LiveSignal {
  signal_state: string
  signal_values: Record<string, number>
}

interface Props {
  strategy: Strategy
  orders: ActiveOrder[]   // live WS orders — already filtered to this strategy by parent
  onClose: () => void
  liveSignal?: LiveSignal
}

function shortId(id?: string | null) { return id ? id.slice(0, 8) : '—' }

function parseLinkLabel(linkId?: string | null): string {
  if (!linkId) return '?'
  if (linkId.includes('-msl-')) {
    const parts = linkId.split('-')
    const levelIdx = parts.length > 3 ? parts[3] : '?'
    return `SL_L(${levelIdx})`
  }
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

interface OverrideState { active: boolean; value: number }

export function DebugCycleWindow({ strategy, orders, onClose, liveSignal }: Props) {
  const [state, setState] = useState<StrategyState | null>(null)
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null)
  const [fetchError, setFetchError] = useState<string | null>(null)

  // Signal overrides keyed by signal name
  const [overrides, setOverrides] = useState<Record<string, OverrideState>>({})
  const [saving, setSaving] = useState<Record<string, boolean>>({})
  const debounceRefs = useRef<Record<string, ReturnType<typeof setTimeout>>>({})

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

  // Load current override values for test signals on mount
  const testSignals = (strategy.signal_configs ?? []).filter(sc => sc.name.endsWith('-test'))
  useEffect(() => {
    for (const sc of testSignals) {
      apiClient.get<{ active: boolean; value: number }>(`/admin/signal-override/${sc.name}`)
        .then(r => {
          setOverrides(prev => ({ ...prev, [sc.name]: { active: r.data.active, value: r.data.active ? r.data.value : 50 } }))
          if (r.data.active && sc.name === 'rsi-test') setRsiTestOverride(r.data.value)
        })
        .catch(() => {})
    }
    return () => {
      for (const sc of testSignals) {
        if (sc.name === 'rsi-test') setRsiTestOverride(null)
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [strategy.id])

  async function sendOverride(name: string, value: number | null) {
    setSaving(prev => ({ ...prev, [name]: true }))
    try {
      await apiClient.put(`/admin/signal-override/${name}`, { value })
    } finally {
      setSaving(prev => ({ ...prev, [name]: false }))
    }
  }

  function handleSlider(name: string, v: number) {
    setOverrides(prev => ({ ...prev, [name]: { ...prev[name], value: v } }))
    if (name === 'rsi-test') setRsiTestOverride(v)
    if (debounceRefs.current[name]) clearTimeout(debounceRefs.current[name])
    debounceRefs.current[name] = setTimeout(() => sendOverride(name, v), 200)
  }

  async function handleToggle(name: string) {
    const cur = overrides[name] ?? { active: false, value: 50 }
    const nextActive = !cur.active
    setOverrides(prev => ({ ...prev, [name]: { ...cur, active: nextActive } }))
    const nextValue = nextActive ? cur.value : null
    if (name === 'rsi-test') setRsiTestOverride(nextValue)
    await sendOverride(name, nextValue)
  }

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
  // Per-level SL orders for matrix strategies use "-msl-" prefix (not cycle-num-based)
  const mslPrefix   = `SIS_STR-${strategy.id.slice(0, 8)}-msl-`

  const cycleOrders = cyclePrefix
    ? orders.filter(o => {
        const lk = o.orderLinkId ?? ''
        return lk.startsWith(cyclePrefix) || (tpPrefix && lk.startsWith(tpPrefix)) || (slPrefix && lk.startsWith(slPrefix)) || lk.startsWith(mslPrefix)
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

      {/* Signal panel */}
      {strategy.signal_configs && strategy.signal_configs.length > 0 && (() => {
        const sigState  = liveSignal?.signal_state ?? state?.signal_state ?? ''
        const sigValues = liveSignal?.signal_values ?? state?.signal_values ?? {}
        const stateColor = sigState === 'buy' ? '#5be0a0' : sigState === 'sell' ? '#fca5a5' : '#8b9ab8'
        const stateBg    = sigState === 'buy' ? 'rgba(65,210,139,.08)' : sigState === 'sell' ? 'rgba(248,113,113,.08)' : 'rgba(91,100,140,.08)'

        return (
          <div className="shrink-0 border-b" style={{ borderColor: '#2e2e48', background: stateBg }}>
            {/* state header */}
            <div className="flex items-center gap-3 px-4 py-1.5">
              <span className="text-[10px] text-slate-500 uppercase tracking-wider font-semibold">Сигнал</span>
              <span className="font-mono font-bold text-[12px]" style={{ color: stateColor }}>
                {sigState || '—'}
              </span>
              {strategy.signal_configs.filter(sc => !sc.name.endsWith('-test')).map(sc => {
                const val = sigValues[sc.name]
                return (
                  <span key={sc.name} className="inline-flex items-center gap-1 ml-2">
                    <span className="text-[10px] text-slate-500 font-mono">{sc.name}</span>
                    <span className="text-[11px] font-mono font-semibold text-slate-200">
                      {val != null ? val.toFixed(1) : '—'}
                    </span>
                  </span>
                )
              })}
            </div>

            {/* sliders for test signals */}
            {testSignals.map(sc => {
              const ov = overrides[sc.name] ?? { active: false, value: 50 }
              const val = ov.value
              const isSaving = saving[sc.name] ?? false
              const liveVal = sigValues[sc.name]
              const dispVal = ov.active ? val : (liveVal ?? val)
              const threshold = (sc.params?.threshold as number) ?? 30
              const stLabel = dispVal < threshold ? { label: 'Buy', color: '#5be0a0' }
                            :                       { label: 'Neutral', color: '#8babff' }

              return (
                <div key={sc.name} className="px-4 pb-2.5 flex flex-col gap-1.5">
                  <div className="flex items-center gap-2">
                    <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-[.5px] font-mono">{sc.name}</span>
                    <button
                      type="button"
                      onClick={() => handleToggle(sc.name)}
                      disabled={isSaving}
                      className={`text-[9px] font-bold px-1.5 py-[2px] rounded-[4px] transition-colors disabled:opacity-50 ${
                        ov.active
                          ? 'bg-amber-400/20 border border-amber-400/40 text-amber-300'
                          : 'bg-white/[.05] border border-white/[.10] text-slate-500'
                      }`}
                    >
                      {ov.active ? 'ON' : 'OFF'}
                    </button>
                    <span className="font-mono text-[12px] font-bold ml-auto" style={{ color: stLabel.color }}>
                      {dispVal.toFixed(1)}
                    </span>
                    <span className="text-[10px] font-semibold w-12 text-right" style={{ color: stLabel.color }}>
                      {stLabel.label}
                    </span>
                  </div>
                  <input
                    type="range" min={0} max={100} step={0.5}
                    value={ov.active ? val : (liveVal ?? val)}
                    disabled={!ov.active}
                    onChange={e => handleSlider(sc.name, Number(e.target.value))}
                    className="w-full h-1.5 rounded-full appearance-none cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                    style={{ accentColor: stLabel.color }}
                  />
                  <div className="flex justify-between text-[9px] text-slate-600">
                    <span>0 · Buy</span>
                    <span style={{ color: '#5be0a0' }}>{threshold}</span>
                    <span>100 · Neutral</span>
                  </div>
                </div>
              )
            })}
          </div>
        )
      })()}

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
                  const slOnExch = l.sl_order_id ? !!exchById[l.sl_order_id] : null
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
                        <div className="flex flex-col gap-0.5">
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
                          {l.sl_order_id && (
                            <span
                              className={`text-[9px] ${slOnExch ? 'text-yellow-400' : 'text-amber-400'}`}
                              title={`SL: ${l.sl_order_id}`}
                            >
                              SL:{shortId(l.sl_order_id)}
                              {!slOnExch && <span className="ml-0.5 text-amber-500">⚠нет</span>}
                            </span>
                          )}
                        </div>
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
                  const labelColor = label === 'TP' ? 'text-blue-400' : label === 'SL' || label.startsWith('SL_L') ? 'text-yellow-400' : 'text-slate-200'
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
