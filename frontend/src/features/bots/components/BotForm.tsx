import { useState, useEffect, useRef } from 'react';
import { X, Bot, ToggleLeft, ToggleRight, Camera, Trash2 } from 'lucide-react';
import { CoinPicker } from '../../../components/common/CoinPicker';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { Toggle, Tip, SignalPickerField } from '../../../components/strategies/FormWidgets';
import type { Bot as BotType, CreateBotInput, StrategyConfig } from '../types';
import type { SignalConfig } from '../../../types';

type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => void;
  onClose: () => void;
};

type OuterTab = 'basic' | 'strategy' | 'symbols';
type StrategySubTab = 'entry' | 'grid' | 'exit';

// ─── Default config ───────────────────────────────────────────────────────────

function defaultConfig(bot?: BotType): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    symbol:                s.symbol                ?? 'BTCUSDT',
    category:              s.category              ?? 'linear',
    direction:             s.direction             ?? 'long',
    strategy_type:         s.strategy_type         ?? 'grid',
    entry_order_type:      s.entry_order_type      ?? 'limit',
    leverage:              s.leverage              ?? 5,
    margin_type:           s.margin_type           ?? 'isolated',
    hedge_mode:            s.hedge_mode            ?? false,
    grid_size_usdt:        s.grid_size_usdt        ?? 100,
    grid_active:           s.grid_active           ?? 0,
    steps:                 s.steps                 ?? [
      { price_move_pct: 1.0, lots: 1 },
      { price_move_pct: 1.5, lots: 2 },
      { price_move_pct: 2.0, lots: 2 },
    ],
    signal_configs:        (s.signal_configs as SignalConfig[] | undefined) ?? [],
    tp_mode:               s.tp_mode               ?? 'total',
    tp_pct:                s.tp_pct                ?? 2.0,
    sl_type:               s.sl_type               ?? 'conditional',
    sl_pct:                s.sl_pct                ?? 5.0,
    signal_filter:         s.signal_filter         ?? false,
    trailing_stop_enabled: s.trailing_stop_enabled ?? false,
    trailing_activation_pct: s.trailing_activation_pct ?? 1.5,
    trailing_callback_pct:   s.trailing_callback_pct   ?? 0.5,
  };
}

// ─── Image compression ────────────────────────────────────────────────────────

function compressImage(file: File, maxPx = 300, quality = 0.82): Promise<string> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    const url = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(url);
      const scale = Math.min(1, maxPx / Math.max(img.width, img.height));
      const w = Math.round(img.width  * scale);
      const h = Math.round(img.height * scale);
      const canvas = document.createElement('canvas');
      canvas.width = w; canvas.height = h;
      canvas.getContext('2d')!.drawImage(img, 0, 0, w, h);
      resolve(canvas.toDataURL('image/jpeg', quality));
    };
    img.onerror = reject;
    img.src = url;
  });
}

// ─── BotForm ─────────────────────────────────────────────────────────────────

export function BotForm({ bot, onSubmit, onClose }: Props) {
  const [outerTab, setOuterTab]   = useState<OuterTab>('basic');
  const [stratTab, setStratTab]   = useState<StrategySubTab>('entry');

  const [name,        setName]        = useState(bot?.name        ?? '');
  const [description, setDescription] = useState(bot?.description ?? '');
  const [isPublic,    setIsPublic]    = useState(bot?.isPublic    ?? false);
  const [avatarUrl,   setAvatarUrl]   = useState<string>(bot?.avatarUrl ?? '');
  const [whitelist,   setWhitelist]   = useState<string[]>(bot?.symbolWhitelist ?? []);
  const [blacklist,   setBlacklist]   = useState<string[]>(bot?.symbolBlacklist ?? []);
  const [config,      setConfig]      = useState<StrategyConfig>(defaultConfig(bot));

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  async function handleAvatarFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      const dataUrl = await compressImage(file);
      setAvatarUrl(dataUrl);
    } catch {
      // ignore — user can retry
    }
    // reset so the same file can be re-selected
    e.target.value = '';
  }

  function patch(partial: Partial<StrategyConfig>) {
    setConfig(c => ({ ...c, ...partial }));
  }

  function patchStep(i: number, field: 'price_move_pct' | 'lots', value: number) {
    const steps = (config.steps ?? []).map((s, idx) =>
      idx === i ? { ...s, [field]: value } : s
    );
    patch({ steps });
  }

  function addStep() {
    patch({ steps: [...(config.steps ?? []), { price_move_pct: 1.0, lots: 1 }] });
  }

  function removeStep(i: number) {
    patch({ steps: (config.steps ?? []).filter((_, idx) => idx !== i) });
  }

  const handleSubmit = () => {
    if (!name.trim()) return;
    const steps = config.steps ?? [];
    onSubmit({
      name: name.trim(),
      description: description.trim(),
      isPublic,
      avatarUrl: avatarUrl || undefined,
      symbolWhitelist: whitelist,
      symbolBlacklist: blacklist,
      strategyConfig: {
        ...config,
        grid_levels: steps.length || 1,
        grid_step_pct: steps[0]?.price_move_pct ?? 1.0,
        signal_filter: (config.signal_configs?.length ?? 0) > 0,
      },
    });
    onClose();
  };

  const outerTabs: { id: OuterTab; label: string }[] = [
    { id: 'basic',    label: 'Основное' },
    { id: 'strategy', label: 'Стратегия' },
    { id: 'symbols',  label: 'Символы' },
  ];

  const stratTabs: { id: StrategySubTab; label: string }[] = [
    { id: 'entry', label: '1. Базовые' },
    { id: 'grid',  label: '2. Сетка ордеров' },
    { id: 'exit',  label: '3. Завершение' },
  ];

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur"
    >
      <div
        onClick={e => e.stopPropagation()}
        className="flex max-h-[calc(100vh-48px)] w-full max-w-[680px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]"
      >
        {/* header */}
        <div className="flex items-center gap-3 border-b border-white/[.06] px-5 py-4">
          <div className="h-9 w-9 shrink-0 overflow-hidden rounded-[9px] border border-[#5b8cff]/30">
            {avatarUrl
              ? <img src={avatarUrl} alt="" className="h-full w-full object-cover" />
              : <div className="flex h-full w-full items-center justify-center bg-[#5b8cff]/[.12] text-[#5b8cff]"><Bot size={16} strokeWidth={2} /></div>
            }
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {bot ? 'Редактировать бота' : 'Создать бота'}
            </h2>
            <p className="text-[11px] text-slate-400">{bot ? bot.name : 'Новый торговый бот'}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          >
            <X size={14} />
          </button>
        </div>

        {/* outer tabs */}
        <div className="flex border-b border-white/[.06] px-5">
          {outerTabs.map(t => (
            <button
              key={t.id}
              type="button"
              onClick={() => setOuterTab(t.id)}
              className={
                'relative mr-4 py-3 text-[12px] font-semibold transition-colors ' +
                (outerTab === t.id ? 'text-slate-50' : 'text-slate-400 hover:text-slate-200')
              }
            >
              {t.label}
              {outerTab === t.id && (
                <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full bg-[#5b8cff]" />
              )}
            </button>
          ))}
        </div>

        {/* body */}
        <div className="flex-1 overflow-auto p-5">

          {outerTab === 'basic' && (
            <div className="flex flex-col gap-4">

              {/* Avatar upload */}
              <div className="flex items-center gap-4">
                <div className="relative shrink-0 group">
                  <div className="h-[72px] w-[72px] overflow-hidden rounded-[14px] border border-white/[.08] bg-black/[.3]">
                    {avatarUrl
                      ? <img src={avatarUrl} alt="avatar" className="h-full w-full object-cover" />
                      : <div className="flex h-full w-full items-center justify-center text-slate-600">
                          <Bot size={26} strokeWidth={1.5} />
                        </div>
                    }
                  </div>
                  {/* hover overlay */}
                  <div
                    onClick={() => fileInputRef.current?.click()}
                    className="absolute inset-0 flex cursor-pointer items-center justify-center rounded-[14px] bg-black/55 opacity-0 transition-opacity group-hover:opacity-100"
                  >
                    <Camera size={18} className="text-white" />
                  </div>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept="image/*"
                    className="hidden"
                    onChange={handleAvatarFile}
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <div className="text-[13px] font-semibold text-slate-200">Аватар бота</div>
                  <div className="text-[11px] text-slate-500">PNG, JPG, GIF, WebP · сжимается до 300×300</div>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => fileInputRef.current?.click()}
                      className="inline-flex items-center gap-1.5 rounded-md border border-white/[.08] bg-white/[.04] px-3 py-1 text-[11px] font-semibold text-slate-300 hover:bg-white/[.08]"
                    >
                      <Camera size={11} /> Загрузить
                    </button>
                    {avatarUrl && (
                      <button
                        type="button"
                        onClick={() => setAvatarUrl('')}
                        className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-rose-400 hover:text-rose-300"
                      >
                        <Trash2 size={11} /> Удалить
                      </button>
                    )}
                  </div>
                </div>
              </div>

              <Field label="Название" required>
                <input
                  type="text"
                  value={name}
                  onChange={e => setName(e.target.value)}
                  placeholder="BTC Grid Pro"
                  className={inputCls}
                />
              </Field>
              <Field label="Описание">
                <textarea
                  value={description}
                  onChange={e => setDescription(e.target.value)}
                  rows={4}
                  placeholder="Опишите стратегию бота..."
                  className={`${inputCls} resize-none`}
                />
              </Field>
              <div className="flex items-center justify-between rounded-lg border border-white/[.06] bg-white/[.02] px-4 py-3">
                <div>
                  <div className="text-[13px] font-semibold text-slate-200">Публичный бот</div>
                  <div className="mt-0.5 text-[11px] text-slate-400">
                    Другие пользователи смогут подписаться на этого бота
                  </div>
                </div>
                <button type="button" onClick={() => setIsPublic(v => !v)} className="text-slate-400">
                  {isPublic
                    ? <ToggleRight size={28} className="text-[#5b8cff]" />
                    : <ToggleLeft size={28} />}
                </button>
              </div>
            </div>
          )}

          {outerTab === 'strategy' && (
            <div className="flex flex-col gap-4">
              {/* strategy type selector */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-bold uppercase tracking-wider text-slate-400">Тип стратегии</span>
                <div className="flex gap-1">
                  {(['grid', 'dca'] as const).map(t => (
                    <button
                      key={t}
                      type="button"
                      onClick={() => patch({ strategy_type: t })}
                      className={`rounded-lg border px-4 py-1.5 text-[12px] font-semibold transition-colors ${
                        config.strategy_type === t
                          ? 'border-[#5b8cff]/40 bg-[#5b8cff]/[.18] text-[#a0b8ff]'
                          : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
                      }`}
                    >
                      {t === 'grid' ? 'Grid' : 'Matrix'}
                    </button>
                  ))}
                </div>
              </div>

              {/* strategy sub-tabs */}
              <div className="flex gap-0 rounded-lg border border-white/[.06] bg-white/[.015] p-0.5">
                {stratTabs.map(t => (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => setStratTab(t.id)}
                    className={`flex-1 rounded-md py-1.5 text-[11px] font-semibold transition-colors ${
                      stratTab === t.id
                        ? 'bg-white/[.07] text-slate-100 shadow-sm'
                        : 'text-slate-400 hover:text-slate-200'
                    }`}
                  >
                    {t.label}
                  </button>
                ))}
              </div>

              {/* sub-tab content */}
              {stratTab === 'entry' && (
                <div className="flex flex-col gap-3">
                  <div>
                    <label className={labelCls}>
                      Символ
                      <Tip text="Основной инструмент бота. Можно изменить при каждом деплое." />
                    </label>
                    <CoinPicker
                      value={config.symbol ?? 'BTCUSDT'}
                      onChange={sym => patch({ symbol: sym })}
                      triggerClassName="w-full px-3 py-1.5 text-sm rounded-md border-gray-700 bg-gray-800"
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Направление
                      <Tip text="Long — покупаешь на росте. Short — продаёшь на падении. Both — бот работает в обоих направлениях." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Long',  value: 'long' },
                        { label: 'Short', value: 'short' },
                        { label: 'Both',  value: 'both' },
                      ]}
                      value={config.direction ?? 'long'}
                      onChange={v => patch({ direction: v as StrategyConfig['direction'] })}
                      optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white', both: 'bg-blue-700 text-white' }}
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Тип входного ордера
                      <Tip text="Limit — мейкер, ждёт свою цену (дешевле ~0.02%). Stop Market — тейкер, гарантирует исполнение (~0.055%)." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Limit',       value: 'limit' },
                        { label: 'Stop Market', value: 'stop_market' },
                      ]}
                      value={config.entry_order_type ?? 'limit'}
                      onChange={v => patch({ entry_order_type: v as StrategyConfig['entry_order_type'] })}
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>
                        Плечо (×)
                        <Tip text="Кратность заёмных средств. ×5 = на $100 открывается позиция $500." />
                      </label>
                      <input
                        type="number" min={1} max={100}
                        value={config.leverage ?? 5}
                        onChange={e => patch({ leverage: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>
                        Объём 1 лота (USDT)
                        <Tip text="Размер одного базового ордера. Итоговый объём уровня = лоты × этот размер." />
                      </label>
                      <input
                        type="number" min={1}
                        value={config.grid_size_usdt ?? 100}
                        onChange={e => patch({ grid_size_usdt: Number(e.target.value) })}
                        className={inputCls}
                      />
                    </div>
                  </div>

                  <div>
                    <label className={labelCls}>
                      Тип маржи
                      <Tip text="Isolated — убыток ограничен суммой под позицию. Cross — участвует вся свободная маржа." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Isolated', value: 'isolated' },
                        { label: 'Cross',    value: 'cross' },
                      ]}
                      value={config.margin_type ?? 'isolated'}
                      onChange={v => patch({ margin_type: v as StrategyConfig['margin_type'] })}
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Хедж режим
                      <Tip text="Long и Short по одной монете живут в отдельных слотах и не компенсируют друг друга." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Нет',       value: 'false' },
                        { label: 'Да (Hedge)', value: 'true' },
                      ]}
                      value={String(config.hedge_mode ?? false)}
                      onChange={v => patch({ hedge_mode: v === 'true' })}
                    />
                  </div>
                </div>
              )}

              {stratTab === 'grid' && (
                <div className="flex flex-col gap-4">
                  <div>
                    <div className="text-[10px] font-bold uppercase tracking-wider text-gray-500 pb-1 border-b border-gray-800 mb-2">
                      Шаги усреднения
                    </div>
                    <div className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 text-[10px] text-gray-600 mb-1 px-0.5">
                      <div className="text-center">#</div>
                      <div className="text-center">Движение %</div>
                      <div className="text-center">Лотов</div>
                      <div className="text-center">USDT</div>
                      <div />
                    </div>
                    <div className="max-h-[200px] overflow-y-auto">
                      {(config.steps ?? []).map((step, i) => (
                        <div
                          key={i}
                          className="grid grid-cols-[28px_1fr_1fr_1fr_28px] gap-2 items-center py-1.5 border-b border-gray-800/60 last:border-0"
                        >
                          <div className="text-center text-[10px] text-gray-600">{i + 1}</div>
                          <input
                            type="number" step="0.1" min="0"
                            value={step.price_move_pct}
                            onChange={e => patchStep(i, 'price_move_pct', Number(e.target.value))}
                            className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                          />
                          <input
                            type="number" step="0.1" min="0.1"
                            value={step.lots}
                            onChange={e => patchStep(i, 'lots', Number(e.target.value))}
                            className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                          />
                          <input
                            type="number" step="1" min="1"
                            value={+(step.lots * (config.grid_size_usdt ?? 100)).toFixed(2)}
                            onChange={e => {
                              const usdt = Number(e.target.value);
                              const base = config.grid_size_usdt ?? 100;
                              if (usdt > 0 && base > 0)
                                patchStep(i, 'lots', Math.round(usdt / base * 100) / 100);
                            }}
                            className="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                          />
                          <button
                            type="button"
                            onClick={() => removeStep(i)}
                            className="text-center text-sm text-red-500 hover:text-red-400"
                          >
                            ✕
                          </button>
                        </div>
                      ))}
                    </div>
                    <button
                      type="button"
                      onClick={addStep}
                      className="mt-2 w-full border border-dashed border-blue-800 text-blue-500 rounded-md py-1.5 text-[11px] hover:border-blue-600 hover:text-blue-400 transition-colors"
                    >
                      + Добавить шаг
                    </button>
                  </div>

                  <div>
                    <label className={labelCls}>
                      Одновременно на бирже (0 = все шаги)
                      <Tip text="Сколько ордеров из сетки висит на бирже одновременно. 0 = все сразу." />
                    </label>
                    <input
                      type="number" min={0}
                      value={config.grid_active ?? 0}
                      onChange={e => patch({ grid_active: Number(e.target.value) })}
                      className={inputCls}
                      placeholder="0"
                    />
                  </div>

                  <div>
                    <div className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-gray-500 pb-1 border-b border-gray-800 mb-2">
                      Сигналы для входа
                      <Tip text="AND-логика: все добавленные сигналы должны совпасть для запуска цикла." />
                    </div>
                    <SignalPickerField
                      configs={(config.signal_configs as SignalConfig[]) ?? []}
                      onChange={v => patch({ signal_configs: v })}
                    />
                  </div>
                </div>
              )}

              {stratTab === 'exit' && (
                <div className="flex flex-col gap-4">
                  {/* TP */}
                  <div className="rounded-lg border border-green-900/50 bg-green-950/20 p-3 space-y-3">
                    <div className="text-xs font-semibold text-green-400">Take Profit</div>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          TP %
                          <Tip text="Процент прибыли от средней цены входа, при котором выставляется закрывающий ордер." />
                        </label>
                        <input
                          type="number" step="0.1" min="0.1"
                          value={config.tp_pct ?? 2.0}
                          onChange={e => patch({ tp_pct: Number(e.target.value) })}
                          className={inputCls}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          Режим
                          <Tip text="«На всю позицию» — один TP закрывает всё. «По уровням» — каждый уровень получает свой TP." />
                        </label>
                        <Toggle
                          options={[
                            { label: 'На всю позицию', value: 'total' },
                            { label: 'По уровням',     value: 'per_level' },
                          ]}
                          value={config.tp_mode ?? 'total'}
                          onChange={v => patch({ tp_mode: v as StrategyConfig['tp_mode'] })}
                        />
                      </div>
                    </div>
                  </div>

                  {/* SL */}
                  <div className="rounded-lg border border-red-900/50 bg-red-950/20 p-3 space-y-3">
                    <div className="text-xs font-semibold text-red-400">Stop Loss</div>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          SL %
                          <Tip text="Процент убытка от средней цены, при котором позиция принудительно закрывается." />
                        </label>
                        <input
                          type="number" step="0.1" min="0.1"
                          value={config.sl_pct ?? 5.0}
                          onChange={e => patch({ sl_pct: Number(e.target.value) })}
                          className={inputCls}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          Тип
                          <Tip text="«На бирже» — стоп-ордер на Bybit, работает даже без сервера. «Программный» — сервис следит через WS." />
                        </label>
                        <Toggle
                          options={[
                            { label: 'На бирже',    value: 'conditional' },
                            { label: 'Программный', value: 'programmatic' },
                          ]}
                          value={config.sl_type ?? 'conditional'}
                          onChange={v => patch({ sl_type: v as StrategyConfig['sl_type'] })}
                        />
                      </div>
                    </div>
                  </div>

                  {/* Trailing stop */}
                  <div className="space-y-3">
                    <div className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-gray-500 pb-1 border-b border-gray-800">
                      Трейлинг-стоп
                      <Tip text="Подтягивает стоп вслед за ценой, фиксируя прибыль. При откате на callback — позиция закрывается." />
                    </div>
                    <div className="flex items-center gap-3">
                      <button
                        type="button"
                        onClick={() => patch({ trailing_stop_enabled: !(config.trailing_stop_enabled ?? false) })}
                        className={`w-10 h-5 rounded-full relative transition-colors ${
                          config.trailing_stop_enabled ? 'bg-green-600' : 'bg-gray-700'
                        }`}
                      >
                        <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${
                          config.trailing_stop_enabled ? 'translate-x-5' : 'translate-x-0'
                        }`} />
                      </button>
                      <span className={`text-xs ${config.trailing_stop_enabled ? 'text-green-400' : 'text-gray-500'}`}>
                        {config.trailing_stop_enabled ? 'Включён' : 'Выключен'}
                      </span>
                    </div>
                    {config.trailing_stop_enabled && (
                      <div className="grid grid-cols-2 gap-3">
                        <div>
                          <label className={labelCls}>
                            Активация %
                            <Tip text="Прибыль от входа, при которой трейлинг начинает следить за ценой." />
                          </label>
                          <input
                            type="number" step="0.1" min="0.1"
                            value={config.trailing_activation_pct ?? 1.5}
                            onChange={e => patch({ trailing_activation_pct: Number(e.target.value) })}
                            className={inputCls}
                          />
                        </div>
                        <div>
                          <label className={labelCls}>
                            Callback %
                            <Tip text="Откат от пика, после которого трейлинг-стоп срабатывает." />
                          </label>
                          <input
                            type="number" step="0.1" min="0.1"
                            value={config.trailing_callback_pct ?? 0.5}
                            onChange={e => patch({ trailing_callback_pct: Number(e.target.value) })}
                            className={inputCls}
                          />
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}

          {outerTab === 'symbols' && (
            <div className="flex flex-col gap-5">
              <div>
                <div className="mb-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                  Whitelist
                </div>
                <div className="mb-2 text-[11px] text-slate-500">
                  Бот торгует только этими символами. Пусто — без ограничений.
                </div>
                <CoinMultiPicker
                  values={whitelist}
                  onChange={setWhitelist}
                  color="blue"
                  placeholder="Выбрать монеты для whitelist..."
                />
              </div>
              <div>
                <div className="mb-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                  Blacklist
                </div>
                <div className="mb-2 text-[11px] text-slate-500">
                  Эти символы исключены.
                </div>
                <CoinMultiPicker
                  values={blacklist}
                  onChange={setBlacklist}
                  color="red"
                  placeholder="Выбрать монеты для blacklist..."
                />
              </div>
            </div>
          )}
        </div>

        {/* footer */}
        <div className="flex items-center justify-end gap-2 border-t border-white/[.06] px-5 py-3.5">
          <button
            type="button"
            onClick={onClose}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-4 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={!name.trim()}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            {bot ? 'Сохранить' : 'Создать'}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function Field({ label, children, required }: { label: string; children: React.ReactNode; required?: boolean }) {
  return (
    <div>
      <label className="mb-1.5 block text-[11px] font-bold uppercase tracking-wider text-slate-400">
        {label}{required && <span className="ml-0.5 text-[#5b8cff]">*</span>}
      </label>
      {children}
    </div>
  );
}

const inputCls =
  'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50';

const labelCls = 'flex items-center text-xs text-gray-500 mb-1';
