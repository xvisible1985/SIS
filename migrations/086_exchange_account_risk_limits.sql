-- migrations/086_exchange_account_risk_limits.sql
-- Configurable per-account risk guardrails, surfaced in the UI (AccountsPage):
--   margin_warn_pct         — accountMMRate (%) at which a warning is logged
--   margin_pause_pct        — accountMMRate (%) at which new matrix/grid entries pause
--   max_symbol_notional_pct — cap on a single symbol's combined position notional,
--                             as % of account equity, aggregated across all strategies
--                             on that symbol (not per-strategy)
-- Defaults are starting points only — editable per account via the UI.
ALTER TABLE exchange_accounts
  ADD COLUMN IF NOT EXISTS margin_warn_pct         NUMERIC NOT NULL DEFAULT 50,
  ADD COLUMN IF NOT EXISTS margin_pause_pct        NUMERIC NOT NULL DEFAULT 75,
  ADD COLUMN IF NOT EXISTS max_symbol_notional_pct NUMERIC NOT NULL DEFAULT 25;
