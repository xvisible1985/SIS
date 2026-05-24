CREATE TABLE proxies (
    id            SERIAL PRIMARY KEY,
    protocol      VARCHAR(10)  NOT NULL DEFAULT 'http',
    host          VARCHAR(255) NOT NULL,
    port          INT          NOT NULL,
    username      VARCHAR(255),
    password_enc  TEXT,
    weight        INT          NOT NULL DEFAULT 1,
    is_active     BOOLEAN      NOT NULL DEFAULT true,
    health_status VARCHAR(20)  NOT NULL DEFAULT 'unknown',
    last_checked  TIMESTAMPTZ,
    fail_count    INT          NOT NULL DEFAULT 0,
    total_reqs    BIGINT       NOT NULL DEFAULT 0,
    active_reqs   INT          NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_proxies_active ON proxies(is_active);
