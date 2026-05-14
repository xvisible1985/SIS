import { useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Minus, Plus } from 'lucide-react';

function HintBadge({ text }: { text: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(false);
  const [pos, setPos] = useState({ top: 0, left: 0 });

  return (
    <span
      ref={ref}
      className="ml-1.5 inline-flex cursor-default"
      onMouseEnter={() => {
        if (ref.current) {
          const r = ref.current.getBoundingClientRect();
          setPos({ top: r.top, left: r.left + r.width / 2 });
        }
        setVisible(true);
      }}
      onMouseLeave={() => setVisible(false)}
    >
      <span className="h-3.5 w-3.5 rounded-full border border-white/[.10] bg-white/[.06] text-[9px] text-slate-400 inline-flex items-center justify-center font-bold leading-none select-none">?</span>
      {visible && createPortal(
        <span
          className="pointer-events-none fixed w-56 rounded-[9px] px-3 py-2.5 text-[11px] leading-relaxed whitespace-normal shadow-2xl"
          style={{
            background: '#2d2500',
            border: '1px solid rgba(247,166,0,.35)',
            color: '#f5d97a',
            zIndex: 99999,
            top: pos.top,
            left: pos.left,
            transform: 'translate(-50%, calc(-100% - 8px))',
          }}
        >
          {text}
        </span>,
        document.body
      )}
    </span>
  );
}

type NumberProps = {
  label: string;
  hint?: string;
  value: number;
  step?: number;
  min?: number;
  max?: number;
  suffix?: string;
  decimals?: number;
  onChange: (v: number) => void;
};

export function ParamNumber({ label, hint, value, step = 1, min, max, suffix, decimals = 0, onChange }: NumberProps) {
  const clamp = (v: number) => {
    if (min != null && v < min) v = min;
    if (max != null && v > max) v = max;
    return +v.toFixed(decimals);
  };
  return (
    <div className="grid grid-cols-[1fr_auto] items-center gap-2 py-1">
      <div className="flex items-center text-[11px] font-medium text-slate-200">
        {label}
        {hint && <HintBadge text={hint} />}
      </div>
      <div className="flex gap-1">
        <button type="button" onClick={() => onChange(clamp(value - step))}
          className="flex h-5 w-5 items-center justify-center rounded border border-white/[.06] bg-white/[.04] text-slate-400 hover:bg-white/[.08]">
          <Minus size={11} />
        </button>
        <span className="inline-flex min-w-[54px] items-center justify-end gap-1 rounded-[5px] border border-white/[.06] bg-black/30 px-2 py-[3px] text-right font-mono text-[11px] font-semibold text-slate-200">
          {value.toFixed(decimals)}
          {suffix && <span className="text-[9px] text-slate-500">{suffix}</span>}
        </span>
        <button type="button" onClick={() => onChange(clamp(value + step))}
          className="flex h-5 w-5 items-center justify-center rounded border border-white/[.06] bg-white/[.04] text-slate-400 hover:bg-white/[.08]">
          <Plus size={11} />
        </button>
      </div>
    </div>
  );
}

type SegProps = {
  label: string;
  hint?: string;
  value: string;
  options: readonly string[];
  onChange: (v: string) => void;
};

export function ParamSegmented({ label, hint, value, options, onChange }: SegProps) {
  return (
    <div className="grid grid-cols-[1fr_auto] items-center gap-2 py-1">
      <span className="flex items-center text-[11px] font-medium text-slate-200">
        {label}
        {hint && <HintBadge text={hint} />}
      </span>
      <div className="flex gap-px rounded-md border border-white/[.06] bg-black/25 p-0.5">
        {options.map(opt => {
          const on = opt === value;
          return (
            <button key={opt} type="button" onClick={() => onChange(opt)}
              className={
                'rounded px-2 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider transition-colors ' +
                (on ? 'bg-[#5b8cff]/[.18] text-[#b8c8ff] shadow-[inset_0_0_0_1px_rgba(91,140,255,.3)]' : 'text-slate-400 hover:text-slate-200')
              }>
              {opt}
            </button>
          );
        })}
      </div>
    </div>
  );
}

export function useParams<T extends Record<string, unknown>>(initial: T) {
  const [p, setP] = useState<T>(initial);
  const set = <K extends keyof T>(k: K, v: T[K]) => setP(prev => ({ ...prev, [k]: v }));
  return [p, set] as const;
}
