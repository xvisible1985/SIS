import { useState, useEffect, useCallback, useRef } from 'react'
import { getCycleAudit, restartCycle, getStrategyState } from '../../api/strategies'
import type { CycleAuditData, CycleAuditLevel } from '../../types'

interface Props {
  strategyId: string
  strategySymbol: string
  onClose: () => void
}

function FlagBadge({ flag }: { flag: string }) {
  if (flag === 'ok') return null
  const styles: Record<string, string> = {
    pending:      'bg-slate-500/20 text-slate-400 border-slate-500/30',
    missing_fill: 'bg-orange-500/20 text-orange-300 border-orange-500/30',
    cancelled:    'bg-red-500/20 text-red-300 border-red-500/30',
    orphan:       'bg-purple-500/20 text-purple-300 border-purple-500/30',
    missing:      'bg-red-500/20 text-red-300 border-red-500/30',
    not_placed:   'bg-slate-500/20 text-slate-400 border-slate-500/30',
  }
  const labels: Record<string, string> = {
    pending:      'ожидание',
    missing_fill: '⚠ пропущен fill',
    cancelled:    'отменён',
    orphan:       'orphan',
    missing:      'отсутствует',
    not_placed:   'не размещён',
  }
  const cls = styles[flag] ?? 'bg-slate-500/20 text-slate-400 border-slate-500/30'
  return (
    <span className={`text-[10px] px-1.5 py-[2px] rounded-[4px] border font-mono font-semibold ${cls}`}>
      {labels[flag] ?? flag}
    </span>
  )
}

function DbStatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    pending:   'bg-slate-500/20 text-slate-400',
    placed:    'bg-blue-500/20 text-blue-300',
    filled:    'bg-emerald-500/20 text-emerald-300',
    cancelled: 'bg-red-500/20 text-red-400',
    sl_closed: 'bg-rose-500/20 text-rose-300',
  }
  const labels: Record<string, string> = {
    sl_closed: 'sl_closed',
  }
  return (
    <span className={`text-[10px] px-1.5 py-[2px] rounded-[4px] font-mono font-semibold ${styles[status] ?? 'text-slate-400'}`}>
      {labels[status] ?? status}
    </span>
  )
}

function LiveCell({ live, status }: { live: boolean; status: string }) {
  if (status === 'pending' || status === 'filled' || status === 'sl_closed') {
    return <span className="text-slate-600">—</span>
  }
  return live
    ? <span className="text-emerald-400">✓</span>
    : <span className="text-red-400">✗</span>
}

function IndexCell({ inIndex, status }: { inIndex: boolean; status: string }) {
  if (status === 'pending' || status === 'filled' || status === 'sl_closed') {
    return <span className="text-slate-600">—</span>
  }
  return inIndex
    ? <span className="text-emerald-400">✓</span>
    : <span className="text-red-400">✗</span>
}

function SlStatusCell({ l }: { l: CycleAuditLevel }) {
  if (!l.sl_order_id) return <span className="text-slate-600">—</span>
  if (l.db_status === 'sl_closed') {
    return <span className="text-rose-300 text-[10px] font-semibold">закрыт</span>
  }
  const live = l.sl_live_on_exchange ?? false
  const idx  = l.sl_in_order_index ?? false
  return (
    <span className={`text-[10px] font-semibold ${live && idx ? 'text-emerald-400' : live ? 'text-yellow-400' : 'text-red-400'}`}>
      {live && idx ? '✓ live' : live ? 'orphan' : '✗ miss'}
    </span>
  )
}

export function CycleAuditModal({ strategyId, strategySymbol, onClose }: Props) {
  const [data, setData] = useState<CycleAuditData | null>(null)
  const [safeZone, setSafeZone] = useState<{ low: number; high: number } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [spinning, setSpinning] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [winPos, setWinPos] = useState({ left: Math.max(40, window.innerWidth / 2 - 460), top: 80 })

  const requestIdRef = useRef(0)
  const drag = useRef<{ mx: number; my: number; left: number; top: number } | null>(null)

  const fetchData = useCallback(async () => {
    const currentId = ++requestIdRef.current
    setSpinning(true)
    try {
      const [d, st] = await Promise.all([
        getCycleAudit(strategyId),
        getStrategyState(strategyId).catch(() => null),
      ])
      if (currentId !== requestIdRef.current) return
      setData(d)
      setSafeZone(st?.safe_zone ?? null)
      setLastUpdated(new Date())
      setError(null)
    } catch (e: any) {
      if (currentId !== requestIdRef.current) return
      setError(e?.message ?? 'ошибка')
    } finally {
      if (currentId === requestIdRef.current) {
        setSpinning(false)
        setLoading(false)
      }
    }
  }, [strategyId])

  useEffect(() => {
    fetchData()
    const t = setInterval(fetchData, 4000)
    return () => clearInterval(t)
  }, [fetchData])

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!drag.current) return
      setWinPos({ left: drag.current.left + e.clientX - drag.current.mx, top: drag.current.top + e.clientY - drag.current.my })
    }
    const onUp = () => { drag.current = null }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [])

  function onHeaderMouseDown(e: React.MouseEvent) {
    if ((e.target as HTMLElement).closest('button')) return
    drag.current = { mx: e.clientX, my: e.clientY, left: winPos.left, top: winPos.top }
  }

  function rowBg(flag: string, status: string) {
    if (status === 'sl_closed') return 'bg-rose-500/[.04]'
    if (flag === 'missing_fill' || flag === 'orphan') return 'bg-orange-500/[.06]'
    if (flag === 'cancelled' || flag === 'missing') return 'bg-red-500/[.06]'
    return ''
  }

  const pos = data?.position
  const delta = data && !data.no_active_cycle ? parseFloat(data.qty_discrepancy ?? '0') : 0
  const isNoCycle = data?.no_active_cycle
  const isMatrix = data?.strategy_type === 'matrix'

  return (
    <div
      className="flex flex-col rounded-[16px] border border-white/[.10] shadow-2xl"
      style={{ background: '#0d1220', maxHeight: '85vh', position: 'fixed', zIndex: 9998, width: isMatrix ? 1060 : 920, left: winPos.left, top: winPos.top }}
    >
      {/* Header */}
      <div
        className="flex items-center justify-between px-5 py-3 border-b border-white/[.08] shrink-0 cursor-move select-none"
        onMouseDown={onHeaderMouseDown}
      >
        <div className="flex items-center gap-3">
          <span className="text-[13px] font-semibold text-slate-200">
            {isMatrix ? 'Матрица — аудит цикла' : 'Аудит цикла'} {data && !isNoCycle ? `#${data.cycle_num}` : ''} — {strategySymbol}
          </span>
          {spinning && (
            <svg className="animate-spin w-3 h-3 text-blue-400" viewBox="0 0 24 24" fill="none">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.37 0 0 5.37 0 12h4z" />
            </svg>
          )}
          {lastUpdated && (
            <span className="text-[11px] text-slate-500">
              ↻ {lastUpdated.toLocaleTimeString('ru-RU')}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={async () => {
              if (restarting) return
              setRestarting(true)
              try { await restartCycle(strategyId) } finally { setRestarting(false) }
            }}
            disabled={restarting}
            className="px-2 py-1 rounded-[6px] text-[11px] font-semibold border border-orange-500/40 text-orange-300 hover:bg-orange-500/10 disabled:opacity-40 transition-colors"
          >
            {restarting ? '…' : 'Сбросить цикл'}
          </button>
          <button
            onClick={onClose}
            className="w-6 h-6 flex items-center justify-center text-slate-500 hover:text-slate-200 text-[13px] rounded transition-colors"
          >
            ✕
          </button>
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-auto p-5 flex flex-col gap-4">
        {loading && (
          <div className="text-center text-slate-500 text-[12px] py-10">Загрузка…</div>
        )}

        {error && !loading && (
          <div className="px-3 py-2 rounded-[8px] bg-red-500/10 border border-red-500/20 text-red-300 text-[12px]">
            {error}
          </div>
        )}

        {!loading && isNoCycle && (
          <div className="text-center text-slate-500 text-[12px] py-10">Нет активного цикла</div>
        )}

        {!loading && data && !isNoCycle && (
          <>
            {/* Summary row */}
            <div className="flex gap-2 flex-wrap">
              <span className="px-3 py-1.5 rounded-[8px] bg-white/[.04] border border-white/[.08] text-[12px] font-mono text-slate-200">
                Позиция: {pos ? `${pos.size} ${pos.side} @ ${pos.avg_entry}` : '—'}
              </span>
              <span className="px-3 py-1.5 rounded-[8px] bg-white/[.04] border border-white/[.08] text-[12px] font-mono text-slate-200">
                Ожидаемо из fills: {data.expected_qty_from_fills}
              </span>
              <span className={`px-3 py-1.5 rounded-[8px] bg-white/[.04] border text-[12px] font-mono font-semibold ${
                delta === 0
                  ? 'border-white/[.08] text-emerald-400'
                  : 'border-yellow-500/30 text-yellow-300'
              }`}>
                Дельта: {delta >= 0 ? '+' : ''}{data.qty_discrepancy}
              </span>
              {isMatrix && (
                <span className="px-3 py-1.5 rounded-[8px] border border-violet-500/30 bg-violet-500/10 text-[12px] font-semibold text-violet-300">
                  Матрица
                </span>
              )}
              {isMatrix && safeZone && (
                <span className="px-3 py-1.5 rounded-[8px] border border-amber-500/30 bg-amber-500/[.08] text-[12px] font-mono text-amber-300">
                  ⚠ Safe Zone: {safeZone.low.toFixed(4)} — {safeZone.high.toFixed(4)}
                </span>
              )}
            </div>

            {/* Levels table */}
            <div className="rounded-[10px] border border-white/[.08] overflow-hidden">
              <table className="w-full text-[11px] font-mono">
                <thead>
                  <tr className="border-b border-white/[.06]" style={{ background: 'rgba(255,255,255,.03)' }}>
                    <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">{isMatrix ? 'Уровень' : '#'}</th>
                    <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Сторона</th>
                    <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена цели</th>
                    <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Кол-во</th>
                    <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Статус БД</th>
                    <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена исп.</th>
                    {isMatrix ? (
                      <>
                        <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">SL цена</th>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">SL статус</th>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">SL замена</th>
                      </>
                    ) : (
                      <>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Биржа</th>
                        <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Память</th>
                      </>
                    )}
                    <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Флаг</th>
                  </tr>
                </thead>
                <tbody>
                  {(isMatrix
                    ? [...(data.levels ?? [])].sort((a, b) => (b.target_price ?? 0) - (a.target_price ?? 0))
                    : (data.levels ?? [])
                  ).map(l => (
                    <tr key={l.idx} className={`border-b border-white/[.04] ${rowBg(l.flag, l.db_status)}`}>
                      <td className="px-3 py-2">
                        {isMatrix
                          ? l.slot != null
                            ? <span className={`text-[10px] font-semibold px-1.5 py-[2px] rounded-[4px] ${
                                l.slot === 0 ? 'bg-violet-500/20 text-violet-300' :
                                l.slot < 0  ? 'bg-blue-500/20 text-blue-300' :
                                              'bg-amber-500/20 text-amber-300'
                              }`}>
                                L({l.slot})
                              </span>
                            : <span className="text-slate-600">—</span>
                          : <span className="text-slate-400">L{l.idx}</span>
                        }
                      </td>
                      <td className={`px-3 py-2 font-semibold ${l.side === 'Buy' ? 'text-emerald-400' : 'text-red-400'}`}>
                        {l.side}
                      </td>
                      <td className="px-3 py-2 text-right text-slate-200">{l.target_price.toFixed(4)}</td>
                      <td className="px-3 py-2 text-right text-slate-300">{l.qty}</td>
                      <td className="px-3 py-2 text-center"><DbStatusBadge status={l.db_status} /></td>
                      <td className="px-3 py-2 text-right text-slate-300">
                        {l.filled_price > 0 ? l.filled_price.toFixed(4) : '—'}
                      </td>
                      {isMatrix ? (
                        <>
                          <td className="px-3 py-2 text-right text-slate-300">
                            {(l.sl_price ?? 0) > 0 ? l.sl_price!.toFixed(4) : '—'}
                          </td>
                          <td className="px-3 py-2 text-center">
                            <SlStatusCell l={l} />
                          </td>
                          <td className="px-3 py-2 text-center">
                            {l.sl_replaced
                              ? <span className="text-[10px] font-semibold text-amber-400">заменён</span>
                              : <span className="text-slate-600">—</span>
                            }
                          </td>
                        </>
                      ) : (
                        <>
                          <td className="px-3 py-2 text-center">
                            <LiveCell live={l.live_on_exchange} status={l.db_status} />
                          </td>
                          <td className="px-3 py-2 text-center">
                            <IndexCell inIndex={l.in_order_index} status={l.db_status} />
                          </td>
                        </>
                      )}
                      <td className="px-3 py-2"><FlagBadge flag={l.flag} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* TP/SL section — only for non-matrix strategies */}
            {!isMatrix && (data.tp || data.sl) && (
              <div className="rounded-[10px] border border-white/[.08] overflow-hidden">
                <table className="w-full text-[11px] font-mono">
                  <thead>
                    <tr className="border-b border-white/[.06]" style={{ background: 'rgba(255,255,255,.03)' }}>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Тип</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">ID ордера</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Биржа</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Память</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Флаг</th>
                    </tr>
                  </thead>
                  <tbody>
                    {[{ label: 'TP', item: data.tp }, { label: 'SL', item: data.sl }].map(({ label, item }) => {
                      if (!item) return null
                      return (
                        <tr key={label} className={`border-b border-white/[.04] ${rowBg(item.flag, '')}`}>
                          <td className={`px-3 py-2 font-semibold ${label === 'TP' ? 'text-emerald-400' : 'text-rose-400'}`}>
                            {label}
                          </td>
                          <td className="px-3 py-2 text-slate-400">
                            {item.order_id ? item.order_id.slice(0, 12) + '…' : '—'}
                          </td>
                          <td className="px-3 py-2 text-center">
                            {item.order_id
                              ? item.live_on_exchange
                                ? <span className="text-emerald-400">✓</span>
                                : <span className="text-red-400">✗</span>
                              : <span className="text-slate-600">—</span>
                            }
                          </td>
                          <td className="px-3 py-2 text-center">
                            {item.order_id
                              ? item.in_order_index
                                ? <span className="text-emerald-400">✓</span>
                                : <span className="text-red-400">✗</span>
                              : <span className="text-slate-600">—</span>
                            }
                          </td>
                          <td className="px-3 py-2"><FlagBadge flag={item.flag} /></td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}

            {/* Matrix: per-level SL summary */}
            {isMatrix && (
              <div className="text-[11px] text-slate-500 px-1">
                SL — индивидуальный для каждого уровня. Нет глобального TP/SL для матрикс-стратегии.
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
