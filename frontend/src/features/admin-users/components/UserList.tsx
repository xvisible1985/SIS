import { useMemo, useState, useEffect, useCallback } from 'react';
import { Search, X, Filter, Plus, RotateCcw, ChevronDown } from 'lucide-react';
import type { AdminUser, StatusFilter } from '../types';
import { daysSince, fmtDate, fmtMoney } from '../utils';
import { Avatar } from './Avatar';
import { StatusPill } from './StatusPill';
import { RoleTag } from './RoleTag';

const COLS = ['Пользователь', 'Роль', 'Баланс', 'Статус', 'Регистрация', ''];
const LS_KEY = 'admin_users_col_widths';
const DEFAULT_WIDTHS = [320, 140, 120, 100, 160, 60];
const MIN_COL_W = 60;

type Props = {
  users: AdminUser[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onCreate?: () => void;
  onRefresh?: () => void;
};

const FILTERS: { id: StatusFilter; label: string; color?: string }[] = [
  { id: 'all',     label: 'Все' },
  { id: 'active',  label: 'Активные', color: 'text-emerald-300' },
  { id: 'pending', label: 'Ожидают',  color: 'text-amber-400' },
  { id: 'blocked', label: 'Заблок.',  color: 'text-rose-300' },
  { id: 'admin',   label: 'Админы',   color: 'text-[#b8c8ff]' },
  { id: 'curator', label: 'Кураторы', color: 'text-[#d8a4ff]' },
];

export function UserList({ users, selectedId, onSelect, onCreate, onRefresh }: Props) {
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<StatusFilter>('all');
  const [widths, setWidths] = useState<number[]>(() => {
    try { const s = localStorage.getItem(LS_KEY); return s ? JSON.parse(s) : DEFAULT_WIDTHS; }
    catch { return DEFAULT_WIDTHS; }
  });

  useEffect(() => { localStorage.setItem(LS_KEY, JSON.stringify(widths)); }, [widths]);

  const startResize = useCallback((idx: number, e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = widths[idx];
    const onMove = (ev: MouseEvent) => {
      const delta = ev.clientX - startX;
      setWidths(prev => {
        const next = [...prev];
        next[idx] = Math.max(MIN_COL_W, startW + delta);
        return next;
      });
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }, [widths]);

  const filtered = useMemo(() => {
    const ql = q.toLowerCase().trim();
    return users.filter((u) => {
      if (filter === 'active'  && u.status !== 'active')  return false;
      if (filter === 'pending' && u.status !== 'pending') return false;
      if (filter === 'blocked' && u.status !== 'blocked') return false;
      if (filter === 'admin'   && u.role !== 'admin')     return false;
      if (filter === 'curator' && !u.curator)             return false;
      if (!ql) return true;
      return (
        u.name.toLowerCase().includes(ql) ||
        u.email.toLowerCase().includes(ql) ||
        u.id.toLowerCase().includes(ql) ||
        u.accounts.some((a) => a.apiKey.toLowerCase().includes(ql))
      );
    });
  }, [q, filter, users]);

  const stats = useMemo(
    () => ({
      total:    users.length,
      active:   users.filter((u) => u.status === 'active').length,
      pending:  users.filter((u) => u.status === 'pending').length,
      blocked:  users.filter((u) => u.status === 'blocked').length,
      admins:   users.filter((u) => u.role === 'admin').length,
      curators: users.filter((u) => u.curator).length,
      balanceSum: users.reduce((a, u) => a + u.balance, 0),
      new7d:    users.filter((u) => daysSince(u.joined) <= 7).length,
    }),
    [users],
  );

  const filterCount: Record<StatusFilter, number> = {
    all: stats.total, active: stats.active, pending: stats.pending,
    blocked: stats.blocked, admin: stats.admins, curator: stats.curators,
  };

  return (
    <div className="flex flex-1 flex-col">
      {/* page header */}
      <div className="px-6 pt-5">
        <div className="mb-3.5 flex items-baseline gap-3.5">
          <h1 className="m-0 font-display text-2xl font-bold tracking-tight text-slate-50">Пользователи</h1>
          <span className="font-mono text-[11px] text-slate-400">{stats.total} записей</span>
          <div className="flex-1" />
          <button
            type="button"
            onClick={onRefresh}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
          >
            <RotateCcw size={12} strokeWidth={2} />
            Обновить
          </button>
          <button
            type="button"
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff,#3a67e6)] px-3 py-2 text-xs font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_4px_12px_-6px_rgba(74,125,255,.6)]"
          >
            <Plus size={12} strokeWidth={2.4} />
            Создать пользователя
          </button>
        </div>

        {/* stats */}
        <div className="mb-4 flex">
          <div className="flex divide-x divide-white/[.05] rounded-xl border border-white/[.06] bg-white/[.02]">
            <Stat label="Всего"    value={stats.total} />
            <Stat label="Активны"  value={stats.active}  c="text-emerald-300" />
            <Stat label="Ожидают"  value={stats.pending} c="text-amber-400" />
            <Stat label="Заблок."  value={stats.blocked} c="text-rose-300" />
            <Stat label="Балансы"  value={fmtMoney(stats.balanceSum).replace(/\.\d+$/, '')} />
            <Stat label="Новых · 7д" value={`+${stats.new7d}`} c="text-[#b8c8ff]" />
          </div>
        </div>

        {/* search + filters */}
        <div className="flex items-center gap-2.5">
          <div className="flex max-w-[520px] flex-1 items-center gap-1 rounded-[10px] border border-white/[.08] bg-white/[.03] px-3">
            <Search size={15} className="text-slate-400" />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Поиск по имени, email, ID, API key…"
              className="flex-1 bg-transparent px-2 py-2.5 text-sm text-slate-100 outline-none"
            />
            {q && (
              <button
                type="button"
                onClick={() => setQ('')}
                className="flex h-7 w-7 items-center justify-center rounded text-slate-400 hover:bg-white/[.05]"
              >
                <X size={12} />
              </button>
            )}
            <span className="ml-1 rounded border border-white/[.06] px-1.5 py-0.5 font-mono text-[10px] text-slate-500">
              ⌘K
            </span>
          </div>

          <div className="flex gap-px rounded-[10px] border border-white/[.06] bg-white/[.02] p-0.5">
            {FILTERS.map((f) => {
              const on = filter === f.id;
              return (
                <button
                  key={f.id}
                  type="button"
                  onClick={() => setFilter(f.id)}
                  className={
                    'inline-flex items-center gap-1.5 rounded-[7px] px-3 py-1.5 text-xs font-semibold transition-colors ' +
                    (on
                      ? 'border border-[#5b8cff]/28 bg-[#5b8cff]/[.16] text-[#b8c8ff]'
                      : 'border border-transparent text-slate-300 hover:text-slate-100')
                  }
                >
                  {f.label}
                  <span className={`rounded bg-black/25 px-1.5 py-px font-mono text-[10px] ${on ? 'text-[#b8c8ff]' : f.color ?? 'text-slate-400'}`}>
                    {filterCount[f.id]}
                  </span>
                </button>
              );
            })}
          </div>

          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[.08] bg-white/[.03] px-3 py-2 text-xs font-semibold text-slate-200 hover:bg-white/[.06]"
          >
            <Filter size={12} strokeWidth={2} />
            Фильтры
          </button>
        </div>
      </div>

      {/* list */}
      <div className="mt-4 flex-1 overflow-auto border-t border-white/[.06] bg-white/[.01]">
        <table className="w-full border-collapse text-left">
          <thead className="sticky top-0 z-10 bg-[#0a0d14]">
            <tr className="border-b border-white/[.06] text-[10px] font-bold uppercase tracking-wider text-slate-500">
              {COLS.map((label, i) => (
                <th
                  key={i}
                  className="relative select-none px-5 py-2.5 text-left"
                  style={{ width: widths[i], minWidth: MIN_COL_W }}
                >
                  {label}
                  {i < COLS.length - 1 && (
                    <div
                      onMouseDown={(e) => startResize(i, e)}
                      className="absolute right-0 top-0 h-full w-1.5 cursor-col-resize hover:bg-[#5b8cff]/40"
                    />
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {filtered.map((u) => {
              const sel = u.id === selectedId;
              return (
                <tr
                  key={u.id}
                  onClick={() => onSelect(u.id)}
                  className={
                    'cursor-pointer border-b border-white/[.04] transition-colors ' +
                    (sel
                      ? 'bg-[linear-gradient(90deg,rgba(91,140,255,.10),rgba(91,140,255,.02))] shadow-[inset_2px_0_0_#5b8cff]'
                      : 'hover:bg-white/[.02]')
                  }
                >
                  <td className="px-5 py-3" style={{ width: widths[0] }}>
                    <div className="flex min-w-0 items-center gap-3">
                      <Avatar name={u.name} size={34} status={u.status} />
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="truncate text-sm font-semibold text-slate-50">{u.name}</span>
                          {!u.emailVerified && (
                            <span title="Email не подтверждён" className="rounded border border-amber-500/30 bg-amber-500/[.14] px-1 py-px text-[9px] font-bold uppercase tracking-wider text-amber-400">
                              не подтв.
                            </span>
                          )}
                        </div>
                        <div className="mt-0.5 font-mono text-[11px] text-slate-400">{u.email}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-5 py-3" style={{ width: widths[1] }}>
                    <RoleTag role={u.role} curator={u.curator} />
                  </td>
                  <td className="px-5 py-3" style={{ width: widths[2] }}>
                    <span className={'font-mono text-sm font-semibold ' + (u.balance > 0 ? 'text-slate-100' : 'text-slate-500')}>
                      {fmtMoney(u.balance)}
                    </span>
                  </td>
                  <td className="px-5 py-3" style={{ width: widths[3] }}>
                    <StatusPill status={u.status} />
                  </td>
                  <td className="px-5 py-3" style={{ width: widths[4] }}>
                    <div className="text-xs text-slate-400">
                      {fmtDate(u.joined)}
                      <div className="mt-0.5 text-[10px] text-slate-500">{daysSince(u.joined)} дн. назад</div>
                    </div>
                  </td>
                  <td className="px-5 py-3" style={{ width: widths[5] }}>
                    <div className="flex items-center gap-1.5">
                      <span className="font-mono text-[11px] text-slate-400">{u.accounts.length} API</span>
                      <ChevronDown size={14} className="-rotate-90 text-slate-500" />
                    </div>
                  </td>
                </tr>
              );
            })}
            {filtered.length === 0 && (
              <tr>
                <td colSpan={COLS.length} className="px-5 py-16 text-center text-sm text-slate-400">
                  Ничего не нашлось. Попробуй изменить запрос или фильтр.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Stat({ label, value, c = 'text-slate-50' }: { label: string; value: string | number; c?: string }) {
  return (
    <div className="px-3.5 py-2.5">
      <div className="text-[10px] font-bold uppercase tracking-wider text-slate-400">{label}</div>
      <div className={`mt-0.5 font-display text-xl font-bold tracking-tight ${c}`}>{value}</div>
    </div>
  );
}
