import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { DashboardPage } from '../pages/DashboardPage'
import * as dashboardApi from '../api/dashboard'
import * as accountsApi from '../api/accounts'
import type { DashboardData } from '../api/dashboard'

vi.mock('../api/dashboard')
vi.mock('../api/accounts')

const fakeDashboard: DashboardData = {
  stats: {
    total: 12,
    wins: 8,
    losses: 4,
    win_rate: 66.7,
    total_pnl: 123.45,
    avg_pnl: 10.29,
    best_trade: 55.5,
    worst_trade: -20.1,
    profit_factor: 2.1,
  },
  daily_pnl: [
    { day: '2026-08-01', pnl: 50, trades: 5, wins: 3 },
    { day: '2026-08-02', pnl: 73.45, trades: 7, wins: 5 },
  ],
  bot_stats: [],
  recent_trades: [],
  equity_series: [],
  granularity: 'day',
}

function renderDashboard() {
  return render(<MemoryRouter><DashboardPage /></MemoryRouter>)
}

beforeEach(() => {
  vi.clearAllMocks()
  // No selected account (default AccountContext value) — loadAccount() short-circuits on an
  // empty id, so getAccountBalance/getAccountPositions are never called in these tests.
  vi.mocked(accountsApi.listAccounts).mockResolvedValue([])
})

test('shows loading state initially', () => {
  vi.mocked(dashboardApi.getDashboard).mockReturnValue(new Promise(() => {}))
  renderDashboard()
  expect(screen.getByText(/загрузка данных/i)).toBeInTheDocument()
})

test('renders dashboard stats after load', async () => {
  vi.mocked(dashboardApi.getDashboard).mockResolvedValue(fakeDashboard)
  renderDashboard()
  await waitFor(() => expect(screen.getAllByText('$123.45').length).toBeGreaterThan(0))
  expect(screen.getByText('66.7%')).toBeInTheDocument()
})

test('shows a specific warning (not a generic error) when the API returns a malformed response', async () => {
  // loadDash guards explicitly against a response missing stats/daily_pnl (e.g. api-gateway
  // serving stale HTML instead of JSON) rather than letting it crash the page.
  vi.mocked(dashboardApi.getDashboard).mockResolvedValue({} as DashboardData)
  renderDashboard()
  await waitFor(() => expect(screen.getByText(/api-gateway пересобран/i)).toBeInTheDocument())
})

test('shows error state on fetch failure', async () => {
  vi.mocked(dashboardApi.getDashboard).mockRejectedValue(new Error('network error'))
  renderDashboard()
  await waitFor(() => expect(screen.getByText(/network error/i)).toBeInTheDocument())
})
