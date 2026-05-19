import type { UserStatus } from '../types';

type Props = { status: UserStatus };

const MAP: Record<UserStatus, { cls: string; dot: string; label: string; pulse: boolean }> = {
  active:  { cls: 'bg-emerald-400/[.14]  border-emerald-400/30  text-emerald-300', dot: 'bg-emerald-300 shadow-[0_0_6px_currentColor]', label: 'Активен',       pulse: true  },
  pending: { cls: 'bg-amber-500/[.14]    border-amber-500/30    text-amber-400',   dot: 'bg-amber-400',   label: 'Ожидает',       pulse: false },
  blocked: { cls: 'bg-rose-400/[.14]     border-rose-400/30     text-rose-300',    dot: 'bg-rose-300',    label: 'Заблокирован', pulse: false },
};

export function StatusPill({ status }: Props) {
  const s = MAP[status];
  return (
    <span
      className={
        'inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 pl-1.5 text-[10px] font-bold uppercase tracking-wider ' +
        s.cls
      }
    >
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot} ${s.pulse ? 'animate-pulse' : ''}`} />
      {s.label}
    </span>
  );
}
