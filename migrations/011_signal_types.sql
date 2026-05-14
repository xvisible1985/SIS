CREATE TABLE IF NOT EXISTS signal_types (
    id         TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO signal_types (id, name) VALUES
    ('rsi-os',    'RSI Oversold'),
    ('macd-x',    'MACD Crossover'),
    ('gc',        'Golden Cross'),
    ('bb-sq',     'BB Squeeze'),
    ('stoch-x',   'Stochastic Cross'),
    ('vol-spike', 'Volume Spike'),
    ('breakout',  'Range Breakout'),
    ('ema-x',     'EMA Crossover'),
    ('div',       'RSI Divergence'),
    ('st-flip',   'SuperTrend Flip')
ON CONFLICT (id) DO NOTHING;
