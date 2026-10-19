package strategy

import (
	"context"
	"fmt"
)

// alsThresholdPrice computes the absolute price at which one AlsThreshold row fires.
// dir is the ALS strategy's OWN (hedge) direction — sr.strategy.Direction, never the
// main position's — see the direction-convention note in als_calc_test.go for the full
// reasoning. triggerType 0 = % from the last filled order (lastOrderPrice); 1 =
// % from the main position's average entry (avgEntryPrice) — mirrors the existing
// hedge-activation act_type convention (0/1), not the source ALS's ROI-based units; see
// docs/superpowers/specs/2026-10-18-als-hedge-strategy-design.md. triggerPct is signed:
// negative means "adverse move" (price against the main position).
func alsThresholdPrice(dir Direction, triggerType int, triggerPct, avgEntryPrice, lastOrderPrice float64) float64 {
	base := avgEntryPrice
	if triggerType == 0 {
		base = lastOrderPrice
	}
	if dir == DirectionShort {
		// Hedge Short ⟹ main is Long ⟹ adverse-for-main = price FALLING ⟹ trigger
		// lands BELOW base (a negative triggerPct correctly lowers it).
		return base * (1 + triggerPct/100)
	}
	// Hedge Long (or Both, treated as Long here — ALS's per-level direction should
	// never actually be "Both"; the UI constrains thresholds to a single hedge
	// direction) ⟹ main is Short ⟹ adverse-for-main = price RISING ⟹ trigger lands
	// ABOVE base (a negative triggerPct correctly raises it).
	return base * (1 - triggerPct/100)
}

// alsThresholdCrossed reports whether currentPrice has reached triggerPrice in the
// adverse-for-main direction. dir is the hedge's own direction, same convention as
// alsThresholdPrice: hedge Short (long main) crosses on a FALL to/below triggerPrice;
// hedge Long (short main) crosses on a RISE to/above triggerPrice.
func alsThresholdCrossed(dir Direction, currentPrice, triggerPrice float64) bool {
	if dir == DirectionShort {
		return currentPrice <= triggerPrice
	}
	return currentPrice >= triggerPrice
}

// alsTopUpUsdt computes how much MORE hedge volume (in USDT) to place for one threshold
// row: the row's target is a CUMULATIVE percentage of the main position's current live
// volume (mainUsdt — pass effectiveDeposit(currentPrice), which already resolves to the
// opposite-direction position's live USDT value when SizeAsMain is set), not a flat
// per-level add. Only the gap between that target and what's already open gets placed —
// see the design spec's worked example. Never negative: if the hedge is already at or
// above this level's target (e.g. main shrank since an earlier level fired), there is
// nothing to top up.
func alsTopUpUsdt(targetHedgePct, mainUsdt, alreadyOpenUsdt float64) float64 {
	target := targetHedgePct / 100 * mainUsdt
	topUp := target - alreadyOpenUsdt
	if topUp < 0 {
		return 0
	}
	return topUp
}

// startAlsCycle starts a brand-new ALS hedge cycle. Stub — see
// docs/superpowers/plans/2026-10-18-als-hedge-engine-core.md for the real implementation
// (will create the strategy_cycles row and seed strategy_levels from AlsThresholds).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
// startAlsCycle creates a new strategy_cycles row and seeds one pending strategy_levels
// row per sr.strategy.AlsThresholds entry — target_price is left at 0 here and computed
// fresh on every alsPriceTick call (Task 3) from the main position's LIVE avg entry, not
// fixed at cycle-start time, since the design requires each threshold's trigger point to
// track the main position's entry as it moves (e.g. via its own DCA).
// Must be called with startMu held and WITHOUT sr.mu held — mirrors startCycle/startMatrixCycle.
func (sr *StrategyRunner) startAlsCycle(ctx context.Context) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	var cycleID string
	var cycleNum int
	err := sr.runner.pool.QueryRow(ctx,
		`INSERT INTO strategy_cycles (strategy_id, cycle_num, started_at)
		 VALUES ($1, COALESCE((SELECT MAX(cycle_num)+1 FROM strategy_cycles WHERE strategy_id=$1), 1), NOW())
		 RETURNING id, cycle_num`,
		sr.strategy.ID,
	).Scan(&cycleID, &cycleNum)
	if err != nil {
		return fmt.Errorf("startAlsCycle: insert cycle: %w", err)
	}
	sr.cycle = &Cycle{ID: cycleID, StrategyID: sr.strategy.ID, CycleNum: cycleNum}
	sr.levels = nil

	for i := range sr.strategy.AlsThresholds {
		levelIdx := i + 1
		side := "Buy"
		if sr.strategy.Direction == DirectionShort {
			side = "Sell"
		}
		var levelID string
		if err := sr.runner.pool.QueryRow(ctx,
			`INSERT INTO strategy_levels (strategy_id, cycle_id, level_idx, side, target_price, size_usdt, qty, status)
			 VALUES ($1,$2,$3,$4,0,0,'0','pending') RETURNING id`,
			sr.strategy.ID, cycleID, levelIdx, side,
		).Scan(&levelID); err != nil {
			return fmt.Errorf("startAlsCycle: insert level %d: %w", levelIdx, err)
		}
		sr.levels = append(sr.levels, GridLevel{
			ID: levelID, LevelIdx: levelIdx, Side: side, Status: LevelPending,
		})
	}

	sr.info(ctx, fmt.Sprintf("ALS: цикл %d запущен, уровней: %d", cycleNum, len(sr.strategy.AlsThresholds)))
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
