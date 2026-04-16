import {
  createContext,
  useContext,
  useState,
  ReactNode,
} from 'react'

interface AuthState {
  token: string | null
  userId: string | null
  isAuthenticated: boolean
  login: (token: string, userId: string) => void
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

  function login(newToken: string, newUserId: string) {
    localStorage.setItem('token', newToken)
    localStorage.setItem('userId', newUserId)
    setToken(newToken)
    setUserId(newUserId)
  }

  function logout() {
    localStorage.removeItem('token')
    localStorage.removeItem('userId')
    setToken(null)
    setUserId(null)
  }

  return (
    <AuthContext.Provider
      value={{ token, userId, isAuthenticated: !!token, login, logout }}
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
