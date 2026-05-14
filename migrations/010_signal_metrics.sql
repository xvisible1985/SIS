-- Aggregate snapshots written every 10 seconds by the signal engine.
CREATE TABLE IF NOT EXISTS signal_metrics_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    ts              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_units    INT         NOT NULL DEFAULT 0,
    active_subs     INT         NOT NULL DEFAULT 0,
    ws_conns        INT         NOT NULL DEFAULT 0,
    computes_per_sec FLOAT      NOT NULL DEFAULT 0,
    cpu_time_ms     BIGINT      NOT NULL DEFAULT 0,
    buffer_mem_mb   FLOAT       NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_signal_metrics_ts
    ON signal_metrics_snapshots (ts DESC);

-- Retain 7 days of history; older rows purged by the application.
-- (No automatic partition — simple DELETE WHERE ts < NOW() - INTERVAL '7 days'.)
