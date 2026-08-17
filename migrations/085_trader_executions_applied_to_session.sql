-- migrations/085_trader_executions_applied_to_session.sql
-- Marks a trader_executions row as already claimed by applyUnappliedFunding
-- (pkg/strategy/trade_recorder.go), so the same Funding execution — whether
-- observed via the real-time WS "execution" topic or the periodic REST syncer's
-- upsert of the same exec_id — is applied to hedge_sessions.accumulated_pnl
-- exactly once, never zero or twice.
ALTER TABLE trader_executions ADD COLUMN IF NOT EXISTS applied_to_session BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_trader_executions_unapplied_funding
  ON trader_executions (account_id, symbol)
  WHERE exec_type = 'Funding' AND applied_to_session = false;
