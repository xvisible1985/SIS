-- migrations/063_coin_filter.sql

-- Single-row table for coin filter settings
CREATE TABLE IF NOT EXISTS coin_filter_settings (
  id                INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  min_turnover_usdt NUMERIC  NOT NULL DEFAULT 500000,
  blacklist         TEXT[]   NOT NULL DEFAULT '{}',
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed default row
INSERT INTO coin_filter_settings DEFAULT VALUES ON CONFLICT DO NOTHING;

-- Per-bot override flag
ALTER TABLE bots
  ADD COLUMN IF NOT EXISTS ignore_coin_filter BOOLEAN NOT NULL DEFAULT FALSE;
