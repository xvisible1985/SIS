import { render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'

vi.mock('../api/client', () => ({
  apiClient: { get: vi.fn() },
}))

import { apiClient } from '../api/client'
import { ServicesTab } from '../features/admin-services/ServicesTab'

beforeEach(() => {
  vi.clearAllMocks()
})

function mockEndpoints(watchdogStats: Record<string, unknown>) {
  vi.mocked(apiClient.get).mockImplementation((url: string) => {
    if (url === '/admin/services') return Promise.resolve({ data: [] })
    if (url === '/admin/watchdog-stats') return Promise.resolve({ data: watchdogStats })
    return Promise.reject(new Error(`unexpected url ${url}`))
  })
}

test('renders nothing for the watchdog panel when the stats file has no known services', async () => {
  mockEndpoints({})
  render(<ServicesTab />)
  await waitFor(() => expect(apiClient.get).toHaveBeenCalledWith('/admin/watchdog-stats'))
  expect(screen.queryByText(/Вотчдог/)).not.toBeInTheDocument()
})

test('shows restart counts per service, highlighting ones that have restarted', async () => {
  mockEndpoints({
    docker: { restarts: 3, last_restart: '2026-10-14T04:11:00Z', last_check: '2026-10-14T15:30:00Z', status: 'ok' },
    'sis-frontend': { restarts: 0, last_restart: null, last_check: '2026-10-14T15:30:00Z', status: 'online' },
    'sis-api-gateway': { restarts: 2, last_restart: '2026-10-14T10:00:00Z', last_check: '2026-10-14T15:30:00Z', status: 'online' },
  })
  render(<ServicesTab />)

  expect(await screen.findByText('Docker')).toBeInTheDocument()
  expect(screen.getByText('Frontend')).toBeInTheDocument()
  expect(screen.getByText('API-Gateway')).toBeInTheDocument()

  // Restart counts for each card
  expect(screen.getByText('3')).toBeInTheDocument()
  expect(screen.getByText('0')).toBeInTheDocument()
  expect(screen.getByText('2')).toBeInTheDocument()

  // sis-frontend never restarted
  expect(screen.getByText(/ни разу/)).toBeInTheDocument()
})
