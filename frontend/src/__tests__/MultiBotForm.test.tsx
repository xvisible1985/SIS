import { forwardRef, useImperativeHandle } from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi } from 'vitest'
import { MultiBotForm } from '../features/bots/components/MultiBotForm'
import { apiClient } from '../api/client'
import type { CreateBotInput } from '../features/bots/types'

// BotForm/HedgeBotForm are ~2000-line forms with their own API-backed effects (admin
// defaults, coin filter, instrument constraints) — irrelevant to what MultiBotForm itself
// is responsible for (collecting both legs' payloads via trySubmit and posting/patching
// the pair). Stub them down to just their trySubmit contract, exposed the same way the
// real forms expose it (forwardRef + useImperativeHandle), matching BotFormHandle /
// HedgeBotFormHandle in BotForm.tsx / HedgeBotForm.tsx.
const signalPayload: CreateBotInput = {
  name: 'stub', description: '', isPublic: false,
  symbolWhitelist: ['BTCUSDT'], symbolBlacklist: [],
  strategyConfig: { bot_kind: 'signal', symbol: 'BTCUSDT' },
  maxStrategies: 0, maxLongStrategies: 0, maxShortStrategies: 0, maxMarginUsdt: 0, maxSymConsecutiveRuns: 0,
  accountId: null, autoMode: false, ignoreCoinFilter: false,
}
const hedgePayload: CreateBotInput = {
  name: 'stub', description: '', isPublic: false,
  symbolWhitelist: [], symbolBlacklist: [],
  strategyConfig: { bot_kind: 'hedge', direction: 'both', hedge_act_type: 1 },
  maxStrategies: 0, maxLongStrategies: 0, maxShortStrategies: 0, maxMarginUsdt: 0, maxSymConsecutiveRuns: 0,
  accountId: null, autoMode: false,
}

vi.mock('../features/bots/components/BotForm', () => ({
  BotForm: forwardRef((_props, ref) => {
    useImperativeHandle(ref, () => ({ trySubmit: () => Promise.resolve(signalPayload) }))
    return <div data-testid="signal-form-stub" />
  }),
}))
vi.mock('../features/bots/components/HedgeBotForm', () => ({
  HedgeBotForm: forwardRef((_props, ref) => {
    useImperativeHandle(ref, () => ({ trySubmit: () => Promise.resolve(hedgePayload) }))
    return <div data-testid="hedge-form-stub" />
  }),
}))
vi.mock('../api/client', () => ({ apiClient: { post: vi.fn(), patch: vi.fn() } }))
vi.mock('../contexts/AccountContext', () => ({
  useSelectedAccount: () => ({ selectedAccountId: 'acct-1', setSelectedAccountId: vi.fn() }),
}))

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(apiClient.post).mockResolvedValue({ data: {} })
  vi.mocked(apiClient.patch).mockResolvedValue({ data: {} })
})

test('Создать disabled until a name is entered', () => {
  render(<MultiBotForm onClose={() => {}} onSaved={() => {}} />)
  expect(screen.getByText('Создать')).toBeDisabled()
  fireEvent.change(screen.getByPlaceholderText('BTC Signal + Hedge'), { target: { value: 'My Combo' } })
  expect(screen.getByText('Создать')).not.toBeDisabled()
})

test('create: posts /bots/multi with both legs\' strategyConfig plus the shared identity fields', async () => {
  const onSaved = vi.fn()
  const onClose = vi.fn()
  render(<MultiBotForm onClose={onClose} onSaved={onSaved} />)

  fireEvent.change(screen.getByPlaceholderText('BTC Signal + Hedge'), { target: { value: 'My Combo' } })
  fireEvent.click(screen.getByText('Создать'))

  await waitFor(() => expect(apiClient.post).toHaveBeenCalledTimes(1))
  const [url, body] = vi.mocked(apiClient.post).mock.calls[0]
  expect(url).toBe('/bots/multi')
  expect(body).toMatchObject({
    name: 'My Combo',
    accountId: 'acct-1',
    strategyConfig: signalPayload.strategyConfig,
    hedgeStrategyConfig: hedgePayload.strategyConfig,
    symbolWhitelist: signalPayload.symbolWhitelist,
  })
  expect(onSaved).toHaveBeenCalled()
  expect(onClose).toHaveBeenCalled()
})

test('edit: patches both legs by id instead of posting /bots/multi', async () => {
  const signalBot = { id: 'signal-1', name: 'My Combo', pairedBotId: 'hedge-1' } as never
  const hedgeBot = { id: 'hedge-1', name: 'My Combo' } as never

  render(<MultiBotForm signalBot={signalBot} hedgeBot={hedgeBot} onClose={() => {}} onSaved={() => {}} />)
  fireEvent.click(screen.getByText('Сохранить'))

  await waitFor(() => expect(apiClient.patch).toHaveBeenCalledTimes(2))
  const calledUrls = vi.mocked(apiClient.patch).mock.calls.map(c => c[0])
  expect(calledUrls).toContain('/bots/signal-1')
  expect(calledUrls).toContain('/bots/hedge-1')
  expect(apiClient.post).not.toHaveBeenCalled()
})
