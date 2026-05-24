-- Add full_description field for detailed bot info
ALTER TABLE bots ADD COLUMN IF NOT EXISTS full_description TEXT NOT NULL DEFAULT '';
