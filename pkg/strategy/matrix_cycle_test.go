package strategy

import (
	"context"
	"runtime/debug"
	"strings"
	"testing"

	"sis/pkg/trader"
)

// TestHandleTPFill_MatrixFallsThroughToSharedClose: handleTPFill must no longer delegate
// matrix strategies to a separate handleMatrixTPFill that re-arms in place — it must reach
// the same closeCycle path hedge/grid uses. Proven by reaching a nil sr.runner panic inside
// closeCycle's DB write (`UPDATE strategy_cycles SET ended_at=NOW()...`), which only
// happens if handleTPFill fell through past any matrix-specific branch instead of
// returning early from a re-arm-in-place implementation that never calls closeCycle.
// Regression for the matrix-cycle-lifecycle redesign (2026-07-21): before this change,
// a matrix TP fill never set ended_at at all, so this panic would never be reached.
func TestHandleTPFill_MatrixFallsThroughToSharedClose(t *testing.T) {
	slot := 0
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:            "11111111-2222-3333-4444-555555555555",
			StrategyType:  "matrix",
			Direction:     DirectionLong,
			Symbol:        "TESTUSDT",
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot, Status: LevelFilled, FilledPrice: 100.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching closeCycle's nil-runner DB write — handleTPFill did not fall through to the shared close path for a matrix strategy")
		}
	}()
	sr.handleTPFill(context.Background(), "tp-order-1", 105.0, 1.0)
}

// TestHandlePartialPositionChange_MatrixGhostFallsThroughToSharedClose: the matrix-specific
// "tail close without ending the cycle" branch in handlePartialPositionChange assumed the
// OLD in-place TP re-arm model (handleMatrixTPFill resets levels to pending, leaving
// ourQty==0 while the cycle stays open) — now removed (Task 4), so this scenario must fall
// through to the same generic closeGhostPosition path non-matrix strategies already use,
// not a matrix-only branch tagged to never end the cycle. Regression for the
// matrix-cycle-lifecycle redesign (2026-07-21): after handleMatrixTPFill's deletion, a
// matrix TP fill always ends the cycle via closeCycle, so any subsequent WS position event
// reporting a leftover tail while ourQty==0 is a genuine ghost/orphan position — closing it
// should also end whatever cycle it's attached to, exactly like it already does for
// hedge/grid.
//
// Proof technique: rather than matching a function name in the panic stack (both the old
// matrix-only branch and the shared path call sr.warn — which itself panics on the nil
// test runner before reaching PlaceOrder — so a name-based check can't distinguish them),
// this compares the exact panic-site LINE NUMBER in cycle.go between a matrix and a
// non-matrix strategy hitting the identical scenario. Before this fix they diverged (the
// matrix branch had its own separate sr.warn call at a different line); after it, both
// strategy types must panic at the exact same line — proof the branch is gone and both
// types now run the same code.
func TestHandlePartialPositionChange_MatrixGhostFallsThroughToSharedClose(t *testing.T) {
	panicLine := func(strategyType string) string {
		sr := &StrategyRunner{
			strategy: Strategy{
				ID:           "11111111-2222-3333-4444-555555555555",
				StrategyType: strategyType,
				Direction:    DirectionLong,
				Symbol:       "TESTUSDT",
			},
			cycle:        &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
			levels:       nil, // no filled levels -> avgEntry() returns ourQty=0
			closedBySelf: true,
		}
		var site string
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for strategyType=%s", strategyType)
				}
				stack := string(debug.Stack())
				for _, line := range strings.Split(stack, "\n") {
					if strings.Contains(line, "cycle.go:") {
						site = strings.TrimSpace(line)
						break
					}
				}
			}()
			sr.handlePartialPositionChange(context.Background(), 5.0)
		}()
		return site
	}

	matrixSite := panicLine("matrix")
	gridSite := panicLine("grid")
	if matrixSite == "" || gridSite == "" {
		t.Fatalf("failed to capture panic site: matrix=%q grid=%q", matrixSite, gridSite)
	}
	if matrixSite != gridSite {
		t.Fatalf("matrix and grid strategies panic at different cycle.go lines — the matrix-specific branch is still present:\nmatrix: %s\ngrid:   %s", matrixSite, gridSite)
	}
}

// TestMatrixHandleSLFlattenOrContinue_LastLevelClosesCycle: when no level remains filled
// (matrixActiveQty() == 0), the position has fully flattened — the cycle must close via
// the shared closeCycle/maybeRestart path, proven by reaching a nil-runner panic inside
// closeCycle, same technique as Task 4's tests.
func TestMatrixHandleSLFlattenOrContinue_LastLevelClosesCycle(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			StrategyType: "matrix",
			Direction:    DirectionLong,
			Symbol:       "TESTUSDT",
		},
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: nil, // no filled levels -> matrixActiveQty() == 0
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching closeCycle — zero active qty must flatten the position and close the cycle")
		}
	}()
	sr.matrixHandleSLFlattenOrContinue(context.Background())
}

// TestMatrixHandleSLFlattenOrContinue_OtherLevelsStillFilled_CycleStaysOpen: when a level
// remains filled (matrixActiveQty() > 0), the position has NOT flattened — the cycle must
// stay open, reaching matrixUpdateTP's TP recompute (via resolveExchangeAvgEntry, which
// touches sr.runner.tradeStream before any strategy-type branch) rather than closeCycle.
// Different panic site than the LastLevelClosesCycle test proves the two branches are
// distinct.
func TestMatrixHandleSLFlattenOrContinue_OtherLevelsStillFilled_CycleStaysOpen(t *testing.T) {
	slot1 := 1
	tpPct := 1.0
	sr := &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			StrategyType: "matrix",
			Direction:    DirectionLong,
			Symbol:       "TESTUSDT",
			// matrixUpdateTP looks up TP config for the governing slot via
			// matrixLevelConfig(1) -> MatrixLevels[direction="above"][0]; without a
			// matching entry (or the TPPct pointer left nil) it bails out at its
			// "TP percentage removed from config" guard before ever reaching
			// resolveExchangeAvgEntry. Providing this keeps the test reaching the
			// intended panic site regardless of exactly where in matrixUpdateTP's body
			// the avgEntry/resolveExchangeAvgEntry call happens to sit.
			MatrixLevels: []MatrixLevel{{Direction: "above", PriceStepPct: 1.0, TPPct: &tpPct}},
		},
		cycle: &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 98.0, Qty: "1.0", SizeUSDT: 98.0},
		},
		// matrixUpdateTP bails out immediately if instr.QtyStep == 0 (its zero value),
		// before ever reaching resolveExchangeAvgEntry — set a nonzero QtyStep so
		// execution actually gets far enough to hit the intended nil-runner panic site.
		instr: trader.InstrumentInfo{QtyStep: 0.001},
	}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching matrixUpdateTP (via resolveExchangeAvgEntry) — cycle must stay open and recompute TP while a level is still filled")
		}
	}()
	sr.matrixHandleSLFlattenOrContinue(context.Background())
}
