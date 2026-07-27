-- migrations/084_rescue_bot.sql
-- RescueBot: отслеживает совокупный объём, снятый с мейн-позиции частичными
-- закрытиями, профинансированными реализованным PnL хеджа (hedge_sessions.
-- accumulated_pnl), плюс метку времени для кулдауна между шагами. См.
-- docs/superpowers/specs/2026-07-26-rescuebot-design.md.
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS main_reduced_coin NUMERIC(18, 8) NOT NULL DEFAULT 0;
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS main_reduced_usdt NUMERIC(18, 8) NOT NULL DEFAULT 0;
ALTER TABLE hedge_sessions ADD COLUMN IF NOT EXISTS last_partial_close_at TIMESTAMPTZ;
