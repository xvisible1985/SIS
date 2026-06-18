-- Add multi-signal config arrays for TP/SL signal gates.
-- tp_signal_configs / sl_signal_configs hold a JSON array of {name, params} objects,
-- allowing multiple signals with AND logic (all must fire in the required direction).
ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS tp_signal_configs jsonb,
  ADD COLUMN IF NOT EXISTS sl_signal_configs jsonb;
