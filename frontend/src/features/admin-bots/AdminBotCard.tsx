import { TrendingUp, Search, Shield, Layers, Settings, Trash2, Eye, EyeOff, Bot, Library } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { Bot as BotType, BotKind } from '../bots/types';
import { getBotKindMeta } from '../bots/botKindMeta';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
  matrix: Layers,
};

type Props = {
  bot: BotType;
  onEdit?: () => void;
  onTogglePublic?: () => void;
  onDelete?: () => void;
  onApprove?: () => void;
  onReject?: () => void;
  onPublishToLibrary?: () => void;
};

export function AdminBotCard({ bot, onEdit, onTogglePublic, onDelete, onApprove, onReject, onPublishToLibrary }: Props) {
  const botKind = (bot.strategyConfig?.bot_kind ?? 'signal') as BotKind;
  const km      = getBotKindMeta(botKind);
  const Icon    = KIND_ICONS[botKind] ?? KIND_ICONS.signal;
  const isActive = bot.status === 'active';

  return (
    <div
      className="relative overflow-hidden rounded-[14px] border"
      style={{
        borderColor: isActive ? km.border : 'rgba(255,255,255,0.06)',
        background:  'rgba(255,255,255,0.015)',
        boxShadow:   isActive ? `0 12px 28px -16px ${km.border}` : 'none',
      }}
    >
      {/* ── цветная шапка ─────────────────────────────────────────── */}
      <div className="px-3.5 pt-3.5 pb-3" style={{ background: km.bgHeader }}>
        <div className="flex items-start gap-2.5">

          {/* иконка */}
          <div
            className="h-9 w-9 shrink-0 overflow-hidden rounded-[9px] border flex items-center justify-center"
            style={{ background: km.iconBg, borderColor: km.border, color: km.color }}
          >
            <Icon size={16} strokeWidth={2} />
          </div>

          {/* имя + бейдж + автор */}
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">
                {bot.name}
              </span>
              {bot.isOfficial && (
                <span className="inline-flex items-center gap-0.5 rounded-[3px] border border-[#5b8cff]/30 bg-[#5b8cff]/[.16] px-1.5 py-px text-[9px] font-bold uppercase tracking-wider text-[#7ba4ff]">
                  <Bot size={8} strokeWidth={2.4} />
                  NOVABOT
                </span>
              )}
            </div>
            <div className="mt-0.5 flex items-center gap-1.5">
              <span
                className="rounded-[4px] px-1.5 py-px font-semibold"
                style={{
                  background: km.iconBg,
                  color:      km.color,
                  border:     `1px solid ${km.border}`,
                  fontSize:   10,
                  lineHeight: '1.5',
                }}
              >
                {km.label}
              </span>
              <span className="text-slate-600">·</span>
              <span className="truncate text-[11px] text-slate-400">{bot.ownerName}</span>
            </div>
          </div>

          {/* статус + согласование */}
          <div className="flex flex-col items-end gap-1 shrink-0">
            <span className={`rounded-full border px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider ${
              bot.status === 'active'
                ? 'border-emerald-400/25 bg-emerald-400/[.10] text-emerald-300'
                : bot.status === 'stopped'
                ? 'border-rose-400/25 bg-rose-400/[.10] text-rose-300'
                : 'border-white/[.08] bg-white/[.05] text-slate-400'
            }`}>
              {bot.status === 'active' ? 'Активен' : bot.status === 'stopped' ? 'Стоп' : 'Черновик'}
            </span>
            {bot.approvalStatus === 'pending' && (
              <span className="rounded-full border border-amber-400/30 bg-amber-400/[.12] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-amber-300">
                Ожидает
              </span>
            )}
            {bot.approvalStatus === 'approved' && (
              <span className="rounded-full border border-emerald-400/25 bg-emerald-400/[.10] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-emerald-300">
                Одобрен
              </span>
            )}
            {bot.approvalStatus === 'rejected' && (
              <span className="rounded-full border border-rose-400/25 bg-rose-400/[.10] px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider text-rose-300">
                Отклонён
              </span>
            )}
          </div>
        </div>
      </div>

      {/* ── тело карточки ─────────────────────────────────────────── */}
      <div className="px-3.5 pb-3.5">

        {/* описание */}
        <div className="mb-3 mt-2.5 min-h-[44px]">
          {bot.description
            ? <p className="line-clamp-3 text-[12px] leading-relaxed text-slate-400">{bot.description}</p>
            : <p className="text-[12px] italic text-slate-600">Описание не указано</p>
          }
        </div>

        {/* мини-статистика */}
        <div className="mb-3 grid grid-cols-4 border-y border-white/[.05] py-2">
          <Stat label="Деплоев"   value={String(bot.deployCount)} />
          <Stat label="Тип"       value={bot.isOfficial ? 'Official' : 'Custom'} />
          <Stat label="Публичный" value={bot.isPublic ? 'Да' : 'Нет'} />
          <Stat label="Цена"      value={bot.price ? `$${bot.price}/мес` : 'Free'} />
        </div>

        {/* кнопки действий */}
        <div className="flex gap-1.5">
          {/* pending: одобрить + отклонить */}
          {onApprove && (
            <button
              type="button"
              onClick={onApprove}
              className="inline-flex flex-1 items-center justify-center gap-1 rounded-lg border border-emerald-400/30 bg-emerald-400/[.12] px-3 py-2 text-xs font-semibold text-emerald-300 hover:bg-emerald-400/[.18]"
            >
              ✓ Одобрить
            </button>
          )}
          {onReject && (
            <button
              type="button"
              onClick={onReject}
              className="inline-flex flex-1 items-center justify-center gap-1 rounded-lg border border-rose-400/28 bg-rose-400/[.10] px-3 py-2 text-xs font-semibold text-rose-300 hover:bg-rose-400/[.18]"
            >
              ✕ Отклонить
            </button>
          )}

          {/* обычные: публикация (flex-1) */}
          {!onApprove && onTogglePublic && (
            <button
              type="button"
              onClick={onTogglePublic}
              className={`inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-xs font-semibold transition-colors ${
                bot.isPublic
                  ? 'border-emerald-400/30 bg-emerald-400/[.12] text-emerald-300 hover:bg-emerald-400/[.18]'
                  : 'border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]'
              }`}
            >
              {bot.isPublic ? <><Eye size={12} />Скрыть</> : <><EyeOff size={12} />Опубликовать</>}
            </button>
          )}

          {/* опубликовать в библиотеку */}
          {onPublishToLibrary && !onApprove && (
            <button
              type="button"
              onClick={onPublishToLibrary}
              title="Опубликовать в библиотеку"
              className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-[#4a7dff]/30 bg-[#4a7dff]/[.08] text-[#7ba4ff] hover:bg-[#4a7dff]/[.16] transition-colors"
            >
              <Library size={13} />
            </button>
          )}

          {/* редактировать (квадрат) */}
          {onEdit && (
            <button
              type="button"
              onClick={onEdit}
              className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
            >
              <Settings size={13} />
            </button>
          )}

          {/* удалить (квадрат) */}
          {onDelete && (
            <button
              type="button"
              onClick={onDelete}
              className="flex h-[34px] w-[34px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-rose-300 hover:bg-rose-400/[.12] hover:text-rose-200"
            >
              <Trash2 size={14} />
            </button>
          )}
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
