-- migrations/004_webhooks.sql

CREATE TABLE IF NOT EXISTS webhooks (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    signal_id  UUID        NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
    url        TEXT        NOT NULL,
    platform   TEXT        NOT NULL DEFAULT 'custom',
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS webhooks_owner  ON webhooks (owner_id);
CREATE INDEX IF NOT EXISTS webhooks_signal ON webhooks (signal_id) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS webhook_logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id  UUID        NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status_code INT         NOT NULL DEFAULT 0,
    response_ms INT         NOT NULL DEFAULT 0,
    success     BOOLEAN     NOT NULL DEFAULT FALSE,
    error       TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS webhook_logs_webhook ON webhook_logs (webhook_id, sent_at DESC);
