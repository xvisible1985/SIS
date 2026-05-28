-- migrations/050_tron_deposits.sql

CREATE TABLE IF NOT EXISTS tron_deposits (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_usdt  NUMERIC(20,6) NOT NULL,   -- желаемая сумма (напр. 50.00)
    amount_exact NUMERIC(20,6) NOT NULL,   -- уникальная сумма к оплате (напр. 50.07)
    status       TEXT        NOT NULL DEFAULT 'pending',  -- pending | confirmed | expired
    tx_hash      TEXT,                     -- хэш транзакции в блокчейне
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 minutes',
    confirmed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS tron_deposits_user_idx
    ON tron_deposits (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS tron_deposits_pending_idx
    ON tron_deposits (expires_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS tron_deposits_amount_idx
    ON tron_deposits (amount_exact)
    WHERE status = 'pending';

CREATE UNIQUE INDEX IF NOT EXISTS tron_deposits_tx_hash_idx
    ON tron_deposits (tx_hash)
    WHERE tx_hash IS NOT NULL;
