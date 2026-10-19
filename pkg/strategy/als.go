package strategy

import "context"

// startAlsCycle starts a brand-new ALS hedge cycle. Stub — see
// docs/superpowers/plans/2026-10-18-als-hedge-engine-core.md for the real implementation
// (will create the strategy_cycles row and seed strategy_levels from AlsThresholds).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	return nil
}

// resumeAlsCycle resumes an ALS cycle that was already running (e.g. after a process
// restart). Stub — see the next plan. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeAlsCycle(ctx context.Context) {
}

// restartAlsCycle re-applies strategy config to an already-running ALS cycle (settings
// changed mid-flight). Stub — see the next plan. Must be called WITHOUT sr.mu held.
func (sr *StrategyRunner) restartAlsCycle(ctx context.Context) {
}

// resumeAlsAfterSignal resumes an ALS cycle that was waiting on an activation signal.
// Stub — see the next plan. Must be called with startMu held and WITHOUT sr.mu held.
func (sr *StrategyRunner) resumeAlsAfterSignal(ctx context.Context) {
}

// handleAlsLevelFill reacts to one ALS threshold level's order filling. Stub — see the
// next plan (will update strategy_levels status and place the level's breakeven stop).
// Must be called with sr.mu held.
func (sr *StrategyRunner) handleAlsLevelFill(ctx context.Context, levelID string, filledPrice float64) {
}

// alsCancelPerLevelSLs cancels every stop-order placed for this ALS strategy's filled
// levels (mirrors matrixCancelPerLevelSLs) — called on stop/restart. Stub — see the next
// plan. Must be called with sr.mu held.
func (sr *StrategyRunner) alsCancelPerLevelSLs(ctx context.Context) {
}
