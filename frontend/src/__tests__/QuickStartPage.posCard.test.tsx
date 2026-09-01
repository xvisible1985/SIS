import { render, screen } from '@testing-library/react'
import { PosCard } from '../pages/QuickStartPage'
import type { Position } from '../types'

// GET /accounts/:id/positions (accounts_handler.go:GetAccountPositions) returns the raw
// trader.Position — unrealisedPnlPct is never populated there (only the WS-fed terminal
// path computes it client-side). This is the regression for QuickStartPage showing "NaN%"
// under the $ PnL: PosCard must compute its own percent from size×markPrice, not trust a
// field this endpoint never sends.
const basePos: Position = {
  symbol: 'BIOUSDT',
  side: 'Buy',
  size: '1139.0000',
  sizeUsdt: 0,
  entryPrice: '',
  markPrice: '0.0080',
  liqPrice: '0',
  unrealisedPnl: '-4.14',
  // Deliberately absent in the real API response — simulated here as undefined the way
  // JSON.parse of a response missing the key would leave it, not as an empty string.
  unrealisedPnlPct: undefined as unknown as string,
  leverage: '50',
  positionIdx: 1,
  category: 'linear',
}

test('computes a real percentage instead of NaN% when unrealisedPnlPct is absent', () => {
  render(<PosCard pos={basePos} />)
  expect(screen.queryByText(/NaN/)).not.toBeInTheDocument()
  // pnl=-4.14, notional = 1139 * 0.008 = 9.112 -> pct ≈ -45.43%
  expect(screen.getByText('-45.43%')).toBeInTheDocument()
})

test('shows 0.00% (not NaN%) when markPrice/size are unavailable', () => {
  render(<PosCard pos={{ ...basePos, size: '0', markPrice: '0' }} />)
  expect(screen.queryByText(/NaN/)).not.toBeInTheDocument()
  expect(screen.getByText('0.00%')).toBeInTheDocument()
})
