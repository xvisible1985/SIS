// frontend/src/features/log-visualizer/LogVisualizerLayersPopup.tsx

import { useEffect, useRef, useState } from 'react'
import type { LayerSettings } from './types'

interface Props {
  settings: LayerSettings
  onChange: (s: LayerSettings) => void
}

function Toggle({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      className="flex-shrink-0 relative focus:outline-none"
      style={{ width: 28, height: 15 }}
    >
      <span
        style={{
          display: 'block',
          width: 28,
          height: 15,
          borderRadius: 8,
          background: on ? '#4a7dff' : 'rgba(255,255,255,.15)',
          transition: 'background .15s',
        }}
      />
      <span
        style={{
          position: 'absolute',
          top: 2,
          ...(on ? { right: 2 } : { left: 2 }),
          width: 11,
          height: 11,
          borderRadius: '50%',
          background: 'white',
          transition: 'left .15s, right .15s',
        }}
      />
    </button>
  )
}

const CHIP_COLORS = {
  info:  { text: '#94a3b8', bg: 'rgba(148,163,184,.15)', border: 'rgba(148,163,184,.3)' },
  warn:  { text: '#fbbf24', bg: 'rgba(251,191,36,.12)',  border: 'rgba(245,158,11,.3)'  },
  error: { text: '#f87171', bg: 'rgba(248,113,113,.12)', border: 'rgba(239,68,68,.3)'   },
}

const TOGGLE_ROWS: Array<{ key: keyof LayerSettings; label: string }> = [
  { key: 'showOrderMarkers', label: 'Маркеры ордеров' },
  { key: 'showLogMarkers',   label: 'Маркеры событий' },
  { key: 'showPriceLines',   label: 'Ценовые линии'   },
]

const LOG_LEVELS = [
  { lvl: 'info',  key: 'showInfo'  as const },
  { lvl: 'warn',  key: 'showWarn'  as const },
  { lvl: 'error', key: 'showError' as const },
]

export function LogVisualizerLayersPopup({ settings, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)

  // Close on outside click
  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  const patch = (update: Partial<LayerSettings>) => onChange({ ...settings, ...update })

  // Button is highlighted when any layer is hidden
  const isModified =
    !settings.showOrderMarkers || !settings.showLogMarkers || !settings.showPriceLines ||
    !settings.showInfo || !settings.showWarn || !settings.showError

  return (
    <div ref={containerRef} className="relative">
      {/* Toolbar button */}
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        title="Слои графика"
        className={`flex items-center gap-1 rounded px-2 py-1 text-xs border transition-colors focus:outline-none ${
          isModified
            ? 'border-[#4a7dff]/40 bg-[#4a7dff]/15 text-[#b8c8ff]'
            : 'border-white/[.08] bg-white/[.04] text-slate-400 hover:bg-white/[.07] hover:text-slate-200'
        }`}
      >
        {/* Layers icon */}
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <line x1="4" y1="6"  x2="20" y2="6"/>
          <line x1="4" y1="12" x2="20" y2="12"/>
          <line x1="4" y1="18" x2="20" y2="18"/>
          <circle cx="9"  cy="6"  r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="15" cy="12" r="2.5" fill="currentColor" stroke="none"/>
          <circle cx="7"  cy="18" r="2.5" fill="currentColor" stroke="none"/>
        </svg>
        Слои
      </button>

      {/* Dropdown popup */}
      {open && (
        <div
          className="absolute top-full mt-1 z-50"
          style={{
            background:   '#0d1220',
            border:       '1px solid rgba(255,255,255,.10)',
            borderRadius: 12,
            width:        220,
            overflow:     'hidden',
          }}
        >
          {/* Section: layer toggles */}
          <div style={{ padding: '10px 12px 4px', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 1, color: '#475569' }}>
            Слои графика
          </div>
          <div style={{ padding: '0 6px 6px' }}>
            {TOGGLE_ROWS.map(({ key, label }) => (
              <div key={key} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '5px 8px', borderRadius: 6 }}>
                <span style={{ fontSize: 12, color: '#e2e8f0' }}>{label}</span>
                <Toggle
                  on={settings[key] as boolean}
                  onToggle={() => patch({ [key]: !settings[key] })}
                />
              </div>
            ))}
          </div>

          {/* Divider */}
          <div style={{ borderTop: '1px solid rgba(255,255,255,.07)', margin: '0 12px' }} />

          {/* Section: log level chips */}
          <div style={{ padding: '4px 12px 4px', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: 1, color: '#475569' }}>
            Уровень лога
          </div>
          <div style={{ display: 'flex', gap: 4, padding: '6px 14px 10px' }}>
            {LOG_LEVELS.map(({ lvl, key }) => {
              const active   = settings[key]
              const disabled = !settings.showLogMarkers
              const c        = CHIP_COLORS[lvl as keyof typeof CHIP_COLORS]
              return (
                <button
                  key={lvl}
                  type="button"
                  disabled={disabled}
                  onClick={() => patch({ [key]: !active })}
                  style={{
                    padding:    '2px 8px',
                    borderRadius: 5,
                    fontSize:   11,
                    fontWeight: 600,
                    cursor:     disabled ? 'default' : 'pointer',
                    opacity:    disabled ? 0.35 : 1,
                    background: active && !disabled ? c.bg   : 'rgba(255,255,255,.04)',
                    color:      active && !disabled ? c.text : '#475569',
                    border:     `1px solid ${active && !disabled ? c.border : 'transparent'}`,
                    transition: 'all .15s',
                  }}
                >
                  {lvl}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
