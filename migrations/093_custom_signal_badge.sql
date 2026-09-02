-- migrations/093_custom_signal_badge.sql
-- Combo signals showed a static "Комбо" badge everywhere they appeared (Webhooks combo
-- picker, strategy SignalPickerField/SignalGateField chips) — indistinguishable from each
-- other and not very informative once a user has more than one saved combo. Replace it with
-- a short user-chosen code (<=4 chars, e.g. "RSI+", "TR3X") shown on a distinctly-colored
-- badge instead of the fixed label.
ALTER TABLE custom_signals ADD COLUMN IF NOT EXISTS badge TEXT NOT NULL DEFAULT '';

-- Backfill existing rows (created before this column existed) with a derived badge so the
-- length CHECK below can apply uniformly going forward.
UPDATE custom_signals SET badge = UPPER(LEFT(name, 4)) WHERE badge = '';

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'custom_signals_badge_len') THEN
    ALTER TABLE custom_signals ADD CONSTRAINT custom_signals_badge_len CHECK (char_length(badge) BETWEEN 1 AND 4);
  END IF;
END $$;
