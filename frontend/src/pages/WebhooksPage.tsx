import { useEffect, useMemo, useState } from 'react'
import { listWebhooks, createWebhook, updateWebhook, deleteWebhook, listWebhookLogs } from '../api/webhooks'
import { listCustomSignals, createCustomSignal, deleteCustomSignal, comboPreview, type ComboComponentInput } from '../api/customSignals'
import { SIGNALS } from '../features/indicators/signals'
import { ParamNumber, ParamSegmented } from '../features/indicators/components/Params'
import { SignalPickCard, CAT_LABEL } from '../features/indicators/components/SignalPickCard'
import { useEnabledSignals } from '../features/indicators/useEnabledSignals'
import { SignalPreviewChart } from '../features/webhooks/SignalPreviewChart'
import { TimesfmForecastPanel } from '../features/webhooks/TimesfmForecastPanel'
import { CoinPicker } from '../components/common/CoinPicker'
import type { SignalDef, IndicatorCategory } from '../features/indicators/types'
import type { ChartEvent } from '../features/indicators/signalChartShared'
import type { Webhook, WebhookLog, CustomSignal } from '../types'

// bybit-news has no pkg/signal registry entry (it's a fundamental trigger, not a
// candle-based computation) — the backend rejects it at creation time regardless, this
// just keeps it off the picker so the rejection is never hit.
const ALERTABLE_SIGNALS = SIGNALS.filter(s => s.id !== 'bybit-news')

const PLATFORMS = ['custom', 'tradingview', '3commas', 'alertatron']
const TIMEFRAMES = [
  { label: '1м', value: '1m' }, { label: '5м', value: '5m' },
  { label: '15м', value: '15m' }, { label: '30м', value: '30m' },
  { label: '1ч', value: '1h' }, { label: '4ч', value: '4h' },
]

const ALL_CATS = Object.keys(CAT_LABEL) as IndicatorCategory[]
// whale/leverage/bybit-news (the "fundamental" category) don't derive state from candle
// history — /signals/chart-history walks Compute() over past candles, and these three only
// return a real state from ComputeWithSymbol (live whale/leverage cache), so their preview
// on this page's chart would always show zero markers. Hidden by default so the catalog
// doesn't get cluttered with entries that look "broken" here; still creatable via the filter.
const DEFAULT_CATS = new Set<IndicatorCategory>(ALL_CATS.filter(c => c !== 'fundamental'))

// What the alert-creation form is currently pointed at — a single catalog signal (its own
// params apply) or a saved combo (each leg carries its own params, nothing to edit here).
type AlertTarget =
  | { kind: 'catalog'; def: SignalDef }
  | { kind: 'custom'; cs: CustomSignal }

function AlertHistory({ webhookId }: { webhookId: string }) {
  const [logs, setLogs] = useState<WebhookLog[] | null>(null)
  useEffect(() => { listWebhookLogs(webhookId).then(setLogs).catch(() => setLogs([])) }, [webhookId])
  if (logs === null) return <div className="px-3 py-2 text-[11px] text-slate-500">Загрузка…</div>
  if (logs.length === 0) return <div className="px-3 py-2 text-[11px] text-slate-500">Пока не срабатывал</div>
  return (
    <div className="max-h-40 overflow-auto px-3 py-2">
      {logs.map(lg => (
        <div key={lg.id} className="flex items-center justify-between gap-2 border-b border-white/[.04] py-1 text-[11px] last:border-0">
          <span className="text-slate-500">{new Date(lg.sent_at).toLocaleString('ru-RU')}</span>
          <span className={lg.success ? 'text-emerald-400' : 'text-rose-400'}>
            {lg.success ? 'доставлено' : `ошибка (${lg.status_code || lg.error || '—'})`}
          </span>
          <span className="text-slate-600">{lg.response_ms} мс</span>
        </div>
      ))}
    </div>
  )
}

function AlertRow({ wh, onToggle, onDelete }: { wh: Webhook; onToggle: (v: boolean) => void; onDelete: () => void }) {
  const [expanded, setExpanded] = useState(false)
  const [copied, setCopied] = useState(false)

  function copyUrl() {
    navigator.clipboard.writeText(wh.url).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="rounded-xl border border-white/[.07] bg-[#0d1018]">
      <div className="flex flex-wrap items-center gap-3 px-4 py-3">
        <div className="min-w-[140px]">
          <div className="text-[12px] font-semibold text-slate-100">{wh.catalog_signal_name}</div>
          <div className="text-[11px] text-slate-500">{wh.symbol} · {wh.timeframe} · {wh.platform}</div>
        </div>
        <button
          type="button"
          onClick={copyUrl}
          title={wh.url}
          className="flex-1 truncate rounded-lg border border-white/[.06] bg-black/25 px-2.5 py-1.5 text-left font-mono text-[11px] text-slate-400 hover:text-slate-200"
        >
          {copied ? 'Скопировано ✓' : wh.url}
        </button>
        <label className="flex items-center gap-1.5 text-[11px] text-slate-400">
          <input type="checkbox" checked={wh.is_active} onChange={e => onToggle(e.target.checked)} className="accent-[#5b8cff]" />
          активен
        </label>
        <button type="button" onClick={() => setExpanded(v => !v)} className="text-[11px] text-slate-500 hover:text-slate-300">
          {expanded ? 'Скрыть историю' : 'История'}
        </button>
        <button type="button" onClick={onDelete} className="text-[11px] text-rose-400 hover:text-rose-300">
          Удалить
        </button>
      </div>
      {expanded && <div className="border-t border-white/[.06]"><AlertHistory webhookId={wh.id} /></div>}
    </div>
  )
}

function CustomSignalCard({ cs, selected, onClick, onDelete }: {
  cs: CustomSignal; selected: boolean; onClick: () => void; onDelete: () => void
}) {
  return (
    <div
      className={`flex flex-col gap-1.5 rounded-xl border p-3 text-left transition-colors ${
        selected ? 'border-[#a78bfa]/60 bg-[#a78bfa]/[.08]' : 'border-white/[.07] bg-[#0d1018] hover:border-white/[.15]'
      }`}
    >
      <div className="flex items-start gap-2">
        <button type="button" onClick={onClick} className="min-w-0 flex-1 text-left">
          <div className="mb-1 flex items-center gap-1.5">
            <span
              className="rounded-full bg-[#e879f9]/[.15] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wide text-[#f0abfc]"
              title="Комбинированный сигнал"
            >
              {cs.badge}
            </span>
          </div>
          <div className="truncate text-[12px] font-semibold text-slate-100">{cs.name}</div>
        </button>
        <button
          type="button"
          onClick={e => { e.stopPropagation(); onDelete() }}
          title="Удалить сигнал"
          className="shrink-0 text-slate-600 hover:text-rose-400"
        >
          ✕
        </button>
      </div>
      <div className="line-clamp-2 text-[11px] leading-snug text-slate-500">
        {cs.components.map(c => c.signal_name).join(' + ')}
      </div>
    </div>
  )
}

export function WebhooksPage() {
  const enabledIds = useEnabledSignals()
  const catalog = enabledIds ? ALERTABLE_SIGNALS.filter(s => enabledIds.has(s.id)) : ALERTABLE_SIGNALS

  const [webhooks, setWebhooks] = useState<Webhook[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [customSignals, setCustomSignals] = useState<CustomSignal[]>([])
  const [target, setTarget] = useState<AlertTarget | null>(null)
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [timeframe, setTimeframe] = useState('15m')
  const [platform, setPlatform] = useState('custom')
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [creating, setCreating] = useState(false)
  const [formError, setFormError] = useState('')
  const [query, setQuery] = useState('')
  const [activeCats, setActiveCats] = useState<Set<IndicatorCategory>>(DEFAULT_CATS)

  // ── Combo builder ────────────────────────────────────────────────────────
  interface ComboLeg { def: SignalDef; params: Record<string, unknown> }
  const [comboMode, setComboMode] = useState(false)
  const [comboLegs, setComboLegs] = useState<ComboLeg[]>([])
  const [comboName, setComboName] = useState('')
  const [comboBadge, setComboBadge] = useState('')
  const [comboSaving, setComboSaving] = useState(false)
  const [comboSaveError, setComboSaveError] = useState('')
  const [editingLegId, setEditingLegId] = useState<string | null>(null)

  function toggleCat(cat: IndicatorCategory) {
    setActiveCats(prev => {
      const next = new Set(prev)
      next.has(cat) ? next.delete(cat) : next.add(cat)
      return next
    })
  }

  const filteredCatalog = catalog
    .filter(s => activeCats.has(s.cat))
    .filter(s => !query.trim() ||
      s.name.toLowerCase().includes(query.toLowerCase()) ||
      s.abbr.toLowerCase().includes(query.toLowerCase())
    )

  useEffect(() => {
    listWebhooks()
      .then(setWebhooks)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Не удалось загрузить'))
      .finally(() => setLoading(false))
  }, [])

  function refreshCustomSignals() {
    return listCustomSignals().then(setCustomSignals).catch(() => {})
  }
  useEffect(() => { refreshCustomSignals() }, [])

  function selectSignal(def: SignalDef) {
    if (comboMode) {
      setComboLegs(prev => {
        if (prev.some(l => l.def.id === def.id)) {
          if (editingLegId === def.id) setEditingLegId(null)
          return prev.filter(l => l.def.id !== def.id)
        }
        return [...prev, { def, params: def.defaults }]
      })
      return
    }
    setTarget({ kind: 'catalog', def })
    setParams(def.defaults)
    setFormError('')
  }

  function setLegParam(defId: string, key: string, v: unknown) {
    setComboLegs(prev => prev.map(l => l.def.id === defId ? { ...l, params: { ...l.params, [key]: v } } : l))
  }

  function selectCustomSignal(cs: CustomSignal) {
    setTarget({ kind: 'custom', cs })
    setFormError('')
  }

  function toggleComboMode() {
    setComboMode(v => {
      if (v) { setComboLegs([]); setComboName(''); setComboBadge(''); setComboSaveError(''); setEditingLegId(null) }
      return !v
    })
  }

  async function handleSaveCombo() {
    if (comboLegs.length < 2) return
    if (!comboName.trim()) { setComboSaveError('Введите название'); return }
    if (!comboBadge.trim()) { setComboSaveError('Задайте шилдик (до 4 символов)'); return }
    setComboSaveError('')
    setComboSaving(true)
    try {
      const components: ComboComponentInput[] = comboLegs.map(l => ({ signal_id: l.def.id, params: l.params }))
      const { id } = await createCustomSignal(comboName.trim(), comboBadge.trim(), components)
      const saved = await listCustomSignals()
      setCustomSignals(saved)
      const created = saved.find(cs => cs.id === id)
      setComboLegs([])
      setComboName('')
      setComboBadge('')
      setComboMode(false)
      setEditingLegId(null)
      if (created) selectCustomSignal(created)
    } catch (e: unknown) {
      setComboSaveError(e instanceof Error ? e.message : 'Не удалось сохранить')
    } finally {
      setComboSaving(false)
    }
  }

  async function handleDeleteCustomSignal(cs: CustomSignal) {
    if (!confirm(`Удалить сигнал «${cs.name}»? Оповещения на его основе тоже удалятся.`)) return
    try {
      await deleteCustomSignal(cs.id)
      if (target?.kind === 'custom' && target.cs.id === cs.id) setTarget(null)
      setCustomSignals(prev => prev.filter(c => c.id !== cs.id))
      setWebhooks(prev => prev.filter(w => w.custom_signal_id !== cs.id))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Не удалось удалить сигнал')
    }
  }

  function setParam(key: string, v: unknown) {
    setParams(prev => ({ ...prev, [key]: v }))
  }

  async function handleCreate() {
    if (!target) { setFormError('Выберите сигнал в каталоге ниже'); return }
    if (!symbol.trim()) { setFormError('Укажите монету'); return }
    setFormError('')
    setCreating(true)
    try {
      const wh = await createWebhook(
        target.kind === 'catalog'
          ? { catalog_signal_id: target.def.id, symbol: symbol.trim().toUpperCase(), timeframe, params, platform }
          : { custom_signal_id: target.cs.id, symbol: symbol.trim().toUpperCase(), timeframe, platform }
      )
      setWebhooks(prev => [wh, ...prev])
      setTarget(null)
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : 'Не удалось создать оповещение')
    } finally {
      setCreating(false)
    }
  }

  async function handleToggle(wh: Webhook, active: boolean) {
    const updated = await updateWebhook(wh.id, { is_active: active })
    setWebhooks(prev => prev.map(w => w.id === wh.id ? updated : w))
  }

  async function handleDelete(id: string) {
    try {
      await deleteWebhook(id)
      setWebhooks(prev => prev.filter(w => w.id !== id))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Не удалось удалить')
    }
  }

  // ── Chart preview wiring ─────────────────────────────────────────────────
  // A combo being actively built (2+ legs picked) takes priority over a saved combo
  // that's merely selected as the alert target — if you're building something new, that's
  // what you want to see on the chart, even if a different saved combo is still "selected".
  const activeComboComponents: ComboComponentInput[] | null = useMemo(() => {
    if (comboLegs.length >= 2) return comboLegs.map(l => ({ signal_id: l.def.id, params: l.params }))
    if (target?.kind === 'custom') return target.cs.components.map(c => ({ signal_id: c.signal_id, params: c.params }))
    return null
  }, [comboLegs, target])

  const [comboPreviewState, setComboPreviewState] = useState<{ events: ChartEvent[]; loading: boolean; error: string | null } | null>(null)

  useEffect(() => {
    if (!activeComboComponents) { setComboPreviewState(null); return }
    let cancelled = false
    setComboPreviewState({ events: [], loading: true, error: null })
    comboPreview(symbol, timeframe, activeComboComponents)
      .then(events => { if (!cancelled) setComboPreviewState({ events, loading: false, error: null }) })
      .catch((e: unknown) => {
        if (!cancelled) setComboPreviewState({ events: [], loading: false, error: e instanceof Error ? e.message : 'Ошибка' })
      })
    return () => { cancelled = true }
  }, [symbol, timeframe, activeComboComponents])

  const chartActiveSignal = !activeComboComponents && target?.kind === 'catalog' ? target.def : null
  const chartHintLabel = activeComboComponents
    ? (comboLegs.length >= 2 ? comboLegs.map(l => l.def.name).join(' + ') : target?.kind === 'custom' ? target.cs.name : '')
    : target?.kind === 'catalog' ? target.def.name : ''

  if (loading) return <p className="p-5 text-sm text-slate-500">Загрузка…</p>

  return (
    <div className="flex h-full flex-col overflow-y-auto bg-[#0a0d14] text-slate-200">
      <div className="flex h-11 flex-shrink-0 items-center gap-3 border-b border-white/[.06] px-5">
        <span className="text-sm font-semibold text-slate-100">Вебхуки</span>
        <span className="text-[11px] text-slate-500">
          Выберите сигнал, настройте оповещение — SIS сформирует URL и перешлёт срабатывание в Telegram
        </span>
      </div>

      <div className="flex flex-1 gap-3 overflow-hidden p-3">
        {/* Left column — chart on top, signal catalog below (mirrors TerminalPage) */}
        <div className="flex flex-col gap-3 overflow-hidden" style={{ flex: '0 0 68%', minWidth: 0 }}>

          {/* Chart */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ flex: '0 0 65%', minHeight: 0 }}>
            <div className="flex flex-shrink-0 items-center gap-2 border-b border-white/[.06] px-4 py-2">
              <CoinPicker value={symbol} onChange={setSymbol} />
              <div className="flex items-center gap-px rounded-lg border border-white/[.08] bg-black/25 p-px">
                {TIMEFRAMES.map(tf => (
                  <button
                    key={tf.value}
                    type="button"
                    onClick={() => setTimeframe(tf.value)}
                    className={`rounded-[5px] px-2 py-1 text-[11px] font-semibold transition-colors ${
                      timeframe === tf.value ? 'bg-[#5b8cff] text-white' : 'text-slate-400 hover:text-white'
                    }`}
                  >
                    {tf.label}
                  </button>
                ))}
              </div>
              {chartHintLabel && (
                <span className="ml-auto truncate text-[11px] text-slate-500">
                  {chartHintLabel} <span className="text-slate-600">— стрелки на графике</span>
                </span>
              )}
            </div>
            <div className="min-h-0 flex-1">
              {chartActiveSignal?.id === 'timesfm'
                ? <TimesfmForecastPanel symbol={symbol} tf={timeframe} />
                : <SignalPreviewChart symbol={symbol} tf={timeframe} activeSignal={chartActiveSignal} combo={comboPreviewState} />}
            </div>
          </div>

          {/* Signal catalog — role of Positions/Orders in the terminal */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ flex: 1, minHeight: 0 }}>
            <div className="flex flex-shrink-0 items-center gap-3 border-b border-white/[.06] bg-violet-500/[.07] px-4 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-violet-300/70">
                Доступные сигналы <span className="ml-1.5 font-mono text-violet-400/50">{filteredCatalog.length}</span>
              </span>
              <button
                type="button"
                onClick={toggleComboMode}
                title="Выбрать несколько сигналов и объединить их по И (AND) в новый сигнал"
                className={`rounded-full border px-2.5 py-1 text-[10.5px] font-semibold uppercase tracking-wide transition-colors ${
                  comboMode
                    ? 'border-[#a78bfa]/50 bg-[#a78bfa]/[.18] text-[#c4b1fb]'
                    : 'border-white/[.08] bg-white/[.03] text-slate-500 hover:text-slate-300'
                }`}
              >
                {comboMode ? 'Комбинировать: вкл' : 'Комбинировать'}
              </button>
              <input
                value={query}
                onChange={e => setQuery(e.target.value)}
                placeholder="Поиск сигнала…"
                className="ml-auto w-52 rounded-lg border border-white/[.08] bg-black/25 px-3 py-1 text-[12px] text-slate-200 outline-none placeholder:text-slate-600 focus:border-[#5b8cff]/50"
              />
            </div>

            <div className="flex flex-shrink-0 flex-wrap items-center gap-1.5 border-b border-white/[.06] px-4 py-2">
              {ALL_CATS.map(cat => {
                const on = activeCats.has(cat)
                return (
                  <button
                    key={cat}
                    type="button"
                    onClick={() => toggleCat(cat)}
                    title={cat === 'fundamental' ? 'Не основаны на свечах — без превью на графике' : undefined}
                    className={`rounded-full border px-2.5 py-1 text-[10.5px] font-semibold uppercase tracking-wide transition-colors ${
                      on
                        ? 'border-[#5b8cff]/50 bg-[#5b8cff]/[.15] text-[#b8c8ff]'
                        : 'border-white/[.08] bg-white/[.03] text-slate-500 hover:text-slate-300'
                    }`}
                  >
                    {CAT_LABEL[cat] ?? cat}
                  </button>
                )
              })}
            </div>

            {comboMode && (
              <div className="flex flex-shrink-0 flex-col gap-2 border-b border-white/[.06] bg-[#a78bfa]/[.05] px-4 py-2.5">
                <div className="flex flex-wrap items-center gap-2">
                  {comboLegs.length === 0 ? (
                    <span className="text-[11px] text-slate-500">Кликните 2+ карточки ниже, чтобы собрать сигнал по И (AND)</span>
                  ) : (
                    comboLegs.map(l => (
                      <span key={l.def.id} className="inline-flex items-center gap-1 rounded-full border border-[#a78bfa]/40 bg-[#a78bfa]/[.12] pl-2.5 pr-1.5 py-1 text-[11px] font-semibold text-[#c4b1fb]">
                        {l.def.name}
                        {l.def.params.length > 0 && (
                          <button
                            type="button"
                            title="Параметры"
                            onClick={() => setEditingLegId(id => id === l.def.id ? null : l.def.id)}
                            className={`rounded px-1 ${editingLegId === l.def.id ? 'text-white' : 'text-[#c4b1fb]/70 hover:text-white'}`}
                          >
                            ⚙
                          </button>
                        )}
                        <button type="button" onClick={() => setComboLegs(prev => prev.filter(x => x.def.id !== l.def.id))} className="text-[#c4b1fb]/70 hover:text-white">✕</button>
                      </span>
                    ))
                  )}
                  {comboLegs.length >= 2 && (
                    <div className="ml-auto flex items-center gap-1.5">
                      <input
                        value={comboName}
                        onChange={e => setComboName(e.target.value)}
                        placeholder="Название сигнала…"
                        className="w-44 rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-1 text-[12px] text-slate-200 outline-none placeholder:text-slate-600 focus:border-[#a78bfa]/50"
                      />
                      <input
                        value={comboBadge}
                        onChange={e => setComboBadge(e.target.value.slice(0, 4).toUpperCase())}
                        placeholder="Шилдик"
                        title="Короткая метка на бейдже, до 4 символов"
                        maxLength={4}
                        className="w-16 rounded-lg border border-white/[.08] bg-black/25 px-2 py-1 text-center text-[12px] font-bold uppercase tracking-wide text-slate-200 outline-none placeholder:text-slate-600 placeholder:normal-case placeholder:font-normal placeholder:tracking-normal focus:border-[#a78bfa]/50"
                      />
                      <button
                        type="button"
                        onClick={handleSaveCombo}
                        disabled={comboSaving}
                        className="rounded-lg bg-gradient-to-b from-[#a78bfa] to-[#8b5cf6] px-3 py-1 text-[11px] font-semibold text-white disabled:opacity-50"
                      >
                        {comboSaving ? 'Сохраняем…' : 'Сохранить как сигнал'}
                      </button>
                    </div>
                  )}
                </div>

                {editingLegId && (() => {
                  const leg = comboLegs.find(l => l.def.id === editingLegId)
                  if (!leg) return null
                  return (
                    <div className="rounded-lg border border-[#a78bfa]/30 bg-black/25 px-2.5">
                      <div className="flex items-center justify-between pt-2">
                        <span className="text-[10px] uppercase tracking-wide text-[#c4b1fb]/70">Параметры «{leg.def.name}»</span>
                        <button type="button" onClick={() => setEditingLegId(null)} className="pb-2 text-[11px] text-slate-500 hover:text-slate-300">Готово</button>
                      </div>
                      {leg.def.params.map(p => p.kind === 'number' ? (
                        <ParamNumber
                          key={p.key}
                          label={p.label}
                          hint={p.hint}
                          value={Number(leg.params[p.key] ?? 0)}
                          step={p.step}
                          min={p.min}
                          max={p.max}
                          suffix={p.suffix}
                          decimals={p.decimals}
                          onChange={v => setLegParam(leg.def.id, p.key, v)}
                        />
                      ) : (
                        <ParamSegmented
                          key={p.key}
                          label={p.label}
                          hint={p.hint}
                          value={String(leg.params[p.key] ?? p.options[0])}
                          options={p.options}
                          onChange={v => setLegParam(leg.def.id, p.key, v)}
                        />
                      ))}
                    </div>
                  )
                })()}

                {comboSaveError && <p className="text-[11px] text-rose-400">{comboSaveError}</p>}
              </div>
            )}

            <div className="flex-1 overflow-auto p-3">
              {customSignals.length > 0 && (
                <div className="mb-3">
                  <div className="mb-2 text-[10px] font-semibold uppercase tracking-wide text-slate-500">
                    Мои сигналы <span className="font-mono text-slate-600">{customSignals.length}</span>
                  </div>
                  <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2.5">
                    {customSignals.map(cs => (
                      <CustomSignalCard
                        key={cs.id}
                        cs={cs}
                        selected={target?.kind === 'custom' && target.cs.id === cs.id}
                        onClick={() => selectCustomSignal(cs)}
                        onDelete={() => handleDeleteCustomSignal(cs)}
                      />
                    ))}
                  </div>
                  <div className="my-3 h-px bg-white/[.06]" />
                </div>
              )}
              {filteredCatalog.length === 0 ? (
                <p className="py-6 text-center text-[12px] text-slate-500">Ничего не найдено</p>
              ) : (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2.5">
                  {filteredCatalog.map(def => (
                    <SignalPickCard
                      key={def.id}
                      def={def}
                      selected={comboMode ? comboLegs.some(l => l.def.id === def.id) : target?.kind === 'catalog' && target.def.id === def.id}
                      onClick={() => selectSignal(def)}
                    />
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Right column — create alert form on top, existing alerts below (mirrors the
            left column's chart/catalog split — same row heights as chart/catalog) */}
        <div className="flex flex-col gap-3 overflow-hidden" style={{ flex: 1, minWidth: 360 }}>

          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ flex: '0 0 65%', minHeight: 0 }}>
            <div className="border-b border-white/[.06] bg-emerald-500/[.07] px-4 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-emerald-300/70">Новое оповещение</span>
            </div>
            <div className="flex-1 overflow-auto p-4">
              {!target ? (
                <div className="flex h-full items-center justify-center text-center text-[12px] text-slate-500">
                  Выберите сигнал в каталоге ниже
                </div>
              ) : (
                <div className="space-y-3">
                  <div>
                    <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Сигнал</div>
                    <div className="rounded-lg border border-[#5b8cff]/30 bg-[#5b8cff]/[.08] px-2.5 py-1.5 text-[12px] font-semibold text-[#b8c8ff]">
                      {target.kind === 'catalog' ? target.def.name : target.cs.name}
                    </div>
                  </div>

                  <div>
                    <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Монета и таймфрейм</div>
                    <div className="rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-1.5 text-[12px] font-semibold text-slate-200">
                      {symbol} <span className="text-slate-500">· {timeframe}</span>
                      <span className="ml-1.5 font-normal text-slate-600">— задаётся на графике слева</span>
                    </div>
                  </div>

                  {target.kind === 'catalog' && target.def.params.length > 0 && (
                    <div>
                      <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Параметры</div>
                      <div className="rounded-lg border border-white/[.06] bg-black/20 px-2.5">
                        {target.def.params.map(p => p.kind === 'number' ? (
                          <ParamNumber
                            key={p.key}
                            label={p.label}
                            hint={p.hint}
                            value={Number(params[p.key] ?? 0)}
                            step={p.step}
                            min={p.min}
                            max={p.max}
                            suffix={p.suffix}
                            decimals={p.decimals}
                            onChange={v => setParam(p.key, v)}
                          />
                        ) : (
                          <ParamSegmented
                            key={p.key}
                            label={p.label}
                            hint={p.hint}
                            value={String(params[p.key] ?? p.options[0])}
                            options={p.options}
                            onChange={v => setParam(p.key, v)}
                          />
                        ))}
                      </div>
                    </div>
                  )}

                  {target.kind === 'custom' && (
                    <div>
                      <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Состав (по И)</div>
                      <div className="rounded-lg border border-white/[.06] bg-black/20 px-2.5 py-2 text-[11px] text-slate-400">
                        {target.cs.components.map(c => c.signal_name).join(' + ')}
                      </div>
                    </div>
                  )}

                  <div>
                    <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Платформа</div>
                    <select
                      value={platform}
                      onChange={e => setPlatform(e.target.value)}
                      className="w-full rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-1.5 text-[12px] text-slate-200 outline-none"
                    >
                      {PLATFORMS.map(p => <option key={p} value={p}>{p}</option>)}
                    </select>
                  </div>

                  {formError && <p className="text-[11px] text-rose-400">{formError}</p>}

                  <button
                    type="button"
                    onClick={handleCreate}
                    disabled={creating}
                    className="w-full rounded-lg bg-gradient-to-b from-[#4a7dff] to-[#3a67e6] py-2 text-[12px] font-semibold text-white disabled:opacity-50"
                  >
                    {creating ? 'Создаём…' : 'Создать оповещение'}
                  </button>
                </div>
              )}
            </div>
          </div>

          {/* Existing alerts — aligned with the signal catalog box on the left */}
          <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ flex: 1, minHeight: 0 }}>
            <div className="flex-shrink-0 border-b border-white/[.06] px-4 py-2.5">
              <span className="text-xs font-semibold uppercase tracking-wider text-slate-500">
                Мои оповещения <span className="font-mono text-slate-600">{webhooks.length}</span>
              </span>
            </div>
            <div className="flex-1 overflow-auto p-3">
              {error && <p className="mb-2 text-[11px] text-rose-400">{error}</p>}
              {webhooks.length === 0 ? (
                <p className="py-6 text-center text-[12px] text-slate-500">Оповещений пока нет</p>
              ) : (
                <div className="space-y-2">
                  {webhooks.map(wh => (
                    <AlertRow key={wh.id} wh={wh} onToggle={v => handleToggle(wh, v)} onDelete={() => handleDelete(wh.id)} />
                  ))}
                </div>
              )}
            </div>
          </div>

        </div>
      </div>
    </div>
  )
}
