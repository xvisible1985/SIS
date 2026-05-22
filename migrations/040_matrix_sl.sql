-- migrations/040_matrix_sl.sql
ALTER TABLE strategy_levels
  ADD COLUMN IF NOT EXISTS sl_order_id  TEXT,
  ADD COLUMN IF NOT EXISTS sl_price     NUMERIC(18,8),
  ADD COLUMN IF NOT EXISTS sl_replaced  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS slot         SMALLINT;
-- 'sl_closed' is a valid value for the status column (no constraint — it is TEXT).
