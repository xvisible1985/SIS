-- migrations/061_hedge_sessions_unique_active.sql
-- Prevent duplicate active sessions for the same hedge strategy.
-- Only one open (ended_at IS NULL) session per hedge_strategy_id is allowed.
CREATE UNIQUE INDEX idx_hedge_sessions_unique_active
    ON hedge_sessions(hedge_strategy_id)
    WHERE ended_at IS NULL;
