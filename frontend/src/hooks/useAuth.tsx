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
  isAuthenticated: boolean
  login: (token: string, userId: string, email: string, persist?: boolean) => void
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

  function login(newToken: string, newUserId: string, newEmail: string, persist = true) {
    const store = persist ? localStorage : sessionStorage
    store.setItem('token', newToken)
    store.setItem('userId', newUserId)
    store.setItem('email', newEmail)
    setToken(newToken)
    setUserId(newUserId)
    setEmail(newEmail)
  }

  function logout() {
    localStorage.removeItem('token')
    localStorage.removeItem('userId')
    localStorage.removeItem('email')
    setToken(null)
    setUserId(null)
    setEmail(null)
  }

  return (
    <AuthContext.Provider
      value={{ token, userId, email, isAuthenticated: !!token, login, logout }}
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
