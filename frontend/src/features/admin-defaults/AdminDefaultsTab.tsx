import { useState, useEffect } from 'react'
import { RefreshCw } from 'lucide-react'
import { apiClient } from '../../api/client'
import { updateStrategyDefaults, invalidateStrategyDefaultsCache, updateCoinFilter, getCoinFilter } from './api'
import type { GridDefaults, MatrixDefaults, GridStep, AllStrategyDefaults, CoinFilterSettings } from './types'

// ── Local numeric input (text-based, decimal) ─────────────────────────────────

function NumInput({
  value,
  onChange,
  className,
}: {
  value: number
  onChange: (v: number) => void
  className?: string
}) {
  const [draft, setDraft] = useState<string | null>(null)
  return (
    <input
      type="text"
      inputMode="decimal"
      value={draft !== null ? draft : value}
      onChange={e => {
        setDraft(e.target.value)
        const n = parseFloat(e.target.value)
        if (!isNaN(n)) onChange(n)
      }}
      onBlur={() => {
        if (draft !== null) {
          const n = parseFloat(draft)
          if (!isNaN(n)) onChange(n)
        }
        setDraft(null)
      }}
      onFocus={() => setDraft(String(value))}
      className={
        className ??
        'w-full rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200'
      }
    />
  )
}

// ── Field row helper ──────────────────────────────────────────────────────────

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-3">
      <label className="w-48 shrink-0 text-sm text-slate-400">{label}</label>
      <div className="flex-1">{children}</div>
    </div>
  )
}

// ── Grid section ──────────────────────────────────────────────────────────────

function GridSection({
  initial,
  onSaved,
}: {
  initial: GridDefaults
  onSaved: () => void
}) {
  const [d, setD] = useState<GridDefaults>(initial)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { setD(initial) }, [initial])

  function patchStep(i: number, field: keyof GridStep, value: number) {
    setD(prev => {
      const steps = [...(prev.steps ?? [])]
      steps[i] = { ...steps[i], [field]: value }
      return { ...prev, steps }
    })
  }

  function addStep() {
    setD(prev => ({
      ...prev,
      steps: [...(prev.steps ?? []), { price_move_pct: 0, size_pct: 100 }],
    }))
  }

  function removeStep(i: number) {
    setD(prev => ({
      ...prev,
      steps: (prev.steps ?? []).filter((_, idx) => idx !== i),
    }))
  }

  async function handleSave() {
    setSaving(true)
    setError(null)
    try {
      await updateStrategyDefaults('grid', d)
      setSaved(true)
      onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="rounded-lg border border-slate-700 bg-slate-900/50 p-5">
      <h3 className="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-300">
        Grid
      </h3>

      <div className="space-y-3">
        <Field label="Плечо (leverage)">
          <NumInput value={d.leverage ?? 5} onChange={v => setD(p => ({ ...p, leverage: v }))} />
        </Field>
        <Field label="Депозит (grid_size_usdt)">
          <NumInput value={d.grid_size_usdt ?? 100} onChange={v => setD(p => ({ ...p, grid_size_usdt: v }))} />
        </Field>
        <Field label="TP %">
          <NumInput value={d.tp_pct ?? 2.0} onChange={v => setD(p => ({ ...p, tp_pct: v }))} />
        </Field>
        <Field label="SL %">
          <NumInput value={d.sl_pct ?? 5.0} onChange={v => setD(p => ({ ...p, sl_pct: v }))} />
        </Field>
        <Field label="Трейлинг активация %">
          <NumInput value={d.trailing_activation_pct ?? 1.5} onChange={v => setD(p => ({ ...p, trailing_activation_pct: v }))} />
        </Field>
        <Field label="Трейлинг callback %">
          <NumInput value={d.trailing_callback_pct ?? 0.5} onChange={v => setD(p => ({ ...p, trailing_callback_pct: v }))} />
        </Field>
      </div>

      {/* Steps table */}
      <div className="mt-5">
        <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">
          Шаги
        </div>
        <div className="overflow-x-auto rounded border border-slate-700">
          <table className="w-full text-sm">
            <thead className="bg-slate-800 text-xs uppercase text-slate-400">
              <tr>
                <th className="w-10 px-3 py-2 text-left">#</th>
                <th className="px-3 py-2 text-left">Движение %</th>
                <th className="px-3 py-2 text-left">Лотов %</th>
                <th className="w-10 px-3 py-2" />
              </tr>
            </thead>
            <tbody>
              {(d.steps ?? []).map((step, i) => (
                <tr key={i} className="border-t border-slate-800">
                  <td className="px-3 py-2 text-slate-500">{i + 1}</td>
                  <td className="px-3 py-2">
                    <NumInput
                      value={step.price_move_pct}
                      onChange={v => patchStep(i, 'price_move_pct', v)}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <NumInput
                      value={step.size_pct}
                      onChange={v => patchStep(i, 'size_pct', v)}
                    />
                  </td>
                  <td className="px-3 py-2">
                    <button
                      type="button"
                      onClick={() => removeStep(i)}
                      className="rounded px-1.5 py-0.5 text-xs text-slate-500 hover:bg-rose-500/10 hover:text-rose-400"
                    >
                      ✕
                    </button>
                  </td>
                </tr>
              ))}
              {(d.steps ?? []).length === 0 && (
                <tr>
                  <td colSpan={4} className="px-3 py-4 text-center text-xs text-slate-600">
                    Нет шагов
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <button
          type="button"
          onClick={addStep}
          className="mt-2 rounded border border-slate-700 bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700"
        >
          + Добавить шаг
        </button>
      </div>

      {error && (
        <div className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
          {error}
        </div>
      )}

      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          {saving ? 'Сохранение…' : 'Сохранить'}
        </button>
        {saved && (
          <span className="text-sm text-emerald-400">Сохранено ✓</span>
        )}
      </div>
    </div>
  )
}

// ── Matrix section ────────────────────────────────────────────────────────────

function MatrixSection({
  initial,
  onSaved,
}: {
  initial: MatrixDefaults
  onSaved: () => void
}) {
  const [d, setD] = useState<MatrixDefaults>(initial)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { setD(initial) }, [initial])

  async function handleSave() {
    setSaving(true)
    setError(null)
    try {
      await updateStrategyDefaults('matrix', d)
      setSaved(true)
      onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="rounded-lg border border-slate-700 bg-slate-900/50 p-5">
      <h3 className="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-300">
        Matrix
      </h3>

      <div className="space-y-3">
        <Field label="Плечо (leverage)">
          <NumInput value={d.leverage ?? 5} onChange={v => setD(p => ({ ...p, leverage: v }))} />
        </Field>
        <Field label="Депозит (grid_size_usdt)">
          <NumInput value={d.grid_size_usdt ?? 100} onChange={v => setD(p => ({ ...p, grid_size_usdt: v }))} />
        </Field>
        <Field label="Safe Zone %">
          <NumInput value={d.safe_zone_pct ?? 1.5} onChange={v => setD(p => ({ ...p, safe_zone_pct: v }))} />
        </Field>
      </div>

      {error && (
        <div className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
          {error}
        </div>
      )}

      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          {saving ? 'Сохранение…' : 'Сохранить'}
        </button>
        {saved && (
          <span className="text-sm text-emerald-400">Сохранено ✓</span>
        )}
      </div>
    </div>
  )
}

// ── Coin filter section ───────────────────────────────────────────────────────

function CoinFilterSection({
  initial,
  onSaved,
}: {
  initial: CoinFilterSettings
  onSaved: () => void
}) {
  const [d, setD]                     = useState<CoinFilterSettings>(initial)
  const [blacklistInput, setInput]    = useState('')
  const [saving, setSaving]           = useState(false)
  const [saved, setSaved]             = useState(false)
  const [error, setError]             = useState<string | null>(null)

  useEffect(() => { setD(initial) }, [initial])

  function addToBlacklist() {
    const sym = blacklistInput.trim().toUpperCase()
    if (!sym || d.blacklist.includes(sym)) { setInput(''); return }
    setD(p => ({ ...p, blacklist: [...p.blacklist, sym] }))
    setInput('')
  }

  function removeFromBlacklist(sym: string) {
    setD(p => ({ ...p, blacklist: p.blacklist.filter(s => s !== sym) }))
  }

  async function handleSave() {
    setSaving(true); setError(null)
    try {
      await updateCoinFilter(d)
      setSaved(true); onSaved()
      setTimeout(() => setSaved(false), 2000)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally { setSaving(false) }
  }

  return (
    <div className="rounded-lg border border-slate-700 bg-slate-900/50 p-5 lg:col-span-2">
      <h3 className="mb-4 text-sm font-semibold uppercase tracking-wider text-slate-300">
        Фильтр монет
      </h3>

      <div className="space-y-4">
        <Field label="Мин. объём 24ч (USDT)">
          <NumInput
            value={d.min_turnover_usdt}
            onChange={v => setD(p => ({ ...p, min_turnover_usdt: v }))}
          />
        </Field>

        <div>
          <label className="mb-2 block text-sm text-slate-400">Чёрный список</label>
          <div className="flex gap-2 mb-2">
            <input
              type="text"
              value={blacklistInput}
              onChange={e => setInput(e.target.value.toUpperCase())}
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); addToBlacklist() } }}
              placeholder="PEPEUSDT"
              className="flex-1 rounded border border-slate-700 bg-slate-800 px-2 py-1.5 text-sm text-slate-200 placeholder-slate-600"
            />
            <button
              type="button"
              onClick={addToBlacklist}
              className="rounded bg-slate-700 px-3 py-1.5 text-sm text-slate-200 hover:bg-slate-600"
            >
              + Добавить
            </button>
          </div>
          <div className="flex flex-wrap gap-1.5 min-h-[1.75rem]">
            {d.blacklist.map(sym => (
              <span
                key={sym}
                className="inline-flex items-center gap-1 rounded border border-rose-500/30 bg-rose-500/10 px-2 py-0.5 text-xs font-mono text-rose-300"
              >
                {sym}
                <button
                  type="button"
                  onClick={() => removeFromBlacklist(sym)}
                  className="text-rose-400 hover:text-rose-200 leading-none"
                >
                  ✕
                </button>
              </span>
            ))}
            {d.blacklist.length === 0 && (
              <span className="text-xs text-slate-600 leading-[1.75rem]">Пусто</span>
            )}
          </div>
        </div>
      </div>

      {error && (
        <div className="mt-3 rounded border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-400">
          {error}
        </div>
      )}

      <div className="mt-4 flex items-center gap-3">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          {saving ? 'Сохранение…' : 'Сохранить'}
        </button>
        {saved && <span className="text-sm text-emerald-400">Сохранено ✓</span>}
      </div>
    </div>
  )
}

// ── Main tab component ────────────────────────────────────────────────────────

export function AdminDefaultsTab() {
  const [defaults, setDefaults] = useState<AllStrategyDefaults | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [coinFilter, setCoinFilter] = useState<CoinFilterSettings | null>(null)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const [defaultsRes, filterRes] = await Promise.all([
        apiClient.get<AllStrategyDefaults>('/admin/strategy-defaults'),
        getCoinFilter(),
      ])
      setDefaults(defaultsRes.data ?? {})
      setCoinFilter(filterRes)
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  function handleSaved() {
    // After saving, invalidate the shared cache so BotForm picks up new defaults
    invalidateStrategyDefaultsCache()
  }

  return (
    <div className="space-y-6 p-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-semibold text-slate-200">
          Дефолтные значения форм стратегий
        </h2>
        <button
          type="button"
          onClick={load}
          disabled={loading}
          className="ml-auto flex items-center gap-1.5 rounded border border-slate-700 bg-slate-800 px-2.5 py-1 text-xs text-slate-300 hover:bg-slate-700 disabled:opacity-50"
        >
          <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
          Обновить
        </button>
      </div>

      {error && (
        <div className="rounded border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-400">
          {error}
        </div>
      )}

      {loading && !defaults && (
        <div className="py-10 text-center text-sm text-slate-500">Загрузка…</div>
      )}

      {defaults && (
        <div className="grid gap-6 lg:grid-cols-2">
          <GridSection
            initial={defaults.grid ?? {}}
            onSaved={handleSaved}
          />
          <MatrixSection
            initial={defaults.matrix ?? {}}
            onSaved={handleSaved}
          />
          {coinFilter && (
            <CoinFilterSection initial={coinFilter} onSaved={handleSaved} />
          )}
        </div>
      )}
    </div>
  )
}
