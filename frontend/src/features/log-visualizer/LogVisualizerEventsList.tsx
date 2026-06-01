// frontend/src/features/log-visualizer/LogVisualizerEventsList.tsx

import { useEffect, useRef } from 'react'
import type { MergedEvent } from './types'

interface Props {
  events:       MergedEvent[]
  currentIndex: number           // -1 = before first event
  onJump:       (idx: number) => void
}

export function LogVisualizerEventsList({ events, currentIndex, onJump }: Props) {
  const listRef    = useRef<HTMLDivElement>(null)
  const activeRef  = useRef<HTMLButtonElement>(null)

  // Auto-scroll to current event
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [currentIndex])

  function fmtTime(tsMs: number) {
    return new Date(tsMs).toLocaleTimeString('ru-RU', {
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  }

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

      {/* List */}
      <div ref={listRef} className="flex-1 overflow-y-auto">
        {events.length === 0 ? (
          <div className="flex h-full items-center justify-center text-[11px] text-slate-600">
            Нет событий
          </div>
        ) : (
          events.map((ev, idx) => {
            const isCurrent = idx === currentIndex
            const isPast    = idx < currentIndex
            return (
              <button
                key={idx}
                ref={isCurrent ? activeRef : undefined}
                onClick={() => onJump(idx)}
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
                    <div className="text-[9px] text-slate-500 font-mono">{fmtTime(ev.tsMs)}</div>
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
