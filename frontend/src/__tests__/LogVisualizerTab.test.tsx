import { describe, it, expect } from 'vitest'
import { makeMergedEventLabel, filterEvents, computeCardStats, formatPnl, filterEventsList } from '../features/log-visualizer/utils'
import type { LVLevel, LVEvent, MergedEvent, LayerSettings, EventListFilter } from '../features/log-visualizer/types'
import { DEFAULT_LAYER_SETTINGS } from '../features/log-visualizer/types'

describe('makeMergedEventLabel', () => {
  it('formats filled Buy level', () => {
    const level: LVLevel = { levelIdx: 3, side: 'Buy', filledPrice: 0.4225, qty: '47 USDT', sizeUsdt: 0, status: 'filled', tsMs: 0 }
    expect(makeMergedEventLabel('level', undefined, level)).toBe('▲ L3 0.4225 · 47 USDT')
  })

  it('formats sl_closed Sell level', () => {
    const level: LVLevel = { levelIdx: 5, side: 'Sell', filledPrice: 1.2345, qty: '100 USDT', sizeUsdt: 0, status: 'sl_closed', tsMs: 0 }
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

// ── Test helpers ──────────────────────────────────────────────────────────────

function makeLevelEvent(side: 'Buy' | 'Sell' = 'Buy', sizeUsdt = 100): MergedEvent {
  return {
    tsMs: 0, kind: 'level',
    level: { levelIdx: 1, side, filledPrice: 50000, qty: '0.002', sizeUsdt, status: 'filled', tsMs: 0 },
    label: '',
  }
}

function makeLogEvent(level: 'info' | 'warn' | 'error' = 'info'): MergedEvent {
  return {
    tsMs: 0, kind: 'log',
    log: { message: 'test', level, tsMs: 0 },
    label: '',
  }
}

// ── filterEvents ──────────────────────────────────────────────────────────────

describe('filterEvents', () => {
  const all: MergedEvent[] = [
    makeLevelEvent('Buy'),
    makeLogEvent('info'),
    makeLogEvent('warn'),
    makeLogEvent('error'),
  ]

  it('default settings keeps all 4 events', () => {
    expect(filterEvents(all, DEFAULT_LAYER_SETTINGS)).toHaveLength(4)
  })

  it('showOrderMarkers=false removes the level event', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showOrderMarkers: false })
    expect(result).toHaveLength(3)
    expect(result.every(e => e.kind === 'log')).toBe(true)
  })

  it('showLogMarkers=false removes all 3 log events', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showLogMarkers: false })
    expect(result).toHaveLength(1)
    expect(result[0].kind).toBe('level')
  })

  it('showWarn=false removes only warn event', () => {
    const result = filterEvents(all, { ...DEFAULT_LAYER_SETTINGS, showWarn: false })
    expect(result).toHaveLength(3)
    expect(result.find(e => e.log?.level === 'warn')).toBeUndefined()
  })

  it('showLogMarkers=false overrides individual level flags', () => {
    const s: LayerSettings = { ...DEFAULT_LAYER_SETTINGS, showLogMarkers: false, showInfo: true }
    expect(filterEvents(all, s).filter(e => e.kind === 'log')).toHaveLength(0)
  })
})

// ── computeCardStats ──────────────────────────────────────────────────────────

describe('computeCardStats', () => {
  it('empty events → 0 count, 0 volume', () => {
    expect(computeCardStats([])).toEqual({ filledCount: 0, volumeUsdt: 0 })
  })

  it('counts only level events', () => {
    const events = [makeLevelEvent('Buy', 100), makeLogEvent('info')]
    expect(computeCardStats(events).filledCount).toBe(1)
  })

  it('sums sizeUsdt of all level events', () => {
    const events = [makeLevelEvent('Buy', 100), makeLevelEvent('Sell', 150)]
    expect(computeCardStats(events).volumeUsdt).toBe(250)
  })
})

// ── formatPnl ─────────────────────────────────────────────────────────────────

describe('formatPnl', () => {
  it('positive number gets + prefix', () => {
    expect(formatPnl(124.5)).toBe('+124.50 $')
  })

  it('negative number has no extra prefix', () => {
    expect(formatPnl(-30)).toBe('-30.00 $')
  })

  it('zero is treated as positive', () => {
    expect(formatPnl(0)).toBe('+0.00 $')
  })
})

// ── filterEventsList ──────────────────────────────────────────────────────────

function makeSlClosedLevel(): MergedEvent {
  return {
    tsMs: 0, kind: 'level',
    level: { levelIdx: 2, side: 'Sell', filledPrice: 50000, qty: '0.002', sizeUsdt: 100, status: 'sl_closed', tsMs: 0 },
    label: '',
  }
}

function makeLogWithMsg(level: 'info' | 'warn' | 'error', message: string): MergedEvent {
  return {
    tsMs: 0, kind: 'log',
    log: { message, level, tsMs: 0 },
    label: '',
  }
}

describe('filterEventsList', () => {
  const order   = makeLevelEvent('Buy')
  const slLevel = makeSlClosedLevel()
  const tpLog   = makeLogWithMsg('info', 'TP исполнен @ 99100')
  const slLog   = makeLogWithMsg('info', 'SL сработал')
  const errLog  = makeLogEvent('error')
  const infoLog = makeLogEvent('info')
  const all     = [order, slLevel, tpLog, slLog, errLog, infoLog]

  it("'all' returns every event unchanged", () => {
    expect(filterEventsList(all, 'all')).toHaveLength(6)
    expect(filterEventsList(all, 'all')).toBe(all)
  })

  it("'orders' returns only kind==='level' events (both filled and sl_closed)", () => {
    const result = filterEventsList(all, 'orders')
    expect(result).toHaveLength(2)
    expect(result.every(e => e.kind === 'level')).toBe(true)
  })

  it("'closes' returns sl_closed levels + logs matching TP/SL regex", () => {
    const result = filterEventsList(all, 'closes')
    expect(result).toHaveLength(3)
    expect(result).toContain(slLevel)
    expect(result).toContain(tpLog)
    expect(result).toContain(slLog)
  })

  it("'errors' returns only error-level log events", () => {
    const result = filterEventsList(all, 'errors')
    expect(result).toHaveLength(1)
    expect(result[0]).toBe(errLog)
  })

  it('empty array returns empty for all filters', () => {
    expect(filterEventsList([], 'all')).toHaveLength(0)
    expect(filterEventsList([], 'orders')).toHaveLength(0)
    expect(filterEventsList([], 'closes')).toHaveLength(0)
    expect(filterEventsList([], 'errors')).toHaveLength(0)
  })
})
