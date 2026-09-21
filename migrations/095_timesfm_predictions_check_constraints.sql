-- migrations/095_timesfm_predictions_check_constraints.sql
-- Backstops context_bars/horizon_bars > 0 at the database layer — Fix 1 (pkg/signal/registry.go)
-- now clamps these at signal-construction time, but the schema itself should enforce the
-- invariant too, independent of which application code path ends up writing to this table.

ALTER TABLE timesfm_predictions
  ADD CONSTRAINT timesfm_predictions_context_bars_positive CHECK (context_bars > 0),
  ADD CONSTRAINT timesfm_predictions_horizon_bars_positive CHECK (horizon_bars > 0);
