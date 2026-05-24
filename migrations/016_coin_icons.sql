CREATE TABLE IF NOT EXISTS coin_icons (
    symbol      TEXT PRIMARY KEY,           -- lowercase base, e.g. "btc"
    data        BYTEA,                       -- NULL means all CDNs failed
    content_type TEXT NOT NULL DEFAULT 'image/png',
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
