import { render, screen, act } from '@testing-library/react'
import { AuthProvider, useAuth } from '../hooks/useAuth'

function TestWidget() {
  const { token, userId, isAuthenticated, login, logout } = useAuth()
  return (
    <div>
      <span data-testid="authed">{isAuthenticated ? 'yes' : 'no'}</span>
      <span data-testid="token">{token ?? 'none'}</span>
      <span data-testid="userId">{userId ?? 'none'}</span>
      <button onClick={() => login('tok123', 'uid456', 'u@test.com')}>do-login</button>
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
