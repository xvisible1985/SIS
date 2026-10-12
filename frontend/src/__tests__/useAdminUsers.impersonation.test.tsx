import { renderHook, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

vi.mock('../api/client', () => ({
  apiClient: { get: vi.fn(), patch: vi.fn(), post: vi.fn(), delete: vi.fn() },
}))

import { apiClient } from '../api/client'
import { useAdminUsers } from '../features/admin-users/api'
import { ADMIN_TOKEN_KEY } from '../contexts/ImpersonationContext'

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  vi.mocked(apiClient.get).mockResolvedValue({ data: [] })
})

test('loads /admin/users with no explicit auth override when not impersonating', async () => {
  const { result } = renderHook(() => useAdminUsers())
  await waitFor(() => expect(result.current.loading).toBe(false))
  expect(apiClient.get).toHaveBeenCalledWith('/admin/users', undefined)
})

// Regression: /admin/users is gated by the backend's RequireAdmin, which checks the
// BEARER TOKEN'S OWN role. While impersonating, apiClient's default token is the
// impersonated (non-admin) user's, so the call came back 403 and AdminUserPickerBar's
// list silently stayed empty ("Не найдено" for everyone) — found live.
test('loads /admin/users with the real admin token explicitly while impersonating', async () => {
  localStorage.setItem(ADMIN_TOKEN_KEY, 'real-admin-tok')
  const { result } = renderHook(() => useAdminUsers())
  await waitFor(() => expect(result.current.loading).toBe(false))
  expect(apiClient.get).toHaveBeenCalledWith('/admin/users', {
    headers: { Authorization: 'Bearer real-admin-tok' },
  })
})
