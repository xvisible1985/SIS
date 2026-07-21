package strategy

import (
	"context"
	"testing"
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
