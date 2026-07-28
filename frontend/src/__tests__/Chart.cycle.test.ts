import { describe, it, expect } from 'vitest'
import { extractCycleNum, isOtherStrategyLinkId, deriveEffectiveCycleNum, isCycleLive } from '../components/terminal/Chart'

describe('extractCycleNum', () => {
  it('parses cycle from a TP order linkId', () => {
    expect(extractCycleNum('SIS_STR-abc12345-tp-5-1')).toBe(5)
  })

  it('parses cycle from a slotted TP order linkId', () => {
    expect(extractCycleNum('SIS_STR-abc12345-tpl2-5-1')).toBe(5)
  })

  it('parses cycle from an SL order linkId', () => {
    expect(extractCycleNum('SIS_STR-abc12345-sl-5-1')).toBe(5)
  })

  it('parses cycle from a matrix/grid entry linkId', () => {
    expect(extractCycleNum('SIS_STR-abc12345-5-2')).toBe(5)
  })

  it('returns null for unrecognised formats', () => {
    expect(extractCycleNum('')).toBeNull()
    expect(extractCycleNum(undefined)).toBeNull()
    expect(extractCycleNum('manual-order-id')).toBeNull()
  })
})

describe('isOtherStrategyLinkId', () => {
  it('is true for a different strategy id', () => {
    expect(isOtherStrategyLinkId('SIS_STR-deadbeef-tp-5-1', 'abc12345')).toBe(true)
  })

  it('is false for the same strategy id', () => {
    expect(isOtherStrategyLinkId('SIS_STR-abc12345-tp-5-1', 'abc12345')).toBe(false)
  })

  it('is false when stratIdShort is not known yet', () => {
    expect(isOtherStrategyLinkId('SIS_STR-deadbeef-tp-5-1', null)).toBe(false)
  })
})

describe('deriveEffectiveCycleNum', () => {
  const strat = 'abc12345'

  it('returns currentCycleNum directly when it is already known', () => {
    const orders = [{ orderLinkId: `SIS_STR-${strat}-4-1` }]
    expect(deriveEffectiveCycleNum(5, strat, orders)).toBe(5)
  })

  // Regression for the ghost-order-lines bug: right after switching strategy
  // cards (or before the per-strategy cycle fetch resolves), currentCycleNum is
  // briefly null. The chart must NOT fall back to "no cycle filter" during that
  // window — it must derive the real current cycle from the strategy's own
  // orders, so an already-reset old cycle's orders never render as stale lines.
  it('derives the max cycle from this strategy\'s own orders when currentCycleNum is null', () => {
    const orders = [
      { orderLinkId: `SIS_STR-${strat}-tp-4-1` },   // stale — already-reset cycle 4
      { orderLinkId: `SIS_STR-${strat}-5-1` },      // current cycle 5
      { orderLinkId: `SIS_STR-${strat}-sl-5-2` },   // current cycle 5
    ]
    expect(deriveEffectiveCycleNum(null, strat, orders)).toBe(5)
  })

  it('ignores another strategy\'s orders when deriving the fallback', () => {
    const orders = [
      { orderLinkId: 'SIS_STR-deadbeef-tp-9-1' }, // different strategy — must not leak in
    ]
    expect(deriveEffectiveCycleNum(null, strat, orders)).toBeNull()
  })

  it('returns null when there is nothing to derive from (no orders yet)', () => {
    expect(deriveEffectiveCycleNum(null, strat, [])).toBeNull()
  })

  it('returns null when the strategy id itself is not known yet', () => {
    const orders = [{ orderLinkId: `SIS_STR-${strat}-5-1` }]
    expect(deriveEffectiveCycleNum(null, null, orders)).toBeNull()
  })
})

describe('isCycleLive', () => {
  const strat = 'abc12345'

  // Regression for a ghost-lines bug distinct from deriveEffectiveCycleNum's (found live
  // 2026-07-17): once a matrix cycle ends, GetStrategyState deliberately empties
  // strategyLevels, but leftover exchange orders from that same now-dead cycle still
  // carry a cycle number that matches (nothing bumps it until a new cycle starts) — so
  // deriveEffectiveCycleNum/extractCycleNum alone can't catch this case. A live cycle
  // always has at least L(0), so zero levels + a known cycle number means "ended".
  it('is false once the cycle has ended (known cycle number, zero levels)', () => {
    expect(isCycleLive(1, strat, [])).toBe(false)
  })

  it('is true for a live cycle with at least one level', () => {
    expect(isCycleLive(1, strat, [{ level_idx: 1 }])).toBe(true)
  })

  it('is true when the cycle number has not loaded yet (nothing to gate on)', () => {
    expect(isCycleLive(null, strat, [])).toBe(true)
  })

  it('is true when no strategy is selected', () => {
    expect(isCycleLive(1, null, [])).toBe(true)
  })
})
