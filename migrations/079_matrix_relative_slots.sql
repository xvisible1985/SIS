-- Novabot-style relative matrix slots: opt-in mode where slots renumber toward
-- entry after a per-slot SL fires. Mutually exclusive with matrix_rebuild_on_sl /
-- matrix_rebuild_from_entry (ignored when relative_slots is true).
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS relative_slots BOOLEAN NOT NULL DEFAULT false;
