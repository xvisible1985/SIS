-- migrations/001_initial.sql

CREATE TABLE IF NOT EXISTS candles (
    exchange   TEXT        NOT NULL,
    symbol     TEXT        NOT NULL,
    market     TEXT        NOT NULL,
    timeframe  TEXT        NOT NULL,
    open_time  TIMESTAMPTZ NOT NULL,
    open       NUMERIC     NOT NULL,
    high       NUMERIC     NOT NULL,
    low        NUMERIC     NOT NULL,
    close      NUMERIC     NOT NULL,
    volume     NUMERIC     NOT NULL,
    PRIMARY KEY (exchange, symbol, market, timeframe, open_time)
);

SELECT create_hypertable('candles', by_range('open_time'), if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS candles_lookup
    ON candles (exchange, symbol, market, timeframe, open_time DESC);

ALTER TABLE candles SET (timescaledb.compress = true, timescaledb.compress_segmentby = 'exchange,symbol,market,timeframe');

SELECT add_compression_policy('candles', compress_after => INTERVAL '7 days', if_not_exists => TRUE);
