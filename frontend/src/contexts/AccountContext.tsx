import { createContext, useContext, useState, useCallback, type ReactNode } from 'react'

interface AccountContextValue {
  selectedAccountId: string
  setSelectedAccountId: (id: string) => void
}

const AccountContext = createContext<AccountContextValue>({
  selectedAccountId: '',
  setSelectedAccountId: () => {},
})

export function useSelectedAccount() {
  return useContext(AccountContext)
}

export function AccountProvider({ children }: { children: ReactNode }) {
  const [selectedAccountId, setSelectedAccountIdState] = useState(() => localStorage.getItem('sis_account_id') ?? '')

  const setSelectedAccountId = useCallback((id: string) => {
    setSelectedAccountIdState(id)
    if (id) localStorage.setItem('sis_account_id', id)
    else localStorage.removeItem('sis_account_id')
  }, [])

  return (
    <AccountContext.Provider value={{ selectedAccountId, setSelectedAccountId }}>
      {children}
    </AccountContext.Provider>
  )
}
