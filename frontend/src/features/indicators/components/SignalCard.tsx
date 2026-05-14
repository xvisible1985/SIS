import { useRef, useState } from 'react';
import { ChevronUp, Settings } from 'lucide-react';
import { CAT_STYLES, TFS } from '../categories';
import type { SignalDef, Candle, SignalState } from '../types';
import { SignalPill } from './SignalPill';
import { ParamNumber, ParamSegmented, useParams } from './Params';

type Props<P extends Record<string, unknown>> = {
  signal: SignalDef<P>;
  candles?: Candle[];
  value?: P;
  onChange?: (next: P) => void;
  defaultExpanded?: boolean;
  onSettingsClick?: (rect: DOMRect) => void;
  hideBadge?: boolean;
};

function cardBg(state?: 'buy' | 'sell' | 'neutral') {
  if (state === 'buy')
    return 'border-emerald-400/30 bg-[linear-gradient(180deg,rgba(52,211,153,.08)_0%,rgba(52,211,153,.03)_100%)] shadow-[0_10px_28px_-18px_rgba(52,211,153,.35)]';
  if (state === 'sell')
    return 'border-rose-400/30 bg-[linear-gradient(180deg,rgba(251,113,133,.08)_0%,rgba(251,113,133,.03)_100%)] shadow-[0_10px_28px_-18px_rgba(251,113,133,.35)]';
  return 'border-white/[.06] bg-white/[.08]';
}

export function SignalCard<P extends Record<string, unknown>>({ signal, candles, value, onChange, defaultExpanded, onSettingsClick, hideBadge }: Props<P>) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(defaultExpanded ?? false);
  const [innerParams, setInner] = useParams<P>(signal.defaults);
  const params = value ?? innerParams;
  const setParam = <K extends keyof P>(k: K, v: P[K]) => {
    if (onChange) onChange({ ...params, [k]: v });
    else setInner(k, v);
  };
  const cat = CAT_STYLES[signal.cat];
  const liveState: SignalState | undefined = signal.compute
    ? signal.compute(params, candles ?? [])
    : signal.state;

  return (
    <div ref={rootRef} className={`flex flex-col overflow-hidden rounded-[12px] border ${cardBg(liveState)}`}>
      <div className="flex items-start gap-2.5 p-3 pb-2.5">
<div className="min-w-0 flex-1">
          <div className="truncate text-[14px] font-bold leading-tight tracking-tight text-slate-50">{signal.name}</div>
          <div className="mt-1 min-h-[30px] text-[11px] leading-snug text-slate-400 line-clamp-2">{signal.desc}</div>
        </div>
        {!hideBadge && (
          <div className="shrink-0">
            {liveState && <SignalPill signal={{ state: liveState }} />}
          </div>
        )}
      </div>

      <div className="px-3 pb-2.5">
        <div className="min-h-[52px] rounded-md border border-white/[.06] bg-black/30 px-2.5 py-2 font-mono text-[11px] leading-relaxed text-slate-200 break-words line-clamp-3">
          {signal.formula(params)}
        </div>
      </div>

      {expanded && (
        <div className="flex flex-col gap-2.5 border-t border-white/[.06] px-3 pt-2.5 pb-3">
          {signal.about && (
            <p className="text-[11px] leading-relaxed text-slate-400 border-b border-white/[.04] pb-2.5">{signal.about}</p>
          )}
          {signal.params.map(p => {
            const key = p.key as keyof P;
            if (p.kind === 'number') {
              return (
                <ParamNumber key={p.key} label={p.label} hint={p.hint}
                  value={params[key] as unknown as number}
                  step={p.step} min={p.min} max={p.max} suffix={p.suffix} decimals={p.decimals}
                  onChange={v => setParam(key, v as P[keyof P])} />
              );
            }
            return (
              <ParamSegmented key={p.key} label={p.label} hint={p.hint}
                value={params[key] as unknown as string}
                options={p.options}
                onChange={v => setParam(key, v as P[keyof P])} />
            );
          })}
          {'tf' in (params as object) && (
            <ParamSegmented label="Таймфрейм" value={(params as any).tf} options={TFS}
              onChange={v => setParam('tf' as keyof P, v as P[keyof P])} />
          )}
        </div>
      )}

      <button type="button" onClick={() => {
          if (onSettingsClick && rootRef.current) onSettingsClick(rootRef.current.getBoundingClientRect())
          else setExpanded(v => !v)
        }}
        className="mt-auto flex items-center gap-1.5 border-t border-white/[.06] px-3 py-2 text-[10px] font-semibold uppercase tracking-wider text-slate-400 hover:bg-white/[.02]">
        {expanded ? <><ChevronUp size={11} />Свернуть</> : <><Settings size={11} />Настроить</>}
        <span className="ml-auto font-mono text-[10px] text-slate-500">{signal.params.length} парам.</span>
      </button>
    </div>
  );
}
