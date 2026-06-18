import { useState, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import { X, Shield, ToggleLeft, ToggleRight, Camera, Trash2, Smile } from 'lucide-react';
import { BotIconPicker } from './BotIconPicker';
import { Toggle, Tip, SignalPickerField } from '../../../components/strategies/FormWidgets';
import { getStrategyDefaults } from '../../admin-defaults/api';
import { CoinMultiPicker } from '../../../components/common/CoinMultiPicker';
import { getInstrumentConstraints, type InstrumentConstraints } from '../../../api/strategies';
import { apiClient } from '../../../api/client';
import { useSelectedAccount } from '../../../contexts/AccountContext';
import type { Bot as BotType, CreateBotInput, StrategyConfig, MatrixLevel, MatrixEntryLevel } from '../types';
import { BOT_KIND_META } from '../botKindMeta';
import { ResetStatsConfirmModal } from './ResetStatsConfirmModal';
import type { SignalConfig } from '../../../types';

// ─── Types ─────────────────────────────────────────────────────────────────────

type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => Promise<void> | void;
  onClose: () => void;
  mode?: 'user' | 'admin';
};

type OuterTab = 'basic' | 'activation' | 'strategy';
type StratTab = 'entry' | 'matrix' | 'params';

// Hedge activation/deactivation config
type HedgeActivation = {
  act_type:          number;          // 0=last_order%, 1=drawdown%, 2=pnl$, 3=roi%
  act_value:         number;
  force_activation:  boolean;         // bypass act_type/act_value — open hedge immediately
  close_type:        number;          // 0=at_cycle_end, 1=max_loss$
  close_value:       number;
  deact_close_type:  number;          // 0=pnl$, 1=roi%, 2=breakeven
  deact_close_value: number;
  profit_lazy:       boolean;
  profit_lazy_pct:   number;
  deact_type:        number;          // 0=drawdown%, 1=pnl$, 2=roi%, 3=last_order%, 4=wait_pair
  deact_value:       number;
};

// Minimal bot descriptor for the bot filter picker.
type BotOption = { id: string; name: string; avatarUrl?: string };

// ─── Constants ─────────────────────────────────────────────────────────────────

const ACT_TYPES  = ['От последнего ордера Main', 'Просадка Main-позиции', 'PNL Main-позиции', 'ROI Main-позиции'];
const ACT_UNITS  = ['%', '%', '$', '%'];

const CLOSE_TYPES = ['По завершению цикла', 'Принять убыток не более, $', 'Приостановить и восстановить'];

const DEACT_CLOSE_TYPES = ['PNL (общий), USDT', 'ROI (общий), %', 'В безубыток'];

const DEACT_TYPES = [
  'Просадка Main-позиции',
  'PNL Main-позиции',
  'ROI Main-позиции',
  'От последнего ордера Main-позиции',
  'Ожидать парного закрытия',
];
const DEACT_UNITS = ['%', '$', '%', '%', ''];

// ─── Defaults ──────────────────────────────────────────────────────────────────

function defaultHedgeAct(s?: StrategyConfig): HedgeActivation {
  return {
    act_type:          s?.hedge_act_type          ?? 1,
    act_value:         s?.hedge_act_value          ?? -4,
    force_activation:  s?.hedge_force_activation   ?? false,
    close_type:        s?.hedge_close_type         ?? 0,
    close_value:       s?.hedge_close_value        ?? 10,
    deact_close_type:  s?.hedge_deact_close_type   ?? 0,
    deact_close_value: s?.hedge_deact_close_value  ?? 50,
    profit_lazy:       s?.hedge_profit_lazy        ?? false,
    profit_lazy_pct:   s?.hedge_profit_lazy_pct    ?? 2,
    deact_type:        s?.hedge_deact_type         ?? 0,
    deact_value:       s?.hedge_deact_value        ?? 3,
  };
}

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
    bot_kind:                'hedge',
    symbol:                  s.symbol                  ?? 'BTCUSDT',
    category:                s.category                ?? 'linear',
    direction:               s.direction               ?? 'both',
    strategy_type:           'matrix',
    entry_order_type:        s.entry_order_type        ?? 'limit',
    leverage:                s.leverage                ?? 5,
    margin_type:             s.margin_type             ?? 'isolated',
    hedge_mode:              s.hedge_mode              ?? true,
    grid_size_usdt:          s.grid_size_usdt          ?? 100,
    grid_active:             s.grid_active             ?? 0,
    max_stop_active:         s.max_stop_active         ?? 0,
    signal_configs:          (s.signal_configs          as SignalConfig[]) ?? [],
    activation_signals:      (s.activation_signals      as SignalConfig[]) ?? [],
    after_stop_mode:         s.after_stop_mode         ?? 'restart',
    max_cycles:              s.max_cycles              ?? 0,
    // Matrix
    matrix_levels:           s.matrix_levels           ?? DEFAULT_MATRIX_LEVELS,
    matrix_entry_level:      s.matrix_entry_level      ?? DEFAULT_MATRIX_ENTRY,
    safe_zone_pct:           s.safe_zone_pct           ?? 1.5,
    protected_build:         s.protected_build         ?? false,
    matrix_rebuild_on_sl:    s.matrix_rebuild_on_sl    ?? false,
    matrix_rebuild_from_entry: s.matrix_rebuild_from_entry ?? false,
    size_as_main:            s.size_as_main            ?? false,
  };
}

// ─── Image compression ─────────────────────────────────────────────────────────

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

// ─── HedgeBotForm ──────────────────────────────────────────────────────────────

export function HedgeBotForm({ bot, onSubmit, onClose, mode = 'user' }: Props) {
  const [outerTab, setOuterTab] = useState<OuterTab>('basic');
  const [stratTab, setStratTab] = useState<StratTab>('entry');

  // Basic fields
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

  // Hedge activation config
  const [ha, setHa] = useState<HedgeActivation>(defaultHedgeAct(bot?.strategyConfig));
  const patchHa = (p: Partial<HedgeActivation>) => setHa(prev => ({ ...prev, ...p }));

  // Strategy config
  const [config, setConfig] = useState<StrategyConfig>(defaultStratConfig(bot));
  const patch = (p: Partial<StrategyConfig>) => setConfig(c => ({ ...c, ...p }));

  const [whitelist,     setWhitelist]     = useState<string[]>(bot?.symbolWhitelist ?? []);
  const [blacklist,     setBlacklist]     = useState<string[]>(bot?.symbolBlacklist ?? []);

  // Bot whitelist / blacklist (by bot ID)
  const [botWhitelist,    setBotWhitelist]    = useState<string[]>(
    (bot?.strategyConfig?.hedge_bot_whitelist ?? []) as string[],
  );
  const [botBlacklist,    setBotBlacklist]    = useState<string[]>(
    (bot?.strategyConfig?.hedge_bot_blacklist ?? []) as string[],
  );
  const [availableBots,   setAvailableBots]   = useState<BotOption[]>([]);

  const [actValDraft,        setActValDraft]        = useState<string | null>(null);
  const [closeValDraft,      setCloseValDraft]      = useState<string | null>(null);
  const [deactCloseValDraft, setDeactCloseValDraft] = useState<string | null>(null);
  const [deactValDraft,      setDeactValDraft]      = useState<string | null>(null);

  const [sizeAsMain,   setSizeAsMain]   = useState<boolean>(bot?.strategyConfig?.size_as_main ?? false);
  const [leverageMax,  setLeverageMax]  = useState(false);

  const [submitting,            setSubmitting]            = useState(false);
  const [submitError,           setSubmitError]           = useState<string | null>(null);
  const [showResetStatsConfirm, setShowResetStatsConfirm] = useState(false);
  const [instrInfo,   setInstrInfo]   = useState<InstrumentConstraints | null>(null);
  const [minLotEnabled, setMinLotEnabled] = useState(false);

  const { selectedAccountId } = useSelectedAccount();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [showIconPicker, setShowIconPicker] = useState(false);

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  useEffect(() => {
    if (outerTab === 'activation') {
      if (availableBots.length === 0) {
        apiClient
          .get<{ catalog: Record<string, unknown>[]; mine: Record<string, unknown>[] }>('/bots')
          .then(res => {
            const toOption = (b: Record<string, unknown>): BotOption => ({
              id:        b.id        as string,
              name:      b.name      as string,
              avatarUrl: (b.avatarUrl as string) || undefined,
            });
            // Include both mine and catalog so that whitelist tags can resolve
            // bot names even when the bot belongs to a different user.
            const seen = new Set<string>();
            const list: BotOption[] = [];
            for (const b of [...res.data.mine, ...res.data.catalog].map(toOption)) {
              if (b.id === bot?.id || seen.has(b.id)) continue;
              seen.add(b.id);
              list.push(b);
            }
            setAvailableBots(list);
          })
          .catch(() => {});
      }
    }
  }, [outerTab]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    getInstrumentConstraints('BTCUSDT', config.category ?? 'linear')
      .then(setInstrInfo).catch(() => setInstrInfo(null));
  }, [config.category]); // eslint-disable-line react-hooks/exhaustive-deps

  // Apply admin-configured defaults for new bots
  useEffect(() => {
    if (bot) return
    getStrategyDefaults().then(defaults => {
      const d = defaults.bot_hedge
      if (!d) return
      setConfig(c => {
        const o: Partial<typeof c> = {}
        if (d.leverage !== undefined)      o.leverage      = d.leverage
        if (d.grid_size_usdt !== undefined) o.grid_size_usdt = d.grid_size_usdt
        if (d.entry_order_type !== undefined) o.entry_order_type = d.entry_order_type
        if (d.margin_type !== undefined)   o.margin_type   = d.margin_type
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
        return { ...c, ...o }
      })
      if (d.hedge_act_type !== undefined || d.hedge_act_value !== undefined ||
          d.hedge_force_activation !== undefined || d.hedge_close_type !== undefined ||
          d.hedge_deact_close_type !== undefined || d.hedge_deact_type !== undefined) {
        setHa(prev => ({
          ...prev,
          ...(d.hedge_act_type !== undefined ? { act_type: d.hedge_act_type } : {}),
          ...(d.hedge_act_value !== undefined ? { act_value: d.hedge_act_value } : {}),
          ...(d.hedge_force_activation !== undefined ? { force_activation: d.hedge_force_activation } : {}),
          ...(d.hedge_close_type !== undefined ? { close_type: d.hedge_close_type } : {}),
          ...(d.hedge_close_value !== undefined ? { close_value: d.hedge_close_value } : {}),
          ...(d.hedge_deact_close_type !== undefined ? { deact_close_type: d.hedge_deact_close_type } : {}),
          ...(d.hedge_deact_close_value !== undefined ? { deact_close_value: d.hedge_deact_close_value } : {}),
          ...(d.hedge_profit_lazy !== undefined ? { profit_lazy: d.hedge_profit_lazy } : {}),
          ...(d.hedge_profit_lazy_pct !== undefined ? { profit_lazy_pct: d.hedge_profit_lazy_pct } : {}),
          ...(d.hedge_deact_type !== undefined ? { deact_type: d.hedge_deact_type } : {}),
          ...(d.hedge_deact_value !== undefined ? { deact_value: d.hedge_deact_value } : {}),
        }))
      }
    }).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (minLotEnabled && instrInfo && instrInfo.min_order_usdt > 0) {
      patch({ grid_size_usdt: instrInfo.min_order_usdt });
    }
  }, [minLotEnabled, instrInfo]); // eslint-disable-line react-hooks/exhaustive-deps

  async function handleAvatarFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    try { setAvatarUrl(await compressImage(file)); } catch {}
    e.target.value = '';
  }

  // ── Matrix helpers ───────────────────────────────────────────────────────────
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

  // Core submit — runs after all confirmations are resolved.
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
          bot_kind:      'hedge',
          strategy_type: 'matrix',
          grid_levels:   totalMatrixLevels,
          grid_step_pct: belowLevels[0]?.price_step_pct ?? aboveLevels[0]?.price_step_pct ?? 0,
          signal_filter: false,
          hedge_act_type:          ha.act_type,
          hedge_act_value:         ha.act_value,
          hedge_force_activation:  ha.force_activation,
          hedge_close_type:        ha.close_type,
          hedge_close_value:       ha.close_value,
          hedge_deact_close_type:  ha.deact_close_type,
          hedge_deact_close_value: ha.deact_close_value,
          hedge_profit_lazy:       ha.profit_lazy,
          hedge_profit_lazy_pct:   ha.profit_lazy_pct,
          hedge_deact_type:        ha.deact_type,
          hedge_deact_value:       ha.deact_value,
          hedge_bot_whitelist:     botWhitelist,
          hedge_bot_blacklist:     botBlacklist,
          size_as_main:            sizeAsMain,
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

    // Warn about stats reset when editing a bot that already has trade history.
    if (bot && (bot.tradesTotal ?? 0) > 0) {
      setShowResetStatsConfirm(true);
      return;
    }
    await doSubmit();
  };

  const outerTabs: { id: OuterTab; label: string }[] = [
    { id: 'basic',      label: 'Основное'  },
    { id: 'activation', label: 'Активация' },
    { id: 'strategy',   label: 'Стратегия' },
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
          <div className="h-9 w-9 shrink-0 overflow-hidden rounded-[9px] border border-amber-500/30">
            {avatarUrl
              ? <img src={avatarUrl} alt="" className="h-full w-full object-cover" />
              : <div className="flex h-full w-full items-center justify-center bg-amber-900/[.20] text-amber-500"><Shield size={16} strokeWidth={2} /></div>
            }
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              {bot ? 'Редактировать HedgeBot' : 'Создать HedgeBot'}
            </h2>
            <p className="text-[11px] text-slate-400">{bot ? bot.name : 'Новый хедж-бот'}</p>
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
                <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full bg-amber-500" />
              )}
            </button>
          ))}
        </div>

        {/* ── body ── */}
        <div className="flex-1 overflow-auto p-5">

          {/* ═══════════════════════════ BASIC ═══════════════════════════════ */}
          {outerTab === 'basic' && (
            <div className="flex flex-col gap-4">

              {/* Avatar */}
              <div className="flex items-center gap-4">
                <div className="relative shrink-0 group">
                  <div className="h-[72px] w-[72px] overflow-hidden rounded-[14px] border border-white/[.08] bg-black/[.3]">
                    {avatarUrl
                      ? <img src={avatarUrl} alt="avatar" className="h-full w-full object-cover" />
                      : <div className="flex h-full w-full items-center justify-center text-slate-600">
                          <Shield size={26} strokeWidth={1.5} />
                        </div>
                    }
                  </div>
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
                      placeholder="My HedgeBot"
                      className={inputCls}
                      maxLength={30}
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
                        onChange={url => setAvatarUrl(url)}
                        onClose={() => setShowIconPicker(false)}
                      />
                    )}
                  </div>
                </div>
              </div>

              <Field label="Краткое описание" hint="Показывается на карточке">
                <div className="relative">
                  <textarea
                    value={description}
                    onChange={e => setDescription(e.target.value.slice(0, 120))}
                    placeholder="Хеджирование Main-позиции при просадке"
                    rows={2}
                    className={`${inputCls} resize-none`}
                    maxLength={120}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">
                    {description.length}/120
                  </span>
                </div>
              </Field>

              <Field label="Подробное описание" hint="Полное описание бота">
                <div className="relative">
                  <textarea
                    value={fullDescription}
                    onChange={e => setFullDescription(e.target.value.slice(0, 2000))}
                    placeholder="Опишите логику и условия активации хеджа..."
                    rows={4}
                    className={`${inputCls} resize-none`}
                    maxLength={2000}
                  />
                  <span className="pointer-events-none absolute right-3 bottom-2 text-[10px] text-slate-600">
                    {fullDescription.length}/2000
                  </span>
                </div>
              </Field>

              {mode !== 'admin' && !bot?.sourceBotId && (
                <div className="flex items-center justify-between rounded-lg border border-white/[.06] bg-white/[.02] px-4 py-3">
                  <div>
                    <div className="text-[13px] font-semibold text-slate-200">Публичный бот</div>
                    <div className="mt-0.5 text-[11px] text-slate-400">Другие пользователи смогут подписаться</div>
                  </div>
                  <button type="button" onClick={() => setIsPublic(v => !v)} className="text-slate-400">
                    {isPublic
                      ? <ToggleRight size={28} className="text-amber-400" />
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
                      Хедж открывается автоматически при выполнении условий активации
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
                    <div className="text-[12px] font-medium text-slate-300">Удалять стратегию после завершения</div>
                    <div className="text-[11px] text-slate-500">
                      {config.after_stop_mode === 'delete'
                        ? 'Хедж удаляется — можно открыть новый при следующей активации'
                        : 'Хедж перезапускается автоматически'}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => patch({ after_stop_mode: config.after_stop_mode === 'delete' ? 'restart' : 'delete' })}
                    className="ml-4 shrink-0 text-slate-400"
                  >
                    {config.after_stop_mode === 'delete'
                      ? <ToggleRight size={26} className="text-amber-400" />
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
                    <input type="number" min={0} step={1} value={maxStrategies}
                      onChange={e => setMaxStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={inputCls} />
                  </Field>
                  <Field label="Макс. лонг" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxLongStrategies}
                      onChange={e => setMaxLongStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={`${inputCls} border-emerald-900/40`} />
                  </Field>
                  <Field label="Макс. шорт" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxShortStrategies}
                      onChange={e => setMaxShortStrategies(Math.max(0, parseInt(e.target.value) || 0))}
                      className={`${inputCls} border-rose-900/40`} />
                  </Field>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="Лимит маржи, USDT" hint="0 — без ограничений">
                    <input type="number" min={0} step={10} value={maxMarginUsdt}
                      onChange={e => setMaxMarginUsdt(Math.max(0, parseFloat(e.target.value) || 0))}
                      className={inputCls} />
                  </Field>
                  <Field label="Повторов подряд" hint="0 = ∞">
                    <input type="number" min={0} step={1} value={maxSymConsecutiveRuns}
                      onChange={e => setMaxSymConsecutiveRuns(Math.max(0, parseInt(e.target.value) || 0))}
                      className={inputCls} />
                  </Field>
                </div>
              </div>

            </div>
          )}

          {/* ═══════════════════════════ ACTIVATION ══════════════════════════ */}
          {outerTab === 'activation' && (
            <div className="flex flex-col gap-5">

              {/* Фильтр по монетам */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <div className="mb-1.5 flex items-center gap-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                    Монеты Whitelist
                    <Tip text="Хедж-бот мониторит только позиции по этим символам. Пусто — без ограничений по монетам." />
                  </div>
                  <CoinMultiPicker
                    values={whitelist}
                    onChange={setWhitelist}
                    color="blue"
                    placeholder="Выбрать монеты для whitelist..."
                  />
                </div>
                <div>
                  <div className="mb-1.5 flex items-center gap-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                    Монеты Blacklist
                    <Tip text="Позиции по этим символам исключены из мониторинга хедж-бота." />
                  </div>
                  <CoinMultiPicker
                    values={blacklist}
                    onChange={setBlacklist}
                    color="red"
                    placeholder="Выбрать монеты для blacklist..."
                  />
                </div>
              </div>

              {/* ── Фильтр ботов ── */}
              <SectionHeader icon="🤖" color="green">Фильтр по ботам</SectionHeader>

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <div className="mb-1.5 flex items-center gap-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                    Боты Whitelist
                    <Tip text="Хеджировать только позиции от стратегий этих ботов. Пусто — мониторятся позиции любого бота. Ручные позиции (без бота) проходят, если whitelist пуст." />
                  </div>
                  <BotMultiPicker
                    allBots={availableBots}
                    selected={botWhitelist}
                    onChange={setBotWhitelist}
                    color="blue"
                    placeholder="Добавить бот в whitelist..."
                  />
                </div>

                <div>
                  <div className="mb-1.5 flex items-center gap-1 text-[11px] font-bold uppercase tracking-wider text-slate-400">
                    Боты Blacklist
                    <Tip text="Позиции от стратегий этих ботов исключены из мониторинга хеджа. Blacklist имеет приоритет над whitelist." />
                  </div>
                  <BotMultiPicker
                    allBots={availableBots}
                    selected={botBlacklist}
                    onChange={setBotBlacklist}
                    color="red"
                    placeholder="Добавить бот в blacklist..."
                  />
                </div>
              </div>

              {/* ── Направление хеджа ── */}
              <div>
                <label className={labelCls}>
                  Направление хеджа
                  <Tip text="Какую позицию хеджировать. Long — хеджируем только лонги (открываем шорт). Short — только шорты (открываем лонг). Both — любую сторону." />
                </label>
                <Toggle
                  options={[
                    { label: 'Long',  value: 'long'  },
                    { label: 'Short', value: 'short' },
                    { label: 'Both',  value: 'both'  },
                  ]}
                  value={config.direction ?? 'both'}
                  onChange={v => patch({ direction: v as StrategyConfig['direction'] })}
                  optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white' }}
                />
              </div>

              {/* ── Секция: Активация ── */}
              <SectionHeader icon="▶" color="amber">Активация хеджа</SectionHeader>

              {/* Принудительная активация */}
              <div className="rounded-lg border border-amber-500/20 bg-amber-950/[.12] p-3 space-y-2">
                <div className="flex items-center gap-2.5">
                  <ToggleSwitch
                    enabled={ha.force_activation}
                    onToggle={() => patchHa({ force_activation: !ha.force_activation })}
                  />
                  <span className="flex items-center gap-1 text-[12px] font-semibold text-amber-300">
                    Принудительная активация
                    <Tip text="Хедж открывается немедленно для каждой подходящей позиции — условие активации ниже игнорируется. Фильтры монет, ботов и направления по-прежнему применяются." />
                  </span>
                </div>
                {ha.force_activation && (
                  <p className="pl-11 text-[11px] leading-relaxed text-amber-400/80">
                    Хедж будет открыт сразу при обнаружении позиции. Порог активации ниже не учитывается.
                  </p>
                )}
              </div>

              {/* Условие активации + Закрыть Safe — одна строка */}
              <div className={`grid grid-cols-2 gap-3${ha.force_activation ? ' opacity-40 pointer-events-none select-none' : ''}`}>
                <div>
                  <label className={labelCls}>
                    Активировать при
                    <Tip text="Условие на Main-позиции, при котором открывается хедж-позиция." />
                  </label>
                  <div className="flex items-stretch gap-1.5">
                    <div className="w-[70%]">
                      <EnumPicker
                        options={ACT_TYPES}
                        value={ha.act_type}
                        onChange={v => patchHa({ act_type: v })}
                      />
                    </div>
                    <div className="flex w-[30%] items-center gap-1 min-w-0">
                      {ACT_UNITS[ha.act_type] && (
                        <span className="text-[10px] text-slate-500 shrink-0">{ACT_UNITS[ha.act_type]}</span>
                      )}
                      <input
                        type="text"
                        inputMode="decimal"
                        value={actValDraft !== null ? actValDraft : String(ha.act_value)}
                        onChange={e => {
                          const raw = e.target.value;
                          setActValDraft(raw);
                          if (/^-?\d*\.?\d*$/.test(raw) && raw !== '' && raw !== '-') {
                            const v = parseFloat(raw);
                            if (!isNaN(v)) patchHa({ act_value: v });
                          }
                        }}
                        onBlur={() => {
                          if (actValDraft !== null) {
                            const v = parseFloat(actValDraft);
                            patchHa({ act_value: isNaN(v) ? ha.act_value : v });
                            setActValDraft(null);
                          }
                        }}
                        className={`${inputCls} flex-1 min-w-0 text-center`}
                      />
                    </div>
                  </div>
                  <p className="mt-1 text-[10px] text-slate-500 leading-tight">
                    {ha.act_type === 0 && (ha.act_value < 0 ? <>
                      Цена ушла ПРОТИВ позиции от последнего ордера.<br />
                      Long: цена упала на {Math.abs(ha.act_value)}% ниже последнего ордера.<br />
                      Short: цена выросла на {Math.abs(ha.act_value)}% выше последнего ордера.
                    </> : <>
                      Цена ушла В ПОЛЬЗУ позиции от последнего ордера.<br />
                      Long: цена выросла на {ha.act_value}% выше последнего ордера.<br />
                      Short: цена упала на {ha.act_value}% ниже последнего ордера.
                    </>)}
                    {ha.act_type === 1 && (ha.act_value < 0 ? <>
                      Цена ушла ПРОТИВ позиции от ТВХ (просадка).<br />
                      Long: цена упала на {Math.abs(ha.act_value)}% ниже ТВХ.<br />
                      Short: цена выросла на {Math.abs(ha.act_value)}% выше ТВХ.
                    </> : <>
                      Цена ушла В ПОЛЬЗУ позиции от ТВХ (буфер прибыли).<br />
                      Long: цена выросла на {ha.act_value}% выше ТВХ.<br />
                      Short: цена упала на {ha.act_value}% ниже ТВХ.
                    </>)}
                    {ha.act_type === 2 && (ha.act_value < 0 ? <>
                      Хедж активируется при нереализованном убытке ≥ {Math.abs(ha.act_value)} USDT.<br />
                      Пример: −50 = убыток от 50 USDT.
                    </> : <>
                      Хедж активируется при нереализованной прибыли ≥ {ha.act_value} USDT.<br />
                      Пример: +50 = прибыль от 50 USDT.
                    </>)}
                    {ha.act_type === 3 && (ha.act_value < 0 ? <>
                      Хедж активируется при ROI позиции ≤ −{Math.abs(ha.act_value)}%.<br />
                      ROI = PnL / начальная маржа × 100. Пример: −10 = потеряно 10% маржи.
                    </> : <>
                      Хедж активируется при ROI позиции ≥ +{ha.act_value}%.<br />
                      ROI = PnL / начальная маржа × 100. Пример: +10 = заработано 10% маржи.
                    </>)}
                  </p>
                </div>

                <div>
                  <label className={labelCls}>
                    Если занят слот
                    <Tip text="Что делать, если в направлении хеджа уже открыта стратегия. «По завершению цикла» — ждём TP/SL, затем открываем хедж. «Принять убыток» — принудительно останавливаем при достижении заданного убытка и сразу открываем хедж. «Приостановить и восстановить» — отменяем все ордера конфликтующей стратегии, перехватываем управление, а после деактивации хеджа возобновляем её с нового цикла." />
                  </label>
                  <div className="flex items-stretch gap-1.5">
                    <div className="w-[70%]">
                      <EnumPicker
                        options={CLOSE_TYPES}
                        value={ha.close_type}
                        onChange={v => patchHa({ close_type: v })}
                      />
                    </div>
                    {ha.close_type === 1 && (
                      <div className="flex w-[30%] items-center gap-1 min-w-0">
                        <span className="text-[10px] text-slate-500 shrink-0">$</span>
                        <input
                          type="text"
                          inputMode="decimal"
                          value={closeValDraft !== null ? closeValDraft : String(ha.close_value)}
                          onChange={e => {
                            const raw = e.target.value;
                            setCloseValDraft(raw);
                            if (/^-?\d*\.?\d*$/.test(raw) && raw !== '' && raw !== '-') {
                              const v = parseFloat(raw);
                              if (!isNaN(v)) patchHa({ close_value: v });
                            }
                          }}
                          onBlur={() => {
                            if (closeValDraft !== null) {
                              const v = parseFloat(closeValDraft);
                              patchHa({ close_value: isNaN(v) ? ha.close_value : v });
                              setCloseValDraft(null);
                            }
                          }}
                          className={`${inputCls} flex-1 min-w-0 text-center`}
                        />
                      </div>
                    )}
                  </div>
                  <p className="mt-1 text-[10px] text-slate-500 leading-tight">
                    {ha.close_type === 0 && <>
                      Бот ждёт естественного завершения цикла конкурирующей стратегии (TP или SL),
                      и только после этого открывает хедж.
                    </>}
                    {ha.close_type === 1 && <>
                      Если убыток конкурирующей стратегии достигает {ha.close_value} USDT —
                      бот принудительно её останавливает и немедленно открывает хедж.
                    </>}
                    {ha.close_type === 2 && <>
                      Бот отменяет все ордера конкурирующей стратегии и перехватывает управление.
                      После деактивации хеджа стратегия возобновляется с нового цикла.
                    </>}
                  </p>
                </div>
              </div>

              {/* Сигналы активации */}
              <div>
                <div className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-slate-400 pb-1 border-b border-white/[.05] mb-2">
                  Сигналы активации
                  <Tip text="AND-логика: все указанные сигналы должны совпасть, чтобы хедж был активирован." />
                </div>
                <SignalPickerField
                  configs={(config.activation_signals as SignalConfig[]) ?? []}
                  onChange={v => patch({ activation_signals: v })}
                />
              </div>

              {/* ── Секция: Деактивация ── */}
              <SectionHeader icon="◀" color="blue">Деактивация хеджа</SectionHeader>

              {/* Деактивация — два пикера в одну строку */}
              <div className="grid grid-cols-2 gap-3">

                {/* Закрыть обе позиции */}
                <div>
                  <label className={labelCls}>
                    Закрыть обе позиции при
                    <Tip text="Суммарный результат (Main + хедж), при котором обе позиции закрываются. «В безубыток» — закрытие когда совокупный PnL с учётом комиссий биржи ≥ 0." />
                  </label>
                  <div className="flex items-stretch gap-1.5">
                    <div className="w-[70%]">
                      <EnumPicker
                        options={DEACT_CLOSE_TYPES}
                        value={ha.deact_close_type}
                        onChange={v => patchHa({ deact_close_type: v })}
                      />
                    </div>
                    {ha.deact_close_type !== 2 && (
                      <div className="flex w-[30%] items-center gap-1 min-w-0">
                        <span className="text-[10px] text-slate-500 shrink-0">
                          {ha.deact_close_type === 0 ? '$' : '%'}
                        </span>
                        <input
                          type="text"
                          inputMode="decimal"
                          value={deactCloseValDraft !== null ? deactCloseValDraft : String(ha.deact_close_value)}
                          onChange={e => {
                            const raw = e.target.value;
                            setDeactCloseValDraft(raw);
                            if (/^-?\d*\.?\d*$/.test(raw) && raw !== '' && raw !== '-') {
                              const v = parseFloat(raw);
                              if (!isNaN(v)) patchHa({ deact_close_value: v });
                            }
                          }}
                          onBlur={() => {
                            if (deactCloseValDraft !== null) {
                              const v = parseFloat(deactCloseValDraft);
                              patchHa({ deact_close_value: isNaN(v) ? ha.deact_close_value : v });
                              setDeactCloseValDraft(null);
                            }
                          }}
                          className={`${inputCls} flex-1 min-w-0 text-center`}
                        />
                      </div>
                    )}
                  </div>
                </div>

                {/* Деактивировать хедж */}
                <div>
                  <label className={labelCls}>
                    Деактивировать хедж при
                    <Tip text="Условие восстановления Main-позиции, при котором хедж закрывается без закрытия Main." />
                  </label>
                  <div className="flex items-stretch gap-1.5">
                    <div className="w-[70%]">
                      <EnumPicker
                        options={DEACT_TYPES}
                        value={ha.deact_type}
                        onChange={v => patchHa({ deact_type: v })}
                      />
                    </div>
                    {DEACT_UNITS[ha.deact_type] && (
                      <div className="flex w-[30%] items-center gap-1 min-w-0">
                        <span className="text-[10px] text-slate-500 shrink-0">{DEACT_UNITS[ha.deact_type]}</span>
                        <input
                          type="text"
                          inputMode="decimal"
                          value={deactValDraft !== null ? deactValDraft : String(ha.deact_value)}
                          onChange={e => {
                            const raw = e.target.value;
                            setDeactValDraft(raw);
                            if (/^-?\d*\.?\d*$/.test(raw) && raw !== '' && raw !== '-') {
                              const v = parseFloat(raw);
                              if (!isNaN(v)) patchHa({ deact_value: v });
                            }
                          }}
                          onBlur={() => {
                            if (deactValDraft !== null) {
                              const v = parseFloat(deactValDraft);
                              patchHa({ deact_value: isNaN(v) ? ha.deact_value : v });
                              setDeactValDraft(null);
                            }
                          }}
                          className={`${inputCls} flex-1 min-w-0 text-center`}
                        />
                      </div>
                    )}
                  </div>
                </div>

              </div>

              {/* Отложенный профит */}
              <div className="rounded-lg border border-white/[.06] bg-white/[.02] p-3 space-y-3">
                <div className="flex items-center gap-2.5">
                  <ToggleSwitch
                    enabled={ha.profit_lazy}
                    onToggle={() => patchHa({ profit_lazy: !ha.profit_lazy })}
                  />
                  <span className="flex items-center gap-1 text-[12px] font-semibold text-slate-300">
                    Отложенный профит
                    <Tip text="Закрыть только хедж при достижении заданного % прибыли. Main-позиция продолжает работать." />
                  </span>
                </div>
                {ha.profit_lazy && (
                  <div className="pl-11">
                    <Field label="Шаг трейлинга, %">
                      <input
                        type="number" step="0.1" min="0.1"
                        value={ha.profit_lazy_pct}
                        onChange={e => patchHa({ profit_lazy_pct: Number(e.target.value) })}
                        className={`${inputCls} w-28`}
                      />
                    </Field>
                  </div>
                )}
              </div>

            </div>
          )}

          {/* ═══════════════════════════ STRATEGY ════════════════════════════ */}
          {outerTab === 'strategy' && (
            <div className="flex flex-col gap-4">

              {bot && (
                <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/[.08] px-3 py-2.5 text-[11.5px] text-amber-300 leading-relaxed">
                  <span className="mt-px shrink-0 text-amber-400">⚠</span>
                  <span>Изменения применятся только к <strong className="text-amber-200">новым хедж-стратегиям</strong>. Уже запущенные затронуты не будут.</span>
                </div>
              )}

              <div className="flex items-start gap-2.5 rounded-lg border border-[#5b8cff]/20 bg-[#5b8cff]/[.07] px-3 py-2.5">
                <span className="mt-0.5 shrink-0 text-[14px] text-[#5b8cff]">⚡</span>
                <p className="text-[11px] leading-relaxed text-slate-400">
                  Настройки <span className="font-semibold text-slate-300">хедж-позиции</span> — как бот будет торговать после активации. Условия активации задаются на вкладке{' '}
                  <button type="button" onClick={() => setOuterTab('activation')}
                    className="font-semibold text-[#a0b8ff] underline underline-offset-2 hover:text-[#c4d4ff]">
                    Активация
                  </button>.
                </p>
              </div>

              {/* Strategy type selector */}
              <div className="flex items-center gap-3">
                <span className="text-[11px] font-bold uppercase tracking-wider text-slate-400">Тип стратегии</span>
                <div className="flex gap-1">
                  <button
                    type="button"
                    className="rounded-lg border px-4 py-1.5 text-[12px] font-semibold border-amber-500/40 bg-amber-500/[.18] text-amber-300"
                  >
                    Matrix
                  </button>
                  <div
                    title="В разработке"
                    className="relative rounded-lg border border-white/[.07] bg-white/[.02] px-4 py-1.5 text-[12px] font-semibold text-slate-600 cursor-not-allowed opacity-50"
                  >
                    ALS
                    <span className="ml-1.5 text-[9px] font-bold uppercase tracking-wide text-slate-500">скоро</span>
                  </div>
                </div>
              </div>

              {/* Strategy sub-tabs */}
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

              {/* 1. Базовые */}
              {stratTab === 'entry' && (
                <div className="flex flex-col gap-3">
                  <div>
                    <label className={labelCls}>
                      Направление хеджа
                      <Tip text="Both — хедж открывает позицию в любом направлении. Short — только шорт при просадке лонга. Long — наоборот." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Long',  value: 'long'  },
                        { label: 'Short', value: 'short' },
                        { label: 'Both',  value: 'both'  },
                      ]}
                      value={config.direction ?? 'both'}
                      onChange={v => patch({ direction: v as StrategyConfig['direction'] })}
                      optionColors={{ long: 'bg-emerald-700 text-white', short: 'bg-rose-700 text-white' }}
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Тип входного ордера
                      <Tip text="Limit — мейкер (дешевле). Stop Market — гарантирует исполнение." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Limit',       value: 'limit'       },
                        { label: 'Stop Market', value: 'stop_market' },
                      ]}
                      value={config.entry_order_type ?? 'limit'}
                      onChange={v => patch({ entry_order_type: v as StrategyConfig['entry_order_type'] })}
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Тип маржи
                      <Tip text="Isolated — риск ограничен суммой хеджа. Cross — задействована вся маржа." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Isolated', value: 'isolated' },
                        { label: 'Cross',    value: 'cross'    },
                      ]}
                      value={config.margin_type ?? 'isolated'}
                      onChange={v => patch({ margin_type: v as StrategyConfig['margin_type'] })}
                    />
                  </div>

                  <div>
                    <label className={labelCls}>
                      Хедж режим биржи
                      <Tip text="Лонг и шорт в отдельных слотах — обязательно для хеджа." />
                    </label>
                    <Toggle
                      options={[
                        { label: 'Нет',        value: 'false' },
                        { label: 'Да (Hedge)', value: 'true'  },
                      ]}
                      value={String(config.hedge_mode ?? true)}
                      onChange={v => patch({ hedge_mode: v === 'true' })}
                    />
                  </div>

                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className={labelCls}>
                        Активных слотов (0 = все)
                        <Tip text="Скользящее окно: сколько лимитных ордеров матрицы висит на бирже одновременно. 0 = все сразу." />
                      </label>
                      <MxNum
                        value={config.grid_active ?? 0}
                        onChange={v => patch({ grid_active: Math.max(0, Math.round(v)) })}
                        cls={inputCls}
                      />
                    </div>
                    <div>
                      <label className={labelCls}>
                        Условных стопов (0 = без лимита)
                        <Tip text="Максимум одновременных stop-loss ордеров на бирже. Bybit ограничивает conditional-ордера на символ. 0 = без ограничений." />
                      </label>
                      <MxNum
                        value={config.max_stop_active ?? 0}
                        onChange={v => patch({ max_stop_active: Math.max(0, Math.round(v)) })}
                        cls={inputCls}
                      />
                    </div>
                  </div>
                </div>
              )}

              {/* 2. Матрица */}
              {stratTab === 'matrix' && (() => {
                const colGrid = 'grid grid-cols-[30px_1fr_1fr_6px_1fr_1fr_1fr_1fr_42px_36px_18px] gap-[3px] items-center';
                const sep = <div className="flex items-center justify-center"><div className="w-px h-[20px] bg-gray-700/60" /></div>;
                const isShort = config.direction === 'short';
                const numCls = (err: boolean, disabled: boolean) =>
                  `bg-gray-900 border rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-amber-500 ${err ? 'border-red-500' : 'border-gray-700'} ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`;

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
                    <span className="text-[8px] text-gray-400 text-center">Шаг %<Tip text="% от точки входа до уровня." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Объём %<Tip text="Размер ордера как % от депозита." /></span>
                    <span />
                    <span className="text-[8px] text-gray-400 text-center">Стоп %<Tip text="Расстояние стопа от цены взятия. Пусто = не ставить." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Усл %<Tip text="Условие перевыставления стопа." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Замена %<Tip text="Новое расстояние стопа после перевыставления." /></span>
                    <span className="text-[8px] text-gray-400 text-center">ТП %<Tip text="Тейкпрофит от цены входа. Пусто = не ставить." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Тип<Tip text="Broker — сразу на биржу. Virtual — бот следит." /></span>
                    <span className="text-[8px] text-gray-400 text-center">Сигн<Tip text="Выставлять только при активном сигнале." /></span>
                    <span />
                  </div>
                );

                const levelRow = (direction: 'above' | 'below', level: MatrixLevel, dirIdx: number, labelNum: number, canDelete: boolean) => {
                  const isAbove = direction === 'above';
                  const badgeCls = isAbove
                    ? (isShort ? 'bg-red-900 text-red-300 text-[8px] font-bold rounded text-center py-0.5' : 'bg-emerald-900 text-emerald-300 text-[8px] font-bold rounded text-center py-0.5')
                    : 'bg-blue-900 text-blue-300 text-[8px] font-bold rounded text-center py-0.5';
                  const rowCls = isAbove ? (isShort ? 'bg-red-950/20' : 'bg-emerald-950/20') : 'bg-blue-950/30';
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
                          <div className="flex items-center h-[34px] px-3 rounded-lg border border-amber-500/20 bg-amber-950/20 text-[11px] text-amber-400">
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
                                  ? 'border-amber-500/40 bg-amber-500/[.18] text-amber-300'
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
                          <Tip text="Фиксированный — задаёте USDT вручную. Объём Main — каждый слот размещается от объёма мейн-позиции в реальном времени." />
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

                    {/* ── Строка 2: Плечо слайдер (½ ширины) + toggle Фикс./Макс. (½ ширины) ── */}
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
                          style={{ accentColor: BOT_KIND_META['hedge'].color }}
                        />
                        <div className="flex justify-between text-[10px] text-slate-500 mt-0.5">
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

              {/* 3. Параметры */}
              {stratTab === 'params' && (
                <div className="flex flex-col gap-5">

                  {/* ── Управление Хедж ── */}
                  <div>
                    <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                      Управление Хедж
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
                          ⟳ Перестройка сетки от SZ
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

                  {/* ── Управление Мэйн ── */}
                  <div>
                    <div className="flex items-center gap-1 text-[10px] text-gray-400 uppercase tracking-wider pb-1 border-b border-gray-800 mb-3">
                      Управление Мэйн
                    </div>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className={labelCls}>
                          Отменять ТП на Main
                          <Tip text="При активации хеджа — отменяет все ТП-ордера основного бота." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                          value={String(config.hedge_cancel_main_tp ?? false)}
                          onChange={v => patch({ hedge_cancel_main_tp: v === 'true' })}
                          optionColors={{ true: 'bg-rose-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          Отменять СЛ на Main
                          <Tip text="При активации хеджа — отменяет все СЛ-ордера основного бота." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                          value={String(config.hedge_cancel_main_sl ?? false)}
                          onChange={v => patch({ hedge_cancel_main_sl: v === 'true' })}
                          optionColors={{ true: 'bg-rose-700 text-white' }}
                        />
                      </div>
                      <div>
                        <label className={labelCls}>
                          Переводить Main в остановлено
                          <Tip text="При активации хеджа — полностью останавливает основной бот." />
                        </label>
                        <Toggle
                          options={[{ label: 'Выкл', value: 'false' }, { label: 'Вкл', value: 'true' }]}
                          value={String(config.hedge_stop_main ?? false)}
                          onChange={v => patch({ hedge_stop_main: v === 'true' })}
                          optionColors={{ true: 'bg-orange-700 text-white' }}
                        />
                      </div>
                    </div>
                  </div>

                </div>
              )}
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
              className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#f59e0b,#d97706)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(245,158,11,.5)] disabled:cursor-not-allowed disabled:opacity-40"
            >
              {submitting ? 'Сохранение...' : (bot ? 'Сохранить' : 'Создать')}
            </button>
          </div>
        </div>
      </div>

      {/* Stats reset confirmation modal */}
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

// ─── Helpers ───────────────────────────────────────────────────────────────────

/** Dropdown-пикер: только текст + шеврон (ширина задаётся родителем) */
function EnumPicker({ options, value, onChange }: {
  options: string[];
  value: number;
  onChange: (v: number) => void;
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState({ top: 0, left: 0, width: 0 });
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (!btnRef.current?.contains(e.target as Node) && !menuRef.current?.contains(e.target as Node))
        setOpen(false);
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
    <div className="relative w-full h-full">
      <button
        ref={btnRef}
        type="button"
        onClick={toggle}
        className="w-full h-full flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-black/[.25] px-2.5 py-[7px] text-[12px] text-slate-200 outline-none transition-colors hover:border-white/[.16] cursor-pointer"
      >
        <span className="flex-1 text-left truncate">{options[value]}</span>
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6"
          strokeLinecap="round" strokeLinejoin="round" className="shrink-0 opacity-40">
          <path d="M6 9l6 6 6-6"/>
        </svg>
      </button>

      {open && createPortal(
        <div
          ref={menuRef}
          className="fixed z-[9999] rounded-[9px] p-1 flex flex-col gap-px"
          style={{
            top: pos.top,
            left: pos.left,
            minWidth: Math.max(pos.width, 240),
            background: '#181b28',
            border: '1px solid rgba(255,255,255,.22)',
            boxShadow: '0 8px 32px rgba(0,0,0,.85), 0 0 0 1px rgba(255,255,255,.06)',
          }}
        >
          {options.map((label, i) => (
            <button
              key={i}
              type="button"
              onClick={() => { onChange(i); setOpen(false); }}
              className={`flex items-center gap-2 px-[9px] py-[7px] text-[12px] font-medium text-[#cfd5e1] rounded-[6px] w-full text-left transition-colors hover:bg-white/[.06] ${value === i ? 'bg-white/[.05] text-white' : ''}`}
            >
              <span className={`w-[5px] h-[5px] rounded-full shrink-0 transition-colors ${value === i ? 'bg-amber-400' : 'bg-transparent'}`} />
              {label}
            </button>
          ))}
        </div>,
        document.body
      )}
    </div>
  );
}

function MxNum({ value, onChange, cls, err, disabled }: {
  value: number; onChange: (v: number) => void; cls?: string; err?: string | null; disabled?: boolean;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  return (
    <div className="relative group">
      <input
        type="text"
        inputMode="decimal"
        disabled={disabled}
        value={draft !== null ? draft : value}
        onChange={e => { setDraft(e.target.value); const n = parseFloat(e.target.value); if (!isNaN(n)) onChange(n); }}
        onBlur={() => { if (draft !== null && draft !== '' && draft !== '-') { const n = parseFloat(draft); if (!isNaN(n)) onChange(n); } setDraft(null); }}
        onFocus={() => setDraft(String(value))}
        className={cls ?? 'bg-gray-900 border border-gray-700 rounded py-1 px-1 text-[11px] text-gray-100 text-center w-full outline-none focus:border-amber-500'}
      />
      {err && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">
          {err}
        </div>
      )}
    </div>
  );
}

function MxNull({ value, onChange, err }: {
  value: number | null; onChange: (v: number | null) => void; err?: string | null;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  const display = draft !== null ? draft : (value === null ? '' : String(value));
  return (
    <div className="relative group">
      <input
        type="text"
        inputMode="decimal"
        placeholder="—"
        value={display}
        onChange={e => {
          setDraft(e.target.value);
          const n = parseFloat(e.target.value);
          if (!isNaN(n)) onChange(n);
          else if (e.target.value === '' || e.target.value === '-') onChange(null);
        }}
        onBlur={() => {
          if (draft !== null) {
            if (draft === '' || draft === '-') onChange(null);
            else { const n = parseFloat(draft); onChange(isNaN(n) ? null : n); }
          }
          setDraft(null);
        }}
        onFocus={() => setDraft(value === null ? '' : String(value))}
        className={`bg-gray-900 border rounded py-1 px-1 text-[11px] text-center w-full outline-none focus:border-amber-500 ${err ? 'border-red-500 text-gray-100' : value === null && draft === null ? 'border-dashed border-gray-700 text-gray-500' : 'border-gray-700 text-gray-100'}`}
      />
      {err && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1 z-[200] px-2 py-1 text-[9px] bg-red-950 border border-red-600 text-red-200 rounded whitespace-nowrap pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">
          {err}
        </div>
      )}
    </div>
  );
}

/** Small section divider with icon and color */
function SectionHeader({ children, icon, color }: {
  children: React.ReactNode;
  icon?: string;
  color?: 'amber' | 'blue' | 'green';
}) {
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

/** Simple toggle switch */
function ToggleSwitch({ enabled, onToggle }: { enabled: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      className={`w-9 h-5 rounded-full relative transition-colors shrink-0 ${enabled ? 'bg-amber-500' : 'bg-gray-700'}`}
    >
      <span className={`absolute left-0.5 top-0.5 w-4 h-4 bg-white rounded-full shadow transition-transform ${enabled ? 'translate-x-4' : 'translate-x-0'}`} />
    </button>
  );
}

function Field({ label, children, required, hint }: {
  label: string; children: React.ReactNode; required?: boolean; hint?: string;
}) {
  return (
    <div>
      <div className="mb-1.5 flex items-baseline gap-2">
        <label className="block text-[11px] font-bold uppercase tracking-wider text-slate-400">
          {label}{required && <span className="ml-0.5 text-amber-400">*</span>}
        </label>
        {hint && <span className="text-[10px] text-slate-600">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

// ─── BotMultiPicker ────────────────────────────────────────────────────────────

/** Inline multi-select for choosing bots by name (whitelist / blacklist). */
function BotMultiPicker({
  allBots,
  selected,
  onChange,
  color,
  placeholder,
}: {
  allBots:     BotOption[];
  selected:    string[];
  onChange:    (ids: string[]) => void;
  color:       'blue' | 'red';
  placeholder: string;
}) {
  const [search, setSearch] = useState('');
  const [open,   setOpen]   = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // Close on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const toggle = (id: string) => {
    onChange(selected.includes(id) ? selected.filter(x => x !== id) : [...selected, id]);
  };

  const filtered = allBots.filter(b =>
    !selected.includes(b.id) &&
    b.name.toLowerCase().includes(search.toLowerCase()),
  );

  const tagCls = color === 'blue'
    ? 'border-blue-700/50 bg-blue-900/20 text-blue-300'
    : 'border-rose-700/50 bg-rose-900/20 text-rose-300';

  const dotCls = color === 'blue' ? 'bg-blue-400' : 'bg-rose-400';

  return (
    <div ref={rootRef} className="relative flex flex-col gap-1.5">
      {/* Selected tags */}
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {selected.map(id => {
            const b = allBots.find(x => x.id === id);
            return (
              <span
                key={id}
                className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium ${tagCls}`}
              >
                <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${dotCls}`} />
                <span className="max-w-[120px] truncate">{b?.name ?? id}</span>
                <button
                  type="button"
                  onClick={() => toggle(id)}
                  className="ml-0.5 text-current opacity-60 hover:opacity-100"
                >
                  ×
                </button>
              </span>
            );
          })}
        </div>
      )}

      {/* Input to open dropdown */}
      <div
        className="flex min-h-[34px] cursor-text items-center gap-2 rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-1.5"
        onClick={() => setOpen(true)}
      >
        {open ? (
          <input
            autoFocus
            type="text"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder={placeholder}
            className="flex-1 bg-transparent text-[12px] text-slate-200 outline-none placeholder:text-slate-500"
          />
        ) : (
          <span className="flex-1 text-[12px] text-slate-500">{placeholder}</span>
        )}
        <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6"
          strokeLinecap="round" strokeLinejoin="round" className="shrink-0 opacity-30 text-slate-400">
          <path d="M6 9l6 6 6-6"/>
        </svg>
      </div>

      {/* Dropdown */}
      {open && (
        <div className="absolute top-full left-0 right-0 z-50 mt-1 max-h-[200px] overflow-y-auto rounded-[9px] border border-white/[.14] bg-[#181b28] p-1 shadow-[0_8px_32px_rgba(0,0,0,.8)]">
          {allBots.length === 0 && (
            <div className="px-3 py-2 text-[11px] text-slate-500">Загрузка ботов...</div>
          )}
          {allBots.length > 0 && filtered.length === 0 && (
            <div className="px-3 py-2 text-[11px] text-slate-500">Ничего не найдено</div>
          )}
          {filtered.map(b => (
            <button
              key={b.id}
              type="button"
              onClick={() => { toggle(b.id); setSearch(''); }}
              className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-left text-[12px] text-slate-300 transition-colors hover:bg-white/[.06]"
            >
              <div className="flex h-6 w-6 shrink-0 items-center justify-center overflow-hidden rounded-md border border-white/[.08] bg-white/[.04] text-slate-500">
                {b.avatarUrl
                  ? <img src={b.avatarUrl} alt="" className="h-full w-full object-cover" />
                  : <Shield size={11} strokeWidth={1.8} />
                }
              </div>
              <span className="flex-1 truncate">{b.name}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

const inputCls =
  'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-amber-500/50';


const labelCls = 'flex items-center text-xs text-gray-500 mb-1';
