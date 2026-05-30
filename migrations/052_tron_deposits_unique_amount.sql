-- migrations/052_tron_deposits_unique_amount.sql
-- Enforce DB-level uniqueness: no two active (pending) deposits can share
-- the same amount_exact. This prevents the race-condition where two concurrent
-- INSERT … WHERE NOT EXISTS both succeed before either commits.
--
-- When a deposit expires or is confirmed the status changes, so the partial index
-- no longer covers it — the slot becomes available for new pending deposits.
CREATE UNIQUE INDEX IF NOT EXISTS tron_deposits_pending_amount_uniq
    ON tron_deposits (amount_exact)
    WHERE status = 'pending';
