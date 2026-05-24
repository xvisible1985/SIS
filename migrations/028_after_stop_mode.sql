-- Add after_stop_mode to strategies table
ALTER TABLE strategies ADD COLUMN IF NOT EXISTS after_stop_mode VARCHAR(20) NOT NULL DEFAULT 'restart';
