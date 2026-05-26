import { useState, useEffect, useRef, useCallback } from 'react'
import { getStrategyState, getStrategyEvents } from '../../api/strategies'
import type { Strategy, StrategyState, StrategyEvent, StrategyLevel } from '../../types'

// ── helpers ─────────────────────────────────────────────────────────────────

const STATUS_COLOR: Record<string, string> = {
  pending:  'bg-gray-600 text-gray-300',
  placed:   'bg-blue-700 text-blue-200',
  filled:   'bg-emerald-700 text-emerald-200',
  sl_closed:'bg-rose-800 text-rose-300',
  cancelled:'bg-gray-700 text-gray-500',
}
const STATUS_DOT: Record<string, string> = {
  pending:  'bg-gray-500',
  placed:   'bg-blue-400',
  filled:   'bg-emerald-400',
  sl_closed:'bg-rose-400',
  cancelled:'bg-gray-600',
}
const EVENT_COLOR: Record<string, string> = {
  info:  'text-gray-300',
  warn:  'text-amber-400',
  error: 'text-rose-400',
}

function fmt(n: number | undefined | null, d = 4) {
  if (!n) return '—'
  return n.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d })
}
function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// ── slot grid bar ────────────────────────────────────────────────────────────

function SlotBar({ levels }: { levels: StrategyLevel[] }) {
  const slotLevels = levels.filter(l => l.slot != null)
  if (slotLevels.length === 0) return null

  const slots = slotLevels.map(l => l.slot as number)
  const minSlot = Math.min(...slots)
  const maxSlot = Math.max(...slots)
  const range = Array.from({ length: maxSlot - minSlot + 1 }, (_, i) => minSlot + i)

  return (
    <div className="flex items-center gap-1 flex-wrap">
      {range.map(slot => {
        // prefer filled/sl_closed over placed over pending (latest status wins)
        const lvl = slotLevels
          .filter(l => l.slot === slot)
          .sort((a, b) => {
            const order = { sl_closed: 4, filled: 3, placed: 2, pending: 1, cancelled: 0 }
            return (order[b.status] ?? 0) - (order[a.status] ?? 0)
          })[0]
        if (!lvl) return <div key={slot} className="w-8 h-8 rounded border border-gray-700 bg-gray-800 flex items-center justify-center text-[10px] text-gray-600">{slot}</div>
        return (
          <div
            key={slot}
            title={`Slot ${slot}: ${lvl.status} @ ${fmt(lvl.target_price || lvl.filled_price)}`}
            className={`w-8 h-8 rounded flex flex-col items-center justify-center text-[9px] font-bold border border-white/10 cursor-default ${STATUS_COLOR[lvl.status] ?? 'bg-gray-700'}`}
          >
            <span>{slot > 0 ? `+${slot}` : slot}</span>
            <span className="text-[7px] opacity-70">{lvl.status === 'sl_closed' ? 'SL' : lvl.status.slice(0,1).toUpperCase()}</span>
          </div>
        )
      })}
    </div>
  )
}

// ── level table ──────────────────────────────────────────────────────────────

function LevelTable({ levels }: { levels: StrategyLevel[] }) {
  const isMatrix = levels.some(l => l.slot != null)
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs border-collapse">
        <thead>
          <tr className="text-gray-500 border-b border-gray-700">
            {isMatrix && <th className="text-left py-1 pr-2">Slot</th>}
            <th className="text-left py-1 pr-2">Idx</th>
            <th className="text-left py-1 pr-2">Status</th>
            <th className="text-right py-1 pr-2">Target</th>
            <th className="text-right py-1 pr-2">Filled</th>
            {isMatrix && <th className="text-right py-1 pr-2">SL price</th>}
            {isMatrix && <th className="text-center py-1">Replaced</th>}
          </tr>
        </thead>
        <tbody>
          {levels.map((l, i) => (
            <tr key={i} className={`border-b border-gray-800 ${l.status === 'sl_closed' ? 'opacity-60' : ''}`}>
              {isMatrix && (
                <td className="py-1 pr-2 font-mono text-gray-400">
                  {l.slot != null ? (l.slot > 0 ? `+${l.slot}` : l.slot) : '—'}
                </td>
              )}
              <td className="py-1 pr-2 text-gray-500">{l.level_idx}</td>
              <td className="py-1 pr-2">
                <span className={`inline-flex items-center gap-1`}>
                  <span className={`w-1.5 h-1.5 rounded-full ${STATUS_DOT[l.status] ?? 'bg-gray-600'}`} />
                  <span className="text-gray-300">{l.status}</span>
                </span>
              </td>
              <td className="py-1 pr-2 text-right font-mono text-gray-300">
                {l.target_price ? fmt(l.target_price) : <span className="text-blue-400">MARKET</span>}
              </td>
              <td className="py-1 pr-2 text-right font-mono text-emerald-400">
                {l.filled_price ? fmt(l.filled_price) : '—'}
              </td>
              {isMatrix && (
                <td className="py-1 pr-2 text-right font-mono text-rose-400">
                  {l.sl_price ? fmt(l.sl_price) : '—'}
                </td>
              )}
              {isMatrix && (
                <td className="py-1 text-center text-xs">
                  {l.sl_replaced ? <span className="text-amber-400">✓</span> : <span className="text-gray-600">—</span>}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── event log ────────────────────────────────────────────────────────────────

function EventLog({ events }: { events: StrategyEvent[] }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight
  }, [events])

  return (
    <div ref={ref} className="h-40 overflow-y-auto font-mono text-[11px] space-y-0.5 pr-1">
      {events.length === 0 && <div className="text-gray-600 italic">нет событий</div>}
      {events.map((e, i) => (
        <div key={i} className="flex gap-2 leading-tight">
          <span className="text-gray-600 shrink-0">{fmtTime(e.created_at)}</span>
          <span className={EVENT_COLOR[e.level] ?? 'text-gray-300'}>{e.message}</span>
        </div>
      ))}
    </div>
  )
}

// ── main overlay ─────────────────────────────────────────────────────────────

interface Props {
  strategies: Strategy[]
  onClose: () => void
}

export function MatrixDebugOverlay({ strategies, onClose }: Props) {
  const dcaStrategies = strategies.filter(s => s.strategy_type === 'matrix')
  const [selectedId, setSelectedId] = useState<string>(dcaStrategies[0]?.id ?? '')
  const [state, setState] = useState<StrategyState | null>(null)
  const [events, setEvents] = useState<StrategyEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const selectedStrategy = dcaStrategies.find(s => s.id === selectedId)

  const refresh = useCallback(async () => {
    if (!selectedId) return
    setLoading(true)
    setError('')
    try {
      const [st, ev] = await Promise.all([
        getStrategyState(selectedId),
        getStrategyEvents(selectedId, { limit: 30 }),
      ])
      setState(st)
      setEvents(ev.events.slice().reverse())
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [selectedId])

  // Initial load + auto-refresh
  useEffect(() => {
    refresh()
    if (autoRefresh) {
      timerRef.current = setInterval(refresh, 3000)
    }
    return () => { if (timerRef.current) clearInterval(timerRef.current) }
  }, [refresh, autoRefresh])

  const cycleLevels = state?.levels ?? []
  const sz = state?.safe_zone

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-end p-4 pointer-events-none">
      <div className="pointer-events-auto w-[680px] max-h-[90vh] flex flex-col rounded-xl border border-gray-700 bg-gray-900 shadow-2xl overflow-hidden">

        {/* header */}
        <div className="flex items-center justify-between px-4 py-2.5 border-b border-gray-700 bg-gray-800/80 shrink-0">
          <div className="flex items-center gap-2">
            <span className="text-xs font-semibold text-gray-200 tracking-wide uppercase">Matrix Debug</span>
            {loading && <span className="text-[10px] text-blue-400 animate-pulse">обновление…</span>}
          </div>
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-1.5 text-xs text-gray-400 cursor-pointer">
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={e => setAutoRefresh(e.target.checked)}
                className="accent-blue-500"
              />
              авто 3s
            </label>
            <button
              onClick={refresh}
              className="text-xs text-gray-400 hover:text-white px-2 py-0.5 rounded border border-gray-700 hover:border-gray-500"
            >↺</button>
            <button onClick={onClose} className="text-gray-500 hover:text-white text-lg leading-none">×</button>
          </div>
        </div>

        {/* strategy selector */}
        <div className="px-4 py-2 border-b border-gray-800 shrink-0">
          {dcaStrategies.length === 0 ? (
            <p className="text-xs text-gray-500">Нет DCA/Matrix стратегий</p>
          ) : (
            <select
              value={selectedId}
              onChange={e => setSelectedId(e.target.value)}
              className="w-full bg-gray-800 border border-gray-700 rounded px-2 py-1 text-xs text-gray-200 focus:outline-none"
            >
              {dcaStrategies.map(s => (
                <option key={s.id} value={s.id}>
                  {s.symbol} {s.direction.toUpperCase()} — {s.status} — {s.grid_size_usdt} USDT
                </option>
              ))}
            </select>
          )}
        </div>

        {error && (
          <div className="px-4 py-2 text-xs text-rose-400 border-b border-gray-800 shrink-0">{error}</div>
        )}

        <div className="overflow-y-auto flex-1 divide-y divide-gray-800">

          {/* cycle header */}
          {state && (
            <div className="px-4 py-3 grid grid-cols-2 gap-x-6 gap-y-1 text-xs">
              <div className="flex justify-between">
                <span className="text-gray-500">Цикл</span>
                <span className="text-gray-200 font-mono">#{state.cycle_num}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Start price</span>
                <span className="text-gray-200 font-mono">{fmt(state.start_price)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Avg entry</span>
                <span className="text-emerald-400 font-mono">{fmt(state.avg_entry)}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Volume</span>
                <span className="text-gray-200 font-mono">{state.volume_usdt.toFixed(2)} USDT</span>
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Global TP</span>
                {state.tp_order_id
                  ? <span className="text-emerald-400 font-mono text-[10px] truncate max-w-[120px]">{state.tp_order_id.slice(-8)}</span>
                  : <span className="text-gray-600">—</span>}
              </div>
              <div className="flex justify-between">
                <span className="text-gray-500">Старт</span>
                <span className="text-gray-400 font-mono">{fmtTime(state.started_at)}</span>
              </div>
            </div>
          )}

          {/* safe zone */}
          {sz && (
            <div className="px-4 py-2 flex items-center gap-3 text-xs bg-amber-950/30">
              <span className="text-amber-400 font-semibold shrink-0">⚠ Safe Zone</span>
              <span className="text-amber-300 font-mono">{fmt(sz.low)} — {fmt(sz.high)}</span>
              <span className="text-amber-600 text-[10px]">новые ордера в зоне заблокированы</span>
            </div>
          )}

          {/* slot bar */}
          {cycleLevels.some(l => l.slot != null) && (
            <div className="px-4 py-3">
              <div className="text-[10px] text-gray-500 mb-2 uppercase tracking-wide">Слоты</div>
              <SlotBar levels={cycleLevels} />
              <div className="flex gap-3 mt-2 flex-wrap">
                {Object.entries(STATUS_DOT).map(([s, cls]) => (
                  <span key={s} className="flex items-center gap-1 text-[10px] text-gray-500">
                    <span className={`w-2 h-2 rounded-full ${cls}`} />
                    {s}
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* strategy config summary */}
          {selectedStrategy?.matrix_levels && selectedStrategy.matrix_levels.length > 0 && (
            <div className="px-4 py-2 text-xs">
              <div className="text-[10px] text-gray-500 mb-1.5 uppercase tracking-wide">Конфиг уровней</div>
              <div className="flex gap-2 flex-wrap">
                {selectedStrategy.matrix_entry_level && (
                  <div className="bg-gray-800 rounded px-2 py-1 text-[10px]">
                    <span className="text-gray-500">L(0) entry: </span>
                    <span className="text-gray-200">{selectedStrategy.matrix_entry_level.size_pct}%</span>
                    {selectedStrategy.matrix_entry_level.stop_pct != null && (
                      <span className="text-rose-400"> SL {selectedStrategy.matrix_entry_level.stop_pct}%</span>
                    )}
                    {selectedStrategy.matrix_entry_level.tp_pct != null && (
                      <span className="text-emerald-400"> TP {selectedStrategy.matrix_entry_level.tp_pct}%</span>
                    )}
                  </div>
                )}
                {selectedStrategy.matrix_levels.map((ml, i) => (
                  <div key={i} className="bg-gray-800 rounded px-2 py-1 text-[10px]">
                    <span className={ml.direction === 'above' ? 'text-blue-400' : 'text-orange-400'}>
                      {ml.direction === 'above' ? '↑' : '↓'} {ml.price_step_pct}%
                    </span>
                    <span className="text-gray-400"> {ml.size_pct}%</span>
                    {ml.stop_pct != null && <span className="text-rose-400"> SL{ml.stop_pct}%</span>}
                    {ml.tp_pct != null && <span className="text-emerald-400"> TP{ml.tp_pct}%</span>}
                    {ml.stop_cond_pct != null && <span className="text-amber-400"> cond{ml.stop_cond_pct}%</span>}
                  </div>
                ))}
                {selectedStrategy.safe_zone_pct != null && (
                  <div className="bg-amber-900/40 rounded px-2 py-1 text-[10px] text-amber-400">
                    Safe Zone ±{selectedStrategy.safe_zone_pct}%
                  </div>
                )}
              </div>
            </div>
          )}

          {/* level table */}
          <div className="px-4 py-3">
            <div className="text-[10px] text-gray-500 mb-2 uppercase tracking-wide">
              Уровни ({cycleLevels.length})
            </div>
            {cycleLevels.length > 0
              ? <LevelTable levels={cycleLevels} />
              : <div className="text-xs text-gray-600 italic">нет активного цикла</div>}
          </div>

          {/* event log */}
          <div className="px-4 py-3">
            <div className="text-[10px] text-gray-500 mb-2 uppercase tracking-wide">Лог событий</div>
            <EventLog events={events} />
          </div>

        </div>
      </div>
    </div>
  )
}
