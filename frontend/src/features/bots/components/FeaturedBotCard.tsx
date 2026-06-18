import { useState } from 'react';
import { Check, Flame, Users, Copy, TrendingUp, Search, Shield, Layers, RotateCcw, BookOpen, CheckCircle2 } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import type { FeaturedBot } from '../ui-types';
import type { BotKind } from '../types';
import { getBotKindMeta } from '../botKindMeta';
import { PriceBadge } from './PriceBadge';
import { Sparkline } from './Sparkline';

const KIND_ICONS: Record<BotKind, LucideIcon> = {
  signal: TrendingUp,
  parser: Search,
  hedge:  Shield,
  matrix: Layers,
};

type Props = {
  bot: FeaturedBot;
  alreadyOwned?: boolean;
  onOpen: () => void;
  onAdd: () => void;
};

/** Карточка готового бота с 3D-флипом при клике (если есть fullDescription) */
export function FeaturedBotCard({ bot, alreadyOwned = false, onOpen, onAdd }: Props) {
  const [flipped, setFlipped] = useState(false);

  const km    = getBotKindMeta(bot.botKind);
  const Icon: LucideIcon = KIND_ICONS[bot.botKind ?? 'signal'];

  const headerBg   = km.bgHeader;
  const iconBg     = km.iconBg;
  const iconBorder = km.border;
  const iconColor  = km.color;

  const hasDesc = !!bot.fullDescription;

  function handleCardClick() {
    if (hasDesc) setFlipped(true);
    else onOpen();
  }

  return (
    <div className="relative h-full" style={{ perspective: '1200px' }}>

      {/* flip container */}
      <div
        className={`relative h-full w-full transition-transform duration-700 ease-[cubic-bezier(.4,0,.2,1)] [transform-style:preserve-3d] ${flipped ? '[transform:rotateY(180deg)]' : ''}`}
      >

        {/* ── FRONT ──────────────────────────────────────────────────────── */}
        <div className="[backface-visibility:hidden] h-full w-full">
          <div
            role="button"
            tabIndex={0}
            onClick={handleCardClick}
            onKeyDown={(e) => e.key === 'Enter' && handleCardClick()}
            className={`relative flex h-full flex-col overflow-hidden rounded-[14px] border bg-[rgba(255,255,255,.04)] text-left transition-colors hover:brightness-110 ${hasDesc ? 'cursor-pointer' : ''}`}
            style={{ borderColor: iconBorder }}
          >

            {/* ── Шапка: 2 строки ────────────────────────────────────────── */}
            <div className="relative px-4 pt-3.5 pb-3" style={{ background: headerBg }}>

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

              <div className="flex items-center gap-3">

                {/* Иконка */}
                <div
                  className="flex h-[38px] w-[38px] shrink-0 items-center justify-center rounded-[10px] border"
                  style={{ background: iconBg, borderColor: iconBorder, color: iconColor }}
                >
                  <Icon size={17} strokeWidth={2} />
                </div>

                {/* Правая колонка: 2 строки, высота = иконке */}
                <div className="flex min-w-0 flex-1 flex-col justify-center gap-0.5 pr-20">

                  {/* Строка 1: название + verified */}
                  <div className="flex items-center gap-1.5">
                    <span className="truncate font-display text-[15px] font-bold tracking-tight text-slate-50">
                      {bot.name}
                    </span>
                    {bot.verified && (
                      <span
                        title="Verified by NovaBot"
                        className="inline-flex h-[14px] w-[14px] shrink-0 items-center justify-center rounded-full bg-[#5b8cff] text-white"
                      >
                        <Check size={10} strokeWidth={3} />
                      </span>
                    )}
                  </div>

                  {/* Строка 2: тип бота + автор */}
                  <div className="flex items-center gap-1.5">
                    <span
                      className="rounded-[4px] px-1.5 py-px font-semibold"
                      style={{ background: km.iconBg, color: km.color, border: `1px solid ${km.border}`, fontSize: 10, lineHeight: '1.5' }}
                    >
                      {km.label}
                    </span>
                    <span className="text-slate-600">·</span>
                    <span className="text-[11px] text-slate-400">
                      от <span className="font-semibold text-slate-300">{bot.author}</span>
                    </span>
                  </div>
                </div>
              </div>
            </div>

            {/* ── Тело карточки ──────────────────────────────────────────── */}
            <div className="flex flex-1 flex-col bg-[linear-gradient(180deg,rgba(255,255,255,.018)_0%,rgba(255,255,255,.006)_100%)] px-4 pb-4 pt-3">

              <p className="mb-3 line-clamp-3 h-[54px] overflow-hidden text-xs leading-[1.5] text-slate-300">{bot.desc}</p>

              {/* spark — фиксированная высота, всегда рендерится */}
              <div className="mb-3 h-[52px] overflow-hidden rounded-[10px] bg-black/20">
                <Sparkline data={bot.spark} color="#5be0a0" width={280} height={52} />
              </div>

              {/* footer */}
              <div className="mt-auto flex items-center gap-2 border-t border-white/[.05] pt-3">
                <div className="flex items-center gap-1.5 text-[11px] text-slate-400">
                  <Users size={12} />
                  <span className="font-semibold text-slate-200">{bot.users.toLocaleString('ru-RU')}</span>
                  <span className="text-slate-600">активных</span>
                </div>
                {hasDesc && (
                  <div className="ml-1 flex items-center gap-1 text-[10px] text-slate-600">
                    <BookOpen size={9} />
                    <span>описание</span>
                  </div>
                )}
                <div className="flex-1" />
                {alreadyOwned ? (
                  <span className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-500/25 bg-emerald-500/[.08] px-3 py-2 text-xs font-semibold text-emerald-400/70 cursor-default">
                    <CheckCircle2 size={11} strokeWidth={2} />
                    Уже в ваших ботах
                  </span>
                ) : (
                  <span
                    onClick={(e) => { e.stopPropagation(); onAdd(); }}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-[#5b8cff]/35 bg-[#5b8cff]/[.12] px-3 py-2 text-xs font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.20] transition-colors cursor-pointer"
                  >
                    <Copy size={11} strokeWidth={2.2} />
                    В свои боты
                  </span>
                )}
              </div>
            </div>
          </div>
        </div>

        {/* ── BACK ───────────────────────────────────────────────────────── */}
        <div className="absolute inset-0 [backface-visibility:hidden] [transform:rotateY(180deg)]">
          <div
            className="flex h-full flex-col overflow-hidden rounded-[14px] border"
            style={{ borderColor: iconBorder }}
          >
            <div className="flex items-center gap-2.5 px-4 py-3" style={{ background: headerBg }}>
              <div
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg border"
                style={{ background: iconBg, borderColor: iconBorder, color: iconColor }}
              >
                <Icon size={13} strokeWidth={2} />
              </div>
              <span className="flex-1 truncate font-display text-sm font-bold text-slate-50">{bot.name}</span>
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); setFlipped(false); }}
                title="Назад"
                className="flex h-6 w-6 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-400 hover:bg-white/[.08] hover:text-slate-200 transition-colors"
              >
                <RotateCcw size={11} />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto bg-[linear-gradient(180deg,rgba(255,255,255,.018)_0%,rgba(255,255,255,.006)_100%)] px-4 py-3.5">
              <p className="whitespace-pre-wrap text-[13px] leading-[1.7] text-slate-300">
                {bot.fullDescription}
              </p>
            </div>

            <div className="flex items-center gap-2 border-t border-white/[.05] bg-[rgba(255,255,255,.01)] px-4 py-3">
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onOpen(); }}
                className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06] transition-colors"
              >
                Подробнее
              </button>
              <div className="flex-1" />
              {alreadyOwned ? (
                <span className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-500/25 bg-emerald-500/[.08] px-3 py-2 text-xs font-semibold text-emerald-400/70 cursor-default">
                  <CheckCircle2 size={11} strokeWidth={2} />
                  Уже в ваших ботах
                </span>
              ) : (
                <button
                  type="button"
                  onClick={(e) => { e.stopPropagation(); onAdd(); }}
                  className="inline-flex items-center gap-1.5 rounded-lg border border-[#5b8cff]/35 bg-[#5b8cff]/[.12] px-3 py-2 text-xs font-semibold text-[#a0b8ff] hover:bg-[#5b8cff]/[.20] transition-colors"
                >
                  <Copy size={11} strokeWidth={2.2} />
                  В свои боты
                </button>
              )}
            </div>
          </div>
        </div>

      </div>
    </div>
  );
}
