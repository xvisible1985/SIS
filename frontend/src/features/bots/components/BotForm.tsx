import { useState, useEffect, useRef, useMemo } from 'react';
import { getStrategyDefaults } from '../../admin-defaults/api';
import { X, Bot, ToggleLeft, ToggleRight, Camera, Trash2, Search, Loader2 } from 'lucide-react';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { Toggle, Tip, SignalPickerField } from '../../../components/strategies/FormWidgets';
import { SIGNALS } from '../../indicators/signals';
import { apiClient } from '../../../api/client';
import { getAllSymbols, matchesPattern } from '../../../components/common/CoinMultiPicker';
import { getInstrumentConstraints, type InstrumentConstraints } from '../../../api/strategies';
import type { Bot as BotType, BotKind, CreateBotInput, StrategyConfig, MatrixLevel, MatrixEntryLevel } from '../types';
import { getBotKindMeta } from '../botKindMeta';
import type { SignalConfig } from '../../../types';
import { useSelectedAccount } from '../../../contexts/AccountContext';

type Props = {
  bot?: BotType;
  initialKind?: BotKind;
  onSubmit: (data: CreateBotInput) => Promise<void> | void;
  onClose: () => void;
  mode?: 'user' | 'admin';
};

type OuterTab = 'basic' | 'activation' | 'strategy';
type StrategySubTab = 'entry' | 'grid' | 'exit' | 'matrix' | 'params';

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

type ScanItem = { symbol: string; state: 'buy' | 'sell' };

// ─── Default config ───────────────────────────────────────────────────────────

function defaultConfig(bot?: BotType, kind?: BotKind): StrategyConfig {
  const s = bot?.strategyConfig ?? {};
  return {
    bot_kind:              s.bot_kind              ?? kind ?? 'signal',
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
      { price_move_pct: 0,   size_pct: 50 },
      { price_move_pct: 1.5, size_pct: 100 },
      { price_move_pct: 2.0, size_pct: 150 },
    ],
    signal_configs:        (s.signal_configs        as SignalConfig[] | undefined) ?? [],
    activation_signals:    (s.activation_signals    as SignalConfig[] | undefined) ?? [],
    tp_mode:               s.tp_mode               ?? 'total',
    tp_pct:                s.tp_pct                ?? 2.0,
    sl_type:               s.sl_type               ?? 'conditional',
    sl_pct:                s.sl_pct                ?? 5.0,
    signal_filter:         s.signal_filter         ?? false,
    trailing_stop_enabled: s.trailing_stop_enabled ?? false,
    trailing_activation_pct: s.trailing_activation_pct ?? 1.5,
    trailing_callback_pct:   s.trailing_callback_pct   ?? 0.5,
    after_stop_mode:         s.after_stop_mode         ?? 'restart',
    max_cycles:              s.max_cycles              ?? 0,
    priority_signal:         s.priority_signal         ?? undefined,
    // Signal-gated exit
    tp_signal_name:          s.tp_signal_name          ?? undefined,
    tp_signal_dir:           s.tp_signal_dir           ?? undefined,
    sl_signal_name:          s.sl_signal_name          ?? undefined,
    sl_signal_dir:           s.sl_signal_dir           ?? undefined,
    // Matrix-specific
    matrix_levels:           s.matrix_levels           ?? DEFAULT_MATRIX_LEVELS,
    matrix_entry_level:      s.matrix_entry_level      ?? DEFAULT_MATRIX_ENTRY,
    safe_zone_pct:           s.safe_zone_pct           ?? 1.5,
    protected_build:         s.protected_build         ?? false,
    matrix_rebuild_on_sl:    s.matrix_rebuild_on_sl    ?? false,
    matrix_rebuild_from_entry: s.matrix_rebuild_from_entry ?? false,
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

export function BotForm({ bot, initialKind, onSubmit, onClose, mode = 'user' }: Props) {
  const [outerTab, setOuterTab]   = useState<OuterTab>('basic');
  const [stratTab, setStratTab]   = useState<StrategySubTab>('entry');

  const [name,        setName]        = useState(bot?.name        ?? '');
  const [description, setDescription] = useState(bot?.description ?? '');
  const [fullDescription, setFullDescription] = useState(bot?.fullDescription ?? '');
  const [isPublic,    setIsPublic]    = useState(bot?.isPublic    ?? false);
  const [avatarUrl,   setAvatarUrl]   = useState<string>(bot?.avatarUrl ?? '');
  const [whitelist,       setWhitelist]       = useState<string[]>(bot?.symbolWhitelist ?? []);
  const [blacklist,       setBlacklist]       = useState<string[]>(bot?.symbolBlacklist ?? []);
  const [config,          setConfig]          = useState<StrategyConfig>(defaultConfig(bot, initialKind));
  const [maxStrategies,      setMaxStrategies]      = useState<number>(bot?.maxStrategies ?? 0);
  const [maxLongStrategies,  setMaxLongStrategies]  = useState<number>(bot?.maxLongStrategies ?? 0);
  const [maxShortStrategies, setMaxShortStrategies] = useState<number>(bot?.maxShortStrategies ?? 0);
  const [maxMarginUsdt,      setMaxMarginUsdt]      = useState<number>(bot?.maxMarginUsdt ?? 0);
  const [maxSymConsecutiveRuns, setMaxSymConsecutiveRuns] = useState<number>(bot?.maxSymConsecutiveRuns ?? 0);
  const [autoMode,        setAutoMode]        = useState<boolean>(bot?.autoMode ?? false);
  const [submitting,      setSubmitting]      = useState(false);
  const { selectedAccountId } = useSelectedAccount();
  const [submitError, setSubmitError] = useState<string | null>(null);

  const [scanLoading,  setScanLoading]  = useState(false);
  const [scanResult,   setScanResult]   = useState<ScanItem[] | null>(null);
  const [scanError,    setScanError]    = useState<string | null>(null);
  const [allSymbols,   setAllSymbols]   = useState<string[]>([]);
  const [instrInfo,    setInstrInfo]    = useState<InstrumentConstraints | null>(null);
  const [minLotEnabled, setMinLotEnabled] = useState(false);
  const [leverageMax,   setLeverageMax]   = useState(false);
  const [sizeAsMain,    setSizeAsMain]    = useState<boolean>(bot?.strategyConfig?.size_as_main ?? false);

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  // Сбрасываем вкладку стратегии при смене типа стратегии
  useEffect(() => {
    setStratTab('entry');
  }, [config.strategy_type]);

  // Загружаем все доступные символы при открытии вкладки Активация
  useEffect(() => {
    if (outerTab === 'activation' && allSymbols.length === 0) {
      getAllSymbols().then(setAllSymbols);
    }
  }, [outerTab]); // eslint-disable-line react-hooks/exhaustive-deps

  // Загружаем ограничения инструмента для отображения минимального лота (используем BTCUSDT как эталон)
  useEffect(() => {
    const category = config.category ?? 'linear';
    setInstrInfo(null);
    getInstrumentConstraints('BTCUSDT', category).then(setInstrInfo).catch(() => setInstrInfo(null));
  }, [config.category]); // eslint-disable-line react-hooks/exhaustive-deps

  // Если включён min-lot — форсируем grid_size_usdt
  useEffect(() => {
    if (minLotEnabled && instrInfo && instrInfo.min_order_usdt > 0) {
      patch({ grid_size_usdt: instrInfo.min_order_usdt });
    }
  }, [minLotEnabled, instrInfo]); // eslint-disable-line react-hooks/exhaustive-deps

  // Apply admin-configured defaults for new bots
  useEffect(() => {
    if (bot) return // don't override existing bot config
    getStrategyDefaults().then(defaults => {
      const isMatrix = config.strategy_type === 'matrix'
      const d = isMatrix ? defaults.matrix : defaults.grid
      if (!d) return
      setConfig(c => {
        const overrides: Partial<typeof c> = {}
        if (d.leverage !== undefined) overrides.leverage = d.leverage
        if (d.grid_size_usdt !== undefined) overrides.grid_size_usdt = d.grid_size_usdt
        if (!isMatrix) {
          const gd = d as import('../../admin-defaults/types').GridDefaults
          if (gd.tp_pct !== undefined) overrides.tp_pct = gd.tp_pct
          if (gd.sl_pct !== undefined) overrides.sl_pct = gd.sl_pct
          if (gd.trailing_activation_pct !== undefined) overrides.trailing_activation_pct = gd.trailing_activation_pct
          if (gd.trailing_callback_pct !== undefined) overrides.trailing_callback_pct = gd.trailing_callback_pct
          if (gd.steps) overrides.steps = gd.steps
        } else {
          const md = d as import('../../admin-defaults/types').MatrixDefaults
          if (md.safe_zone_pct !== undefined) overrides.safe_zone_pct = md.safe_zone_pct
        }
        return { ...c, ...overrides }
      })
    }).catch(() => {}) // silently ignore errors — fall back to hardcoded defaults
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Итоговый список монет: применяем правила whitelist и blacklist (с поддержкой масок)
  const resolvedSymbols = useMemo(() => {
    if (allSymbols.length === 0) return [];
    let result = allSymbols;
    if (whitelist.length > 0) {
      result = result.filter(sym => whitelist.some(rule => matchesPattern(sym, rule)));
    }
    result = result.filter(sym => !blacklist.some(rule => matchesPattern(sym, rule)));
    return result;
  }, [allSymbols, whitelist, blacklist]);

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

  function patchStep(i: number, field: 'price_move_pct' | 'size_pct', value: number) {
    const steps = (config.steps ?? []).map((s, idx) =>
      idx === i ? { ...s, [field]: value } : s
    );
    patch({ steps });
  }

  function addStep() {
    patch({ steps: [...(config.steps ?? []), { price_move_pct: 1.0, size_pct: 50 }] });
  }

  function removeStep(i: number) {
    patch({ steps: (config.steps ?? []).filter((_, idx) => idx !== i) });
  }

  // ── Matrix helpers ────────────────────────────────────────────────────────
  const matrixLevels      = config.matrix_levels      ?? DEFAULT_MATRIX_LEVELS;
  const matrixEntryLevel  = config.matrix_entry_level ?? DEFAULT_MATRIX_ENTRY;
  const aboveLevels = matrixLevels.filter(l => l.direction === 'above');
  const belowLevels = matrixLevels.filter(l => l.direction === 'below');

  function addAboveLevel() {
    const last = aboveLevels[aboveLevels.length - 1];
    const newLevel: MatrixLevel = {
      direction: 'above',
      price_step_pct: last ? +(last.price_step_pct + 2).toFixed(1) : 2.0,
      size_pct: last ? +(last.size_pct + 2).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    };
    patch({ matrix_levels: [...matrixLevels, newLevel] });
  }
  function addBelowLevel() {
    const last = belowLevels[belowLevels.length - 1];
    const newLevel: MatrixLevel = {
      direction: 'below',
      price_step_pct: last ? +(last.price_step_pct - 1.5).toFixed(1) : -1.5,
      size_pct: last ? +(last.size_pct + 3).toFixed(1) : 5,
      stop_pct: null, stop_cond_pct: null, stop_replace_pct: null, tp_pct: null,
    };
    patch({ matrix_levels: [...matrixLevels, newLevel] });
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
    let dirCount = 0;
    patch({ matrix_levels: matrixLevels.map(l => {
      if (l.direction !== direction) return l;
      if (dirCount++ === idx) return { ...l, [field]: value };
      return l;
    }) });
  }
  function updateEntryLevel(field: keyof MatrixEntryLevel, value: number | null | string | boolean) {
    patch({ matrix_entry_level: { ...matrixEntryLevel, [field]: value } });
  }

  const matrixLevelErrors = matrixLevels.map(l => ({
    price_step_pct: l.direction === 'above' && l.price_step_pct <= 0 ? 'Должно быть положительным'
                  : l.direction === 'below' && l.price_step_pct >= 0 ? 'Должно быть отрицательным'
                  : null,
    size_pct: l.size_pct <= 0 ? 'Объём > 0' : null,
    stop_pct: l.stop_pct !== null && l.stop_pct >= 0 ? 'Стоп отрицательным' : null,
  }));
  const matrixEntryErrors = {
    size_pct: matrixEntryLevel.size_pct <= 0 ? 'Объём > 0' : null,
    stop_pct: matrixEntryLevel.stop_pct !== null && matrixEntryLevel.stop_pct >= 0 ? 'Стоп отрицательным' : null,
  };

  const handleScan = async () => {
    const signals = (config.activation_signals as SignalConfig[]) ?? [];
    if (signals.length === 0) {
      setScanError('Добавьте хотя бы один сигнал для проверки');
      return;
    }
    const targets = resolvedSymbols.length > 0 ? resolvedSymbols : allSymbols;
    if (targets.length === 0) {
      setScanError('Список символов ещё загружается');
      return;
    }
    // Extract interval from signal config (params.tf), fall back to '15'.
    const interval: string = signals.reduce<string>((acc, s) => {
      const tf = (s as { params?: Record<string, unknown> }).params?.tf;
      return acc !== '15' ? acc : (typeof tf === 'string' && tf ? tf : acc);
    }, '15');
    setScanLoading(true);
    setScanError(null);
    setScanResult(null);
    try {
      const res = await apiClient.post<{ results: ScanItem[] }>('/bots/signal-scan', {
        signal_configs: signals,
        whitelist: targets,   // уже разрешённый список, маски применены на фронте
        blacklist: [],
        interval,
        direction: config.direction ?? 'both',
      });
      setScanResult(res.data.results ?? []);
    } catch {
      setScanError('Ошибка при проверке символов');
    } finally {
      setScanLoading(false);
    }
  };

  const handleSubmit = async () => {
    if (!name.trim()) return;
    setSubmitting(true);
    setSubmitError(null);
    const steps = config.steps ?? [];
    const isMatrix = config.strategy_type === 'matrix';
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
          grid_levels: isMatrix ? totalMatrixLevels : (steps.length || 1),
          grid_step_pct: isMatrix
            ? (belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0)
            : (steps[0]?.price_move_pct ?? 1.0),
          signal_filter: (config.signal_configs?.length ?? 0) > 0,
          size_as_main: isMatrix ? sizeAsMain : false,
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

  const outerTabs: { id: OuterTab; label: string }[] = [
    { id: 'basic',      label: 'Основное' },
    { id: 'activation', label: 'Активация' },
    { id: 'strategy',   label: 'Стратегия' },
  ];

  const stratTabs: { id: StrategySubTab; label: string }[] = config.strategy_type === 'matrix'
    ? [
        { id: 'entry',  label: '1. Базовые' },
        { id: 'matrix', label: '2. Матрица' },
        { id: 'params', label: '3. Параметры' },
      ]
    : [
        { id: 'entry', label: '1. Базовые' },
        { id: 'grid',  label: '2. Сетка ордеров' },
        { id: 'exit',  label: '3. Завершение' },
      ];

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur">
      <div className="flex max-h-[calc(100vh-48px)] w-full max-w-[680px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]">
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
              {bot ? (mode === 'admin' ? 'Редактировать NovaBot' : 'Редактировать бота')
                   : (mode === 'admin' ? 'Создать NovaBot' : 'Создать бота')}
            </h2>
            <p className="text-[11px] text-slate-400">{bot ? bot.name : (mode === 'admin' ? 'Новый официальный бот' : 'Новый торговый бот')}</p>
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

                <div className="flex flex-1 flex-col gap-2">
                  <Field label="Название" required>
                    <input
                      type="text"
                      value={name}
                      onChange={e => setName(e.target.value.slice(0, 30))}
                      placeholder="BTC Grid Pro"
                      className={inputCls}
                      maxLength={30}
                    />
                  </Field>
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
                    <span className="text-[11px] text-slate-500">PNG, JPG, GIF, WebP · до 300×300</span>
                  </div>
                </div>
              </div>
              <Field label="Краткое описание" hint="Показывается под названием на карточке бота">
                <div className="relative">
                  <textarea
                    value={description}
                    onChange={e => setDescription(e.target.value.slice(0, 120))}
                    placeholder="Сеточная стратегия по тренду на BTC"
                    rows={2}
                    className={`${inputCls} resize-none`}
                    maxLength={120}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">
                    {description.length}/120
                  </span>
                </div>
              </Field>
              <Field label="Подробное описание" hint="Развёрнутое описание для страницы бота">
                <div className="relative">
                  <textarea
                    value={fullDescription}
                    onChange={e => setFullDescription(e.target.value.slice(0, 2000))}
                    placeholder="Опишите логику работы бота, рекомендуемые рыночные условия, особенности настройки..."
                    rows={4}
                    className={`${inputCls} resize-none`}
                    maxLength={2000}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">
                    {fullDescription.length}/2000
                  </span>
                </div>
              </Field>
              {mode !== 'admin' && (
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
              )}

              {/* Авто-режим */}
              <div className="rounded-lg border border-white/[.06] bg-white/[.02]">
                <div className="flex items-center justify-between px-4 py-3">
                  <div>
                    <div className="text-[13px] font-semibold text-slate-200">Авто-режим</div>
                    <div className="mt-0.5 text-[11px] text-slate-400">
                      Бот сам открывает стратегии при срабатывании сигналов — без подтверждения
                    </div>
                  </div>
                  <button type="button" onClick={() => setAutoMode(v => !v)} className="text-slate-400">
                    {autoMode
                      ? <ToggleRight size={28} className="text-emerald-400" />
                      : <ToggleLeft size={28} />}
                  </button>
                </div>
                <div className="flex items-center justify-between border-t border-white/[.05] px-4 py-2.5">
                  <div>
                    <div className="text-[12px] font-medium text-slate-300">Удалять стратегию после TP/SL</div>
                    <div className="text-[11px] text-slate-500">
                      {config.after_stop_mode === 'delete'
                        ? 'Стратегия удаляется — бот сможет открыть новую на той же монете'
                        : 'Стратегия перезапускается — продолжает работу без нового сигнала'}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => patch({ after_stop_mode: config.after_stop_mode === 'delete' ? 'restart' : 'delete' })}
                    className="ml-4 shrink-0 text-slate-400"
                  >
                    {config.after_stop_mode === 'delete'
                      ? <ToggleRight size={26} className="text-[#5b8cff]" />
                      : <ToggleLeft size={26} />}
                  </button>
                </div>
              </div>

              {/* Ограничения */}
              <div>
                <div className="mb-3 flex items-center gap-2">
                  <span className="text-[11px] font-bold uppercase tracking-wider text-slate-500">Ограничения</span>
                  <div className="h-px flex-1 bg-white/[.05]" />
                </div>
                <div className="grid grid-cols-3 gap-3 mb-3">
                  <Field label="Всего стратегий" hint="0 = ∞">
                    <input
                      type="number"
                      min={0}
                      step={1}
                      value={maxStrategies}
                      onChange={e => setMaxStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={inputCls}
                    />
                  </Field>
                  <Field label="Макс. лонг" hint="0 = ∞">
                    <input
                      type="number"
                      min={0}
                      step={1}
                      value={maxLongStrategies}
                      onChange={e => setMaxLongStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={`${inputCls} border-emerald-900/40`}
                    />
                  </Field>
                  <Field label="Макс. шорт" hint="0 = ∞">
                    <input
                      type="number"
                      min={0}
                      step={1}
                      value={maxShortStrategies}
                      onChange={e => setMaxShortStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={`${inputCls} border-rose-900/40`}
                    />
                  </Field>
                </div>
                <Field label="Лимит маржи, USDT" hint="0 — без ограничений">
                  <input
                    type="number"
                    min={0}
                    step={10}
                    value={maxMarginUsdt}
                    onChange={e => setMaxMarginUsdt(Math.max(0, parseFloat(e.target.value) || 0))}
                    className={inputCls}
                  />
                </Field>
                <Field label="Повторов подряд" hint="0 = ∞">
                  <input
                    type="number"
                    min={0}
                    step={1}
                    value={maxSymConsecutiveRuns}
                    onChange={e => setMaxSymConsecutiveRuns(Math.max(0, parseInt(e.target.value) || 0))}
                    className={inputCls}
                  />
                </Field>
                <p className="mt-2 text-[11px] text-slate-600">
                  Бот не откроет стратегию при достижении лимита. Лонг/шорт — отдельные счётчики. «Повторов подряд» — после N запусков одной пары бот пропустит её, пока не запустит другую.
                </p>
              </div>

            </div>
          )}

          {outerTab === 'strategy' && (
            <div className="flex flex-col gap-4">
              {bot && (
                <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[.08] px-3 py-2.5 text-[11.5px] text-amber-300 leading-relaxed">
                  <span className="mt-px shrink-0 text-amber-400">⚠</span>
                  <span>Изменения настроек стратегии применятся только к <strong className="text-amber-200">новым стратегиям</strong>, которые бот создаст в будущем. Уже запущенные стратегии затронуты не будут.</span>
                </div>
              )}
              {/* strategy type selector */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-bold uppercase tracking-wider text-slate-400">Тип стратегии</span>
                <div className="flex gap-1">
                  {(['grid', 'matrix'] as const).map(t => (
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
                  <div className="flex items-start gap-2.5 rounded-lg border border-[#5b8cff]/20 bg-[#5b8cff]/[.07] px-3 py-2.5">
                    <span className="mt-0.5 shrink-0 text-[14px] text-[#5b8cff]">⚡</span>
                    <p className="text-[11px] leading-relaxed text-slate-400">
                      <span className="font-semibold text-slate-300">Монеты выбираются автоматически</span> — бот сканирует рынок по сигналам активации
                      и открывает стратегию на каждой подходящей монете. Настрой сигналы и список монет на вкладке{' '}
                      <button
                        type="button"
                        onClick={() => setOuterTab('activation')}
                        className="font-semibold text-[#a0b8ff] underline underline-offset-2 hover:text-[#c4d4ff]"
                      >
                        Активация
                      </button>.
                    </p>
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

                  {/* ── Депозит + Плечо ── */}
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>
                        Депозит (USDT)
                        {instrInfo && (
                          <span className="ml-1.5 text-[10px] text-slate-500">
                            мин. {instrInfo.min_order_usdt.toFixed(2)}$
                          </span>
                        )}
                        <Tip text="Базовая сумма в USDT, от которой считается % каждого шага. USDT шага = % × депозит / 100." />
                      </label>
                      <MxNum
                        value={config.grid_size_usdt ?? 100}
                        onChange={v => patch({ grid_size_usdt: v })}
                        cls={inputCls}
                      />
                    </div>
                    <div className="grid grid-cols-[1fr_auto] gap-2">
                      <div>
                        <label className={labelCls}>
                          Плечо&nbsp;<span className="font-bold text-white">×{config.leverage ?? 5}</span>
                          {instrInfo && <span className="ml-1.5 text-[10px] text-slate-500">макс. ×{instrInfo.max_leverage}</span>}
                          <Tip text="Кратность заёмных средств. ×5 = на $100 открывается позиция $500." />
                        </label>
                        <input
                          type="range" min={1} max={instrInfo?.max_leverage ?? 100} step={1}
                          value={config.leverage ?? 5}
                          disabled={leverageMax}
                          onChange={e => { setLeverageMax(false); patch({ leverage: parseInt(e.target.value) }) }}
                          className="w-full h-1.5 mt-2 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                          style={{ accentColor: getBotKindMeta(initialKind ?? 'signal').color }}
                        />
                        <div className="flex justify-between text-[10px] text-gray-500 mt-0.5">
                          <span>×1</span>
                          <span>×{instrInfo?.max_leverage ?? 100}</span>
                        </div>
                      </div>
                      <div className="self-end min-w-[96px]">
                        <Toggle
                          options={[
                            { label: 'Фикс.', value: 'fixed' },
                            { label: 'Макс.', value: 'max'   },
                          ]}
                          value={leverageMax ? 'max' : 'fixed'}
                          onChange={v => {
                            const isMax = v === 'max'
                            setLeverageMax(isMax)
                            if (isMax) patch({ leverage: instrInfo?.max_leverage ?? 100 })
                          }}
                        />
                      </div>
                    </div>
                  </div>

                  {/* ── Шаги усреднения ── */}
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
                          <MxNum
                            value={step.price_move_pct}
                            onChange={v => patchStep(i, 'price_move_pct', v)}
                            cls="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                          />
                          <MxNum
                            value={step.size_pct ?? 0}
                            onChange={v => patchStep(i, 'size_pct', v)}
                            cls="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
                          />
                          <MxNum
                            value={+((step.size_pct ?? 0) / 100 * (config.grid_size_usdt ?? 100)).toFixed(2)}
                            onChange={v => {
                              const base = config.grid_size_usdt ?? 100;
                              if (base > 0)
                                patchStep(i, 'size_pct', Math.round(v / base * 10000) / 100);
                            }}
                            cls="bg-gray-800 border border-gray-700 rounded px-2 py-1 text-[11px] text-gray-100 text-center w-full"
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
                    <MxNum
                      value={config.grid_active ?? 0}
                      onChange={v => patch({ grid_active: Math.max(0, Math.round(v)) })}
                      cls={inputCls}
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

              {stratTab === 'matrix' && (() => {
                const colGrid = 'grid grid-cols-[30px_1fr_1fr_6px_1fr_1fr_1fr_1fr_42px_36px_18px] gap-[3px] items-center';
                const sep = <div className="flex items-center justify-center"><div className="w-px h-[20px] bg-gray-700/60" /></div>;
                const isShort = config.direction === 'short';
                const numCls = (err: boolean, disabled: boolean) =>
                  `bg-gray-900 border rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-blue-500 ${err ? 'border-red-500' : 'border-gray-700'} ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`;

                const orderTypeBtn = (isVirtual: boolean, onToggle: () => void) => (
                  <button
                    type="button"
                    onClick={onToggle}
                    title={isVirtual ? 'Виртуальный — бот следит за ценой' : 'Биржевой — ордер сразу на биржу'}
                    className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                      isVirtual
                        ? 'bg-violet-950/60 text-violet-300 border border-violet-800/50 hover:bg-violet-900/60'
                        : 'bg-gray-800 text-gray-500 border border-gray-700 hover:text-gray-300'
                    }`}
                  >
                    {isVirtual ? 'Virtual' : 'Broker'}
                  </button>
                );

                const colHdrs = () => (
                  <div className={`${colGrid} px-1 pb-1`}>
                    <span />
                    <span className="text-[8px] text-gray-400 text-center">Шаг %<Tip text="% от точки входа до этого уровня." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Объём %<Tip text="Размер ордера как % от Депозита." /></span>
                    <span />
                    <span className="text-[8px] text-gray-400 text-center">Стоп %<Tip text="Расстояние частичного стопа от цены взятия. Пусто = не ставить." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Усл %<Tip text="Условие перевыставления стопа." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Замена %<Tip text="Новое расстояние стопа после перевыставления." /></span>
                    <span className="text-[8px] text-gray-400 text-center">ТП %<Tip text="Тейкпрофит от цены входа. Пусто = не ставить." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Тип<Tip text="Broker — сразу на биржу. Virtual — бот следит за ценой." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Сигн<Tip text="Выставлять только при активном сигнале стратегии." /></span>
                    <span />
                  </div>
                );

                const levelRow = (
                  direction: 'above' | 'below',
                  level: MatrixLevel,
                  dirIdx: number,
                  labelNum: number,
                  canDelete: boolean
                ) => {
                  const isAbove = direction === 'above';
                  const badgeCls = isAbove
                    ? (isShort ? 'bg-red-900 text-red-300 text-[8px] font-bold rounded text-center py-0.5'
                               : 'bg-emerald-900 text-emerald-300 text-[8px] font-bold rounded text-center py-0.5')
                    : 'bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5';
                  const rowCls = isAbove
                    ? (isShort ? 'bg-red-950/20' : 'bg-emerald-950/20')
                    : 'bg-blue-950/30';
                  const globalIdx = matrixLevels.indexOf(level);
                  const errs = matrixLevelErrors[globalIdx] ?? {};
                  return (
                    <div key={`${direction}-${dirIdx}`} className={`${colGrid} px-1 py-1 rounded mb-0.5 ${rowCls}`}>
                      <div className={badgeCls}>{isAbove ? `L(${labelNum})` : `L(-${labelNum})`}</div>
                      <MxNum value={level.price_step_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'price_step_pct', v)} err={errs.price_step_pct} cls={numCls(!!errs.price_step_pct, false)} />
                      <MxNum value={level.size_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'size_pct', v)} err={errs.size_pct} cls={numCls(!!errs.size_pct, false)} />
                      {sep}
                      <MxNull value={level.stop_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_pct', v)} err={errs.stop_pct} />
                      <MxNull value={level.stop_cond_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_cond_pct', v)} />
                      <MxNull value={level.stop_replace_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'stop_replace_pct', v)} />
                      <MxNull value={level.tp_pct} onChange={v => updateMatrixLevel(direction, dirIdx, 'tp_pct', v)} />
                      {orderTypeBtn(level.order_type === 'virtual', () => updateMatrixLevel(direction, dirIdx, 'order_type', level.order_type === 'virtual' ? 'exchange' : 'virtual'))}
                      <button
                        type="button"
                        onClick={() => updateMatrixLevel(direction, dirIdx, 'use_signal', !level.use_signal)}
                        className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                          level.use_signal
                            ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50 hover:bg-emerald-900/60'
                            : 'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'
                        }`}
                      >{level.use_signal ? 'Signal' : 'No'}</button>
                      {canDelete
                        ? <button type="button" onClick={() => removeMatrixLevel(direction, dirIdx)} className="text-center text-xs text-red-500 hover:text-red-400">✕</button>
                        : <span />}
                    </div>
                  );
                };

                return (
                  <div className="flex flex-col gap-3">
                    {/* ── Строка 1: Депозит + toggle Фикс./Объём Main ── */}
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          Депозит
                          {instrInfo && !sizeAsMain && (
                            <span className="ml-1.5 text-[10px] text-slate-500">
                              мин. {instrInfo.min_order_usdt.toFixed(2)}$
                            </span>
                          )}
                          <Tip text="Фиксированный — вводите USDT. Объём Main — размер от текущего объёма мейн-позиции." />
                        </label>
                        {sizeAsMain ? (
                          <div className="flex items-center h-[34px] px-3 rounded-lg border border-[#5b8cff]/20 bg-[#5b8cff]/[.08] text-[11px] text-[#a0b8ff]">
                            = объём Main-позиции
                          </div>
                        ) : (
                          <div className="flex items-center gap-2">
                            <MxNum
                              value={config.grid_size_usdt ?? 100}
                              onChange={v => patch({ grid_size_usdt: v })}
                              disabled={minLotEnabled}
                              cls={`${inputCls} ${minLotEnabled ? 'cursor-not-allowed opacity-50' : ''}`}
                            />
                            <button
                              type="button"
                              onClick={() => setMinLotEnabled(v => !v)}
                              className={`shrink-0 rounded-lg border px-3 py-2 text-[11px] font-semibold transition-colors ${
                                minLotEnabled
                                  ? 'border-[#5b8cff]/40 bg-[#5b8cff]/[.18] text-[#a0b8ff]'
                                  : 'border-white/[.07] bg-white/[.02] text-slate-400 hover:bg-white/[.05]'
                              }`}
                            >
                              min
                            </button>
                          </div>
                        )}
                      </div>
                      <div className="flex flex-col justify-end">
                        <label className={labelCls}>
                          Режим депозита
                          <Tip text="Фиксированный — задаёте USDT вручную. Объём Main — каждый слот размещается от объёма мейн-позиции." />
                        </label>
                        <Toggle
                          options={[
                            { label: 'Фикс.',       value: 'fixed' },
                            { label: 'Объём Main',  value: 'main'  },
                          ]}
                          value={sizeAsMain ? 'main' : 'fixed'}
                          onChange={v => setSizeAsMain(v === 'main')}
                          btnClassName="!py-2"
                        />
                      </div>
                    </div>

                    {/* ── Строка 2: Плечо слайдер + toggle Фикс./Макс. ── */}
                    <div className="grid grid-cols-2 gap-3 items-end">
                      <div>
                        <label className={labelCls}>
                          Плечо&nbsp;
                          <span className="font-bold text-white">×{config.leverage ?? 5}</span>
                          {instrInfo && <span className="ml-1.5 text-[10px] text-slate-500">макс. ×{instrInfo.max_leverage}</span>}
                          <Tip text="Кратность заёмных средств." />
                        </label>
                        <input
                          type="range"
                          min={1}
                          max={instrInfo?.max_leverage ?? 100}
                          step={1}
                          value={config.leverage ?? 5}
                          disabled={leverageMax}
                          onChange={e => { setLeverageMax(false); patch({ leverage: parseInt(e.target.value) }) }}
                          className="w-full h-1.5 mt-2 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                          style={{ accentColor: getBotKindMeta(initialKind ?? 'signal').color }}
                        />
                        <div className="flex justify-between text-[10px] text-gray-500 mt-0.5">
                          <span>×1</span>
                          <span>×{instrInfo?.max_leverage ?? 100}</span>
                        </div>
                      </div>
                      <div className="flex flex-col justify-end pb-[22px]">
                        <label className={labelCls}>Фикс. / Макс.</label>
                        <Toggle
                          options={[
                            { label: 'Фикс.', value: 'fixed' },
                            { label: 'Макс.', value: 'max'   },
                          ]}
                          value={leverageMax ? 'max' : 'fixed'}
                          onChange={v => {
                            const isMax = v === 'max'
                            setLeverageMax(isMax)
                            if (isMax) patch({ leverage: instrInfo?.max_leverage ?? 100 })
                          }}
                        />
                      </div>
                    </div>

                    {/* ABOVE */}
                    <div>
                      <div className="text-[9px] text-gray-400 uppercase tracking-wider flex items-center justify-between pb-1 border-b border-gray-800 mb-1">
                        {isShort
                          ? <span>Выше точки входа <span className="normal-case text-gray-600 font-normal">(против направления)</span></span>
                          : <span>Выше точки входа <span className="normal-case text-gray-600 font-normal">(в сторону)</span></span>}
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
                        <MxNum value={matrixEntryLevel.size_pct} onChange={v => updateEntryLevel('size_pct', v)} err={matrixEntryErrors.size_pct} cls={numCls(!!matrixEntryErrors.size_pct, false)} />
                        {sep}
                        <MxNull value={matrixEntryLevel.stop_pct} onChange={v => updateEntryLevel('stop_pct', v)} err={matrixEntryErrors.stop_pct} />
                        <MxNull value={matrixEntryLevel.stop_cond_pct} onChange={v => updateEntryLevel('stop_cond_pct', v)} />
                        <MxNull value={matrixEntryLevel.stop_replace_pct} onChange={v => updateEntryLevel('stop_replace_pct', v)} />
                        <MxNull value={matrixEntryLevel.tp_pct} onChange={v => updateEntryLevel('tp_pct', v)} />
                        {orderTypeBtn(
                          matrixEntryLevel.order_type === 'virtual',
                          () => updateEntryLevel('order_type', matrixEntryLevel.order_type === 'virtual' ? 'exchange' : 'virtual')
                        )}
                        <button
                          type="button"
                          onClick={() => updateEntryLevel('use_signal', !matrixEntryLevel.use_signal)}
                          className={`text-[8px] leading-4 font-bold rounded w-full py-1 transition-colors ${
                            matrixEntryLevel.use_signal
                              ? 'bg-emerald-950/60 text-emerald-300 border border-emerald-800/50 hover:bg-emerald-900/60'
                              : 'bg-gray-800 text-gray-600 border border-gray-700 hover:text-gray-400'
                          }`}
                        >{matrixEntryLevel.use_signal ? 'Signal' : 'No'}</button>
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
                        {isShort
                          ? <span>Ниже точки входа <span className="normal-case text-gray-600 font-normal">(в сторону)</span></span>
                          : <span>Ниже точки входа <span className="normal-case text-gray-600 font-normal">(против направления)</span></span>}
                        <span className="bg-blue-900/60 text-blue-400 rounded px-1.5 py-0.5 text-[8px]">{belowLevels.length} уровней</span>
                      </div>
                      <button type="button" onClick={addBelowLevel}
                        className="w-full border border-dashed border-blue-900 text-blue-600 rounded py-1 text-[10px] hover:border-blue-700 hover:text-blue-400 transition-colors">
                        + Добавить уровень ниже
                      </button>
                    </div>

                    {/* Сигналы */}
                    <div>
                      <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800">
                        Сигналы для входа<Tip text="AND-логика: все сигналы должны совпасть. Если не добавлены — матрица стартует сразу." />
                      </div>
                      <SignalPickerField
                        configs={(config.signal_configs as SignalConfig[]) ?? []}
                        onChange={v => patch({ signal_configs: v })}
                      />
                    </div>
                  </div>
                );
              })()}

              {stratTab === 'params' && (
                <div className="flex flex-col gap-4">
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>
                        Safe-Zone от SL %
                        <Tip text="После срабатывания стоп-лосса — зона вокруг цены SL, в которой новые ордера матрицы не выставляются. Защита от немедленного повторного входа." />
                      </label>
                      <MxNum
                        value={config.safe_zone_pct ?? 1.5}
                        onChange={v => patch({ safe_zone_pct: v })}
                        cls={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>
                        🔒 Защищённое построение
                        <Tip text="Следующий уровень матрицы выставляется только после того, как предыдущий прикрыт хотя бы одним стопом. Также блокирует возврат SL-уровня до подтверждения стопа на опорном уровне." />
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
                        ⟳ Перестройка сетки от SZ
                        <Tip text="После срабатывания SL немедленно переставляет все незаполненные уровни от нижней границы SafeZone, не дожидаясь заполнения соседних уровней. Если SafeZone = 0, anchor = цена SL." />
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
                        <Tip text="Все уровни строятся от цены заполнения L(0). После SL L(0) тоже ждёт SafeZone и перезаходит маркетом; остальные уровни восстанавливаются от новой цены входа." />
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
                        <MxNum
                          value={config.tp_pct ?? 2.0}
                          onChange={v => patch({ tp_pct: v })}
                          cls={inputCls}
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
                    {/* Trailing stop inside TP */}
                    <div className="pt-2 border-t border-green-900/40 space-y-3">
                      <div className="flex items-center gap-1 text-[10px] text-green-600/80 font-bold uppercase tracking-wider pb-1">
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
                            <MxNum
                              value={config.trailing_activation_pct ?? 1.5}
                              onChange={v => patch({ trailing_activation_pct: v })}
                              cls={inputCls}
                            />
                          </div>
                          <div>
                            <label className={labelCls}>
                              Callback %
                              <Tip text="Откат от пика, после которого трейлинг-стоп срабатывает." />
                            </label>
                            <MxNum
                              value={config.trailing_callback_pct ?? 0.5}
                              onChange={v => patch({ trailing_callback_pct: v })}
                              cls={inputCls}
                            />
                          </div>
                        </div>
                      )}
                    </div>
                    <BotSignalGate
                      label="Сигнал для ТП"
                      tip="Тейкпрофит сработает только при подтверждении указанным сигналом. Для Long-позиции ждите Sell-сигнал (разворот вниз)."
                      name={config.tp_signal_name ?? null}
                      dir={config.tp_signal_dir ?? null}
                      onChange={(n, d) => patch({ tp_signal_name: n ?? undefined, tp_signal_dir: d ?? undefined })}
                    />
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
                        <MxNum
                          value={config.sl_pct ?? 5.0}
                          onChange={v => patch({ sl_pct: v })}
                          cls={inputCls}
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
                    <BotSignalGate
                      label="Сигнал для СЛ"
                      tip="Стоп-лосс сработает только при подтверждении указанным сигналом. Для Long-позиции ждите Sell-сигнал (разворот вниз)."
                      name={config.sl_signal_name ?? null}
                      dir={config.sl_signal_dir ?? null}
                      onChange={(n, d) => patch({ sl_signal_name: n ?? undefined, sl_signal_dir: d ?? undefined })}
                    />
                  </div>

                </div>
              )}
            </div>
          )}

          {outerTab === 'activation' && (
            <div className="flex flex-col gap-5">

              {/* Signals */}
              <div>
                <div className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-slate-400 pb-1 border-b border-white/[.05] mb-2">
                  Сигналы активации
                  <Tip text="AND-логика: все указанные сигналы должны совпасть, чтобы символ был активирован." />
                </div>
                <SignalPickerField
                  configs={(config.activation_signals as SignalConfig[]) ?? []}
                  onChange={v => {
                    const names = new Set((v as SignalConfig[]).map(s => s.name));
                    const keep = config.priority_signal && names.has(config.priority_signal)
                      ? config.priority_signal
                      : undefined;
                    patch({ activation_signals: v, priority_signal: keep });
                  }}
                />
              </div>

              {/* Priority signal selector */}
              {((config.activation_signals as SignalConfig[]) ?? []).length > 0 && (
                <div>
                  <div className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-slate-400 pb-1 border-b border-white/[.05] mb-2">
                    Приоритетный сигнал
                    <Tip text="Бот сортирует монеты по этому сигналу. Volume Spike — по величине объёма. SuperTrend — по остатку TTL (самый свежий первым)." />
                  </div>
                  <div className="flex flex-col gap-1.5">
                    {((config.activation_signals as SignalConfig[]) ?? []).map(sig => (
                      <label key={sig.name} className="flex items-center gap-2.5 cursor-pointer group">
                        <input
                          type="radio"
                          name="priority_signal"
                          value={sig.name}
                          checked={config.priority_signal === sig.name}
                          onChange={() => patch({ priority_signal: sig.name })}
                          className="accent-[#5b8cff] h-3.5 w-3.5 cursor-pointer"
                        />
                        <span className="text-[12px] text-slate-300 group-hover:text-slate-100 transition-colors">
                          {sigLabel(sig.name)}
                        </span>
                        {config.priority_signal === sig.name && SIG_SORT_HINT[sig.name] && (
                          <span className="text-[10px] text-slate-500">— {SIG_SORT_HINT[sig.name]}</span>
                        )}
                      </label>
                    ))}
                    {config.priority_signal && (
                      <button
                        type="button"
                        onClick={() => patch({ priority_signal: undefined })}
                        className="mt-0.5 self-start text-[10px] text-slate-600 hover:text-slate-400 underline underline-offset-2"
                      >
                        Сбросить
                      </button>
                    )}
                  </div>
                </div>
              )}

              {/* Whitelist */}
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

              {/* Blacklist */}
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

              {/* Resolved symbols */}
              <div>
                <div className="mb-2 flex items-center justify-between">
                  <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400 pb-1 border-b border-white/[.05] flex-1">
                    Итоговый список символов
                  </div>
                </div>
                {allSymbols.length === 0 ? (
                  <div className="text-[11px] text-slate-500">Загрузка символов...</div>
                ) : resolvedSymbols.length === 0 && (whitelist.length > 0 || blacklist.length > 0) ? (
                  <div className="text-[11px] text-rose-400">Все символы исключены правилами</div>
                ) : (
                  <div>
                    <div className="mb-1.5 text-[11px] text-slate-400">
                      {whitelist.length === 0 && blacklist.length === 0
                        ? <span>Все символы биржи: <span className="font-semibold text-slate-200">{allSymbols.length}</span></span>
                        : <span>Подходит: <span className="font-semibold text-slate-200">{resolvedSymbols.length}</span> из {allSymbols.length}</span>
                      }
                    </div>
                    {(whitelist.length > 0 || blacklist.length > 0) && resolvedSymbols.length > 0 && (
                      <div className="flex flex-wrap gap-1 max-h-[100px] overflow-y-auto">
                        {resolvedSymbols.map(sym => (
                          <span key={sym} className="rounded-md border border-white/[.08] bg-white/[.03] px-1.5 py-0.5 font-mono text-[10px] text-slate-300">
                            {sym.replace(/USDT$/i, '')}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>

              {/* Scan button + results */}
              <div>
                <div className="mb-2 text-[10px] font-bold uppercase tracking-wider text-slate-400 pb-1 border-b border-white/[.05]">
                  Проверка в моменте
                </div>
                <p className="mb-3 text-[11px] text-slate-500">
                  Запускает проверку доступных символов на соответствие выбранным сигналам прямо сейчас.
                </p>
                <button
                  type="button"
                  onClick={handleScan}
                  disabled={scanLoading}
                  className="inline-flex items-center gap-2 rounded-lg border border-[#5b8cff]/30 bg-[#5b8cff]/[.12] px-4 py-2 text-[12px] font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.20] disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {scanLoading
                    ? <><Loader2 size={13} className="animate-spin" />Проверяем...</>
                    : <><Search size={13} />Проверить</>
                  }
                </button>

                {scanError && (
                  <div className="mt-3 rounded-lg border border-rose-500/30 bg-rose-500/[.08] px-3 py-2 text-[12px] text-rose-300">
                    {scanError}
                  </div>
                )}

                {scanResult !== null && (
                  <div className="mt-3">
                    {scanResult.length === 0 ? (
                      <div className="text-[12px] text-slate-500">Совпадений не найдено</div>
                    ) : (
                      <>
                        <div className="mb-1.5 text-[11px] text-slate-400">
                          Найдено символов: <span className="font-semibold text-slate-200">{scanResult.length}</span>
                        </div>
                        <div className="flex flex-wrap gap-1.5 max-h-[160px] overflow-y-auto">
                          {scanResult.map(r => (
                            <span
                              key={r.symbol}
                              className={
                                'rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ' +
                                (r.state === 'buy'
                                  ? 'border-emerald-500/30 bg-emerald-500/[.12] text-emerald-300'
                                  : 'border-rose-500/30 bg-rose-500/[.12] text-rose-300')
                              }
                            >
                              {r.symbol}
                            </span>
                          ))}
                        </div>
                      </>
                    )}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* footer */}
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
              className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              {submitting ? 'Сохранение...' : (bot ? 'Сохранить' : 'Создать')}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

/** Signal-gated exit widget for BotForm's TP/SL sections */
function BotSignalGate({ label, tip, name, dir, onChange }: {
  label: string;
  tip: string;
  name: string | null;
  dir: 'buy' | 'sell' | null;
  onChange: (name: string | null, dir: 'buy' | 'sell' | null) => void;
}) {
  const enabled = !!name;
  return (
    <div className="pt-2 border-t border-white/[.04] space-y-2">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => onChange(enabled ? null : (SIGNALS[0]?.id ?? ''), enabled ? null : 'sell')}
          className={`w-8 h-4 rounded-full relative transition-colors shrink-0 ${enabled ? 'bg-[#5b8cff]' : 'bg-gray-700'}`}
        >
          <span className={`absolute left-0.5 top-0.5 w-3 h-3 bg-white rounded-full shadow transition-transform ${enabled ? 'translate-x-4' : 'translate-x-0'}`} />
        </button>
        <span className="flex items-center gap-1 text-[10px] text-slate-400 font-semibold uppercase tracking-wider">
          {label}<Tip text={tip} />
        </span>
      </div>
      {enabled && (
        <div className="grid grid-cols-[1fr_auto] gap-2 pl-10">
          <select
            value={name ?? ''}
            onChange={e => onChange(e.target.value || null, dir)}
            className="w-full rounded-lg border border-white/[.08] bg-black/[.25] px-2 py-1.5 text-[12px] text-slate-200 outline-none focus:border-[#5b8cff]/50"
          >
            {SIGNALS.map(s => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
          <Toggle
            options={[{ label: 'Buy', value: 'buy' }, { label: 'Sell', value: 'sell' }]}
            value={dir ?? 'sell'}
            onChange={v => onChange(name, v as 'buy' | 'sell')}
            optionColors={{ buy: 'bg-emerald-700 text-white', sell: 'bg-rose-700 text-white' }}
          />
        </div>
      )}
    </div>
  );
}

/** Numeric input for matrix level cells (non-nullable) */
function MxNum({ value, onChange, cls, err, disabled }: {
  value: number;
  onChange: (v: number) => void;
  cls?: string;
  err?: string | null;
  disabled?: boolean;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  return (
    <div className="relative group">
      <input
        type="text" inputMode="decimal"
        disabled={disabled}
        value={draft !== null ? draft : value}
        onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); }}
        onBlur={() => { if (draft !== null && draft !== '' && draft !== '-') { const n = parseFloat(draft); if (!isNaN(n)) onChange(n); } setDraft(null); }}
        onFocus={() => setDraft(String(value))}
        className={cls ?? 'bg-gray-900 border border-gray-700 rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-blue-500'}
      />
      {err && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">
          {err}
        </div>
      )}
    </div>
  );
}

/** Nullable numeric input for matrix level cells */
function MxNull({ value, onChange, err }: {
  value: number | null;
  onChange: (v: number | null) => void;
  err?: string | null;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const display = draft !== null ? draft : (value === null ? '' : String(value));
  return (
    <div className="relative group">
      <input
        type="text" inputMode="decimal" placeholder="—"
        value={display}
        onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); else if (e.target.value === '' || e.target.value === '-') onChange(null); }}
        onBlur={() => { if (draft !== null) { if (draft === '' || draft === '-') onChange(null); else { const n = parseFloat(draft); onChange(isNaN(n) ? null : n); } } setDraft(null); }}
        onFocus={() => setDraft(value === null ? '' : String(value))}
        className={`bg-gray-900 border rounded py-1 px-1 text-[11px] text-center w-full outline-none focus:border-blue-500 ${err ? 'border-red-500 text-gray-100' : value === null && draft === null ? 'border-dashed border-gray-700 text-gray-500' : 'border-gray-700 text-gray-100'}`}
      />
      {err && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">
          {err}
        </div>
      )}
    </div>
  );
}

function Field({ label, children, required, hint }: { label: string; children: React.ReactNode; required?: boolean; hint?: string }) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline gap-2">
        <label className="block text-[11px] font-bold uppercase tracking-wider text-slate-400">
          {label}{required && <span className="ml-0.5 text-[#5b8cff]">*</span>}
        </label>
        {hint && <span className="text-[10px] text-slate-600">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

const inputCls =
  'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50';

const labelCls = 'flex items-center text-xs text-gray-500 mb-1';

const SIG_LABELS: Record<string, string> = {
  'rsi-os':    'RSI Oversold',
  'macd-x':    'MACD Crossover',
  'gc':        'Golden Cross',
  'bb-sq':     'BB Squeeze',
  'stoch-x':   'Stochastic Cross',
  'vol-spike': 'Volume Spike',
  'breakout':  'Range Breakout',
  'ema-x':     'EMA Crossover',
  'div':       'RSI Divergence',
  'st-flip':   'SuperTrend Flip',
};

function sigLabel(name: string): string {
  return SIG_LABELS[name] ?? name;
}

const SIG_SORT_HINT: Record<string, string> = {
  'st-flip':   'сначала с наибольшим остатком TTL',
  'vol-spike': 'сначала с наибольшим всплеском объёма',
};
