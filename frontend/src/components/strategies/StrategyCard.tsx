import { useState } from 'react'
import { getStrategyState, setStrategyStatus, deleteStrategy } from '../../api/strategies'
import { SignalMiniCard } from './SignalMiniCard'
import type { Strategy, StrategyState, ExchangeAccount } from '../../types'

interface Props {
  strategy: Strategy
  accounts: ExchangeAccount[]
  onEdit: (s: Strategy) => void
  onChanged: () => void
}

interface CardState {
  open: boolean
  state: StrategyState | null
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

export function StrategyCard({ strategy: s, accounts, onEdit, onChanged }: Props) {
  const [cs, setCs] = useState<CardState>({ open: false, state: null, loading: false, acting: false })

  const accountName = accounts.find(a => a.id === s.account_id)?.label ?? s.account_id.slice(0, 8)

  async function toggle() {
    if (!cs.open && !cs.state) {
      setCs(p => ({ ...p, open: true, loading: true }))
      try {
        const data = await getStrategyState(s.id)
        setCs(p => ({ ...p, state: data, loading: false }))
      } catch {
        setCs(p => ({ ...p, loading: false }))
      }
    } else {
      setCs(p => ({ ...p, open: !p.open }))
    }
  }

  async function handleStatus(status: 'active' | 'finishing' | 'stopped') {
    setCs(p => ({ ...p, acting: true }))
    try {
      await setStrategyStatus(s.id, status)
      onChanged()
    } finally {
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

  // Compute TP/SL display prices from strategy settings + state
  function tpPrice() {
    if (!cs.state || cs.state.avg_entry === 0) return null
    return s.direction === 'short'
      ? cs.state.avg_entry * (1 - s.tp_pct / 100)
      : cs.state.avg_entry * (1 + s.tp_pct / 100)
  }

  function slPrice() {
    if (!cs.state || cs.state.start_price === 0) return null
    return s.direction === 'short'
      ? cs.state.start_price * (1 + s.sl_pct / 100)
      : cs.state.start_price * (1 - s.sl_pct / 100)
  }

  const btnBase = 'px-3 py-1.5 text-xs rounded-lg transition-colors disabled:opacity-50'

  return (
    <li className="border border-gray-700 rounded-xl overflow-hidden">
      {/* Collapsed header */}
      <button
        onClick={toggle}
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
            <div className="text-[10px] text-gray-600">Grid</div>
            <div className="text-[11px] text-gray-300">{s.active_levels}/{s.grid_levels}</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-600">Объём</div>
            <div className="text-[11px] text-gray-300">{s.volume_usdt.toFixed(0)} USDT</div>
          </div>
          <div>
            <div className="text-[10px] text-gray-600">P&L</div>
            <div className={`text-[11px] font-mono ${pnlColor(s.last_pnl)}`}>
              {s.last_pnl === 0 ? '—' : `${s.last_pnl > 0 ? '+' : ''}${s.last_pnl.toFixed(2)}`}
            </div>
          </div>
          {statusBadge(s.status)}
          <svg
            className={`w-4 h-4 text-gray-500 transition-transform ${cs.open ? 'rotate-180' : ''}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {/* Expanded body */}
      {cs.open && (
        <div className="border-t border-gray-700 bg-gray-900/40">

          {cs.loading && (
            <div className="px-5 py-6 text-sm text-gray-500 text-center">Загрузка…</div>
          )}

          {!cs.loading && (
            <>
              {/* Signals */}
              {s.signal_configs && s.signal_configs.length > 0 && (
                <div className="px-5 py-3 border-b border-gray-800">
                  <div className="text-[10px] text-gray-600 uppercase tracking-wider mb-2">Сигналы</div>
                  <div className="flex gap-2 flex-wrap">
                    {s.signal_configs.map(sc => (
                      <SignalMiniCard key={sc.name} signal={sc} />
                    ))}
                  </div>
                </div>
              )}

              {/* Ждёт */}
              <div className="px-5 py-3 border-b border-gray-800">
                <div className="text-[10px] text-gray-600 uppercase tracking-wider mb-2">Ждёт</div>
                <div className="flex gap-4">
                  {/* Left: levels */}
                  <div className="flex-1 space-y-1.5">
                    <div className="text-[9px] text-gray-600 uppercase mb-1">Уровни</div>
                    {cs.state && cs.state.levels.length > 0 ? (
                      cs.state.levels.map(l => (
                        <div key={l.level_idx} className="flex items-center gap-2 text-[11px]">
                          <span className={
                            l.status === 'filled' ? 'text-green-400' :
                            l.status === 'placed' ? 'text-yellow-400' : 'text-gray-600'
                          }>
                            {l.status === 'filled' ? '✓' : l.status === 'placed' ? '◉' : '○'}
                          </span>
                          <span className="text-gray-400">
                            L{l.level_idx + 1} @ <span className="text-gray-200">{l.target_price.toFixed(2)}</span>
                          </span>
                          <span className={`ml-auto text-[9px] ${
                            l.status === 'filled' ? 'text-green-500' :
                            l.status === 'placed' ? 'text-yellow-500' : 'text-gray-600'
                          }`}>
                            {l.status === 'filled' ? 'заполнен' : l.status === 'placed' ? 'ожидает' : 'pending'}
                          </span>
                        </div>
                      ))
                    ) : (
                      <div className="text-[11px] text-gray-600">Нет активного цикла</div>
                    )}
                  </div>

                  {/* Divider */}
                  <div className="w-px bg-gray-800 shrink-0" />

                  {/* Right: TP/SL */}
                  <div className="w-44 space-y-2">
                    <div className="text-[9px] text-gray-600 uppercase mb-1">Выход</div>
                    <div className="border border-green-900/40 bg-green-950/20 rounded-lg px-3 py-2">
                      <div className="flex items-center justify-between mb-1">
                        <span className="text-[10px] text-green-400 font-semibold">Take Profit</span>
                        {cs.state?.tp_order_id
                          ? <span className="text-[9px] bg-green-900/40 text-green-500 px-1.5 py-0.5 rounded">выставлен</span>
                          : <span className="text-[9px] text-gray-600">—</span>
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
                        <span className="text-[9px] text-gray-600">{s.sl_type}</span>
                      </div>
                      <div className="font-mono text-gray-100 text-sm">
                        {slPrice() ? slPrice()!.toFixed(2) : '—'}
                      </div>
                      <div className="text-[10px] text-red-400">−{s.sl_pct}% от start</div>
                    </div>
                  </div>
                </div>
              </div>

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
      )}
    </li>
  )
}
