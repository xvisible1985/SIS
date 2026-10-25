import { forwardRef, useImperativeHandle } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { MultiBotForm } from '../features/bots/components/MultiBotForm'
import { apiClient } from '../api/client'
import type { Bot as BotType, CreateBotInput } from '../features/bots/types'

// Editing a Мультибот used to show the stats-reset question twice — once from each embedded
// leg form (both legs have trade history). The Мультибот now asks once for the pair and hands
// the answer down through resetStatsDecisionRef; these stubs read that ref the way the real
// forms do, so the test sees exactly what each leg would put into its payload.
const base: CreateBotInput = {
  name: 'stub', description: '', isPublic: false, symbolWhitelist: [], symbolBlacklist: [],
  maxStrategies: 0, maxLongStrategies: 0, maxShortStrategies: 0, maxMarginUsdt: 0, maxSymConsecutiveRuns: 0,
  accountId: null, autoMode: false,
}
function stub(kind: 'signal' | 'hedge') {
  return forwardRef((props: { resetStatsDecisionRef?: { current: boolean | null } }, ref) => {
    useImperativeHandle(ref, () => ({
      trySubmit: () => Promise.resolve({
        ...base, strategyConfig: { bot_kind: kind } as never,
        resetStats: props.resetStatsDecisionRef?.current ?? undefined,
      }),
    }))
    return <div data-testid={`${kind}-stub`} />
  })
}
vi.mock('../features/bots/components/BotForm', () => ({ BotForm: stub('signal') }))
vi.mock('../features/bots/components/HedgeBotForm', () => ({ HedgeBotForm: stub('hedge') }))
vi.mock('../api/client', () => ({ apiClient: { post: vi.fn(), patch: vi.fn() } }))
vi.mock('../contexts/AccountContext', () => ({
  useSelectedAccount: () => ({ selectedAccountId: 'acct-1', setSelectedAccountId: vi.fn() }),
}))

const leg = (id: string, over: Partial<BotType> = {}) => ({
  id, name: 'Multi', description: '', fullDescription: '', isPublic: false, autoMode: false,
  strategyConfig: {}, tradesTotal: 3, tradesWin: 2, netPnlTotal: 1.5, activeStrategiesCount: 0,
  ...over,
}) as unknown as BotType

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(apiClient.patch).mockResolvedValue({ data: {} })
})

test('asks the stats-reset question once for both legs; "Сохранить без сброса" keeps both histories', async () => {
  const onSaved = vi.fn()
  render(<MultiBotForm signalBot={leg('s1')} hedgeBot={leg('h1')} onClose={() => {}} onSaved={onSaved} />)

  fireEvent.click(screen.getByText('Сохранить'))

  expect(await screen.findAllByText('Обнуление статистики')).toHaveLength(1)
  fireEvent.click(screen.getByText('Сохранить без сброса'))

  await waitFor(() => expect(onSaved).toHaveBeenCalled())
  expect(apiClient.patch).toHaveBeenCalledTimes(2)
  for (const call of vi.mocked(apiClient.patch).mock.calls) {
    expect((call[1] as { resetStats?: boolean }).resetStats).toBe(false)
  }
})

test('"Обнулить и сохранить" sends resetStats=true for both legs', async () => {
  const onSaved = vi.fn()
  render(<MultiBotForm signalBot={leg('s1')} hedgeBot={leg('h1')} onClose={() => {}} onSaved={onSaved} />)

  fireEvent.click(screen.getByText('Сохранить'))
  await screen.findByText('Обнуление статистики')
  fireEvent.click(screen.getByText('Обнулить и сохранить'))

  await waitFor(() => expect(onSaved).toHaveBeenCalled())
  for (const call of vi.mocked(apiClient.patch).mock.calls) {
    expect((call[1] as { resetStats?: boolean }).resetStats).toBe(true)
  }
})

test('a public leg makes the reset mandatory — no "Сохранить без сброса" button', async () => {
  render(<MultiBotForm signalBot={leg('s1', { isPublic: true })} hedgeBot={leg('h1')} onClose={() => {}} onSaved={() => {}} />)

  fireEvent.click(screen.getByText('Сохранить'))
  await screen.findByText('Обнуление статистики')
  expect(screen.queryByText('Сохранить без сброса')).toBeNull()
})

test('no trade history on either leg: no reset question, saves straight away', async () => {
  const onSaved = vi.fn()
  render(<MultiBotForm signalBot={leg('s1', { tradesTotal: 0 })} hedgeBot={leg('h1', { tradesTotal: 0 })} onClose={() => {}} onSaved={onSaved} />)

  fireEvent.click(screen.getByText('Сохранить'))

  await waitFor(() => expect(onSaved).toHaveBeenCalled())
  expect(screen.queryByText('Обнуление статистики')).toBeNull()
})
