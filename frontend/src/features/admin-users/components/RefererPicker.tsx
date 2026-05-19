import { useMemo, useState } from 'react';
import { Search, User, X, ChevronDown } from 'lucide-react';
import type { AdminUser } from '../types';
import { Avatar } from './Avatar';
import { RoleTag } from './RoleTag';

type Props = {
  value: string | null;
  onChange: (v: string | null) => void;
  /** ID of the user being edited — excluded from picker */
  currentUserId: string;
  users: AdminUser[];
};

export function RefererPicker({ value, onChange, currentUserId, users }: Props) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState('');

  const selected = value ? users.find((u) => u.id === value) : null;

  const filtered = useMemo(() => {
    const ql = q.toLowerCase().trim();
    return users
      .filter((u) => u.id !== currentUserId)
      .filter((u) => !ql || u.name.toLowerCase().includes(ql) || u.email.toLowerCase().includes(ql))
      .slice(0, 8);
  }, [q, currentUserId, users]);

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2.5 rounded-lg border border-white/[.08] bg-black/25 px-2.5 py-2.5 text-left text-sm text-slate-200"
      >
        {selected ? (
          <>
            <Avatar name={selected.name} size={22} />
            <div className="min-w-0 flex-1">
              <div className="text-sm font-semibold text-slate-100">{selected.name}</div>
              <div className="font-mono text-[11px] text-slate-400">{selected.email}</div>
            </div>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onChange(null);
              }}
              className="flex h-[22px] w-[22px] items-center justify-center rounded border border-white/[.08] bg-white/[.04] text-slate-400 hover:text-slate-200"
            >
              <X size={11} />
            </button>
          </>
        ) : (
          <>
            <User size={14} className="text-slate-400" />
            <span className="flex-1 text-slate-400">Выбрать реферера…</span>
          </>
        )}
        <ChevronDown size={14} className="text-slate-400" />
      </button>

      {open && (
        <div className="absolute left-0 right-0 top-[calc(100%+6px)] z-20 animate-[fadeIn_.18s_ease-out] rounded-[10px] border border-white/10 bg-[#11172a] p-1.5 shadow-[0_24px_48px_-16px_rgba(0,0,0,.7)]">
          <div className="mb-1.5 flex items-center gap-2 rounded-md bg-black/25 px-2 py-1.5">
            <Search size={13} className="text-slate-400" />
            <input
              autoFocus
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Поиск по имени или email…"
              className="flex-1 bg-transparent text-xs text-slate-100 outline-none"
            />
          </div>
          <div className="max-h-[260px] overflow-auto">
            {filtered.map((u) => (
              <button
                key={u.id}
                type="button"
                onClick={() => {
                  onChange(u.id);
                  setOpen(false);
                  setQ('');
                }}
                className="flex w-full items-center gap-2.5 rounded-md p-2 text-left hover:bg-white/[.03]"
              >
                <Avatar name={u.name} size={26} />
                <div className="min-w-0 flex-1">
                  <div className="text-xs font-semibold text-slate-100">{u.name}</div>
                  <div className="font-mono text-[10px] text-slate-400">{u.email}</div>
                </div>
                {u.role === 'admin' && <RoleTag role={u.role} curator={false} />}
              </button>
            ))}
            {filtered.length === 0 && (
              <div className="p-3 text-center text-xs text-slate-400">Ничего не найдено</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
