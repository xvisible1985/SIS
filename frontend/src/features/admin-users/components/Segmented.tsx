import type { LucideIcon } from 'lucide-react';

type Option<T extends string> = {
  value: T;
  label: string;
  icon?: LucideIcon;
  /** Optional tw classes for the active state */
  activeClass?: string;
};

type Props<T extends string> = {
  value: T;
  options: Option<T>[];
  onChange: (v: T) => void;
};

const DEFAULT_ACTIVE = 'border-[#5b8cff]/30 bg-[#5b8cff]/[.18] text-[#b8c8ff]';

export function Segmented<T extends string>({ value, options, onChange }: Props<T>) {
  return (
    <div className="inline-flex gap-0.5 rounded-lg border border-white/[.08] bg-black/25 p-0.5">
      {options.map((o) => {
        const on = o.value === value;
        const Icon = o.icon;
        return (
          <button
            key={o.value}
            type="button"
            onClick={() => onChange(o.value)}
            className={
              'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ' +
              (on
                ? `border ${o.activeClass ?? DEFAULT_ACTIVE}`
                : 'border border-transparent text-slate-400 hover:text-slate-200')
            }
          >
            {Icon && <Icon size={12} strokeWidth={2} />}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
