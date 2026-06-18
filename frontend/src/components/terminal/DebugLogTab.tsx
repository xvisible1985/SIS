import { useState } from 'react'
import { useDebugEventsWs } from '../../hooks/terminal/useDebugEventsWs'
import type { DebugEvent } from '../../hooks/terminal/useDebugEventsWs'

const LEVEL_TEXT: Record<string, string> = {
  info:  'text-slate-300',
  warn:  'text-amber-300',
  error: 'text-rose-400',
}

const LEVEL_ROW: Record<string, string> = {
  warn:  'bg-amber-500/[.06]',
  error: 'bg-rose-500/[.08]',
}

const LEVEL_BADGE: Record<string, string> = {
  info:  'text-slate-500',
  warn:  'text-amber-400',
  error: 'text-rose-400 font-bold',
}

const DIR_LABEL: Record<string, string> = {
  long:  'L',
  short: 'S',
}

export function DebugLogTab() {
  const { events, clear } = useDebugEventsWs(true)

  const [levelFilter, setLevelFilter] = useState<'' | 'info' | 'warn' | 'error'>('')
  const [sourceFilter, setSourceFilter] = useState<'' | 'strategy' | 'bot'>('')
  const [search, setSearch] = useState('')

  const filtered = events.filter(e => {
    if (levelFilter && e.level !== levelFilter) return false
    if (sourceFilter && e.source !== sourceFilter) return false
    if (search) {
      const q = search.toLowerCase()
      return (
        e.message.toLowerCase().includes(q) ||
        e.symbol.toLowerCase().includes(q) ||
        e.bot_name.toLowerCase().includes(q) ||
        e.source_id.includes(q) ||
        e.category.toLowerCase().includes(q)
      )
    }
    return true
  })

  const warnCount = events.filter(e => e.level === 'warn').length
  const errorCount = events.filter(e => e.level === 'error').length

  return (
    <div className="flex flex-col h-full font-mono text-[11px]">

      {/* Filter bar */}
      <div className="flex items-center gap-2 px-2 py-1 border-b border-white/[.06] flex-shrink-0 flex-wrap bg-black/10">
        {/* Level filter */}
        <div className="flex gap-0.5">
          {([['', 'Все'], ['info', 'Info'], ['warn', 'Warn'], ['error', 'Err']] as const).map(([v, l]) => (
            <button
              key={v}
              onClick={() => setLevelFilter(v)}
              className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                levelFilter === v
                  ? 'bg-white/10 text-white'
                  : v === 'warn' ? 'text-amber-600 hover:text-amber-400'
                  : v === 'error' ? 'text-rose-600 hover:text-rose-400'
                  : 'text-slate-500 hover:text-slate-300'
              }`}
            >{l}</button>
          ))}
        </div>

        <div className="h-3 w-px bg-white/[.08]" />

        {/* Source filter */}
        <div className="flex gap-0.5">
          {([['', 'Все'], ['strategy', 'Стр'], ['bot', 'Бот']] as const).map(([v, l]) => (
            <button
              key={v}
              onClick={() => setSourceFilter(v)}
              className={`px-2 py-0.5 rounded text-[10px] font-medium transition-colors ${
                sourceFilter === v ? 'bg-white/10 text-white' : 'text-slate-500 hover:text-slate-300'
              }`}
            >{l}</button>
          ))}
        </div>

        <div className="h-3 w-px bg-white/[.08]" />

        {/* Search */}
        <input
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Поиск..."
          className="w-28 rounded border border-white/[.08] bg-white/[.03] px-2 py-0.5 text-[10px] text-slate-300 placeholder-slate-600 outline-none focus:border-white/[.20]"
        />

        {/* Stats */}
        <div className="flex items-center gap-2 ml-auto">
          {errorCount > 0 && (
            <span className="text-[10px] text-rose-400 font-semibold">{errorCount} ошиб.</span>
          )}
          {warnCount > 0 && (
            <span className="text-[10px] text-amber-400">{warnCount} предупр.</span>
          )}
          <span className="text-[10px] text-slate-600">{filtered.length}</span>
          <button
            onClick={clear}
            className="px-2 py-0.5 rounded text-[10px] text-slate-500 hover:text-slate-300 border border-white/[.06] hover:border-white/[.14]"
          >Очистить</button>
        </div>
      </div>

      {/* Log lines — newest first */}
      <div className="flex-1 overflow-y-auto px-1 py-0.5">
        {filtered.length === 0 && (
          <div className="py-8 text-center text-[11px] text-slate-600">Нет событий</div>
        )}
        {[...filtered].reverse().map((e, i) => (
          <LogLine key={i} e={e} />
        ))}
      </div>
    </div>
  )
}

function LogLine({ e }: { e: DebugEvent }) {
  const ts = new Date(e.created_at)
  const timeStr = ts.toLocaleTimeString('ru', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  const dateStr = ts.toLocaleDateString('ru', { day: '2-digit', month: '2-digit' })
  const isToday = ts.toDateString() === new Date().toDateString()

  return (
    <div className={`flex gap-1.5 py-[1px] px-1 leading-[1.55] rounded ${LEVEL_ROW[e.level] ?? ''}`}>
      {/* Timestamp */}
      <span className="shrink-0 text-slate-600 tabular-nums">
        {!isToday && <span className="mr-1">{dateStr}</span>}
        {timeStr}
      </span>

      {/* Source badge */}
      {e.source === 'strategy' ? (
        <span className="shrink-0 text-blue-500/80 w-[58px] truncate" title={e.source_id}>{e.source_id}</span>
      ) : (
        <span className="shrink-0 text-purple-400/80 w-[58px] truncate" title={e.bot_name}>{e.bot_name || e.source_id}</span>
      )}

      {/* Symbol + direction */}
      <span className="shrink-0 w-[78px] truncate">
        {e.symbol ? (
          <>
            <span className="text-slate-300">{e.symbol.replace('USDT', '')}</span>
            {e.direction && <span className={`ml-0.5 text-[9px] font-bold ${e.direction === 'long' ? 'text-emerald-500' : 'text-rose-500'}`}>{DIR_LABEL[e.direction] ?? ''}</span>}
          </>
        ) : e.category ? (
          <span className="text-slate-600">{e.category}</span>
        ) : null}
      </span>

      {/* Level */}
      <span className={`shrink-0 w-[26px] uppercase text-[9px] pt-[1px] ${LEVEL_BADGE[e.level] ?? ''}`}>{e.level}</span>

      {/* Message */}
      <span className={LEVEL_TEXT[e.level] ?? 'text-slate-300'}>{e.message}</span>
    </div>
  )
}
