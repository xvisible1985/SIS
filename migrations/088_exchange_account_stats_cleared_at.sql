-- migrations/088_exchange_account_stats_cleared_at.sql
-- "Очистить статистику" on the Dashboard: a non-destructive marker, not a DELETE.
-- GetDashboard treats trade_history before this timestamp as invisible for this account
-- (in addition to the selected period's own lower bound — whichever is later wins), while
-- the underlying rows stay intact for every other consumer (accounting, hedge_sessions
-- accumulation, etc).
ALTER TABLE exchange_accounts ADD COLUMN IF NOT EXISTS stats_cleared_at TIMESTAMPTZ;
