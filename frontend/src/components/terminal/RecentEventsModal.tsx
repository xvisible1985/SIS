import { useState, useEffect, useRef, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { apiClient } from '../../api/client'

interface RecentEvent {
  created_at: string
  level: string
  source: string | null
  message: string
  symbol: string
  strategy_type: string
  strategy_name: string
}

// ── Source badge ──────────────────────────────────────────────────────────────

const SOURCE_STYLES: Record<string, { label: string; bg: string; text: string; border: string }> = {
  'strategy-runner':    { label: 'runner',        bg: '#1e3a5f', text: '#93c5fd', border: '#3b82f6' },
  'hedge-engine':       { label: 'hedge',          bg: '#2e1a4a', text: '#c4b5fd', border: '#8b5cf6' },
  'api':                { label: 'api',            bg: '#1e2535', text: '#94a3b8', border: '#475569' },
  'reconcile':          { label: 'reconcile',      bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'sweep-orphans':      { label: 'sweep',          bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'reprice-from-fills': { label: 'reprice-fills',  bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'reprice-stale':      { label: 'reprice-stale',  bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'trim-excess':        { label: 'trim-excess',    bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'reset-cancelled':    { label: 'reset-cancel',   bg: '#0f3040', text: '#67e8f9', border: '#06b6d4' },
  'cancel-all':         { label: 'cancel-all',     bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'cancel-levels':      { label: 'cancel-lvls',    bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'cancel-all-placed':  { label: 'cancel-placed',  bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'stop-request':       { label: 'stop-req',       bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'tp-sl-cancelled':    { label: 'tp-sl-cancel',   bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'level-cancelled':    { label: 'lvl-cancel',     bg: '#3b1212', text: '#fca5a5', border: '#ef4444' },
  'update-tp':          { label: 'update-tp',      bg: '#0d3321', text: '#6ee7b7', border: '#10b981' },
  'update-sl':          { label: 'update-sl',      bg: '#0d3321', text: '#6ee7b7', border: '#10b981' },
  'trailing-stop':      { label: 'trailing',       bg: '#0d3321', text: '#6ee7b7', border: '#10b981' },
  'restart-cycle':      { label: 'restart',        bg: '#3b2800', text: '#fcd34d', border: '#f59e0b' },
  'restart-matrix':     { label: 'restart-mtx',    bg: '#3b2800', text: '#fcd34d', border: '#f59e0b' },
  'restart-grid':       { label: 'restart-grid',   bg: '#3b2800', text: '#fcd34d', border: '#f59e0b' },
  'matrix-replace-slots': { label: 'mtx-slots',    bg: '#2d1a4a', text: '#d8b4fe', border: '#a855f7' },
  'matrix-update-tp':     { label: 'mtx-tp',       bg: '#2d1a4a', text: '#d8b4fe', border: '#a855f7' },
  'matrix-reprice':       { label: 'mtx-reprice',  bg: '#2d1a4a', text: '#d8b4fe', border: '#a855f7' },
  'matrix-cancel-sls':    { label: 'mtx-sls',      bg: '#2d1a4a', text: '#d8b4fe', border: '#a855f7' },
}

const LEVEL_STYLE: Record<string, { dot: string; row: string }> = {
  error: { dot: '#ef4444', row: 'rgba(239,68,68,.06)'   },
  warn:  { dot: '#f59e0b', row: 'rgba(245,158,11,.04)'  },
  info:  { dot: '#475569', row: 'transparent'            },
}

function SourceBadge({ source }: { source: string }) {
  const s = SOURCE_STYLES[source]
  return (
    <span style={{
      fontSize: 9, padding: '1px 5px', borderRadius: 3,
      border: `1px solid ${s?.border ?? '#475569'}`,
      background: s?.bg ?? '#1e2535',
      color: s?.text ?? '#94a3b8',
      whiteSpace: 'nowrap', flexShrink: 0,
    }}>
      {s?.label ?? source}
    </span>
  )
}

function fmtTime(iso: string) {
  const d = new Date(iso)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

function fmtDate(iso: string) {
  const d = new Date(iso)
  return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
}

// ── Main modal ────────────────────────────────────────────────────────────────

interface Props {
  onClose: () => void
}

type LevelFilter = 'all' | 'error' | 'warn' | 'info'

export function RecentEventsModal({ onClose }: Props) {
  const [events, setEvents]         = useState<RecentEvent[]>([])
  const [loading, setLoading]       = useState(true)
  const [levelFilter, setLevelFilter] = useState<LevelFilter>('all')
  const [sourceFilter, setSourceFilter] = useState('')
  const [minutes, setMinutes]       = useState(30)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)

  // Drag state
  const [pos, setPos] = useState({ x: Math.max(0, (window.innerWidth - 860) / 2), y: 60 })
  const dragging = useRef(false)
  const dragOffset = useRef({ x: 0, y: 0 })
  const modalRef = useRef<HTMLDivElement>(null)

  const load = useCallback(async () => {
    try {
      const res = await apiClient.get<RecentEvent[]>(`/events/recent?minutes=${minutes}`)
      setEvents(Array.isArray(res.data) ? res.data : [])
      setLastUpdated(new Date())
    } catch { /* silent */ }
    finally { setLoading(false) }
  }, [minutes])

  useEffect(() => {
    setLoading(true)
    load()
    const id = setInterval(load, 20_000)
    return () => clearInterval(id)
  }, [load])

  // Drag handlers
  const onMouseDown = useCallback((e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('button')) return
    dragging.current = true
    dragOffset.current = { x: e.clientX - pos.x, y: e.clientY - pos.y }
    e.preventDefault()
  }, [pos])

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragging.current) return
      setPos({
        x: Math.max(0, Math.min(window.innerWidth  - (modalRef.current?.offsetWidth  ?? 860), e.clientX - dragOffset.current.x)),
        y: Math.max(0, Math.min(window.innerHeight - (modalRef.current?.offsetHeight ?? 500), e.clientY - dragOffset.current.y)),
      })
    }
    function onUp() { dragging.current = false }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup',   onUp)
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [])

  // Filter
  const filtered = events.filter(e => {
    if (levelFilter !== 'all' && e.level !== levelFilter) return false
    if (sourceFilter && !(e.source ?? '').includes(sourceFilter) && !e.symbol.toLowerCase().includes(sourceFilter.toLowerCase())) return false
    return true
  })

  const modal = (
    <div
      ref={modalRef}
      style={{
        position: 'fixed', left: pos.x, top: pos.y, zIndex: 9998,
        width: 860, maxWidth: 'calc(100vw - 24px)',
        maxHeight: 'calc(100vh - 80px)',
        background: '#0d0f18',
        border: '1px solid rgba(255,255,255,.12)',
        borderRadius: 12,
        boxShadow: '0 32px 80px rgba(0,0,0,.9)',
        display: 'flex', flexDirection: 'column',
        userSelect: dragging.current ? 'none' : 'text',
      }}
    >
      {/* Header — drag handle */}
      <div
        onMouseDown={onMouseDown}
        style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '10px 14px', cursor: 'grab',
          borderBottom: '1px solid rgba(255,255,255,.07)',
          flexShrink: 0,
        }}
      >
        <span style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.06em', textTransform: 'uppercase', color: '#94a3b8' }}>
          Лог событий
        </span>
        {lastUpdated && (
          <span style={{ fontSize: 10, color: '#475569' }}>
            обновлено {fmtTime(lastUpdated.toISOString())}
          </span>
        )}
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 8 }}>
          {/* Minutes selector */}
          {([15, 30, 60, 120] as const).map(m => (
            <button
              key={m}
              onClick={() => setMinutes(m)}
              style={{
                fontSize: 10, padding: '2px 7px', borderRadius: 4, cursor: 'pointer',
                border: `1px solid ${minutes === m ? 'rgba(74,125,255,.5)' : 'rgba(255,255,255,.1)'}`,
                background: minutes === m ? 'rgba(74,125,255,.18)' : 'rgba(255,255,255,.04)',
                color: minutes === m ? '#b8c8ff' : '#64748b',
              }}
            >
              {m < 60 ? `${m}м` : `${m/60}ч`}
            </button>
          ))}
          <button
            onClick={onClose}
            style={{ fontSize: 16, lineHeight: 1, background: 'none', border: 'none', color: '#64748b', cursor: 'pointer', padding: '0 2px' }}
          >×</button>
        </div>
      </div>

      {/* Filter bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 14px', borderBottom: '1px solid rgba(255,255,255,.05)', flexShrink: 0 }}>
        {(['all', 'error', 'warn', 'info'] as LevelFilter[]).map(lv => {
          const colors: Record<LevelFilter, string> = { all: '#94a3b8', error: '#ef4444', warn: '#f59e0b', info: '#475569' }
          const active = levelFilter === lv
          return (
            <button
              key={lv}
              onClick={() => setLevelFilter(lv)}
              style={{
                fontSize: 10, padding: '2px 9px', borderRadius: 4, cursor: 'pointer',
                border: `1px solid ${active ? colors[lv] + '88' : 'rgba(255,255,255,.08)'}`,
                background: active ? colors[lv] + '22' : 'rgba(255,255,255,.03)',
                color: active ? colors[lv] : '#475569',
                fontWeight: active ? 700 : 400,
              }}
            >
              {lv === 'all' ? 'Все' : lv}
            </button>
          )
        })}
        <input
          value={sourceFilter}
          onChange={e => setSourceFilter(e.target.value)}
          placeholder="фильтр: symbol или source…"
          style={{
            marginLeft: 4, flex: 1, fontSize: 10, padding: '3px 8px',
            background: 'rgba(255,255,255,.04)', border: '1px solid rgba(255,255,255,.08)',
            borderRadius: 5, color: '#94a3b8', outline: 'none',
          }}
        />
        <span style={{ fontSize: 10, color: '#334155', whiteSpace: 'nowrap' }}>
          {filtered.length} / {events.length}
        </span>
      </div>

      {/* Table */}
      <div style={{ flex: 1, overflowY: 'auto', fontFamily: 'monospace' }}>
        {loading ? (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 120, color: '#334155', fontSize: 12 }}>
            Загрузка…
          </div>
        ) : filtered.length === 0 ? (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: 120, color: '#334155', fontSize: 12 }}>
            Нет событий
          </div>
        ) : filtered.map((ev, i) => {
          const ls = LEVEL_STYLE[ev.level] ?? LEVEL_STYLE.info
          const prevDate = i > 0 ? fmtDate(filtered[i-1].created_at) : null
          const thisDate = fmtDate(ev.created_at)
          const showDateSep = prevDate !== thisDate

          return (
            <div key={i}>
              {showDateSep && (
                <div style={{ fontSize: 9, color: '#334155', padding: '4px 14px 2px', textAlign: 'center', letterSpacing: '0.08em' }}>
                  ── {thisDate} ──
                </div>
              )}
              <div style={{
                display: 'flex', alignItems: 'baseline', gap: 8,
                padding: '4px 14px',
                background: ls.row,
                borderBottom: '1px solid rgba(255,255,255,.025)',
              }}>
                {/* dot */}
                <span style={{ width: 6, height: 6, borderRadius: '50%', background: ls.dot, flexShrink: 0, marginTop: 3, alignSelf: 'flex-start' }} />

                {/* time */}
                <span style={{ fontSize: 10, color: '#475569', whiteSpace: 'nowrap', flexShrink: 0, minWidth: 52 }}>
                  {fmtTime(ev.created_at)}
                </span>

                {/* symbol */}
                <span style={{ fontSize: 10, color: '#64748b', whiteSpace: 'nowrap', flexShrink: 0, minWidth: 72 }}>
                  {ev.symbol}
                </span>

                {/* source badge */}
                {ev.source && <SourceBadge source={ev.source} />}

                {/* message */}
                <span style={{ fontSize: 11, color: ev.level === 'error' ? '#fca5a5' : ev.level === 'warn' ? '#fcd34d' : '#94a3b8', lineHeight: 1.45, flex: 1, wordBreak: 'break-word' }}>
                  {ev.message}
                </span>
              </div>
            </div>
          )
        })}
      </div>

      {/* Footer */}
      <div style={{ padding: '6px 14px', borderTop: '1px solid rgba(255,255,255,.05)', display: 'flex', justifyContent: 'space-between', flexShrink: 0 }}>
        <span style={{ fontSize: 9, color: '#1e293b', letterSpacing: '0.06em' }}>
          ОБНОВЛЕНИЕ КАЖДЫЕ 20С • MAX 500 СТРОК
        </span>
        <button
          onClick={load}
          style={{ fontSize: 9, background: 'none', border: 'none', color: '#334155', cursor: 'pointer', padding: 0 }}
        >
          ↺ обновить
        </button>
      </div>
    </div>
  )

  return createPortal(modal, document.body)
}
