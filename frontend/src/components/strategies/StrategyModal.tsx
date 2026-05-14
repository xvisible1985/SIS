import { useState, useEffect, useRef } from 'react'
import ReactDOM from 'react-dom'
import { TemplateSelector } from './TemplateSelector'
import { createStrategy, updateStrategy, getStrategyState } from '../../api/strategies'
import { listAccounts } from '../../api/accounts'
import { CoinPicker } from '../common/CoinPicker'
import { SIGNALS } from '../../features/indicators/signals'
import { INDICATORS } from '../../features/indicators/indicators'
import type { Strategy, StrategyFormData, GridStep, SignalConfig, ExchangeAccount } from '../../types'
import { apiClient } from '../../api/client'

function useAllowedSignalIds(): { ids: Set<string> | null; loading: boolean } {
  const [ids, setIds] = useState<Set<string> | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    // Always try admin endpoint first; if 403 (not admin) fall back to user endpoint.
    apiClient.get<{ id: string; status: string }[]>('/admin/signal-types')
      .then(res => {
        setIds(new Set(res.data.filter(s => s.status !== 'disabled').map(s => s.id)))
        setLoading(false)
      })
      .catch(() =>
        apiClient.get<{ id: string }[]>('/signal-types')
          .then(res => {
            setIds(new Set(res.data.map(s => s.id)))
          })
          .catch(() => setIds(null))
          .finally(() => setLoading(false))
      )
  }, [])

  return { ids, loading }
}

const TIMEFRAMES = ['1m', '5m', '15m', '1h', '4h', '1D'] as const

function inferSignalDir(sc: { name: string; params?: Record<string, unknown> }): 'buy' | 'sell' | 'neutral' {
  const dir = sc.params?.dir as string | undefined
  if (dir === 'вверх' || dir === 'up' || dir === 'bull') return 'buy'
  if (dir === 'вниз' || dir === 'down' || dir === 'bear') return 'sell'
  if (dir === 'оба') return 'neutral'
  const sig = SIGNALS.find(s => s.id === sc.name)
  return (sig?.state as 'buy' | 'sell' | 'neutral' | undefined) ?? 'neutral'
}

const SIG_CHIP: Record<'buy' | 'sell' | 'neutral', { wrap: string; abbr: string; label: string }> = {
  buy:     { wrap: 'bg-emerald-950/[.5] border-emerald-500/[.3]',   abbr: 'bg-emerald-900/[.4] text-emerald-300', label: 'text-emerald-200' },
  sell:    { wrap: 'bg-rose-950/[.5] border-rose-500/[.3]',         abbr: 'bg-rose-900/[.4] text-rose-300',       label: 'text-rose-200'    },
  neutral: { wrap: 'bg-[#0f1a38] border-[rgba(91,140,255,.35)]',    abbr: 'bg-[#1a2545] text-[#8babff]',          label: 'text-blue-200'    },
}

function Toggle({ options, value, onChange }: {
  options: { label: string; value: string }[]
  value: string
  onChange: (v: string) => void
}) {
  return (
    <div className="flex gap-1">
      {options.map(o => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          className={`flex-1 text-center rounded-md py-1.5 text-xs transition-colors ${
            value === o.value
              ? 'bg-blue-700 text-white'
              : 'bg-gray-800 text-gray-400 hover:bg-gray-700'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

function Tip({ text }: { text: string }) {
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

function SignalPickerField({ configs, onChange, onAutoSave }: {
  configs: SignalConfig[]
  onChange: (configs: SignalConfig[]) => void
  onAutoSave?: (configs: SignalConfig[]) => void
}) {
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<'list' | 'config' | 'edit'>('list')
  const [picked, setPicked] = useState<typeof SIGNALS[0] | typeof INDICATORS[0] | null>(null)
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [editingName, setEditingName] = useState<string | null>(null)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false); setStep('list'); setPicked(null); setEditingName(null)
      }
    }
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])

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
  // Merge SIGNALS and INDICATORS so indicator IDs moved to the signals panel by admin are pickable.
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
            {/* card */}
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
            {/* delete — outside */}
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
          onClick={() => { setOpen(v => !v); if (!open) { setStep('list'); setPicked(null) } }}
          className="w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
        >
          + Добавить сигнал
        </button>

        {open && (
          <div className="absolute bottom-full mb-1.5 left-0 bg-[#0d1628] border border-white/[.10] rounded-xl shadow-2xl z-20 w-72">
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
            ) : step === 'config' && picked ? (
              <div className="p-3 space-y-3">
                <div className="flex items-center gap-2">
                  <span className="w-8 h-6 shrink-0 rounded-[4px] bg-[#1a2545] border border-[rgba(91,140,255,.3)] text-[#8babff] font-bold text-[9px] flex items-center justify-center">
                    {picked.abbr}
                  </span>
                  <span className="text-[12px] font-semibold text-[#e6ebf5]">{picked.name}</span>
                  <button onClick={() => setStep('list')} className="ml-auto text-[11px] text-slate-500 hover:text-slate-300 transition-colors">← назад</button>
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
                          type="number"
                          step={p.step ?? 1}
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

                <button onClick={handleAdd}
                  className="w-full py-2 bg-blue-600 hover:bg-blue-500 text-white text-[12px] font-semibold rounded-lg transition-colors">
                  Добавить сигнал
                </button>
              </div>
            ) : step === 'edit' && picked ? (
              <div className="p-3 space-y-3">
                <div className="flex items-center gap-2">
                  <span className="w-8 h-6 shrink-0 rounded-[4px] bg-[#1a2545] border border-[rgba(91,140,255,.3)] text-[#8babff] font-bold text-[9px] flex items-center justify-center">
                    {picked.abbr}
                  </span>
                  <span className="text-[12px] font-semibold text-[#e6ebf5]">{picked.name}</span>
                  <button
                    onClick={() => { setOpen(false); setStep('list'); setPicked(null); setEditingName(null) }}
                    className="ml-auto text-slate-500 hover:text-slate-300 text-[13px] leading-none transition-colors"
                  >✕</button>
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
                          type="number"
                          step={p.step ?? 1}
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

                <button onClick={handleApplyEdit}
                  className="w-full py-2 bg-blue-600 hover:bg-blue-500 text-white text-[12px] font-semibold rounded-lg transition-colors">
                  Применить
                </button>
              </div>
            ) : null}
          </div>
        )}
      </div>
    </div>
  )
}

function defaultForm(): StrategyFormData {
  return {
    account_id: '',
    symbol: '',
    category: 'linear',
    direction: 'long',
    strategy_type: 'grid',
    robot_enabled: true,
    virtual_orders: false,
    entry_order_type: 'limit' as const,
    leverage: 5,
    grid_size_usdt: 100,
    margin_type: 'isolated',
    hedge_mode: false,
    steps: [
      { price_move_pct: 1.0, lots: 1 },
      { price_move_pct: 1.5, lots: 2 },
      { price_move_pct: 2.0, lots: 2 },
    ],
    grid_active: 0,
    signal_configs: [],
    tp_pct: 2.0,
    tp_mode: 'total',
    sl_pct: 5.0,
    sl_type: 'conditional',
    trailing_stop_enabled: false,
    trailing_activation_pct: 1.5,
    trailing_callback_pct: 0.5,
  }
}

function strategyToForm(s: Strategy): StrategyFormData {
  return {
    account_id: s.account_id,
    symbol: s.symbol,
    category: s.category,
    direction: s.direction === 'short' ? 'short' : 'long',
    strategy_type: s.strategy_type,
    robot_enabled: true,
    virtual_orders: false,
    entry_order_type: s.entry_order_type ?? 'limit',
    leverage: s.leverage,
    grid_size_usdt: s.grid_size_usdt,
    margin_type: s.margin_type,
    hedge_mode: s.hedge_mode,
    steps: s.steps ?? [{ price_move_pct: s.grid_step_pct, lots: 1 }],
    grid_active: s.grid_active ?? 0,
    signal_configs: s.signal_configs ?? [],
    tp_pct: s.tp_pct,
    tp_mode: s.tp_mode,
    sl_pct: s.sl_pct,
    sl_type: s.sl_type,
    trailing_stop_enabled: s.trailing_stop_enabled,
    trailing_activation_pct: s.trailing_activation_pct ?? 1.5,
    trailing_callback_pct: s.trailing_callback_pct ?? 0.5,
  }
}

interface Props {
  strategy?: Strategy
  filledLevels?: number
  onClose: () => void
  onSaved: () => void
}

export function StrategyModal({ strategy, filledLevels: filledLevelsProp = 0, onClose, onSaved }: Props) {
  const [tab, setTab] = useState(0)
  const [form, setForm] = useState<StrategyFormData>(
    strategy ? strategyToForm(strategy) : defaultForm()
  )
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [filledLevels, setFilledLevels] = useState(filledLevelsProp)

  useEffect(() => {
    listAccounts().then(setAccounts).catch(() => {})
  }, [])

  // Fetch live filled count when editing — card state may not be loaded yet.
  useEffect(() => {
    if (!strategy) return
    getStrategyState(strategy.id)
      .then(state => setFilledLevels(state.levels?.filter(l => l.status === 'filled').length ?? 0))
      .catch(() => {})
  }, [strategy?.id])

  function patch(partial: Partial<StrategyFormData>) {
    setForm(f => ({ ...f, ...partial }))
  }

  function addStep() {
    patch({ steps: [...form.steps, { price_move_pct: 1.0, lots: 1 }] })
  }

  function removeStep(i: number) {
    patch({ steps: form.steps.filter((_, idx) => idx !== i) })
  }

  function updateStep(i: number, field: keyof GridStep, value: number) {
    const steps = form.steps.map((s, idx) =>
      idx === i ? { ...s, [field]: value } : s
    )
    patch({ steps })
  }

  function loadTemplate(config: Partial<StrategyFormData>) {
    setForm(f => ({ ...f, ...config }))
  }

  async function handleSubmit() {
    if (!form.account_id || !form.symbol) {
      setError('Заполните символ и аккаунт')
      return
    }
    setSaving(true)
    setError(null)
    try {
      const payload = {
        ...form,
        grid_levels: form.steps.length || 1,
        grid_active: form.grid_active > 0 ? form.grid_active : form.steps.length || 1,
        grid_step_pct: form.steps[0]?.price_move_pct ?? 0,
        signal_filter: form.signal_configs.length > 0,
      }
      if (strategy) {
        await updateStrategy(strategy.id, payload as any)
      } else {
        await createStrategy(payload as any)
      }
      onSaved()
    } catch (e: any) {
      const status = e?.response?.status
      const data = e?.response?.data
      const serverMsg = typeof data === 'object' ? data?.error : typeof data === 'string' ? data : null
      if (serverMsg) {
        setError(`${serverMsg}${status ? ` (HTTP ${status})` : ''}`)
      } else if (status) {
        setError(`Сервер вернул HTTP ${status}. Откройте консоль бэкенда для деталей.`)
      } else {
        setError(e?.message ?? 'Неизвестная ошибка')
      }
    } finally {
      setSaving(false)
    }
  }

  const tabLabels = ['1. Базовые', '2. Сетка ордеров', '3. Завершение']
  const inputCls = 'w-full bg-gray-800 border border-gray-700 rounded-md px-3 py-1.5 text-sm text-gray-100 focus:outline-none focus:border-blue-500'
  const labelCls = 'flex items-center text-xs text-gray-500 mb-1'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="bg-gray-900 border border-gray-700 rounded-xl w-[500px] shadow-2xl flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-gray-700">
          <span className="text-gray-100 font-semibold">
            {strategy ? 'Редактировать стратегию' : 'Новая стратегия'}
          </span>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 text-xl leading-none">✕</button>
        </div>

        {/* Template bar */}
        <TemplateSelector formData={form} onLoad={loadTemplate} />

        {/* Tabs */}
        <div className="flex border-b border-gray-700">
          {tabLabels.map((label, i) => (
            <button
              key={i}
              onClick={() => setTab(i)}
              className={`flex-1 py-2.5 text-xs font-medium border-b-2 transition-colors ${
                tab === i
                  ? 'border-blue-500 text-blue-400'
                  : 'border-transparent text-gray-500 hover:text-gray-300'
              }`}
            >
              {label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">

          {/* Tab 1: Entry */}
          {tab === 0 && (
            <>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>Символ<Tip text="Торговый инструмент — монета или контракт, которым будет управлять стратегия. После создания стратегии сменить монету нельзя." /></label>
                  <CoinPicker
                    value={form.symbol || 'BTCUSDT'}
                    onChange={sym => patch({ symbol: sym })}
                    triggerClassName="w-full px-3 py-1.5 text-sm rounded-md border-gray-700 bg-gray-800"
                  />
                </div>
                <div>
                  <label className={labelCls}>Аккаунт<Tip text="API-аккаунт биржи, с которого стратегия будет выставлять ордера и списывать маржу. Один аккаунт может одновременно вести несколько стратегий по разным монетам." /></label>
                  <select
                    value={form.account_id}
                    onChange={e => patch({ account_id: e.target.value })}
                    className={inputCls}
                  >
                    <option value="">Выбрать…</option>
                    {accounts.map(a => (
                      <option key={a.id} value={a.id}>{a.label}</option>
                    ))}
                  </select>
                </div>
              </div>

              <div>
                <label className={labelCls}>Направление<Tip text="Long — покупаешь в расчёте на рост цены. Short — продаёшь в расчёте на падение. Сетка усреднения строится в выбранную сторону." /></label>
                <Toggle
                  options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }]}
                  value={form.direction}
                  onChange={v => patch({ direction: v as 'long' | 'short' })}
                />
              </div>

              <div>
                <label className={labelCls}>Тип стратегии<Tip text="Grid — сетка лимитных ордеров: стратегия сама расставляет уровни и усредняет позицию по мере движения цены. Matrix — усреднение по сигналам индикаторов." /></label>
                <Toggle
                  options={[
                    { label: 'Grid', value: 'grid' },
                    { label: 'Matrix', value: 'dca' },
                  ]}
                  value={form.strategy_type}
                  onChange={v => patch({ strategy_type: v as StrategyFormData['strategy_type'] })}
                />
              </div>

              <div>
                <label className={labelCls}>Робот<Tip text="Разрешить или запретить боту управлять стратегией автоматически." /></label>
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => patch({ robot_enabled: !form.robot_enabled })}
                    className={`w-10 h-5 rounded-full relative transition-colors ${
                      form.robot_enabled ? 'bg-blue-600' : 'bg-gray-700'
                    }`}
                  >
                    <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                      form.robot_enabled ? 'translate-x-5' : 'translate-x-0'
                    }`} />
                  </button>
                  <span className={`text-xs ${form.robot_enabled ? 'text-blue-400' : 'text-gray-500'}`}>
                    {form.robot_enabled ? 'Разрешить управление бота' : 'Запретить управление бота'}
                  </span>
                </div>
              </div>

              <div>
                <label className={labelCls}>Виртуальные ордера<Tip text="Когда включено — ордера не отправляются на биржу, исполнение симулируется внутри системы. Полезно для тестирования стратегии без реальных средств." /></label>
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => patch({ virtual_orders: !form.virtual_orders })}
                    className={`w-10 h-5 rounded-full relative transition-colors ${
                      form.virtual_orders ? 'bg-blue-600' : 'bg-gray-700'
                    }`}
                  >
                    <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                      form.virtual_orders ? 'translate-x-5' : 'translate-x-0'
                    }`} />
                  </button>
                  <span className={`text-xs ${form.virtual_orders ? 'text-blue-400' : 'text-gray-500'}`}>
                    {form.virtual_orders ? 'Включены' : 'Выключены'}
                  </span>
                </div>
              </div>

              <div>
                <label className={labelCls}>Тип входного ордера<Tip text="Limit — мейкер-ордер ждёт своей цены в стакане (комиссия ~0.02%). Stop Market — тейкер-ордер срабатывает при пробое уровня (комиссия ~0.055%). Limit дешевле, Stop Market гарантирует исполнение." /></label>
                <Toggle
                  options={[
                    { label: 'Limit', value: 'limit' },
                    { label: 'Stop Market', value: 'stop_market' },
                  ]}
                  value={form.entry_order_type}
                  onChange={v => patch({ entry_order_type: v as 'limit' | 'stop_market' })}
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>Плечо (×)<Tip text="Кратность заёмных средств. Плечо ×5 означает, что на $100 маржи открывается позиция на $500. Чем выше плечо — тем больше потенциальная прибыль и тем ближе ликвидация." /></label>
                  <input
                    type="number" min={1} max={100}
                    value={form.leverage}
                    onChange={e => patch({ leverage: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>Объём 1 лота (USDT)<Tip text="Размер одного базового ордера в USDT. Итоговый объём каждого уровня сетки = лоты шага × этот размер. Например, при 100 USDT/лот и 2 лотах на шаге — ордер на 200 USDT." /></label>
                  <input
                    type="number" min={1}
                    value={form.grid_size_usdt}
                    onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
              </div>

              <div>
                <label className={labelCls}>Тип маржи<Tip text="Isolated — маржа ограничена суммой, зарезервированной под конкретную позицию; максимальный убыток не превысит её. Cross — в расчёте ликвидации участвует вся свободная маржа аккаунта." /></label>
                <Toggle
                  options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
                  value={form.margin_type}
                  onChange={v => patch({ margin_type: v as 'isolated' | 'cross' })}
                />
              </div>

              <div>
                <label className={labelCls}>Хедж режим<Tip text="Режим биржи, при котором Long и Short по одной монете живут в отдельных слотах и не компенсируют друг друга. Нужен для одновременного ведения двух стратегий по одному инструменту в разных направлениях." /></label>
                <Toggle
                  options={[{ label: 'Нет', value: 'false' }, { label: 'Да (Hedge)', value: 'true' }]}
                  value={String(form.hedge_mode)}
                  onChange={v => patch({ hedge_mode: v === 'true' })}
                />
              </div>
            </>
          )}

          {/* Tab 2: Averaging */}
          {tab === 1 && (
            <>
              <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800">
                Шаги усреднения
              </div>
              <div>
                <div className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 text-[10px] text-gray-600 mb-1 px-0.5">
                  <div className="text-center">#</div>
                  <div className="text-center">Движение %</div>
                  <div className="text-center">Лотов</div>
                  <div className="text-center">USDT</div>
                  <div />
                </div>
                <div className="max-h-[216px] overflow-y-auto">
                  {form.steps.map((step, i) => {
                    const isFilled = i < filledLevels
                    return (
                      <div
                        key={i}
                        className={`grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 items-center py-1.5 border-b border-gray-800/60 last:border-0 ${isFilled ? 'bg-green-950/30' : ''}`}
                      >
                        <div className="text-center text-[10px]">
                          {isFilled
                            ? <span className="text-green-400 font-bold">✓</span>
                            : <span className="text-gray-600">{i + 1}</span>
                          }
                        </div>
                        <input
                          type="number" step="0.1" min="0"
                          value={step.price_move_pct}
                          onChange={e => updateStep(i, 'price_move_pct', Number(e.target.value))}
                          className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                        />
                        <input
                          type="number" step="0.1" min="0.1"
                          value={step.lots}
                          onChange={e => updateStep(i, 'lots', Number(e.target.value))}
                          className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                        />
                        <input
                          type="number" step="1" min="1"
                          value={+(step.lots * form.grid_size_usdt).toFixed(2)}
                          onChange={e => {
                            const usdt = Number(e.target.value)
                            if (usdt > 0 && form.grid_size_usdt > 0)
                              updateStep(i, 'lots', Math.round(usdt / form.grid_size_usdt * 100) / 100)
                          }}
                          className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                        />
                        <button
                          onClick={() => removeStep(i)}
                          disabled={isFilled}
                          className={`text-center text-sm ${isFilled ? 'text-gray-700 cursor-not-allowed' : 'text-red-500 hover:text-red-400'}`}
                        >
                          ✕
                        </button>
                      </div>
                    )
                  })}
                </div>
                <button
                  onClick={addStep}
                  className="mt-2 w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
                >
                  + Добавить шаг
                </button>
              </div>

              <div>
                <label className={labelCls}>Одновременно на бирже (0 = все шаги)<Tip text="Сколько ордеров из сетки висит на бирже в один момент. При 0 — сразу все. При значении N — после заполнения одного ордера автоматически выставляется следующий, поддерживая N активных заявок." /></label>
                <input
                  type="number" min={0} max={form.steps.length}
                  value={form.grid_active}
                  onChange={e => patch({ grid_active: Number(e.target.value) })}
                  className={inputCls}
                  placeholder="0"
                />
                <p className="text-[10px] text-gray-600 mt-1">
                  При заполнении ордера автоматически выставляется следующий шаг.
                </p>
              </div>

              <div className="flex items-center gap-1 text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800 mt-2">
                Сигналы для входа<Tip text="Технические индикаторы, сигнал которых должен совпасть для запуска цикла. Если сигналов нет — цикл стартует сразу. AND-логика: все добавленные сигналы должны совпасть одновременно." />
              </div>
              <SignalPickerField
                configs={form.signal_configs}
                onChange={v => patch({ signal_configs: v })}
                onAutoSave={strategy ? (newConfigs) => {
                  const payload = {
                    ...form,
                    signal_configs: newConfigs,
                    grid_levels: form.steps.length || 1,
                    grid_active: form.grid_active > 0 ? form.grid_active : form.steps.length || 1,
                    grid_step_pct: form.steps[0]?.price_move_pct ?? 0,
                    signal_filter: newConfigs.length > 0,
                  }
                  updateStrategy(strategy.id, payload as any).catch(() => {})
                } : undefined}
              />
            </>
          )}

          {/* Tab 3: Exit */}
          {tab === 2 && (
            <>
              {/* TP */}
              <div className="border border-green-900/50 bg-green-950/20 rounded-lg p-3 space-y-3">
                <div className="text-xs font-semibold text-green-400">Take Profit</div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>TP %<Tip text="Процент прибыли от средней цены входа по всей позиции, при достижении которого выставляется ордер закрытия. Отсчитывается от средневзвешенной цены всех заполненных уровней." /></label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.tp_pct}
                      onChange={e => patch({ tp_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Режим<Tip text="«На всю позицию» — один TP-ордер закрывает сразу всё. «По уровням» — каждый заполненный уровень получает свой TP; позиция закрывается порциями по мере роста цены." /></label>
                    <Toggle
                      options={[{ label: 'На всю позицию', value: 'total' }, { label: 'По уровням', value: 'per_level' }]}
                      value={form.tp_mode}
                      onChange={v => patch({ tp_mode: v as 'total' | 'per_level' })}
                    />
                  </div>
                </div>
              </div>

              {/* SL */}
              <div className="border border-red-900/50 bg-red-950/20 rounded-lg p-3 space-y-3">
                <div className="text-xs font-semibold text-red-400">Stop Loss</div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>SL %<Tip text="Процент убытка от средней цены входа, при достижении которого позиция принудительно закрывается. Защищает от неконтролируемого роста убытка при сильном движении против позиции." /></label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.sl_pct}
                      onChange={e => patch({ sl_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Тип<Tip text="«На бирже» — стоп-ордер живёт непосредственно на Bybit и сработает даже если сервер недоступен. «Программный» — сервис сам следит за ценой через WS и закрывает позицию рыночным ордером при достижении уровня." /></label>
                    <Toggle
                      options={[
                        { label: 'На бирже', value: 'conditional' },
                        { label: 'Программный', value: 'programmatic' },
                      ]}
                      value={form.sl_type}
                      onChange={v => patch({ sl_type: v as 'conditional' | 'programmatic' })}
                    />
                  </div>
                </div>
              </div>

              {/* Trailing stop */}
              <div className="space-y-3">
                <div className="flex items-center gap-1 text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800">
                  Трейлинг-стоп<Tip text="Автоматически подтягивает стоп вслед за ценой, фиксируя накопленную прибыль. Пока цена идёт в твою сторону — стоп следует за ней. Как только цена откатывается на заданный callback — позиция закрывается." />
                </div>
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => patch({ trailing_stop_enabled: !form.trailing_stop_enabled })}
                    className={`w-10 h-5 rounded-full relative transition-colors ${
                      form.trailing_stop_enabled ? 'bg-green-600' : 'bg-gray-700'
                    }`}
                  >
                    <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                      form.trailing_stop_enabled ? 'translate-x-5' : 'translate-x-0'
                    }`} />
                  </button>
                  <span className={`text-xs ${form.trailing_stop_enabled ? 'text-green-400' : 'text-gray-500'}`}>
                    {form.trailing_stop_enabled ? 'Включён' : 'Выключен'}
                  </span>
                </div>
                {form.trailing_stop_enabled && (
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>Активация %<Tip text="Прибыль от средней цены входа, при достижении которой трейлинг-стоп начинает следить за ценой. До этого порога стоп стоит на фиксированном уровне SL." /></label>
                      <input
                        type="number" step="0.1" min="0.1"
                        value={form.trailing_activation_pct}
                        onChange={e => patch({ trailing_activation_pct: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>Callback %<Tip text="Максимальный откат от пика цены, после которого трейлинг-стоп срабатывает и закрывает позицию. Чем меньше значение — тем раньше фиксируется прибыль, но выше риск закрыться на случайном колебании." /></label>
                      <input
                        type="number" step="0.1" min="0.1"
                        value={form.trailing_callback_pct}
                        onChange={e => patch({ trailing_callback_pct: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                  </div>
                )}
              </div>
            </>
          )}
        </div>

        {/* Footer */}
        {error && (
          <div className="px-5 py-2.5 text-xs text-red-400 border-t border-gray-700 break-words select-text">
            <span className="font-semibold">✗ Ошибка:</span> {error}
          </div>
        )}
        <div className="flex justify-end gap-2 px-5 py-2.5 border-t border-gray-700">
          <button
            onClick={onClose}
            className="px-4 py-1.5 text-sm bg-gray-800 text-gray-300 rounded-lg hover:bg-gray-700 transition-colors"
          >
            Отмена
          </button>
          <button
            onClick={handleSubmit}
            disabled={saving}
            className="px-4 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
          >
            {saving ? 'Сохранение…' : strategy ? 'Сохранить' : 'Создать стратегию'}
          </button>
        </div>
      </div>
    </div>
  )
}
