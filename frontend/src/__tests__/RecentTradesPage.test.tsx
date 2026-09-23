import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { RecentTradesPage } from '../pages/RecentTradesPage'
import * as dashboardApi from '../api/dashboard'
import type { RecentTrade } from '../api/dashboard'

vi.mock('../api/dashboard')

function trade(id: string, symbol: string): RecentTrade {
  return {
    id, symbol, direction: 'long', result: 'tp', bot_name: null,
    pnl: 10, pnl_pct: 1.5, closed_at: '2026-09-22T10:00:00Z',
  }
}

// Captures the callback passed to `new IntersectionObserver(cb)` so tests can simulate the
// sentinel scrolling into view without a real jsdom layout/intersection engine (jsdom has
// none — see setupTests.ts's no-op polyfill, which this test-local mock overrides).
let lastIntersectionCallback: IntersectionObserverCallback | null = null
class FakeIntersectionObserver implements IntersectionObserver {
  readonly root: Element | Document | null = null
  readonly rootMargin: string = ''
  readonly thresholds: ReadonlyArray<number> = []
  constructor(cb: IntersectionObserverCallback) { lastIntersectionCallback = cb }
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords(): IntersectionObserverEntry[] { return [] }
}

function renderPage(initialEntry = '/dashboard/trades?period=30d') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <RecentTradesPage />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  lastIntersectionCallback = null
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver)
})

test('shows loading state initially', () => {
  vi.mocked(dashboardApi.getDashboardRecentTrades).mockReturnValue(new Promise(() => {}))
  renderPage()
  expect(screen.getByText(/загрузка данных/i)).toBeInTheDocument()
})

test('renders the first page of trades and passes the period from the URL', async () => {
  vi.mocked(dashboardApi.getDashboardRecentTrades).mockResolvedValue({
    trades: [trade('1', 'BTCUSDT')], has_more: false,
  })
  renderPage('/dashboard/trades?period=7d')
  await waitFor(() => expect(screen.getByText('BTCUSDT')).toBeInTheDocument())
  expect(dashboardApi.getDashboardRecentTrades).toHaveBeenCalledWith('7d', undefined, 30, 0)
  expect(screen.getByText(/это все сделки/i)).toBeInTheDocument()
})

test('defaults to period=30d when the URL has none', async () => {
  vi.mocked(dashboardApi.getDashboardRecentTrades).mockResolvedValue({ trades: [], has_more: false })
  render(<MemoryRouter initialEntries={['/dashboard/trades']}><RecentTradesPage /></MemoryRouter>)
  await waitFor(() => expect(dashboardApi.getDashboardRecentTrades).toHaveBeenCalledWith('30d', undefined, 30, 0))
})

test('scrolling the sentinel into view loads the next page and appends it', async () => {
  vi.mocked(dashboardApi.getDashboardRecentTrades)
    .mockResolvedValueOnce({ trades: [trade('1', 'FIRSTUSDT')], has_more: true })
    .mockResolvedValueOnce({ trades: [trade('2', 'SECONDUSDT')], has_more: false })

  renderPage()
  await waitFor(() => expect(screen.getByText('FIRSTUSDT')).toBeInTheDocument())
  expect(lastIntersectionCallback).not.toBeNull()

  // Simulate the sentinel scrolling into view.
  lastIntersectionCallback!([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)

  await waitFor(() => expect(screen.getByText('SECONDUSDT')).toBeInTheDocument())
  // The first page's trade must still be there — infinite scroll appends, it doesn't replace.
  expect(screen.getByText('FIRSTUSDT')).toBeInTheDocument()
  expect(dashboardApi.getDashboardRecentTrades).toHaveBeenLastCalledWith('30d', undefined, 30, 1)
})

test('shows an empty state when there are no trades in the selected period', async () => {
  vi.mocked(dashboardApi.getDashboardRecentTrades).mockResolvedValue({ trades: [], has_more: false })
  renderPage()
  await waitFor(() => expect(screen.getByText(/нет закрытых сделок/i)).toBeInTheDocument())
})
