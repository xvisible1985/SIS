import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import type { ReactNode } from 'react'

const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Terminal', to: '/terminal' },
  { label: 'Webhooks', to: '/webhooks' },
]

export function Layout({ children }: { children: ReactNode }) {
  const { pathname } = useLocation()
  const { logout } = useAuth()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-gray-100">
      {/* Sidebar */}
      <aside className="w-48 bg-white shadow flex flex-col">
        <div className="px-4 py-5 font-bold text-lg text-blue-600">SIS</div>
        <nav className="flex-1 px-2 space-y-1">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className={`block px-3 py-2 rounded text-sm font-medium ${
                pathname === item.to
                  ? 'bg-blue-50 text-blue-600'
                  : 'text-gray-700 hover:bg-gray-50'
              }`}
            >
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="p-3 border-t">
          <button
            onClick={handleLogout}
            className="w-full text-left px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 rounded"
          >
            Sign out
          </button>
        </div>
      </aside>
      {/* Main */}
      <main className={`flex-1 overflow-auto ${pathname === '/terminal' ? '' : 'p-6'}`}>{children}</main>
    </div>
  )
}
