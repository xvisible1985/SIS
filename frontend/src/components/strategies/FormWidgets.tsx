import React, { useState, useEffect, useRef } from 'react'
import ReactDOM from 'react-dom'
import { SIGNALS } from '../../features/indicators/signals'
import { INDICATORS } from '../../features/indicators/indicators'
import type { SignalConfig } from '../../types'
import { apiClient } from '../../api/client'

// ─── useAllowedSignalIds ────────────────────────────────────────────────────

export function useAllowedSignalIds(): { ids: Set<string> | null; loading: boolean } {
  const [ids, setIds] = useState<Set<string> | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    apiClient.get<{ id: string; status: string }[]>('/admin/signal-types')
      .then(res => {
        setIds(new Set(res.data.filter(s => s.status !== 'disabled').map(s => s.id)))
        setLoading(false)
      })
      .catch(() =>
        apiClient.get<{ id: string }[]>('/signal-types')
          .then(res => { setIds(new Set(res.data.map(s => s.id))) })
          .catch(() => setIds(null))
          .finally(() => setLoading(false))
      )
  }, [])

  return { ids, loading }
}

// ─── Toggle ─────────────────────────────────────────────────────────────────

export function Toggle({ options, value, onChange, optionColors }: {
  options: { label: string; value: string }[]
  value: string
  onChange: (v: string) => void
  optionColors?: Record<string, string>
}) {
  return (
    <div className="flex gap-1">
      {options.map(o => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          className={`flex-1 text-center rounded-md py-1.5 text-xs transition-colors ${
            value === o.value
              ? (optionColors?.[o.value] ?? 'bg-blue-700 text-white')
              : 'bg-gray-800 text-gray-400 hover:bg-gray-700'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

// ─── Tip ────────────────────────────────────────────────────────────────────

export function Tip({ text }: { text: string }) {
  const [visible, setVisible] = useState(false)
  const [coords, setCoords] = useState({ top: 0, left: 0 })
  const badgeRef = useRef<HTMLSpanElement>(null)

  function show() {
    if (badgeRef.current) {
      const r = badgeRef.current.getBoundingClientRect()
      setCoords({ top: r.top, left: r.left + r.width / 2 })
    }
    setVisible(true)
  }

  return (
    <span
      ref={badgeRef}
      className="ml-1 inline-flex items-center cursor-default"
      onMouseEnter={show}
      onMouseLeave={() => setVisible(false)}
    >
      <span className="w-3.5 h-3.5 rounded-full bg-white/[.06] border border-white/[.10] text-[9px] text-slate-400 inline-flex items-center justify-center font-bold leading-none select-none">?</span>
      {visible && ReactDOM.createPortal(
        <span
          className="fixed w-60 rounded-[9px] px-3 py-2.5 text-[11px] leading-relaxed shadow-2xl font-medium pointer-events-none whitespace-normal"
          style={{
            background: '#2d2500',
            border: '1px solid rgba(247,166,0,.35)',
            color: '#f5d97a',
            zIndex: 9999,
            top: coords.top,
            left: coords.left,
            transform: 'translate(-50%, calc(-100% - 8px))',
          }}
        >
          {text}
        </span>,
        document.body
      )}
    </span>
  )
}

// ─── SignalPickerField ───────────────────────────────────────────────────────

const TIMEFRAMES = ['1m', '5m', '15m', '1h', '4h', '1D'] as const

function inferSignalDir(sc: { name: string; params?: Record<string, unknown> }): 'buy' | 'sell' | 'neutral' {
  const dir = sc.params?.dir as string | undefined
  if (dir === 'вверх' || dir === 'up' || dir === 'bull') return 'buy'
  if (dir === 'вниз' || dir === 'down' || dir === 'bear') return 'sell'
  if (dir === 'оба') return 'neutral'
  const sig = SIGNALS.find(s => s.id === sc.name)
  return (sig?.state as 'buy' | 'sell' | 'neutral' | undefined) ?? 'neutral'
}

export function SignalPickerField({ configs, onChange, onAutoSave }: {
  configs: SignalConfig[]
  onChange: (configs: SignalConfig[]) => void
  onAutoSave?: (configs: SignalConfig[]) => void
}) {
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<'list' | 'config' | 'edit'>('list')
  const [picked, setPicked] = useState<typeof SIGNALS[0] | typeof INDICATORS[0] | null>(null)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [editingName, setEditingName] = useState<string | null>(null)
  const [dropStyle, setDropStyle] = useState<React.CSSProperties>({})
  const ref = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dropRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      const t = e.target as Node
      if (
        ref.current && !ref.current.contains(t) &&
        dropRef.current && !dropRef.current.contains(t)
      ) {
        setOpen(false); setStep('list'); setPicked(null); setEditingName(null)
      }
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

  function computeDropStyle() {
    if (!buttonRef.current) return
    const r = buttonRef.current.getBoundingClientRect()
    const dropW = 288 // w-72
    const spaceBelow = window.innerHeight - r.bottom
    const above = spaceBelow < 320 && r.top > 320
    setDropStyle({
      position: 'fixed',
      zIndex: 9999,
      width: dropW,
      left: Math.min(r.left, window.innerWidth - dropW - 8),
      ...(above
        ? { bottom: window.innerHeight - r.top + 4 }
        : { top: r.bottom + 4 }),
    })
  }

  function selectSignal(sig: typeof SIGNALS[0] | typeof INDICATORS[0]) {
    setPicked(sig)
    setParams({ ...sig.defaults })
    setStep('config')
  }

  function openEdit(sc: SignalConfig) {
    const allPickable = [...SIGNALS, ...(INDICATORS as unknown as typeof SIGNALS)]
    const sig = allPickable.find(s => s.id === sc.name)
    if (!sig) return
    setPicked(sig)
    setParams({ ...(sc.params ?? {}) })
    setEditingName(sc.name)
    setStep('edit')
    computeDropStyle()
    setOpen(true)
  }

  function handleAdd() {
    if (!picked) return
    onChange([...configs, { name: picked.id, params }])
    setOpen(false); setStep('list'); setPicked(null); setEditingName(null)
  }

  function handleApplyEdit() {
    if (!editingName) return
    const newConfigs = configs.map(c => c.name === editingName ? { ...c, params } : c)
    onChange(newConfigs)
    onAutoSave?.(newConfigs)
    setOpen(false); setStep('list'); setPicked(null); setEditingName(null)
  }

  function remove(id: string) {
    onChange(configs.filter(c => c.name !== id))
  }

  const { ids: allowedIds, loading: allowedLoading } = useAllowedSignalIds()
  const allPickable = [...SIGNALS, ...(INDICATORS as unknown as typeof SIGNALS)]
  const available = allowedLoading
    ? []
    : allPickable.filter(s =>
        !configs.find(c => c.name === s.id) &&
        (allowedIds === null || allowedIds.has(s.id))
      )

  return (
    <div className="flex flex-col gap-1.5">
      {configs.map(sc => {
        const sig = SIGNALS.find(s => s.id === sc.name) ?? (INDICATORS as any[]).find(i => i.id === sc.name)
        const tf = sc.params?.tf as string | undefined
        const statusKey = inferSignalDir(sc)
        const CARD_STYLE = {
          buy:     { bg: 'rgba(65,210,139,.07)',  border: 'rgba(65,210,139,.22)',  name: '#a7f3d0' },
          sell:    { bg: 'rgba(248,113,113,.07)', border: 'rgba(248,113,113,.22)', name: '#fca5a5' },
          neutral: { bg: 'rgba(91,140,255,.06)',  border: 'rgba(91,140,255,.13)',  name: '#c4d2ff' },
        }
        const cs = CARD_STYLE[statusKey]
        const paramBadge = (sc.params?.dir ?? sc.params?.kind) as string | undefined
        return (
          <div key={sc.name} className="flex items-center gap-2 min-w-0">
            <div
              className="flex-1 flex items-center gap-3 px-3 py-2.5 rounded-[9px] min-w-0"
              style={{ background: cs.bg, border: `1px solid ${cs.border}` }}
            >
              <span className="shrink-0 text-[15px] font-semibold" style={{ color: cs.name }}>
                {sig?.name ?? sc.name}
              </span>
              {sig?.desc
                ? <span className="flex-1 min-w-0 text-[12px] text-[#8b9ab8] truncate">{sig.desc}</span>
                : <span className="flex-1" />
              }
              {tf && (
                <span className="shrink-0 inline-flex items-center gap-[3px] text-[12px] font-mono">
                  <span className="text-[#5b6479]">TF</span>
                  <span className="text-[#8babff] font-semibold">{tf}</span>
                </span>
              )}
              {paramBadge && (
                <span className="shrink-0 px-2 py-[3px] rounded-[5px] text-[10px] font-mono text-[#8b9ab8] bg-black/20 border border-white/[.07]">
                  {paramBadge}
                </span>
              )}
              <button
                onClick={() => openEdit(sc)}
                className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-[#8babff] hover:bg-black/20 transition-colors"
                title="Редактировать"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
            </div>
            <button
              onClick={() => remove(sc.name)}
              className="shrink-0 w-7 h-7 inline-flex items-center justify-center rounded-[6px] text-slate-500 hover:text-rose-300 hover:bg-rose-400/[.10] transition-colors"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d="M4 7h16M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/>
              </svg>
            </button>
          </div>
        )
      })}

      <div ref={ref} className="relative">
        <button
          ref={buttonRef}
          onClick={() => {
            if (!open) { computeDropStyle(); setStep('list'); setPicked(null) }
            setOpen(v => !v)
          }}
          className="w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
        >
          + Добавить сигнал
        </button>

        {open && ReactDOM.createPortal(
          <div ref={dropRef} style={dropStyle} className="bg-[#0d1628] border border-white/[.10] rounded-xl shadow-2xl">
            {step === 'list' ? (
              <div className="max-h-64 overflow-y-auto p-1">
                {allowedLoading ? (
                  <div className="py-5 text-center text-[11px] text-slate-500">Загрузка...</div>
                ) : available.length === 0 ? (
                  <div className="py-5 text-center text-[11px] text-slate-500">Все сигналы добавлены</div>
                ) : available.map(sig => (
                  <button key={sig.id} onClick={() => selectSignal(sig)}
                    className="w-full text-left px-2.5 py-2 rounded-lg hover:bg-white/[.05] flex items-start gap-2.5 transition-colors">
                    <span className="shrink-0 mt-px w-8 h-6 rounded-[4px] bg-[#1a2545] border border-[rgba(91,140,255,.3)] text-[#8babff] font-bold text-[9px] flex items-center justify-center tracking-[.3px]">
                      {sig.abbr}
                    </span>
                    <div className="min-w-0">
                      <div className="text-[12px] font-semibold text-[#e6ebf5] leading-none mb-0.5">{sig.name}</div>
                      <div className="text-[10px] text-slate-500 leading-snug">{sig.desc}</div>
                    </div>
                  </button>
                ))}
              </div>
            ) : (step === 'config' || step === 'edit') && picked ? (
              <div className="p-3 space-y-3">
                <div className="flex items-center gap-2">
                  <span className="w-8 h-6 shrink-0 rounded-[4px] bg-[#1a2545] border border-[rgba(91,140,255,.3)] text-[#8babff] font-bold text-[9px] flex items-center justify-center">
                    {picked.abbr}
                  </span>
                  <span className="text-[12px] font-semibold text-[#e6ebf5]">{picked.name}</span>
                  <button
                    onClick={() => step === 'edit'
                      ? (setOpen(false), setStep('list'), setPicked(null), setEditingName(null))
                      : setStep('list')
                    }
                    className="ml-auto text-[11px] text-slate-500 hover:text-slate-300 transition-colors"
                  >
                    {step === 'edit' ? '✕' : '← назад'}
                  </button>
                </div>

                <div>
                  <div className="text-[10px] text-slate-500 mb-1.5">Таймфрейм</div>
                  <div className="flex gap-1 flex-wrap">
                    {TIMEFRAMES.map(tf => (
                      <button key={tf}
                        onClick={() => setParams(p => ({ ...p, tf }))}
                        className={`px-2 py-1 rounded-[5px] text-[10px] font-semibold transition-colors ${
                          params.tf === tf ? 'bg-blue-700 text-white' : 'bg-[#0e1525] text-slate-400 hover:bg-white/[.06]'
                        }`}
                      >
                        {tf}
                      </button>
                    ))}
                  </div>
                </div>

                {picked.params.filter(p => p.key !== 'tf').map(p => {
                  const val = params[p.key]
                  return (
                    <div key={p.key}>
                      <div className="text-[10px] text-slate-500 mb-1.5">{p.label}</div>
                      {p.kind === 'number' ? (
                        <input
                          type="number" step={p.step ?? 1}
                          value={typeof val === 'number' ? val : 0}
                          onChange={e => setParams(pr => ({ ...pr, [p.key]: Number(e.target.value) }))}
                          className="w-full bg-[#0e1525] border border-white/10 rounded-md px-2.5 py-1.5 text-sm text-gray-100 focus:outline-none focus:border-blue-500"
                        />
                      ) : p.kind === 'segmented' && p.options ? (
                        <div className="flex gap-1 flex-wrap">
                          {p.options.map(opt => (
                            <button key={opt}
                              onClick={() => setParams(pr => ({ ...pr, [p.key]: opt }))}
                              className={`px-2.5 py-1 rounded-[5px] text-[11px] font-medium transition-colors ${
                                val === opt ? 'bg-blue-700 text-white' : 'bg-[#0e1525] text-slate-400 hover:bg-white/[.06]'
                              }`}
                            >
                              {opt}
                            </button>
                          ))}
                        </div>
                      ) : null}
                    </div>
                  )
                })}

                <button
                  onClick={step === 'edit' ? handleApplyEdit : handleAdd}
                  className="w-full py-2 bg-blue-600 hover:bg-blue-500 text-white text-[12px] font-semibold rounded-lg transition-colors"
                >
                  {step === 'edit' ? 'Применить' : 'Добавить сигнал'}
                </button>
              </div>
            ) : null}
          </div>,
          document.body
        )}
      </div>
    </div>
  )
}
