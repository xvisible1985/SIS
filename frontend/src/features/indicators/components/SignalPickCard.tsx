import type { SignalDef } from '../types'

const CAT_LABEL: Record<string, string> = {
  momentum: 'Моментум', trend: 'Тренд', volatility: 'Волатильность',
  volume: 'Объём', fundamental: 'Фундаментал',
}

export function SignalPickCard({ def, selected, onClick }: { def: SignalDef; selected: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex flex-col gap-1.5 rounded-xl border p-3 text-left transition-colors ${
        selected
          ? 'border-[#5b8cff]/60 bg-[#5b8cff]/[.08]'
          : 'border-white/[.07] bg-[#0d1018] hover:border-white/[.15]'
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] font-bold text-slate-300">{def.abbr}</span>
        <span className="rounded-full bg-white/[.05] px-1.5 py-0.5 text-[9px] uppercase tracking-wide text-slate-500">
          {CAT_LABEL[def.cat] ?? def.cat}
        </span>
      </div>
      <div className="text-[12px] font-semibold text-slate-100">{def.name}</div>
      <div className="line-clamp-2 text-[11px] leading-snug text-slate-500">{def.desc}</div>
    </button>
  )
}
