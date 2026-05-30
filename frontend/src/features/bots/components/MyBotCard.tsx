import { useState } from 'react';
import { Play, Pause, Settings, Trash2, TrendingUp, Search, Shield } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { MyBot } from '../ui-types';
import type { BotKind } from '../types';
import { getBotKindMeta, BOT_KIND_META } from '../botKindMeta';
import { StatusPill } from './StatusPill';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
}

type Props = {
  bot: MyBot;
  onToggle: (next: 'running' | 'paused') => void;
  onEdit:  () => void;
  onDelete: () => void;
};

/** Карточка бота пользователя — в секции «Мои боты» */
export function MyBotCard({ bot, onToggle, onEdit, onDelete }: Props) {
  const [optimisticRunning, setOptimisticRunning] = useState(bot.status === 'running');
  const running = optimisticRunning;

  const km   = getBotKindMeta(bot.botKind)
  const Icon = bot.botKind ? KIND_ICONS[bot.botKind] : KIND_ICONS.signal

  const flip = () => {
    const next = running ? 'paused' : 'running';
    setOptimisticRunning(next === 'running');
    onToggle(next);
  };

  return (
    <div
      className="relative overflow-hidden rounded-[14px] border"
      style={{
        borderColor: running ? km.border : 'rgba(255,255,255,0.06)',
        background:  'rgba(255,255,255,0.015)',
        boxShadow:   running ? `0 12px 28px -16px ${km.border}` : 'none',
      }}
    >
      {/* ── цветная шапка ────────────────────────────────────────────── */}
      <div
        className="px-3.5 pt-3.5 pb-3"
        style={{ background: km.bgHeader }}
      >
        <div className="flex items-start gap-2.5">
          {/* аватар / иконка */}
          <div className="relative h-9 w-9 shrink-0">
            {running && (
              <span
                aria-hidden
                className="pointer-events-none absolute inset-[-1.5px] overflow-hidden rounded-[11px]"
              >
                <span
                  className="absolute animate-spin"
                  style={{
                    width: '200%', height: '200%', top: '-50%', left: '-50%',
                    background: `conic-gradient(from 0deg, transparent 0deg, ${km.color}cc 50deg, transparent 100deg)`,
                    animationDuration: '2.5s',
                  }}
                />
              </span>
            )}
            <div
              className="h-9 w-9 overflow-hidden rounded-[9px] border flex items-center justify-center"
              style={{
                background:  km.iconBg,
                borderColor: km.border,
                color:       km.color,
              }}
            >
              {bot.avatarUrl
                ? <img src={bot.avatarUrl} alt="" className="h-full w-full object-cover" />
                : <Icon size={16} strokeWidth={2} />
              }
            </div>
          </div>

          {/* имя + тип бота */}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">
                {bot.name}
              </span>
              {bot.custom && (
                <span className="rounded-[3px] border border-[#c14dff]/30 bg-[#c14dff]/[.16] px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-[#d8a4ff]">
                  Custom
                </span>
              )}
            </div>
            {/* тип бота вместо "bybit · ×1 futures" */}
            <div className="mt-0.5 flex items-center gap-1.5 text-[11px]">
              <span
                className="rounded-[4px] px-1.5 py-px font-semibold"
                style={{
                  background:  km.iconBg,
                  color:       km.color,
                  border:      `1px solid ${km.border}`,
                  fontSize:    10,
                  lineHeight:  '1.5',
                }}
              >
                {km.label}
              </span>
              {bot.pair && (
                <>
                  <span className="text-slate-600">·</span>
                  <span className="font-mono font-semibold text-slate-400">{bot.pair}</span>
                </>
              )}
            </div>
          </div>

          <StatusPill status={running ? 'running' : 'paused'} />
        </div>
      </div>

      {/* ── тело карточки ────────────────────────────────────────────── */}
      <div className="px-3.5 pb-3.5">
        {/* description */}
        <div className="mb-3 mt-2.5 min-h-[44px]">
          {bot.description
            ? <p className="text-[12px] leading-relaxed text-slate-400 line-clamp-3">{bot.description}</p>
            : <p className="text-[12px] italic text-slate-600">Описание не указано</p>
          }
        </div>

        {/* row stats */}
        <div className="mb-3 grid grid-cols-3 border-y border-white/[.05] py-2">
          <Stat label="Монет"  value={bot.symbolsTotal} />
          <Stat label="Сигнал" value={bot.symbolsWithSignal} />
          <Stat label="Лимит"  value={bot.symbolsLimit} />
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
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="mb-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className="font-mono text-[13px] font-semibold text-slate-50">{value}</div>
    </div>
  );
}
