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

/** True if any field in SYNCABLE_SETTINGS_FIELDS differs (deep) between a and b. */
export function hasSyncableDiff(
  a: Record<string, unknown>,
  b: Record<string, unknown>,
): boolean {
  return SYNCABLE_SETTINGS_FIELDS.some(
    field => JSON.stringify(a[field] ?? null) !== JSON.stringify(b[field] ?? null),
  )
}
