-- Add signal-gated exit columns to strategies.
-- These allow a TP or SL to only trigger when a named signal is in a given direction.
ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS tp_signal_name TEXT,
  ADD COLUMN IF NOT EXISTS tp_signal_dir  TEXT,
  ADD COLUMN IF NOT EXISTS sl_signal_name TEXT,
  ADD COLUMN IF NOT EXISTS sl_signal_dir  TEXT;
