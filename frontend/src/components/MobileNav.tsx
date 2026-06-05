import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import {
  LayoutDashboard, Terminal, BarChart2, Users, Shield,
  Bot, Webhook, LogOut, X, Menu as MenuIcon, Settings,
} from 'lucide-react'

const PRIMARY = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/terminal',  label: 'Terminal',  icon: Terminal },
  { to: '/signals',   label: 'Сигналы',   icon: BarChart2 },
  { to: '/accounts',  label: 'Api keys',  icon: Users },
]

const MORE = [
  { to: '/bots',      label: 'Боты',      icon: Bot },
  { to: '/webhooks',  label: 'Webhooks',  icon: Webhook },
  { to: '/admin',     label: 'Админка',   icon: Shield },
]

type Props = {
  onLogout: () => void
  onOpenSettings: () => void
  isAdmin?: boolean
}

export function MobileNav({ onLogout, onOpenSettings, isAdmin = false }: Props) {
  const { pathname } = useLocation()
  const [sheetOpen, setSheetOpen] = useState(false)

  const isActive = (to: string) => pathname === to || pathname.startsWith(to + '/')

  return (
    <>
      {/* Bottom bar */}
      <nav className="fixed bottom-0 left-0 right-0 z-50 flex h-16 items-center justify-around border-t border-white/[.06] bg-[#0c1018]/95 backdrop-blur md:hidden">
        {PRIMARY.map((item) => {
          const active = isActive(item.to)
          const Icon = item.icon
          return (
            <NavLink
              key={item.to}
              to={item.to}
              className="flex flex-col items-center gap-0.5 px-3 py-1"
            >
              <Icon
                size={20}
                strokeWidth={1.7}
                className={active ? 'text-[#5b8cff]' : 'text-slate-500'}
              />
              <span className={`text-[10px] font-medium ${active ? 'text-[#5b8cff]' : 'text-slate-500'}`}>
                {item.label}
              </span>
            </NavLink>
          )
        })}
        <button
          type="button"
          onClick={() => setSheetOpen(true)}
          className="flex flex-col items-center gap-0.5 px-3 py-1"
        >
          <MenuIcon size={20} strokeWidth={1.7} className="text-slate-500" />
          <span className="text-[10px] font-medium text-slate-500">Ещё</span>
        </button>
      </nav>

      {/* Sheet / Drawer */}
      {sheetOpen && (
        <div className="fixed inset-0 z-[60] md:hidden">
          <div
            className="absolute inset-0 bg-black/60"
            onClick={() => setSheetOpen(false)}
          />
          <div className="absolute bottom-0 left-0 right-0 rounded-t-2xl border-t border-white/[.08] bg-[#0c1018] p-4 shadow-[0_-8px_32px_rgba(0,0,0,.6)]">
            <div className="mb-3 flex items-center justify-between">
              <span className="text-sm font-semibold text-slate-200">Меню</span>
              <button
                type="button"
                onClick={() => setSheetOpen(false)}
                className="flex h-8 w-8 items-center justify-center rounded-md text-slate-400 hover:bg-white/[.05]"
              >
                <X size={16} />
              </button>
            </div>
            <div className="flex flex-col gap-1">
              {MORE.filter(item => item.to !== '/admin' || isAdmin).map((item) => {
                const active = isActive(item.to)
                const Icon = item.icon
                return (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    onClick={() => setSheetOpen(false)}
                    className={`flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${
                      active
                        ? 'bg-[#5b8cff]/[.12] text-[#b8c8ff]'
                        : 'text-slate-300 hover:bg-white/[.03]'
                    }`}
                  >
                    <Icon size={18} strokeWidth={1.7} className={active ? 'text-[#b8c8ff]' : 'text-slate-500'} />
                    {item.label}
                  </NavLink>
                )
              })}
              <div className="my-1 h-px bg-white/[.06]" />
              <button
                type="button"
                onClick={() => { setSheetOpen(false); onOpenSettings() }}
                className="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-300 transition-colors hover:bg-white/[.03]"
              >
                <Settings size={18} strokeWidth={1.7} className="text-slate-500" />
                Настройки
              </button>
              <button
                type="button"
                onClick={() => { setSheetOpen(false); onLogout() }}
                className="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium text-slate-300 transition-colors hover:bg-white/[.03]"
              >
                <LogOut size={18} strokeWidth={1.7} className="text-slate-500" />
                Выйти
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
