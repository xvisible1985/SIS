-- Add subscription price column to bots table
ALTER TABLE bots ADD COLUMN IF NOT EXISTS price_usd_month NUMERIC(12,2) NOT NULL DEFAULT 0;
