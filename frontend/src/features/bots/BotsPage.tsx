import type { FeaturedBot, MyBot } from './ui-types';
import { MyBotsSection } from './sections/MyBotsSection';
import { LibrarySection } from './sections/LibrarySection';

type Props = {
  myBots: MyBot[];
  featured: FeaturedBot[];

  /** Мои боты */
  onCreateBot:    () => void;
  onExportBots:   () => void;
  onToggleBot:    (botId: string, next: 'running' | 'paused') => void;
  onEditBot:      (botId: string) => void;
  onDeleteBot:    (botId: string) => void;
  onRequestApproval: (botId: string) => void;

  /** Библиотека */
  onLaunchTpl:    (tplId: string) => void;
  onConfigureTpl: (tplId: string) => void;
  onCloneTpl:     (tplId: string) => void;
};

export function BotsPage({
  myBots, featured,
  onCreateBot, onExportBots, onToggleBot, onEditBot, onDeleteBot,
  onRequestApproval,
  onLaunchTpl, onConfigureTpl, onCloneTpl,
}: Props) {
  const runningCount = myBots.filter((b) => b.status === 'running').length;

  return (
    <div className="min-h-screen bg-[#0a0d14] font-sans text-slate-200">
      <header className="mx-auto max-w-[1400px] px-8 pt-6">
        <div className="flex items-baseline gap-3.5">
          <h1 className="m-0 font-display text-[26px] font-bold tracking-tight text-slate-50">Боты</h1>
          <span className="text-[13px] text-slate-400">
            {runningCount > 0 && (
              <span className="mr-2 inline-flex items-center gap-1.5 rounded-full border border-emerald-400/25 bg-emerald-400/[.14] px-2.5 py-px text-[11px] font-bold text-emerald-300">
                <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-300 shadow-[0_0_6px_currentColor]" />
                {runningCount} активных
              </span>
            )}
            из {myBots.length}
          </span>
        </div>
        <p className="mt-1.5 max-w-[680px] text-[13px] leading-relaxed text-slate-400">
          Запускай готовые торговые стратегии или создавай свои. Все боты работают на твоих API-ключах,
          под контролем NovaBot.
        </p>
      </header>

      <MyBotsSection
        bots={myBots}
        onCreate={onCreateBot}
        onExport={onExportBots}
        onToggle={onToggleBot}
        onEdit={onEditBot}
        onDelete={onDeleteBot}
        onRequestApproval={onRequestApproval}
      />

      <LibrarySection
        bots={featured}
        onLaunch={onLaunchTpl}
        onConfigure={onConfigureTpl}
        onClone={onCloneTpl}
      />
    </div>
  );
}
