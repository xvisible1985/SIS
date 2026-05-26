-- migrations/043_max_stop_active.sql
ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS max_stop_active SMALLINT NOT NULL DEFAULT 0;
-- 0 = no limit (all per-level SL orders placed immediately)
-- N = at most N conditional stop orders on exchange simultaneously
