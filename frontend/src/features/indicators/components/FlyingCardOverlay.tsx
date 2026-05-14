import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import type { IndicatorDef, BaseParams, SignalDef, Candle } from '../types';
import { IndicatorCard } from './IndicatorCard';
import { SignalCard } from './SignalCard';

type Phase = 'source' | 'fly' | 'open' | 'fly-back';

export type FlyingItem =
  | { type: 'indicator'; def: IndicatorDef<BaseParams> }
  | { type: 'signal'; def: SignalDef<Record<string, unknown>> };

interface Props {
  item: FlyingItem;
  sourceRect: DOMRect;
  candles?: Candle[];
  value?: Record<string, unknown>;
  onChange?: (next: Record<string, unknown>) => void;
  onClose: () => void;
}

const CARD_W = 400;

export function FlyingCardOverlay({ item, sourceRect, candles, value, onChange, onClose }: Props) {
  const [phase, setPhase] = useState<Phase>('source');

  // Fixed anchor point: center of screen
  const cx = Math.max(16, (window.innerWidth  - CARD_W) / 2);
  const cy = Math.max(16, (window.innerHeight - 520)   / 2);

  // Offset from center anchor to source card (center-to-center on X, top-to-top on Y)
  const dx = (sourceRect.left + sourceRect.width / 2) - (cx + CARD_W / 2);
  const dy = sourceRect.top - cy;

  // Scale so the card visually starts at source card width
  const srcScale = Math.min(1, sourceRect.width / CARD_W);

  // Double rAF: browser paints 'source' state before transition starts
  useEffect(() => {
    let r1: number, r2: number;
    r1 = requestAnimationFrame(() => {
      r2 = requestAnimationFrame(() => setPhase('fly'));
    });
    return () => { cancelAnimationFrame(r1); cancelAnimationFrame(r2); };
  }, []);

  // Phase timing chain
  useEffect(() => {
    let t: number;
    // expo-out fly: 420ms until settled
    if (phase === 'fly')      t = window.setTimeout(() => setPhase('open'),    420);
    // fly-back finishes in 300ms
    if (phase === 'fly-back') t = window.setTimeout(onClose,                   320);
    return () => clearTimeout(t);
  }, [phase, onClose]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  });

  function close() {
    if (phase === 'open') setPhase('fly-back');
  }

  const atSource   = phase === 'source';
  const flyingBack = phase === 'fly-back';
  const atCenter   = phase === 'open';

  // Source & fly-back: card starts/ends at source position, slightly smaller
  // Center: card at anchor, full size
  const transform =
    atSource || flyingBack
      ? `translate(${dx}px, ${dy}px) scale(${srcScale})`
      : 'translate(0px, 0px) scale(1)';

  const transition =
    phase === 'fly'
      // expo-out: snappy lift-off, smooth arrival
      ? 'transform 420ms cubic-bezier(0.16, 1, 0.3, 1), opacity 180ms ease-out'
      : phase === 'fly-back'
      // ease-in: card accelerates away, fades simultaneously
      ? 'transform 300ms cubic-bezier(0.4, 0, 1, 1), opacity 240ms ease-in'
      : 'none';

  const cardOpacity = atSource ? 0.6 : flyingBack ? 0 : 1;

  return createPortal(
    <div className="fixed inset-0 z-[9999]">
      {/* Backdrop — fades in with fly, out with fly-back */}
      <div
        className="absolute inset-0 bg-black/55 backdrop-blur-[6px]"
        style={{
          opacity: atSource || flyingBack ? 0 : 1,
          transition: 'opacity 280ms ease',
        }}
        onClick={close}
      />

      {/* Card container — GPU-composited via transform only, fixed at center anchor */}
      <div
        style={{
          position: 'fixed',
          left: cx,
          top: cy,
          width: CARD_W,
          opacity: cardOpacity,
          transform,
          transition,
          transformOrigin: 'top center',
          pointerEvents: atCenter ? 'auto' : 'none',
          zIndex: 10001,
          // Lift shadow grows during fly-in
          filter: atCenter
            ? 'drop-shadow(0 32px 56px rgba(0,0,0,.85)) drop-shadow(0 8px 18px rgba(0,0,0,.6))'
            : 'drop-shadow(0 4px 8px rgba(0,0,0,.4))',
        }}
      >
        {/* Solid background wrapper so card is never transparent in overlay */}
        <div className="overflow-hidden rounded-[12px] bg-[#0f1420] ring-1 ring-white/[.10]">
          {item.type === 'indicator' && (
            <IndicatorCard
              indicator={item.def}
              candles={candles}
              defaultExpanded={true}
              onSettingsClick={close}
              value={value as BaseParams | undefined}
              onChange={p => onChange?.(p)}
            />
          )}
          {item.type === 'signal' && (
            <SignalCard
              signal={item.def}
              candles={candles}
              defaultExpanded={true}
              onSettingsClick={close}
              value={value}
              onChange={onChange}
            />
          )}
        </div>
      </div>
    </div>,
    document.body
  );
}
