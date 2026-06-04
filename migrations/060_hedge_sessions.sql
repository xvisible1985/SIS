-- migrations/060_hedge_sessions.sql
-- Tracks each hedge bot activation session.
-- main_entry_at_start: exchange avg_entry of main position at hedge activation moment.
-- hedge_entry_at_start: exchange avg_entry of hedge position once first opened (set async).
-- gap_at_start: |main_entry - hedge_entry| — immutable reference, set when hedge first opens.
-- cumulative_hedge_pnl is computed on-the-fly in the API from trade_history.
CREATE TABLE hedge_sessions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bot_id               UUID NOT NULL REFERENCES bots(id)       ON DELETE CASCADE,
    main_strategy_id     UUID           REFERENCES strategies(id) ON DELETE SET NULL,
    hedge_strategy_id    UUID NOT NULL   REFERENCES strategies(id) ON DELETE CASCADE,
    main_entry_at_start  NUMERIC(18, 8),
    hedge_entry_at_start NUMERIC(18, 8),
    gap_at_start         NUMERIC(18, 8),
    started_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at             TIMESTAMPTZ
);

CREATE INDEX idx_hedge_sessions_hedge ON hedge_sessions(hedge_strategy_id);
CREATE INDEX idx_hedge_sessions_main  ON hedge_sessions(main_strategy_id);
