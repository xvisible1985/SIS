import { useState, useEffect } from 'react'
import { TemplateSelector } from './TemplateSelector'
import { Toggle, Tip, SignalPickerField } from './FormWidgets'
import { createStrategy, updateStrategy, getStrategyState, getInstrumentConstraints, type InstrumentConstraints } from '../../api/strategies'
import { CoinPicker } from '../common/CoinPicker'
import type { Strategy, StrategyFormData, GridStep, MatrixLevel, MatrixEntryLevel } from '../../types'

const DEFAULT_MATRIX_LEVELS: MatrixLevel[] = [
  { direction: 'below', price_step_pct: 1.5, size_pct: 5,  stop_pct: -3.0, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 2.5 },
  { direction: 'below', price_step_pct: 3.0, size_pct: 8,  stop_pct: -2.5, stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 3.5 },
  { direction: 'below', price_step_pct: 5.0, size_pct: 12, stop_pct: null,  stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 4.0 },
  { direction: 'above', price_step_pct: 2.0, size_pct: 5,  stop_pct: -3.5, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 3.0 },
  { direction: 'above', price_step_pct: 4.0, size_pct: 8,  stop_pct: -3.0, stop_cond_pct: -1.5, stop_replace_pct: -1.5, tp_pct: 4.5 },
]

const DEFAULT_MATRIX_ENTRY: MatrixEntryLevel = {
  size_pct: 10, stop_pct: -5.0, stop_cond_pct: -2.0, stop_replace_pct: 1.0, tp_pct: 2.0,
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
    sl_pct: -5.0,
    sl_type: 'conditional',
    trailing_stop_enabled: false,
    trailing_activation_pct: 1.5,
    trailing_callback_pct: 0.5,
    after_stop_mode: 'restart',
    matrix_levels: DEFAULT_MATRIX_LEVELS,
    safe_zone_pct: 1.5,
    matrix_entry_level: DEFAULT_MATRIX_ENTRY,
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
    after_stop_mode: s.after_stop_mode ?? 'restart',
    matrix_levels: (s.matrix_levels ?? DEFAULT_MATRIX_LEVELS).map(l => ({
      direction: (l as any).direction ?? 'below',
      price_step_pct: l.price_step_pct,
      size_pct: (l as any).size_pct ?? 0,
      stop_pct: l.stop_pct ?? null,
      stop_cond_pct: (l as any).stop_cond_pct ?? null,
      stop_replace_pct: (l as any).stop_replace_pct ?? null,
      tp_pct: (l as any).tp_pct ?? null,
      order_type: (l as any).order_type ?? 'exchange',
      use_signal: (l as any).use_signal ?? false,
    })),
    safe_zone_pct: s.safe_zone_pct ?? 1.5,
    matrix_entry_level: s.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY,
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
  const [minLotEnabled, setMinLotEnabled] = useState(false)

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

  // Если включён min-lot — форсируем grid_size_usdt
  useEffect(() => {
    if (minLotEnabled && instrInfo && instrInfo.min_order_usdt > 0) {
      patch({ grid_size_usdt: instrInfo.min_order_usdt })
    }
  }, [minLotEnabled, instrInfo])

  function patch(partial: Partial<StrategyFormData>) {
    setForm(f => ({ ...f, ...partial }))
  }

  function addStep() {
    patch({ steps: [...form.steps, { price_move_pct: 1.0, lots: 1 }] })
  }

  function removeStep(i: number) {
    patch({ steps: form.steps.filter((_, idx) => idx !== i) })
  }

  function updateStep(i: number, field: keyof GridStep, value: number | string | boolean) {
    const steps = form.steps.map((s, idx) =>
      idx === i ? { ...s, [field]: value } : s
    )
    patch({ steps })
  }

  const aboveLevels = form.matrix_levels.filter(l => l.direction === 'above')
  const belowLevels = form.matrix_levels.filter(l => l.direction === 'below')

  function addAboveLevel() {
    const last = aboveLevels[aboveLevels.length - 1]
    const newLevel: MatrixLevel = {
      direction: 'above',
      price_step_pct: last ? +(last.price_step_pct + 2).toFixed(1) : 2.0,
      size_pct: last ? +(last.size_pct + 2).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    }
    patch({ matrix_levels: [...form.matrix_levels, newLevel] })
  }
  function addBelowLevel() {
    const last = belowLevels[belowLevels.length - 1]
    const newLevel: MatrixLevel = {
      direction: 'below',
      price_step_pct: last ? +(last.price_step_pct + 1.5).toFixed(1) : 1.5,
      size_pct: last ? +(last.size_pct + 3).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    }
    patch({ matrix_levels: [...form.matrix_levels, newLevel] })
  }
  function removeMatrixLevel(direction: 'above' | 'below', idx: number) {
    const dirLevels = form.matrix_levels.filter(l => l.direction === direction)
    if (dirLevels.length <= 1) return
    let removed = false
    patch({ matrix_levels: form.matrix_levels.filter(l => {
      if (l.direction !== direction) return true
      const thisIdx = form.matrix_levels.filter(x => x.direction === direction).indexOf(l)
      if (thisIdx === idx && !removed) { removed = true; return false }
      return true
    }) })
  }
  function updateMatrixLevel(direction: 'above' | 'below', idx: number, field: keyof MatrixLevel, value: number | string | boolean | null) {
    let dirCount = 0
    patch({ matrix_levels: form.matrix_levels.map(l => {
      if (l.direction !== direction) return l
      if (dirCount++ === idx) return { ...l, [field]: value }
      return l
    }) })
  }
  function updateEntryLevel(field: keyof MatrixEntryLevel, value: number | null) {
    patch({ matrix_entry_level: { ...form.matrix_entry_level, [field]: value } })
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
      if (instrInfo.min_order_usdt > 0 && form.grid_size_usdt < instrInfo.min_order_usdt) {
        setError(`Минимальный размер ордера для ${form.symbol}: ${instrInfo.min_order_usdt.toFixed(2)}$`)
        return
      }
    }
    setSaving(true)
    setError(null)
    try {
      const isMatrix = form.strategy_type === 'dca'
      const totalMatrixLevels = aboveLevels.length + 1 + belowLevels.length
      const payload = {
        ...form,
        grid_levels: isMatrix ? totalMatrixLevels : (form.steps.length || 1),
        grid_active: isMatrix ? totalMatrixLevels : (form.grid_active > 0 ? form.grid_active : form.steps.length || 1),
        grid_step_pct: isMatrix ? (belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0) : (form.steps[0]?.price_move_pct ?? 0),
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

  const tabLabels = ['1. Базовые', form.strategy_type === 'dca' ? '2. Матрица' : '2. Сетка ордеров', '3. Завершение']
  const inputCls = 'w-full bg-gray-800 border border-gray-700 rounded-md px-3 py-1.5 text-sm text-gray-100 focus:outline-none focus:border-blue-500'
  const labelCls = 'flex items-center text-xs text-gray-400 mb-1'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <div className="bg-gray-900 border border-gray-700 rounded-xl w-[740px] shadow-2xl flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-gray-700">
          <span className="text-gray-100 font-semibold">
            {strategy ? 'Редактировать стратегию' : 'Новая стратегия'}
          </span>
          <button onClick={onClose} className="text-gray-500 hover:text-gray-300 text-xl leading-none">✕</button>
        </div>

        {/* Template bar + strategy type switcher */}
        <TemplateSelector
          formData={form}
          onLoad={loadTemplate}
          strategyType={form.strategy_type as 'grid' | 'dca'}
          onStrategyTypeChange={t => patch({ strategy_type: t })}
        />

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

              {form.strategy_type !== 'dca' && (
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
              )}

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>
                    Плечо (×)
                    {instrInfo && <span className="ml-1.5 text-[10px] text-gray-500">макс. ×{instrInfo.max_leverage}</span>}
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
                    {instrInfo?.min_order_usdt ? <span className="ml-1.5 text-[10px] text-gray-500">мин. {instrInfo.min_order_usdt.toFixed(2)}$</span> : null}
                    <Tip text="Размер одного базового ордера в USDT. Итоговый объём каждого уровня сетки = лоты шага × этот размер. Например, при 100 USDT/лот и 2 лотах на шаге — ордер на 200 USDT." />
                  </label>
                  <div className="flex items-center gap-2">
                    <input
                      type="number" min={instrInfo?.min_order_usdt ?? 1}
                      value={form.grid_size_usdt}
                      onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                      disabled={minLotEnabled}
                      className={`${inputCls} [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none ${minLotEnabled ? 'cursor-not-allowed opacity-50' : ''} ${instrInfo && form.grid_size_usdt < instrInfo.min_order_usdt ? 'border-red-500' : ''}`}
                    />
                    <button
                      type="button"
                      onClick={() => setMinLotEnabled(v => !v)}
                      className={`shrink-0 rounded-lg border px-3 py-1.5 text-[11px] font-semibold transition-colors ${
                        minLotEnabled
                          ? 'border-blue-500/40 bg-blue-500/[.18] text-blue-400'
                          : 'border-gray-700 bg-gray-800 text-gray-400 hover:bg-gray-700'
                      }`}
                    >
                      min
                    </button>
                  </div>
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

          {/* Tab 2: Matrix */}
          {tab === 1 && form.strategy_type === 'dca' && (() => {
            const miniInput = (
              value: number | null,
              onChange: (v: number | null) => void,
              extraCls = ''
            ) => (
              <input
                type="number" step="0.1"
                value={value ?? ''}
                placeholder="—"
                onChange={e => onChange(e.target.value === '' ? null : Number(e.target.value))}
                className={`bg-gray-900 border rounded py-1 px-1 text-xs text-gray-100 text-center w-full outline-none focus:border-blue-500 ${value === null ? 'border-dashed border-gray-700 text-gray-600 placeholder-gray-700' : 'border-gray-700'} ${extraCls}`}
              />
            )
            const colHdrCls = 'text-[8px] text-gray-400 text-center flex items-center justify-center gap-0.5'
            const colGrid = 'grid grid-cols-[34px_1fr_1fr_6px_1fr_1fr_1fr_1fr_46px_40px_18px] gap-[3px] items-center'
            const sep = <div className="flex items-center justify-center"><div className="w-px h-[22px] bg-gray-700/60" /></div>
            const orderTypeBtn = (isVirtual: boolean, isFilled: boolean, onToggle: () => void) => (
              <button
                onClick={onToggle}
                disabled={isFilled}
                title={isVirtual ? 'Виртуальный — бот следит за ценой и выставляет ордер в нужный момент' : 'Биржевой — ордер сразу выставляется на биржу'}
                className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                  isFilled ? 'opacity-40 cursor-not-allowed bg-gray-800 text-gray-600 border border-gray-700' :
                  isVirtual ? 'bg-violet-950/60 text-violet-300 border border-violet-800/50 hover:bg-violet-900/60' :
                  'bg-gray-800 text-gray-500 border border-gray-700 hover:text-gray-300'
                }`}
              >
                {isVirtual ? 'Virtual' : 'Broker'}
              </button>
            )
            const colHdrs = () => (
              <div className={`${colGrid} px-1 pb-1`}>
                <span />
                <span className={colHdrCls}>Шаг %<Tip text="% от точки входа до этого уровня. L(1) = первый шаг от входа, L(2) = ещё на этот % дальше." /></span>
                <span className={colHdrCls}>Объём %<Tip text="Размер ордера как % от Депозита. Например 5% от 1000 USDT = ордер 50 USDT." /></span>
                <span />
                <span className={colHdrCls}>Стоп %<Tip text="Расстояние частичного стопа от цены взятия ордера. Ставится сразу при заполнении. Пусто = не ставить." /></span>
                <span className={colHdrCls}>Усл %<Tip text="Условие перевыставления стопа: когда цена уйдёт от ТВХ на этот %, стоп отменяется и ставится заново на расстоянии «Замена %»." /></span>
                <span className={colHdrCls}>Замена %<Tip text="Новое расстояние стопа от цены взятия после перевыставления. Отрицательное значение переводит стоп в зону прибыли." /></span>
                <span className={colHdrCls}>ТП %<Tip text="Тейкпрофит от цены входа (ТВХ). При достижении закрывает ВСЮ позицию. Пусто = не ставить." /></span>
                <span className={colHdrCls}>Тип<Tip text="Broker — ордер сразу выставляется на биржу. Virtual — бот следит за ценой и выставляет ордер в нужный момент." /></span>
                <span className={colHdrCls}>Сигн<Tip text="Signal — ордер выставляется только при активном сигнале стратегии. No — ордер выставляется независимо от сигнала." /></span>
                <span />
              </div>
            )
            const levelRow = (
              direction: 'above' | 'below',
              level: MatrixLevel,
              dirIdx: number,
              labelNum: number,
              isFilled: boolean,
              canDelete: boolean
            ) => {
              const isAbove = direction === 'above'
              const badgeCls = isAbove
                ? 'bg-emerald-900 text-emerald-300 text-[8px] font-bold rounded text-center py-0.5'
                : 'bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5'
              const rowCls = isAbove ? 'bg-emerald-950/20' : 'bg-blue-950/30'
              return (
                <div key={`${direction}-${dirIdx}`} className={`${colGrid} px-1 py-1 rounded mb-0.5 ${isFilled ? 'bg-green-950/40' : rowCls}`}>
                  <div className={badgeCls}>
                    {isFilled ? <span className="text-green-400">✓</span> : (isAbove ? `L(${labelNum})` : `L(-${labelNum})`)}
                  </div>
                  <input type="number" step="0.1" value={level.price_step_pct} disabled={isFilled}
                    onChange={e => updateMatrixLevel(direction, dirIdx, 'price_step_pct', Number(e.target.value))}
                    className={`bg-gray-900 border border-gray-700 rounded py-1 px-1 text-xs text-gray-100 text-center w-full outline-none focus:border-blue-500 ${isFilled ? 'opacity-50 cursor-not-allowed' : ''}`} />
                  <input type="number" step="0.5" value={level.size_pct} disabled={isFilled}
                    onChange={e => updateMatrixLevel(direction, dirIdx, 'size_pct', Number(e.target.value))}
                    className={`bg-gray-900 border border-gray-700 rounded py-1 px-1 text-xs text-gray-100 text-center w-full outline-none focus:border-blue-500 ${isFilled ? 'opacity-50 cursor-not-allowed' : ''}`} />
                  {sep}
                  {miniInput(level.stop_pct, v => updateMatrixLevel(direction, dirIdx, 'stop_pct', v))}
                  {miniInput(level.stop_cond_pct, v => updateMatrixLevel(direction, dirIdx, 'stop_cond_pct', v))}
                  {miniInput(level.stop_replace_pct, v => updateMatrixLevel(direction, dirIdx, 'stop_replace_pct', v))}
                  {miniInput(level.tp_pct, v => updateMatrixLevel(direction, dirIdx, 'tp_pct', v))}
                  {orderTypeBtn(level.order_type === 'virtual', isFilled, () => updateMatrixLevel(direction, dirIdx, 'order_type', level.order_type === 'virtual' ? 'exchange' : 'virtual'))}
                  <button
                    onClick={() => !isFilled && updateMatrixLevel(direction, dirIdx, 'use_signal', !level.use_signal)}
                    disabled={isFilled}
                    title={level.use_signal ? 'Выставлять уровень только при активном сигнале' : 'Выставлять уровень независимо от сигнала'}
                    className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                      isFilled ? 'opacity-40 cursor-not-allowed bg-gray-800 text-gray-600 border border-gray-700' :
                      level.use_signal ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50 hover:bg-emerald-900/60' :
                      'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'
                    }`}
                  >
                    {level.use_signal ? 'Signal' : 'No'}
                  </button>
                  <button onClick={() => removeMatrixLevel(direction, dirIdx)} disabled={isFilled || !canDelete}
                    className={`text-[11px] text-center ${isFilled || !canDelete ? 'text-gray-700 cursor-not-allowed' : 'text-red-600 hover:text-red-400'}`}>✕</button>
                </div>
              )
            }

            return (
              <>
                {/* Auto-virtual fallback notice */}
                <div className="flex items-start gap-2 bg-violet-950/30 border border-violet-800/40 rounded-lg px-3 py-2 text-[10px] text-violet-300">
                  <span className="mt-0.5 shrink-0 text-violet-400">ℹ</span>
                  <span>
                    Если биржа отклоняет ордер (нехватка баланса), уровень автоматически переходит в режим <strong>Virtual</strong> — бот будет следить за ценой и исполнит его рыночным ордером в нужный момент.
                    Устанавливайте <strong>Virtual</strong> заранее для уровней, по которым ожидаете нехватку маржи.
                  </span>
                </div>

                {/* Global params */}
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>Депозит (USDT)<Tip text="Базовая сумма в USDT, от которой рассчитывается объём каждого ордера. Объём уровня = Депозит × Объём %." /></label>
                    <input type="number" min={1} value={form.grid_size_usdt}
                      onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                      className={inputCls} />
                  </div>
                  <div>
                    <label className={labelCls}>Safe-Zone от SL %<Tip text="После срабатывания стоп-лосса — зона вокруг цены SL, в которой новые ордера матрицы не выставляются. Защита от немедленного повторного входа." /></label>
                    <input type="number" step="0.1" min={0} value={form.safe_zone_pct}
                      onChange={e => patch({ safe_zone_pct: Number(e.target.value) })}
                      className={inputCls} />
                  </div>
                </div>

                {/* ABOVE section */}
                <div>
                  <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pb-1 border-b border-gray-800 mb-1">
                    <span>Выше точки входа <span className="normal-case text-gray-600 font-normal">(в сторону)</span></span>
                    <span className="bg-emerald-900/60 text-emerald-400 rounded px-1.5 py-0.5 text-[8px]">{aboveLevels.length} уровней</span>
                  </div>
                  <button onClick={addAboveLevel}
                    className="w-full border border-dashed border-emerald-900 text-emerald-700 rounded py-1 text-[10px] hover:border-emerald-700 hover:text-emerald-400 transition-colors mb-2">
                    + Добавить уровень выше
                  </button>
                  <div>
                    {[...aboveLevels].reverse().map((level, revIdx) => {
                      const dirIdx = aboveLevels.length - 1 - revIdx
                      const labelNum = dirIdx + 1
                      return levelRow('above', level, dirIdx, labelNum, false, aboveLevels.length > 1)
                    })}
                  </div>
                </div>

                {/* ENTRY level (L0) */}
                <div className="border border-yellow-700/60 bg-yellow-950/10 rounded-lg p-2">
                  <div className="text-[9px] text-yellow-600 uppercase tracking-wider mb-1.5 flex items-center gap-1">
                    ⬡ Нулевой уровень — точка входа L(0)
                  </div>
                  {colHdrs()}
                  <div className={`${colGrid} px-1 py-1 rounded bg-yellow-950/20`}>
                    <div className="bg-yellow-800/60 text-yellow-200 text-[8px] font-bold rounded text-center py-0.5">L(0)</div>
                    <div className="bg-gray-900 border border-dashed border-gray-700 rounded py-1 text-[9px] text-gray-600 text-center">—</div>
                    <input type="number" step="0.5" value={form.matrix_entry_level.size_pct}
                      onChange={e => updateEntryLevel('size_pct', Number(e.target.value))}
                      className="bg-gray-900 border border-gray-700 rounded py-1 px-1 text-xs text-gray-100 text-center w-full outline-none focus:border-blue-500" />
                    {sep}
                    {miniInput(form.matrix_entry_level.stop_pct, v => updateEntryLevel('stop_pct', v))}
                    {miniInput(form.matrix_entry_level.stop_cond_pct, v => updateEntryLevel('stop_cond_pct', v))}
                    {miniInput(form.matrix_entry_level.stop_replace_pct, v => updateEntryLevel('stop_replace_pct', v))}
                    {miniInput(form.matrix_entry_level.tp_pct, v => updateEntryLevel('tp_pct', v))}
                    {orderTypeBtn(
                      form.matrix_entry_level.order_type === 'virtual',
                      false,
                      () => patch({ matrix_entry_level: { ...form.matrix_entry_level, order_type: form.matrix_entry_level.order_type === 'virtual' ? 'exchange' : 'virtual' } })
                    )}
                    <button
                      onClick={() => patch({ matrix_entry_level: { ...form.matrix_entry_level, use_signal: !form.matrix_entry_level.use_signal } })}
                      title={form.matrix_entry_level.use_signal ? 'Выставлять уровень только при активном сигнале' : 'Выставлять уровень независимо от сигнала'}
                      className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                        form.matrix_entry_level.use_signal
                          ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50 hover:bg-emerald-900/60'
                          : 'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'
                      }`}
                    >
                      {form.matrix_entry_level.use_signal ? 'Signal' : 'No'}
                    </button>
                    <span />

                  </div>
                </div>

                {/* BELOW section */}
                <div>
                  <div>
                    {belowLevels.map((level, dirIdx) => {
                      const isFilled = dirIdx < filledLevels
                      return levelRow('below', level, dirIdx, dirIdx + 1, isFilled, belowLevels.length > 1)
                    })}
                  </div>
                  <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pt-1 border-t border-gray-800 mt-1 mb-1">
                    <span>Ниже точки входа <span className="normal-case text-gray-600 font-normal">(против направления)</span></span>
                    <span className="bg-blue-900/60 text-blue-400 rounded px-1.5 py-0.5 text-[8px]">{belowLevels.length} уровней</span>
                  </div>
                  <button onClick={addBelowLevel}
                    className="w-full border border-dashed border-blue-900 text-blue-600 rounded py-1 text-[10px] hover:border-blue-700 hover:text-blue-400 transition-colors">
                    + Добавить уровень ниже
                  </button>
                </div>

                {/* Signals */}
                <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mt-1">
                  Сигналы для входа<Tip text="Технические индикаторы, сигнал которых должен совпасть для запуска матрицы. Если сигналов нет — матрица стартует сразу при активации." />
                </div>
                <SignalPickerField
                  configs={form.signal_configs}
                  onChange={v => patch({ signal_configs: v })}
                  onAutoSave={strategy ? (newConfigs) => {
                    updateStrategy(strategy.id, { ...form, signal_configs: newConfigs, signal_filter: newConfigs.length > 0 } as any).catch(() => {})
                  } : undefined}
                />
              </>
            )
          })()}

          {/* Tab 2: Grid (non-matrix) */}
          {tab === 1 && form.strategy_type !== 'dca' && (
            <>
              <div className="text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800">
                Шаги усреднения
              </div>
              <div>
                <div className="grid grid-cols-[28px_1fr_1fr_1fr_46px_40px_28px] gap-2 text-[10px] text-gray-400 mb-1 px-0.5">
                  <div className="flex items-center justify-center">#<Tip text="Номер шага усреднения. Шаги исполняются последовательно по мере движения цены." /></div>
                  <div className="flex items-center justify-center">Движение %<Tip text="Процент движения цены от предыдущего шага. На этом расстоянии выставляется ордер." /></div>
                  <div className="flex items-center justify-center">Лотов<Tip text="Количество лотов на этом шаге. Итоговый объём = Лотов × Объём 1 лота (USDT)." /></div>
                  <div className="flex items-center justify-center">USDT<Tip text="Итоговый объём ордера в USDT. Редактируется напрямую — автоматически пересчитывает Лотов." /></div>
                  <div className="flex items-center justify-center text-[8px]">Тип<Tip text="Broker — ордер сразу выставляется на биржу. Virtual — бот следит за ценой и выставляет ордер в нужный момент." /></div>
                  <div className="flex items-center justify-center text-[8px]">Сигн<Tip text="Signal — ордер выставляется только при активном сигнале стратегии. No — независимо от сигнала." /></div>
                  <div />
                </div>
                <div className="max-h-[216px] overflow-y-auto">
                  {form.steps.map((step, i) => {
                    const isFilled = i < filledLevels
                    const isVirtual = step.order_type === 'virtual'
                    return (
                      <div
                        key={i}
                        className={`grid grid-cols-[28px_1fr_1fr_1fr_46px_40px_28px] gap-2 items-center py-1.5 border-b border-gray-800/60 last:border-0 ${isFilled ? 'bg-green-950/30' : ''}`}
                      >
                        <div className="text-center text-[10px]">
                          {isFilled
                            ? <span className="text-green-400 font-bold">✓</span>
                            : <span className="text-gray-500">{i + 1}</span>
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
                          onClick={() => !isFilled && updateStep(i, 'order_type', isVirtual ? 'exchange' : 'virtual')}
                          disabled={isFilled}
                          title={isVirtual ? 'Виртуальный — бот следит за ценой и выставляет ордер в нужный момент' : 'Биржевой — ордер сразу выставляется на биржу'}
                          className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                            isFilled ? 'opacity-40 cursor-not-allowed bg-gray-800 text-gray-600 border border-gray-700' :
                            isVirtual ? 'bg-violet-950/60 text-violet-300 border border-violet-800/50 hover:bg-violet-900/60' :
                            'bg-gray-800 text-gray-500 border border-gray-700 hover:text-gray-300'
                          }`}
                        >
                          {isVirtual ? 'Virtual' : 'Broker'}
                        </button>
                        <button
                          onClick={() => !isFilled && updateStep(i, 'use_signal', !step.use_signal)}
                          disabled={isFilled}
                          title={step.use_signal ? 'Выставлять уровень только при активном сигнале' : 'Выставлять уровень независимо от сигнала'}
                          className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                            isFilled ? 'opacity-40 cursor-not-allowed bg-gray-800 text-gray-600 border border-gray-700' :
                            step.use_signal ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50 hover:bg-emerald-900/60' :
                            'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'
                          }`}
                        >
                          {step.use_signal ? 'Signal' : 'No'}
                        </button>
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
                <p className="text-[10px] text-gray-500 mt-1">
                  При заполнении ордера автоматически выставляется следующий шаг.
                </p>
              </div>

              <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mt-2">
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
          {tab === 2 && form.strategy_type === 'dca' && (
            <div className="py-10 text-center text-gray-500 text-sm">
              Настройки завершения для матрикс-стратегии будут добавлены позже.
            </div>
          )}
          {tab === 2 && form.strategy_type !== 'dca' && (
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
                {/* Trailing stop inside TP */}
                <div className="pt-2 border-t border-green-900/40 space-y-3">
                  <div className="flex items-center gap-1 text-[10px] text-green-600/80 uppercase tracking-wider pb-1">
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
              </div>

              {/* SL */}
              <div className="border border-red-900/50 bg-red-950/20 rounded-lg p-3 space-y-3">
                <div className="text-xs font-semibold text-red-400">Stop Loss</div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className={labelCls}>SL %<Tip text="Расстояние стоп-лосса от средней цены входа. Отрицательное значение = ниже средней для Long (например, -5 = SL на 5% ниже). Положительное значение = выше средней (для Short SL)." /></label>
                    <input
                      type="number" step="0.1"
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
