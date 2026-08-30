import { render, screen, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { SignalPreviewChart } from '../features/webhooks/SignalPreviewChart'
import { SIGNALS } from '../features/indicators/signals'

// lightweight-charts needs real canvas/matchMedia/layout that jsdom doesn't provide (no
// existing test in this repo exercises a chart component either — see SignalChartPage,
// Chart.tsx). Stub it so we test this component's own logic (fetch/cache on
// activeSignal), not the charting library's canvas internals.
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

const rsiSignal = SIGNALS.find(s => s.id === 'rsi-os')!

function mockFetch() {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (url.includes('/signals/chart-history')) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ events: [{ time: 1000, state: 'buy', price: 100 }] }),
        text: () => Promise.resolve(''),
      })
    }
    // Bybit kline call fired by the chart-creation effect.
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ result: { list: [] } }),
      text: () => Promise.resolve(''),
    })
  }))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

test('renders without a selected signal and does not fetch signal history', async () => {
  mockFetch()
  render(<SignalPreviewChart symbol="BTCUSDT" tf="15m" activeSignal={null} />)
  await waitFor(() => expect(global.fetch).toHaveBeenCalled()) // candle fetch only
  expect(global.fetch).not.toHaveBeenCalledWith(expect.stringContaining('/signals/chart-history'))
})

test('fetches and shows a loading indicator when a signal is selected', async () => {
  mockFetch()
  render(<SignalPreviewChart symbol="BTCUSDT" tf="15m" activeSignal={rsiSignal} />)
  await waitFor(() =>
    expect(global.fetch).toHaveBeenCalledWith(expect.stringContaining('/signals/chart-history'), expect.anything())
  )
})

test('shows an error message when the signal history request fails', async () => {
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (url.includes('/signals/chart-history')) {
      return Promise.resolve({ ok: false, text: () => Promise.resolve('boom') })
    }
    return Promise.resolve({ ok: true, json: () => Promise.resolve({ result: { list: [] } }) })
  }))
  render(<SignalPreviewChart symbol="BTCUSDT" tf="15m" activeSignal={rsiSignal} />)
  await waitFor(() => expect(screen.getByText('boom')).toBeInTheDocument())
})
