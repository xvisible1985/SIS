import { useEffect, useState } from 'react'
import { listWebhooks, createWebhook, updateWebhook, deleteWebhook, listWebhookLogs } from '../api/webhooks'
import { SIGNALS } from '../features/indicators/signals'
import { ParamNumber, ParamSegmented } from '../features/indicators/components/Params'
import { SignalPickCard } from '../features/indicators/components/SignalPickCard'
import { useEnabledSignals } from '../features/indicators/useEnabledSignals'
import { SignalPreviewChart } from '../features/webhooks/SignalPreviewChart'
import type { SignalDef } from '../features/indicators/types'
import type { Webhook, WebhookLog } from '../types'

// bybit-news has no pkg/signal registry entry (it's a fundamental trigger, not a
// candle-based computation) — the backend rejects it at creation time regardless, this
// just keeps it off the picker so the rejection is never hit.
const ALERTABLE_SIGNALS = SIGNALS.filter(s => s.id !== 'bybit-news')

const PLATFORMS = ['custom', 'tradingview', '3commas', 'alertatron']
const COINS = [
  'BTCUSDT', 'ETHUSDT', 'BNBUSDT', 'SOLUSDT', 'XRPUSDT',
  'ADAUSDT', 'DOGEUSDT', 'AVAXUSDT', 'DOTUSDT', 'MATICUSDT',
]
const TIMEFRAMES = [
  { label: '1м', value: '1m' }, { label: '5м', value: '5m' },
  { label: '15м', value: '15m' }, { label: '30м', value: '30m' },
  { label: '1ч', value: '1h' }, { label: '4ч', value: '4h' },
]

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

export function WebhooksPage() {
  const enabledIds = useEnabledSignals()
  const catalog = enabledIds ? ALERTABLE_SIGNALS.filter(s => enabledIds.has(s.id)) : ALERTABLE_SIGNALS

  const [webhooks, setWebhooks] = useState<Webhook[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [selected, setSelected] = useState<SignalDef | null>(null)
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [timeframe, setTimeframe] = useState('15m')
  const [platform, setPlatform] = useState('custom')
  const [params, setParams] = useState<Record<string, unknown>>({})
  const [creating, setCreating] = useState(false)
  const [formError, setFormError] = useState('')

  useEffect(() => {
    listWebhooks()
      .then(setWebhooks)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Не удалось загрузить'))
      .finally(() => setLoading(false))
  }, [])

  function selectSignal(def: SignalDef) {
    setSelected(def)
    setParams(def.defaults)
    setFormError('')
  }

  function setParam(key: string, v: unknown) {
    setParams(prev => ({ ...prev, [key]: v }))
  }

  async function handleCreate() {
    if (!selected) { setFormError('Выберите сигнал слева'); return }
    if (!symbol.trim()) { setFormError('Укажите монету'); return }
    setFormError('')
    setCreating(true)
    try {
      const wh = await createWebhook({
        catalog_signal_id: selected.id,
        symbol: symbol.trim().toUpperCase(),
        timeframe,
        params,
        platform,
      })
      setWebhooks(prev => [wh, ...prev])
      setSelected(null)
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

  if (loading) return <p className="p-5 text-sm text-slate-500">Загрузка…</p>

  return (
    <div className="flex h-full flex-col overflow-y-auto bg-[#0a0d14] text-slate-200">
      <div className="flex h-11 flex-shrink-0 items-center gap-3 border-b border-white/[.06] px-5">
        <span className="text-sm font-semibold text-slate-100">Вебхуки</span>
        <span className="text-[11px] text-slate-500">
          Выберите сигнал, настройте оповещение — SIS сформирует URL и перешлёт срабатывание в Telegram
        </span>
      </div>

      <div className="flex flex-shrink-0 gap-3 p-3" style={{ height: 480 }}>
        {/* Left 70% — signal catalog */}
        <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ width: '70%' }}>
          <div className="border-b border-white/[.06] bg-violet-500/[.07] px-4 py-2.5">
            <span className="text-xs font-semibold uppercase tracking-wider text-violet-300/70">
              Доступные сигналы <span className="ml-1.5 font-mono text-violet-400/50">{catalog.length}</span>
            </span>
          </div>
          <div className="flex-1 overflow-auto p-3">
            <div className="grid grid-cols-[repeat(auto-fill,minmax(200px,1fr))] gap-2.5">
              {catalog.map(def => (
                <SignalPickCard key={def.id} def={def} selected={selected?.id === def.id} onClick={() => selectSignal(def)} />
              ))}
            </div>
          </div>
        </div>

        {/* Right 30% — create alert form */}
        <div className="flex flex-col overflow-hidden rounded-xl border border-white/[.07] bg-[#0d1018]" style={{ width: '30%' }}>
          <div className="border-b border-white/[.06] bg-emerald-500/[.07] px-4 py-2.5">
            <span className="text-xs font-semibold uppercase tracking-wider text-emerald-300/70">Новое оповещение</span>
          </div>
          <div className="flex-1 overflow-auto p-4">
            {!selected ? (
              <div className="flex h-full items-center justify-center text-center text-[12px] text-slate-500">
                Выберите сигнал слева
              </div>
            ) : (
              <div className="space-y-3">
                <div>
                  <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Сигнал</div>
                  <div className="rounded-lg border border-[#5b8cff]/30 bg-[#5b8cff]/[.08] px-2.5 py-1.5 text-[12px] font-semibold text-[#b8c8ff]">
                    {selected.name}
                  </div>
                </div>

                <div>
                  <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Монета</div>
                  <input
                    list="webhook-coins"
                    value={symbol}
                    onChange={e => setSymbol(e.target.value)}
                    className="w-full rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-1.5 text-[12px] text-slate-200 outline-none focus:border-[#5b8cff]/50"
                  />
                  <datalist id="webhook-coins">
                    {COINS.map(c => <option key={c} value={c} />)}
                  </datalist>
                </div>

                <div>
                  <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Таймфрейм</div>
                  <div className="flex flex-wrap gap-1">
                    {TIMEFRAMES.map(tf => (
                      <button
                        key={tf.value}
                        type="button"
                        onClick={() => setTimeframe(tf.value)}
                        className={`rounded px-2 py-1 text-[11px] transition-colors ${
                          timeframe === tf.value ? 'bg-[#5b8cff]/30 text-[#b8c8ff]' : 'bg-white/[.04] text-slate-500 hover:text-slate-300'
                        }`}
                      >
                        {tf.label}
                      </button>
                    ))}
                  </div>
                </div>

                {selected.params.length > 0 && (
                  <div>
                    <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-500">Параметры</div>
                    <div className="rounded-lg border border-white/[.06] bg-black/20 px-2.5">
                      {selected.params.map(p => p.kind === 'number' ? (
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
      </div>

      {/* Preview on chart — TradingView-style: price chart, buy/sell arrows for the clicked signal */}
      <div className="flex-shrink-0 px-3 pb-3">
        <SignalPreviewChart />
      </div>

      {/* Existing alerts */}
      <div className="flex-shrink-0 border-t border-white/[.06] p-3" style={{ maxHeight: 480, overflow: 'auto' }}>
        <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">
          Мои оповещения <span className="font-mono text-slate-600">{webhooks.length}</span>
        </div>
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
  )
}
