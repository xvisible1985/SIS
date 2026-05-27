type Props = { size?: number };

export function NovaMark({ size = 28 }: Props) {
  return (
    <img
      src="/favicon.svg"
      width={size}
      height={size}
      alt="NovaBot"
      style={{ flexShrink: 0 }}
    />
  );
}
