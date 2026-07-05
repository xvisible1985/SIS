-- Speed up ListStrategies: the handler runs 4 correlated subqueries per row,
-- each JOINing strategy_cycles (filter ended_at IS NULL) with strategy_levels
-- (filter status = 'filled'). Without targeted indexes this causes full-table
-- scans as the tables grow, making the endpoint take 10+ seconds.
--
-- Active-cycle lookup: one per strategy, used by all 3 filled-levels subqueries.
CREATE INDEX IF NOT EXISTS idx_strategy_cycles_active
    ON strategy_cycles(strategy_id)
    WHERE ended_at IS NULL;

-- Latest closed cycle per strategy: used by the last_pnl subquery.
CREATE INDEX IF NOT EXISTS idx_strategy_cycles_closed_latest
    ON strategy_cycles(strategy_id, cycle_num DESC)
    WHERE ended_at IS NOT NULL;

-- Filled levels per cycle: used by volume_usdt + active_levels subqueries.
CREATE INDEX IF NOT EXISTS idx_strategy_levels_filled
    ON strategy_levels(cycle_id)
    WHERE status = 'filled';

-- Filled levels ordered by level_idx DESC: used by last_filled_price subquery (LIMIT 1).
CREATE INDEX IF NOT EXISTS idx_strategy_levels_filled_idx
    ON strategy_levels(cycle_id, level_idx DESC)
    WHERE status = 'filled';

-- Speed up GetStrategyState: ORDER BY cycle_num DESC LIMIT 1 on a per-strategy basis.
CREATE INDEX IF NOT EXISTS idx_strategy_cycles_latest
    ON strategy_cycles(strategy_id, cycle_num DESC);
