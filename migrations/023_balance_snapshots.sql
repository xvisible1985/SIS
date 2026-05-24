-- Track equity over time to show 24h change in sidebar
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    equity     NUMERIC(18,8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS balance_snapshots_account_time
    ON balance_snapshots (account_id, created_at DESC);
