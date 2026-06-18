import { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { X, Layers, Camera, Smile, Trash2 } from 'lucide-react';
import { BotIconPicker } from './BotIconPicker';
import { Toggle, Tip } from '../../../components/strategies/FormWidgets';
import { getStrategyDefaults } from '../../admin-defaults/api';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { getInstrumentConstraints, type InstrumentConstraints } from '../../../api/strategies';
import { useSelectedAccount } from '../../../contexts/AccountContext';
import type { Bot as BotType, CreateBotInput, StrategyConfig, MatrixLevel, MatrixEntryLevel } from '../types';
import { BOT_KIND_META } from '../botKindMeta';
import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';

// ─── Types ─────────────────────────────────────────────────────────────────────

type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => Promise<void> | void;
  onClose: () => void;
  mode?: 'user' | 'admin';
};

type OuterTab = 'basic' | 'strategy' | 'close';
type StratTab = 'entry' | 'matrix' | 'params';

// ─── Constants ──────────────────────────────────────────────────────────────────

const DEACT_CLOSE_TYPES = ['PNL (общий), USDT', 'ROI (общий), %', 'В безубыток'];

// ─── Defaults ──────────────────────────────────────────────────────────────────

const DEFAULT_MATRIX_LEVELS: MatrixLevel[] = [
  { direction: 'below', price_step_pct: -1.5, size_pct: 5,  stop_pct: -3.0, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 2.5 },
  { direction: 'below', price_step_pct: -3.0, size_pct: 8,  stop_pct: -2.5, stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 3.5 },
  { direction: 'below', price_step_pct: -5.0, size_pct: 12, stop_pct: null,  stop_cond_pct: null,  stop_replace_pct: null,  tp_pct: 4.0 },
  { direction: 'above', price_step_pct: 2.0,  size_pct: 5,  stop_pct: -3.5, stop_cond_pct: -1.0, stop_replace_pct: -0.5, tp_pct: 3.0 },
  { direction: 'above', price_step_pct: 4.0,  size_pct: 8,  stop_pct: -3.0, stop_cond_pct: -1.5, stop_replace_pct: -1.5, tp_pct: 4.5 },
];
const DEFAULT_MATRIX_ENTRY: MatrixEntryLevel = {
  price_step_pct: null, size_pct: 10, stop_pct: -5.0, stop_cond_pct: -2.0, stop_replace_pct: 1.0, tp_pct: 2.0,
};

function defaultStratConfig(bot?: BotType): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    bot_kind:                'matrix',
    symbol:                  s.symbol                  ?? 'BTCUSDT',
    category:                s.category                ?? 'linear',
    direction:               'both',
    strategy_type:           'matrix',
    entry_order_type:        s.entry_order_type        ?? 'limit',
    leverage:                s.leverage                ?? 5,
    margin_type:             s.margin_type             ?? 'isolated',
    hedge_mode:              s.hedge_mode              ?? true,
    grid_size_usdt:          s.grid_size_usdt          ?? 100,
    grid_active:             s.grid_active             ?? 0,
    max_stop_active:         s.max_stop_active         ?? 0,
    after_stop_mode:         s.after_stop_mode         ?? 'restart',
    max_cycles:              s.max_cycles              ?? 0,
    matrix_levels:           s.matrix_levels           ?? DEFAULT_MATRIX_LEVELS,
    matrix_entry_level:      s.matrix_entry_level      ?? DEFAULT_MATRIX_ENTRY,
    safe_zone_pct:           s.safe_zone_pct           ?? 1.5,
    protected_build:         s.protected_build         ?? false,
    matrix_rebuild_on_sl:    s.matrix_rebuild_on_sl    ?? false,
    matrix_rebuild_from_entry: s.matrix_rebuild_from_entry ?? false,
    hedge_deact_close_type:  s.hedge_deact_close_type  ?? 0,
    hedge_deact_close_value: s.hedge_deact_close_value ?? 50,
    hedge_profit_lazy:       s.hedge_profit_lazy       ?? false,
    hedge_profit_lazy_pct:   s.hedge_profit_lazy_pct   ?? 2,
  };
}

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

// ─── MatrixBotForm ─────────────────────────────────────────────────────────────

export function MatrixBotForm({ bot, onSubmit, onClose, mode = 'user' }: Props) {
  const [outerTab, setOuterTab] = useState<OuterTab>('basic');
  const [stratTab, setStratTab] = useState<StratTab>('entry');

  const [name,            setName]            = useState(bot?.name            ?? '');
  const [description,     setDescription]     = useState(bot?.description     ?? '');
  const [fullDescription, setFullDescription] = useState(bot?.fullDescription ?? '');
  const [isPublic,        setIsPublic]        = useState(bot?.isPublic        ?? false);
  const [avatarUrl,       setAvatarUrl]       = useState<string>(bot?.avatarUrl ?? '');
  const [autoMode,        setAutoMode]        = useState(bot?.autoMode        ?? false);
  const [maxStrategies,         setMaxStrategies]         = useState(bot?.maxStrategies         ?? 0);
  const [maxLongStrategies,     setMaxLongStrategies]     = useState(bot?.maxLongStrategies     ?? 0);
  const [maxShortStrategies,    setMaxShortStrategies]    = useState(bot?.maxShortStrategies    ?? 0);
  const [maxMarginUsdt,         setMaxMarginUsdt]         = useState(bot?.maxMarginUsdt         ?? 0);
  const [maxSymConsecutiveRuns, setMaxSymConsecutiveRuns] = useState(bot?.maxSymConsecutiveRuns ?? 0);

  const [config, setConfig] = useState<StrategyConfig>(defaultStratConfig(bot));
  const patch = (p: Partial<StrategyConfig>) => setConfig(c => ({ ...c, ...p }));

  const [whitelist, setWhitelist] = useState<string[]>(bot?.symbolWhitelist ?? []);
  const [blacklist, setBlacklist] = useState<string[]>(bot?.symbolBlacklist ?? []);

  const [deactCloseValDraft, setDeactCloseValDraft] = useState<string | null>(null);
  const [profitLazyPctDraft, setProfitLazyPctDraft] = useState<string | null>(null);
  const [leverageMax,  setLeverageMax]  = useState(false);

  const [submitting,            setSubmitting]            = useState(false);
  const [submitError,           setSubmitError]           = useState<string | null>(null);
  const [showResetStatsConfirm, setShowResetStatsConfirm] = useState(false);
  const [instrInfo,   setInstrInfo]   = useState<InstrumentConstraints | null>(null);

  const { selectedAccountId } = useSelectedAccount();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [showIconPicker, setShowIconPicker] = useState(false);

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  useEffect(() => {
    if (outerTab === 'strategy' && stratTab === 'entry' && config.symbol) {
      getInstrumentConstraints(config.symbol).then(setInstrInfo).catch(() => {});
    }
  }, [outerTab, stratTab, config.symbol]);

  // Apply admin-configured defaults for new bots
  useEffect(() => {
    if (bot) return
    getStrategyDefaults().then(defaults => {
      const d = defaults.bot_matrix
      if (!d) return
      setConfig(c => {
        const o: Partial<typeof c> = {}
        if (d.leverage !== undefined)      o.leverage      = d.leverage
        if (d.grid_size_usdt !== undefined) o.grid_size_usdt = d.grid_size_usdt
        if (d.entry_order_type !== undefined) o.entry_order_type = d.entry_order_type
        if (d.margin_type !== undefined)   o.margin_type   = d.margin_type
        if (d.hedge_mode !== undefined)    o.hedge_mode    = d.hedge_mode
        if (d.grid_active !== undefined)   o.grid_active   = d.grid_active
        if (d.max_stop_active !== undefined) o.max_stop_active = d.max_stop_active
        if (d.after_stop_mode !== undefined) o.after_stop_mode = d.after_stop_mode
        if (d.max_cycles !== undefined)    o.max_cycles    = d.max_cycles
        if (d.safe_zone_pct !== undefined) o.safe_zone_pct = d.safe_zone_pct
        if (d.protected_build !== undefined) o.protected_build = d.protected_build
        if (d.matrix_rebuild_on_sl !== undefined) o.matrix_rebuild_on_sl = d.matrix_rebuild_on_sl
        if (d.matrix_rebuild_from_entry !== undefined) o.matrix_rebuild_from_entry = d.matrix_rebuild_from_entry
        if (d.matrix_levels)       o.matrix_levels       = d.matrix_levels
        if (d.matrix_entry_level)  o.matrix_entry_level  = d.matrix_entry_level
        if (d.hedge_deact_close_type !== undefined) o.hedge_deact_close_type  = d.hedge_deact_close_type
        if (d.hedge_deact_close_value !== undefined) o.hedge_deact_close_value = d.hedge_deact_close_value
        if (d.hedge_profit_lazy !== undefined) o.hedge_profit_lazy = d.hedge_profit_lazy
        if (d.hedge_profit_lazy_pct !== undefined) o.hedge_profit_lazy_pct = d.hedge_profit_lazy_pct
        return { ...c, ...o }
      })
    }).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const handleAvatarFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]; if (!file) return;
    const dataUrl = await compressImage(file);
    setAvatarUrl(dataUrl);
    e.target.value = '';
  };

  // ── Matrix helpers ─────────────────────────────────────────────────────────
  const matrixLevels     = config.matrix_levels     ?? DEFAULT_MATRIX_LEVELS;
  const matrixEntryLevel = config.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY;
  const aboveLevels = matrixLevels.filter(l => l.direction === 'above');
  const belowLevels = matrixLevels.filter(l => l.direction === 'below');

  function addAboveLevel() {
    const last = aboveLevels[aboveLevels.length - 1];
    const nv: MatrixLevel = {
      direction: 'above',
      price_step_pct: last ? +(last.price_step_pct + 2).toFixed(1) : 2.0,
      size_pct: last ? +(last.size_pct + 2).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    };
    patch({ matrix_levels: [...matrixLevels, nv] });
  }
  function addBelowLevel() {
    const last = belowLevels[belowLevels.length - 1];
    const nv: MatrixLevel = {
      direction: 'below',
      price_step_pct: last ? +(last.price_step_pct - 1.5).toFixed(1) : -1.5,
      size_pct: last ? +(last.size_pct + 3).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    };
    patch({ matrix_levels: [...matrixLevels, nv] });
  }
  function removeMatrixLevel(direction: 'above' | 'below', idx: number) {
    const dirLevels = matrixLevels.filter(l => l.direction === direction);
    if (dirLevels.length <= 1) return;
    let removed = false;
    patch({ matrix_levels: matrixLevels.filter(l => {
      if (l.direction !== direction) return true;
      const thisIdx = matrixLevels.filter(x => x.direction === direction).indexOf(l);
      if (thisIdx === idx && !removed) { removed = true; return false; }
      return true;
    }) });
  }
  function updateMatrixLevel(direction: 'above' | 'below', idx: number, field: keyof MatrixLevel, value: number | string | boolean | null) {
    let cnt = 0;
    patch({ matrix_levels: matrixLevels.map(l => {
      if (l.direction !== direction) return l;
      if (cnt++ === idx) return { ...l, [field]: value };
      return l;
    }) });
  }
  function updateEntryLevel(field: keyof MatrixEntryLevel, value: number | null | string | boolean) {
    patch({ matrix_entry_level: { ...matrixEntryLevel, [field]: value } });
  }

  const matrixLevelErrors = matrixLevels.map(l => ({
    price_step_pct: l.direction === 'above' && l.price_step_pct <= 0 ? 'Должно быть положительным'
                  : l.direction === 'below' && l.price_step_pct >= 0 ? 'Должно быть отрицательным' : null,
    size_pct: l.size_pct <= 0 ? 'Объём > 0' : null,
    stop_pct: l.stop_pct !== null && l.stop_pct >= 0 ? 'Стоп отрицательным' : null,
  }));
  const matrixEntryErrors = {
    size_pct: matrixEntryLevel.size_pct <= 0 ? 'Объём > 0' : null,
    stop_pct: matrixEntryLevel.stop_pct !== null && matrixEntryLevel.stop_pct >= 0 ? 'Стоп отрицательным' : null,
  };

  const doSubmit = async () => {
    setSubmitting(true);
    setSubmitError(null);
    const totalMatrixLevels = aboveLevels.length + 1 + belowLevels.length;
    try {
      await onSubmit({
        name: name.trim(),
        description: description.trim(),
        fullDescription: fullDescription.trim() || undefined,
        isPublic,
        avatarUrl: avatarUrl || undefined,
        symbolWhitelist: whitelist,
        symbolBlacklist: blacklist,
        strategyConfig: {
          ...config,
          bot_kind:      'matrix',
          direction:     'both',
          strategy_type: 'matrix',
          grid_levels:   totalMatrixLevels,
          grid_step_pct: belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0,
          signal_filter: false,
          hedge_deact_type: 4, // always wait_pair
        },
        maxStrategies,
        maxLongStrategies,
        maxShortStrategies,
        maxMarginUsdt,
        maxSymConsecutiveRuns,
        accountId: selectedAccountId || null,
        autoMode,
      });
      onClose();
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : 'Неизвестная ошибка');
    } finally {
      setSubmitting(false);
    }
  };

  const handleSubmit = async () => {
    if (!name.trim()) return;
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }
    await doSubmit();
  };

  const META = BOT_KIND_META['matrix'];

  const outerTabs: { id: OuterTab; label: string }[] = [
    { id: 'basic',    label: 'Основное'  },
    { id: 'strategy', label: 'Стратегия' },
    { id: 'close',    label: 'Закрытие'  },
  ];
  const stratTabs: { id: StratTab; label: string }[] = [
    { id: 'entry',  label: '1. Базовые'   },
    { id: 'matrix', label: '2. Матрица'   },
    { id: 'params', label: '3. Параметры' },
  ];

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur">
      <div className="flex max-h-[calc(100vh-48px)] w-full max-w-[680px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]">

        {/* ── header ── */}
        <div className="flex items-center gap-3 border-b border-white/[.06] px-5 py-4">
          <div className="h-9 w-9 shrink-0 overflow-hidden rounded-[9px]" style={{ border: `1px solid ${META.border}` }}>
            {avatarUrl
              ? <img src={avatarUrl} alt="" className="h-full w-full object-cover" />
              : <div className="flex h-full w-full items-center justify-center" style={{ background: META.iconBg, color: META.color }}><Layers size={16} strokeWidth={2} /></div>
            }
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {bot ? 'Редактировать MatrixBot' : 'Создать MatrixBot'}
            </h2>
            <p className="text-[11px] text-slate-400">{bot ? bot.name : 'Парная матрица с симметричным закрытием'}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          >
            <X size={14} />
          </button>
        </div>

        {/* ── outer tabs ── */}
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
                <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full" style={{ background: META.color }} />
              )}
            </button>
          ))}
        </div>

        {/* ── body ── */}
        <div className="flex-1 overflow-auto p-5">

          {/* ═══ BASIC ════════════════════════════════════════════════════════ */}
          {outerTab === 'basic' && (
            <div className="flex flex-col gap-4">

              {/* Avatar */}
              <div className="flex items-center gap-4">
                <div className="relative shrink-0 group">
                  <div className="h-[72px] w-[72px] overflow-hidden rounded-[14px] border border-white/[.08] bg-black/[.3]">
                    {avatarUrl
                      ? <img src={avatarUrl} alt="avatar" className="h-full w-full object-cover" />
                      : <div className="flex h-full w-full items-center justify-center text-slate-600">
                          <Layers size={26} strokeWidth={1.5} />
                        </div>
                    }
                  </div>
                  <div
                    onClick={() => fileInputRef.current?.click()}
                    className="absolute inset-0 flex cursor-pointer items-center justify-center rounded-[14px] bg-black/55 opacity-0 transition-opacity group-hover:opacity-100"
                  >
                    <Camera size={18} className="text-white" />
                  </div>
                  <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleAvatarFile} />
                </div>

                <div className="flex flex-1 flex-col gap-2">
                  <Field label="Название" required>
                    <input
                      type="text"
                      value={name}
                      onChange={e => setName(e.target.value.slice(0, 30))}
                      placeholder="My MatrixBot"
                      className={inputCls}
                    />
                  </Field>
                  <div className="relative flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => fileInputRef.current?.click()}
                      className="inline-flex items-center gap-1.5 rounded-md border border-white/[.08] bg-white/[.04] px-3 py-1 text-[11px] font-semibold text-slate-300 hover:bg-white/[.08]"
                    >
                      <Camera size={11} /> Загрузить
                    </button>
                    <button
                      type="button"
                      onClick={() => setShowIconPicker(v => !v)}
                      className="inline-flex items-center gap-1.5 rounded-md border border-white/[.08] bg-white/[.04] px-3 py-1 text-[11px] font-semibold text-slate-300 hover:bg-white/[.08]"
                    >
                      <Smile size={11} /> Иконки
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
                    {showIconPicker && (
                      <BotIconPicker
                        value={avatarUrl}
                        onChange={url => { setAvatarUrl(url); setShowIconPicker(false); }}
                        onClose={() => setShowIconPicker(false)}
                      />
                    )}
                  </div>
                </div>
              </div>

              {/* Description */}
              <Field label="Описание">
                <input
                  type="text"
                  value={description}
                  onChange={e => setDescription(e.target.value.slice(0, 120))}
                  placeholder="Краткое описание стратегии"
                  className={inputCls}
                />
              </Field>

              {mode === 'admin' && (
                <Field label="Полное описание">
                  <textarea
                    value={fullDescription}
                    onChange={e => setFullDescription(e.target.value)}
                    rows={3}
                    placeholder="Расширенное описание для витрины"
                    className={inputCls + ' resize-none'}
                  />
                </Field>
              )}

              {/* Whitelist / Blacklist */}
              <div className="flex flex-col gap-3">
                <Field label="Whitelist монет" hint="Бот торгует только этими монетами (пусто = все)">
                  <CoinMultiPicker values={whitelist} onChange={setWhitelist} />
                </Field>
                <Field label="Blacklist монет" hint="Монеты, исключённые из торговли">
                  <CoinMultiPicker values={blacklist} onChange={setBlacklist} color="red" />
                </Field>
              </div>

              {/* Лимиты */}
              <div className="grid grid-cols-3 gap-3">
                <Field label="Макс. стратегий" hint="0 = не ограничено">
                  <input type="number" min={0} value={maxStrategies} onChange={e => setMaxStrategies(+e.target.value)} className={inputCls} />
                </Field>
                <Field label="Макс. Long">
                  <input type="number" min={0} value={maxLongStrategies} onChange={e => setMaxLongStrategies(+e.target.value)} className={inputCls} />
                </Field>
                <Field label="Макс. Short">
                  <input type="number" min={0} value={maxShortStrategies} onChange={e => setMaxShortStrategies(+e.target.value)} className={inputCls} />
                </Field>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Макс. маржа, USDT" hint="0 = не ограничено">
                  <input type="number" min={0} value={maxMarginUsdt} onChange={e => setMaxMarginUsdt(+e.target.value)} className={inputCls} />
                </Field>
                <Field label="Макс. повт. по монете" hint="0 = не ограничено">
                  <input type="number" min={0} value={maxSymConsecutiveRuns} onChange={e => setMaxSymConsecutiveRuns(+e.target.value)} className={inputCls} />
                </Field>
              </div>

              {/* Авторежим / Публичный */}
              <div className="rounded-xl border border-white/[.06] bg-white/[.02] divide-y divide-white/[.04]">
                <div className="flex items-center justify-between px-4 py-2.5">
                  <div>
                    <div className="text-[12px] font-semibold text-slate-200">Авторежим</div>
                    <div className="text-[10px] text-slate-500">Автоматически запускать бота</div>
                  </div>
                  <button type="button" onClick={() => setAutoMode(v => !v)} className="text-slate-400">
                    {autoMode
                      ? <span className="inline-block w-[28px] h-[16px] rounded-full relative" style={{ background: META.color }}><span className="absolute right-0.5 top-0.5 w-3.5 h-3.5 bg-white rounded-full" /></span>
                      : <span className="inline-block w-[28px] h-[16px] rounded-full bg-gray-700 relative"><span className="absolute left-0.5 top-0.5 w-3.5 h-3.5 bg-white rounded-full" /></span>
                    }
                  </button>
                </div>
                {mode === 'admin' && (
                  <div className="flex items-center justify-between px-4 py-2.5">
                    <div>
                      <div className="text-[12px] font-semibold text-slate-200">Публичный</div>
                      <div className="text-[10px] text-slate-500">Показывать в витрине</div>
                    </div>
                    <button type="button" onClick={() => setIsPublic(v => !v)} className="text-slate-400">
                      {isPublic
                        ? <span className="inline-block w-[28px] h-[16px] rounded-full relative" style={{ background: META.color }}><span className="absolute right-0.5 top-0.5 w-3.5 h-3.5 bg-white rounded-full" /></span>
                        : <span className="inline-block w-[28px] h-[16px] rounded-full bg-gray-700 relative"><span className="absolute left-0.5 top-0.5 w-3.5 h-3.5 bg-white rounded-full" /></span>
                      }
                    </button>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* ═══ STRATEGY ═════════════════════════════════════════════════════ */}
          {outerTab === 'strategy' && (
            <div className="flex flex-col gap-4">

              {/* Strategy sub-tabs */}
              <div className="flex gap-1 rounded-lg border border-white/[.06] bg-white/[.02] p-1">
                {stratTabs.map(t => (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => setStratTab(t.id)}
                    className={`flex-1 rounded-md py-1.5 text-[11px] font-semibold transition-colors ${
                      stratTab === t.id ? 'text-white' : 'text-slate-400 hover:text-slate-200'
                    }`}
                    style={stratTab === t.id ? { background: META.iconBg, color: META.color } : {}}
                  >
                    {t.label}
                  </button>
                ))}
              </div>

              {/* 1. Базовые */}
              {stratTab === 'entry' && (
                <div className="flex flex-col gap-3">
                  <div className="grid grid-cols-2 gap-3">
                    <Field label="Плечо">
                      <input
                        type="range"
                        min={1}
                        max={instrInfo?.max_leverage ?? 100}
                        step={1}
                        value={config.leverage ?? 5}
                        disabled={leverageMax}
                        onChange={e => { setLeverageMax(false); patch({ leverage: parseInt(e.target.value) }); }}
                        className="w-full h-1.5 mt-2 cursor-pointer disabled:opacity-40"
                        style={{ accentColor: META.color }}
                      />
                      <div className="flex justify-between text-[10px] text-slate-500 mt-0.5">
                        <span>×1</span>
                        <span className="font-bold text-slate-200">×{config.leverage ?? 5}</span>
                        <span>×{instrInfo?.max_leverage ?? 100}</span>
                      </div>
                    </Field>
                    <Field label="Фикс. / Макс.">
                      <Toggle
                        options={[{ label: 'Фикс.', value: 'fixed' }, { label: 'Макс.', value: 'max' }]}
                        value={leverageMax ? 'max' : 'fixed'}
                        onChange={v => {
                          const isMax = v === 'max';
                          setLeverageMax(isMax);
                          if (isMax) patch({ leverage: instrInfo?.max_leverage ?? 100 });
                        }}
                      />
                    </Field>
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <Field label="Тип маржи">
                      <Toggle
                        options={[{ label: 'Isolated', value: 'isolated' }, { label: 'Cross', value: 'cross' }]}
                        value={config.margin_type ?? 'isolated'}
                        onChange={v => patch({ margin_type: v as 'isolated' | 'cross' })}
                      />
                    </Field>
                    <Field label="Hedge Mode">
                      <Toggle
                        options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                        value={String(config.hedge_mode ?? true)}
                        onChange={v => patch({ hedge_mode: v === 'true' })}
                        optionColors={{ true: 'bg-violet-700 text-white' }}
                      />
                    </Field>
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <Field label="Категория">
                      <Toggle
                        options={[{ label: 'Linear', value: 'linear' }, { label: 'Inverse', value: 'inverse' }]}
                        value={config.category ?? 'linear'}
                        onChange={v => patch({ category: v })}
                      />
                    </Field>
                    <Field label="Тип входа">
                      <Toggle
                        options={[{ label: 'Limit', value: 'limit' }, { label: 'Stop-Market', value: 'stop_market' }]}
                        value={config.entry_order_type ?? 'limit'}
                        onChange={v => patch({ entry_order_type: v as 'limit' | 'stop_market' })}
                      />
                    </Field>
                  </div>

                  <Field label="Депозит на символ, USDT">
                    <input
                      type="number"
                      min={1}
                      step={1}
                      value={config.grid_size_usdt ?? 100}
                      onChange={e => patch({ grid_size_usdt: +e.target.value })}
                      className={inputCls}
                    />
                  </Field>

                  <div className="grid grid-cols-2 gap-3">
                    <Field label="После стопа">
                      <Toggle
                        options={[{ label: 'Рестарт', value: 'restart' }, { label: 'Удалить', value: 'delete' }]}
                        value={config.after_stop_mode ?? 'restart'}
                        onChange={v => patch({ after_stop_mode: v as 'restart' | 'delete' })}
                      />
                    </Field>
                    <Field label="Макс. циклов" hint="0 = не ограничено">
                      <input type="number" min={0} value={config.max_cycles ?? 0} onChange={e => patch({ max_cycles: +e.target.value })} className={inputCls} />
                    </Field>
                  </div>
                </div>
              )}

              {/* 2. Матрица */}
              {stratTab === 'matrix' && (() => {
                const colGrid = 'grid grid-cols-[30px_1fr_1fr_6px_1fr_1fr_1fr_1fr_42px_36px_18px] gap-[3px] items-center';
                const sep = <div className="flex items-center justify-center"><div className="w-px h-[20px] bg-gray-700/60" /></div>;
                const numCls = (err: boolean) =>
                  `bg-gray-900 border rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-violet-500 ${err ? 'border-red-500' : 'border-gray-700'}`;

                const orderTypeBtn = (isVirtual: boolean, onToggle: () => void) => (
                  <button type="button" onClick={onToggle}
                    title={isVirtual ? 'Виртуальный' : 'Биржевой'}
                    className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${isVirtual ? 'bg-violet-950/60 text-violet-300 border border-violet-800/50 hover:bg-violet-900/60' : 'bg-gray-800 text-gray-500 border border-gray-700 hover:text-gray-300'}`}>
                    {isVirtual ? 'Virtual' : 'Broker'}
                  </button>
                );

                const colHdrs = () => (
                  <div className={`${colGrid} px-1 pb-1`}>
                    <span />
                    <span className="text-[8px] text-gray-400 text-center">Шаг %</span>
                    <span className="text-[8px] text-gray-400 text-center">Объём %</span>
                    <span />
                    <span className="text-[8px] text-gray-400 text-center">Стоп %</span>
                    <span className="text-[8px] text-gray-400 text-center">Усл %</span>
                    <span className="text-[8px] text-gray-400 text-center">Замена %</span>
                    <span className="text-[8px] text-gray-400 text-center">ТП %</span>
                    <span className="text-[8px] text-gray-400 text-center">Тип</span>
                    <span className="text-[8px] text-gray-400 text-center">Сигн</span>
                    <span />
                  </div>
                );

                const levelRow = (direction: 'above' | 'below', level: MatrixLevel, dirIdx: number, labelNum: number, canDelete: boolean) => {
                  const isAbove = direction === 'above';
                  const badgeCls = isAbove
                    ? 'bg-emerald-900 text-emerald-300 text-[8px] font-bold rounded text-center py-0.5'
                    : 'bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5';
                  const rowCls = isAbove ? 'bg-emerald-950/20' : 'bg-blue-950/30';
                  const globalIdx = matrixLevels.indexOf(level);
                  const errs = matrixLevelErrors[globalIdx] ?? {};
                  return (
                    <div key={`${direction}-${dirIdx}`} className={`${colGrid} px-1 py-1 rounded mb-0.5 ${rowCls}`}>
                      <div className={badgeCls}>{isAbove ? `L(${labelNum})` : `L(-${labelNum})`}</div>
                      <MxNum value={level.price_step_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'price_step_pct', v)} err={errs.price_step_pct} cls={numCls(!!errs.price_step_pct)} />
                      <MxNum value={level.size_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'size_pct', v)} err={errs.size_pct} cls={numCls(!!errs.size_pct)} />
                      {sep}
                      <MxNull value={level.stop_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_pct', v)} err={errs.stop_pct} />
                      <MxNull value={level.stop_cond_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_cond_pct', v)} />
                      <MxNull value={level.stop_replace_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_replace_pct', v)} />
                      <MxNull value={level.tp_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'tp_pct', v)} />
                      {orderTypeBtn(level.order_type === 'virtual', () => updateMatrixLevel(direction, dirIdx, 'order_type', level.order_type === 'virtual' ? 'exchange' : 'virtual'))}
                      <button type="button" onClick={() => updateMatrixLevel(direction, dirIdx, 'use_signal', !level.use_signal)}
                        className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${level.use_signal ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50' : 'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'}`}>
                        {level.use_signal ? 'Signal' : 'No'}
                      </button>
                      {canDelete
                        ? <button type="button" onClick={() => removeMatrixLevel(direction, dirIdx)} className="text-center text-xs text-red-500 hover:text-red-400">✕</button>
                        : <span />}
                    </div>
                  );
                };

                return (
                  <div className="flex flex-col gap-3">
                    {/* Депозит */}
                    <Field label="Депозит на символ, USDT">
                      <input type="number" min={1} step={1}
                        value={config.grid_size_usdt ?? 100}
                        onChange={e => patch({ grid_size_usdt: +e.target.value })}
                        className={inputCls} />
                    </Field>

                    {/* ABOVE */}
                    <div>
                      <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pb-1 border-b border-gray-800 mb-1">
                        <span>Выше точки входа</span>
                        <span className="bg-emerald-900/60 text-emerald-400 rounded px-1.5 py-0.5 text-[8px]">{aboveLevels.length} уровней</span>
                      </div>
                      <button type="button" onClick={addAboveLevel}
                        className="w-full border border-dashed border-emerald-900 text-emerald-700 rounded py-1 text-[10px] hover:border-emerald-700 hover:text-emerald-400 transition-colors mb-2">
                        + Добавить уровень выше
                      </button>
                      {colHdrs()}
                      <div>
                        {[...aboveLevels].reverse().map((level, revIdx) => {
                          const dirIdx = aboveLevels.length - 1 - revIdx;
                          return levelRow('above', level, dirIdx, dirIdx + 1, aboveLevels.length > 1);
                        })}
                      </div>
                    </div>

                    {/* Entry L(0) */}
                    <div className="border border-yellow-700/60 bg-yellow-950/10 rounded-lg p-2">
                      <div className="text-[9px] text-yellow-600 uppercase tracking-wider mb-1.5">⬡ Нулевой уровень — точка входа L(0)</div>
                      {colHdrs()}
                      <div className={`${colGrid} px-1 py-1 rounded bg-yellow-950/20`}>
                        <div className="text-[8px] font-bold rounded text-center py-0.5 bg-yellow-800/60 text-yellow-200">L(0)</div>
                        <MxNull value={matrixEntryLevel.price_step_pct ?? null} onChange={v => updateEntryLevel('price_step_pct', v)} />
                        <MxNum value={matrixEntryLevel.size_pct} onChange={v => updateEntryLevel('size_pct', v)} err={matrixEntryErrors.size_pct} cls={numCls(!!matrixEntryErrors.size_pct)} />
                        {sep}
                        <MxNull value={matrixEntryLevel.stop_pct} onChange={v => updateEntryLevel('stop_pct', v)} err={matrixEntryErrors.stop_pct} />
                        <MxNull value={matrixEntryLevel.stop_cond_pct} onChange={v => updateEntryLevel('stop_cond_pct', v)} />
                        <MxNull value={matrixEntryLevel.stop_replace_pct} onChange={v => updateEntryLevel('stop_replace_pct', v)} />
                        <MxNull value={matrixEntryLevel.tp_pct} onChange={v => updateEntryLevel('tp_pct', v)} />
                        {orderTypeBtn(matrixEntryLevel.order_type === 'virtual', () => updateEntryLevel('order_type', matrixEntryLevel.order_type === 'virtual' ? 'exchange' : 'virtual'))}
                        <button type="button" onClick={() => updateEntryLevel('use_signal', !matrixEntryLevel.use_signal)}
                          className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${matrixEntryLevel.use_signal ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50' : 'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'}`}>
                          {matrixEntryLevel.use_signal ? 'Signal' : 'No'}
                        </button>
                        <span />
                      </div>
                    </div>

                    {/* BELOW */}
                    <div>
                      {colHdrs()}
                      <div>
                        {belowLevels.map((level, dirIdx) =>
                          levelRow('below', level, dirIdx, dirIdx + 1, belowLevels.length > 1)
                        )}
                      </div>
                      <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pt-1 border-t border-gray-800 mt-1 mb-1">
                        <span>Ниже точки входа</span>
                        <span className="bg-blue-900/60 text-blue-400 rounded px-1.5 py-0.5 text-[8px]">{belowLevels.length} уровней</span>
                      </div>
                      <button type="button" onClick={addBelowLevel}
                        className="w-full border border-dashed border-blue-900 text-blue-600 rounded py-1 text-[10px] hover:border-blue-700 hover:text-blue-400 transition-colors">
                        + Добавить уровень ниже
                      </button>
                    </div>
                  </div>
                );
              })()}

              {/* 3. Параметры */}
              {stratTab === 'params' && (
                <div className="flex flex-col gap-5">
                  <div>
                    <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                      Управление матрицей
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          Safe-Zone от SL %
                          <Tip text="После срабатывания SL — зона вокруг цены, в которой новые ордера матрицы не выставляются." />
                        </label>
                        <input type="number" step="0.1" min={0}
                          value={config.safe_zone_pct ?? 1.5}
                          onChange={e => patch({ safe_zone_pct: Number(e.target.value) })}
                          className={inputCls} />
                      </div>
                      <div>
                        <label className={labelCls}>
                          🔒 Защищённое построение
                          <Tip text="Следующий уровень выставляется только после того, как предыдущий прикрыт хотя бы одним стопом." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '🔒 Вкл', value: 'true' }]}
                          value={String(config.protected_build ?? false)}
                          onChange={v => patch({ protected_build: v === 'true' })}
                          optionColors={{ true: 'bg-amber-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          ⟳ Перестройка от SZ
                          <Tip text="После SL немедленно переставляет все незаполненные уровни от нижней границы SafeZone." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '⟳ Вкл', value: 'true' }]}
                          value={String(config.matrix_rebuild_on_sl ?? false)}
                          onChange={v => patch({ matrix_rebuild_on_sl: v === 'true' })}
                          optionColors={{ true: 'bg-blue-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          ⚓ Якорь на точку входа
                          <Tip text="Все уровни строятся от цены заполнения L(0). После SL перезаходит маркетом из SafeZone." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: '⚓ Вкл', value: 'true' }]}
                          value={String(config.matrix_rebuild_from_entry ?? false)}
                          onChange={v => patch({ matrix_rebuild_from_entry: v === 'true' })}
                          optionColors={{ true: 'bg-violet-700 text-white' }}
                        />
                      </div>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* ═══ CLOSE ════════════════════════════════════════════════════════ */}
          {outerTab === 'close' && (
            <div className="flex flex-col gap-4">

              <div className="rounded-lg border border-violet-500/20 bg-violet-950/[.10] px-4 py-3 text-[12px] text-violet-300 leading-relaxed">
                Бот открывает <b>Long + Short</b> на каждом символе из whitelist. Закрывает пару когда суммарный PnL достигает цели — затем автоматически открывает новую пару.
              </div>

              <SectionHeader icon="✕" color="blue">Парное закрытие</SectionHeader>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelCls}>
                    Закрыть пару при
                    <Tip text="Суммарный результат Long + Short, при котором обе позиции закрываются. «В безубыток» — закрытие когда совокупный PnL ≥ 0." />
                  </label>
                  <EnumPicker
                    options={DEACT_CLOSE_TYPES}
                    value={config.hedge_deact_close_type ?? 0}
                    onChange={v => patch({ hedge_deact_close_type: v })}
                  />
                </div>
                {(config.hedge_deact_close_type ?? 0) !== 2 && (
                  <div>
                    <label className={labelCls}>
                      {(config.hedge_deact_close_type ?? 0) === 0 ? 'PnL, USDT' : 'ROI, %'}
                    </label>
                    <input
                      type="text"
                      inputMode="decimal"
                      value={deactCloseValDraft !== null ? deactCloseValDraft : String(config.hedge_deact_close_value ?? 50)}
                      onChange={e => {
                        const raw = e.target.value;
                        setDeactCloseValDraft(raw);
                        if (/^-?\d*\.?\d*$/.test(raw) && raw !== '' && raw !== '-') {
                          const v = parseFloat(raw);
                          if (!isNaN(v)) patch({ hedge_deact_close_value: v });
                        }
                      }}
                      onBlur={() => {
                        if (deactCloseValDraft !== null) {
                          const v = parseFloat(deactCloseValDraft);
                          patch({ hedge_deact_close_value: isNaN(v) ? (config.hedge_deact_close_value ?? 50) : v });
                          setDeactCloseValDraft(null);
                        }
                      }}
                      className={inputCls}
                    />
                  </div>
                )}
              </div>

              <SectionHeader icon="↗" color="green">Трейлинг профит</SectionHeader>

              <div className="flex items-center gap-3">
                <ToggleSwitch
                  enabled={config.hedge_profit_lazy ?? false}
                  onToggle={() => patch({ hedge_profit_lazy: !(config.hedge_profit_lazy ?? false) })}
                />
                <span className="text-[12px] text-slate-300">
                  Закрывать если прибыль хеджа ≥
                  <Tip text="Если ROI хедж-позиции достигает порога — закрыть, не дожидаясь парного условия." />
                </span>
                {(config.hedge_profit_lazy ?? false) && (
                  <div className="flex items-center gap-1.5 ml-2">
                    <input
                      type="text"
                      inputMode="decimal"
                      value={profitLazyPctDraft !== null ? profitLazyPctDraft : String(config.hedge_profit_lazy_pct ?? 2)}
                      onChange={e => {
                        const raw = e.target.value;
                        setProfitLazyPctDraft(raw);
                        const v = parseFloat(raw);
                        if (!isNaN(v)) patch({ hedge_profit_lazy_pct: v });
                      }}
                      onBlur={() => {
                        if (profitLazyPctDraft !== null) {
                          const v = parseFloat(profitLazyPctDraft);
                          patch({ hedge_profit_lazy_pct: isNaN(v) ? (config.hedge_profit_lazy_pct ?? 2) : v });
                          setProfitLazyPctDraft(null);
                        }
                      }}
                      className="w-20 rounded-lg border border-white/[.08] bg-black/[.25] px-2 py-1.5 text-[12px] text-slate-200 text-center outline-none focus:border-violet-500/50"
                    />
                    <span className="text-[11px] text-slate-500">%</span>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* ── footer ── */}
        <div className="border-t border-white/[.06] px-5 py-3.5">
          {submitError && (
            <div className="mb-2.5 rounded-lg border border-rose-500/30 bg-rose-500/[.1] px-3 py-2 text-[12px] text-rose-300">
              {submitError}
            </div>
          )}
          <div className="flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-4 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06] disabled:opacity-40"
            >
              Отмена
            </button>
            <button
              type="button"
              onClick={handleSubmit}
              disabled={!name.trim() || submitting}
              className="inline-flex items-center gap-1.5 rounded-lg px-4 py-2 text-xs font-semibold text-white shadow disabled:cursor-not-allowed disabled:opacity-40"
              style={{ background: `linear-gradient(180deg,${META.color},#7c3aed)`, boxShadow: `0 4px 12px -6px ${META.color}80` }}
            >
              {submitting ? 'Сохранение...' : (bot ? 'Сохранить' : 'Создать')}
            </button>
          </div>
        </div>
      </div>

      {showResetStatsConfirm && bot && (
        <ResetStatsConfirmModal
          tradesTotal={bot.tradesTotal ?? 0}
          netPnlTotal={bot.netPnlTotal ?? 0}
          tradesWin={bot.tradesWin ?? 0}
          onConfirm={() => { setShowResetStatsConfirm(false); void doSubmit(); }}
          onCancel={() => setShowResetStatsConfirm(false)}
        />
      )}
    </div>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function EnumPicker({ options, value, onChange }: { options: string[]; value: number; onChange: (v: number) => void }) {
  const [open, setOpen] = useState(false);
  const [pos, setPos]   = useState({ top: 0, left: 0, width: 0 });
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (!btnRef.current?.contains(e.target as Node) && !menuRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const toggle = () => {
    if (!open && btnRef.current) {
      const r = btnRef.current.getBoundingClientRect();
      setPos({ top: r.bottom + 4, left: r.left, width: r.width });
    }
    setOpen(o => !o);
  };

  return (
    <div className="relative w-full">
      <button ref={btnRef} type="button" onClick={toggle}
        className="w-full flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-black/[.25] px-2.5 py-[9px] text-[12px] text-slate-200 outline-none hover:border-white/[.16] cursor-pointer">
        <span className="flex-1 text-left truncate">{options[value]}</span>
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" className="shrink-0 opacity-40"><path d="M6 9l6 6 6-6"/></svg>
      </button>
      {open && createPortal(
        <div ref={menuRef} className="fixed z-[9999] rounded-[9px] p-1 flex flex-col gap-px"
          style={{ top: pos.top, left: pos.left, minWidth: Math.max(pos.width, 240), background: '#181b28', border: '1px solid rgba(255,255,255,.22)', boxShadow: '0 8px 32px rgba(0,0,0,.85)' }}>
          {options.map((label, i) => (
            <button key={i} type="button" onClick={() => { onChange(i); setOpen(false); }}
              className={`flex items-center gap-2 px-[9px] py-[7px] text-[12px] font-medium text-[#cfd5e1] rounded-[6px] w-full text-left hover:bg-white/[.06] ${value === i ? 'bg-white/[.05] text-white' : ''}`}>
              <span className={`w-[5px] h-[5px] rounded-full shrink-0 ${value === i ? 'bg-violet-400' : 'bg-transparent'}`} />
              {label}
            </button>
          ))}
        </div>,
        document.body
      )}
    </div>
  );
}

function MxNum({ value, onChange, cls, err }: { value: number; onChange: (v: number) => void; cls?: string; err?: string | null }) {
  const [draft, setDraft] = useState<string | null>(null);
  return (
    <div className="relative group">
      <input type="text" inputMode="decimal"
        value={draft !== null ? draft : value}
        onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); }}
        onBlur={() => { if (draft !== null && draft !== '' && draft !== '-') { const n = parseFloat(draft); if (!isNaN(n)) onChange(n); } setDraft(null); }}
        onFocus={() => setDraft(String(value))}
        className={cls ?? 'bg-gray-900 border border-gray-700 rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-violet-500'}
      />
      {err && <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">{err}</div>}
    </div>
  );
}

function MxNull({ value, onChange, err }: { value: number | null; onChange: (v: number | null) => void; err?: string | null }) {
  const [draft, setDraft] = useState<string | null>(null);
  const display = draft !== null ? draft : (value === null ? '' : String(value));
  return (
    <div className="relative group">
      <input type="text" inputMode="decimal" placeholder="—" value={display}
        onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); else if (e.target.value === '' || e.target.value === '-') onChange(null); }}
        onBlur={() => { if (draft !== null) { if (draft === '' || draft === '-') onChange(null); else { const n = parseFloat(draft); onChange(isNaN(n) ? null : n); } } setDraft(null); }}
        onFocus={() => setDraft(value === null ? '' : String(value))}
        className={`bg-gray-900 border rounded py-1 px-1 text-[11px] text-center w-full outline-none focus:border-violet-500 ${err ? 'border-red-500 text-gray-100' : value === null && draft === null ? 'border-dashed border-gray-700 text-gray-500' : 'border-gray-700 text-gray-100'}`}
      />
      {err && <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">{err}</div>}
    </div>
  );
}

function SectionHeader({ children, icon, color }: { children: React.ReactNode; icon?: string; color?: 'amber' | 'blue' | 'green' }) {
  const cls = color === 'amber' ? 'text-amber-400 border-amber-900/40'
    : color === 'blue'          ? 'text-blue-400  border-blue-900/40'
    :                             'text-green-400 border-green-900/40';
  return (
    <div className={`flex items-center gap-2 text-[11px] font-bold uppercase tracking-wider ${cls} pt-1`}>
      {icon && <span>{icon}</span>}
      <span>{children}</span>
      <div className="h-px flex-1 bg-current opacity-20" />
    </div>
  );
}

function ToggleSwitch({ enabled, onToggle }: { enabled: boolean; onToggle: () => void }) {
  return (
    <button type="button" onClick={onToggle}
      className={`w-9 h-5 rounded-full relative transition-colors shrink-0 ${enabled ? 'bg-violet-500' : 'bg-gray-700'}`}>
      <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${enabled ? 'translate-x-4' : 'translate-x-0'}`} />
    </button>
  );
}

function Field({ label, children, required, hint }: { label: string; children: React.ReactNode; required?: boolean; hint?: string }) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline gap-2">
        <label className="block text-[11px] font-bold uppercase tracking-wider text-slate-400">
          {label}{required && <span className="ml-0.5 text-violet-400">*</span>}
        </label>
        {hint && <span className="text-[10px] text-slate-600">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

const inputCls = 'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-violet-500/50';
const labelCls = 'flex items-center text-xs text-gray-500 mb-1';
