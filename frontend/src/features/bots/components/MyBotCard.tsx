import { useState } from 'react';
import { Play, Pause, Settings, MoreHorizontal } from 'lucide-react';
import type { MyBot } from '../ui-types';
import { STRAT_META } from '../strategyMeta';
import { StatusPill } from './StatusPill';
import { Sparkline } from './Sparkline';

type Props = {
  bot: MyBot;
  /** Сразу обновляем UI оптимистично + триггерим mutation */
  onToggle: (next: 'running' | 'paused') => void;
  onEdit:  () => void;
  onMore:  () => void;
};

/** Карточка бота пользователя — в секции «Мои боты» */
export function MyBotCard({ bot, onToggle, onEdit, onMore }: Props) {
  const [optimisticRunning, setOptimisticRunning] = useState(bot.status === 'running');
  const running = optimisticRunning;
  const profitable = bot.pnlMonth >= 0;
  const m = STRAT_META[bot.strategy];
  const Icon = m.icon;

  const flip = () => {
    const next = running ? 'paused' : 'running';
    setOptimisticRunning(next === 'running');
    onToggle(next);
  };

  return (
    <div
      className={
        'relative overflow-hidden rounded-[14px] border p-3.5 ' +
        (running
          ? 'border-[#5b8cff]/25 bg-[linear-gradient(180deg,rgba(91,140,255,.05)_0%,rgba(123,91,255,.03)_100%)] shadow-[0_12px_28px_-16px_rgba(91,140,255,.35)]'
          : 'border-white/[.06] bg-white/[.02]')
      }
    >
      {/* head */}
      <div className="mb-3 flex items-start gap-2.5">
        <div
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[9px] border"
          style={{ background: m.bg, borderColor: m.border, color: m.color }}
        >
          <Icon size={16} strokeWidth={2} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">{bot.name}</span>
            {bot.custom && (
              <span className="rounded-[3px] border border-[#c14dff]/30 bg-[#c14dff]/[.16] px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-[#d8a4ff]">
                Custom
              </span>
            )}
          </div>
          <div className="mt-0.5 flex items-center gap-1.5 font-mono text-[11px] text-slate-400">
            <span className="font-semibold text-slate-200">{bot.pair}</span>
            <span>·</span><span>{bot.exchange}</span>
            <span>·</span><span>×{bot.lev} {bot.mode}</span>
          </div>
        </div>
        <StatusPill status={running ? 'running' : 'paused'} />
      </div>

      {/* metrics + spark */}
      <div className="mb-3 grid grid-cols-[1fr_auto] items-center gap-3.5">
        <div className="flex flex-col gap-1">
          <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400">P&L · 30 дней</div>
          <div className={'font-display text-2xl font-bold tracking-tight ' + (profitable ? 'text-emerald-300' : 'text-rose-300')}>
            {profitable ? '+' : ''}{bot.pnlMonth.toFixed(2)}%
          </div>
          <div className="font-mono text-[11px] text-slate-400">
            ${(bot.balance - bot.capital).toFixed(2)} <span className="text-slate-500">· баланс</span>{' '}
            <span className="font-semibold text-slate-200">${bot.balance.toFixed(2)}</span>
          </div>
        </div>
        <Sparkline
          data={[20, 22, 21, 24, 23, 26, 28, 27, 30, 29, 32, 31, 34, 36, 35, 38, 40, 42, 44]}
          color={profitable ? '#5be0a0' : '#fca5a5'}
          width={140}
          height={48}
        />
      </div>

      {/* row stats */}
      <div className="mb-3 grid grid-cols-3 border-y border-white/[.05] py-2">
        <Stat label="Сделок"   value={String(bot.trades)} />
        <Stat
          label="Win-rate"
          value={`${bot.winRate}%`}
          color={bot.winRate >= 70 ? 'text-emerald-300' : bot.winRate >= 50 ? 'text-slate-200' : 'text-rose-300'}
        />
        <Stat label="Капитал"  value={`$${bot.capital}`} />
      </div>

      {/* actions */}
      <div className="flex gap-1.5">
        <button
          type="button"
          onClick={flip}
          className={
            'inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3.5 py-2 text-xs font-semibold ' +
            (running
              ? 'border-rose-400/28 bg-rose-400/[.10] text-rose-300 hover:bg-rose-400/[.18]'
              : 'border-emerald-400/30 bg-emerald-400/[.12] text-emerald-300 hover:bg-emerald-400/[.18]')
          }
        >
          {running ? <><Pause size={12} />Остановить</> : <><Play size={11} fill="currentColor" />Запустить</>}
        </button>
        <button
          type="button"
          onClick={onEdit}
          className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
        >
          <Settings size={13} />
        </button>
        <button
          type="button"
          onClick={onMore}
          className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
        >
          <MoreHorizontal size={14} />
        </button>
      </div>
    </div>
  );
}

function Stat({ label, value, color = 'text-slate-50' }: { label: string; value: string; color?: string }) {
  return (
    <div>
      <div className="mb-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`font-mono text-[13px] font-semibold ${color}`}>{value}</div>
    </div>
  );
}
