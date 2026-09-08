import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { StrategyModal } from '../components/strategies/StrategyModal'
import * as strategiesApi from '../api/strategies'
import type { Strategy } from '../types'

vi.mock('../api/strategies', async (importOriginal) => {
  const actual = await importOriginal<typeof strategiesApi>()
  return {
    ...actual,
    updateStrategy: vi.fn().mockResolvedValue(undefined),
    getStrategyState: vi.fn().mockResolvedValue({ cycle_num: 1, start_price: 0, levels: [] }),
    getInstrumentConstraints: vi.fn().mockResolvedValue({ max_leverage: 100, min_order_usdt: 0, tick_size: 0.01, qty_step: 0.001, min_qty: 0 }),
  }
})

const baseStrategy: Strategy = {
  id: 'strat-1', account_id: 'acc-1', symbol: 'BTCUSDT', category: 'linear', direction: 'long',
  status: 'active', grid_levels: 5, grid_active: 3, max_stop_active: 0, grid_step_pct: 1,
  grid_size_usdt: 100, tp_mode: 'total', tp_pct: 2, sl_type: 'conditional', sl_pct: -5,
  signal_filter: false, leverage: 1, margin_type: 'isolated', hedge_mode: false,
  strategy_type: 'grid', entry_order_type: 'limit', signal_configs: [], steps: [],
  trailing_stop_enabled: false, trailing_activation_pct: null, trailing_callback_pct: null,
  created_at: '', updated_at: '', volume_usdt: 0, active_levels: 0, last_pnl: 0,
  bot_id: 'bot-1', bot_name: 'Gonchar 2.0',
} as unknown as Strategy

describe('StrategyModal — apply to bot confirm', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('shows the confirm dialog when a bot-owned strategy changes a syncable field', async () => {
    render(<StrategyModal strategy={baseStrategy} onClose={() => {}} onSaved={() => {}} />)

    fireEvent.click(await screen.findByRole('button', { name: '3. Завершение' }))
    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))

    expect(await screen.findByRole('button', { name: /да, применить к боту/i })).toBeInTheDocument()
  })

  it('calls updateStrategy with applyToBot=true when confirmed', async () => {
    render(<StrategyModal strategy={baseStrategy} onClose={() => {}} onSaved={() => {}} />)

    fireEvent.click(await screen.findByRole('button', { name: '3. Завершение' }))
    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))
    fireEvent.click(await screen.findByRole('button', { name: /да, применить к боту/i }))

    await waitFor(() => {
      expect(strategiesApi.updateStrategy).toHaveBeenCalledWith(
        'strat-1',
        expect.objectContaining({ tp_pct: 5 }),
        { applyToBot: true },
      )
    })
  })

  it('does not show the confirm dialog for a manual (non-bot) strategy', async () => {
    render(<StrategyModal strategy={{ ...baseStrategy, bot_id: null, bot_name: null }} onClose={() => {}} onSaved={() => {}} />)

    fireEvent.click(await screen.findByRole('button', { name: '3. Завершение' }))
    const tpInput = await screen.findByTestId('strategy-tp-pct-input')
    fireEvent.change(tpInput, { target: { value: '5' } })
    fireEvent.blur(tpInput)
    fireEvent.click(screen.getByTestId('strategy-save-button'))

    await waitFor(() => expect(strategiesApi.updateStrategy).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: /да, применить к боту/i })).not.toBeInTheDocument()
  })
})
