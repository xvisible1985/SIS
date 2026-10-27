import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { DashboardPage } from '../pages/DashboardPage'
import * as dashboardApi from '../api/dashboard'
import * as accountsApi from '../api/accounts'
import type { DashboardData, DepositDay } from '../api/dashboard'

vi.mock('../api/dashboard')
vi.mock('../api/accounts')
vi.mock('../contexts/AccountContext', () => ({
  useSelectedAccount: () => ({ selectedAccountId: 'acct-1', setSelectedAccountId: vi.fn() }),
}))

const dash = {
  stats: { total: 0, wins: 0, losses: 0, win_rate: 0, total_pnl: 0, avg_pnl: 0, best_trade: null, worst_trade: null, profit_factor: 0 },
  daily_pnl: [], bot_stats: [], recent_trades: [], equity_series: [], granularity: 'day',
} as unknown as DashboardData

const day = (over: Partial<DepositDay>): DepositDay => ({
  day: '2026-10-25', equity_open: 1000, equity_close: 1000, change_usd: 0, result_usd: 0, result_pct: 0,
  realized_usd: 0, fees_usd: 0, funding_usd: 0, positions_usd: 0, deposits_usd: 0, withdrawals_usd: 0,
  trades: 0, live: false,
  unrealized_usd: null, unrealized_pct: null, clean_close: null, dd_worst_usd: null, dd_worst_pct: null, ...over,
})

// 25.10: equity 1010→1160 = +150, of which a 100 deposit → result +50 = 34 −3 −1 +20.
// 26.10: equity 1160→1140 = −20, with a 50 withdrawal → result +30 = 22 −2 +10.
const DAYS: DepositDay[] = [
  day({ day: '2026-10-25', equity_open: 1010, equity_close: 1160, change_usd: 150, result_usd: 50, result_pct: 4.95,
    realized_usd: 34, fees_usd: -3, funding_usd: -1, positions_usd: 20, deposits_usd: 100, trades: 4 }),
  day({ day: '2026-10-26', equity_open: 1160, equity_close: 1140, change_usd: -20, result_usd: 30, result_pct: 2.59,
    realized_usd: 22, fees_usd: -2, positions_usd: 10, withdrawals_usd: 50, trades: 2, live: true,
    unrealized_usd: -60, unrealized_pct: -5.26, clean_close: 1200, dd_worst_usd: -95, dd_worst_pct: -8.1 }),
]

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(accountsApi.listAccounts).mockResolvedValue([])
  vi.mocked(accountsApi.getAccountBalance).mockResolvedValue({ ok: false } as never)
  vi.mocked(accountsApi.getAccountPositions).mockResolvedValue({ ok: true, positions: [] })
  vi.mocked(dashboardApi.getDashboard).mockResolvedValue(dash)
})

test('«Депозит» заменяет «Просадку»: результат дня и из чего он сложился', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue(DAYS)
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)

  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(2))
  expect(screen.getByRole('heading', { name: 'Депозит' })).toBeInTheDocument()
  expect(screen.queryByText('Просадка')).toBeNull()
  expect(dashboardApi.getDashboardDeposit).toHaveBeenCalledWith('30d', 'acct-1')

  const [newest, older] = screen.getAllByTestId('deposit-day')
  // newest first, flagged in-progress; headline is the result, not the raw equity change
  expect(newest).toHaveTextContent('Пн, 26 октября')
  expect(newest).toHaveTextContent('идёт')
  expect(newest).toHaveTextContent('+$30.00') // headline = result without the 50 withdrawal: (1140 − 1160) + 50
  expect(newest).toHaveTextContent('$1,160.00 → $1,140.00')
  expect(newest).toHaveTextContent('Вывод -$50.00')
  expect(newest).toHaveTextContent('Открытые позиции+$10.00')
  // every component is listed, no catch-all "Прочее"
  expect(older).toHaveTextContent('Закрытые сделки · 4+$34.00')
  expect(older).toHaveTextContent('Комиссии-$3.00')
  expect(older).toHaveTextContent('Фандинг-$1.00')
  expect(older).toHaveTextContent('Ввод +$100.00')
  expect(screen.queryByText(/Прочее/)).toBeNull()
})

test('сводка за период суммирует результат и компоненты', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue(DAYS)
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)

  const summary = await screen.findByTestId('deposit-summary')
  expect(summary).toHaveTextContent('Результат за период')
  expect(summary).toHaveTextContent('+$80.00') // (1140 − 1010) − 100 deposited + 50 withdrawn
  expect(summary).toHaveTextContent('$1,010.00 → $1,140.00')
  expect(summary).toHaveTextContent('Закрытые сделки · 6+$56.00')
  expect(summary).toHaveTextContent('Открытые позиции+$30.00')
  expect(summary).toHaveTextContent('Ввод +$100.00')
  expect(summary).toHaveTextContent('Вывод -$50.00')
})

test('пустая история — подсказка, а не падение', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue([])
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  expect(await screen.findByText(/Данные накапливаются/)).toBeInTheDocument()
})

test('кнопка «Развернуть» открывает окно по центру с тем же списком, Esc и крестик закрывают', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue([DAYS[1]])
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(1))
  expect(screen.queryByTestId('deposit-modal')).toBeNull()

  fireEvent.click(screen.getByLabelText('Развернуть'))
  expect(screen.getByTestId('deposit-modal')).toBeInTheDocument()
  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(2)) // карточка + окно

  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.queryByTestId('deposit-modal')).toBeNull()

  fireEvent.click(screen.getByLabelText('Развернуть'))
  fireEvent.click(screen.getByLabelText('Закрыть'))
  expect(screen.queryByTestId('deposit-modal')).toBeNull()
})

test('в развёрнутом окне — свой фильтр День/Неделя/Месяц: результаты складываются в срезы', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue(DAYS)
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(2))

  fireEvent.click(screen.getByLabelText('Развернуть'))
  // the expanded view loads the whole history, independent of the dashboard period tabs
  await waitFor(() => expect(dashboardApi.getDashboardDeposit).toHaveBeenCalledWith('all', 'acct-1'))
  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(4)) // 2 in card + 2 in modal

  fireEvent.click(screen.getByRole('tab', { name: 'Неделя' }))
  // 25.10 (Sun) and 26.10 (Mon) fall into different ISO weeks → still two slices, labelled as ranges
  const modal = screen.getByTestId('deposit-modal')
  expect(modal).toHaveTextContent('19–25 октября')
  expect(modal).toHaveTextContent('26 окт – 1 ноя')

  fireEvent.click(screen.getByRole('tab', { name: 'Месяц' }))
  expect(modal).toHaveTextContent('Октябрь 2026')
  const monthSlices = Array.from(modal.querySelectorAll('[data-testid="deposit-day"]'))
  expect(monthSlices).toHaveLength(1)
  expect(monthSlices[0]).toHaveTextContent('+$80.00')            // 130 − 100 deposited + 50 withdrawn
  expect(monthSlices[0]).toHaveTextContent('+7.2%')              // return: 80 / (1010 + 100 deposited)
  expect(monthSlices[0]).toHaveTextContent('$1,010.00 → $1,140.00')
  expect(monthSlices[0]).toHaveTextContent('Закрытые сделки · 6+$56.00')
})

test('грязный депозит: чистый баланс, просадка на закрытие и максимальная — с процентами', async () => {
  vi.mocked(dashboardApi.getDashboardDeposit).mockResolvedValue(DAYS)
  render(<MemoryRouter><DashboardPage /></MemoryRouter>)
  await waitFor(() => expect(screen.getAllByTestId('deposit-day')).toHaveLength(2))

  const [newest, older] = screen.getAllByTestId('deposit-day')
  const dd = newest.querySelector('[data-testid="deposit-drawdown"]')!
  expect(dd).toHaveTextContent('Депозит$1,200.00')
  expect(dd).toHaveTextContent('Просадка на закрытие-$60.00-5.3%')
  expect(dd).toHaveTextContent('Макс. просадка-$95.00-8.1%')
  // legacy day recorded before drawdown tracking: no invented numbers
  expect(older.querySelector('[data-testid="deposit-drawdown"]')).toBeNull()
  // the period summary reports the deepest drawdown of the span
  expect(screen.getByTestId('deposit-summary')).toHaveTextContent('Макс. просадка-$95.00-8.1%')
})
