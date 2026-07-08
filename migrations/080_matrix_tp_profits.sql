-- 080_matrix_tp_profits.sql
-- Records realized PnL from each global matrix-TP re-arm.
--
-- A matrix strategy takes profit via its global TP many times within a single
-- long-lived cycle (handleMatrixTPFill closes the whole position, then re-anchors
-- and re-enters). trade_history has a UNIQUE(strategy_id, cycle_num) index, so it
-- can hold only ONE row per cycle — it cannot accumulate the many TP profits of a
-- matrix cycle. This table holds one row per matrix-TP fill so the "Накоплено"
-- counter (GetHedgeSession) can sum them alongside trade_history + per-level SL.
CREATE TABLE IF NOT EXISTS matrix_tp_profits (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id    UUID        NOT NULL REFERENCES strategies(id) ON DELETE CASCADE,
    bot_id         UUID,
    account_id     UUID        NOT NULL,
    cycle_num      INTEGER     NOT NULL,
    symbol         TEXT        NOT NULL,
    gross_pnl      NUMERIC     NOT NULL DEFAULT 0,
    fees           NUMERIC     NOT NULL DEFAULT 0,
    net_pnl        NUMERIC     NOT NULL DEFAULT 0,
    bybit_order_id TEXT,
    closed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotency: a WS fill event can be replayed on reconnect; the closing order is
-- unique per account, so DO NOTHING on conflict prevents double-counting.
CREATE UNIQUE INDEX IF NOT EXISTS matrix_tp_profits_order_uq
    ON matrix_tp_profits (account_id, bybit_order_id)
    WHERE bybit_order_id IS NOT NULL;

-- Accumulation lookups filter by strategy_id since the last paired-close (closed_at).
CREATE INDEX IF NOT EXISTS matrix_tp_profits_strat_idx
    ON matrix_tp_profits (strategy_id, closed_at);
