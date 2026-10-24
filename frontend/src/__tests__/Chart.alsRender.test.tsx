import { render } from '@testing-library/react'
import { vi } from 'vitest'

// Stub lightweight-charts with a self-returning proxy: every chart/series method exists and
// returns something chainable, so Chart's effects run end to end and any exception thrown by
// OUR code (not the library) surfaces as a test failure.
vi.mock('lightweight-charts', () => {
  const make = (): any => new Proxy(function () {}, {
    get: (_t, p) => (p === 'then' ? undefined : p === Symbol.toPrimitive ? () => 0 : p === 'priceToCoordinate' ? () => 100 : make()),
    apply: () => make(),
  })
  return {
    createChart: () => make(), createSeriesMarkers: () => make(),
    CandlestickSeries: {}, ColorType: { Solid: 'solid' },
  }
})

class RO { observe() {} unobserve() {} disconnect() {} }
;(globalThis as any).ResizeObserver = RO

import { Chart } from '../components/terminal/Chart'

const candles = Array.from({ length: 30 }, (_, i) => ({ time: 1_700_000_000 + i * 60, open: 0.19, high: 0.192, low: 0.188, close: 0.19 }))

// Regression guard for the 2026-10-24 "chart vanished" report: render Chart with a full ALS
// strategy state (re-armed levels, stamped link IDs, resting orders, fills, Safe Zone).
test('Chart renders an ALS strategy state without throwing', () => {
  const sid = 'abc12345'
  const levels = [
    { level_idx: 1, side: 'Sell', target_price: 0, size_usdt: 0, status: 'sl_closed', filled_price: 0.1832, exchange_order_id: '' },
    { level_idx: 1, side: 'Sell', target_price: 0, size_usdt: 0, status: 'filled', filled_price: 0.1869, exchange_order_id: '' },
    { level_idx: 2, side: 'Sell', target_price: 0.1842, size_usdt: 0, status: 'placed', filled_price: 0, exchange_order_id: 'o2' },
    { level_idx: 3, side: 'Sell', target_price: 0.1745, size_usdt: 0, status: 'placed', filled_price: 0, exchange_order_id: 'o3' },
  ] as any
  const orders = [
    { orderId: 'o2', orderLinkId: `SIS_STR-${sid}-alsx-1-2-k9x2ab`, symbol: 'DNAUSDT', side: 'Sell', orderType: 'Market', price: '0', qty: '56', cumExecQty: '0', orderStatus: 'Untriggered', triggerPrice: '0.1842', category: 'linear', orderFilter: 'StopOrder' },
    { orderId: 'o3', orderLinkId: `SIS_STR-${sid}-alsx-1-3-k9x2ac`, symbol: 'DNAUSDT', side: 'Sell', orderType: 'Market', price: '0', qty: '233', cumExecQty: '0', orderStatus: 'Untriggered', triggerPrice: '0.1745', category: 'linear', orderFilter: 'StopOrder' },
  ] as any
  const executions = [
    { execId: 'e1', symbol: 'DNAUSDT', side: 'Sell', price: 0.1869, qty: '106', timeMs: 1_700_000_100_000, orderLinkId: `SIS_STR-${sid}-als-1-1-k9x2aa` },
    { execId: 'e2', symbol: 'DNAUSDT', side: 'Sell', price: 0.1832, qty: '109', timeMs: 1_700_000_000_000, orderLinkId: `SIS_STR-${sid}-als-1-1` },
    { execId: 'e3', symbol: 'DNAUSDT', side: 'Buy', price: 0.18137, qty: '109', timeMs: 1_700_000_050_000, orderLinkId: `SIS_STR-${sid}-alssl-1-1` },
  ] as any
  const settings = { showPositions: true, showPlacedOrders: true, showTakenOrders: true, showTakeProfit: true, showStopLoss: true, bothDirections: false, showSafeZone: true, showSLMarkers: true }
  const { unmount } = render(
    <Chart candles={candles} candleSymbol="DNAUSDT" positions={[]} orders={orders} executions={executions}
      symbol="DNAUSDT" lastPrice="0.1887" onLoadMore={async () => {}} overlaySettings={settings}
      strategyDir="short" stratIdShort={sid} currentCycleNum={1} strategyLevels={levels}
      safeZone={{ low: 0.17865, high: 0.18137 }} hedgePairTarget={null} />,
  )
  unmount()
})
