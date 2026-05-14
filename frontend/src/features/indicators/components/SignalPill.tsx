import type { IndicatorSignal } from '../types';

type Props = { signal?: IndicatorSignal };

const PALETTE = {
  buy:     'bg-emerald-400/[.14] border-emerald-400/35 text-emerald-300',
  sell:    'bg-rose-400/[.14]    border-rose-400/35    text-rose-300',
  neutral: 'bg-white/[.04]       border-white/10       text-slate-400',
} as const;

export function SignalPill({ signal }: Props) {
  if (!signal) return null;
  const lbl   = signal.state === 'buy' ? 'BUY' : signal.state === 'sell' ? 'SELL' : 'NEUTRAL';
  const arrow = signal.state === 'buy' ? '↑'   : signal.state === 'sell' ? '↓'    : '→';
  return (
    <div className={`inline-flex min-w-[62px] shrink-0 flex-col items-end gap-0.5 rounded-[7px] border px-2 py-[5px] ${PALETTE[signal.state]}`}>
      <div className="inline-flex items-center gap-1 font-mono text-[11px] font-extrabold leading-none tracking-wide">
        {arrow}{lbl}
      </div>
      {signal.value != null && (
        <div className="font-mono text-[9px] leading-none opacity-75">{signal.value}</div>
      )}
    </div>
  );
}
