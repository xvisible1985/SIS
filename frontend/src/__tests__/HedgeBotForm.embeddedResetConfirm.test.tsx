import { useRef } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { HedgeBotForm, type HedgeBotFormHandle } from '../features/bots/components/HedgeBotForm'
import { apiClient } from '../api/client'
import type { Bot as BotType } from '../features/bots/types'

// HedgeBotForm is a ~2000-line form with its own API-backed effects, irrelevant to the bug
// under test — a stats-reset confirm dialog that used to render in-place instead of via a
// portal. Stub the same leaf pieces MultiBotForm.test.tsx already stubs this form with.
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn(), patch: vi.fn() } }))
vi.mock('../api/strategies', () => ({
  getInstrumentConstraints: vi.fn().mockResolvedValue(null),
}))
vi.mock('../components/strategies/FormWidgets', () => ({
  Toggle: () => <div data-testid="toggle-stub" />,
  Tip: () => <span data-testid="tip-stub" />,
  SignalPickerField: () => <div data-testid="signal-picker-stub" />,
}))
vi.mock('../contexts/AccountContext', () => ({
  useSelectedAccount: () => ({ selectedAccountId: 'acct-1', setSelectedAccountId: vi.fn() }),
}))

beforeEach(() => {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { catalog: [], mine: [] } })
})

const hedgeBotWithTrades = {
  id: 'hedge-1',
  name: 'My Hedge Leg',
  description: 'desc',
  tradesTotal: 7,
  tradesWin: 4,
  netPnlTotal: -12.3,
  strategyConfig: { bot_kind: 'hedge', direction: 'both', hedge_act_type: 1 },
} as unknown as BotType

// Mirrors MultiBotForm's actual tab markup: the embedded HedgeBotForm instance stays
// mounted but hidden with display:none whenever a different tab (e.g. "Основное"/"Сигнал")
// is active, so refs stay attached across tab switches (see MultiBotForm.tsx).
function Harness({ onResult }: { onResult: (p: unknown) => void }) {
  const ref = useRef<HedgeBotFormHandle>(null)
  return (
    <div>
      <div style={{ display: 'none' }}>
        <HedgeBotForm ref={ref} embedded hideFilters bot={hedgeBotWithTrades} onSubmit={() => {}} onClose={() => {}} />
      </div>
      <button onClick={() => { void ref.current?.trySubmit().then(onResult) }}>trigger</button>
    </div>
  )
}

test('stats-reset confirm dialog stays reachable when the embedded hedge form sits on a hidden tab (regression for the Мультибот save hang)', async () => {
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
