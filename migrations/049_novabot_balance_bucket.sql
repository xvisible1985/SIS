-- migrations/049_novabot_balance_bucket.sql
-- Add bucket to distinguish real deposits from virtual (admin-issued) credits.

ALTER TABLE novabot_transactions
  ADD COLUMN IF NOT EXISTS bucket TEXT NOT NULL DEFAULT 'virtual'
    CHECK (bucket IN ('real', 'virtual'));
