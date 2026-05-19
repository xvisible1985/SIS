import { avatarGradient, initials } from '../utils';

type Status = 'active' | 'pending' | 'blocked';

type Props = {
  name: string;
  size?: number;
  status?: Status;
};

const STATUS_DOT: Record<Status, string> = {
  active:  'bg-emerald-400',
  pending: 'bg-amber-500',
  blocked: 'bg-slate-500',
};

export function Avatar({ name, size = 32, status }: Props) {
  const isLarge = size >= 36;
  return (
    <div
      className={
        'relative flex shrink-0 items-center justify-center font-sans font-bold text-white ' +
        (isLarge ? 'rounded-[10px]' : 'rounded-lg')
      }
      style={{
        width: size,
        height: size,
        background: avatarGradient(name),
        fontSize: isLarge ? 13 : 11,
      }}
    >
      {initials(name)}
      {status && (
        <span
          className={
            'absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full border-2 border-[#0a0d14] ' +
            STATUS_DOT[status]
          }
        />
      )}
    </div>
  );
}
