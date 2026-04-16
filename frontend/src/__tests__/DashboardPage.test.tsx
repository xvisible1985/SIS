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
  expect(screen.getAllByRole('link', { name: /create signal/i }).length).toBeGreaterThan(0)
})

test('shows error state on fetch failure', async () => {
  vi.mocked(signalsApi.listSignals).mockRejectedValue(new Error('network error'))
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getByText(/network error/i)).toBeInTheDocument())
})
