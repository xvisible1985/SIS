// Fields shared between an open strategy's row and a bot's strategy_config template —
// used to decide whether an edit is worth offering to sync in either direction (see
// docs/superpowers/specs/2026-09-07-bot-strategy-settings-sync-design.md). Deliberately
// excludes instance-specific fields (symbol, direction, category, status) that exist only
// on one side, or mean something different there (a bot's `direction` is a template
// default, not a live position's direction).
export const SYNCABLE_SETTINGS_FIELDS = [
  'grid_levels', 'grid_active', 'grid_step_pct', 'grid_size_usdt',
  'tp_mode', 'tp_pct', 'sl_type', 'sl_pct', 'signal_filter',
  'leverage', 'margin_type', 'hedge_mode', 'entry_order_type',
  'signal_configs', 'steps',
  'trailing_stop_enabled', 'trailing_activation_pct', 'trailing_callback_pct',
  'matrix_levels', 'matrix_entry_level', 'safe_zone_pct',
  'protected_build', 'matrix_rebuild_on_sl', 'matrix_rebuild_from_entry', 'relative_slots',
] as const

export type SyncableSettingsField = typeof SYNCABLE_SETTINGS_FIELDS[number]

// The matrix-config field cluster — every field that only means anything for a
// matrix-type strategy/bot. Every form/template editor (StrategyModal's
// toForm/defaultForm, BotForm's/HedgeBotForm's defaultConfig) always fills all of these
// with a non-null default literal so the matrix tab has something to render, even for a
// grid entity where they're genuinely null/absent in storage. Comparing them
// unconditionally would make hasSyncableDiff report a "change" on every grid save (form
// default vs stored null) even when nothing the user controls actually changed — see
// docs/superpowers/plans/2026-09-07-bot-strategy-settings-sync.md. Kept as its own list
// (rather than inferring from name) so it stays an explicit, reviewable subset of
// SYNCABLE_SETTINGS_FIELDS.
const MATRIX_ONLY_FIELDS = new Set<SyncableSettingsField>([
  'matrix_levels', 'matrix_entry_level', 'safe_zone_pct',
  'protected_build', 'matrix_rebuild_on_sl', 'matrix_rebuild_from_entry', 'relative_slots',
])

/** Deep structural equality. Arrays are compared element-by-element in order (index order
 * is meaningful — e.g. matrix_levels/steps ordering), but plain-object keys are compared
 * unordered, since Postgres' jsonb round-trip does not preserve the key order the frontend
 * writes objects in and a raw JSON.stringify comparison would otherwise flag that key
 * reordering as a fake diff for array-of-object fields (matrix_levels, signal_configs, steps). */
function deepEqual(x: unknown, y: unknown): boolean {
  if (x === y) return true
  if (typeof x !== 'object' || typeof y !== 'object' || x === null || y === null) return false
  if (Array.isArray(x) || Array.isArray(y)) {
    if (!Array.isArray(x) || !Array.isArray(y) || x.length !== y.length) return false
    return x.every((v, i) => deepEqual(v, y[i]))
  }
  const xRec = x as Record<string, unknown>
  const yRec = y as Record<string, unknown>
  const xKeys = Object.keys(xRec)
  const yKeys = Object.keys(yRec)
  if (xKeys.length !== yKeys.length) return false
  return xKeys.every(k => Object.prototype.hasOwnProperty.call(yRec, k) && deepEqual(xRec[k], yRec[k]))
}

/** True if any field in SYNCABLE_SETTINGS_FIELDS differs (deep) between a and b.
 *
 * `a`/`b` are expected to each carry their own `strategy_type` (a Strategy's own field, or
 * a StrategyConfig template's) — every real call site's payload does, since the form/config
 * builders always default it. MATRIX_ONLY_FIELDS are only compared when at least one side
 * is actually `'matrix'`; for a grid (or manual) entity, the other side's form-filled
 * matrix defaults are ignored rather than compared against stored null, since they don't
 * represent a real user-facing change. A genuine matrix-vs-matrix diff (or a type switch
 * into/out of matrix) is still fully compared. Absence of `strategy_type` on both sides
 * (e.g. a caller not passing strategy-shaped objects at all) falls back to comparing
 * matrix fields normally, matching the pre-existing behavior. */
export function hasSyncableDiff(
  a: Record<string, unknown>,
  b: Record<string, unknown>,
): boolean {
  const aType = a['strategy_type']
  const bType = b['strategy_type']
  const strategyTypeKnown = aType !== undefined || bType !== undefined
  const isMatrix = aType === 'matrix' || bType === 'matrix'
  const skipMatrixFields = strategyTypeKnown && !isMatrix

  return SYNCABLE_SETTINGS_FIELDS.some(field => {
    if (skipMatrixFields && MATRIX_ONLY_FIELDS.has(field)) return false
    return !deepEqual(a[field] ?? null, b[field] ?? null)
  })
}
