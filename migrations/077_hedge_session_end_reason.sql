-- Track why a hedge/matrix-pair session ended, so the cumulative PnL counter
-- can reset only on genuine paired closes and keep accumulating across
-- deactivation/trailing-profit stop-restart cycles.
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS end_reason TEXT;

-- Timestamp of a per-level matrix SL close, needed to filter realized_pnl
-- into the correct session window (previously only filled_at was tracked,
-- which records entry time, not close time).
ALTER TABLE strategy_levels ADD COLUMN IF NOT EXISTS sl_closed_at TIMESTAMPTZ;
