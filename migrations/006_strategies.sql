CREATE TABLE strategies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id    UUID NOT NULL REFERENCES exchange_accounts(id) ON DELETE CASCADE,
    symbol        TEXT NOT NULL,
    category      TEXT NOT NULL DEFAULT 'linear',
    direction     TEXT NOT NULL DEFAULT 'long',   -- long | short | both
    status        TEXT NOT NULL DEFAULT 'stopped', -- active | finishing | stopped

    grid_levels   INT          NOT NULL DEFAULT 5,
    grid_active   INT          NOT NULL DEFAULT 3,
    grid_step_pct NUMERIC(10,4) NOT NULL DEFAULT 1.0,
    grid_size_usdt NUMERIC(18,2) NOT NULL DEFAULT 100,

    tp_mode       TEXT          NOT NULL DEFAULT 'total', -- per_level | total
    tp_pct        NUMERIC(10,4) NOT NULL DEFAULT 2.0,

    sl_type       TEXT          NOT NULL DEFAULT 'conditional', -- conditional | programmatic
    sl_pct        NUMERIC(10,4) NOT NULL DEFAULT 5.0,

    signal_filter BOOLEAN NOT NULL DEFAULT FALSE,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE strategy_cycles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    cycle_num   INT  NOT NULL,
    start_price NUMERIC(18,8),
    tp_order_id TEXT,
    sl_order_id TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    result      TEXT,
    realized_pnl NUMERIC(18,8),
    UNIQUE(strategy_id, cycle_num)
);

CREATE TABLE strategy_levels (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id       UUID NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    cycle_id          UUID NOT NULL REFERENCES strategy_cycles(id) ON DELETE CASCADE,
    level_idx         INT  NOT NULL,
    side              TEXT NOT NULL,
    target_price      NUMERIC(18,8) NOT NULL,
    size_usdt         NUMERIC(18,2) NOT NULL,
    qty               TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending',
    exchange_order_id TEXT,
    exchange_link_id  TEXT,
    placed_at         TIMESTAMPTZ,
    filled_at         TIMESTAMPTZ,
    filled_price      NUMERIC(18,8)
);

CREATE INDEX idx_strategy_cycles_strategy ON strategy_cycles(strategy_id);
CREATE INDEX idx_strategy_levels_cycle    ON strategy_levels(cycle_id);
CREATE INDEX idx_strategy_levels_order_id ON strategy_levels(exchange_order_id)
    WHERE exchange_order_id IS NOT NULL;
