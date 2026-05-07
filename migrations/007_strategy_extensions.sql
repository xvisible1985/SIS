-- migrations/007_strategy_extensions.sql

ALTER TABLE strategies
  ADD COLUMN leverage                INT          NOT NULL DEFAULT 1,
  ADD COLUMN margin_type             TEXT         NOT NULL DEFAULT 'isolated',
  ADD COLUMN hedge_mode              BOOLEAN      NOT NULL DEFAULT false,
  ADD COLUMN strategy_type           TEXT         NOT NULL DEFAULT 'grid',
  ADD COLUMN signal_configs          JSONB        NOT NULL DEFAULT '[]',
  ADD COLUMN steps                   JSONB        DEFAULT NULL,
  ADD COLUMN trailing_stop_enabled   BOOLEAN      NOT NULL DEFAULT false,
  ADD COLUMN trailing_activation_pct NUMERIC(10,4),
  ADD COLUMN trailing_callback_pct   NUMERIC(10,4);

CREATE TABLE strategy_templates (
  id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT        NOT NULL,
  config     JSONB       NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_strategy_templates_owner ON strategy_templates(owner_id);
