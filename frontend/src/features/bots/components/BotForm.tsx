import { useState, useEffect } from 'react';
import { X, Bot, ToggleLeft, ToggleRight } from 'lucide-react';
import type { Bot as BotType, CreateBotInput, StrategyConfig } from '../types';

type Props = {
  bot?: BotType;
  onSubmit: (data: CreateBotInput) => void;
  onClose: () => void;
};

type Tab = 'basic' | 'strategy' | 'symbols';

export function BotForm({ bot, onSubmit, onClose }: Props) {
  const [tab, setTab] = useState<Tab>('basic');
  const [name, setName] = useState(bot?.name ?? '');
  const [description, setDescription] = useState(bot?.description ?? '');
  const [isPublic, setIsPublic] = useState(bot?.isPublic ?? false);
  const [whitelist, setWhitelist] = useState<string[]>(bot?.symbolWhitelist ?? []);
  const [blacklist, setBlacklist] = useState<string[]>(bot?.symbolBlacklist ?? []);
  const [config, setConfig] = useState<StrategyConfig>({
    symbol:         bot?.strategyConfig?.symbol         ?? 'BTCUSDT',
    category:       bot?.strategyConfig?.category       ?? 'linear',
    direction:      bot?.strategyConfig?.direction      ?? 'long',
    grid_levels:    bot?.strategyConfig?.grid_levels    ?? 5,
    grid_active:    bot?.strategyConfig?.grid_active    ?? 3,
    grid_step_pct:  bot?.strategyConfig?.grid_step_pct  ?? 1.0,
    grid_size_usdt: bot?.strategyConfig?.grid_size_usdt ?? 100,
    tp_mode:        bot?.strategyConfig?.tp_mode        ?? 'total',
    tp_pct:         bot?.strategyConfig?.tp_pct         ?? 2.0,
    sl_type:        bot?.strategyConfig?.sl_type        ?? 'conditional',
    sl_pct:         bot?.strategyConfig?.sl_pct         ?? 5.0,
    signal_filter:  bot?.strategyConfig?.signal_filter  ?? false,
  });

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  const handleSubmit = () => {
    if (!name.trim()) return;
    onSubmit({
      name: name.trim(),
      description: description.trim(),
      isPublic,
      symbolWhitelist: whitelist,
      symbolBlacklist: blacklist,
      strategyConfig: config,
    });
    onClose();
  };

  const tabs: { id: Tab; label: string }[] = [
    { id: 'basic',    label: 'Основное' },
    { id: 'strategy', label: 'Стратегия' },
    { id: 'symbols',  label: 'Символы' },
  ];

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[calc(100vh-48px)] w-full max-w-[680px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]"
      >
        {/* header */}
        <div className="flex items-center gap-3 border-b border-white/[.06] px-5 py-4">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[9px] border border-[#5b8cff]/30 bg-[#5b8cff]/[.12] text-[#5b8cff]">
            <Bot size={16} strokeWidth={2} />
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

        {/* tabs */}
        <div className="flex border-b border-white/[.06] px-5">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              className={
                'relative mr-4 py-3 text-[12px] font-semibold transition-colors ' +
                (tab === t.id ? 'text-slate-50' : 'text-slate-400 hover:text-slate-200')
              }
            >
              {t.label}
              {tab === t.id && (
                <span className="absolute bottom-0 left-0 right-0 h-[2px] rounded-full bg-[#5b8cff]" />
              )}
            </button>
          ))}
        </div>

        {/* body */}
        <div className="flex-1 overflow-auto p-5">
          {tab === 'basic' && (
            <BasicTab
              name={name} setName={setName}
              description={description} setDescription={setDescription}
              isPublic={isPublic} setIsPublic={setIsPublic}
            />
          )}
          {tab === 'strategy' && (
            <StrategyTab config={config} setConfig={setConfig} />
          )}
          {tab === 'symbols' && (
            <SymbolsTab
              whitelist={whitelist} setWhitelist={setWhitelist}
              blacklist={blacklist} setBlacklist={setBlacklist}
            />
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

// ─── Basic tab ──────────────────────────────────────────────────────────────

function BasicTab({
  name, setName,
  description, setDescription,
  isPublic, setIsPublic,
}: {
  name: string; setName: (v: string) => void;
  description: string; setDescription: (v: string) => void;
  isPublic: boolean; setIsPublic: (v: boolean) => void;
}) {
  return (
    <div className="flex flex-col gap-4">
      <Field label="Название" required>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="BTC Grid Pro"
          className="w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50"
        />
      </Field>

      <Field label="Описание">
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={4}
          placeholder="Опишите стратегию бота..."
          className="w-full resize-none rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50"
        />
      </Field>

      <div className="flex items-center justify-between rounded-lg border border-white/[.06] bg-white/[.02] px-4 py-3">
        <div>
          <div className="text-[13px] font-semibold text-slate-200">Публичный бот</div>
          <div className="mt-0.5 text-[11px] text-slate-400">
            Другие пользователи смогут подписаться на этого бота
          </div>
        </div>
        <button type="button" onClick={() => setIsPublic(!isPublic)} className="text-slate-400 hover:text-slate-200">
          {isPublic
            ? <ToggleRight size={28} className="text-[#5b8cff]" />
            : <ToggleLeft size={28} />}
        </button>
      </div>
    </div>
  );
}

// ─── Strategy tab ────────────────────────────────────────────────────────────

function StrategyTab({
  config, setConfig,
}: {
  config: StrategyConfig;
  setConfig: (c: StrategyConfig) => void;
}) {
  const set = <K extends keyof StrategyConfig>(k: K, v: StrategyConfig[K]) =>
    setConfig({ ...config, [k]: v });

  const num = (k: keyof StrategyConfig) => (e: React.ChangeEvent<HTMLInputElement>) =>
    set(k, Number(e.target.value) as StrategyConfig[typeof k]);

  return (
    <div className="flex flex-col gap-5">
      {/* Market */}
      <Section label="Рынок">
        <div className="grid grid-cols-3 gap-3">
          <Field label="Символ">
            <input
              type="text"
              value={config.symbol ?? ''}
              onChange={(e) => set('symbol', e.target.value.toUpperCase())}
              placeholder="BTCUSDT"
              className={inputCls}
            />
          </Field>
          <Field label="Категория">
            <select value={config.category ?? 'linear'} onChange={(e) => set('category', e.target.value)} className={selectCls}>
              <option value="linear">Linear</option>
              <option value="inverse">Inverse</option>
              <option value="spot">Spot</option>
            </select>
          </Field>
          <Field label="Направление">
            <select value={config.direction ?? 'long'} onChange={(e) => set('direction', e.target.value as StrategyConfig['direction'])} className={selectCls}>
              <option value="long">Long</option>
              <option value="short">Short</option>
              <option value="both">Both</option>
            </select>
          </Field>
        </div>
      </Section>

      {/* Grid */}
      <Section label="Сетка">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Уровней в сетке">
            <input type="number" min={1} max={50} value={config.grid_levels ?? 5} onChange={num('grid_levels')} className={inputCls} />
          </Field>
          <Field label="Активных ордеров">
            <input type="number" min={1} max={50} value={config.grid_active ?? 3} onChange={num('grid_active')} className={inputCls} />
          </Field>
          <Field label="Шаг сетки, %">
            <input type="number" step={0.1} min={0.1} value={config.grid_step_pct ?? 1.0} onChange={num('grid_step_pct')} className={inputCls} />
          </Field>
          <Field label="Размер позиции, USDT">
            <input type="number" min={1} value={config.grid_size_usdt ?? 100} onChange={num('grid_size_usdt')} className={inputCls} />
          </Field>
        </div>
      </Section>

      {/* TP / SL */}
      <Section label="Take Profit / Stop Loss">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Режим TP">
            <select value={config.tp_mode ?? 'total'} onChange={(e) => set('tp_mode', e.target.value as StrategyConfig['tp_mode'])} className={selectCls}>
              <option value="total">Total</option>
              <option value="per_level">Per Level</option>
            </select>
          </Field>
          <Field label="TP, %">
            <input type="number" step={0.1} min={0.1} value={config.tp_pct ?? 2.0} onChange={num('tp_pct')} className={inputCls} />
          </Field>
          <Field label="Тип SL">
            <select value={config.sl_type ?? 'conditional'} onChange={(e) => set('sl_type', e.target.value as StrategyConfig['sl_type'])} className={selectCls}>
              <option value="conditional">Conditional</option>
              <option value="programmatic">Programmatic</option>
            </select>
          </Field>
          <Field label="SL, %">
            <input type="number" step={0.1} min={0.1} value={config.sl_pct ?? 5.0} onChange={num('sl_pct')} className={inputCls} />
          </Field>
        </div>
      </Section>

      {/* Other */}
      <div className="flex items-center justify-between rounded-lg border border-white/[.06] bg-white/[.02] px-4 py-3">
        <div>
          <div className="text-[13px] font-semibold text-slate-200">Signal filter</div>
          <div className="mt-0.5 text-[11px] text-slate-400">Фильтровать входы по сигналу индикатора</div>
        </div>
        <button type="button" onClick={() => set('signal_filter', !config.signal_filter)} className="text-slate-400 hover:text-slate-200">
          {config.signal_filter
            ? <ToggleRight size={28} className="text-[#5b8cff]" />
            : <ToggleLeft size={28} />}
        </button>
      </div>
    </div>
  );
}

// ─── Symbols tab ─────────────────────────────────────────────────────────────

function SymbolsTab({
  whitelist, setWhitelist,
  blacklist, setBlacklist,
}: {
  whitelist: string[]; setWhitelist: (v: string[]) => void;
  blacklist: string[]; setBlacklist: (v: string[]) => void;
}) {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <div className="mb-1.5 text-[11px] font-bold uppercase tracking-wider text-slate-400">
          Whitelist
        </div>
        <div className="mb-1 text-[11px] text-slate-500">
          Бот будет торговать только этими символами. Оставь пустым — без ограничений.
        </div>
        <TagInput
          tags={whitelist}
          setTags={setWhitelist}
          placeholder="BTCUSDT — Enter для добавления"
          color="blue"
        />
      </div>
      <div>
        <div className="mb-1.5 text-[11px] font-bold uppercase tracking-wider text-slate-400">
          Blacklist
        </div>
        <div className="mb-1 text-[11px] text-slate-500">
          Эти символы будут исключены.
        </div>
        <TagInput
          tags={blacklist}
          setTags={setBlacklist}
          placeholder="DOGEUSDT — Enter для добавления"
          color="red"
        />
      </div>
    </div>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

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

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="rounded-[10px] border border-white/[.06] bg-white/[.015] p-4">
      <div className="mb-3 text-[10px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      {children}
    </div>
  );
}

function TagInput({
  tags, setTags, placeholder, color,
}: {
  tags: string[];
  setTags: (v: string[]) => void;
  placeholder: string;
  color: 'blue' | 'red';
}) {
  const [input, setInput] = useState('');

  const add = () => {
    const val = input.trim().toUpperCase();
    if (val && !tags.includes(val)) setTags([...tags, val]);
    setInput('');
  };

  const remove = (tag: string) => setTags(tags.filter((t) => t !== tag));

  const tagCls = color === 'blue'
    ? 'border-[#5b8cff]/25 bg-[#5b8cff]/[.12] text-[#a0b8ff]'
    : 'border-rose-500/25 bg-rose-500/[.12] text-rose-300';

  return (
    <div className="rounded-lg border border-white/[.08] bg-black/[.2] p-3">
      {tags.length > 0 && (
        <div className="mb-2.5 flex flex-wrap gap-1.5">
          {tags.map((t) => (
            <span
              key={t}
              className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-[11px] font-semibold ${tagCls}`}
            >
              {t}
              <button
                type="button"
                onClick={() => remove(t)}
                className="opacity-60 hover:opacity-100"
              >
                <X size={9} />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex items-center gap-2">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(); }
          }}
          placeholder={placeholder}
          className="flex-1 bg-transparent font-mono text-[12px] text-slate-200 outline-none placeholder:text-slate-500"
        />
        <button
          type="button"
          onClick={add}
          className="text-[11px] font-semibold text-[#5b8cff] hover:text-[#7ba0ff]"
        >
          Добавить
        </button>
      </div>
    </div>
  );
}

const inputCls =
  'w-full rounded-lg border border-white/[.08] bg-black/[.25] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors placeholder:text-slate-500 focus:border-[#5b8cff]/50';

const selectCls =
  'w-full rounded-lg border border-white/[.08] bg-[#0d1120] px-3 py-2 text-[13px] text-slate-200 outline-none transition-colors focus:border-[#5b8cff]/50';
