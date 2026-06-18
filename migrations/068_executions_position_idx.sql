-- Add position_idx to trader_executions so fees can be split by direction
-- (long = 1, short = 2) when the same symbol has concurrent long+short strategies.
ALTER TABLE trader_executions ADD COLUMN IF NOT EXISTS position_idx INT;

-- Backfill: derive from side for 'Trade' executions where we can infer direction.
-- Buy opens long (idx=1) or closes short; Sell opens short (idx=2) or closes long.
-- We can't perfectly reconstruct, so leave historical rows NULL —
-- the fee query will fall back to unfiltered behaviour for those cycles.
