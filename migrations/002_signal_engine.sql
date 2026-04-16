-- migrations/002_signal_engine.sql

-- Minimal users table (full auth added in Plan 4)
CREATE TABLE IF NOT EXISTS users (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email      TEXT        NOT NULL UNIQUE,
    plan       TEXT        NOT NULL DEFAULT 'free',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Signals: definition of a trading signal as a JSON condition tree
CREATE TABLE IF NOT EXISTS signals (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    exchange    TEXT        NOT NULL,
    symbol      TEXT        NOT NULL,
    market      TEXT        NOT NULL,
    timeframe   TEXT        NOT NULL,
    direction   TEXT        NOT NULL DEFAULT 'LONG',  -- LONG | SHORT | BOTH
    conditions  JSONB       NOT NULL,
    is_active   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS signals_owner ON signals (owner_id);
CREATE INDEX IF NOT EXISTS signals_active ON signals (is_active) WHERE is_active = TRUE;

-- Backtest results
CREATE TABLE IF NOT EXISTS backtest_results (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    signal_id     UUID        REFERENCES signals(id) ON DELETE CASCADE,
    symbol        TEXT        NOT NULL,
    timeframe     TEXT        NOT NULL,
    period_from   TIMESTAMPTZ NOT NULL,
    period_to     TIMESTAMPTZ NOT NULL,
    mode          TEXT        NOT NULL,  -- 'fast' | 'walk_forward'
    total_signals INT         NOT NULL DEFAULT 0,
    win_count     INT         NOT NULL DEFAULT 0,
    loss_count    INT         NOT NULL DEFAULT 0,
    win_rate      NUMERIC(6,4) NOT NULL DEFAULT 0,
    avg_gain      NUMERIC(10,4) NOT NULL DEFAULT 0,
    max_drawdown  NUMERIC(10,4) NOT NULL DEFAULT 0,
    profit_factor NUMERIC(10,4) NOT NULL DEFAULT 0,
    patterns      JSONB       NOT NULL DEFAULT '{}',
    trades        JSONB       NOT NULL DEFAULT '[]',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS backtest_results_signal ON backtest_results (signal_id, created_at DESC);

-- Optimization results
CREATE TABLE IF NOT EXISTS optimization_results (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    signal_id        UUID        REFERENCES signals(id) ON DELETE CASCADE,
    job_params       JSONB       NOT NULL DEFAULT '{}',
    mode             TEXT        NOT NULL,  -- 'fast' | 'walk_forward'
    top_combinations JSONB       NOT NULL DEFAULT '[]',
    best_params      JSONB       NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
