import { useState, useEffect } from 'react'
import { TemplateSelector } from './TemplateSelector'
import { Toggle, Tip, SignalPickerField } from './FormWidgets'
import { createStrategy, updateStrategy, getStrategyState, getInstrumentConstraints, type InstrumentConstraints } from '../../api/strategies'
import { CoinPicker } from '../common/CoinPicker'
import type { Strategy, StrategyFormData, GridStep, SignalConfig } from '../../types'

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
  defaultAccountId?: string
  onClose: () => void
  onSaved: () => void
}

export function StrategyModal({ strategy, filledLevels: filledLevelsProp = 0, defaultAccountId, onClose, onSaved }: Props) {
  const [tab, setTab] = useState(0)
  const [form, setForm] = useState<StrategyFormData>(
    strategy ? strategyToForm(strategy) : { ...defaultForm(), account_id: defaultAccountId ?? '' }
  )
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [filledLevels, setFilledLevels] = useState(filledLevelsProp)
  const [instrInfo, setInstrInfo] = useState<InstrumentConstraints | null>(null)

  // Fetch live filled count when editing — card state may not be loaded yet.
  useEffect(() => {
    if (!strategy) return
    getStrategyState(strategy.id)
      .then(state => setFilledLevels(state.levels?.filter(l => l.status === 'filled').length ?? 0))
      .catch(() => {})
  }, [strategy?.id])

  // Fetch instrument constraints whenever symbol or category changes.
  useEffect(() => {
    if (!form.symbol) return
    getInstrumentConstraints(form.symbol, form.category).then(setInstrInfo).catch(() => {})
  }, [form.symbol, form.category])

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
    if (instrInfo) {
      if (form.leverage > instrInfo.max_leverage) {
        setError(`Максимальное плечо для ${form.symbol}: ×${instrInfo.max_leverage}`)
        return
      }
      if (instrInfo.min_notional_value > 0 && form.grid_size_usdt < instrInfo.min_notional_value) {
        setError(`Минимальный размер ордера для ${form.symbol}: ${instrInfo.min_notional_value}$`)
        return
      }
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
              <div>
                <label className={labelCls}>Символ<Tip text="Торговый инструмент — монета или контракт, которым будет управлять стратегия. После создания стратегии сменить монету нельзя." /></label>
                <CoinPicker
                  value={form.symbol || 'BTCUSDT'}
                  onChange={sym => patch({ symbol: sym })}
                  triggerClassName="w-full px-3 py-1.5 text-sm rounded-md border-gray-700 bg-gray-800"
                  disabled={!!strategy}
                  disabledTooltip="Монета выбирается только при создании стратегии. Для смены монеты создайте новую стратегию."
                />
              </div>

              <div>
                <label className={labelCls}>Направление<Tip text="Long — покупаешь в расчёте на рост цены. Short — продаёшь в расчёте на падение. Сетка усреднения строится в выбранную сторону." /></label>
                <Toggle
                  options={[{ label: 'Long', value: 'long' }, { label: 'Short', value: 'short' }]}
                  value={form.direction}
                  onChange={v => patch({ direction: v as 'long' | 'short' })}
                  optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white' }}
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
                  <label className={labelCls}>
                    Плечо (×)
                    {instrInfo && <span className="ml-1.5 text-[10px] text-gray-600">макс. ×{instrInfo.max_leverage}</span>}
                    <Tip text="Кратность заёмных средств. Плечо ×5 означает, что на $100 маржи открывается позиция на $500. Чем выше плечо — тем больше потенциальная прибыль и тем ближе ликвидация." />
                  </label>
                  <input
                    type="number" min={1} max={instrInfo?.max_leverage ?? 100}
                    value={form.leverage}
                    onChange={e => patch({ leverage: Math.min(Number(e.target.value), instrInfo?.max_leverage ?? 100) })}
                    className={inputCls}
                  />
                </div>
                <div>
                  <label className={labelCls}>
                    Объём 1 лота (USDT)
                    {instrInfo?.min_notional_value ? <span className="ml-1.5 text-[10px] text-gray-600">мин. {instrInfo.min_notional_value}$</span> : null}
                    <Tip text="Размер одного базового ордера в USDT. Итоговый объём каждого уровня сетки = лоты шага × этот размер. Например, при 100 USDT/лот и 2 лотах на шаге — ордер на 200 USDT." />
                  </label>
                  <input
                    type="number" min={instrInfo?.min_notional_value ?? 1}
                    value={form.grid_size_usdt}
                    onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                    className={instrInfo && form.grid_size_usdt < instrInfo.min_notional_value ? `${inputCls} border-red-500` : inputCls}
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
