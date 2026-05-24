ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS safe_zone_pct float8,
  ADD COLUMN IF NOT EXISTS matrix_entry_level jsonb;
