import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useTheme } from '../hooks/useTheme'
import { listAccounts } from '../api/accounts'
import type { ExchangeAccount } from '../types'
import { useState, useEffect, type ReactNode } from 'react'

const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Terminal', to: '/terminal' },
  { label: 'Webhooks', to: '/webhooks' },
  { label: 'Аккаунты', to: '/accounts' },
]

export function Layout({ children }: { children: ReactNode }) {
  const { pathname } = useLocation()
  const { logout, email } = useAuth()
  const navigate = useNavigate()
  const { theme, toggle } = useTheme()

  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [selectedId, setSelectedId] = useState<string>('')

  useEffect(() => {
    listAccounts().then(accs => {
      setAccounts(accs)
      if (accs.length > 0 && accs[0]) setSelectedId(accs[0].id)
    }).catch(() => {})
  }, [])

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-gray-100 dark:bg-gray-900">
      {/* Sidebar */}
      <aside className="w-[269px] bg-white dark:bg-gray-800 shadow flex flex-col" style={{ height: '100vh' }}>
        {/* Top zone — 15%: logo, user, platform balance, account picker */}
        <div className="flex flex-col justify-between px-3 py-3 border-b dark:border-gray-700 shrink-0 gap-1.5" style={{ height: '15%' }}>
          <span className="font-bold text-base text-blue-600 dark:text-blue-400 leading-none">Novabot</span>
          <span className="text-[11px] text-gray-400 truncate leading-none">
            {email ?? '—'}
          </span>
          <div className="flex items-center justify-between gap-1">
            <span className="text-sm font-semibold text-gray-800 dark:text-gray-100 leading-none">$0.00</span>
            <button className="text-[10px] px-1.5 py-0.5 rounded bg-blue-600 text-white hover:bg-blue-700 transition-colors leading-none">
              Пополнить
            </button>
          </div>
          {accounts.length > 0 && (
            <select
              value={selectedId}
              onChange={e => setSelectedId(e.target.value)}
              className="w-full bg-gray-100 dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded px-1.5 py-0.5 text-[11px] text-gray-700 dark:text-gray-200 focus:outline-none"
            >
              {accounts.map(a => (
                <option key={a.id} value={a.id}>{a.label}</option>
              ))}
            </select>
          )}
        </div>
        {/* Nav + footer — 85% */}
        <div className="flex flex-col" style={{ height: '85%' }}>
          <nav className="flex-1 px-2 py-2 space-y-1 overflow-y-auto">
            {navItems.map((item) => (
              <Link
                key={item.to}
                to={item.to}
                className={`block px-3 py-2 rounded text-sm font-medium ${
                  pathname === item.to
                    ? 'bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400'
                    : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-700'
                }`}
              >
                {item.label}
              </Link>
            ))}
          </nav>
          <div className="p-3 border-t dark:border-gray-700 shrink-0">
            <button
              onClick={toggle}
              className="w-full text-left px-3 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700 rounded"
            >
              {theme === 'dark' ? '☀ Светлая' : '☾ Тёмная'}
            </button>
            <button
              onClick={handleLogout}
              className="w-full text-left px-3 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700 rounded"
            >
              Sign out
            </button>
          </div>
        </div>
      </aside>
      {/* Main */}
      <main className={`flex-1 overflow-auto dark:text-gray-100 ${pathname === '/terminal' ? '' : 'p-6'}`}>{children}</main>
    </div>
  )
}
