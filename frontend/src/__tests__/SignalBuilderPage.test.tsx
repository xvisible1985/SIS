import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { vi } from 'vitest'
import { SignalBuilderPage } from '../pages/SignalBuilderPage'
import * as signalsApi from '../api/signals'
import type { Signal } from '../types'

vi.mock('../api/signals')
vi.mock('../hooks/useAuth', () => ({
  useAuth: () => ({ token: 'tok', userId: 'uid1', isAuthenticated: true }),
}))

const fakeSignal: Signal = {
  id: 's1',
  name: 'My Signal',
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

beforeEach(() => vi.clearAllMocks())

function renderNew() {
  return render(
    <MemoryRouter initialEntries={['/signals/new']}>
      <Routes>
        <Route path="/signals/new" element={<SignalBuilderPage />} />
        <Route path="/" element={<div>dashboard</div>} />
      </Routes>
    </MemoryRouter>
  )
}

function renderEdit() {
  vi.mocked(signalsApi.getSignal).mockResolvedValue(fakeSignal)
  return render(
    <MemoryRouter initialEntries={['/signals/s1/edit']}>
      <Routes>
        <Route path="/signals/:id/edit" element={<SignalBuilderPage />} />
        <Route path="/" element={<div>dashboard</div>} />
      </Routes>
    </MemoryRouter>
  )
}

test('new signal form has required fields', () => {
  renderNew()
  expect(screen.getByPlaceholderText('Signal name')).toBeInTheDocument()
  expect(screen.getByLabelText(/symbol/i)).toBeInTheDocument()
  expect(screen.getByLabelText(/timeframe/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument()
})

test('creates signal and redirects to dashboard', async () => {
  vi.mocked(signalsApi.createSignal).mockResolvedValue(fakeSignal)
  renderNew()
  fireEvent.change(screen.getByPlaceholderText('Signal name'), {
    target: { value: 'My Signal' },
  })
  fireEvent.click(screen.getByRole('button', { name: /save/i }))
  await waitFor(() =>
    expect(signalsApi.createSignal).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'My Signal' })
    )
  )
  await waitFor(() =>
    expect(screen.getByText('dashboard')).toBeInTheDocument()
  )
})

test('edit mode loads existing signal data', async () => {
  renderEdit()
  await waitFor(() =>
    expect(screen.getByDisplayValue('My Signal')).toBeInTheDocument()
  )
})

test('edit mode calls updateSignal on save', async () => {
  vi.mocked(signalsApi.updateSignal).mockResolvedValue(fakeSignal)
  renderEdit()
  await waitFor(() => screen.getByDisplayValue('My Signal'))
  fireEvent.click(screen.getByRole('button', { name: /save/i }))
  await waitFor(() =>
    expect(signalsApi.updateSignal).toHaveBeenCalledWith('s1', expect.any(Object))
  )
})
