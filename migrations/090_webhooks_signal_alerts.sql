-- migrations/090_webhooks_signal_alerts.sql
-- Redesigns `webhooks` from "notify an arbitrary user-supplied URL when one of the user's
-- own condition-tree `signals` rows fires" (never actually wired up — nothing ever
-- published to the signals:fired stream) into "alert on a CATALOG signal (pkg/signal
-- registry, e.g. rsi-os/whale/leverage) for a chosen symbol+timeframe+params, delivered to
-- a SIS-generated relay URL that forwards to Telegram". Table is empty in production
-- (dormant feature), so this is a clean restructure, no backfill needed.
ALTER TABLE webhooks
  DROP CONSTRAINT IF EXISTS webhooks_signal_id_fkey,
  DROP COLUMN IF EXISTS signal_id;

DROP INDEX IF EXISTS webhooks_signal;

-- Token: two concatenated gen_random_uuid()s (no dashes) — unguessable, unique, and needs
-- no extension (pgcrypto's gen_random_bytes/digest aren't installed on this DB).
ALTER TABLE webhooks
  ADD COLUMN IF NOT EXISTS catalog_signal_id TEXT NOT NULL REFERENCES signal_types(id),
  ADD COLUMN IF NOT EXISTS symbol             TEXT NOT NULL,
  ADD COLUMN IF NOT EXISTS timeframe          TEXT NOT NULL DEFAULT '15m',
  ADD COLUMN IF NOT EXISTS params              JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS token              TEXT NOT NULL
    DEFAULT (replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', ''));

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'webhooks_token_unique') THEN
    ALTER TABLE webhooks ADD CONSTRAINT webhooks_token_unique UNIQUE (token);
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS webhooks_active_lookup ON webhooks (id) WHERE is_active = TRUE;
