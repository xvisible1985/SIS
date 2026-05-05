-- migrations/005_exchange_accounts.sql

CREATE TABLE IF NOT EXISTS exchange_accounts (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exchange    TEXT        NOT NULL CHECK (exchange IN ('bybit', 'binance')),
    label       TEXT        NOT NULL DEFAULT '',
    api_key_enc TEXT        NOT NULL,
    secret_enc  TEXT        NOT NULL,
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner_id, exchange, label)
);
CREATE INDEX IF NOT EXISTS exchange_accounts_owner ON exchange_accounts (owner_id);

CREATE TABLE IF NOT EXISTS trader_orders (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id     UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    order_link_id  TEXT        NOT NULL UNIQUE,
    order_id       TEXT,
    exchange       TEXT        NOT NULL,
    symbol         TEXT        NOT NULL,
    category       TEXT        NOT NULL,
    side           TEXT        NOT NULL,
    order_type     TEXT        NOT NULL,
    qty            NUMERIC     NOT NULL,
    price          NUMERIC,
    trigger_price  NUMERIC,
    status         TEXT        NOT NULL DEFAULT 'New',
    cum_exec_qty   NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_value NUMERIC     NOT NULL DEFAULT 0,
    cum_exec_fee   NUMERIC     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS trader_orders_owner   ON trader_orders (owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS trader_orders_account ON trader_orders (account_id, status);
CREATE INDEX IF NOT EXISTS trader_orders_link    ON trader_orders (order_link_id);

CREATE TABLE IF NOT EXISTS trader_executions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    UUID        NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    exec_id       TEXT        NOT NULL,
    order_id      TEXT,
    order_link_id TEXT,
    exchange      TEXT        NOT NULL,
    symbol        TEXT        NOT NULL,
    category      TEXT        NOT NULL,
    side          TEXT,
    exec_type     TEXT        NOT NULL,
    qty           NUMERIC,
    price         NUMERIC,
    exec_value    NUMERIC,
    exec_fee      NUMERIC,
    fee_rate      NUMERIC,
    is_maker      BOOLEAN,
    exec_time     TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (account_id, exec_id)
);
CREATE INDEX IF NOT EXISTS trader_executions_owner   ON trader_executions (owner_id, exec_time DESC);
CREATE INDEX IF NOT EXISTS trader_executions_account ON trader_executions (account_id, exec_type, exec_time DESC);
CREATE INDEX IF NOT EXISTS trader_executions_link    ON trader_executions (order_link_id) WHERE order_link_id IS NOT NULL;
