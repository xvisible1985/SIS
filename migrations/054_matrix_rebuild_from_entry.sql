ALTER TABLE strategies ADD COLUMN IF NOT EXISTS matrix_rebuild_from_entry BOOLEAN DEFAULT false;
