-- Trade history v2: add bybit dedup key and source attribution.

ALTER TABLE trade_history
    ADD COLUMN IF NOT EXISTS bybit_close_order_id text,
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'strategy';

-- Dedup for strategy trades: one row per (strategy, cycle).
CREATE UNIQUE INDEX IF NOT EXISTS idx_trade_history_strategy_cycle
    ON trade_history (strategy_id, cycle_num)
    WHERE strategy_id IS NOT NULL;

-- Dedup for manual trades: one row per (account, closing_order).
CREATE UNIQUE INDEX IF NOT EXISTS idx_trade_history_bybit_close
    ON trade_history (account_id, bybit_close_order_id)
    WHERE bybit_close_order_id IS NOT NULL;
