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

  describe('matrix-field null-vs-default false positive (grid entities)', () => {
    // A form/template editor always fills matrix_levels/matrix_entry_level with a
    // non-null default literal regardless of strategy_type, while the stored side is
    // genuinely null/absent for a grid entity — see syncableSettingsFields.ts.
    const formDefaultedMatrixLevels = [{ direction: 'below', price_step_pct: -1.5, size_pct: 5 }]
    const formDefaultedMatrixEntry = { size_pct: 10, stop_pct: -5 }

    it('does not report a diff when only the form-defaulted matrix fields differ from stored null on a grid entity', () => {
      const a = {
        strategy_type: 'grid', tp_pct: 2,
        matrix_levels: formDefaultedMatrixLevels, matrix_entry_level: formDefaultedMatrixEntry,
      }
      const b = {
        strategy_type: 'grid', tp_pct: 2,
        matrix_levels: null, matrix_entry_level: null,
      }
      expect(hasSyncableDiff(a, b)).toBe(false)
    })

    it('still reports a real (non-matrix) field diff on a grid entity even with mismatched matrix filler', () => {
      const a = {
        strategy_type: 'grid', tp_pct: 5,
        matrix_levels: formDefaultedMatrixLevels, matrix_entry_level: formDefaultedMatrixEntry,
      }
      const b = {
        strategy_type: 'grid', tp_pct: 2,
        matrix_levels: null, matrix_entry_level: null,
      }
      expect(hasSyncableDiff(a, b)).toBe(true)
    })

    it('still reports a genuine matrix_levels diff when the entity is actually matrix-type', () => {
      const a = { strategy_type: 'matrix', matrix_levels: [{ direction: 'below', price_step_pct: -1.5, size_pct: 5 }] }
      const b = { strategy_type: 'matrix', matrix_levels: [{ direction: 'below', price_step_pct: -2.0, size_pct: 5 }] }
      expect(hasSyncableDiff(a, b)).toBe(true)
    })

    it('does not report a diff when a real matrix entity is genuinely unchanged', () => {
      const levels = [{ direction: 'below', price_step_pct: -1.5, size_pct: 5 }]
      const entry = { size_pct: 10, stop_pct: -5 }
      const a = { strategy_type: 'matrix', matrix_levels: levels, matrix_entry_level: entry }
      const b = { strategy_type: 'matrix', matrix_levels: levels, matrix_entry_level: entry }
      expect(hasSyncableDiff(a, b)).toBe(false)
    })

    it('reports a diff when switching a strategy from grid to matrix with real matrix config', () => {
      const a = { strategy_type: 'matrix', matrix_levels: [{ direction: 'below', price_step_pct: -1.5, size_pct: 5 }] }
      const b = { strategy_type: 'grid', matrix_levels: null }
      expect(hasSyncableDiff(a, b)).toBe(true)
    })

    it('treats a manual strategy_type the same as grid — matrix filler is not a real diff', () => {
      const a = { strategy_type: 'manual', matrix_levels: formDefaultedMatrixLevels }
      const b = { strategy_type: 'manual', matrix_levels: null }
      expect(hasSyncableDiff(a, b)).toBe(false)
    })
  })

  describe('key-order-insensitive comparison for array-of-object fields', () => {
    it('does not report a diff when an object field inside an array has reordered keys (jsonb round-trip)', () => {
      const a = { matrix_levels: [{ direction: 'below', price_step_pct: -1.5, size_pct: 5 }] }
      const b = { matrix_levels: [{ size_pct: 5, direction: 'below', price_step_pct: -1.5 }] }
      expect(hasSyncableDiff(a, b)).toBe(false)
    })

    it('still detects a real diff when array element order changes', () => {
      const a = { steps: [{ price_move_pct: 0, size_pct: 50 }, { price_move_pct: -1.5, size_pct: 100 }] }
      const b = { steps: [{ price_move_pct: -1.5, size_pct: 100 }, { price_move_pct: 0, size_pct: 50 }] }
      expect(hasSyncableDiff(a, b)).toBe(true)
    })
  })
})

describe('ALS hedge settings', () => {
  const thresholds = [{ trigger_type: 1, trigger_pct: -2, target_hedge_pct: 20 }]

  it('reports a diff when only the ALS thresholds changed (so "apply to active" is offered)', () => {
    const a = { strategy_type: 'als', als_thresholds: [{ ...thresholds[0], target_hedge_pct: 30 }] }
    const b = { strategy_type: 'als', als_thresholds: thresholds }
    expect(hasSyncableDiff(a, b)).toBe(true)
  })

  it('reports a diff for each other ALS limit / flag', () => {
    const base = { strategy_type: 'als', als_thresholds: thresholds }
    for (const [field, value] of [
      ['als_stop_cond_pct', 3], ['als_stop_replace_pct', 1], ['unsafe_hedge_min_pct', 5],
      ['unsafe_hedge_max_pct', 80], ['stop_market_size_k', 1.2], ['tp_control_enabled', true],
      ['initial_stop_be_pct', 2], ['als_exchange_orders', true], ['size_as_main', true],
    ] as const) {
      expect(hasSyncableDiff({ ...base, [field]: value }, base), field).toBe(true)
    }
  })

  it('ignores form-filled ALS defaults on a non-ALS entity', () => {
    const a = { strategy_type: 'matrix', tp_pct: 2, als_thresholds: thresholds, unsafe_hedge_max_pct: 80, als_stop_cond_pct: 3 }
    const b = { strategy_type: 'matrix', tp_pct: 2, als_thresholds: null }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })

  it('treats a missing boolean as false (form writes explicit false, stored config omits it)', () => {
    const a = { strategy_type: 'als', size_as_main: false, als_exchange_orders: false, tp_control_enabled: false }
    const b = { strategy_type: 'als' }
    expect(hasSyncableDiff(a, b)).toBe(false)
  })
})
