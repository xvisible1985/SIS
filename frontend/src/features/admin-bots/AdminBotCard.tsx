import { Check, Bot, Users, Pencil, Trash2, Eye, EyeOff } from 'lucide-react';
import { STRAT_META } from '../bots/strategyMeta';
import { StrategyChip } from '../bots/components/StrategyChip';
import type { Bot as BotType } from '../bots/types';

type Props = {
  bot: BotType;
  onEdit?: () => void;
  onTogglePublic?: () => void;
  onDelete?: () => void;
};

export function AdminBotCard({ bot, onEdit, onTogglePublic, onDelete }: Props) {
  const strat = (bot.strategyConfig?.strategy_type ?? 'grid') as keyof typeof STRAT_META;
  const m = STRAT_META[strat] ?? STRAT_META.grid;
  const Icon = m.icon;
  const dir = bot.strategyConfig?.direction ?? 'long';
  const lev = bot.strategyConfig?.leverage ?? 5;
  const margin = bot.strategyConfig?.margin_type ?? 'isolated';

  const dirColor =
    dir === 'long' ? 'text-emerald-300' :
    dir === 'short' ? 'text-rose-300' :
    'text-blue-300';
  const dirBg =
    dir === 'long' ? 'bg-emerald-400/10 border-emerald-400/25' :
    dir === 'short' ? 'bg-rose-400/10 border-rose-400/25' :
    'bg-blue-400/10 border-blue-400/25';

  return (
    <div className="relative flex flex-col overflow-hidden rounded-[14px] border border-white/[.06] bg-[linear-gradient(180deg,rgba(255,255,255,.025)_0%,rgba(255,255,255,.01)_100%)] p-4 text-left transition-colors hover:border-white/15">
      {/* corner: status + NOVABOT */}
      <div className="absolute right-3 top-3 flex items-center gap-1.5">
        {bot.isOfficial && (
          <span className="inline-flex items-center gap-1 rounded-full border border-[#5b8cff]/25 bg-[#5b8cff]/15 px-1.5 py-0.5 text-[9px] font-extrabold uppercase tracking-wider text-[#7ba4ff]">
            <Bot size={9} strokeWidth={2.4} />
            NOVABOT
          </span>
        )}
        <span className={`rounded-full border px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider ${
          bot.status === 'active'
            ? 'border-emerald-400/25 bg-emerald-400/[.10] text-emerald-300'
            : bot.status === 'stopped'
            ? 'border-rose-400/25 bg-rose-400/[.10] text-rose-300'
            : 'border-white/[.08] bg-white/[.05] text-slate-400'
        }`}>
          {bot.status}
        </span>
      </div>

      {/* header */}
      <div className="mb-3 flex items-start gap-3 pr-24">
        <div
          className="flex h-[42px] w-[42px] shrink-0 items-center justify-center rounded-[10px] border"
          style={{ background: m.bg, borderColor: m.border, color: m.color }}
        >
          <Icon size={18} strokeWidth={2} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">{bot.name}</span>
            {bot.isOfficial && (
              <span
                title="Verified by NovaBot"
                className="inline-flex h-[14px] w-[14px] shrink-0 items-center justify-center rounded-full bg-[#5b8cff] text-white"
              >
                <Check size={10} strokeWidth={3} />
              </span>
            )}
          </div>
          <div className="mt-0.5 text-[11px] text-slate-400">
            от <span className="font-semibold text-slate-200">{bot.ownerName}</span>
          </div>
        </div>
      </div>

      {/* chips */}
      <div className="mb-3 flex flex-wrap items-center gap-1.5">
        {strat in STRAT_META && (
          <StrategyChip strategy={strat as 'grid' | 'matrix'} size="sm" />
        )}
        <span className={`inline-flex items-center rounded-full border px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${dirBg} ${dirColor}`}>
          {dir === 'long' ? 'Long' : dir === 'short' ? 'Short' : 'Both'}
        </span>
        <span className="font-mono text-[10px] text-slate-400">
          ×{lev} {margin}
        </span>
        {!bot.isPublic && (
          <span className="inline-flex items-center rounded-full border border-slate-600/30 bg-slate-600/10 px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-slate-400">
            Приватный
          </span>
        )}
      </div>

      {/* desc */}
      <p className="mb-3.5 min-h-[36px] text-xs leading-relaxed text-slate-300">
        {bot.description || '—'}
      </p>

      {/* mini stats */}
      <div className="mb-3 grid grid-cols-3 gap-0">
        <MiniStat label="Деплоев" value={bot.deployCount.toString()} />
        <MiniStat label="Создан" value={new Date(bot.createdAt).toLocaleDateString('ru-RU')} />
        <MiniStat
          label="Тип"
          value={bot.isOfficial ? 'NovaBot' : 'Пользовательский'}
          color={bot.isOfficial ? 'text-[#7ba4ff]' : 'text-slate-200'}
        />
      </div>

      {/* footer actions */}
      <div className="mt-auto flex items-center gap-1.5 border-t border-white/[.05] pt-3">
        {bot.isOfficial && onEdit && (
          <button
            type="button"
            onClick={onEdit}
            className="flex h-7 w-7 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
            title="Редактировать"
          >
            <Pencil size={13} />
          </button>
        )}
        {onTogglePublic && (
          <button
            type="button"
            onClick={onTogglePublic}
            className={`flex h-7 w-7 items-center justify-center rounded-md border transition-colors ${
              bot.isPublic
                ? 'border-emerald-400/25 bg-emerald-400/[.10] text-emerald-300 hover:bg-emerald-400/[.18]'
                : 'border-white/[.08] bg-white/[.04] text-slate-400 hover:text-slate-200'
            }`}
            title={bot.isPublic ? 'Скрыть из библиотеки' : 'Опубликовать'}
          >
            {bot.isPublic ? <Eye size={13} /> : <EyeOff size={13} />}
          </button>
        )}
        {onDelete && (
          <button
            type="button"
            onClick={onDelete}
            className="flex h-7 w-7 items-center justify-center rounded-md border border-rose-400/20 bg-rose-400/[.08] text-rose-300 hover:bg-rose-400/[.15]"
            title="Удалить"
          >
            <Trash2 size={13} />
          </button>
        )}
      </div>
    </div>
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
