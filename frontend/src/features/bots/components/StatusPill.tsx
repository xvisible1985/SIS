import type { RunStatus } from '../ui-types';

const MAP: Record<RunStatus, { c: string; bg: string; bd: string; label: string; pulse: boolean }> = {
  running: { c: '#5be0a0', bg: 'rgba(65,210,139,.14)',  bd: 'rgba(65,210,139,.32)', label: 'Запущен',     pulse: true  },
  paused:  { c: '#f7a600', bg: 'rgba(247,166,0,.14)',   bd: 'rgba(247,166,0,.30)',  label: 'Пауза',       pulse: false },
  stopped: { c: '#9aa6c8', bg: 'rgba(255,255,255,.06)', bd: 'rgba(255,255,255,.10)', label: 'Остановлен', pulse: false },
};

export function StatusPill({ status }: { status: RunStatus }) {
  const s = MAP[status];
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 pl-1.5 text-[10px] font-bold uppercase tracking-wider"
      style={{ background: s.bg, borderColor: s.bd, color: s.c }}
    >
      <span
        className={'h-1.5 w-1.5 rounded-full ' + (s.pulse ? 'animate-pulse' : '')}
        style={{ background: s.c, boxShadow: s.pulse ? `0 0 6px ${s.c}` : 'none' }}
      />
      {s.label}
    </span>
  );
}
