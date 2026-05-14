CREATE TABLE IF NOT EXISTS indicator_types (
    id         TEXT PRIMARY KEY,
    name       TEXT        NOT NULL,
    enabled    BOOLEAN     NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO indicator_types (id, name) VALUES
    ('rsi',  'RSI'),
    ('macd', 'MACD'),
    ('ema',  'EMA'),
    ('sma',  'SMA'),
    ('bb',   'Bollinger Bands'),
    ('stoch','Stochastic'),
    ('atr',  'ATR'),
    ('adx',  'ADX'),
    ('ichi', 'Ichimoku'),
    ('vwap', 'VWAP'),
    ('obv',  'OBV'),
    ('cci',  'CCI'),
    ('wpr',  'Williams %R'),
    ('psar', 'Parabolic SAR'),
    ('mfi',  'MFI'),
    ('vol',  'Volume'),
    ('roc',  'Rate of Change'),
    ('st',   'SuperTrend'),
    ('kc',   'Keltner Channels'),
    ('ao',   'Awesome Oscillator')
ON CONFLICT (id) DO NOTHING;
