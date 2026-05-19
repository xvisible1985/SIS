-- migrations/021_user_management.sql

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role           TEXT         NOT NULL DEFAULT 'user'
        CHECK (role IN ('user', 'admin')),
    ADD COLUMN IF NOT EXISTS is_curator     BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS is_blocked     BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS email_verified BOOLEAN      NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS block_reason   TEXT,
    ADD COLUMN IF NOT EXISTS referrer_id    UUID         REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS novabot_balance NUMERIC(18,8) NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS users_role      ON users (role);
CREATE INDEX IF NOT EXISTS users_referrer  ON users (referrer_id) WHERE referrer_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS novabot_transactions (
    id         UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    admin_id   UUID          REFERENCES users(id) ON DELETE SET NULL,
    amount     NUMERIC(18,8) NOT NULL,
    note       TEXT          NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS novabot_transactions_user
    ON novabot_transactions (user_id, created_at DESC);
