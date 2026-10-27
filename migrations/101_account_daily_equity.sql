-- Per-account, per-UTC-day equity snapshot (last snapshot of the day wins) plus the day's
-- external funds movements, powering the dashboard "Депозит" widget. A row may exist with
-- equity_close NULL when only flows were recorded for that day.
CREATE TABLE IF NOT EXISTS account_daily_equity (
    account_id      UUID          NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    day             DATE          NOT NULL,
    equity_open     NUMERIC(18,8),
    equity_close    NUMERIC(18,8),
    deposits_usd    NUMERIC(18,8) NOT NULL DEFAULT 0,
    withdrawals_usd NUMERIC(18,8) NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, day)
);

-- Last time the collector refreshed this account's deposit/withdrawal history from the exchange.
CREATE TABLE IF NOT EXISTS account_funds_sync (
    account_id UUID PRIMARY KEY REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    synced_at  TIMESTAMPTZ NOT NULL
);

-- One-time backfill of past days from the snapshots the frontend already collected.
INSERT INTO account_daily_equity (account_id, day, equity_open, equity_close)
SELECT account_id,
       (created_at AT TIME ZONE 'UTC')::date AS day,
       (ARRAY_AGG(equity ORDER BY created_at ASC))[1],
       (ARRAY_AGG(equity ORDER BY created_at DESC))[1]
FROM balance_snapshots
GROUP BY account_id, (created_at AT TIME ZONE 'UTC')::date
ON CONFLICT (account_id, day) DO NOTHING;
