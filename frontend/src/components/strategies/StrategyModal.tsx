import { useState, useEffect } from 'react'
import { TemplateSelector } from './TemplateSelector'
import { createStrategy, updateStrategy } from '../../api/strategies'
import { listAccounts } from '../../api/accounts'
import type { Strategy, StrategyFormData, GridStep, SignalConfig, ExchangeAccount } from '../../types'

const AVAILABLE_SIGNALS = [
  'RSI', 'MACD', 'EMA', 'SMA', 'Bollinger', 'Stochastic', 'ADX',
  'CCI', 'ATR', 'OBV', 'Williams %R', 'MFI', 'SAR', 'Ichimoku',
  'Supertrend', 'Volume Spike', 'ATR Breakout', 'Divergence',
]

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

function defaultForm(): StrategyFormData {
  return {
    account_id: '',
    symbol: '',
    category: 'linear',
    direction: 'long',
    strategy_type: 'grid',
    leverage: 5,
    grid_size_usdt: 100,
    margin_type: 'isolated',
    hedge_mode: false,
    steps: [
      { price_move_pct: 1.0, lots: 1 },
      { price_move_pct: 1.5, lots: 2 },
      { price_move_pct: 2.0, lots: 2 },
    ],
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
    leverage: s.leverage,
    grid_size_usdt: s.grid_size_usdt,
    margin_type: s.margin_type,
    hedge_mode: s.hedge_mode,
    steps: s.steps ?? [{ price_move_pct: s.grid_step_pct, lots: 1 }],
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
  onClose: () => void
  onSaved: () => void
}

export function StrategyModal({ strategy, onClose, onSaved }: Props) {
  const [tab, setTab] = useState(0)
  const [form, setForm] = useState<StrategyFormData>(
    strategy ? strategyToForm(strategy) : defaultForm()
  )
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [signalPickerOpen, setSignalPickerOpen] = useState(false)

  useEffect(() => {
    listAccounts().then(setAccounts).catch(() => {})
  }, [])

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

  function addSignal(name: string) {
    if (form.signal_configs.find(s => s.name === name)) return
    patch({ signal_configs: [...form.signal_configs, { name, params: {} }] })
    setSignalPickerOpen(false)
  }

  function removeSignal(name: string) {
    patch({ signal_configs: form.signal_configs.filter(s => s.name !== name) })
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
        grid_active: form.steps.length || 1,
        grid_step_pct: form.steps[0]?.price_move_pct ?? 0,
        signal_filter: false,
      }
      if (strategy) {
        await updateStrategy(strategy.id, payload as any)
      } else {
        await createStrategy(payload as any)
      }
      onSaved()
    } catch (e: any) {
      setError(e?.response?.data?.error ?? 'Ошибка сохранения')
    } finally {
      setSaving(false)
    }
  }

  const tabLabels = ['1. Вход', '2. Усреднение', '3. Выход']
  const inputCls = 'w-full bg-gray-800 border border-gray-700 rounded-md px-3 py-1.5 text-sm text-gray-100 focus:outline-none focus:border-blue-500'
  const labelCls = 'block text-xs text-gray-500 mb-1'

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
                  <label className={labelCls}>Символ</label>
                  <input
                    value={form.symbol}
                    onChange={e => patch({ symbol: e.target.value.toUpperCase() })}
                    placeholder="BTCUSDT"
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>Аккаунт</label>
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
                <label className={labelCls}>Направление</label>
                <Toggle
                  options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }]}
                  value={form.direction}
                  onChange={v => patch({ direction: v as 'long' | 'short' })}
                />
              </div>

              <div>
                <label className={labelCls}>Тип стратегии</label>
                <Toggle
                  options={[
                    { label: 'Grid', value: 'grid' },
                    { label: 'DCA', value: 'dca' },
                    { label: 'Manual', value: 'manual' },
                  ]}
                  value={form.strategy_type}
                  onChange={v => patch({ strategy_type: v as StrategyFormData['strategy_type'] })}
                />
              </div>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>Плечо (×)</label>
                  <input
                    type="number" min={1} max={100}
                    value={form.leverage}
                    onChange={e => patch({ leverage: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>Объём 1 лота (USDT)</label>
                  <input
                    type="number" min={1}
                    value={form.grid_size_usdt}
                    onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                    className={inputCls}
                  />
                </div>
              </div>

              <div>
                <label className={labelCls}>Тип маржи</label>
                <Toggle
                  options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
                  value={form.margin_type}
                  onChange={v => patch({ margin_type: v as 'isolated' | 'cross' })}
                />
              </div>

              <div>
                <label className={labelCls}>Хедж режим</label>
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
                {form.steps.map((step, i) => (
                  <div
                    key={i}
                    className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 items-center py-1.5 border-b border-gray-800/60 last:border-0"
                  >
                    <div className="text-center text-[10px] text-gray-600">{i + 1}</div>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={step.price_move_pct}
                      onChange={e => updateStep(i, 'price_move_pct', Number(e.target.value))}
                      className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                    />
                    <input
                      type="number" step="1" min="1"
                      value={step.lots}
                      onChange={e => updateStep(i, 'lots', Number(e.target.value))}
                      className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                    />
                    <div className="text-center text-[11px] text-gray-500">
                      {(step.lots * form.grid_size_usdt).toFixed(0)}
                    </div>
                    <button
                      onClick={() => removeStep(i)}
                      className="text-red-500 hover:text-red-400 text-center text-sm"
                    >
                      ✕
                    </button>
                  </div>
                ))}
                <button
                  onClick={addStep}
                  className="mt-2 w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
                >
                  + Добавить шаг
                </button>
              </div>

              <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800 mt-2">
                Сигналы для усреднения
              </div>
              <div className="flex flex-wrap gap-2">
                {form.signal_configs.map(sc => (
                  <div
                    key={sc.name}
                    className="flex items-center gap-1.5 bg-blue-900/20 border border-blue-800/50 rounded-md px-2.5 py-1"
                  >
                    <span className="text-blue-300 text-[11px]">{sc.name}</span>
                    <button
                      onClick={() => removeSignal(sc.name)}
                      className="text-gray-600 hover:text-gray-400 text-xs"
                    >
                      ✕
                    </button>
                  </div>
                ))}
                <div className="relative">
                  <button
                    onClick={() => setSignalPickerOpen(v => !v)}
                    className="border border-dashed border-blue-800 text-blue-500 rounded-md px-2.5 py-1 text-[11px] hover:border-blue-600 transition-colors"
                  >
                    + Добавить
                  </button>
                  {signalPickerOpen && (
                    <div className="absolute top-full mt-1 left-0 bg-gray-800 border border-gray-600 rounded-lg z-10 w-48 shadow-xl max-h-48 overflow-y-auto">
                      {AVAILABLE_SIGNALS
                        .filter(name => !form.signal_configs.find(s => s.name === name))
                        .map(name => (
                          <button
                            key={name}
                            onClick={() => addSignal(name)}
                            className="w-full text-left px-3 py-1.5 text-[11px] text-gray-300 hover:bg-gray-700 border-b border-gray-700/50 last:border-0"
                          >
                            {name}
                          </button>
                        ))}
                    </div>
                  )}
                </div>
              </div>
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
                    <label className={labelCls}>TP %</label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.tp_pct}
                      onChange={e => patch({ tp_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Режим</label>
                    <Toggle
                      options={[{ label: 'total', value: 'total' }, { label: 'per level', value: 'per_level' }]}
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
                    <label className={labelCls}>SL %</label>
                    <input
                      type="number" step="0.1" min="0.1"
                      value={form.sl_pct}
                      onChange={e => patch({ sl_pct: Number(e.target.value) })}
                      className={inputCls}
                    />
                  </div>
                  <div>
                    <label className={labelCls}>Тип</label>
                    <Toggle
                      options={[
                        { label: 'conditional', value: 'conditional' },
                        { label: 'market', value: 'programmatic' },
                      ]}
                      value={form.sl_type}
                      onChange={v => patch({ sl_type: v as 'conditional' | 'programmatic' })}
                    />
                  </div>
                </div>
              </div>

              {/* Trailing stop */}
              <div className="space-y-3">
                <div className="text-[10px] text-gray-500 uppercase tracking-wider pb-1 border-b border-gray-800">
                  Трейлинг-стоп
                </div>
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => patch({ trailing_stop_enabled: !form.trailing_stop_enabled })}
                    className={`w-10 h-5 rounded-full relative transition-colors ${
                      form.trailing_stop_enabled ? 'bg-green-600' : 'bg-gray-700'
                    }`}
                  >
                    <span className={`absolute top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                      form.trailing_stop_enabled ? 'translate-x-5' : 'translate-x-0.5'
                    }`} />
                  </button>
                  <span className={`text-xs ${form.trailing_stop_enabled ? 'text-green-400' : 'text-gray-500'}`}>
                    {form.trailing_stop_enabled ? 'Включён' : 'Выключен'}
                  </span>
                </div>
                {form.trailing_stop_enabled && (
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>Активация %</label>
                      <input
                        type="number" step="0.1" min="0.1"
                        value={form.trailing_activation_pct}
                        onChange={e => patch({ trailing_activation_pct: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>Callback %</label>
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
          <div className="px-5 py-2 text-sm text-red-400 border-t border-gray-700">✗ {error}</div>
        )}
        <div className="flex justify-end gap-2 px-5 py-3 border-t border-gray-700">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm bg-gray-800 text-gray-300 rounded-lg hover:bg-gray-700 transition-colors"
          >
            Отмена
          </button>
          <button
            onClick={handleSubmit}
            disabled={saving}
            className="px-4 py-2 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 transition-colors"
          >
            {saving ? 'Сохранение…' : strategy ? 'Сохранить' : 'Создать стратегию'}
          </button>
        </div>
      </div>
    </div>
  )
}
