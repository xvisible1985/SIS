-- 057_hedge_main_controls.sql
-- Adds suppression flags for hedge→main control actions.
-- hedge_tp_suppressed: when true, TP orders must not be placed/re-placed on this strategy.
-- hedge_sl_suppressed: when true, SL orders (cycle-level + per-level) must not be placed/re-placed.
-- hedge_stopped_by:    UUID of the hedge strategy that stopped this main strategy; NULL when not stopped by hedge.

ALTER TABLE strategies
  ADD COLUMN IF NOT EXISTS hedge_tp_suppressed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS hedge_sl_suppressed BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS hedge_stopped_by    UUID REFERENCES strategies(id) ON DELETE SET NULL;
