import { useState, useEffect } from 'react';
import { X, Rocket, Users } from 'lucide-react';
import type { Bot } from '../types';

type Props = {
  bot: Bot;
  onDeploy: (whitelist: string[], blacklist: string[]) => void;
  onClose: () => void;
};

export function DeployModal({ bot, onDeploy, onClose }: Props) {
  const [whitelist, setWhitelist] = useState<string[]>([...bot.symbolWhitelist]);
  const [blacklist, setBlacklist] = useState<string[]>([...bot.symbolBlacklist]);

  useEffect(() => {
    const fn = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', fn);
    return () => window.removeEventListener('keydown', fn);
  }, [onClose]);

  const handleDeploy = () => {
    onDeploy(whitelist, blacklist);
    onClose();
  };

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-[100] flex items-center justify-center overflow-auto bg-[rgba(8,11,18,.78)] p-6 backdrop-blur"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-[520px] overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]"
      >
        {/* header */}
        <div className="flex items-center gap-3 border-b border-white/[.06] px-5 py-4">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[9px] border border-emerald-400/30 bg-emerald-400/[.12] text-emerald-300">
            <Rocket size={16} strokeWidth={2} />
          </div>
          <div className="min-w-0 flex-1">
            <h2 className="font-display text-[16px] font-bold tracking-tight text-slate-50">
              Подписаться на бота
            </h2>
            <p className="text-[11px] text-slate-400">Создаёт копию в «Мои боты»</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-[30px] w-[30px] items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-300 hover:bg-white/[.08]"
          >
            <X size={14} />
          </button>
        </div>

        {/* bot info */}
        <div className="mx-5 mt-4 rounded-[10px] border border-white/[.06] bg-white/[.025] p-4">
          <div className="mb-1 font-display text-[15px] font-bold tracking-tight text-slate-50">
            {bot.name}
          </div>
          {bot.description && (
            <p className="mb-2.5 text-[12px] leading-relaxed text-slate-400">{bot.description}</p>
          )}
          <div className="flex items-center gap-3 text-[11px] text-slate-400">
            <span>
              автор: <span className="font-semibold text-slate-200">{bot.ownerName}</span>
            </span>
            <span>·</span>
            <span className="inline-flex items-center gap-1">
              <Users size={11} />
              {bot.deployCount} подписчиков
            </span>
            {bot.strategyConfig.symbol && (
              <>
                <span>·</span>
                <span className="font-mono font-semibold text-slate-200">{bot.strategyConfig.symbol}</span>
              </>
            )}
          </div>
        </div>

        {/* strategy summary */}
        {(bot.strategyConfig.grid_size_usdt || bot.strategyConfig.tp_pct || bot.strategyConfig.sl_pct) && (
          <div className="mx-5 mt-3 grid grid-cols-3 gap-2">
            {bot.strategyConfig.grid_size_usdt && (
              <StatChip label="Капитал" value={`$${bot.strategyConfig.grid_size_usdt}`} />
            )}
            {bot.strategyConfig.tp_pct && (
              <StatChip label="TP" value={`${bot.strategyConfig.tp_pct}%`} color="text-emerald-300" />
            )}
            {bot.strategyConfig.sl_pct && (
              <StatChip label="SL" value={`${bot.strategyConfig.sl_pct}%`} color="text-rose-300" />
            )}
          </div>
        )}

        {/* symbol customization */}
        <div className="mx-5 mt-4 flex flex-col gap-3">
          <div className="text-[11px] font-bold uppercase tracking-wider text-slate-400">
            Настройка символов
          </div>
          <div>
            <div className="mb-1 text-[11px] text-slate-500">
              Whitelist — торговать только этими символами (пусто = без ограничений)
            </div>
            <TagInput
              tags={whitelist}
              setTags={setWhitelist}
              placeholder="BTCUSDT — Enter"
              color="blue"
            />
          </div>
          <div>
            <div className="mb-1 text-[11px] text-slate-500">
              Blacklist — исключить эти символы
            </div>
            <TagInput
              tags={blacklist}
              setTags={setBlacklist}
              placeholder="DOGEUSDT — Enter"
              color="red"
            />
          </div>
        </div>

        {/* footer */}
        <div className="flex items-center justify-end gap-2 border-t border-white/[.06] px-5 py-3.5 mt-5">
          <button
            type="button"
            onClick={onClose}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-4 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
          >
            Отмена
          </button>
          <button
            type="button"
            onClick={handleDeploy}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#22c97a,#17a866)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(34,201,122,.5)]"
          >
            <Rocket size={12} />
            Подписаться
          </button>
        </div>
      </div>
    </div>
  );
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function StatChip({ label, value, color = 'text-slate-200' }: { label: string; value: string; color?: string }) {
  return (
    <div className="rounded-[8px] border border-white/[.06] bg-white/[.025] px-3 py-2">
      <div className="text-[9px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-0.5 font-display text-[15px] font-bold tracking-tight ${color}`}>{value}</div>
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
