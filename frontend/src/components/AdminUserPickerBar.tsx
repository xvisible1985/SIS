import { useState, useRef, useEffect, useMemo } from 'react'
import { Search, Shield, X, LogOut } from 'lucide-react'
import type { AdminUser } from '../features/admin-users/types'
import { useAdminUsers } from '../features/admin-users/api'
import { useSystemHealth } from '../hooks/useSystemHealth'
import { apiClient } from '../api/client'
import {
  getImpersonatingAs,
  isImpersonating,
  startImpersonation,
  stopImpersonation,
} from '../contexts/ImpersonationContext'

function metricColor(pct: number): string {
  if (pct < 60) return 'text-emerald-400'
  if (pct < 80) return 'text-amber-400'
  return 'text-rose-400'
}

function SystemMetricChip({ label, pct }: { label: string; pct: number }) {
  return (
    <span className="hidden md:flex items-baseline gap-[3px]">
      <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">{label}</span>
      <span className={`text-[11px] font-semibold tabular-nums ${metricColor(pct)}`}>
        {Math.round(pct)}%
      </span>
    </span>
  )
}

export function AdminUserPickerBar() {
  const impersonating = isImpersonating()
  const impAs = getImpersonatingAs()

  const { users, loading } = useAdminUsers()
  const [search, setSearch] = useState('')
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)
  const health = useSystemHealth()

  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const filtered = useMemo(() => {
    const q = search.toLowerCase().trim()
    if (!q) return users.slice(0, 15)
    return users
      .filter((u: AdminUser) =>
        (u.name ?? '').toLowerCase().includes(q) ||
        u.email.toLowerCase().includes(q) ||
        u.id.toLowerCase().includes(q),
      )
      .slice(0, 15)
  }, [users, search])

  async function handleSelectUser(u: AdminUser) {
    setOpen(false)
    setSearch('')
    setBusy(true)
    try {
      const res = await apiClient.post<{ token: string }>(`/admin/impersonate/${u.id}`)
      startImpersonation(res.data.token, {
        id: u.id,
        name: u.name ?? '',
        email: u.email,
      })
      window.location.href = '/'
    } catch {
      setBusy(false)
    }
  }

  function handleReturn() {
    stopImpersonation()
    window.location.href = '/'
  }

  return (
    <div
      className="flex items-center gap-2 px-3 flex-shrink-0 border-b border-amber-500/15 bg-[#0d0a00]"
      style={{ height: 44 }}
    >
      <div className="flex items-center gap-1.5 text-amber-500/60">
        <Shield size={12} strokeWidth={2} />
        <span className="text-[10px] font-bold uppercase tracking-[1px]">Admin</span>
      </div>

      <div className="h-3 w-px bg-white/[.07]" />

      {/* Impersonating badge */}
      {impersonating && impAs && (
        <div className="flex items-center gap-1.5 rounded-md border border-amber-400/25 bg-amber-400/[.10] px-2 py-0.5">
          <span className="text-[10px] text-amber-400/60">как</span>
          <span className="max-w-[160px] truncate text-[11px] font-semibold text-amber-200">
            {impAs.name || impAs.email}
          </span>
          <button
            type="button"
            onClick={handleReturn}
            title="Вернуться в свой аккаунт"
            className="ml-0.5 flex items-center gap-1 rounded px-1 py-0.5 text-[10px] text-amber-400/60 hover:bg-amber-400/[.15] hover:text-amber-200 transition-colors"
          >
            <LogOut size={10} />
            <span className="hidden sm:inline">Выйти</span>
          </button>
        </div>
      )}

      {/* System health */}
      <div className="flex flex-1 items-center justify-center gap-3 min-w-0 overflow-hidden px-2">
        {health && (
          <>
            <SystemMetricChip label="CPU"  pct={health.cpu_pct} />
            <SystemMetricChip label="RAM"  pct={health.ram_pct} />
            <SystemMetricChip label="Disk" pct={health.disk_pct} />
            <span className="hidden md:flex items-baseline gap-[3px]">
              <span className="text-[9px] font-bold uppercase tracking-[0.8px] text-slate-600">DB</span>
              <span className={`text-[11px] font-semibold ${health.db_ok ? 'text-emerald-400' : 'text-rose-400'}`}>
                {health.db_ok ? `✓${(health.db_size_mb / 1024).toFixed(1)}G` : '✗'}
              </span>
              {health.db_ok && health.db_growth_mb_per_day >= 50 && (
                <span className="text-[10px] text-slate-500">+{Math.round(health.db_growth_mb_per_day)}M/д</span>
              )}
            </span>
          </>
        )}
      </div>

      {/* User picker — hidden when impersonating */}
      {!impersonating && (
        <div className="relative" ref={wrapRef}>
          <button
            type="button"
            disabled={busy}
            onClick={() => { setOpen(v => !v); setSearch('') }}
            className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-[11px] font-medium transition-colors ${
              open
                ? 'border-amber-400/40 bg-amber-400/[.12] text-amber-200'
                : 'border-white/[.10] bg-white/[.04] text-slate-400 hover:text-slate-200 hover:bg-white/[.07]'
            } disabled:opacity-50`}
          >
            <Search size={11} />
            {busy ? 'Загрузка...' : 'Войти как пользователь'}
          </button>

          {open && (
            <div className="absolute right-0 top-full mt-1.5 z-[100] w-[300px] overflow-hidden rounded-[13px] border border-white/[.10] bg-[#0d1220] shadow-[0_16px_40px_-8px_rgba(0,0,0,.9)]">
              <div className="border-b border-white/[.07] p-2">
                <input
                  autoFocus
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                  placeholder="Имя, email или ID..."
                  className="w-full rounded-lg border border-white/[.08] bg-white/[.04] px-2.5 py-1.5 text-[12px] text-slate-200 placeholder-slate-600 outline-none focus:border-white/[.18]"
                />
              </div>
              <div className="max-h-[260px] overflow-y-auto py-1">
                {loading && <div className="py-4 text-center text-[11px] text-slate-600">Загрузка...</div>}
                {!loading && filtered.length === 0 && <div className="py-4 text-center text-[11px] text-slate-600">Не найдено</div>}
                {filtered.map((u: AdminUser) => (
                  <button
                    key={u.id}
                    type="button"
                    onClick={() => handleSelectUser(u)}
                    className="flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors hover:bg-white/[.04]"
                  >
                    <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-[linear-gradient(135deg,#6b8cff,#c14dff)] text-[9px] font-bold text-white">
                      {(u.name || u.email).slice(0, 2).toUpperCase()}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-[12px] font-semibold text-slate-200">{u.name || u.email}</div>
                      {u.name && <div className="truncate text-[10px] text-slate-500">{u.email}</div>}
                    </div>
                    <div className="text-[10px] text-slate-600 shrink-0">{u.accounts.length} акк.</div>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* While impersonating: quick return button also visible at the right */}
      {impersonating && (
        <button
          type="button"
          onClick={handleReturn}
          className="flex items-center gap-1.5 rounded-lg border border-white/[.10] bg-white/[.04] px-2.5 py-1 text-[11px] font-medium text-slate-400 hover:bg-rose-500/[.12] hover:border-rose-500/30 hover:text-rose-300 transition-colors"
        >
          <X size={11} />
          Выйти из режима
        </button>
      )}
    </div>
  )
}
