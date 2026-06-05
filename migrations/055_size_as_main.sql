-- Strategy: deposit = main-position volume mode
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS size_as_main bool NOT NULL DEFAULT false;
