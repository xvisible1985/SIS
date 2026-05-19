import { Shield, Star } from 'lucide-react';
import type { UserRole } from '../types';

type Props = { role: UserRole; curator: boolean };

export function RoleTag({ role, curator }: Props) {
  return (
    <span className="inline-flex items-center gap-2">
      <span
        className={
          'inline-flex items-center gap-1 rounded-[5px] border px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ' +
          (role === 'admin'
            ? 'border-[#5b8cff]/35 bg-[#5b8cff]/[.18] text-[#b8c8ff]'
            : 'border-white/10 bg-white/[.04] text-slate-200')
        }
      >
        {role === 'admin' && <Shield size={10} strokeWidth={2.2} />}
        {role === 'admin' ? 'Админ' : 'User'}
      </span>
      {curator && (
        <span className="inline-flex items-center gap-1 rounded-[5px] border border-[#c14dff]/32 bg-[#c14dff]/[.18] px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-[#d8a4ff]">
          <Star size={9} strokeWidth={2.4} />
          Куратор
        </span>
      )}
    </span>
  );
}
