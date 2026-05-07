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
  login: (token: string, userId: string, email: string) => void
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(
    () => localStorage.getItem('token')
  )
  const [userId, setUserId] = useState<string | null>(
    () => localStorage.getItem('userId')
  )
  const [email, setEmail] = useState<string | null>(
    () => localStorage.getItem('email')
  )

  function login(newToken: string, newUserId: string, newEmail: string) {
    localStorage.setItem('token', newToken)
    localStorage.setItem('userId', newUserId)
    localStorage.setItem('email', newEmail)
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
