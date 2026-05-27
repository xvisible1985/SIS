-- Persistent cache of max leverage per (symbol, category), refreshed every 10 minutes.
CREATE TABLE IF NOT EXISTS symbol_leverage (
    symbol       TEXT        NOT NULL,
    category     TEXT        NOT NULL DEFAULT 'linear',
    max_leverage INT         NOT NULL DEFAULT 1,
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (symbol, category)
);
