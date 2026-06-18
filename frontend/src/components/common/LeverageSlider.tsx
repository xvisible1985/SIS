import type { CSSProperties } from 'react';

/** Hex цвет → rgba строка с заданной прозрачностью */
function hexToRgba(hex: string, alpha: number): string {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r},${g},${b},${alpha})`;
}

type Props = {
  value: number;
  min?: number;
  max: number;
  onChange: (v: number) => void;
  disabled?: boolean;
  /** Hex-цвет thumb и заполненной части трека. По умолчанию #4a7dff */
  color?: string;
  className?: string;
};

/**
 * Кастомный ползунок плеча:
 * - заполненный трек слева (градиент)
 * - круглый цветной thumb с glow-кольцом
 * - анимация hover/active
 */
export function LeverageSlider({
  value,
  min = 1,
  max,
  onChange,
  disabled,
  color = '#4a7dff',
  className = '',
}: Props) {
  const pct = max > min ? ((value - min) / (max - min)) * 100 : 0;

  const trackBg  = `linear-gradient(to right, ${color} ${pct}%, rgba(255,255,255,0.09) ${pct}%)`;
  const glowRgba = hexToRgba(color, 0.28);

  const style: CSSProperties = {
    background: trackBg,
    '--lever-color': color,
    '--lever-glow':  glowRgba,
  } as CSSProperties;

  return (
    <input
      type="range"
      min={min}
      max={max}
      step={1}
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(parseInt(e.target.value))}
      className={`lever-slider w-full ${className}`}
      style={style}
    />
  );
}
