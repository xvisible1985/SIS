import { useState, useRef, useEffect } from 'react';
import { createPortal } from 'react-dom';
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
  onCloneTpl: (tplId: string, sourceRect: DOMRect) => void;
};

type FlyState = { sourceRect: DOMRect };

function FlyingCard({
  sourceRect,
  targetRef,
  onDone,
}: {
  sourceRect: DOMRect;
  targetRef: React.RefObject<HTMLElement | null>;
  onDone: () => void;
}) {
  const [flying, setFlying] = useState(false);
  const doneRef = useRef(false);

  useEffect(() => {
    // два RAF чтобы браузер успел нарисовать начальное положение
    const id = requestAnimationFrame(() => {
      requestAnimationFrame(() => setFlying(true));
    });
    return () => cancelAnimationFrame(id);
  }, []);

  const tgt = targetRef.current?.getBoundingClientRect() ?? new DOMRect(0, 0, 60, 60);

  function handleTransitionEnd() {
    if (!doneRef.current) {
      doneRef.current = true;
      onDone();
    }
  }

  return createPortal(
    <div
      style={{
        position:  'fixed',
        left:      flying ? tgt.left + 10 : sourceRect.left,
        top:       flying ? tgt.top  + 6  : sourceRect.top,
        width:     flying ? 60 : sourceRect.width,
        height:    flying ? 60 : sourceRect.height,
        opacity:   flying ? 0  : 0.88,
        transform: flying ? 'scale(0.32) rotate(-5deg)' : 'scale(1) rotate(0deg)',
        transition: flying
          ? [
              'left   0.54s cubic-bezier(0.4,0,0.2,1)',
              'top    0.54s cubic-bezier(0.4,0,0.2,1)',
              'width  0.54s cubic-bezier(0.4,0,0.2,1)',
              'height 0.54s cubic-bezier(0.4,0,0.2,1)',
              'opacity   0.32s ease-in 0.22s',
              'transform 0.54s cubic-bezier(0.4,0,0.2,1)',
            ].join(', ')
          : 'none',
        pointerEvents: 'none',
        zIndex:        9999,
        borderRadius:  14,
        background:    'linear-gradient(135deg,rgba(91,140,255,.26),rgba(74,125,255,.12))',
        border:        '1.5px solid rgba(91,140,255,.48)',
        backdropFilter: 'blur(12px)',
        boxShadow:     '0 8px 32px -8px rgba(74,125,255,.42),0 2px 8px rgba(0,0,0,.28)',
      }}
      onTransitionEnd={handleTransitionEnd}
    />,
    document.body,
  );
}

export function BotsPage({
  myBots, featured,
  onCreateBot, onExportBots, onToggleBot, onEditBot, onDeleteBot,
  onRequestApproval,
  onCloneTpl,
}: Props) {
  const runningCount = myBots.filter((b) => b.status === 'running').length;
  const myBotsSectionRef = useRef<HTMLElement | null>(null);
  const [flyState, setFlyState] = useState<FlyState | null>(null);

  function handleClone(tplId: string, sourceRect: DOMRect) {
    onCloneTpl(tplId, sourceRect);
    // анимируем только если у нас есть реальный rect (не из модалки)
    if (sourceRect.width > 0) {
      setFlyState({ sourceRect });
    }
  }

  return (
    <div className="min-h-screen bg-[#0a0d14] font-sans text-slate-200">
      {flyState && (
        <FlyingCard
          sourceRect={flyState.sourceRect}
          targetRef={myBotsSectionRef}
          onDone={() => setFlyState(null)}
        />
      )}

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
        ref={myBotsSectionRef}
        bots={myBots}
        onCreate={onCreateBot}
        onExport={onExportBots}
        onToggle={onToggleBot}
        onEdit={onEditBot}
        onDelete={onDeleteBot}
        onRequestApproval={onRequestApproval}
        onDropBot={(botId) => onCloneTpl(botId, new DOMRect())}
      />

      <LibrarySection
        bots={featured}
        ownedTplIds={new Set(myBots.map((b) => b.tplId).filter(Boolean) as string[])}
        onClone={handleClone}
      />
    </div>
  );
}
