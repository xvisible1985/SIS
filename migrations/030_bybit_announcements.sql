CREATE TABLE bybit_announcements (
    id              SERIAL PRIMARY KEY,
    announcement_id VARCHAR(64) UNIQUE NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    type_key        VARCHAR(50),
    type_title      VARCHAR(100),
    tags            TEXT[],
    url             TEXT,
    date_ts         BIGINT,
    start_date_ts   BIGINT,
    end_date_ts     BIGINT,
    is_new_listing  BOOLEAN DEFAULT false,
    is_delisting    BOOLEAN DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bybit_news_type ON bybit_announcements(type_key);
CREATE INDEX idx_bybit_news_date ON bybit_announcements(date_ts DESC);
CREATE INDEX idx_bybit_news_created ON bybit_announcements(created_at DESC);
