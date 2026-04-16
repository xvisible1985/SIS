import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { OptimizerPage } from '../pages/OptimizerPage'
import * as signalsApi from '../api/signals'
import type { OptimizationResult } from '../types'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1' }),
}))
vi.mock('../hooks/useJobProgress', () => ({
  useJobProgress: () => ({ pct: 0, status: '' }),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/signals/s1/optimize']}>
      <Routes>
        <Route path="/signals/:id/optimize" element={<OptimizerPage />} />
      </Routes>
    </MemoryRouter>
  )
}

const fakeResult: OptimizationResult = {
  id: 'o1',
  signal_id: 's1',
  mode: 'fast',
  top_combinations: [
    {
      params: { rsi_period: 14 },
      take_profit: 2,
      stop_loss: 1,
      score: 0.85,
      win_rate: 0.65,
      avg_gain: 1.4,
      profit_factor: 2.3,
      total_signals: 55,
    },
  ],
  best_params: { rsi_period: 14 },
  created_at: '2026-01-01T00:00:00Z',
}

beforeEach(() => vi.clearAllMocks())

test('renders optimizer form', () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([])
  renderPage()
  expect(screen.getByLabelText(/period from/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/period to/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /run optimizer/i })).toBeInTheDocument()
})

test('submits optimization with correct payload', async () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([])
  vi.mocked(signalsApi.submitOptimize).mockResolvedValue({ job_id: 'job2' })
  renderPage()

  fireEvent.change(screen.getByLabelText(/period from/i), {
    target: { value: '2025-01-01' },
  })
  fireEvent.change(screen.getByLabelText(/period to/i), {
    target: { value: '2026-01-01' },
  })
  fireEvent.click(screen.getByRole('button', { name: /run optimizer/i }))

  await waitFor(() =>
    expect(signalsApi.submitOptimize).toHaveBeenCalledWith(
      's1',
      expect.objectContaining({ period_from: '2025-01-01', period_to: '2026-01-01' })
    )
  )
})

test('shows top combinations table from past results', async () => {
  vi.mocked(signalsApi.getOptimizationResults).mockResolvedValue([fakeResult])
  renderPage()
  await waitFor(() => expect(screen.getByText('0.85')).toBeInTheDocument())
  expect(screen.getByText('65.0%')).toBeInTheDocument()
})
