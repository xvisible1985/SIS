import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { SignalPickerField } from '../components/strategies/FormWidgets'
import { apiClient } from '../api/client'
import * as customSignalsApi from '../api/customSignals'
import type { SignalConfig, CustomSignal } from '../types'

vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() } }))
vi.mock('../api/customSignals')

const fakeCombo: CustomSignal = {
  id: 'cs1',
  name: 'RSI + MACD combo',
  created_at: '2026-01-01T00:00:00Z',
  components: [
    { signal_id: 'rsi-os', signal_name: 'RSI Oversold', params: {} },
    { signal_id: 'macd-x', signal_name: 'MACD Crossover', params: {} },
  ],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(apiClient.get).mockImplementation((url: string) => {
    if (url === '/admin/signal-types') return Promise.reject(new Error('forbidden'))
    if (url === '/signal-types') return Promise.resolve({ data: [{ id: 'rsi-os', panel: 'signal' }] })
    return Promise.reject(new Error('unexpected url ' + url))
  })
  vi.mocked(customSignalsApi.listCustomSignals).mockResolvedValue([fakeCombo])
})

test('opening the picker shows the user\'s saved combo signals under "Мои сигналы"', async () => {
  render(<SignalPickerField configs={[]} onChange={() => {}} />)
  fireEvent.click(screen.getByText('+ Добавить сигнал'))

  await waitFor(() => screen.getByText('RSI + MACD combo'))
  expect(screen.getByText('Мои сигналы')).toBeInTheDocument()
})

test('picking a combo appends a custom_signal_id entry, not a params-editable catalog one', async () => {
  const onChange = vi.fn()
  render(<SignalPickerField configs={[]} onChange={onChange} />)
  fireEvent.click(screen.getByText('+ Добавить сигнал'))

  await waitFor(() => screen.getByText('RSI + MACD combo'))
  fireEvent.click(screen.getByText('RSI + MACD combo'))

  expect(onChange).toHaveBeenCalledWith([
    { name: 'RSI + MACD combo', custom_signal_id: 'cs1', params: {} },
  ])
})

test('a combo already in configs renders with a "Комбо" badge and no edit button', async () => {
  const configs: SignalConfig[] = [{ name: 'RSI + MACD combo', custom_signal_id: 'cs1', params: {} }]
  render(<SignalPickerField configs={configs} onChange={() => {}} />)

  await waitFor(() => screen.getByText('RSI + MACD combo'))
  expect(screen.getByText('Комбо')).toBeInTheDocument()
  expect(screen.queryByTitle('Редактировать')).not.toBeInTheDocument()
})
