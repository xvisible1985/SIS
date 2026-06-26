-- migrations/073_whale_tables.sql

CREATE TABLE whale_addresses (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL CHECK (chain IN ('eth', 'tron')),
  label        TEXT,
  is_manual    BOOLEAN NOT NULL DEFAULT false,
  volume_30d   FLOAT8 NOT NULL DEFAULT 0,
  is_active    BOOLEAN NOT NULL DEFAULT true,
  last_seen_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (address, chain)
);

CREATE TABLE whale_events (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  address      TEXT NOT NULL,
  chain        TEXT NOT NULL,
  symbol       TEXT NOT NULL,
  amount_usd   FLOAT8 NOT NULL,
  direction    TEXT NOT NULL CHECK (direction IN ('buy', 'sell', 'neutral')),
  score        FLOAT8 NOT NULL,
  tx_hash      TEXT NOT NULL,
  detected_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON whale_events (symbol, detected_at DESC);
CREATE INDEX ON whale_events (detected_at DESC);
CREATE INDEX ON whale_events (address, detected_at DESC);
