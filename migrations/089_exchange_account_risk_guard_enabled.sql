-- migrations/089_exchange_account_risk_guard_enabled.sql
-- Master on/off switch for the whole per-account risk guard (margin_warn_pct /
-- margin_pause_pct / max_symbol_notional_pct — see 086_exchange_account_risk_limits.sql).
-- When false, riskGate (pkg/strategy/risk.go) skips both the margin-ratio pause check
-- and the per-symbol notional cap entirely — the three threshold values stay stored but
-- are not enforced. Defaults to true (enabled) so existing accounts keep current behavior.
ALTER TABLE exchange_accounts
  ADD COLUMN IF NOT EXISTS risk_guard_enabled BOOLEAN NOT NULL DEFAULT true;
