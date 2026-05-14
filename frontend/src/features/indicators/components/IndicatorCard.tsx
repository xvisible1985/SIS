import { useRef, useState } from 'react';
import { ChevronUp, Settings } from 'lucide-react';
import { CAT_STYLES, SOURCES, TFS } from '../categories';
import type { IndicatorDef, BaseParams } from '../types';
import { SignalPill } from './SignalPill';
import { ParamNumber, ParamSegmented, useParams } from './Params';

type Props<P extends BaseParams> = {
  indicator: IndicatorDef<P>;
  candles?: import('../types').Candle[];
  value?: P;
  onChange?: (next: P) => void;
  defaultExpanded?: boolean;
  onSettingsClick?: (rect: DOMRect) => void;
  hideBadge?: boolean;
};

export function IndicatorCard<P extends BaseParams>({ indicator, candles, value, onChange, defaultExpanded, onSettingsClick, hideBadge }: Props<P>) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(defaultExpanded ?? false);
  const [innerParams, setInner] = useParams<P>(indicator.defaults);
  const params = value ?? innerParams;
  const setParam = <K extends keyof P>(k: K, v: P[K]) => {
    if (onChange) onChange({ ...params, [k]: v });
    else setInner(k, v);
  };

  const cat = CAT_STYLES[indicator.cat];
  const signal = indicator.compute?.(params, candles ?? []);
  const sigOn = signal && signal.state !== 'neutral';

  return (
    <div ref={rootRef} className={
      'flex flex-col overflow-hidden rounded-[12px] border transition-colors ' +
      (sigOn
        ? 'border-[#5b8cff]/30 bg-[linear-gradient(180deg,rgba(91,140,255,.07)_0%,rgba(123,91,255,.04)_100%)] shadow-[0_10px_28px_-18px_rgba(91,140,255,.4)]'
        : 'border-white/[.06] bg-white/[.08]')
    }>
      <div className="flex items-start gap-2.5 p-3 pb-2.5">
<div className="min-w-0 flex-1">
          <div className="truncate text-[14px] font-bold leading-tight tracking-tight text-slate-50">{indicator.name}</div>
          <div className="mt-1 min-h-[30px] text-[11px] leading-snug text-slate-400 line-clamp-2">{indicator.desc}</div>
        </div>
        {!hideBadge && (
          <div className="shrink-0">
            <SignalPill signal={signal} />
          </div>
        )}
      </div>

      {indicator.Preview && (
        <div className="relative mx-3 mb-2.5 h-[38px] overflow-hidden rounded-md bg-black/25">
          <indicator.Preview params={params} candles={candles} />
        </div>
      )}

      {expanded && (
        <div className="flex flex-col gap-2.5 border-t border-white/[.06] px-3 pt-2.5 pb-3">
          {indicator.about && (
            <p className="text-[11px] leading-relaxed text-slate-400 border-b border-white/[.04] pb-2.5">{indicator.about}</p>
          )}
          {indicator.params.map(p => {
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
          {'source' in params && (
            <ParamSegmented label="Источник" value={params.source as string} options={SOURCES}
              onChange={v => setParam('source' as keyof P, v as P[keyof P])} />
          )}
          <ParamSegmented label="Таймфрейм" value={params.tf} options={TFS}
            onChange={v => setParam('tf' as keyof P, v as P[keyof P])} />
        </div>
      )}

      <button type="button" onClick={() => {
          if (onSettingsClick && rootRef.current) onSettingsClick(rootRef.current.getBoundingClientRect())
          else setExpanded(v => !v)
        }}
        className="mt-auto flex items-center gap-1.5 border-t border-white/[.06] px-3 py-2 text-[10px] font-semibold uppercase tracking-wider text-slate-400 hover:bg-white/[.02]">
        {expanded ? <><ChevronUp size={11} />Свернуть</> : <><Settings size={11} />Настроить</>}
        <span className="ml-auto font-mono text-[10px] text-slate-500">{indicator.params.length} парам.</span>
      </button>
    </div>
  );
}
