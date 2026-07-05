-- Persistent log of which strategy/bot opened a position.
-- No FK constraints so records survive strategy and bot deletion.
CREATE TABLE IF NOT EXISTS position_source_log (
    id           BIGSERIAL     PRIMARY KEY,
    account_id   UUID          NOT NULL,
    symbol       TEXT          NOT NULL,
    direction    TEXT          NOT NULL,
    strategy_id  UUID,
    bot_id       UUID,
    bot_name     TEXT,
    cycle_id     UUID          NOT NULL,
    cycle_num    INT           NOT NULL,
    start_price  NUMERIC,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_psl_lookup
    ON position_source_log (account_id, symbol, direction, created_at DESC);
