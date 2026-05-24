-- Add official / NovaBot flag to bots table
ALTER TABLE bots ADD COLUMN IF NOT EXISTS is_official BOOLEAN NOT NULL DEFAULT FALSE;

-- Index for quickly listing official bots
CREATE INDEX IF NOT EXISTS bots_official ON bots (is_official, created_at DESC) WHERE is_official = TRUE;
