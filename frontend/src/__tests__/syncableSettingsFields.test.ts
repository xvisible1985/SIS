import { describe, it, expect } from 'vitest'
import { SYNCABLE_SETTINGS_FIELDS, hasSyncableDiff } from '../features/bots/syncableSettingsFields'

describe('hasSyncableDiff', () => {
  it('returns false when nothing in the synchronizable set changed', () => {
    const a = { tp_pct: 2, symbol: 'BTCUSDT', status: 'active' }
    const b = { tp_pct: 2, symbol: 'ETHUSDT', status: 'stopped' }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })

  it('returns true when a synchronizable scalar field changed', () => {
    const a = { tp_pct: 2 }
    const b = { tp_pct: 3 }
    expect(hasSyncableDiff(a, b)).toBe(true)
  })

  it('returns true when a synchronizable array field changed', () => {
    const a = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    const b = { matrix_levels: [{ tp_pct: 3, size_pct: 10 }] }
    expect(hasSyncableDiff(a, b)).toBe(true)
  })

  it('returns false when an array field is deep-equal but a different object reference', () => {
    const a = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    const b = { matrix_levels: [{ tp_pct: 2, size_pct: 10 }] }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })

  it('SYNCABLE_SETTINGS_FIELDS does not include instance-specific fields', () => {
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('symbol')
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('direction')
    expect(SYNCABLE_SETTINGS_FIELDS).not.toContain('status')
  })
})
