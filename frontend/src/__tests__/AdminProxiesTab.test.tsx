// frontend/src/__tests__/AdminProxiesTab.test.tsx
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi, beforeEach } from 'vitest'
import { AdminProxiesTab } from '../features/admin-proxies/AdminProxiesTab'
import type { Proxy } from '../features/admin-proxies/types'

const fakeProxy: Proxy = {
  id: 1,
  protocol: 'http',
  host: '1.2.3.4',
  port: 8080,
  username: 'user1',
  weight: 2,
  is_active: true,
  health_status: 'healthy',
  fail_count: 0,
  total_reqs: 10,
  active_reqs: 0,
}

const { mockRefresh, mockUpdateProxy } = vi.hoisted(() => ({
  mockRefresh: vi.fn(),
  mockUpdateProxy: vi.fn(),
}))

vi.mock('../features/admin-proxies/api', () => ({
  useProxies: vi.fn(() => ({
    proxies: [fakeProxy],
    metrics: [],
    loading: false,
    error: null,
    refresh: mockRefresh,
  })),
  createProxy: vi.fn(),
  updateProxy: mockUpdateProxy,
  deleteProxy: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

test('shows edit button in proxy row', () => {
  render(<AdminProxiesTab />)
  expect(screen.getByRole('button', { name: /изменить/i })).toBeInTheDocument()
})

test('clicking edit opens inline form pre-filled with proxy data', () => {
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  expect(screen.getByDisplayValue('1.2.3.4')).toBeInTheDocument()
  expect(screen.getByDisplayValue('8080')).toBeInTheDocument()
  expect(screen.getByDisplayValue('user1')).toBeInTheDocument()
  expect(screen.getByDisplayValue('2')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /сохранить/i })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /отмена/i })).toBeInTheDocument()
})

test('cancel button closes edit form', () => {
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  fireEvent.click(screen.getByRole('button', { name: /отмена/i }))
  expect(screen.queryByRole('button', { name: /сохранить/i })).not.toBeInTheDocument()
  // edit button reappears
  expect(screen.getByRole('button', { name: /изменить/i })).toBeInTheDocument()
})

test('save calls updateProxy with edited values and refreshes', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))

  const hostInput = screen.getByDisplayValue('1.2.3.4')
  fireEvent.change(hostInput, { target: { value: '9.9.9.9' } })

  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() =>
    expect(mockUpdateProxy).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ host: '9.9.9.9', protocol: 'http', port: 8080, weight: 2 }),
    ),
  )
  await waitFor(() => expect(mockRefresh).toHaveBeenCalled())
})

test('empty password field is NOT included in updateProxy call', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))
  // password field is left blank
  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() => expect(mockUpdateProxy).toHaveBeenCalled())
  const callBody = mockUpdateProxy.mock.calls[0][1] as Record<string, unknown>
  expect(callBody).not.toHaveProperty('password')
})

test('filled password field IS included in updateProxy call', async () => {
  mockUpdateProxy.mockResolvedValue(undefined)
  render(<AdminProxiesTab />)
  fireEvent.click(screen.getByRole('button', { name: /изменить/i }))

  const passInput = screen.getByPlaceholderText(/без изменений/i)
  fireEvent.change(passInput, { target: { value: 'newpass123' } })

  fireEvent.click(screen.getByRole('button', { name: /сохранить/i }))

  await waitFor(() =>
    expect(mockUpdateProxy).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ password: 'newpass123' }),
    ),
  )
})
