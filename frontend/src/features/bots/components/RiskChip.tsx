import { RISK_META } from '../strategyMeta';
import type { RiskLevel } from '../ui-types';

export function RiskChip({ risk }: { risk: RiskLevel }) {
  const m = RISK_META[risk];
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 pr-2 text-[10px] font-bold uppercase tracking-wider"
      style={{ background: m.bg, borderColor: m.border, color: m.color }}
    >
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: m.color }} />
      {m.label}
    </span>
  );
}
