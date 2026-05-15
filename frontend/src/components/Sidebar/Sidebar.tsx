import { NavLink } from 'react-router-dom';
import {
  LayoutDashboard,
  Terminal,
  Webhook,
  Users,
  Plus,
  ChevronDown,
  Settings,
  ArrowUp,
  Eye,
  LogOut,
  BarChart2,
  Shield,
} from 'lucide-react';
import { NovaMark } from './NovaMark';
import { Sparkline } from './Sparkline';
import type { SidebarProps } from './Sidebar.types';

const NAV = [
  { to: '/accounts',  label: 'Api key',   icon: Users,    badge: 'count' as const, key: 'accounts' },
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/terminal',  label: 'Terminal',  icon: Terminal, badge: 'live' as const },
  { to: '/signals',   label: 'Сигналы',   icon: BarChart2 },
  { to: '/webhooks',  label: 'Webhooks',  icon: Webhook,  badge: 'count' as const, key: 'webhooks' },
  { to: '/admin',     label: 'Админка',   icon: Shield },
];

export function Sidebar({
  version = 'v0.0.0',
  account,
  equity,
  pnl24h,
  spark,
  novabotBalance,
  user,
  counters = {},
  onSelectAccount,
  onTopUp,
  onOpenSettings,
  onLogout,
}: SidebarProps) {
  return (
    <aside className="flex h-full w-[256px] flex-col gap-3.5 bg-[#0c1018] p-[18px_14px] font-sans text-slate-200">
      {/* brand */}
      <div className="flex items-center gap-2.5 px-1.5 py-0.5">
        <NovaMark size={28} />
        <div className="text-[19px] font-bold leading-none tracking-tight text-slate-50">
          NovaBot
        </div>
        <div className="ml-auto rounded-md border border-white/[.06] bg-white/[.04] px-[7px] py-[3px] font-mono text-[10px] font-medium tracking-wide text-slate-400">
          {version}
        </div>
      </div>

      {/* account + equity card */}
      <div className="relative overflow-hidden rounded-[14px] border border-[#7b8cff]/20 bg-[linear-gradient(135deg,#1d2540_0%,#1a1f37_60%,#2a1d3d_100%)] shadow-[0_12px_28px_-16px_rgba(91,140,255,.35)]">
        <div className="pointer-events-none absolute -right-7 -top-10 h-40 w-40 rounded-full bg-[radial-gradient(circle,rgba(123,91,255,.35),transparent_65%)] blur-lg" />

        {/* account selector */}
        <button
          type="button"
          onClick={onSelectAccount}
          className="relative flex w-full items-center justify-between border-b border-white/[.06] p-3 text-left hover:bg-white/[.02]"
        >
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-[linear-gradient(135deg,#f7a600,#e88f00)] text-[13px] font-extrabold text-[#1a1100] shadow-[0_2px_6px_-2px_rgba(247,166,0,.5)]">
              {account.exchangeBadge}
            </div>
            <div className="min-w-0">
              <div className="truncate text-[13px] font-semibold leading-none text-slate-50">
                {account.name}
              </div>
              <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-slate-400">
                <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_#41d28b]" />
                {account.exchange} · {account.status}
              </div>
            </div>
          </div>
          <ChevronDown size={16} className="shrink-0 text-slate-400" />
        </button>

        {/* equity body */}
        <div className="relative px-3.5 py-3">
          <div className="flex items-center justify-between">
            <div className="text-[10px] font-semibold uppercase tracking-[1.4px] text-slate-400">
              Equity
            </div>
            <button
              type="button"
              className="flex h-5 w-5 items-center justify-center rounded text-slate-400 hover:bg-white/[.05]"
              aria-label="скрыть баланс"
            >
              <Eye size={12} />
            </button>
          </div>
          <div className="mt-1 flex items-baseline gap-1.5 text-[26px] font-bold tracking-[-0.6px] text-white">
            ${equity.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
            <span className="text-xs font-medium text-slate-400">USDT</span>
          </div>

          <div className="mt-1.5 flex items-center gap-1.5">
            <span
              className={
                'inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 pr-2 text-[11px] font-semibold ' +
                (pnl24h.percent >= 0
                  ? 'border-emerald-400/25 bg-emerald-400/[.14] text-emerald-300'
                  : 'border-rose-400/25 bg-rose-400/[.14] text-rose-300')
              }
            >
              <ArrowUp
                size={10}
                strokeWidth={2.6}
                className={pnl24h.percent >= 0 ? '' : 'rotate-180'}
              />
              {pnl24h.percent >= 0 ? '+' : ''}
              {pnl24h.percent.toFixed(2)}% · ${Math.abs(pnl24h.usd).toFixed(0)}
            </span>
            <span className="text-[10px] text-slate-500">за 24ч</span>
          </div>

          <div className="-mx-0.5 mt-2">
            <Sparkline data={spark} width={204} height={36} positive={pnl24h.percent >= 0} />
          </div>
        </div>
      </div>

      {/* nav */}
      <nav className="flex flex-col gap-0.5">
        {NAV.map((item) => {
          const Icon = item.icon;
          return (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                'group relative flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ' +
                (isActive
                  ? 'bg-[linear-gradient(90deg,rgba(74,125,255,.18),rgba(74,125,255,.04))] font-semibold text-white shadow-[inset_2px_0_0_#5b8cff]'
                  : 'text-slate-400 hover:bg-white/[.03] hover:text-slate-200')
              }
            >
              {({ isActive }) => (
                <>
                  <Icon
                    size={17}
                    strokeWidth={1.7}
                    className={isActive ? 'text-white' : 'text-slate-500 group-hover:text-slate-300'}
                  />
                  {item.label}
                  {item.badge === 'live' && (
                    <span className="ml-auto inline-flex items-center gap-1 rounded-full bg-emerald-400/15 px-1.5 py-0.5 text-[10px] font-bold text-emerald-300">
                      <span className="h-1 w-1 rounded-full bg-emerald-300" />
                      live
                    </span>
                  )}
                  {item.badge === 'count' && item.key && counters[item.key as keyof typeof counters] != null && (
                    <span className="ml-auto rounded-full bg-white/[.08] px-1.5 py-0.5 text-[10px] font-bold text-slate-300">
                      {counters[item.key as keyof typeof counters]}
                    </span>
                  )}
                </>
              )}
            </NavLink>
          );
        })}
      </nav>

      <div className="flex-1" />

      {/* NovaBot balance + user */}
      <div className="flex flex-col gap-2.5 rounded-xl border border-white/[.06] bg-white/[.03] p-3">
        <div className="flex items-baseline justify-between">
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-[1.3px] text-slate-400">
              Баланс NovaBot
            </div>
            <div className="mt-0.5 text-xl font-bold tracking-tight text-white">
              ${novabotBalance.toFixed(2)}
            </div>
          </div>
          <div className="text-[10px] text-slate-500">для подписки и API</div>
        </div>
        <button
          type="button"
          onClick={onTopUp}
          className="flex w-full items-center justify-center gap-1.5 rounded-lg bg-[linear-gradient(180deg,#4a7dff_0%,#3a67e6_100%)] py-2.5 text-[13px] font-semibold text-white shadow-[inset_0_1px_0_rgba(255,255,255,.18),0_6px_16px_-8px_rgba(74,125,255,.6)] hover:brightness-110 active:brightness-95"
        >
          <Plus size={14} strokeWidth={2.6} /> Пополнить
        </button>
        <div className="flex items-center gap-2.5 px-1">
          <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-[linear-gradient(135deg,#6b8cff,#c14dff)] text-[11px] font-bold text-white">
            {user.initials}
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-xs font-semibold leading-none text-slate-200">
              {user.name}
            </div>
            <div className="mt-0.5 truncate text-[10px] text-slate-400">{user.email}</div>
          </div>
          <button
            type="button"
            onClick={onOpenSettings}
            className="text-slate-400 hover:text-slate-200"
            aria-label="настройки"
          >
            <Settings size={15} />
          </button>
        </div>

        <button
          type="button"
          onClick={onLogout}
          className="flex w-full items-center gap-1.5 px-1 text-[11px] text-slate-500 hover:text-rose-400 transition-colors"
        >
          <LogOut size={12} strokeWidth={2} />
          Выйти из аккаунта
        </button>
      </div>
    </aside>
  );
}
