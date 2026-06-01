import { describe, it, expect } from 'vitest'
import { makeMergedEventLabel } from '../features/log-visualizer/utils'
import type { LVLevel, LVEvent } from '../features/log-visualizer/types'

describe('makeMergedEventLabel', () => {
  it('formats filled Buy level', () => {
    const level: LVLevel = { levelIdx: 3, side: 'Buy', filledPrice: 0.4225, qty: '47 USDT', status: 'filled', tsMs: 0 }
    expect(makeMergedEventLabel('level', undefined, level)).toBe('▲ L3 0.4225 · 47 USDT')
  })

  it('formats sl_closed Sell level', () => {
    const level: LVLevel = { levelIdx: 5, side: 'Sell', filledPrice: 1.2345, qty: '100 USDT', status: 'sl_closed', tsMs: 0 }
    expect(makeMergedEventLabel('level', undefined, level)).toBe('▼ L5 1.2345 · 100 USDT [SL]')
  })

  it('formats log event', () => {
    const log: LVEvent = { message: 'TP placed', level: 'info', tsMs: 0 }
    expect(makeMergedEventLabel('log', log, undefined)).toBe('TP placed')
  })

  it('returns fallback when kind is level but level is undefined', () => {
    expect(makeMergedEventLabel('level', undefined, undefined)).toBe('—')
  })
})
