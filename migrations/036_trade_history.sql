CREATE TABLE trade_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id UUID REFERENCES strategies(id) ON DELETE SET NULL,
    bot_id      UUID REFERENCES bots(id)        ON DELETE SET NULL,
    account_id  UUID NOT NULL,
    owner_id    UUID NOT NULL,
    symbol      TEXT NOT NULL,
    category    TEXT NOT NULL,
    direction   TEXT NOT NULL,
    cycle_num   INT  NOT NULL,
    result      TEXT NOT NULL,        -- 'tp' | 'sl'
    avg_entry   NUMERIC(18,8),
    exit_price  NUMERIC(18,8),
    qty         NUMERIC(18,8),
    volume_usdt NUMERIC(18,4),
    pnl         NUMERIC(18,4),
    pnl_pct     NUMERIC(10,4),
    opened_at   TIMESTAMPTZ NOT NULL,
    closed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trade_history_owner   ON trade_history(owner_id,   closed_at DESC);
CREATE INDEX idx_trade_history_bot     ON trade_history(bot_id,     closed_at DESC);
CREATE INDEX idx_trade_history_strategy ON trade_history(strategy_id, closed_at DESC);
CREATE INDEX idx_trade_history_account ON trade_history(account_id, closed_at DESC);
