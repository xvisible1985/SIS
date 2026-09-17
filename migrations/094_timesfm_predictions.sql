-- migrations/094_timesfm_predictions.sql
-- Registers the new "timesfm" catalog signal (pkg/signal/timesfm.go) and creates its
-- prediction log. Every real model call (never a cache hit — see timesfm.go) writes one row
-- here; services/api-gateway's accuracy backfill job (timesfm_accuracy_job.go) fills in the
-- outcome once the forecast's horizon has passed. This is a platform-wide model-evaluation
-- log, not per-user data (no owner_id) — unlike custom_signals, there's exactly one shared
-- "timesfm" signal definition, not user-created ones.

INSERT INTO signal_types (id, name, status, panel)
VALUES ('timesfm', 'TimesFM Forecast', 'enabled', 'signal')
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS timesfm_predictions (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  symbol              TEXT NOT NULL,
  timeframe           TEXT NOT NULL,
  context_bars        INT NOT NULL,
  horizon_bars        INT NOT NULL,
  predicted_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  price_at_predict    NUMERIC(18,8) NOT NULL,
  predicted_pct       NUMERIC(10,4) NOT NULL,
  predicted_direction TEXT NOT NULL,
  target_at           TIMESTAMPTZ NOT NULL,
  actual_price        NUMERIC(18,8),
  actual_direction    TEXT,
  correct             BOOLEAN,
  checked_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS timesfm_predictions_pending
  ON timesfm_predictions (target_at) WHERE actual_price IS NULL;

CREATE INDEX IF NOT EXISTS timesfm_predictions_symbol
  ON timesfm_predictions (symbol, predicted_at DESC);
