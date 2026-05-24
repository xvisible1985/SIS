ALTER TABLE bots
  ADD COLUMN max_strategies  INT           NOT NULL DEFAULT 0,
  ADD COLUMN max_margin_usdt NUMERIC(12,2) NOT NULL DEFAULT 0;

ALTER TABLE strategies
  ADD COLUMN bot_id UUID REFERENCES bots(id) ON DELETE SET NULL;

CREATE INDEX idx_strategies_bot_id ON strategies(bot_id) WHERE bot_id IS NOT NULL;
