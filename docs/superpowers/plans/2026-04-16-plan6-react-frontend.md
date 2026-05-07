# React Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a React + TypeScript SPA that provides full UI for auth, signal management, backtest/optimize workflows, and webhook management — wired to the existing api-gateway at `localhost:8080`.

**Architecture:** Vite 6 + React 18 SPA living in `frontend/`. Axios handles REST with a JWT interceptor (token stored in `localStorage`). React Router v6 handles routing with a `ProtectedRoute` wrapper. `useAuth` context provides auth state. All pages talk to the api-gateway via Vite's dev proxy (`/auth`, `/signals`, `/webhooks`, `/ws` → `localhost:8080`). The **Chart screen is out of scope** — it requires a `/candles` API endpoint not built yet.

**Tech Stack:** React 18, TypeScript 5, Vite 6, React Router v6, Axios, Tailwind CSS 3, Vitest 2, @testing-library/react 16, jsdom

---

## File Structure

| File | Purpose |
|------|---------|
| `frontend/package.json` | npm deps and scripts |
| `frontend/vite.config.ts` | Vite build + dev proxy + Vitest config |
| `frontend/tsconfig.json` | TypeScript strict mode |
| `frontend/tailwind.config.js` | Tailwind content scan |
| `frontend/postcss.config.js` | PostCSS for Tailwind |
| `frontend/index.html` | Root HTML entry |
| `frontend/src/main.tsx` | ReactDOM.createRoot mount |
| `frontend/src/index.css` | Tailwind directives |
| `frontend/src/App.tsx` | React Router route definitions |
| `frontend/src/setupTests.ts` | Vitest globals + jest-dom matchers |
| `frontend/src/types.ts` | All shared TypeScript types |
| `frontend/src/api/client.ts` | Axios instance with JWT interceptor |
| `frontend/src/api/auth.ts` | `login()`, `register()` |
| `frontend/src/api/signals.ts` | Signal CRUD + backtest + optimize |
| `frontend/src/api/webhooks.ts` | Webhook CRUD |
| `frontend/src/hooks/useAuth.tsx` | `AuthContext` + `AuthProvider` + `useAuth()` |
| `frontend/src/hooks/useJobProgress.ts` | WebSocket job progress hook |
| `frontend/src/components/Layout.tsx` | Sidebar nav + main content wrapper |
| `frontend/src/components/ProtectedRoute.tsx` | Redirect to `/login` if not authenticated |
| `frontend/src/components/ConditionTree.tsx` | Recursive AND/OR tree builder |
| `frontend/src/components/ProgressBar.tsx` | Animated progress bar |
| `frontend/src/pages/LoginPage.tsx` | Login form |
| `frontend/src/pages/RegisterPage.tsx` | Registration form |
| `frontend/src/pages/DashboardPage.tsx` | Signal list overview |
| `frontend/src/pages/SignalBuilderPage.tsx` | Create/edit signal + condition tree |
| `frontend/src/pages/BacktestPage.tsx` | Submit backtest + WS progress + results |
| `frontend/src/pages/OptimizerPage.tsx` | Submit optimize + WS progress + results |
| `frontend/src/pages/WebhooksPage.tsx` | Webhook CRUD list |

---

## API Reference (actual backend responses)

**Auth:**
- `POST /auth/register` → `{ token: string, user_id: string }` (201)
- `POST /auth/login` → `{ token: string, user_id: string }` (200)

**Signals:**
- `GET /signals` → `Signal[]`
- `POST /signals` → `Signal` (201) — body: `{ name, description, exchange, symbol, market, timeframe, direction, conditions }`
- `GET /signals/:id` → `Signal`
- `PUT /signals/:id` → `Signal` — body: `{ name?, description?, direction?, conditions?, is_active? }`
- `DELETE /signals/:id` → 204

**Jobs:**
- `POST /signals/:id/backtest` → `{ job_id: string }` (202) — body: `{ period_from, period_to, take_profit, stop_loss }`
- `POST /signals/:id/optimize` → `{ job_id: string }` (202) — body: `{ period_from, period_to, mode, score_by, top_n, take_profits, stop_losses, param_space, wf_folds }`
- `GET /signals/:id/backtest-results` → `BacktestResult[]`
- `GET /signals/:id/optimization-results` → `OptimizationResult[]`

**Webhooks:**
- `GET /webhooks` → `Webhook[]`
- `POST /webhooks` → `Webhook` (201) — body: `{ signal_id, url, platform? }`
- `PUT /webhooks/:id` → `Webhook` — body: `{ url?, platform?, is_active? }`
- `DELETE /webhooks/:id` → 204

**WebSocket:**
- `GET /ws/jobs/:id/progress?type=backtest|optimize&token=<jwt>` → frames: `{ pct: number, status: string, updated_at: number }`

---

### Task 1: Project Bootstrap

**Files:**
- Create: `frontend/package.json`
- Create: `frontend/vite.config.ts`
- Create: `frontend/tsconfig.json`
- Create: `frontend/tailwind.config.js`
- Create: `frontend/postcss.config.js`
- Create: `frontend/index.html`
- Create: `frontend/src/main.tsx`
- Create: `frontend/src/index.css`
- Create: `frontend/src/App.tsx`
- Create: `frontend/src/setupTests.ts`
- Create: `frontend/src/__tests__/App.test.tsx`

- [ ] **Step 1: Create `frontend/package.json`**

```json
{
  "name": "sis-frontend",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "axios": "^1.7.9",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.27.0"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.6.3",
    "@testing-library/react": "^16.1.0",
    "@testing-library/user-event": "^14.5.2",
    "@types/react": "^18.3.12",
    "@types/react-dom": "^18.3.1",
    "@vitejs/plugin-react": "^4.3.4",
    "autoprefixer": "^10.4.20",
    "jsdom": "^25.0.1",
    "postcss": "^8.4.49",
    "tailwindcss": "^3.4.17",
    "typescript": "^5.7.2",
    "vite": "^6.0.7",
    "vitest": "^2.1.8"
  }
}
```

- [ ] **Step 2: Create `frontend/vite.config.ts`**

```typescript
/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/setupTests.ts'],
  },
  server: {
    proxy: {
      '/auth': 'http://localhost:8080',
      '/signals': 'http://localhost:8080',
      '/webhooks': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
})
```

- [ ] **Step 3: Create `frontend/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true
  },
  "include": ["src"]
}
```

- [ ] **Step 4: Create Tailwind/PostCSS config files**

`frontend/tailwind.config.js`:
```javascript
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: { extend: {} },
  plugins: [],
}
```

`frontend/postcss.config.js`:
```javascript
export default {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
}
```

- [ ] **Step 5: Create `frontend/index.html`**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>SIS — Signal Analyzer</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 6: Create `frontend/src/index.css`**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;
```

- [ ] **Step 7: Create `frontend/src/main.tsx`**

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
```

- [ ] **Step 8: Create initial `frontend/src/App.tsx`**

```tsx
export default function App() {
  return <div className="p-4">SIS — Signal Analyzer</div>
}
```

- [ ] **Step 9: Create `frontend/src/setupTests.ts`**

```typescript
import '@testing-library/jest-dom'
```

- [ ] **Step 10: Write smoke test `frontend/src/__tests__/App.test.tsx`**

```tsx
import { render, screen } from '@testing-library/react'
import App from '../App'

test('renders without crashing', () => {
  render(<App />)
  expect(screen.getByText('SIS — Signal Analyzer')).toBeInTheDocument()
})
```

- [ ] **Step 11: Run test to verify it fails (module not found before install)**

```bash
cd frontend && npx vitest run 2>&1 | head -5
```
Expected: Error about missing modules (vitest not installed yet)

- [ ] **Step 12: Install dependencies**

```bash
cd frontend && npm install
```
Expected: `added NNN packages`

- [ ] **Step 13: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/App.test.tsx > renders without crashing
Test Files  1 passed (1)
Tests  1 passed (1)
```

- [ ] **Step 14: Commit**

```bash
cd frontend && git add -A && git -C .. add frontend/
git -C .. commit -m "feat: bootstrap React frontend (Vite + TS + Tailwind + Vitest)"
```

---

### Task 2: Types + API Client

**Files:**
- Create: `frontend/src/types.ts`
- Create: `frontend/src/api/client.ts`
- Create: `frontend/src/api/auth.ts`
- Create: `frontend/src/__tests__/api.auth.test.ts`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/api.auth.test.ts`**

```typescript
import { describe, test, expect, vi, beforeEach } from 'vitest'

vi.mock('../api/client', () => ({
  apiClient: { post: vi.fn() },
}))

import { apiClient } from '../api/client'
import { login, register } from '../api/auth'

describe('auth API', () => {
  beforeEach(() => vi.clearAllMocks())

  test('login calls POST /auth/login', async () => {
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { token: 'tok1', user_id: 'uid1' },
    })
    const result = await login('a@b.com', 'pass1234')
    expect(apiClient.post).toHaveBeenCalledWith('/auth/login', {
      email: 'a@b.com',
      password: 'pass1234',
    })
    expect(result).toEqual({ token: 'tok1', user_id: 'uid1' })
  })

  test('register calls POST /auth/register', async () => {
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { token: 'tok2', user_id: 'uid2' },
    })
    const result = await register('b@c.com', 'pass1234')
    expect(apiClient.post).toHaveBeenCalledWith('/auth/register', {
      email: 'b@c.com',
      password: 'pass1234',
    })
    expect(result).toEqual({ token: 'tok2', user_id: 'uid2' })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Error|Cannot find"
```
Expected: `Cannot find module '../api/client'`

- [ ] **Step 3: Create `frontend/src/types.ts`**

```typescript
// Auth
export interface AuthResponse {
  token: string
  user_id: string
}

// Signals
export interface Signal {
  id: string
  name: string
  description: string
  exchange: string
  symbol: string
  market: string
  timeframe: string
  direction: string
  conditions: ConditionNode
  is_active: boolean
  created_at: string
}

export type ConditionNode = GroupNode | ConditionLeaf | SignalRefNode

export interface GroupNode {
  type: 'AND' | 'OR'
  children: ConditionNode[]
}

export interface ConditionLeaf {
  type: 'condition'
  indicator: string
  params: Record<string, number>
  operator: string
  value?: number
  compare_to?: { indicator: string; params: Record<string, number> }
}

export interface SignalRefNode {
  type: 'signal_ref'
  signal_id: string
}

// Backtest
export interface BacktestRequest {
  period_from: string
  period_to: string
  take_profit: number
  stop_loss: number
}

export interface BacktestResult {
  id: string
  signal_id: string
  symbol: string
  timeframe: string
  period_from: string
  period_to: string
  mode: string
  total_signals: number
  win_count: number
  loss_count: number
  win_rate: number
  avg_gain: number
  max_drawdown: number
  profit_factor: number
  patterns: unknown
  created_at: string
}

// Optimizer
export interface OptimizeRequest {
  period_from: string
  period_to: string
  mode: 'fast' | 'walk_forward'
  score_by: string
  top_n: number
  take_profits: number[]
  stop_losses: number[]
  param_space: Record<string, number[]>
  wf_folds: number
}

export interface RankedResult {
  params: Record<string, number>
  take_profit: number
  stop_loss: number
  score: number
  win_rate: number
  avg_gain: number
  profit_factor: number
  total_signals: number
}

export interface OptimizationResult {
  id: string
  signal_id: string
  mode: string
  top_combinations: RankedResult[]
  best_params: Record<string, number>
  created_at: string
}

// Webhooks
export interface Webhook {
  id: string
  signal_id: string
  url: string
  platform: string
  is_active: boolean
  created_at: string
}

// Job progress (WebSocket frame)
export interface ProgressMessage {
  pct: number
  status: string
  updated_at: number
}
```

- [ ] **Step 4: Create `frontend/src/api/client.ts`**

```typescript
import axios from 'axios'

export const apiClient = axios.create({
  baseURL: '',
  headers: { 'Content-Type': 'application/json' },
})

// Attach JWT from localStorage on every request
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers = config.headers ?? {}
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})
```

- [ ] **Step 5: Create `frontend/src/api/auth.ts`**

```typescript
import { apiClient } from './client'
import type { AuthResponse } from '../types'

export async function login(email: string, password: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/login', { email, password })
  return res.data
}

export async function register(email: string, password: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/register', { email, password })
  return res.data
}
```

- [ ] **Step 6: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/api.auth.test.ts > auth API > login calls POST /auth/login
✓ src/__tests__/api.auth.test.ts > auth API > register calls POST /auth/register
✓ src/__tests__/App.test.tsx > renders without crashing
Test Files  2 passed (2)
Tests  3 passed (3)
```

- [ ] **Step 7: Commit**

```bash
git -C .. add frontend/src/types.ts frontend/src/api/ frontend/src/__tests__/api.auth.test.ts
git -C .. commit -m "feat: types, axios client with JWT interceptor, auth API"
```

---

### Task 3: useAuth Hook

**Files:**
- Create: `frontend/src/hooks/useAuth.tsx`
- Create: `frontend/src/__tests__/useAuth.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/useAuth.test.tsx`**

```tsx
import { render, screen, act } from '@testing-library/react'
import { AuthProvider, useAuth } from '../hooks/useAuth'

function TestWidget() {
  const { token, userId, isAuthenticated, login, logout } = useAuth()
  return (
    <div>
      <span data-testid="authed">{isAuthenticated ? 'yes' : 'no'}</span>
      <span data-testid="token">{token ?? 'none'}</span>
      <span data-testid="userId">{userId ?? 'none'}</span>
      <button onClick={() => login('tok123', 'uid456')}>do-login</button>
      <button onClick={logout}>do-logout</button>
    </div>
  )
}

function wrapped() {
  return render(
    <AuthProvider>
      <TestWidget />
    </AuthProvider>
  )
}

beforeEach(() => localStorage.clear())

test('starts unauthenticated when localStorage is empty', () => {
  wrapped()
  expect(screen.getByTestId('authed').textContent).toBe('no')
  expect(screen.getByTestId('token').textContent).toBe('none')
})

test('restores session from localStorage on mount', () => {
  localStorage.setItem('token', 'saved-tok')
  localStorage.setItem('userId', 'saved-uid')
  wrapped()
  expect(screen.getByTestId('authed').textContent).toBe('yes')
  expect(screen.getByTestId('token').textContent).toBe('saved-tok')
  expect(screen.getByTestId('userId').textContent).toBe('saved-uid')
})

test('login sets auth state and persists to localStorage', () => {
  wrapped()
  act(() => { screen.getByText('do-login').click() })
  expect(screen.getByTestId('authed').textContent).toBe('yes')
  expect(screen.getByTestId('token').textContent).toBe('tok123')
  expect(screen.getByTestId('userId').textContent).toBe('uid456')
  expect(localStorage.getItem('token')).toBe('tok123')
  expect(localStorage.getItem('userId')).toBe('uid456')
})

test('logout clears auth state and removes from localStorage', () => {
  localStorage.setItem('token', 'tok123')
  localStorage.setItem('userId', 'uid456')
  wrapped()
  act(() => { screen.getByText('do-logout').click() })
  expect(screen.getByTestId('authed').textContent).toBe('no')
  expect(localStorage.getItem('token')).toBeNull()
  expect(localStorage.getItem('userId')).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test -- --reporter=verbose 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../hooks/useAuth'`

- [ ] **Step 3: Create `frontend/src/hooks/useAuth.tsx`**

```tsx
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
```

- [ ] **Step 4: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/useAuth.test.tsx (4 tests)
Test Files  3 passed (3)
Tests  7 passed (7)
```

- [ ] **Step 5: Commit**

```bash
git -C .. add frontend/src/hooks/useAuth.tsx frontend/src/__tests__/useAuth.test.tsx
git -C .. commit -m "feat: useAuth context with localStorage persistence"
```

---

### Task 4: Login/Register Pages + Layout + Routing

**Files:**
- Create: `frontend/src/pages/LoginPage.tsx`
- Create: `frontend/src/pages/RegisterPage.tsx`
- Create: `frontend/src/components/ProtectedRoute.tsx`
- Create: `frontend/src/components/Layout.tsx`
- Modify: `frontend/src/App.tsx`
- Create: `frontend/src/__tests__/LoginPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/LoginPage.test.tsx`**

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { LoginPage } from '../pages/LoginPage'
import { AuthProvider } from '../hooks/useAuth'
import * as authApi from '../api/auth'

vi.mock('../api/auth')

function renderLogin() {
  return render(
    <AuthProvider>
      <MemoryRouter initialEntries={['/login']}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<div>dashboard</div>} />
        </Routes>
      </MemoryRouter>
    </AuthProvider>
  )
}

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

test('renders email and password fields', () => {
  renderLogin()
  expect(screen.getByPlaceholderText('Email')).toBeInTheDocument()
  expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument()
})

test('calls login API and redirects on success', async () => {
  vi.mocked(authApi.login).mockResolvedValue({ token: 'tok', user_id: 'uid1' })
  renderLogin()
  fireEvent.change(screen.getByPlaceholderText('Email'), {
    target: { value: 'a@b.com' },
  })
  fireEvent.change(screen.getByPlaceholderText('Password'), {
    target: { value: 'password123' },
  })
  fireEvent.click(screen.getByRole('button', { name: /sign in/i }))
  await waitFor(() =>
    expect(authApi.login).toHaveBeenCalledWith('a@b.com', 'password123')
  )
  await waitFor(() =>
    expect(screen.getByText('dashboard')).toBeInTheDocument()
  )
})

test('shows error message on failed login', async () => {
  vi.mocked(authApi.login).mockRejectedValue(new Error('invalid credentials'))
  renderLogin()
  fireEvent.change(screen.getByPlaceholderText('Email'), {
    target: { value: 'a@b.com' },
  })
  fireEvent.change(screen.getByPlaceholderText('Password'), {
    target: { value: 'wrongpass' },
  })
  fireEvent.click(screen.getByRole('button', { name: /sign in/i }))
  await waitFor(() =>
    expect(screen.getByText(/invalid credentials/i)).toBeInTheDocument()
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test -- --reporter=verbose 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../pages/LoginPage'`

- [ ] **Step 3: Create `frontend/src/pages/LoginPage.tsx`**

```tsx
import { useState, FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { login } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

export function LoginPage() {
  const navigate = useNavigate()
  const { login: authLogin } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await login(email, password)
      authLogin(res.token, res.user_id)
      navigate('/')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Login failed'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="w-full max-w-sm bg-white rounded-xl shadow p-8">
        <h1 className="text-2xl font-bold mb-6 text-center">SIS</h1>
        <form onSubmit={handleSubmit} className="space-y-4">
          <input
            type="email"
            placeholder="Email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            className="w-full border rounded px-3 py-2 text-sm"
          />
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="w-full border rounded px-3 py-2 text-sm"
          />
          {error && <p className="text-red-600 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full bg-blue-600 text-white rounded px-3 py-2 text-sm font-medium disabled:opacity-50"
          >
            {loading ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        <p className="mt-4 text-center text-sm text-gray-600">
          No account?{' '}
          <Link to="/register" className="text-blue-600 hover:underline">
            Register
          </Link>
        </p>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Create `frontend/src/pages/RegisterPage.tsx`**

```tsx
import { useState, FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { register } from '../api/auth'
import { useAuth } from '../hooks/useAuth'

export function RegisterPage() {
  const navigate = useNavigate()
  const { login: authLogin } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await register(email, password)
      authLogin(res.token, res.user_id)
      navigate('/')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Registration failed'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50">
      <div className="w-full max-w-sm bg-white rounded-xl shadow p-8">
        <h1 className="text-2xl font-bold mb-6 text-center">Create Account</h1>
        <form onSubmit={handleSubmit} className="space-y-4">
          <input
            type="email"
            placeholder="Email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            className="w-full border rounded px-3 py-2 text-sm"
          />
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={8}
            className="w-full border rounded px-3 py-2 text-sm"
          />
          {error && <p className="text-red-600 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full bg-blue-600 text-white rounded px-3 py-2 text-sm font-medium disabled:opacity-50"
          >
            {loading ? 'Creating account…' : 'Create account'}
          </button>
        </form>
        <p className="mt-4 text-center text-sm text-gray-600">
          Have an account?{' '}
          <Link to="/login" className="text-blue-600 hover:underline">
            Sign in
          </Link>
        </p>
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Create `frontend/src/components/ProtectedRoute.tsx`**

```tsx
import { Navigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import type { ReactNode } from 'react'

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth()
  if (!isAuthenticated) return <Navigate to="/login" replace />
  return <>{children}</>
}
```

- [ ] **Step 6: Create `frontend/src/components/Layout.tsx`**

```tsx
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import type { ReactNode } from 'react'

const navItems = [
  { label: 'Dashboard', to: '/' },
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
      <main className="flex-1 overflow-auto p-6">{children}</main>
    </div>
  )
}
```

- [ ] **Step 7: Replace `frontend/src/App.tsx` with full routing**

```tsx
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './hooks/useAuth'
import { ProtectedRoute } from './components/ProtectedRoute'
import { Layout } from './components/Layout'
import { LoginPage } from './pages/LoginPage'
import { RegisterPage } from './pages/RegisterPage'
import { DashboardPage } from './pages/DashboardPage'
import { SignalBuilderPage } from './pages/SignalBuilderPage'
import { BacktestPage } from './pages/BacktestPage'
import { OptimizerPage } from './pages/OptimizerPage'
import { WebhooksPage } from './pages/WebhooksPage'

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route
            path="/*"
            element={
              <ProtectedRoute>
                <Layout>
                  <Routes>
                    <Route path="/" element={<DashboardPage />} />
                    <Route path="/signals/new" element={<SignalBuilderPage />} />
                    <Route path="/signals/:id/edit" element={<SignalBuilderPage />} />
                    <Route path="/signals/:id/backtest" element={<BacktestPage />} />
                    <Route path="/signals/:id/optimize" element={<OptimizerPage />} />
                    <Route path="/webhooks" element={<WebhooksPage />} />
                    <Route path="*" element={<Navigate to="/" replace />} />
                  </Routes>
                </Layout>
              </ProtectedRoute>
            }
          />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
```

> **Note:** App.tsx imports pages that don't exist yet. Create stub files for each to unblock compilation:

Create these stubs now (they'll be replaced in later tasks):

`frontend/src/pages/DashboardPage.tsx`:
```tsx
export function DashboardPage() { return <div>Dashboard</div> }
```

`frontend/src/pages/SignalBuilderPage.tsx`:
```tsx
export function SignalBuilderPage() { return <div>Signal Builder</div> }
```

`frontend/src/pages/BacktestPage.tsx`:
```tsx
export function BacktestPage() { return <div>Backtest</div> }
```

`frontend/src/pages/OptimizerPage.tsx`:
```tsx
export function OptimizerPage() { return <div>Optimizer</div> }
```

`frontend/src/pages/WebhooksPage.tsx`:
```tsx
export function WebhooksPage() { return <div>Webhooks</div> }
```

- [ ] **Step 8: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/LoginPage.test.tsx (3 tests)
✓ src/__tests__/useAuth.test.tsx (4 tests)
✓ src/__tests__/api.auth.test.ts (2 tests)
✓ src/__tests__/App.test.tsx (1 test)
Test Files  4 passed (4)
Tests  10 passed (10)
```

- [ ] **Step 9: Commit**

```bash
git -C .. add frontend/src/
git -C .. commit -m "feat: Login/Register pages, Layout, ProtectedRoute, full routing"
```

---

### Task 5: Dashboard Page

**Files:**
- Create: `frontend/src/api/signals.ts` (listSignals only)
- Modify: `frontend/src/pages/DashboardPage.tsx` (full implementation)
- Create: `frontend/src/__tests__/DashboardPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/DashboardPage.test.tsx`**

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { DashboardPage } from '../pages/DashboardPage'
import * as signalsApi from '../api/signals'
import type { Signal } from '../types'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1', isAuthenticated: true }),
}))

const fakeSignal: Signal = {
  id: 's1',
  name: 'RSI Signal',
  description: 'desc',
  exchange: 'binance',
  symbol: 'BTCUSDT',
  market: 'spot',
  timeframe: '1h',
  direction: 'LONG',
  conditions: { type: 'AND', children: [] },
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

beforeEach(() => vi.clearAllMocks())

test('shows loading state initially', () => {
  vi.mocked(signalsApi.listSignals).mockReturnValue(new Promise(() => {}))
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  expect(screen.getByText(/loading/i)).toBeInTheDocument()
})

test('renders signal list after load', async () => {
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getByText('RSI Signal')).toBeInTheDocument())
  expect(screen.getByText('BTCUSDT')).toBeInTheDocument()
  expect(screen.getByText('1h')).toBeInTheDocument()
})

test('shows empty state when no signals', async () => {
  vi.mocked(signalsApi.listSignals).mockResolvedValue([])
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getByText(/no signals yet/i)).toBeInTheDocument())
  expect(screen.getByRole('link', { name: /create signal/i })).toBeInTheDocument()
})

test('shows error state on fetch failure', async () => {
  vi.mocked(signalsApi.listSignals).mockRejectedValue(new Error('network error'))
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getByText(/network error/i)).toBeInTheDocument())
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../api/signals'`

- [ ] **Step 3: Create `frontend/src/api/signals.ts`**

```typescript
import { apiClient } from './client'
import type {
  Signal,
  BacktestRequest,
  OptimizeRequest,
  BacktestResult,
  OptimizationResult,
} from '../types'

export async function listSignals(): Promise<Signal[]> {
  const res = await apiClient.get<Signal[]>('/signals')
  return res.data
}

export async function getSignal(id: string): Promise<Signal> {
  const res = await apiClient.get<Signal>(`/signals/${id}`)
  return res.data
}

export async function createSignal(
  data: Omit<Signal, 'id' | 'is_active' | 'created_at'>
): Promise<Signal> {
  const res = await apiClient.post<Signal>('/signals', data)
  return res.data
}

export async function updateSignal(
  id: string,
  data: Partial<Pick<Signal, 'name' | 'description' | 'direction' | 'conditions' | 'is_active'>>
): Promise<Signal> {
  const res = await apiClient.put<Signal>(`/signals/${id}`, data)
  return res.data
}

export async function deleteSignal(id: string): Promise<void> {
  await apiClient.delete(`/signals/${id}`)
}

export async function submitBacktest(
  signalId: string,
  req: BacktestRequest
): Promise<{ job_id: string }> {
  const res = await apiClient.post<{ job_id: string }>(
    `/signals/${signalId}/backtest`,
    req
  )
  return res.data
}

export async function submitOptimize(
  signalId: string,
  req: OptimizeRequest
): Promise<{ job_id: string }> {
  const res = await apiClient.post<{ job_id: string }>(
    `/signals/${signalId}/optimize`,
    req
  )
  return res.data
}

export async function getBacktestResults(signalId: string): Promise<BacktestResult[]> {
  const res = await apiClient.get<BacktestResult[]>(`/signals/${signalId}/backtest-results`)
  return res.data
}

export async function getOptimizationResults(signalId: string): Promise<OptimizationResult[]> {
  const res = await apiClient.get<OptimizationResult[]>(
    `/signals/${signalId}/optimization-results`
  )
  return res.data
}
```

- [ ] **Step 4: Replace `frontend/src/pages/DashboardPage.tsx` with full implementation**

```tsx
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listSignals } from '../api/signals'
import type { Signal } from '../types'

export function DashboardPage() {
  const [signals, setSignals] = useState<Signal[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    listSignals()
      .then(setSignals)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-gray-500">Loading…</p>
  if (error) return <p className="text-red-600">{error}</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold">Signals</h1>
        <Link
          to="/signals/new"
          className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium hover:bg-blue-700"
        >
          Create signal
        </Link>
      </div>

      {signals.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="mb-4">No signals yet.</p>
          <Link to="/signals/new" className="text-blue-600 hover:underline">
            Create signal
          </Link>
        </div>
      ) : (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-gray-600 uppercase text-xs">
              <tr>
                <th className="px-4 py-3 text-left">Name</th>
                <th className="px-4 py-3 text-left">Symbol</th>
                <th className="px-4 py-3 text-left">Timeframe</th>
                <th className="px-4 py-3 text-left">Direction</th>
                <th className="px-4 py-3 text-left">Status</th>
                <th className="px-4 py-3 text-left">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {signals.map((sig) => (
                <tr key={sig.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3 font-medium">{sig.name}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.symbol}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.timeframe}</td>
                  <td className="px-4 py-3 text-gray-600">{sig.direction}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                        sig.is_active
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-100 text-gray-600'
                      }`}
                    >
                      {sig.is_active ? 'Active' : 'Inactive'}
                    </span>
                  </td>
                  <td className="px-4 py-3 space-x-2">
                    <Link
                      to={`/signals/${sig.id}/edit`}
                      className="text-blue-600 hover:underline"
                    >
                      Edit
                    </Link>
                    <Link
                      to={`/signals/${sig.id}/backtest`}
                      className="text-gray-600 hover:underline"
                    >
                      Backtest
                    </Link>
                    <Link
                      to={`/signals/${sig.id}/optimize`}
                      className="text-gray-600 hover:underline"
                    >
                      Optimize
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/DashboardPage.test.tsx (4 tests)
Test Files  5 passed (5)
Tests  14 passed (14)
```

- [ ] **Step 6: Commit**

```bash
git -C .. add frontend/src/api/signals.ts frontend/src/pages/DashboardPage.tsx frontend/src/__tests__/DashboardPage.test.tsx
git -C .. commit -m "feat: Dashboard page with signal list"
```

---

### Task 6: Signal Builder + ConditionTree

**Files:**
- Create: `frontend/src/components/ConditionTree.tsx`
- Modify: `frontend/src/pages/SignalBuilderPage.tsx` (full implementation)
- Create: `frontend/src/__tests__/ConditionTree.test.tsx`
- Create: `frontend/src/__tests__/SignalBuilderPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/ConditionTree.test.tsx`**

```tsx
import { render, screen, fireEvent } from '@testing-library/react'
import { ConditionTree } from '../components/ConditionTree'
import type { GroupNode, ConditionLeaf } from '../types'

test('renders AND group with action buttons', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  render(<ConditionTree value={root} onChange={() => {}} />)
  expect(screen.getByText('AND')).toBeInTheDocument()
  expect(screen.getByText('+ Condition')).toBeInTheDocument()
  expect(screen.getByText('+ Group')).toBeInTheDocument()
})

test('toggles AND to OR when group label clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('AND'))
  expect(onChange).toHaveBeenCalledWith({ type: 'OR', children: [] })
})

test('adds a default condition when + Condition clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('+ Condition'))
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [
      {
        type: 'condition',
        indicator: 'RSI',
        params: { period: 14 },
        operator: '<',
        value: 50,
      },
    ],
  })
})

test('adds a nested group when + Group clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('+ Group'))
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [{ type: 'OR', children: [] }],
  })
})

test('removes a child condition when Remove clicked', () => {
  const leaf: ConditionLeaf = {
    type: 'condition',
    indicator: 'RSI',
    params: { period: 14 },
    operator: '<',
    value: 50,
  }
  const root: GroupNode = { type: 'AND', children: [leaf] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('Remove'))
  expect(onChange).toHaveBeenCalledWith({ type: 'AND', children: [] })
})

test('updates indicator select on change', () => {
  const leaf: ConditionLeaf = {
    type: 'condition',
    indicator: 'RSI',
    params: { period: 14 },
    operator: '<',
    value: 50,
  }
  const root: GroupNode = { type: 'AND', children: [leaf] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.change(screen.getByDisplayValue('RSI'), { target: { value: 'EMA' } })
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [{ ...leaf, indicator: 'EMA' }],
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../components/ConditionTree'`

- [ ] **Step 3: Create `frontend/src/components/ConditionTree.tsx`**

```tsx
import type { ConditionNode, GroupNode, ConditionLeaf } from '../types'

const INDICATORS = ['RSI', 'MACD', 'EMA', 'SMA', 'BB', 'Volume', 'ATR', 'Stochastic']
const OPERATORS = ['<', '>', '=', '!=', 'crosses_above', 'crosses_below', '% change >', 'relative_to']

// --- Leaf ---

interface LeafViewProps {
  node: ConditionLeaf
  onChange: (n: ConditionNode) => void
  onRemove: () => void
}

function LeafView({ node, onChange, onRemove }: LeafViewProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 p-2 bg-white border rounded text-sm">
      <select
        value={node.indicator}
        onChange={(e) => onChange({ ...node, indicator: e.target.value })}
        className="border rounded px-2 py-1"
      >
        {INDICATORS.map((i) => <option key={i}>{i}</option>)}
      </select>
      <input
        type="number"
        placeholder="period"
        value={node.params.period ?? ''}
        onChange={(e) =>
          onChange({ ...node, params: { ...node.params, period: Number(e.target.value) } })
        }
        className="border rounded px-2 py-1 w-20"
      />
      <select
        value={node.operator}
        onChange={(e) => onChange({ ...node, operator: e.target.value })}
        className="border rounded px-2 py-1"
      >
        {OPERATORS.map((o) => <option key={o}>{o}</option>)}
      </select>
      <input
        type="number"
        placeholder="value"
        value={node.value ?? ''}
        onChange={(e) => onChange({ ...node, value: Number(e.target.value) })}
        className="border rounded px-2 py-1 w-24"
      />
      <button
        onClick={onRemove}
        className="ml-auto text-red-500 hover:text-red-700 text-xs"
      >
        Remove
      </button>
    </div>
  )
}

// --- Group ---

interface GroupViewProps {
  node: GroupNode
  onChange: (n: GroupNode) => void
  onRemove?: () => void
}

function GroupView({ node, onChange, onRemove }: GroupViewProps) {
  function toggleType() {
    onChange({ ...node, type: node.type === 'AND' ? 'OR' : 'AND' })
  }

  function addCondition() {
    const newLeaf: ConditionLeaf = {
      type: 'condition',
      indicator: 'RSI',
      params: { period: 14 },
      operator: '<',
      value: 50,
    }
    onChange({ ...node, children: [...node.children, newLeaf] })
  }

  function addGroup() {
    const newGroup: GroupNode = { type: 'OR', children: [] }
    onChange({ ...node, children: [...node.children, newGroup] })
  }

  function updateChild(idx: number) {
    return (child: ConditionNode) => {
      const children = [...node.children]
      children[idx] = child
      onChange({ ...node, children })
    }
  }

  function removeChild(idx: number) {
    return () => onChange({ ...node, children: node.children.filter((_, i) => i !== idx) })
  }

  return (
    <div className="border-l-2 border-blue-300 pl-3 space-y-2">
      <div className="flex items-center gap-2">
        <button
          onClick={toggleType}
          className="px-2 py-0.5 rounded bg-blue-100 text-blue-700 text-xs font-bold hover:bg-blue-200"
        >
          {node.type}
        </button>
        {onRemove && (
          <button
            onClick={onRemove}
            className="text-red-500 hover:text-red-700 text-xs"
          >
            Remove group
          </button>
        )}
      </div>
      <div className="space-y-2 pl-2">
        {node.children.map((child, idx) => (
          <NodeView
            key={idx}
            node={child}
            onChange={updateChild(idx)}
            onRemove={removeChild(idx)}
          />
        ))}
      </div>
      <div className="flex gap-2 pt-1">
        <button
          onClick={addCondition}
          className="text-xs text-blue-600 hover:underline"
        >
          + Condition
        </button>
        <button
          onClick={addGroup}
          className="text-xs text-blue-600 hover:underline"
        >
          + Group
        </button>
      </div>
    </div>
  )
}

// --- Dispatcher ---

interface NodeViewProps {
  node: ConditionNode
  onChange: (n: ConditionNode) => void
  onRemove?: () => void
}

function NodeView({ node, onChange, onRemove }: NodeViewProps) {
  if (node.type === 'AND' || node.type === 'OR') {
    return (
      <GroupView
        node={node}
        onChange={(n) => onChange(n)}
        onRemove={onRemove}
      />
    )
  }
  if (node.type === 'condition') {
    return (
      <LeafView node={node} onChange={onChange} onRemove={onRemove!} />
    )
  }
  return null
}

// --- Public export ---

interface ConditionTreeProps {
  value: ConditionNode
  onChange: (v: ConditionNode) => void
}

export function ConditionTree({ value, onChange }: ConditionTreeProps) {
  // Ensure root is always a group
  const root: GroupNode =
    value.type === 'AND' || value.type === 'OR'
      ? (value as GroupNode)
      : { type: 'AND', children: [value] }

  return <GroupView node={root} onChange={onChange} />
}
```

- [ ] **Step 4: Write failing test `frontend/src/__tests__/SignalBuilderPage.test.tsx`**

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { SignalBuilderPage } from '../pages/SignalBuilderPage'
import * as signalsApi from '../api/signals'
import type { Signal } from '../types'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1', isAuthenticated: true }),
}))

const fakeSignal: Signal = {
  id: 's1',
  name: 'My Signal',
  description: '',
  exchange: 'binance',
  symbol: 'BTCUSDT',
  market: 'spot',
  timeframe: '1h',
  direction: 'LONG',
  conditions: { type: 'AND', children: [] },
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

beforeEach(() => vi.clearAllMocks())

function renderNew() {
  return render(
    <MemoryRouter initialEntries={['/signals/new']}>
      <Routes>
        <Route path="/signals/new" element={<SignalBuilderPage />} />
        <Route path="/" element={<div>dashboard</div>} />
      </Routes>
    </MemoryRouter>
  )
}

function renderEdit() {
  vi.mocked(signalsApi.getSignal).mockResolvedValue(fakeSignal)
  return render(
    <MemoryRouter initialEntries={['/signals/s1/edit']}>
      <Routes>
        <Route path="/signals/:id/edit" element={<SignalBuilderPage />} />
        <Route path="/" element={<div>dashboard</div>} />
      </Routes>
    </MemoryRouter>
  )
}

test('new signal form has required fields', () => {
  renderNew()
  expect(screen.getByPlaceholderText('Signal name')).toBeInTheDocument()
  expect(screen.getByLabelText(/symbol/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/timeframe/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument()
})

test('creates signal and redirects to dashboard', async () => {
  vi.mocked(signalsApi.createSignal).mockResolvedValue(fakeSignal)
  renderNew()
  fireEvent.change(screen.getByPlaceholderText('Signal name'), {
    target: { value: 'My Signal' },
  })
  fireEvent.click(screen.getByRole('button', { name: /save/i }))
  await waitFor(() =>
    expect(signalsApi.createSignal).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'My Signal' })
    )
  )
  await waitFor(() =>
    expect(screen.getByText('dashboard')).toBeInTheDocument()
  )
})

test('edit mode loads existing signal data', async () => {
  renderEdit()
  await waitFor(() =>
    expect(screen.getByDisplayValue('My Signal')).toBeInTheDocument()
  )
})

test('edit mode calls updateSignal on save', async () => {
  vi.mocked(signalsApi.updateSignal).mockResolvedValue(fakeSignal)
  renderEdit()
  await waitFor(() => screen.getByDisplayValue('My Signal'))
  fireEvent.click(screen.getByRole('button', { name: /save/i }))
  await waitFor(() =>
    expect(signalsApi.updateSignal).toHaveBeenCalledWith('s1', expect.any(Object))
  )
})
```

- [ ] **Step 5: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Cannot find|expected"
```
Expected: test failures (SignalBuilderPage is a stub)

- [ ] **Step 6: Replace `frontend/src/pages/SignalBuilderPage.tsx` with full implementation**

```tsx
import { useEffect, useState, FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { createSignal, getSignal, updateSignal } from '../api/signals'
import { ConditionTree } from '../components/ConditionTree'
import type { ConditionNode, GroupNode } from '../types'

const EXCHANGES = ['binance', 'bybit']
const MARKETS = ['spot', 'futures']
const TIMEFRAMES = ['1m', '5m', '15m', '1h', '4h', '1d']
const DIRECTIONS = ['LONG', 'SHORT', 'BOTH']

const DEFAULT_CONDITIONS: GroupNode = { type: 'AND', children: [] }

export function SignalBuilderPage() {
  const { id } = useParams<{ id: string }>()
  const isEdit = !!id
  const navigate = useNavigate()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [exchange, setExchange] = useState('binance')
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [market, setMarket] = useState('spot')
  const [timeframe, setTimeframe] = useState('1h')
  const [direction, setDirection] = useState('LONG')
  const [conditions, setConditions] = useState<ConditionNode>(DEFAULT_CONDITIONS)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!isEdit) return
    getSignal(id).then((sig) => {
      setName(sig.name)
      setDescription(sig.description)
      setExchange(sig.exchange)
      setSymbol(sig.symbol)
      setMarket(sig.market)
      setTimeframe(sig.timeframe)
      setDirection(sig.direction)
      setConditions(sig.conditions)
    })
  }, [id, isEdit])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      if (isEdit) {
        await updateSignal(id, { name, description, direction, conditions })
      } else {
        await createSignal({ name, description, exchange, symbol, market, timeframe, direction, conditions })
      }
      navigate('/')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="max-w-2xl">
      <h1 className="text-xl font-semibold mb-6">
        {isEdit ? 'Edit Signal' : 'New Signal'}
      </h1>
      <form onSubmit={handleSubmit} className="space-y-4">
        <input
          type="text"
          placeholder="Signal name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
          className="w-full border rounded px-3 py-2 text-sm"
        />
        <input
          type="text"
          placeholder="Description (optional)"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="w-full border rounded px-3 py-2 text-sm"
        />

        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-gray-600 mb-1">Exchange</label>
            <select
              value={exchange}
              onChange={(e) => setExchange(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {EXCHANGES.map((x) => <option key={x}>{x}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Market</label>
            <select
              value={market}
              onChange={(e) => setMarket(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {MARKETS.map((m) => <option key={m}>{m}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="symbol-input">Symbol</label>
            <input
              id="symbol-input"
              type="text"
              value={symbol}
              onChange={(e) => setSymbol(e.target.value.toUpperCase())}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="timeframe-select">Timeframe</label>
            <select
              id="timeframe-select"
              value={timeframe}
              onChange={(e) => setTimeframe(e.target.value)}
              disabled={isEdit}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {TIMEFRAMES.map((t) => <option key={t}>{t}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Direction</label>
            <select
              value={direction}
              onChange={(e) => setDirection(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {DIRECTIONS.map((d) => <option key={d}>{d}</option>)}
            </select>
          </div>
        </div>

        <div>
          <p className="text-sm font-medium text-gray-700 mb-2">Conditions</p>
          <ConditionTree value={conditions} onChange={setConditions} />
        </div>

        {error && <p className="text-red-600 text-sm">{error}</p>}

        <div className="flex gap-3">
          <button
            type="submit"
            disabled={loading}
            className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {loading ? 'Saving…' : 'Save signal'}
          </button>
          <button
            type="button"
            onClick={() => navigate('/')}
            className="border rounded px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}
```

- [ ] **Step 7: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/ConditionTree.test.tsx (6 tests)
✓ src/__tests__/SignalBuilderPage.test.tsx (4 tests)
Test Files  7 passed (7)
Tests  24 passed (24)
```

- [ ] **Step 8: Commit**

```bash
git -C .. add frontend/src/components/ConditionTree.tsx frontend/src/pages/SignalBuilderPage.tsx frontend/src/__tests__/
git -C .. commit -m "feat: ConditionTree component and Signal Builder page"
```

---

### Task 7: Backtest Page + useJobProgress

**Files:**
- Create: `frontend/src/hooks/useJobProgress.ts`
- Create: `frontend/src/components/ProgressBar.tsx`
- Modify: `frontend/src/pages/BacktestPage.tsx` (full implementation)
- Create: `frontend/src/__tests__/BacktestPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/BacktestPage.test.tsx`**

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { BacktestPage } from '../pages/BacktestPage'
import * as signalsApi from '../api/signals'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))
vi.mock('../hooks/useJobProgress', () => ({
  useJobProgress: () => ({ pct: 0, status: '' }),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/signals/s1/backtest']}>
      <Routes>
        <Route path="/signals/:id/backtest" element={<BacktestPage />} />
      </Routes>
    </MemoryRouter>
  )
}

beforeEach(() => vi.clearAllMocks())

test('renders backtest form fields', () => {
  renderPage()
  expect(screen.getByLabelText(/period from/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/period to/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/take profit/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/stop loss/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /run backtest/i })).toBeInTheDocument()
})

test('submits backtest with correct params', async () => {
  vi.mocked(signalsApi.submitBacktest).mockResolvedValue({ job_id: 'job1' })
  vi.mocked(signalsApi.getBacktestResults).mockResolvedValue([])
  renderPage()

  fireEvent.change(screen.getByLabelText(/period from/i), {
    target: { value: '2025-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/period to/i), {
    target: { value: '2026-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/take profit/i), {
    target: { value: '3' },
  })
  fireEvent.change(screen.getByLabelText(/stop loss/i), {
    target: { value: '1.5' },
  })
  fireEvent.click(screen.getByRole('button', { name: /run backtest/i }))

  await waitFor(() =>
    expect(signalsApi.submitBacktest).toHaveBeenCalledWith('s1', {
      period_from: '2025-01-01',
      period_to: '2026-01-01',
      take_profit: 3,
      stop_loss: 1.5,
    })
  )
})

test('loads existing results on mount', async () => {
  vi.mocked(signalsApi.getBacktestResults).mockResolvedValue([
    {
      id: 'r1',
      signal_id: 's1',
      symbol: 'BTCUSDT',
      timeframe: '1h',
      period_from: '2025-01-01T00:00:00Z',
      period_to: '2026-01-01T00:00:00Z',
      mode: 'fast',
      total_signals: 42,
      win_count: 28,
      loss_count: 14,
      win_rate: 0.667,
      avg_gain: 1.23,
      max_drawdown: 5.4,
      profit_factor: 2.1,
      patterns: null,
      created_at: '2026-01-01T00:00:00Z',
    },
  ])
  renderPage()
  await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  expect(screen.getByText('66.7%')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../hooks/useJobProgress'` or test failures

- [ ] **Step 3: Create `frontend/src/hooks/useJobProgress.ts`**

```typescript
import { useEffect, useState } from 'react'
import type { ProgressMessage } from '../types'

export function useJobProgress(
  jobId: string | null,
  type: 'backtest' | 'optimize'
): ProgressMessage {
  const [progress, setProgress] = useState<ProgressMessage>({
    pct: 0,
    status: '',
    updated_at: 0,
  })

  useEffect(() => {
    if (!jobId) return

    const token = localStorage.getItem('token') ?? ''
    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${wsProtocol}//${window.location.host}/ws/jobs/${jobId}/progress?type=${type}&token=${encodeURIComponent(token)}`

    const ws = new WebSocket(url)

    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string) as ProgressMessage
        setProgress(msg)
      } catch {
        // ignore malformed frames
      }
    }

    ws.onerror = () => ws.close()

    return () => ws.close()
  }, [jobId, type])

  return progress
}
```

- [ ] **Step 4: Create `frontend/src/components/ProgressBar.tsx`**

```tsx
interface ProgressBarProps {
  pct: number
  status: string
}

export function ProgressBar({ pct, status }: ProgressBarProps) {
  if (!status) return null
  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs text-gray-600">
        <span className="capitalize">{status}</span>
        <span>{pct}%</span>
      </div>
      <div className="h-2 bg-gray-200 rounded overflow-hidden">
        <div
          className="h-full bg-blue-500 transition-all duration-300"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Replace `frontend/src/pages/BacktestPage.tsx` with full implementation**

```tsx
import { useEffect, useState, FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { submitBacktest, getBacktestResults } from '../api/signals'
import { useJobProgress } from '../hooks/useJobProgress'
import { ProgressBar } from '../components/ProgressBar'
import type { BacktestResult } from '../types'

export function BacktestPage() {
  const { id } = useParams<{ id: string }>()
  const [periodFrom, setPeriodFrom] = useState('2025-01-01')
  const [periodTo, setPeriodTo] = useState('2026-01-01')
  const [takeProfit, setTakeProfit] = useState(2)
  const [stopLoss, setStopLoss] = useState(1)
  const [jobId, setJobId] = useState<string | null>(null)
  const [results, setResults] = useState<BacktestResult[]>([])
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const progress = useJobProgress(jobId, 'backtest')

  // Load previous results
  useEffect(() => {
    if (!id) return
    getBacktestResults(id).then(setResults).catch(() => {})
  }, [id])

  // Reload results when job completes
  useEffect(() => {
    if (progress.status === 'done' && id) {
      getBacktestResults(id).then(setResults).catch(() => {})
      setJobId(null)
    }
  }, [progress.status, id])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const res = await submitBacktest(id!, {
        period_from: periodFrom,
        period_to: periodTo,
        take_profit: takeProfit,
        stop_loss: stopLoss,
      })
      setJobId(res.job_id)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to submit')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-xl font-semibold">Backtest</h1>

      <div className="bg-white rounded-xl shadow p-5">
        <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="period-from">
              Period From
            </label>
            <input
              id="period-from"
              type="date"
              value={periodFrom}
              onChange={(e) => setPeriodFrom(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="period-to">
              Period To
            </label>
            <input
              id="period-to"
              type="date"
              value={periodTo}
              onChange={(e) => setPeriodTo(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="take-profit">
              Take Profit %
            </label>
            <input
              id="take-profit"
              type="number"
              step="0.1"
              min="0.1"
              value={takeProfit}
              onChange={(e) => setTakeProfit(Number(e.target.value))}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="stop-loss">
              Stop Loss %
            </label>
            <input
              id="stop-loss"
              type="number"
              step="0.1"
              min="0.1"
              value={stopLoss}
              onChange={(e) => setStopLoss(Number(e.target.value))}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div className="col-span-2">
            {error && <p className="text-red-600 text-sm mb-2">{error}</p>}
            <button
              type="submit"
              disabled={submitting || !!jobId}
              className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
            >
              {submitting ? 'Submitting…' : 'Run backtest'}
            </button>
          </div>
        </form>
      </div>

      {jobId && (
        <div className="bg-white rounded-xl shadow p-5">
          <p className="text-sm font-medium mb-3">Running…</p>
          <ProgressBar pct={progress.pct} status={progress.status} />
        </div>
      )}

      {results.length > 0 && (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <div className="px-5 py-3 border-b">
            <h2 className="font-medium text-sm">Results</h2>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-left">Date</th>
                <th className="px-4 py-2 text-right">Total</th>
                <th className="px-4 py-2 text-right">Win Rate</th>
                <th className="px-4 py-2 text-right">Avg Gain %</th>
                <th className="px-4 py-2 text-right">Max DD %</th>
                <th className="px-4 py-2 text-right">Profit Factor</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {results.map((r) => (
                <tr key={r.id}>
                  <td className="px-4 py-2 text-gray-600">
                    {new Date(r.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-2 text-right">{r.total_signals}</td>
                  <td className="px-4 py-2 text-right">
                    {(r.win_rate * 100).toFixed(1)}%
                  </td>
                  <td className="px-4 py-2 text-right">{r.avg_gain.toFixed(2)}</td>
                  <td className="px-4 py-2 text-right">{r.max_drawdown.toFixed(2)}</td>
                  <td className="px-4 py-2 text-right">{r.profit_factor.toFixed(2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 6: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/BacktestPage.test.tsx (3 tests)
Test Files  8 passed (8)
Tests  27 passed (27)
```

- [ ] **Step 7: Commit**

```bash
git -C .. add frontend/src/hooks/useJobProgress.ts frontend/src/components/ProgressBar.tsx frontend/src/pages/BacktestPage.tsx frontend/src/__tests__/BacktestPage.test.tsx
git -C .. commit -m "feat: Backtest page with WebSocket progress tracking"
```

---

### Task 8: Optimizer Page

**Files:**
- Modify: `frontend/src/pages/OptimizerPage.tsx` (full implementation)
- Create: `frontend/src/__tests__/OptimizerPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/OptimizerPage.test.tsx`**

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { OptimizerPage } from '../pages/OptimizerPage'
import * as signalsApi from '../api/signals'
import type { OptimizationResult } from '../types'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))
vi.mock('../hooks/useJobProgress', () => ({
  useJobProgress: () => ({ pct: 0, status: '' }),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/signals/s1/optimize']}>
      <Routes>
        <Route path="/signals/:id/optimize" element={<OptimizerPage />} />
      </Routes>
    </MemoryRouter>
  )
}

const fakeResult: OptimizationResult = {
  id: 'o1',
  signal_id: 's1',
  mode: 'fast',
  top_combinations: [
    {
      params: { rsi_period: 14 },
      take_profit: 2,
      stop_loss: 1,
      score: 0.85,
      win_rate: 0.65,
      avg_gain: 1.4,
      profit_factor: 2.3,
      total_signals: 55,
    },
  ],
  best_params: { rsi_period: 14 },
  created_at: '2026-01-01T00:00:00Z',
}

beforeEach(() => vi.clearAllMocks())

test('renders optimizer form', () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([])
  renderPage()
  expect(screen.getByLabelText(/period from/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/period to/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /run optimizer/i })).toBeInTheDocument()
})

test('submits optimization with correct payload', async () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([])
  vi.mocked(signalsApi.submitOptimize).mockResolvedValue({ job_id: 'job2' })
  renderPage()

  fireEvent.change(screen.getByLabelText(/period from/i), {
    target: { value: '2025-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/period to/i), {
    target: { value: '2026-01-01' },
  })
  fireEvent.click(screen.getByRole('button', { name: /run optimizer/i }))

  await waitFor(() =>
    expect(signalsApi.submitOptimize).toHaveBeenCalledWith(
      's1',
      expect.objectContaining({ period_from: '2025-01-01', period_to: '2026-01-01' })
    )
  )
})

test('shows top combinations table from past results', async () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([fakeResult])
  renderPage()
  await waitFor(() => expect(screen.getByText('0.85')).toBeInTheDocument())
  expect(screen.getByText('65.0%')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|expected"
```
Expected: test failures (OptimizerPage is a stub)

- [ ] **Step 3: Replace `frontend/src/pages/OptimizerPage.tsx` with full implementation**

```tsx
import { useEffect, useState, FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { submitOptimize, getOptimizationResults } from '../api/signals'
import { useJobProgress } from '../hooks/useJobProgress'
import { ProgressBar } from '../components/ProgressBar'
import type { OptimizationResult } from '../types'

export function OptimizerPage() {
  const { id } = useParams<{ id: string }>()
  const [periodFrom, setPeriodFrom] = useState('2025-01-01')
  const [periodTo, setPeriodTo] = useState('2026-01-01')
  const [mode, setMode] = useState<'fast' | 'walk_forward'>('fast')
  const [takeProfit, setTakeProfit] = useState('1.5,2.0,3.0')
  const [stopLoss, setStopLoss] = useState('0.5,1.0,1.5')
  const [jobId, setJobId] = useState<string | null>(null)
  const [results, setResults] = useState<OptimizationResult[]>([])
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const progress = useJobProgress(jobId, 'optimize')

  useEffect(() => {
    if (!id) return
    getOptimizationResults(id).then(setResults).catch(() => {})
  }, [id])

  useEffect(() => {
    if (progress.status === 'done' && id) {
      getOptimizationResults(id).then(setResults).catch(() => {})
      setJobId(null)
    }
  }, [progress.status, id])

  function parseList(s: string): number[] {
    return s
      .split(',')
      .map((v) => Number(v.trim()))
      .filter((v) => !isNaN(v) && v > 0)
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      const res = await submitOptimize(id!, {
        period_from: periodFrom,
        period_to: periodTo,
        mode,
        score_by: 'profit_factor',
        top_n: 10,
        take_profits: parseList(takeProfit),
        stop_losses: parseList(stopLoss),
        param_space: {},
        wf_folds: 5,
      })
      setJobId(res.job_id)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to submit')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-xl font-semibold">Optimizer</h1>

      <div className="bg-white rounded-xl shadow p-5">
        <form onSubmit={handleSubmit} className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="opt-period-from">
              Period From
            </label>
            <input
              id="opt-period-from"
              type="date"
              value={periodFrom}
              onChange={(e) => setPeriodFrom(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1" htmlFor="opt-period-to">
              Period To
            </label>
            <input
              id="opt-period-to"
              type="date"
              value={periodTo}
              onChange={(e) => setPeriodTo(e.target.value)}
              required
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Mode</label>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as 'fast' | 'walk_forward')}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              <option value="fast">Fast (grid search)</option>
              <option value="walk_forward">Walk-Forward</option>
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">
              Take Profit values (comma-separated %)
            </label>
            <input
              type="text"
              value={takeProfit}
              onChange={(e) => setTakeProfit(e.target.value)}
              placeholder="1.5,2.0,3.0"
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">
              Stop Loss values (comma-separated %)
            </label>
            <input
              type="text"
              value={stopLoss}
              onChange={(e) => setStopLoss(e.target.value)}
              placeholder="0.5,1.0,1.5"
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          <div className="col-span-2">
            {error && <p className="text-red-600 text-sm mb-2">{error}</p>}
            <button
              type="submit"
              disabled={submitting || !!jobId}
              className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
            >
              {submitting ? 'Submitting…' : 'Run optimizer'}
            </button>
          </div>
        </form>
      </div>

      {jobId && (
        <div className="bg-white rounded-xl shadow p-5">
          <p className="text-sm font-medium mb-3">Optimizing…</p>
          <ProgressBar pct={progress.pct} status={progress.status} />
        </div>
      )}

      {results.map((result) => (
        <div key={result.id} className="bg-white rounded-xl shadow overflow-hidden">
          <div className="px-5 py-3 border-b flex items-center gap-3">
            <h2 className="font-medium text-sm">
              Top Combinations — {result.mode} —{' '}
              {new Date(result.created_at).toLocaleDateString()}
            </h2>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-right">#</th>
                <th className="px-4 py-2 text-right">Score</th>
                <th className="px-4 py-2 text-right">Win Rate</th>
                <th className="px-4 py-2 text-right">Profit Factor</th>
                <th className="px-4 py-2 text-right">TP %</th>
                <th className="px-4 py-2 text-right">SL %</th>
                <th className="px-4 py-2 text-right">Total</th>
                <th className="px-4 py-2 text-left">Params</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {result.top_combinations.map((combo, idx) => (
                <tr key={idx}>
                  <td className="px-4 py-2 text-right text-gray-500">{idx + 1}</td>
                  <td className="px-4 py-2 text-right font-medium">
                    {combo.score.toFixed(2)}
                  </td>
                  <td className="px-4 py-2 text-right">
                    {(combo.win_rate * 100).toFixed(1)}%
                  </td>
                  <td className="px-4 py-2 text-right">
                    {combo.profit_factor.toFixed(2)}
                  </td>
                  <td className="px-4 py-2 text-right">{combo.take_profit}</td>
                  <td className="px-4 py-2 text-right">{combo.stop_loss}</td>
                  <td className="px-4 py-2 text-right">{combo.total_signals}</td>
                  <td className="px-4 py-2 text-left text-xs text-gray-600">
                    {Object.entries(combo.params)
                      .map(([k, v]) => `${k}=${v}`)
                      .join(', ')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/OptimizerPage.test.tsx (3 tests)
Test Files  9 passed (9)
Tests  30 passed (30)
```

- [ ] **Step 5: Commit**

```bash
git -C .. add frontend/src/pages/OptimizerPage.tsx frontend/src/__tests__/OptimizerPage.test.tsx
git -C .. commit -m "feat: Optimizer page with parameter ranges and results table"
```

---

### Task 9: Webhooks Page

**Files:**
- Create: `frontend/src/api/webhooks.ts`
- Modify: `frontend/src/pages/WebhooksPage.tsx` (full implementation)
- Create: `frontend/src/__tests__/WebhooksPage.test.tsx`

- [ ] **Step 1: Write failing test `frontend/src/__tests__/WebhooksPage.test.tsx`**

```tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { WebhooksPage } from '../pages/WebhooksPage'
import * as webhooksApi from '../api/webhooks'
import * as signalsApi from '../api/signals'
import type { Webhook, Signal } from '../types'

vi.mock('../api/webhooks')
vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))

const fakeSignal: Signal = {
  id: 's1',
  name: 'RSI Signal',
  description: '',
  exchange: 'binance',
  symbol: 'BTCUSDT',
  market: 'spot',
  timeframe: '1h',
  direction: 'LONG',
  conditions: { type: 'AND', children: [] },
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

const fakeWebhook: Webhook = {
  id: 'w1',
  signal_id: 's1',
  url: 'https://example.com/hook',
  platform: 'custom',
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

function renderPage() {
  return render(<MemoryRouter><WebhooksPage /></MemoryRouter>)
}

beforeEach(() => vi.clearAllMocks())

test('shows webhook list', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  renderPage()
  await waitFor(() =>
    expect(screen.getByText('https://example.com/hook')).toBeInTheDocument()
  )
  expect(screen.getByText('custom')).toBeInTheDocument()
})

test('shows empty state when no webhooks', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  renderPage()
  await waitFor(() =>
    expect(screen.getByText(/no webhooks/i)).toBeInTheDocument()
  )
})

test('creates a webhook', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  vi.mocked(webhooksApi.createWebhook).mockResolvedValue(fakeWebhook)
  renderPage()

  await waitFor(() => screen.getByRole('button', { name: /add webhook/i }))
  fireEvent.click(screen.getByRole('button', { name: /add webhook/i }))

  // form appears
  fireEvent.change(screen.getByPlaceholderText('https://…'), {
    target: { value: 'https://example.com/hook' },
  })
  fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

  await waitFor(() =>
    expect(webhooksApi.createWebhook).toHaveBeenCalledWith({
      signal_id: 's1',
      url: 'https://example.com/hook',
      platform: 'custom',
    })
  )
})

test('deletes a webhook when Delete clicked', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  vi.mocked(webhooksApi.deleteWebhook).mockResolvedValue()
  renderPage()

  await waitFor(() => screen.getByText('https://example.com/hook'))
  fireEvent.click(screen.getByRole('button', { name: /delete/i }))

  await waitFor(() =>
    expect(webhooksApi.deleteWebhook).toHaveBeenCalledWith('w1')
  )
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd frontend && npm test 2>&1 | grep -E "FAIL|Cannot find"
```
Expected: `Cannot find module '../api/webhooks'`

- [ ] **Step 3: Create `frontend/src/api/webhooks.ts`**

```typescript
import { apiClient } from './client'
import type { Webhook } from '../types'

export async function listWebhooks(): Promise<Webhook[]> {
  const res = await apiClient.get<Webhook[]>('/webhooks')
  return res.data
}

export async function createWebhook(data: {
  signal_id: string
  url: string
  platform: string
}): Promise<Webhook> {
  const res = await apiClient.post<Webhook>('/webhooks', data)
  return res.data
}

export async function updateWebhook(
  id: string,
  data: Partial<Pick<Webhook, 'url' | 'platform' | 'is_active'>>
): Promise<Webhook> {
  const res = await apiClient.put<Webhook>(`/webhooks/${id}`, data)
  return res.data
}

export async function deleteWebhook(id: string): Promise<void> {
  await apiClient.delete(`/webhooks/${id}`)
}
```

- [ ] **Step 4: Replace `frontend/src/pages/WebhooksPage.tsx` with full implementation**

```tsx
import { useEffect, useState } from 'react'
import {
  listWebhooks,
  createWebhook,
  deleteWebhook,
} from '../api/webhooks'
import { listSignals } from '../api/signals'
import type { Webhook, Signal } from '../types'

const PLATFORMS = ['custom', 'tradingview', '3commas', 'alertatron']

export function WebhooksPage() {
  const [webhooks, setWebhooks] = useState<Webhook[]>([])
  const [signals, setSignals] = useState<Signal[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [formSignalId, setFormSignalId] = useState('')
  const [formUrl, setFormUrl] = useState('')
  const [formPlatform, setFormPlatform] = useState('custom')
  const [formError, setFormError] = useState('')
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    Promise.all([listWebhooks(), listSignals()])
      .then(([whs, sigs]) => {
        setWebhooks(whs)
        setSignals(sigs)
        if (sigs.length > 0) setFormSignalId(sigs[0].id)
      })
      .finally(() => setLoading(false))
  }, [])

  async function handleCreate() {
    if (!formSignalId || !formUrl) return
    setFormError('')
    setCreating(true)
    try {
      const wh = await createWebhook({
        signal_id: formSignalId,
        url: formUrl,
        platform: formPlatform,
      })
      setWebhooks((prev) => [wh, ...prev])
      setShowForm(false)
      setFormUrl('')
    } catch (err: unknown) {
      setFormError(err instanceof Error ? err.message : 'Failed to create')
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(id: string) {
    await deleteWebhook(id)
    setWebhooks((prev) => prev.filter((w) => w.id !== id))
  }

  function signalName(id: string) {
    return signals.find((s) => s.id === id)?.name ?? id
  }

  if (loading) return <p className="text-gray-500">Loading…</p>

  return (
    <div className="max-w-3xl space-y-5">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Webhooks</h1>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium"
        >
          {showForm ? 'Cancel' : 'Add webhook'}
        </button>
      </div>

      {showForm && (
        <div className="bg-white rounded-xl shadow p-5 space-y-3">
          <div>
            <label className="block text-xs text-gray-600 mb-1">Signal</label>
            <select
              value={formSignalId}
              onChange={(e) => setFormSignalId(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {signals.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">Platform</label>
            <select
              value={formPlatform}
              onChange={(e) => setFormPlatform(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            >
              {PLATFORMS.map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-xs text-gray-600 mb-1">URL</label>
            <input
              type="url"
              placeholder="https://…"
              value={formUrl}
              onChange={(e) => setFormUrl(e.target.value)}
              className="w-full border rounded px-2 py-1.5 text-sm"
            />
          </div>
          {formError && <p className="text-red-600 text-sm">{formError}</p>}
          <button
            onClick={handleCreate}
            disabled={creating}
            className="bg-blue-600 text-white rounded px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {creating ? 'Creating…' : 'Create'}
          </button>
        </div>
      )}

      {webhooks.length === 0 ? (
        <p className="text-gray-500 py-10 text-center">No webhooks yet.</p>
      ) : (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-xs text-gray-600 uppercase">
              <tr>
                <th className="px-4 py-2 text-left">Signal</th>
                <th className="px-4 py-2 text-left">URL</th>
                <th className="px-4 py-2 text-left">Platform</th>
                <th className="px-4 py-2 text-left">Status</th>
                <th className="px-4 py-2 text-left">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {webhooks.map((wh) => (
                <tr key={wh.id}>
                  <td className="px-4 py-2 font-medium">{signalName(wh.signal_id)}</td>
                  <td className="px-4 py-2 text-gray-600 truncate max-w-xs">
                    {wh.url}
                  </td>
                  <td className="px-4 py-2 text-gray-600">{wh.platform}</td>
                  <td className="px-4 py-2">
                    <span
                      className={`px-2 py-0.5 rounded-full text-xs font-medium ${
                        wh.is_active
                          ? 'bg-green-100 text-green-700'
                          : 'bg-gray-100 text-gray-600'
                      }`}
                    >
                      {wh.is_active ? 'Active' : 'Inactive'}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <button
                      onClick={() => handleDelete(wh.id)}
                      className="text-red-600 hover:underline text-xs"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 5: Run tests**

```bash
cd frontend && npm test
```
Expected:
```
✓ src/__tests__/WebhooksPage.test.tsx (4 tests)
Test Files  10 passed (10)
Tests  34 passed (34)
```

- [ ] **Step 6: Verify the dev server compiles cleanly**

```bash
cd frontend && npm run build 2>&1 | tail -10
```
Expected: `✓ built in Xs` — no TypeScript errors

- [ ] **Step 7: Commit**

```bash
git -C .. add frontend/src/api/webhooks.ts frontend/src/pages/WebhooksPage.tsx frontend/src/__tests__/WebhooksPage.test.tsx
git -C .. commit -m "feat: Webhooks page with CRUD"
```

---

## Self-Review

### Spec Coverage

| Spec requirement | Covered by |
|---|---|
| React + TypeScript SPA | Task 1 (Vite + React + TS) |
| Auth (register/login) | Tasks 3–4 |
| Dashboard — signal list | Task 5 |
| Signal Builder — AND/OR condition tree | Task 6 |
| Backtest — submit, progress, results | Task 7 |
| Optimizer — parameter ranges, results table | Task 8 |
| Webhooks — manage webhooks | Task 9 |
| Real-time WebSocket progress | Task 7 (useJobProgress) |
| Chart screen | **OUT OF SCOPE** — needs `/candles` API endpoint |
| Settings / Billing | **OUT OF SCOPE** — no billing API exists |

### Type Consistency

- `ConditionNode`, `GroupNode`, `ConditionLeaf`, `SignalRefNode` defined in `types.ts` (Task 2) and used as-is in `ConditionTree.tsx` (Task 6) and `SignalBuilderPage.tsx` (Task 6). ✓
- `BacktestRequest` matches exactly what `submitBacktest()` sends to `POST /signals/:id/backtest`. ✓
- `OptimizeRequest` matches `SubmitOptimize` Go handler's `optimizeRequest` struct fields. ✓
- `RankedResult` matches Go's `RankedResult` struct (params, take_profit, stop_loss, score, win_rate, avg_gain, profit_factor, total_signals). ✓
- `Webhook` matches `webhookRow` Go struct (id, signal_id, url, platform, is_active, created_at). ✓
- `win_rate` from the API is a decimal (0.667), rendered as `(win_rate * 100).toFixed(1)%` in both BacktestPage and OptimizerPage. ✓
