import { STRAT_META } from '../strategyMeta';
import type { BotStrategy } from '../ui-types';

type Props = { strategy: BotStrategy; size?: 'sm' | 'md' };

export function StrategyChip({ strategy, size = 'md' }: Props) {
  const m = STRAT_META[strategy];
  const Icon = m.icon;
  const sm = size === 'sm';
  return (
    <span
      className={
        'inline-flex items-center gap-1 rounded-[5px] border font-bold uppercase tracking-wider ' +
        (sm ? 'px-1.5 py-0.5 text-[10px]' : 'px-2 py-0.5 text-[11px]')
      }
      style={{ background: m.bg, borderColor: m.border, color: m.color }}
    >
      <Icon size={sm ? 10 : 12} strokeWidth={2} />
      {m.label}
    </span>
  );
}
