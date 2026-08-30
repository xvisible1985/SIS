-- migrations/091_custom_signals.sql
-- Lets a user combine several existing catalog signals into one named, reusable AND-combo
-- ("custom signal") and use it wherever a catalog signal is used today, starting with
-- Webhooks alerts. Private per-owner — not part of the shared admin-curated signal_types
-- catalog, so it never shows up in /admin/signal-types management.

CREATE TABLE IF NOT EXISTS custom_signals (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS custom_signals_owner ON custom_signals (owner_id);

-- One row per AND-combo leg. component_signal_id references the built-in pkg/signal
-- registry (signal_types.id), not another custom_signals row — combos are flat, no
-- nesting, matching how pkg/signal's computeUnit.compute() already AND-combines a flat
-- []Config list (see engine.go), so a saved combo can be replayed by simply loading its
-- component rows into that same []Config shape.
CREATE TABLE IF NOT EXISTS custom_signal_components (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  custom_signal_id   UUID NOT NULL REFERENCES custom_signals(id) ON DELETE CASCADE,
  component_signal_id TEXT NOT NULL REFERENCES signal_types(id),
  params             JSONB NOT NULL DEFAULT '{}',
  position           SMALLINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS custom_signal_components_parent ON custom_signal_components (custom_signal_id, position);

-- A webhook now watches EITHER a built-in catalog signal OR a saved custom combo, never
-- both/neither. catalog_signal_id was NOT NULL — dropping that so the CHECK below is the
-- single source of truth for "exactly one of the two is set".
ALTER TABLE webhooks
  ALTER COLUMN catalog_signal_id DROP NOT NULL,
  ADD COLUMN IF NOT EXISTS custom_signal_id UUID REFERENCES custom_signals(id) ON DELETE CASCADE;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'webhooks_signal_source_xor') THEN
    ALTER TABLE webhooks ADD CONSTRAINT webhooks_signal_source_xor CHECK (
      (catalog_signal_id IS NOT NULL AND custom_signal_id IS NULL) OR
      (catalog_signal_id IS NULL AND custom_signal_id IS NOT NULL)
    );
  END IF;
END $$;
