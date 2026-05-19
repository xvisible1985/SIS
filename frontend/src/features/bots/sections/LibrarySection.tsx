import { useMemo, useState } from 'react';
import type { BotFilters, FeaturedBot } from '../ui-types';
import { Filters } from '../components/Filters';
import { FeaturedBotCard } from '../components/FeaturedBotCard';
import { FeaturedBotModal } from '../components/FeaturedBotModal';

type Props = {
  bots: FeaturedBot[];
  /** Callback при «Запустить с дефолтами» — обычно навигация на /bots/launch?tpl=ID */
  onLaunch: (botId: string) => void;
  /** Открыть мастер настройки */
  onConfigure: (botId: string) => void;
  /** Клонировать шаблон как кастомного бота */
  onClone: (botId: string) => void;
};

const DEFAULTS: BotFilters = {
  q: '', strategy: 'all', risk: 'all', mode: 'all',
  sort: 'popular', verified: false, pricing: 'all', view: 'grid',
};

/** Секция «Библиотека ботов» — фильтры + сетка карточек + модалка */
export function LibrarySection({ bots, onLaunch, onConfigure, onClone }: Props) {
  const [filters, setFilters] = useState<BotFilters>(DEFAULTS);
  const [openId, setOpenId] = useState<string | null>(null);

  const filtered = useMemo(() => filterAndSort(bots, filters), [bots, filters]);
  const open = openId ? bots.find((b) => b.id === openId) ?? null : null;

  return (
    <section className="mt-8">
      <div className="mx-auto max-w-[1400px] px-8">
        <div className="mb-3.5 flex items-baseline gap-3 border-b border-white/[.06] pb-3.5">
          <h2 className="m-0 font-display text-lg font-bold tracking-tight text-slate-50">Библиотека ботов</h2>
          <span className="font-mono text-[11px] text-slate-400">
            {filtered.length} из {bots.length}
          </span>
          <div className="flex-1" />
          <span className="text-[11px] text-slate-400">Готовые стратегии от NovaBot и сообщества</span>
        </div>
      </div>

      <div className="mx-auto max-w-[1400px] px-8 pb-4 pt-2">
        <Filters value={filters} onChange={setFilters} />
      </div>

      <div className="mx-auto max-w-[1400px] px-8 pb-12">
        {filtered.length === 0 ? (
          <div className="rounded-[14px] border border-dashed border-white/10 bg-white/[.02] px-6 py-12 text-center">
            <div className="mb-1 text-sm text-slate-200">Ничего не подошло</div>
            <div className="text-xs text-slate-400">Попробуй ослабить фильтры</div>
          </div>
        ) : (
          <div
            className={
              'grid gap-3.5 ' +
              (filters.view === 'grid'
                ? 'grid-cols-[repeat(auto-fill,minmax(310px,1fr))]'
                : 'grid-cols-1')
            }
          >
            {filtered.map((b) => (
              <FeaturedBotCard
                key={b.id}
                bot={b}
                onOpen={() => setOpenId(b.id)}
                onLaunch={() => onLaunch(b.id)}
              />
            ))}
          </div>
        )}
      </div>

      {open && (
        <FeaturedBotModal
          bot={open}
          onClose={() => setOpenId(null)}
          onLaunchDefault={() => { onLaunch(open.id); setOpenId(null); }}
          onConfigure={() => { onConfigure(open.id); setOpenId(null); }}
          onClone={() => { onClone(open.id); setOpenId(null); }}
        />
      )}
    </section>
  );
}

function filterAndSort(bots: FeaturedBot[], f: BotFilters): FeaturedBot[] {
  let res = bots.slice();
  if (f.strategy !== 'all') res = res.filter((b) => b.strategy === f.strategy);
  if (f.risk     !== 'all') res = res.filter((b) => b.risk === f.risk);
  if (f.mode     !== 'all') res = res.filter((b) => b.mode === f.mode);
  if (f.verified)           res = res.filter((b) => b.verified);
  if (f.pricing === 'free') res = res.filter((b) => !b.price || b.price === 0);
  if (f.pricing === 'paid') res = res.filter((b) => b.price > 0);
  if (f.q) {
    const ql = f.q.toLowerCase();
    res = res.filter((b) =>
      b.name.toLowerCase().includes(ql) ||
      b.author.toLowerCase().includes(ql) ||
      b.pairs.some((p) => p.toLowerCase().includes(ql)) ||
      b.tags.some((t) => t.toLowerCase().includes(ql)),
    );
  }
  const sorter: Record<BotFilters['sort'], (a: FeaturedBot, b: FeaturedBot) => number> = {
    popular: (a, b) => b.users - a.users,
    profit:  (a, b) => b.perfMonth - a.perfMonth,
    winrate: (a, b) => b.winRate - a.winRate,
    newest:  () => 0,
  };
  return res.sort(sorter[f.sort]);
}
