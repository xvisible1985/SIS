import { describe, it, expect } from 'vitest'
import { priceChangePct, INDICATORS } from '../features/indicators/indicators'
import type { Candle } from '../features/indicators/types'

// Mirrors pkg/signal/signals_test.go's coverage for priceChangeSignal — this
// indicator is a duplicate (TS) implementation of that Go signal, used for
// the frontend preview/backtest. Keeping the two test suites parallel makes
// it obvious if the two implementations drift apart.

function candlesHourly(closes: number[]): Candle[] {
  const hourMs = 3600 * 1000
  const start = 1_700_000_000_000
  return closes.map((close, i) => ({
    time: start + i * hourMs,
    open: close, high: close, low: close, close, volume: 0,
  }))
}

function flatRun(hours: number, price: number, lastClose: number): Candle[] {
  const closes = Array.from({ length: hours }, () => price)
  closes.push(lastClose)
  return candlesHourly(closes)
}

describe('priceChangePct', () => {
  it('computes % change over the requested window', () => {
    const c = flatRun(24, 100, 125)
    expect(priceChangePct(c, 24)).toBeCloseTo(25, 5)
  })

  it('returns null when there is not enough history', () => {
    const c = flatRun(5, 100, 200)
    expect(priceChangePct(c, 24)).toBeNull()
  })

  it('uses wall-clock time, not candle count', () => {
    // 48 candles spaced 30 minutes apart = 24 real hours of history.
    const halfHourMs = 1800 * 1000
    const start = 1_700_000_000_000
    const closes = Array.from({ length: 48 }, () => 100)
    closes.push(125)
    const c: Candle[] = closes.map((close, i) => ({
      time: start + i * halfHourMs, open: close, high: close, low: close, close, volume: 0,
    }))
    expect(priceChangePct(c, 24)).toBeCloseTo(25, 5)
    expect(priceChangePct(c, 48)).toBeNull() // only 24h of history exists
  })
})

describe('price-change indicator (INDICATORS entry)', () => {
  const def = INDICATORS.find(d => d.id === 'price-change')!

  it('is registered', () => {
    expect(def).toBeTruthy()
  })

  it('rise + trend → buy', () => {
    const c = flatRun(24, 100, 125)
    const out = def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'trend', tf: '1h' }, c)
    expect(out.state).toBe('buy')
  })

  it('fall + trend → sell', () => {
    const c = flatRun(24, 100, 75)
    const out = def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'trend', tf: '1h' }, c)
    expect(out.state).toBe('sell')
  })

  it('rise + counter → sell', () => {
    const c = flatRun(24, 100, 125)
    const out = def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'counter', tf: '1h' }, c)
    expect(out.state).toBe('sell')
  })

  it('fall + counter → buy', () => {
    const c = flatRun(24, 100, 75)
    const out = def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'counter', tf: '1h' }, c)
    expect(out.state).toBe('buy')
  })

  it('below threshold → neutral regardless of mode', () => {
    const c = flatRun(24, 100, 110) // +10%, threshold 20%
    expect(def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'trend', tf: '1h' }, c).state).toBe('neutral')
    expect(def.compute!({ periodHours: 24, thresholdPct: 20, mode: 'counter', tf: '1h' }, c).state).toBe('neutral')
  })
})
