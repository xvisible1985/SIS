import { useRef } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { BotForm, type BotFormHandle } from '../features/bots/components/BotForm'
import type { Bot as BotType } from '../features/bots/types'

// BotForm is a ~2000-line form with its own API-backed effects (admin defaults, coin
// filter, instrument constraints, symbol list, activation-signal picker) that are
// irrelevant to the bug under test — a stats-reset confirm dialog that renders in-place
// instead of via a portal. Stub the same leaf pieces MultiBotForm.test.tsx already stubs
// BotForm/HedgeBotForm with, so this form can mount as itself.
vi.mock('../components/common/coinFilter', () => ({
  getCoinFilter: vi.fn().mockResolvedValue(null),
  checkCoinFlagged: vi.fn().mockReturnValue({ flagged: false }),
}))
vi.mock('../components/common/CoinMultiPicker', () => ({
  CoinMultiPicker: () => <div data-testid="coin-multi-picker-stub" />,
  getAllSymbols: vi.fn().mockResolvedValue([]),
  matchesPattern: vi.fn().mockReturnValue(false),
}))
vi.mock('../api/strategies', () => ({
  getInstrumentConstraints: vi.fn().mockResolvedValue(null),
}))
vi.mock('../components/strategies/FormWidgets', () => ({
  Toggle: () => <div data-testid="toggle-stub" />,
  Tip: () => <span data-testid="tip-stub" />,
  SignalPickerField: () => <div data-testid="signal-picker-stub" />,
  SignalGateField: () => <div data-testid="signal-gate-stub" />,
  sigPriorityKey: () => '',
}))
vi.mock('../contexts/AccountContext', () => ({
  useSelectedAccount: () => ({ selectedAccountId: 'acct-1', setSelectedAccountId: vi.fn() }),
}))

const botWithTrades = {
  id: 'signal-1',
  name: 'My Signal Leg',
  description: 'desc',
  tradesTotal: 42,
  tradesWin: 30,
  netPnlTotal: 123.45,
  strategyConfig: { bot_kind: 'signal', symbol: 'BTCUSDT' },
} as unknown as BotType

// Mirrors MultiBotForm's actual tab markup: the embedded BotForm instance is kept mounted
// but hidden with display:none whenever a different tab (e.g. "Основное"/"Хедж") is active,
// so refs stay attached across tab switches (see MultiBotForm.tsx).
function Harness({ onResult }: { onResult: (p: unknown) => void }) {
  const ref = useRef<BotFormHandle>(null)
  return (
    <div>
      <div style={{ display: 'none' }}>
        <BotForm ref={ref} embedded bot={botWithTrades} onSubmit={() => {}} onClose={() => {}} />
      </div>
      <button onClick={() => { void ref.current?.trySubmit().then(onResult) }}>trigger</button>
    </div>
  )
}

test('stats-reset confirm dialog stays reachable when the embedded form sits on a hidden tab (regression for the Мультибот save hang)', async () => {
  const onResult = vi.fn()
  render(<Harness onResult={onResult} />)

  fireEvent.click(screen.getByText('trigger'))

  // Before the fix, this dialog rendered in-place inside the display:none wrapper — present
  // in the DOM but invisible and unreachable, so the user could never resolve it and
  // trySubmit()'s promise (and MultiBotForm's "Сохранить") hung forever.
  const dialog = await screen.findByText('Обнуление статистики')
  expect(dialog).toBeVisible()

  fireEvent.click(screen.getByText('Обнулить и сохранить'))

  await waitFor(() => expect(onResult).toHaveBeenCalled())
  expect(onResult.mock.calls[0][0]).not.toBeNull()
})
