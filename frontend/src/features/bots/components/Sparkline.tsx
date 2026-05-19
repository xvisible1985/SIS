type Props = {
  data: number[];
  /** stroke + fill colour */
  color?: string;
  width?: number;
  height?: number;
  fill?: boolean;
  /** Если данные пустые/одна точка — рендерит ничего */
};

/**
 * Маленький SVG-спарклайн без зависимостей.
 * Если в проекте уже подключён lightweight-charts — можно заменить, но для
 * спарклайна 36-48px это оверкилл.
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
  const line = pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ');
  const area = `${line} L${width} ${height} L0 ${height} Z`;

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
