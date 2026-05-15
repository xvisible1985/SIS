-- migrations/017_account_page.sql

ALTER TABLE users ADD COLUMN IF NOT EXISTS username TEXT;

CREATE TABLE IF NOT EXISTS telegram_connections (
    user_id      UUID    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    chat_id      BIGINT  NOT NULL,
    username     TEXT,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS telegram_pending_tokens (
    token      TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '10 minutes'
);

CREATE TABLE IF NOT EXISTS telegram_notification_settings (
    user_id   UUID    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    on_trade  BOOLEAN NOT NULL DEFAULT TRUE,
    on_signal BOOLEAN NOT NULL DEFAULT TRUE,
    on_balance BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS referral_codes (
    user_id    UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS referral_signups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    referee_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rewarded    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(referee_id)
);

-- Add indexes for query performance
CREATE UNIQUE INDEX IF NOT EXISTS users_username_unique ON users (username) WHERE username IS NOT NULL;

CREATE INDEX IF NOT EXISTS referral_signups_referrer_idx ON referral_signups (referrer_id);

CREATE INDEX IF NOT EXISTS telegram_pending_tokens_user_idx ON telegram_pending_tokens (user_id);
