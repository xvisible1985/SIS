type Props = {
  price?: number;
  size?: 'sm' | 'md';
};

/** FREE (зелёный) или $N/мес (золотой) */
export function PriceBadge({ price, size = 'md' }: Props) {
  const sm = size === 'sm';

  if (!price || price === 0) {
    return (
      <span
        className={
          'inline-flex items-center gap-1 rounded-full border border-emerald-400/30 bg-emerald-400/[.14] font-extrabold uppercase tracking-wider text-emerald-300 ' +
          (sm ? 'px-1.5 py-0.5 text-[9px]' : 'px-2 py-0.5 text-[10px]')
        }
      >
        FREE
      </span>
    );
  }

  return (
    <span
      className={
        'inline-flex items-baseline gap-px rounded-full border border-amber-500/35 bg-[linear-gradient(135deg,rgba(247,166,0,.18),rgba(247,166,0,.08))] font-display font-extrabold tracking-tight text-amber-400 shadow-[0_4px_12px_-6px_rgba(247,166,0,.4)] ' +
        (sm ? 'px-2 py-0.5 text-[10px]' : 'px-2.5 py-0.5 text-[11px]')
      }
    >
      ${price}
      <span className={'font-sans font-semibold opacity-80 ' + (sm ? 'text-[9px]' : 'text-[10px]')}>
        /мес
      </span>
    </span>
  );
}
