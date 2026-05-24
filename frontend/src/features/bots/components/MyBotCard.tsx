import { useState } from 'react';
import { Play, Pause, Settings, Trash2 } from 'lucide-react';
import type { MyBot } from '../ui-types';
import { STRAT_META } from '../strategyMeta';
import { StatusPill } from './StatusPill';

type Props = {
  bot: MyBot;
  /** Сразу обновляем UI оптимистично + триггерим mutation */
  onToggle: (next: 'running' | 'paused') => void;
  onEdit:  () => void;
  onDelete: () => void;
};

/** Карточка бота пользователя — в секции «Мои боты» */
export function MyBotCard({ bot, onToggle, onEdit, onDelete }: Props) {
  const [optimisticRunning, setOptimisticRunning] = useState(bot.status === 'running');
  const running = optimisticRunning;
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
        <div className="relative h-9 w-9 shrink-0">
          {running && (
            <span
              aria-hidden
              className="pointer-events-none absolute inset-[-1.5px] overflow-hidden rounded-[11px]"
            >
              <span
                className="absolute animate-spin"
                style={{
                  width: '200%',
                  height: '200%',
                  top: '-50%',
                  left: '-50%',
                  background: 'conic-gradient(from 0deg, transparent 0deg, rgba(91,224,160,0.9) 50deg, transparent 100deg)',
                  animationDuration: '2.5s',
                }}
              />
            </span>
          )}
          <div
            className="h-9 w-9 overflow-hidden rounded-[9px] border"
            style={{ background: m.bg, borderColor: m.border, color: m.color }}
          >
            {bot.avatarUrl
              ? <img src={bot.avatarUrl} alt="" className="h-full w-full object-cover" />
              : <div className="flex h-full w-full items-center justify-center"><Icon size={16} strokeWidth={2} /></div>
            }
          </div>
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

      {/* description */}
      <div className="mb-3 min-h-[44px]">
        {bot.description
          ? <p className="text-[12px] leading-relaxed text-slate-400 line-clamp-3">{bot.description}</p>
          : <p className="text-[12px] italic text-slate-600">Описание не указано</p>
        }
      </div>

      {/* row stats */}
      <div className="mb-3 grid grid-cols-3 border-y border-white/[.05] py-2">
        <Stat label="Монет"    value={bot.symbolsTotal} />
        <Stat label="Сигнал"   value={bot.symbolsWithSignal} />
        <Stat label="Лимит"    value={bot.symbolsLimit} />
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
          onClick={() => {
            if (confirm(`Удалить бота «${bot.name}»?`)) onDelete();
          }}
          className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-rose-300 hover:bg-rose-400/[.12] hover:text-rose-200"
        >
          <Trash2 size={14} />
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
