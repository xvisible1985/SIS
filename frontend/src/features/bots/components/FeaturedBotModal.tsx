import { useEffect } from 'react';
import { Check, Flame, X, Copy, Share2, SlidersHorizontal, Play, TrendingUp, Search, Shield } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { FeaturedBot } from '../ui-types';
import type { BotKind } from '../types';
import { STRAT_META } from '../strategyMeta';
import { getBotKindMeta } from '../botKindMeta';
import { StrategyChip } from './StrategyChip';
import { RiskChip } from './RiskChip';
import { PriceBadge } from './PriceBadge';
import { Sparkline } from './Sparkline';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
};

type Props = {
  bot: FeaturedBot;
  onClose: () => void;
  onLaunchDefault: () => void;
  onConfigure: () => void;
  onClone: () => void;
};

/** Развёрнутый просмотр готового бота в модалке */
export function FeaturedBotModal({ bot, onClose, onLaunchDefault, onConfigure, onClone }: Props) {
  // ESC closes
  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  const strat = STRAT_META[bot.strategy];
  const km    = bot.botKind ? getBotKindMeta(bot.botKind) : null;
  const Icon: LucideIcon = bot.botKind ? KIND_ICONS[bot.botKind] : strat.icon;
  const iconBg     = km ? km.iconBg  : strat.bg;
  const iconBorder = km ? km.border  : strat.border;
  const iconColor  = km ? km.color   : strat.color;

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[calc(100vh-48px)] w-full max-w-[820px] flex-col overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]"
      >
        {/* header */}
        <div className="flex items-start gap-3.5 border-b border-white/[.06] px-5 pb-4 pt-5">
          <div
            className="flex h-[54px] w-[54px] shrink-0 items-center justify-center rounded-xl border"
            style={{ background: iconBg, borderColor: iconBorder, color: iconColor }}
          >
            <Icon size={22} strokeWidth={2} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="mb-1.5 flex flex-wrap items-center gap-2">
              <h2 className="m-0 font-display text-[22px] font-bold tracking-tight text-slate-50">{bot.name}</h2>
              {bot.verified && (
                <span className="inline-flex h-[18px] w-[18px] items-center justify-center rounded-full bg-[#5b8cff] text-white">
                  <Check size={13} strokeWidth={3} />
                </span>
              )}
              {bot.fire && (
                <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/32 bg-amber-500/[.18] px-2 py-0.5 text-[10px] font-extrabold uppercase tracking-wider text-amber-400">
                  <Flame size={10} strokeWidth={2.4} />
                  HOT
                </span>
              )}
              <PriceBadge price={bot.price} />
            </div>
            <div className="mb-1.5 flex flex-wrap items-center gap-2">
              <StrategyChip strategy={bot.strategy} />
              <RiskChip risk={bot.risk} />
              {bot.tags.map((t) => (
                <span
                  key={t}
                  className="rounded-[5px] border border-white/[.08] bg-white/[.04] px-2 py-0.5 text-[10px] font-semibold lowercase text-slate-400"
                >
                  {t}
                </span>
              ))}
            </div>
            <div className="text-xs text-slate-400">
              от <span className="font-semibold text-slate-200">{bot.author}</span> ·{' '}
              {bot.users.toLocaleString('en-US')} пользователей · ⭐ {bot.rating}
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          >
            <X size={14} />
          </button>
        </div>

        {/* body */}
        <div className="flex flex-col gap-4 overflow-auto p-5">
          <p className="m-0 text-[13px] leading-relaxed text-slate-200">{bot.desc}</p>

          {/* big perf */}
          <div className="grid grid-cols-[auto_1fr] gap-4 rounded-xl border border-[#5b8cff]/20 bg-[linear-gradient(180deg,rgba(91,140,255,.06),rgba(123,91,255,.03))] p-4">
            <div>
              <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400">
                Доходность · 30 дней
              </div>
              <div className="mt-0.5 font-display text-[32px] font-bold tracking-tight text-emerald-300">
                +{bot.perfMonth.toFixed(2)}%
              </div>
              <div className="mt-0.5 text-[11px] text-slate-400">
                Всего: <span className="font-semibold text-emerald-300">+{bot.perfTotal.toFixed(2)}%</span>
              </div>
            </div>
            <div className="self-center">
              <Sparkline data={bot.spark} color="#5be0a0" width={400} height={60} />
            </div>
          </div>

          {/* full description */}
          {bot.fullDescription && (
            <div>
              <SectionLabel>Описание</SectionLabel>
              <div
                className="rounded-xl border border-white/[.05] bg-white/[.02] px-4 py-3.5 text-[13px] leading-relaxed text-slate-300 whitespace-pre-wrap"
                style={{ borderColor: km ? `${km.border}` : undefined }}
              >
                {bot.fullDescription}
              </div>
            </div>
          )}

          {/* stats */}
          <div>
            <SectionLabel>Метрики</SectionLabel>
            <div className="grid grid-cols-4 gap-2">
              <BigStat label="Win-rate"      value={`${bot.winRate}%`}    color="text-emerald-300" />
              <BigStat label="Sharpe ratio"  value={bot.sharpe.toFixed(2)} />
              <BigStat label="Max drawdown"  value={`${bot.drawdown}%`}    color="text-rose-300" />
              <BigStat label="Мин. капитал"  value={`$${bot.minCap}`} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <SectionLabel>Поддерживаемые пары</SectionLabel>
              <div className="flex flex-wrap gap-1.5">
                {bot.pairs.map((p) => (
                  <span
                    key={p}
                    className="rounded-md border border-white/[.08] bg-white/[.04] px-2.5 py-1 font-mono text-[11px] font-semibold text-slate-200"
                  >
                    {p}USDT
                  </span>
                ))}
              </div>
            </div>
            <div>
              <SectionLabel>Биржи</SectionLabel>
              <div className="flex flex-wrap gap-1.5">
                {bot.exchanges.map((e) => (
                  <span
                    key={e}
                    className="rounded-md border border-white/[.08] bg-white/[.04] px-2.5 py-1 text-[11px] font-semibold text-slate-200"
                  >
                    {e}
                  </span>
                ))}
              </div>
            </div>
          </div>

          {/* config preview */}
          <div className="rounded-lg border border-white/[.05] bg-black/20 px-3.5 py-3">
            <div className="mb-2 flex items-center gap-2">
              <SlidersHorizontal size={13} className="text-slate-400" />
              <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400">Базовые настройки</span>
              <span className="ml-auto text-[10px] text-slate-500">можно изменить при запуске</span>
            </div>
            <div className="grid grid-cols-3 gap-3.5 font-mono text-[11px]">
              <ConfigRow label="Плечо"     value={`×${bot.lev}`} />
              <ConfigRow label="Режим"     value={bot.mode} />
              <ConfigRow label="Комиссия"  value={`${bot.fee}%`} />
            </div>
          </div>
        </div>

        {/* footer */}
        <div className="flex items-center gap-2 border-t border-white/[.06] px-5 py-3.5">
          <FooterBtn onClick={onClone}><Copy size={12} />В свои боты</FooterBtn>
          <FooterBtn onClick={() => { /* share */ }}><Share2 size={12} />Поделиться</FooterBtn>
          <div className="flex-1" />
          <FooterBtn onClick={onConfigure}><SlidersHorizontal size={12} />Настроить и запустить</FooterBtn>
          <button
            type="button"
            onClick={onLaunchDefault}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
          >
            <Play size={11} fill="currentColor" />Запустить с дефолтами
          </button>
        </div>
      </div>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return <div className="mb-2 text-[10px] font-bold uppercase tracking-wider text-slate-400">{children}</div>;
}
function BigStat({ label, value, color = 'text-slate-50' }: { label: string; value: string; color?: string }) {
  return (
    <div className="rounded-[10px] border border-white/[.06] bg-white/[.025] px-3 py-2.5">
      <div className="text-[9px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-1 font-display text-lg font-bold tracking-tight ${color}`}>{value}</div>
    </div>
  );
}
function ConfigRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span className="text-slate-400">{label}: </span>
      <span className="font-semibold text-slate-200">{value}</span>
    </div>
  );
}
function FooterBtn({ children, onClick }: { children: React.ReactNode; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
    >
      {children}
    </button>
  );
}
