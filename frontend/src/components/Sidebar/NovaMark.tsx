type Props = { size?: number };

export function NovaMark({ size = 28 }: Props) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden>
      <defs>
        <linearGradient id="nm-g" x1="0" y1="0" x2="32" y2="32">
          <stop offset="0%" stopColor="#5b8cff" />
          <stop offset="55%" stopColor="#7b5bff" />
          <stop offset="100%" stopColor="#c14dff" />
        </linearGradient>
      </defs>
      <rect x="1" y="1" width="30" height="30" rx="9" fill="url(#nm-g)" />
      <path d="M9 22V10h2.4l8.2 8V10H22v12h-2.4l-8.2-8v8H9z" fill="#fff" />
      <circle cx="24" cy="9" r="2.2" fill="#fff" />
      <circle cx="24" cy="9" r="1.1" fill="#7b5bff" />
    </svg>
  );
}
