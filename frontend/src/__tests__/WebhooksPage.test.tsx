import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { WebhooksPage } from '../pages/WebhooksPage'
import * as webhooksApi from '../api/webhooks'
import * as customSignalsApi from '../api/customSignals'
import { apiClient } from '../api/client'
import type { Webhook } from '../types'

vi.mock('../api/webhooks')
vi.mock('../api/customSignals')
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() } }))
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))

// The page embeds SignalPreviewChart, which uses lightweight-charts — jsdom has neither
// matchMedia nor real canvas layout, and the component would hit the real Bybit API
// (unmocked fetch) besides. Stub the chart library so these tests exercise this page's own
// logic, not the charting library's canvas internals (mirrors SignalPreviewChart.test.tsx).
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
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: true,
    json: () => Promise.resolve({ result: { list: [] } }),
    text: () => Promise.resolve(''),
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

const fakeWebhook: Webhook = {
  id: 'w1',
  catalog_signal_id: 'rsi-os',
  custom_signal_id: null,
  catalog_signal_name: 'RSI Oversold',
  symbol: 'BTCUSDT',
  timeframe: '15m',
  params: { period: 14, threshold: 30, kind: 'cross' },
  url: 'http://localhost:8081/webhooks/relay/abc123',
  platform: 'custom',
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

function renderPage() {
  return render(<MemoryRouter><WebhooksPage /></MemoryRouter>)
}

beforeEach(() => {
  vi.clearAllMocks()
  // Non-admin path: /admin/signal-types rejects, falls back to /signal-types.
  vi.mocked(apiClient.get).mockImplementation((url: string) => {
    if (url === '/admin/signal-types') return Promise.reject(new Error('forbidden'))
    if (url === '/signal-types') return Promise.resolve({ data: [{ id: 'rsi-os', panel: 'signal' }, { id: 'macd-x', panel: 'signal' }] })
    return Promise.reject(new Error('unexpected url ' + url))
  })
  vi.mocked(customSignalsApi.listCustomSignals).mockResolvedValue([])
  vi.mocked(customSignalsApi.comboPreview).mockResolvedValue([])
})

test('shows the signal catalog and existing alerts', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  renderPage()
  // Once as the catalog card name, once as the alert row's resolved name.
  await waitFor(() => expect(screen.getAllByText('RSI Oversold').length).toBeGreaterThanOrEqual(2))
  expect(screen.getByText(/BTCUSDT.*15m.*custom/)).toBeInTheDocument()
})

test('shows empty state when there are no alerts yet', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  renderPage()
  await waitFor(() => expect(screen.getByText(/оповещений пока нет/i)).toBeInTheDocument())
})

test('creates an alert for the selected catalog signal', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(webhooksApi.createWebhook).mockResolvedValue(fakeWebhook)
  renderPage()

  await waitFor(() => screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByText('RSI Oversold'))

  await waitFor(() => screen.getByRole('button', { name: /создать оповещение/i }))
  fireEvent.click(screen.getByRole('button', { name: /создать оповещение/i }))

  await waitFor(() =>
    expect(webhooksApi.createWebhook).toHaveBeenCalledWith({
      catalog_signal_id: 'rsi-os',
      symbol: 'BTCUSDT',
      timeframe: '15m',
      params: { period: 14, threshold: 30, kind: 'cross', tf: '1h' },
      platform: 'custom',
    })
  )
})

test('deletes an alert when Удалить is clicked', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  vi.mocked(webhooksApi.deleteWebhook).mockResolvedValue()
  renderPage()

  await waitFor(() => screen.getByRole('button', { name: /удалить/i }))
  fireEvent.click(screen.getByRole('button', { name: /удалить/i }))

  await waitFor(() => expect(webhooksApi.deleteWebhook).toHaveBeenCalledWith('w1'))
})

test('combo mode: picking two signals adds them as removable chips', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  renderPage()

  await waitFor(() => screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByRole('button', { name: /^комбинировать$/i }))
  fireEvent.click(screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByText('MACD Crossover'))

  // Chips row renders each picked leg with its own remove (✕) button — 2 legs, 2 chips.
  await waitFor(() => expect(screen.getByText('Сохранить как сигнал')).toBeInTheDocument())
  const chipRemoveButtons = screen.getAllByText('✕').filter(el => el.closest('span'))
  expect(chipRemoveButtons.length).toBeGreaterThanOrEqual(2)
})

test('combo mode: saving a combo calls createCustomSignal with both legs and clears the builder', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(customSignalsApi.createCustomSignal).mockResolvedValue({ id: 'cs1' })
  renderPage()

  await waitFor(() => screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByRole('button', { name: /^комбинировать$/i }))
  fireEvent.click(screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByText('MACD Crossover'))

  await waitFor(() => screen.getByPlaceholderText('Название сигнала…'))
  fireEvent.change(screen.getByPlaceholderText('Название сигнала…'), { target: { value: 'My combo' } })
  fireEvent.click(screen.getByText('Сохранить как сигнал'))

  await waitFor(() =>
    expect(customSignalsApi.createCustomSignal).toHaveBeenCalledWith('My combo', [
      { signal_id: 'rsi-os', params: expect.any(Object) },
      { signal_id: 'macd-x', params: expect.any(Object) },
    ])
  )
  // Combo mode exits and the builder chips row is gone after a successful save.
  await waitFor(() => expect(screen.queryByPlaceholderText('Название сигнала…')).not.toBeInTheDocument())
})

test('a saved custom signal appears under "Мои сигналы" and can be selected for the alert form', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(customSignalsApi.listCustomSignals).mockResolvedValue([{
    id: 'cs1',
    name: 'RSI + MACD combo',
    created_at: '2026-01-01T00:00:00Z',
    components: [
      { signal_id: 'rsi-os', signal_name: 'RSI Oversold', params: {} },
      { signal_id: 'macd-x', signal_name: 'MACD Crossover', params: {} },
    ],
  }])
  renderPage()

  await waitFor(() => screen.getByText('RSI + MACD combo'))
  fireEvent.click(screen.getByText('RSI + MACD combo'))

  // The alert form's "Сигнал" panel now shows the combo's own name.
  await waitFor(() => {
    const matches = screen.getAllByText('RSI + MACD combo')
    expect(matches.length).toBeGreaterThanOrEqual(2) // catalog card + form panel
  })
})

test('combo mode: editing a leg\'s params via the ⚙ button carries through to the saved combo', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(customSignalsApi.createCustomSignal).mockResolvedValue({ id: 'cs1' })
  renderPage()

  await waitFor(() => screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByRole('button', { name: /^комбинировать$/i }))
  fireEvent.click(screen.getByText('RSI Oversold'))
  fireEvent.click(screen.getByText('MACD Crossover'))

  // Open the RSI leg's param editor and switch its "Режим" (kind) from the default 'cross' to 'enter'.
  await waitFor(() => expect(screen.getAllByTitle('Параметры').length).toBeGreaterThan(0))
  fireEvent.click(screen.getAllByTitle('Параметры')[0])
  await waitFor(() => screen.getByText('enter'))
  fireEvent.click(screen.getByText('enter'))

  fireEvent.change(screen.getByPlaceholderText('Название сигнала…'), { target: { value: 'Edited combo' } })
  fireEvent.click(screen.getByText('Сохранить как сигнал'))

  await waitFor(() =>
    expect(customSignalsApi.createCustomSignal).toHaveBeenCalledWith('Edited combo', [
      { signal_id: 'rsi-os', params: expect.objectContaining({ kind: 'enter' }) },
      { signal_id: 'macd-x', params: expect.any(Object) },
    ])
  )
})

test('deleting a custom signal calls deleteCustomSignal', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(customSignalsApi.listCustomSignals).mockResolvedValue([{
    id: 'cs1',
    name: 'RSI + MACD combo',
    created_at: '2026-01-01T00:00:00Z',
    components: [
      { signal_id: 'rsi-os', signal_name: 'RSI Oversold', params: {} },
      { signal_id: 'macd-x', signal_name: 'MACD Crossover', params: {} },
    ],
  }])
  vi.mocked(customSignalsApi.deleteCustomSignal).mockResolvedValue()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  renderPage()

  await waitFor(() => screen.getByTitle('Удалить сигнал'))
  fireEvent.click(screen.getByTitle('Удалить сигнал'))

  await waitFor(() => expect(customSignalsApi.deleteCustomSignal).toHaveBeenCalledWith('cs1'))
})
