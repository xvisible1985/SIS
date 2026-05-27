-- migrations/047_telegram_auth.sql

-- One-time tokens for magic-link login (TG → web)
CREATE TABLE IF NOT EXISTS telegram_auth_tokens (
    token      TEXT PRIMARY KEY,
    chat_id    BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '5 minutes'
);

CREATE INDEX IF NOT EXISTS idx_telegram_auth_tokens_chat
    ON telegram_auth_tokens (chat_id);

-- Allow muting notifications per-user
ALTER TABLE telegram_connections
    ADD COLUMN IF NOT EXISTS mute_until TIMESTAMPTZ;

-- Track which strategy error events already triggered a TG notification
ALTER TABLE strategy_events
    ADD COLUMN IF NOT EXISTS tg_notified BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_strategy_events_notify
    ON strategy_events (tg_notified, created_at DESC)
    WHERE tg_notified = FALSE;
