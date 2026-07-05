// frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx

import { useEffect, useRef } from 'react'
import type { MergedEvent, EventListFilter } from './types'
import { filterEventsList } from './utils'

interface Props {
  events:         MergedEvent[]
  currentIndex:   number           // -1 = before first event
  onJump:         (idx: number) => void
  filter:         EventListFilter
  onFilterChange: (f: EventListFilter) => void
}

const SOURCE_STYLES: Record<string, { label: string; color: string }> = {
  // top-level sources
  'strategy-runner':    { label: 'runner',        color: 'bg-blue-900/20 border-blue-500/30 text-blue-300' },
  'hedge-engine':       { label: 'hedge',          color: 'bg-violet-900/20 border-violet-400/30 text-violet-300' },
  'api':                { label: 'api',            color: 'bg-slate-800/40 border-slate-500/30 text-slate-400' },
  // reconcile / maintenance
  'reconcile':          { label: 'reconcile',      color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  'sweep-orphans':      { label: 'sweep',          color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  'reprice-from-fills': { label: 'reprice-fills',  color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  'reprice-stale':      { label: 'reprice-stale',  color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  'trim-excess':        { label: 'trim-excess',    color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  'reset-cancelled':    { label: 'reset-cancel',   color: 'bg-cyan-900/20 border-cyan-500/30 text-cyan-300' },
  // cancel operations
  'cancel-all':         { label: 'cancel-all',     color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  'cancel-levels':      { label: 'cancel-lvls',    color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  'cancel-all-placed':  { label: 'cancel-placed',  color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  'stop-request':       { label: 'stop-req',       color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  'tp-sl-cancelled':    { label: 'tp-sl-cancel',   color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  'level-cancelled':    { label: 'lvl-cancel',     color: 'bg-red-900/20 border-red-500/30 text-red-300' },
  // TP/SL
  'update-tp':          { label: 'update-tp',      color: 'bg-emerald-900/20 border-emerald-500/30 text-emerald-300' },
  'update-sl':          { label: 'update-sl',      color: 'bg-emerald-900/20 border-emerald-500/30 text-emerald-300' },
  'trailing-stop':      { label: 'trailing',       color: 'bg-emerald-900/20 border-emerald-500/30 text-emerald-300' },
  // cycle restarts
  'restart-cycle':      { label: 'restart',        color: 'bg-amber-900/20 border-amber-500/30 text-amber-300' },
  'restart-matrix':     { label: 'restart-mtx',    color: 'bg-amber-900/20 border-amber-500/30 text-amber-300' },
  'restart-grid':       { label: 'restart-grid',   color: 'bg-amber-900/20 border-amber-500/30 text-amber-300' },
  // matrix
  'matrix-replace-slots': { label: 'mtx-slots',   color: 'bg-purple-900/20 border-purple-500/30 text-purple-300' },
  'matrix-update-tp':     { label: 'mtx-tp',      color: 'bg-purple-900/20 border-purple-500/30 text-purple-300' },
  'matrix-reprice':       { label: 'mtx-reprice',  color: 'bg-purple-900/20 border-purple-500/30 text-purple-300' },
  'matrix-cancel-sls':    { label: 'mtx-sls',     color: 'bg-purple-900/20 border-purple-500/30 text-purple-300' },
}

function SourceBadge({ source }: { source: string }) {
  const s = SOURCE_STYLES[source]
  const label = s?.label ?? source
  const color = s?.color ?? 'rgba(100,116,139,.15) border-slate-500/30 text-slate-400'
  return (
    <span
      style={{ fontSize: 8, padding: '1px 4px', borderRadius: 3, border: '1px solid', lineHeight: '1.4' }}
      className={color}
    >
      {label}
    </span>
  )
}

const CHIPS: { label: string; value: EventListFilter }[] = [
  { label: 'Все',      value: 'all'    },
  { label: 'Ордера',   value: 'orders' },
  { label: 'Закрытия', value: 'closes' },
  { label: 'Ошибки',   value: 'errors' },
]

export function LogVisualizerEventsList({ events, currentIndex, onJump, filter, onFilterChange }: Props) {
  const activeRef = useRef<HTMLButtonElement>(null)

  // Auto-scroll to current event
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [currentIndex])

  function fmtTime(tsMs: number) {
    const d    = new Date(tsMs)
    const date = d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
    const time = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    return `${date} ${time}`
  }

  const filtered = filterEventsList(events, filter)

  return (
    <div className="flex flex-col h-full overflow-hidden border-l border-white/[.06]">
      {/* Header */}
      <div className="flex-shrink-0 px-3 py-2 border-b border-white/[.06]">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-slate-400">
          События
          {events.length > 0 && (
            <span className="ml-2 font-mono text-slate-500">{currentIndex + 1}/{events.length}</span>
          )}
        </span>
      </div>

      {/* Filter chips */}
      <div className="flex-shrink-0 flex flex-wrap gap-1 px-3 py-1.5 border-b border-white/[.06]">
        {CHIPS.map(chip => {
          const active = chip.value === filter
          return (
            <button
              key={chip.value}
              onClick={() => onFilterChange(chip.value)}
              style={{
                fontSize:     10,
                padding:      '2px 8px',
                borderRadius: 5,
                border:       active ? '1px solid rgba(74,125,255,.3)' : '1px solid transparent',
                background:   active ? 'rgba(74,125,255,.15)' : 'rgba(255,255,255,.04)',
                color:        active ? '#b8c8ff' : '#475569',
                cursor:       'pointer',
              }}
            >
              {chip.label}
            </button>
          )
        })}
      </div>

      {/* List */}
      <div className="flex-1 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="flex h-full items-center justify-center text-[11px] text-slate-600">
            Нет событий
          </div>
        ) : (
          filtered.map(ev => {
            const originalIdx = events.indexOf(ev)
            const isCurrent   = originalIdx === currentIndex
            const isPast      = originalIdx < currentIndex
            return (
              <button
                key={originalIdx}
                ref={isCurrent ? activeRef : undefined}
                onClick={() => onJump(originalIdx)}
                className={`w-full text-left px-3 py-1.5 border-b border-white/[.03] transition-colors hover:bg-white/[.03] ${
                  isCurrent
                    ? 'bg-amber-400/[.08] border-l-2 border-l-amber-400'
                    : isPast
                    ? 'opacity-60'
                    : 'opacity-40'
                }`}
              >
                <div className="flex items-start gap-1.5">
                  {/* Dot */}
                  <span className={`mt-[3px] shrink-0 h-1.5 w-1.5 rounded-full ${
                    isCurrent ? 'bg-amber-400'
                    : ev.kind === 'level'
                      ? ev.level?.side === 'Buy' ? 'bg-emerald-500' : 'bg-rose-500'
                      : ev.log?.level === 'error' ? 'bg-rose-500'
                        : ev.log?.level === 'warn' ? 'bg-amber-500' : 'bg-slate-500'
                  }`} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-1.5">
                      <span className="text-[9px] text-slate-500 font-mono">{fmtTime(ev.tsMs)}</span>
                      {ev.kind === 'log' && ev.log?.source && (
                        <SourceBadge source={ev.log.source} />
                      )}
                    </div>
                    <div className={`text-[10px] leading-tight truncate ${
                      isCurrent ? 'text-amber-200' : 'text-slate-400'
                    }`}>
                      {ev.label}
                    </div>
                  </div>
                </div>
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}
