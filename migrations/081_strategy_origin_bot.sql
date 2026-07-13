-- 081_strategy_origin_bot.sql
-- Persistent reference to the bot that originally created a strategy. Unlike bot_id
-- (nulled on detach), origin_bot_id is set once at creation and never cleared, so the
-- "Привязать к боту" dialog can highlight the source bot even after detach.
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS origin_bot_id UUID;
