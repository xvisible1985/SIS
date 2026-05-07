import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useTheme } from '../hooks/useTheme'
import type { ReactNode } from 'react'

const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Terminal', to: '/terminal' },
  { label: 'Webhooks', to: '/webhooks' },
  { label: 'Аккаунты', to: '/accounts' },
]

export function Layout({ children }: { children: ReactNode }) {
  const { pathname } = useLocation()
  const { logout } = useAuth()
  const navigate = useNavigate()
  const { theme, toggle } = useTheme()

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-gray-100 dark:bg-gray-900">
      {/* Sidebar */}
      <aside className="w-48 bg-white dark:bg-gray-800 shadow flex flex-col" style={{ height: '100vh' }}>
        {/* Logo — 15% */}
        <div className="flex items-center px-4 border-b dark:border-gray-700 shrink-0" style={{ height: '15%' }}>
          <span className="font-bold text-lg text-blue-600 dark:text-blue-400">SIS</span>
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
