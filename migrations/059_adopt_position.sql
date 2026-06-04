-- Stores exchange position snapshot for "adopt" detach mode.
-- When non-null, startMatrixCycle marks L(0) as already-filled instead of placing a market order.
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS adopt_position_data JSONB;
