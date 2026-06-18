import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useTheme } from '../hooks/useTheme'
import { listAccounts, getAccountBalance, getAccountPositions } from '../api/accounts'
import { getProfile } from '../api/account'
import { Sidebar } from './Sidebar'
import { MobileNav } from './MobileNav'
import { DepositModal } from './DepositModal'
import { AdminUserPickerBar } from './AdminUserPickerBar'
import { useSelectedAccount } from '../contexts/AccountContext'
import { useWalletWs } from '../hooks/useWalletWs'
import { isImpersonating } from '../contexts/ImpersonationContext'
import type { ExchangeAccount } from '../types'
import { useState, useEffect, type ReactNode } from 'react'

export function Layout({ children }: { children: ReactNode }) {
  const { pathname } = useLocation()
  const { logout, email, isAdmin } = useAuth()
  const navigate = useNavigate()
  const { theme, toggle } = useTheme()

  const { selectedAccountId, setSelectedAccountId } = useSelectedAccount()
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [equity, setEquity] = useState(0)
  const wallet = useWalletWs(selectedAccountId)
  const [pnl24h, setPnl24h] = useState({ percent: 0, usd: 0 })
  const [has24hData, setHas24hData] = useState(false)
  const [novabotBalance, setNovabotBalance] = useState(0)
  const [depositOpen, setDepositOpen] = useState(false)

  // Show admin bar when user is admin OR when in impersonation mode
  const showAdminBar = isAdmin || isImpersonating()

  function loadAccounts() {
    listAccounts().then(accs => {
      const active = accs.filter(a => a.is_active)
      setAccounts(active)
      setSelectedAccountId(
        active.find(a => a.id === selectedAccountId)
          ? selectedAccountId
          : active[0]?.id ?? ''
      )
    }).catch(() => {})
  }

  useEffect(() => {
    loadAccounts()
    window.addEventListener('accounts-changed', loadAccounts)
    return () => window.removeEventListener('accounts-changed', loadAccounts)
  }, [])

  useEffect(() => {
    getProfile().then(p => {
      if (p.novabot_balance != null) setNovabotBalance(p.novabot_balance)
    }).catch(() => {})
  }, [])

  useEffect(() => {
    if (!selectedAccountId) return
    Promise.all([
      getAccountBalance(selectedAccountId),
      getAccountPositions(selectedAccountId),
    ]).then(([balanceRes, posRes]) => {
      if (balanceRes.ok && balanceRes.equity != null) {
        setEquity(balanceRes.equity)
        if (balanceRes.equity_change_percent != null && balanceRes.equity_change_usd != null) {
          setPnl24h({ percent: balanceRes.equity_change_percent, usd: balanceRes.equity_change_usd })
          setHas24hData(true)
        } else {
          const positions = posRes.ok ? (posRes.positions ?? []) : []
          const totalPnl = positions.reduce((sum, p) => sum + parseFloat(p.unrealisedPnl || '0'), 0)
          const pct = balanceRes.equity > 0 ? (totalPnl / balanceRes.equity) * 100 : 0
          setPnl24h({ percent: pct, usd: totalPnl })
          setHas24hData(false)
        }
      }
    }).catch(() => {})
  }, [selectedAccountId])

  // Keep equity in sync with live WS wallet updates (overrides the REST snapshot)
  useEffect(() => {
    if (wallet.equity != null && wallet.equity > 0) setEquity(wallet.equity)
  }, [wallet.equity])

  const selectedAccount = accounts.find(a => a.id === selectedAccountId)

  const userEmail = email ?? ''
  const userName = userEmail.split('@')[0] ?? ''
  const userInitials = userName.slice(0, 2).toUpperCase() || '??'

  function handleLogout() {
    logout()
    navigate('/login')
  }

  void theme; void toggle; void pathname

  return (
    <div className="flex h-screen bg-[#0a0d14]">
      <div className="hidden h-full md:flex">
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
          pickerAccounts={accounts.map(a => ({
            id: a.id,
            exchangeBadge: a.exchange[0]?.toUpperCase() ?? '?',
            name: a.label,
            exchange: a.exchange,
          }))}
          selectedAccountId={selectedAccountId}
          equity={equity}
          pnl24h={pnl24h}
          has24hData={has24hData}
          spark={[0, 0]}
          novabotBalance={novabotBalance}
          user={{ initials: userInitials, name: userName, email: userEmail }}
          noActiveAccounts={accounts.length === 0}
          counters={{ accounts: accounts.length }}
          isAdmin={isAdmin}
          onSelectAccount={(id) => setSelectedAccountId(id)}
          onTopUp={() => setDepositOpen(true)}
          onOpenSettings={() => navigate('/account')}
          onLogout={handleLogout}
        />
      </div>
      <MobileNav isAdmin={isAdmin} onLogout={handleLogout} onOpenSettings={() => navigate('/account')} />
      <DepositModal open={depositOpen} onClose={() => setDepositOpen(false)} />

      {/* Right column: admin bar (all pages for admins/impersonation) + page content */}
      <div className="flex flex-1 flex-col min-w-0 h-full">
        {showAdminBar && <AdminUserPickerBar />}
        <main className={
          pathname === '/terminal' || pathname === '/signal-chart'
            ? 'flex-1 h-full overflow-hidden dark:text-gray-100 min-h-0'
            : 'flex-1 overflow-auto pb-16 dark:text-gray-100 md:pb-0 p-4 md:p-6'
        }>
          {children}
        </main>
      </div>
    </div>
  )
}
