import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { BacktestPage } from '../pages/BacktestPage'
import * as signalsApi from '../api/signals'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))
vi.mock('../hooks/useJobProgress', () => ({
  useJobProgress: () => ({ pct: 0, status: '' }),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/signals/s1/backtest']}>
      <Routes>
        <Route path="/signals/:id/backtest" element={<BacktestPage />} />
      </Routes>
    </MemoryRouter>
  )
}

beforeEach(() => vi.clearAllMocks())

test('renders backtest form fields', () => {
  vi.mocked(signalsApi.getBacktestResults).mockResolvedValue([])
  renderPage()
  expect(screen.getByLabelText(/period from/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/period to/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/take profit/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/stop loss/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /run backtest/i })).toBeInTheDocument()
})

test('submits backtest with correct params', async () => {
  vi.mocked(signalsApi.submitBacktest).mockResolvedValue({ job_id: 'job1' })
  vi.mocked(signalsApi.getBacktestResults).mockResolvedValue([])
  renderPage()

  fireEvent.change(screen.getByLabelText(/period from/i), {
    target: { value: '2025-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/period to/i), {
    target: { value: '2026-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/take profit/i), {
    target: { value: '3' },
  })
  fireEvent.change(screen.getByLabelText(/stop loss/i), {
    target: { value: '1.5' },
  })
  fireEvent.click(screen.getByRole('button', { name: /run backtest/i }))

  await waitFor(() =>
    expect(signalsApi.submitBacktest).toHaveBeenCalledWith('s1', {
      period_from: '2025-01-01',
      period_to: '2026-01-01',
      take_profit: 3,
      stop_loss: 1.5,
    })
  )
})

test('loads existing results on mount', async () => {
  vi.mocked(signalsApi.getBacktestResults).mockResolvedValue([
    {
      id: 'r1',
      signal_id: 's1',
      symbol: 'BTCUSDT',
      timeframe: '1h',
      period_from: '2025-01-01T00:00:00Z',
      period_to: '2026-01-01T00:00:00Z',
      mode: 'fast',
      total_signals: 42,
      win_count: 28,
      loss_count: 14,
      win_rate: 0.667,
      avg_gain: 1.23,
      max_drawdown: 5.4,
      profit_factor: 2.1,
      patterns: null,
      created_at: '2026-01-01T00:00:00Z',
    },
  ])
  renderPage()
  await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  expect(screen.getByText('66.7%')).toBeInTheDocument()
})
