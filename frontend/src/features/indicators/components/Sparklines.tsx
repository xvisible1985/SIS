import type { Candle } from '../types';

type Bands = { upper: number[]; lower: number[] };
type Props = {
  data: number[];
  stroke: string;
  fill?: string;
  mid?: { data: number[]; c: string };
  bands?: Bands;
  hLines?: Array<{ v: number; c: string }>;
};

export function SparkLine({ data, stroke, fill, mid, bands, hLines = [] }: Props) {
  const W = 300, H = 38;
  if (data.length < 2) return null;
  const min = Math.min(...data, ...(bands ? bands.lower : []), ...hLines.map(h => h.v));
  const max = Math.max(...data, ...(bands ? bands.upper : []), ...hLines.map(h => h.v));
  const range = max - min || 1;
  const px = (i: number) => (i / (data.length - 1)) * W;
  const py = (v: number) => H - ((v - min) / range) * (H - 4) - 2;
  const path = data.map((v, i) => `${i ? 'L' : 'M'}${px(i).toFixed(1)} ${py(v).toFixed(1)}`).join(' ');

  let bandPath = '';
  if (bands) {
    const top = bands.upper.map((v, i) => `${i ? 'L' : 'M'}${px(i).toFixed(1)} ${py(v).toFixed(1)}`).join(' ');
    const bot = bands.lower.map((v, i) => `L${px(data.length - 1 - i).toFixed(1)} ${py(bands.lower[data.length - 1 - i]!).toFixed(1)}`).join(' ');
    bandPath = `${top} ${bot} Z`;
  }

  return (
    <svg width="100%" height="100%" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="block">
      {hLines.map((h, idx) => (
        <line key={idx} x1="0" y1={py(h.v)} x2={W} y2={py(h.v)} stroke={h.c} strokeWidth="0.5" strokeDasharray="2 3" opacity="0.5" />
      ))}
      {bandPath && <path d={bandPath} fill={fill ?? stroke} opacity="0.25" />}
      {fill && !bands && (
        <path d={`${path} L${W} ${H} L0 ${H} Z`} fill={fill} opacity="0.2" />
      )}
      <path d={path} fill="none" stroke={stroke} strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      {mid && (
        <path
          d={mid.data.map((v, i) => `${i ? 'L' : 'M'}${px(i).toFixed(1)} ${py(v).toFixed(1)}`).join(' ')}
          fill="none" stroke={mid.c} strokeWidth="1" strokeDasharray="2 2"
        />
      )}
    </svg>
  );
}

export function SparkBars({ data, color }: { data: number[]; color: string }) {
  const W = 300, H = 38;
  const max = Math.max(...data) || 1;
  const bw = W / data.length;
  return (
    <svg width="100%" height="100%" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="block">
      {data.map((v, i) => {
        const h = (v / max) * (H - 4);
        return <rect key={i} x={i * bw + 1} y={H - h - 2} width={bw - 2} height={h} fill={color} opacity={i > data.length - 4 ? 1 : 0.5} />;
      })}
    </svg>
  );
}

export const demo = {
  sin:  (n = 32, freq = 2, amp = 20, off = 50) => Array.from({ length: n }, (_, i) => off + Math.sin((i / (n - 1)) * Math.PI * freq) * amp),
  up:   (n = 32) => Array.from({ length: n }, (_, i) => 30 + (i / (n - 1)) * 40 + Math.sin(i * 12) * 4),
  bars: (n = 24) => Array.from({ length: n }, (_, i) => 6 + Math.abs(Math.sin(i * 0.7)) * 24),
};

export function priceOf(c: Candle, src: 'close' | 'open' | 'high' | 'low' | 'hl2' | 'ohlc4') {
  switch (src) {
    case 'hl2':   return (c.high + c.low) / 2;
    case 'ohlc4': return (c.open + c.high + c.low + c.close) / 4;
    default:      return c[src];
  }
}
