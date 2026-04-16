import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { WebhooksPage } from '../pages/WebhooksPage'
import * as webhooksApi from '../api/webhooks'
import * as signalsApi from '../api/signals'
import type { Webhook, Signal } from '../types'

vi.mock('../api/webhooks')
vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))

const fakeSignal: Signal = {
  id: 's1',
  name: 'RSI Signal',
  description: '',
  exchange: 'binance',
  symbol: 'BTCUSDT',
  market: 'spot',
  timeframe: '1h',
  direction: 'LONG',
  conditions: { type: 'AND', children: [] },
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

const fakeWebhook: Webhook = {
  id: 'w1',
  signal_id: 's1',
  url: 'https://example.com/hook',
  platform: 'custom',
  is_active: true,
  created_at: '2026-01-01T00:00:00Z',
}

function renderPage() {
  return render(<MemoryRouter><WebhooksPage /></MemoryRouter>)
}

beforeEach(() => vi.clearAllMocks())

test('shows webhook list', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  renderPage()
  await waitFor(() =>
    expect(screen.getByText('https://example.com/hook')).toBeInTheDocument()
  )
  expect(screen.getByText('custom')).toBeInTheDocument()
})

test('shows empty state when no webhooks', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  renderPage()
  await waitFor(() =>
    expect(screen.getByText(/no webhooks/i)).toBeInTheDocument()
  )
})

test('creates a webhook', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  vi.mocked(webhooksApi.createWebhook).mockResolvedValue(fakeWebhook)
  renderPage()

  await waitFor(() => screen.getByRole('button', { name: /add webhook/i }))
  fireEvent.click(screen.getByRole('button', { name: /add webhook/i }))

  // form appears
  fireEvent.change(screen.getByPlaceholderText('https://…'), {
    target: { value: 'https://example.com/hook' },
  })
  fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

  await waitFor(() =>
    expect(webhooksApi.createWebhook).toHaveBeenCalledWith({
      signal_id: 's1',
      url: 'https://example.com/hook',
      platform: 'custom',
    })
  )
})

test('deletes a webhook when Delete clicked', async () => {
  vi.mocked(webhooksApi.listWebhooks).mockResolvedValue([fakeWebhook])
  vi.mocked(signalsApi.listSignals).mockResolvedValue([fakeSignal])
  vi.mocked(webhooksApi.deleteWebhook).mockResolvedValue()
  renderPage()

  await waitFor(() => screen.getByText('https://example.com/hook'))
  fireEvent.click(screen.getByRole('button', { name: /delete/i }))

  await waitFor(() =>
    expect(webhooksApi.deleteWebhook).toHaveBeenCalledWith('w1')
  )
  await waitFor(() =>
    expect(screen.queryByText('https://example.com/hook')).not.toBeInTheDocument()
  )
})
