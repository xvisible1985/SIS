type Props = {
  on: boolean;
  onChange: (v: boolean) => void;
  /** ARIA label for accessibility */
  label?: string;
};

export function Toggle({ on, onChange, label }: Props) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      onClick={() => onChange(!on)}
      className={
        'relative h-5 w-9 shrink-0 rounded-full transition-colors ' +
        (on ? 'bg-[linear-gradient(180deg,#5b8cff,#7b5bff)]' : 'bg-white/[.08]')
      }
    >
      <span
        className={
          'absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-[0_2px_4px_rgba(0,0,0,.3)] transition-all ' +
          (on ? 'left-[18px]' : 'left-0.5')
        }
      />
    </button>
  );
}
