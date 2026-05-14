ALTER TABLE signal_types
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'enabled',
    ADD COLUMN IF NOT EXISTS panel  TEXT NOT NULL DEFAULT 'signal';

UPDATE signal_types SET status = 'disabled' WHERE enabled = false;

ALTER TABLE indicator_types
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'enabled',
    ADD COLUMN IF NOT EXISTS panel  TEXT NOT NULL DEFAULT 'indicator';

UPDATE indicator_types SET status = 'disabled' WHERE enabled = false;
