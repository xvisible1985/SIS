import {
  createContext,
  useContext,
  useState,
  ReactNode,
} from 'react'

interface AuthState {
  token: string | null
  userId: string | null
  email: string | null
  isAdmin: boolean
  isAuthenticated: boolean
  login: (token: string, userId: string, email: string, isAdmin?: boolean, persist?: boolean) => void
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(
    () => localStorage.getItem('token') ?? sessionStorage.getItem('token')
  )
  const [userId, setUserId] = useState<string | null>(
    () => localStorage.getItem('userId') ?? sessionStorage.getItem('userId')
  )
  const [email, setEmail] = useState<string | null>(
    () => localStorage.getItem('email') ?? sessionStorage.getItem('email')
  )
  const [isAdmin, setIsAdmin] = useState<boolean>(
    () => localStorage.getItem('isAdmin') === 'true' || sessionStorage.getItem('isAdmin') === 'true'
  )

  function login(newToken: string, newUserId: string, newEmail: string, newIsAdmin = false, persist = true) {
    const store = persist ? localStorage : sessionStorage
    store.setItem('token', newToken)
    store.setItem('userId', newUserId)
    store.setItem('email', newEmail)
    store.setItem('isAdmin', String(newIsAdmin))
    setToken(newToken)
    setUserId(newUserId)
    setEmail(newEmail)
    setIsAdmin(newIsAdmin)
  }

  function logout() {
    localStorage.removeItem('token')
    localStorage.removeItem('userId')
    localStorage.removeItem('email')
    localStorage.removeItem('isAdmin')
    setToken(null)
    setUserId(null)
    setEmail(null)
    setIsAdmin(false)
  }

  return (
    <AuthContext.Provider
      value={{ token, userId, email, isAdmin, isAuthenticated: !!token, login, logout }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
