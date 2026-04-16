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
