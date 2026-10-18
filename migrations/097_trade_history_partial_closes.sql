-- Allow multiple partial-close rows (e.g. matrix per-level SL) within the same strategy
-- cycle, without breaking RecordStrategyTrade's existing "exactly one row per full cycle
-- close" upsert invariant on (strategy_id, cycle_num).
ALTER TABLE trade_history
    ADD COLUMN IF NOT EXISTS is_partial boolean NOT NULL DEFAULT false;

-- Re-scope the cycle-uniqueness index to terminal (non-partial) rows only — a matrix cycle
-- can have several partial rows (one per level SL) before it finally ends.
DROP INDEX IF EXISTS idx_trade_history_strategy_cycle;
CREATE UNIQUE INDEX IF NOT EXISTS idx_trade_history_strategy_cycle
    ON trade_history (strategy_id, cycle_num)
    WHERE strategy_id IS NOT NULL AND is_partial = false;
