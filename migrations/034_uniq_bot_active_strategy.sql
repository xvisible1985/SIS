-- Prevent duplicate bot-managed strategies for the same symbol+direction.
-- The partial unique index covers only rows where a bot is actively managing
-- the strategy (status active/finishing, bot_id not null).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_bot_active_strategy
  ON strategies (bot_id, symbol, direction)
  WHERE status IN ('active', 'finishing') AND bot_id IS NOT NULL;
