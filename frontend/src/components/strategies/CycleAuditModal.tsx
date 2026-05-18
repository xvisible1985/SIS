import { useState, useEffect, useCallback } from 'react'
import { getCycleAudit } from '../../api/strategies'
import type { CycleAuditData } from '../../types'

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
  }
  return (
    <span className={`text-[10px] px-1.5 py-[2px] rounded-[4px] font-mono font-semibold ${styles[status] ?? 'text-slate-400'}`}>
      {status}
    </span>
  )
}

export function CycleAuditModal({ strategyId, strategySymbol, onClose }: Props) {
  const [data, setData] = useState<CycleAuditData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)
  const [spinning, setSpinning] = useState(false)

  const fetchData = useCallback(async () => {
    setSpinning(true)
    try {
      const d = await getCycleAudit(strategyId)
      setData(d)
      setLastUpdated(new Date())
      setError(null)
    } catch (e: any) {
      setError(e?.message ?? 'ошибка')
    } finally {
      setSpinning(false)
      setLoading(false)
    }
  }, [strategyId])

  useEffect(() => {
    fetchData()
    const t = setInterval(fetchData, 4000)
    return () => clearInterval(t)
  }, [fetchData])

  function rowBg(flag: string) {
    if (flag === 'missing_fill' || flag === 'orphan') return 'bg-orange-500/[.06]'
    if (flag === 'cancelled' || flag === 'missing') return 'bg-red-500/[.06]'
    return ''
  }

  function liveCell(live: boolean, status: string) {
    if (status === 'pending' || status === 'filled') return <span className="text-slate-600">—</span>
    return live
      ? <span className="text-emerald-400">✓</span>
      : <span className="text-red-400">✗</span>
  }

  function indexCell(inIndex: boolean, status: string) {
    if (status === 'pending' || status === 'filled') return <span className="text-slate-600">—</span>
    return inIndex
      ? <span className="text-emerald-400">✓</span>
      : <span className="text-red-400">✗</span>
  }

  const pos = data?.position
  const delta = data && !data.no_active_cycle ? parseFloat(data.qty_discrepancy ?? '0') : 0
  const isNoCycle = data?.no_active_cycle

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center"
      style={{ background: 'rgba(0,0,0,.60)' }}
      onClick={onClose}
    >
      <div
        className="max-w-3xl w-full mx-4 flex flex-col rounded-[16px] border border-white/[.10]"
        style={{ background: '#0d1220', maxHeight: '85vh' }}
        onClick={e => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3 border-b border-white/[.08] shrink-0">
          <div className="flex items-center gap-3">
            <span className="text-[13px] font-semibold text-slate-200">
              Аудит цикла {data && !isNoCycle ? `#${data.cycle_num}` : ''} — {strategySymbol}
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
          <button
            onClick={onClose}
            className="w-6 h-6 flex items-center justify-center text-slate-500 hover:text-slate-200 text-[13px] rounded transition-colors"
          >
            ✕
          </button>
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
              </div>

              {/* Levels table */}
              <div className="rounded-[10px] border border-white/[.08] overflow-hidden">
                <table className="w-full text-[11px] font-mono">
                  <thead>
                    <tr className="border-b border-white/[.06]" style={{ background: 'rgba(255,255,255,.03)' }}>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">#</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Сторона</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена цели</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Кол-во</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Статус БД</th>
                      <th className="px-3 py-2 text-right text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Цена исп.</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Биржа</th>
                      <th className="px-3 py-2 text-center text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Память</th>
                      <th className="px-3 py-2 text-left text-[10px] text-slate-500 font-semibold uppercase tracking-wide">Флаг</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(data.levels ?? []).map(l => (
                      <tr key={l.idx} className={`border-b border-white/[.04] ${rowBg(l.flag)}`}>
                        <td className="px-3 py-2 text-slate-400">L{l.idx}</td>
                        <td className={`px-3 py-2 font-semibold ${l.side === 'Buy' ? 'text-emerald-400' : 'text-red-400'}`}>
                          {l.side}
                        </td>
                        <td className="px-3 py-2 text-right text-slate-200">{l.target_price.toFixed(2)}</td>
                        <td className="px-3 py-2 text-right text-slate-300">{l.qty}</td>
                        <td className="px-3 py-2 text-center"><DbStatusBadge status={l.db_status} /></td>
                        <td className="px-3 py-2 text-right text-slate-300">
                          {l.filled_price > 0 ? l.filled_price.toFixed(2) : '—'}
                        </td>
                        <td className="px-3 py-2 text-center">{liveCell(l.live_on_exchange, l.db_status)}</td>
                        <td className="px-3 py-2 text-center">{indexCell(l.in_order_index, l.db_status)}</td>
                        <td className="px-3 py-2"><FlagBadge flag={l.flag} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* TP/SL section */}
              {(data.tp || data.sl) && (
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
                          <tr key={label} className={`border-b border-white/[.04] ${rowBg(item.flag)}`}>
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
            </>
          )}
        </div>
      </div>
    </div>
  )
}
