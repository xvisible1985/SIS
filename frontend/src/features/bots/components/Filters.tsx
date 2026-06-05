import { Search, X, Check, LayoutGrid, SlidersHorizontal, TrendingUp, Rss, Shield } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { BotFilters, BotStrategy, RiskLevel, TradeMode } from '../ui-types';
import type { BotKind } from '../types';
import { RISK_META, STRAT_META } from '../strategyMeta';
import { getBotKindMeta } from '../botKindMeta';

const KIND_BUTTONS: { kind: BotKind | 'all'; label: string; Icon?: LucideIcon }[] = [
  { kind: 'all',    label: 'Все' },
  { kind: 'signal', label: 'Signal',  Icon: TrendingUp },
  { kind: 'parser', label: 'Parser',  Icon: Rss        },
  { kind: 'hedge',  label: 'Hedge',   Icon: Shield     },
];

type Props = {
  value: BotFilters;
  onChange: (next: BotFilters) => void;
};

const STRATEGIES: (BotStrategy | 'all')[] = ['all', 'grid', 'matrix', 'signal', 'scalp', 'arbitrage', 'copy', 'trend', 'hold'];
const RISKS:      (RiskLevel | 'all')[]   = ['all', 'low', 'medium', 'high'];
const MODES:      (TradeMode | 'all')[]   = ['all', 'spot', 'futures'];

const SORTS = [
  { id: 'popular' as const, label: 'Популярные' },
  { id: 'profit'  as const, label: 'Доходные' },
  { id: 'winrate' as const, label: 'Win-rate' },
  { id: 'newest'  as const, label: 'Новые' },
];

const PRICING = [
  { id: 'all'  as const, label: 'Все',          c: null,       bg: 'bg-[#5b8cff]/[.16] border-[#5b8cff]/28 text-[#b8c8ff]' },
  { id: 'free' as const, label: 'Бесплатные',   c: '#5be0a0',  bg: 'bg-emerald-400/[.14] border-emerald-400/28 text-emerald-300' },
  { id: 'paid' as const, label: 'Платные',      c: '#f7a600',  bg: 'bg-amber-500/[.14] border-amber-500/28 text-amber-400' },
];

export function Filters({ value, onChange }: Props) {
  const set = <K extends keyof BotFilters>(k: K, v: BotFilters[K]) => onChange({ ...value, [k]: v });

  return (
    <div>
      {/* top row: search + sort + verified + view */}
      <div className="mb-2.5 flex items-center gap-2.5">
        <div className="relative flex max-w-[380px] flex-1 items-center gap-1 rounded-[10px] border border-white/[.08] bg-white/[.03] px-3">
          <Search size={15} className="text-slate-400" />
          <input
            value={value.q}
            onChange={(e) => set('q', e.target.value)}
            placeholder="Поиск бота, пары, тега…"
            className="flex-1 bg-transparent px-2 py-2.5 text-sm text-slate-100 outline-none"
          />
          {value.q && (
            <button
              type="button"
              onClick={() => set('q', '')}
              className="flex h-[30px] w-[30px] items-center justify-center rounded text-slate-400 hover:bg-white/[.05]"
            >
              <X size={11} />
            </button>
          )}
        </div>

        <div className="flex gap-px rounded-lg border border-white/[.06] bg-white/[.02] p-0.5">
          {SORTS.map((s) => {
            const on = value.sort === s.id;
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => set('sort', s.id)}
                className={
                  'rounded-md px-3 py-1.5 text-[11px] font-semibold ' +
                  (on
                    ? 'border border-[#5b8cff]/28 bg-[#5b8cff]/[.16] text-[#b8c8ff]'
                    : 'border border-transparent text-slate-300 hover:text-slate-100')
                }
              >
                {s.label}
              </button>
            );
          })}
        </div>

        <div className="flex-1" />

        <button
          type="button"
          onClick={() => set('verified', !value.verified)}
          className={
            'inline-flex items-center gap-1.5 rounded-lg border px-3 py-2 text-xs font-semibold ' +
            (value.verified
              ? 'border-[#5b8cff]/32 bg-[#5b8cff]/[.16] text-[#b8c8ff]'
              : 'border-white/[.08] bg-white/[.03] text-slate-200')
          }
        >
          <Check size={12} />Только Verified
        </button>

        <div className="flex gap-px rounded-lg border border-white/[.06] bg-white/[.02] p-0.5">
          {(['grid', 'list'] as const).map((id) => {
            const on = value.view === id;
            const Icon = id === 'grid' ? LayoutGrid : SlidersHorizontal;
            return (
              <button
                key={id}
                type="button"
                onClick={() => set('view', id)}
                className={
                  'inline-flex items-center justify-center rounded-md px-2.5 py-1.5 ' +
                  (on
                    ? 'border border-[#5b8cff]/28 bg-[#5b8cff]/[.16] text-[#b8c8ff]'
                    : 'border border-transparent text-slate-400')
                }
              >
                <Icon size={13} />
              </button>
            );
          })}
        </div>
      </div>

      {/* chips row: botKind + strategy + risk + mode + pricing */}
      <div className="flex flex-wrap items-center gap-3.5">
        <FilterGroup label="Бот">
          {KIND_BUTTONS.map(({ kind, label, Icon }) => {
            const on = value.botKind === kind;
            const km = kind !== 'all' ? getBotKindMeta(kind as BotKind) : null;
            return (
              <button
                key={kind}
                type="button"
                onClick={() => set('botKind', kind)}
                className="inline-flex items-center gap-1.5 rounded-[7px] border px-2.5 py-1 text-[11px] font-semibold"
                style={
                  on
                    ? { background: km?.iconBg ?? 'rgba(91,140,255,.16)', borderColor: km?.border ?? 'rgba(91,140,255,.28)', color: km?.color ?? '#b8c8ff' }
                    : { background: 'rgba(255,255,255,.025)', borderColor: 'rgba(255,255,255,.06)', color: '#9aa6c8' }
                }
              >
                {Icon && <Icon size={11} strokeWidth={2} />}
                {label}
              </button>
            );
          })}
        </FilterGroup>

        <Divider />

        <FilterGroup label="Стратегия">
          {STRATEGIES.map((s) => {
            const on = value.strategy === s;
            const m = s !== 'all' ? STRAT_META[s as BotStrategy] : null;
            const Icon = m?.icon;
            return (
              <button
                key={s}
                type="button"
                onClick={() => set('strategy', s)}
                className={
                  'inline-flex items-center gap-1.5 rounded-[7px] border px-2.5 py-1 text-[11px] font-semibold '
                }
                style={
                  on
                    ? { background: m?.bg ?? 'rgba(91,140,255,.16)', borderColor: m?.border ?? 'rgba(91,140,255,.28)', color: m?.color ?? '#b8c8ff' }
                    : { background: 'rgba(255,255,255,.025)', borderColor: 'rgba(255,255,255,.06)', color: '#9aa6c8' }
                }
              >
                {Icon && <Icon size={11} strokeWidth={2} />}
                {s === 'all' ? 'Все' : m?.label}
              </button>
            );
          })}
        </FilterGroup>

        <Divider />

        <FilterGroup label="Риск">
          {RISKS.map((r) => {
            const on = value.risk === r;
            const meta = r !== 'all' ? RISK_META[r as RiskLevel] : null;
            return (
              <button
                key={r}
                type="button"
                onClick={() => set('risk', r)}
                className="inline-flex items-center gap-1.5 rounded-[7px] border px-2.5 py-1 text-[11px] font-semibold"
                style={
                  on
                    ? { background: meta?.bg ?? 'rgba(91,140,255,.16)', borderColor: meta?.border ?? 'rgba(91,140,255,.28)', color: meta?.color ?? '#b8c8ff' }
                    : { background: 'rgba(255,255,255,.025)', borderColor: 'rgba(255,255,255,.06)', color: '#9aa6c8' }
                }
              >
                {meta && <span className="h-1.5 w-1.5 rounded-full" style={{ background: meta.color }} />}
                {r === 'all' ? 'Любой' : meta?.label.replace(' риск', '')}
              </button>
            );
          })}
        </FilterGroup>

        <Divider />

        <FilterGroup label="Режим">
          {MODES.map((m) => {
            const on = value.mode === m;
            return (
              <button
                key={m}
                type="button"
                onClick={() => set('mode', m)}
                className={
                  'rounded-[7px] border px-2.5 py-1 text-[11px] font-semibold ' +
                  (on
                    ? 'border-[#5b8cff]/28 bg-[#5b8cff]/[.16] text-[#b8c8ff]'
                    : 'border-white/[.06] bg-white/[.025] text-slate-400')
                }
              >
                {m === 'all' ? 'Все' : m}
              </button>
            );
          })}
        </FilterGroup>

        <Divider />

        <FilterGroup label="Цена">
          {PRICING.map((p) => {
            const on = value.pricing === p.id;
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => set('pricing', p.id)}
                className={
                  'inline-flex items-center gap-1.5 rounded-[7px] border px-2.5 py-1 text-[11px] font-semibold ' +
                  (on ? p.bg : 'border-white/[.06] bg-white/[.025] text-slate-400')
                }
              >
                {p.c && <span className="h-1.5 w-1.5 rounded-full" style={{ background: p.c }} />}
                {p.label}
              </button>
            );
          })}
        </FilterGroup>
      </div>
    </div>
  );
}

function FilterGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="mr-1 text-[10px] font-bold uppercase tracking-wider text-slate-500">{label}</span>
      {children}
    </div>
  );
}
function Divider() {
  return <div className="h-[18px] w-px bg-white/[.06]" />;
}
