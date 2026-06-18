type Props = {
  data: number[];
  /** stroke + fill colour */
  color?: string;
  width?: number;
  height?: number;
  fill?: boolean;
  /** Если данные пустые/одна точка — рендерит ничего */
};

/** Строит сглаженный SVG-путь по алгоритму Catmull-Rom → Cubic Bezier */
function smoothPath(pts: [number, number][], tension = 0.35): string {
  if (pts.length < 2) return '';
  let d = `M${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[Math.min(pts.length - 1, i + 2)];
    const cp1x = p1[0] + (p2[0] - p0[0]) * tension;
    const cp1y = p1[1] + (p2[1] - p0[1]) * tension;
    const cp2x = p2[0] - (p3[0] - p1[0]) * tension;
    const cp2y = p2[1] - (p3[1] - p1[1]) * tension;
    d += ` C${cp1x.toFixed(1)} ${cp1y.toFixed(1)},${cp2x.toFixed(1)} ${cp2y.toFixed(1)},${p2[0].toFixed(1)} ${p2[1].toFixed(1)}`;
  }
  return d;
}

/**
 * Маленький SVG-спарклайн без зависимостей.
 * Кривая сглажена через Catmull-Rom → Cubic Bezier.
 */
export function Sparkline({ data, color = '#5be0a0', width = 180, height = 42, fill = true }: Props) {
  if (data.length < 2) return <svg width={width} height={height} />;

  const min = Math.min(...data);
  const max = Math.max(...data);
  const range = max - min || 1;
  const step = width / (data.length - 1);

  const pts = data.map<[number, number]>((v, i) => [
    i * step,
    height - ((v - min) / range) * (height - 4) - 2,
  ]);

  const line = smoothPath(pts);
  const last = pts[pts.length - 1];
  const area = `${line} L${last[0].toFixed(1)} ${height} L0 ${height} Z`;

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className="block"
    >
      {fill && <path d={area} fill={color} opacity={0.15} />}
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth={1.6}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
