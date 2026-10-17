import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { OrderForm } from '../components/terminal/OrderForm'
import * as traderApi from '../api/trader'
import * as accountsApi from '../api/accounts'
import * as strategiesApi from '../api/strategies'

// CoinPicker fetches tickers/delistings directly via global fetch, independent of
// apiClient — stub it so tests don't make real network calls (mirrors
// SignalPreviewChart.test.tsx's and WebhooksPage.test.tsx's identical pattern).
vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }))

vi.mock('../api/trader', async (importOriginal) => {
  const actual = await importOriginal<typeof traderApi>()
  return {
    ...actual,
    placeOrder: vi.fn().mockResolvedValue({ ok: true }),
    cancelOrder: vi.fn().mockResolvedValue({ ok: true }),
    setLeverage: vi.fn().mockResolvedValue({ ok: true }),
  }
})

vi.mock('../api/accounts', async (importOriginal) => {
  const actual = await importOriginal<typeof accountsApi>()
  return {
    ...actual,
    listAccounts: vi.fn(),
  }
})

vi.mock('../api/strategies', async (importOriginal) => {
  const actual = await importOriginal<typeof strategiesApi>()
  return {
    ...actual,
    getInstrumentConstraints: vi.fn(),
  }
})

describe('OrderForm — Plan #4b/#4c exchange-aware precision', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(accountsApi.listAccounts as any).mockResolvedValue([
      { id: 'acc-binance', exchange: 'binance', label: 'bin', is_active: true },
    ])
    ;(strategiesApi.getInstrumentConstraints as any).mockResolvedValue({
      tick_size: 0.01, qty_step: 0.001, min_qty: 0.001, max_leverage: 0, min_notional_value: 100, min_order_usdt: 100,
    })
  })

  it('looks up instrument constraints for the account\'s own exchange, not a hardcoded default', async () => {
    render(
      <OrderForm accountId="acc-binance" symbol="BTCUSDT" onSymbolChange={() => {}} lastPrice="50000" orders={[]} hedgeMode={false} />
    )

    await waitFor(() => {
      expect(strategiesApi.getInstrumentConstraints).toHaveBeenCalledWith('BTCUSDT', 'linear', 'binance')
    })
  })

  it('rounds an unrounded price to the exchange tick size before placing the order', async () => {
    render(
      <OrderForm accountId="acc-binance" symbol="BTCUSDT" onSymbolChange={() => {}} lastPrice="50000" orders={[]} hedgeMode={false} />
    )

    await waitFor(() => expect(strategiesApi.getInstrumentConstraints).toHaveBeenCalled())

    const priceInput = screen.getByTestId('order-price-input')
    const qtyInput = screen.getByTestId('order-qty-input')
    // Deliberately over-precise — more decimal places than tick_size (0.01) allows, the
    // exact shape of value that produced the live Binance "-1111 Precision is over the
    // maximum defined for this asset" rejection this migration fixes.
    fireEvent.change(priceInput, { target: { value: '50123.456789' } })
    fireEvent.change(qtyInput, { target: { value: '0.5' } })

    fireEvent.click(screen.getByTestId('order-submit-button'))

    await waitFor(() => expect(traderApi.placeOrder).toHaveBeenCalled())
    const sentOrder = (traderApi.placeOrder as any).mock.calls[0][0]
    expect(sentOrder.price).toBe('50123.46')
  })

  it('rounds qty typed directly into the Кол-во field — not just qty derived from the USDT field', async () => {
    // qty_step=1 (integer-only) mirrors real Binance symbols like NEARUSDT/SUPERUSDT —
    // the exact shape that reproduced the live "-1111 Precision" rejection: this bug
    // survived the exchange-dispatch fix because handleSubmit only ever ran applyStep on
    // qty derived from the "Объём (USDT)" field, never on qty typed directly into
    // "Кол-во" — the far more common way to place a manual order.
    ;(strategiesApi.getInstrumentConstraints as any).mockResolvedValue({
      tick_size: 0.001, qty_step: 1, min_qty: 1, max_leverage: 0, min_notional_value: 5, min_order_usdt: 5,
    })
    render(
      <OrderForm accountId="acc-binance" symbol="NEARUSDT" onSymbolChange={() => {}} lastPrice="3.5" orders={[]} hedgeMode={false} />
    )

    await waitFor(() => expect(strategiesApi.getInstrumentConstraints).toHaveBeenCalled())

    fireEvent.change(screen.getByTestId('order-price-input'), { target: { value: '3.123' } })
    fireEvent.change(screen.getByTestId('order-qty-input'), { target: { value: '12.7' } })

    fireEvent.click(screen.getByTestId('order-submit-button'))

    await waitFor(() => expect(traderApi.placeOrder).toHaveBeenCalled())
    const sentOrder = (traderApi.placeOrder as any).mock.calls[0][0]
    expect(sentOrder.qty).toBe('13')
  })
})
