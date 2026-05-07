import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useTheme } from '../hooks/useTheme'
import { listAccounts, getAccountBalance } from '../api/accounts'
import { Sidebar } from './Sidebar'
import type { ExchangeAccount } from '../types'
import { useState, useEffect, type ReactNode } from 'react'

export function Layout({ children }: { children: ReactNode }) {
  const { pathname } = useLocation()
  const { logout, email } = useAuth()
  const navigate = useNavigate()
  const { theme, toggle } = useTheme()

  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [selectedId, setSelectedId] = useState<string>('')
  const [equity, setEquity] = useState(0)

  useEffect(() => {
    listAccounts().then(accs => {
      setAccounts(accs)
      if (accs.length > 0 && accs[0]) setSelectedId(accs[0].id)
    }).catch(() => {})
  }, [])

  useEffect(() => {
    if (!selectedId) return
    getAccountBalance(selectedId).then(res => {
      if (res.ok && res.equity != null) setEquity(res.equity)
    }).catch(() => {})
  }, [selectedId])

  const selectedAccount = accounts.find(a => a.id === selectedId)

  // derive user display info from email
  const userEmail = email ?? ''
  const userName = userEmail.split('@')[0] ?? ''
  const userInitials = userName.slice(0, 2).toUpperCase() || '??'

  function handleLogout() {
    logout()
    navigate('/login')
  }

  // theme toggle button lives outside sidebar for now — keep it accessible via keyboard shortcut later
  void theme; void toggle; void pathname

  return (
    <div className="flex h-screen bg-[#0a0d14]">
      <Sidebar
        version="v0.1.0"
        account={selectedAccount
          ? {
              exchangeBadge: selectedAccount.exchange[0]?.toUpperCase() ?? 'B',
              name: selectedAccount.label,
              exchange: selectedAccount.exchange,
              status: selectedAccount.is_active ? 'подключено' : 'отключено',
            }
          : { exchangeBadge: '?', name: 'Нет аккаунта', exchange: 'Bybit', status: 'отключено' }
        }
        equity={equity}
        pnl24h={{ percent: 0, usd: 0 }}
        spark={[0, 0]}
        novabotBalance={0}
        user={{ initials: userInitials, name: userName, email: userEmail }}
        counters={{ accounts: accounts.length }}
        onSelectAccount={() => {
          const idx = accounts.findIndex(a => a.id === selectedId)
          const next = accounts[(idx + 1) % accounts.length]
          if (next) setSelectedId(next.id)
        }}
        onTopUp={() => navigate('/billing')}
        onOpenSettings={() => {
          handleLogout()
        }}
      />
      <main className={`flex-1 overflow-auto dark:text-gray-100 ${pathname === '/terminal' ? '' : 'p-6'}`}>
        {children}
      </main>
    </div>
  )
}
