-- Track the last N symbols each bot ran, so consecutive-run checks
-- survive strategy deletion (after_stop_mode = delete).
CREATE TABLE IF NOT EXISTS bot_symbol_history (
    id         BIGSERIAL    PRIMARY KEY,
    bot_id     UUID         NOT NULL REFERENCES bots(id) ON DELETE CASCADE,
    symbol     TEXT         NOT NULL,
    ran_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_bot_symbol_history_bot_ran
    ON bot_symbol_history (bot_id, ran_at DESC);
