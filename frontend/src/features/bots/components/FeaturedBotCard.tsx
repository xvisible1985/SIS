import { Check, Flame, Users, Star, ArrowRight } from 'lucide-react';
import type { FeaturedBot } from '../ui-types';
import { STRAT_META } from '../strategyMeta';
import { StrategyChip } from './StrategyChip';
import { RiskChip } from './RiskChip';
import { PriceBadge } from './PriceBadge';
import { Sparkline } from './Sparkline';

type Props = {
  bot: FeaturedBot;
  onOpen: () => void;
  onLaunch: () => void;
};

/** Свернутая карточка готового бота (для библиотеки) */
export function FeaturedBotCard({ bot, onOpen, onLaunch }: Props) {
  const m = STRAT_META[bot.strategy];
  const Icon = m.icon;

  return (
    <button
      type="button"
      onClick={onOpen}
      className="relative flex flex-col overflow-hidden rounded-[14px] border border-white/[.06] bg-[linear-gradient(180deg,rgba(255,255,255,.025)_0%,rgba(255,255,255,.01)_100%)] p-4 text-left transition-colors hover:border-white/15"
    >
      {/* corner: HOT + price */}
      <div className="absolute right-3 top-3 flex items-center gap-1.5">
        {bot.fire && (
          <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/32 bg-amber-500/[.18] px-1.5 py-0.5 text-[9px] font-extrabold uppercase tracking-wider text-amber-400">
            <Flame size={9} strokeWidth={2.4} />
            HOT
          </span>
        )}
        <PriceBadge price={bot.price} size="sm" />
      </div>

      {/* header */}
      <div className="mb-3 flex items-start gap-3 pr-20">
        <div
          className="flex h-[42px] w-[42px] shrink-0 items-center justify-center rounded-[10px] border"
          style={{ background: m.bg, borderColor: m.border, color: m.color }}
        >
          <Icon size={18} strokeWidth={2} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">{bot.name}</span>
            {bot.verified && (
              <span
                title="Verified by NovaBot"
                className="inline-flex h-[14px] w-[14px] shrink-0 items-center justify-center rounded-full bg-[#5b8cff] text-white"
              >
                <Check size={10} strokeWidth={3} />
              </span>
            )}
          </div>
          <div className="mt-0.5 text-[11px] text-slate-400">
            от <span className="font-semibold text-slate-200">{bot.author}</span>
          </div>
        </div>
      </div>

      {/* chips */}
      <div className="mb-3 flex flex-wrap items-center gap-1.5">
        <StrategyChip strategy={bot.strategy} size="sm" />
        <RiskChip risk={bot.risk} />
        <span className="font-mono text-[10px] text-slate-400">×{bot.lev} {bot.mode}</span>
      </div>

      {/* desc */}
      <p className="mb-3.5 min-h-[36px] text-xs leading-relaxed text-slate-300">{bot.desc}</p>

      {/* perf + spark */}
      <div className="mb-3 flex items-center gap-3 rounded-[10px] bg-black/20 p-3">
        <div>
          <div className="text-[9px] font-bold uppercase tracking-wider text-slate-400">30 дней</div>
          <div className="mt-px font-display text-xl font-bold tracking-tight text-emerald-300">
            +{bot.perfMonth.toFixed(2)}%
          </div>
        </div>
        <div className="flex-1">
          <Sparkline data={bot.spark} color="#5be0a0" width={130} height={36} />
        </div>
      </div>

      {/* mini stats */}
      <div className="mb-3 grid grid-cols-3 gap-0">
        <MiniStat label="Win"    value={`${bot.winRate}%`}   color="text-emerald-300" />
        <MiniStat label="Sharpe" value={bot.sharpe.toFixed(2)} />
        <MiniStat label="DD"     value={`${bot.drawdown}%`}   color="text-rose-300" />
      </div>

      {/* footer */}
      <div className="mt-auto flex items-center gap-2 border-t border-white/[.05] pt-3">
        <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
          <Users size={12} />
          <span className="font-semibold text-slate-200">{bot.users.toLocaleString('en-US')}</span>
        </div>
        <span className="h-0.5 w-0.5 rounded-full bg-slate-600" />
        <div className="flex items-center gap-1 text-[11px] text-slate-400">
          <Star size={11} className="text-amber-400" />
          <span className="font-semibold text-slate-200">{bot.rating}</span>
        </div>
        <div className="flex-1" />
        <span
          onClick={(e) => { e.stopPropagation(); onLaunch(); }}
          className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
        >
          Запустить
          <ArrowRight size={12} strokeWidth={2.4} />
        </span>
      </div>
    </button>
  );
}

function MiniStat({ label, value, color = 'text-slate-200' }: { label: string; value: string; color?: string }) {
  return (
    <div className="text-center">
      <div className="text-[9px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-0.5 font-mono text-xs font-semibold ${color}`}>{value}</div>
    </div>
  );
}
