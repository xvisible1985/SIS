import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { SignalPreviewChart } from '../features/webhooks/SignalPreviewChart'
import { apiClient } from '../api/client'

vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() } }))

// lightweight-charts needs real canvas/matchMedia/layout that jsdom doesn't provide (no
// existing test in this repo exercises a chart component either — see SignalChartPage,
// Chart.tsx). Stub it so we test this component's own logic (toggle/fetch/filter), not
// the charting library's canvas internals.
vi.mock('lightweight-charts', () => ({
  createChart: vi.fn(() => ({
    addSeries: vi.fn(() => ({
      setData: vi.fn(),
      attachPrimitive: vi.fn(),
      priceToCoordinate: vi.fn(),
    })),
    timeScale: vi.fn(() => ({ fitContent: vi.fn(), timeToCoordinate: vi.fn() })),
    applyOptions: vi.fn(),
    remove: vi.fn(),
  })),
  CandlestickSeries: {},
  ColorType: { Solid: 'solid' },
}))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(apiClient.get).mockImplementation((url: string) => {
    if (url === '/admin/signal-types') return Promise.reject(new Error('forbidden'))
    if (url === '/signal-types') return Promise.resolve({ data: [{ id: 'rsi-os', panel: 'signal' }] })
    return Promise.reject(new Error('unexpected url ' + url))
  })
  // Bybit kline call fired once the panel is opened — return an empty candle list so the
  // chart-creation effect completes without needing a real network round trip.
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: () => Promise.resolve({ result: { list: [] } }),
    text: () => Promise.resolve(''),
  }))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

test('is collapsed by default — no chart/search mounted, no candle fetch fired', async () => {
  render(<SignalPreviewChart />)
  expect(screen.getByText('Просмотр на графике')).toBeInTheDocument()
  expect(screen.queryByPlaceholderText('Поиск сигнала…')).not.toBeInTheDocument()
  expect(global.fetch).not.toHaveBeenCalled()
})

test('opening the panel shows the search box and the catalog card', async () => {
  render(<SignalPreviewChart />)
  fireEvent.click(screen.getByText('Просмотр на графике'))
  await waitFor(() => expect(screen.getByPlaceholderText('Поиск сигнала…')).toBeInTheDocument())
  await waitFor(() => expect(screen.getByText('RSI Oversold')).toBeInTheDocument())
})

test('search filters the signal cards', async () => {
  render(<SignalPreviewChart />)
  fireEvent.click(screen.getByText('Просмотр на графике'))
  await waitFor(() => screen.getByText('RSI Oversold'))

  fireEvent.change(screen.getByPlaceholderText('Поиск сигнала…'), { target: { value: 'nothing-matches-this' } })
  await waitFor(() => expect(screen.getByText('Ничего не найдено')).toBeInTheDocument())
  expect(screen.queryByText('RSI Oversold')).not.toBeInTheDocument()
})
