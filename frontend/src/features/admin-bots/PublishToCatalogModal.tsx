import { useState } from 'react';
import { X, Library } from 'lucide-react';
import type { Bot } from '../bots/types';

type Props = {
  bot: Bot;
  onClose: () => void;
  onPublish: (name: string, isOfficial: boolean, price: number) => Promise<void>;
};

/** Модалка «Опубликовать в библиотеку» — выбор названия, автора и цены */
export function PublishToCatalogModal({ bot, onClose, onPublish }: Props) {
  const [name, setName]           = useState(bot.name);
  const [isOfficial, setIsOfficial] = useState(true);
  const [paidMode, setPaidMode]   = useState(false);
  const [price, setPrice]         = useState('');
  const [loading, setLoading]     = useState(false);
  const [error, setError]         = useState('');

  async function handleSubmit() {
    const trimmedName = name.trim();
    if (!trimmedName) { setError('Укажите название'); return; }
    const priceVal = paidMode ? parseFloat(price) : 0;
    if (paidMode && (isNaN(priceVal) || priceVal <= 0)) {
      setError('Укажите корректную цену');
      return;
    }
    setLoading(true);
    setError('');
    try {
      await onPublish(trimmedName, isOfficial, priceVal);
      onClose();
    } catch (e: unknown) {
      const err = e as { response?: { data?: { error?: string } } };
      setError(err?.response?.data?.error ?? 'Ошибка публикации');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div
      onClick={onClose}
      className="fixed inset-0 z-[110] flex items-center justify-center bg-black/60 backdrop-blur-sm p-6"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-[480px] overflow-hidden rounded-[18px] border border-white/[.08] bg-[#0c1018] shadow-[0_32px_80px_-16px_rgba(0,0,0,.7)]"
      >
        {/* ── header ── */}
        <div className="flex items-center justify-between border-b border-white/[.06] px-5 py-4">
          <div className="flex items-center gap-2">
            <Library size={15} className="text-[#7ba4ff]" />
            <h2 className="m-0 text-sm font-semibold text-slate-100">Опубликовать в библиотеку</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="flex h-7 w-7 items-center justify-center rounded-md border border-white/[.08] bg-white/[.04] text-slate-400 hover:bg-white/[.08] hover:text-slate-200 transition-colors"
          >
            <X size={13} />
          </button>
        </div>

        {/* ── body ── */}
        <div className="flex flex-col gap-4 px-5 py-4">

          {/* название */}
          <div>
            <label className="mb-1.5 block text-[11px] font-bold uppercase tracking-wider text-slate-400">
              Название в библиотеке
            </label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full rounded-lg border border-white/[.08] bg-white/[.04] px-3 py-2 text-sm text-slate-100 outline-none placeholder:text-slate-600 focus:border-[#4a7dff]/50 focus:ring-1 focus:ring-[#4a7dff]/30 transition-colors"
              placeholder="Название бота"
            />
          </div>

          {/* автор */}
          <div>
            <label className="mb-1.5 block text-[11px] font-bold uppercase tracking-wider text-slate-400">
              Автор
            </label>
            <div className="grid grid-cols-2 gap-2">
              <ToggleBtn active={isOfficial} onClick={() => setIsOfficial(true)}>
                NovaBot
              </ToggleBtn>
              <ToggleBtn active={!isOfficial} onClick={() => setIsOfficial(false)}>
                {bot.ownerName}
              </ToggleBtn>
            </div>
            <p className="mt-1.5 text-[11px] text-slate-500">
              {isOfficial
                ? 'Бот будет помечен как официальный NovaBot (синяя галочка)'
                : `Автором будет указан ${bot.ownerName}`}
            </p>
          </div>

          {/* подписка */}
          <div>
            <label className="mb-1.5 block text-[11px] font-bold uppercase tracking-wider text-slate-400">
              Подписка
            </label>
            <div className="grid grid-cols-2 gap-2">
              <ToggleBtn active={!paidMode} onClick={() => setPaidMode(false)}>
                Бесплатно
              </ToggleBtn>
              <ToggleBtn active={paidMode} onClick={() => setPaidMode(true)}>
                Платно
              </ToggleBtn>
            </div>
            {paidMode && (
              <div className="mt-2 flex items-center gap-2">
                <input
                  type="number"
                  min={0.01}
                  step={0.01}
                  value={price}
                  onChange={(e) => setPrice(e.target.value)}
                  placeholder="0.00"
                  className="w-full rounded-lg border border-white/[.08] bg-white/[.04] px-3 py-2 text-sm text-slate-100 outline-none placeholder:text-slate-600 focus:border-[#4a7dff]/50 focus:ring-1 focus:ring-[#4a7dff]/30 transition-colors"
                />
                <span className="shrink-0 text-xs text-slate-400">USD / мес</span>
              </div>
            )}
          </div>

          {error && (
            <p className="rounded-lg border border-rose-400/25 bg-rose-400/[.10] px-3 py-2 text-xs text-rose-300">
              {error}
            </p>
          )}
        </div>

        {/* ── footer ── */}
        <div className="flex items-center justify-end gap-2 border-t border-white/[.06] px-5 py-3.5">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-white/[.08] bg-white/[.03] px-4 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06] transition-colors"
          >
            Отмена
          </button>
          <button
            type="button"
            disabled={loading}
            onClick={handleSubmit}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-4 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)] disabled:opacity-60 transition-opacity"
          >
            {loading ? 'Публикация…' : 'Опубликовать'}
          </button>
        </div>
      </div>
    </div>
  );
}

function ToggleBtn({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-lg border px-3 py-2 text-xs font-semibold transition-colors ${
        active
          ? 'border-[#4a7dff]/50 bg-[#4a7dff]/[.15] text-[#7ba4ff]'
          : 'border-white/[.08] bg-white/[.03] text-slate-400 hover:bg-white/[.06]'
      }`}
    >
      {children}
    </button>
  );
}
