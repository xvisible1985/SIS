import { Plus, Download, Bot as BotIcon } from 'lucide-react';
import type { MyBot, RunStatus } from '../ui-types';
import { MyBotCard } from '../components/MyBotCard';

type Props = {
  bots: MyBot[];
  onCreate:     () => void;
  onExport:     () => void;
  onToggle:     (botId: string, next: 'running' | 'paused') => void;
  onEdit:       (botId: string) => void;
  onMore:       (botId: string) => void;
};

/** Секция «Мои боты» — заголовок + сетка карточек или empty state */
export function MyBotsSection({ bots, onCreate, onExport, onToggle, onEdit, onMore }: Props) {
  return (
    <section className="mx-auto max-w-[1400px] px-8 pt-6">
      <div className="mb-3.5 flex items-baseline gap-3 border-b border-white/[.06] pb-3.5">
        <h2 className="m-0 font-display text-lg font-bold tracking-tight text-slate-50">Мои боты</h2>
        <span className="font-mono text-[11px] text-slate-400">{bots.length}</span>
        <div className="flex-1" />
        <button
          type="button"
          onClick={onExport}
          className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
        >
          <Download size={12} />Экспорт
        </button>
        <button
          type="button"
          onClick={onCreate}
          className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
        >
          <Plus size={12} strokeWidth={2.4} />Создать своего бота
        </button>
      </div>

      {bots.length === 0 ? (
        <EmptyState onCreate={onCreate} />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-3.5">
          {bots.map((b) => (
            <MyBotCard
              key={b.id}
              bot={b}
              onToggle={(next: RunStatus | 'paused') => onToggle(b.id, next as 'running' | 'paused')}
              onEdit={() => onEdit(b.id)}
              onMore={() => onMore(b.id)}
            />
          ))}
        </div>
      )}
    </section>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="rounded-[14px] border border-dashed border-white/10 bg-white/[.02] px-6 py-10 text-center">
      <div className="mx-auto mb-3.5 flex h-12 w-12 items-center justify-center rounded-xl border border-[#5b8cff]/25 bg-[#5b8cff]/[.12] text-[#b8c8ff]">
        <BotIcon size={22} />
      </div>
      <div className="mb-1 text-[15px] font-semibold text-slate-50">У тебя ещё нет ботов</div>
      <div className="mb-4 text-[13px] text-slate-400">
        Запусти готового из библиотеки ниже или создай своего с нуля
      </div>
      <button
        type="button"
        onClick={onCreate}
        className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3.5 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
      >
        <Plus size={12} strokeWidth={2.4} />Создать бота
      </button>
    </div>
  );
}
