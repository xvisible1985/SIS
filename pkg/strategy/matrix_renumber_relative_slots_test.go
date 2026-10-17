package strategy

import (
	"context"
	"testing"
)

// TestMatrixRenumberRelativeSlots_PromotesRemainingLevelAfterClose is the regression for the
// Novabot-parity gap found live (2026-10-08, MAGMAUSDT/pol, GONCHAR 2.0): when L1 closed by
// SL while L2 stayed open, SIS left L2 permanently labeled "L2" (and still governed by
// line2's stop/TP config) — unlike novabot-backend's SafeModeLine, which re-ranks every
// currently-open order on a side by distance from entry and rewrites its line number on every
// check, so L2 would become the new L1 the moment L1 closed.
//
// Uses newTestAccountRunner's real-but-unreachable pool so fire-and-forget persistence
// (sr.runner.pool.Exec, sr.info) fails fast without panicking — letting the renumbering loop
// run to completion across every affected level, the way it does in production (a DB write
// error is swallowed, not a panic that would abort the loop after the first level).
func TestMatrixRenumberRelativeSlots_PromotesRemainingLevelAfterClose(t *testing.T) {
	ar := newTestAccountRunner(t, &fakeExchange{})
	slot1, slot2 := 1, 2
	closedLevel := GridLevel{ID: "level-1", Slot: &slot1, Status: LevelSLClosed, FilledPrice: 102.0}
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT",
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			closedLevel,
			{ID: "level-2", Slot: &slot2, Status: LevelFilled, FilledPrice: 104.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}

	sr.matrixRenumberRelativeSlots(context.Background(), &closedLevel)

	if sr.levels[1].Slot == nil || *sr.levels[1].Slot != 1 {
		t.Fatalf("level-2's Slot = %v, want 1 — it was the only remaining open level on the 'above' side and must be promoted to L1", sr.levels[1].Slot)
	}
}

// TestMatrixRenumberRelativeSlots_RanksByDistanceFromEntryNotFillOrder: when multiple levels
// remain open on a side after a close, the new numbering must follow distance-from-entry
// (closest=1), matching novabot-backend's ordersPlus/ordersMinus sort — not fill order or
// level_idx. Seeds levels filled out of distance order to prove the sort, not insertion
// order, drives the result.
func TestMatrixRenumberRelativeSlots_RanksByDistanceFromEntryNotFillOrder(t *testing.T) {
	ar := newTestAccountRunner(t, &fakeExchange{})
	closingSlot := 1
	farSlot, nearSlot := 3, 2
	closedLevel := GridLevel{ID: "level-closing", Slot: &closingSlot, Status: LevelSLClosed, FilledPrice: 101.0}
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT",
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			closedLevel,
			// Seeded in reverse-distance order (far one first in the slice) to prove the
			// renumbering sorts by actual price distance, not slice/level_idx order.
			{ID: "level-far", Slot: &farSlot, Status: LevelFilled, FilledPrice: 110.0, Qty: "1.0", SizeUSDT: 100.0},
			{ID: "level-near", Slot: &nearSlot, Status: LevelFilled, FilledPrice: 103.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}

	sr.matrixRenumberRelativeSlots(context.Background(), &closedLevel)

	if sr.levels[2].Slot == nil || *sr.levels[2].Slot != 1 {
		t.Errorf("level-near (dist=3 from entry) Slot = %v, want 1 — closest remaining level must become L1", sr.levels[2].Slot)
	}
	if sr.levels[1].Slot == nil || *sr.levels[1].Slot != 2 {
		t.Errorf("level-far (dist=10 from entry) Slot = %v, want 2 — the farther remaining level must become L2", sr.levels[1].Slot)
	}
}

// TestMatrixRenumberRelativeSlots_L0CloseIsNoOp: L0 (slot 0, the entry level) isn't ranked
// within either side's config array — closing it must not attempt to renumber anything.
func TestMatrixRenumberRelativeSlots_L0CloseIsNoOp(t *testing.T) {
	ar := newTestAccountRunner(t, &fakeExchange{})
	zero := 0
	slot1 := 1
	closedLevel := GridLevel{ID: "level-0", Slot: &zero, Status: LevelSLClosed, FilledPrice: 100.0}
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT"},
		runner:   ar,
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			closedLevel,
			{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 102.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}

	sr.matrixRenumberRelativeSlots(context.Background(), &closedLevel)

	if *sr.levels[1].Slot != 1 {
		t.Errorf("level-1's Slot = %d, want unchanged 1 — L0 closing must not trigger renumbering", *sr.levels[1].Slot)
	}
}

// TestMatrixRenumberRelativeSlots_AlreadyCorrectlyNumbered_DoesNotTouchDB: when the remaining
// level(s) already have contiguous numbers after a close (e.g. only one was ever open on
// that side, now closing with nothing left), there's nothing to rewrite. Proven by using a
// nil sr.runner: if the function tried to persist anything, sr.runner.pool.Exec would panic.
func TestMatrixRenumberRelativeSlots_AlreadyCorrectlyNumbered_DoesNotTouchDB(t *testing.T) {
	slot1 := 1
	closedLevel := GridLevel{ID: "level-1", Slot: &slot1, Status: LevelSLClosed, FilledPrice: 102.0}
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels:   []GridLevel{closedLevel}, // nothing else open on this side; sr.runner left nil
	}

	// Must NOT panic — no open levels left on the "above" side to renumber, so the (nil)
	// sr.runner is never touched.
	sr.matrixRenumberRelativeSlots(context.Background(), &closedLevel)
}

// TestMatrixCatchUpRelativeSlotNumbering_FixesStaleNumberingFromBeforeTheFeatureExisted is
// the regression for the live incident found right after deploying the reactive renumbering
// (2026-10-11, FUSDT/pol, GONCHAR 2.0): L1 had closed by SL *before* this feature shipped, so
// L2 (the only level left open on that side) never got promoted to L1 — the reactive half
// only fires at the moment a level closes, so a strategy whose numbering was already stale
// stays stale forever without an independent periodic recheck. This is that recheck: it must
// fix numbering regardless of which (if any) level closed most recently, by re-deriving both
// sides from scratch.
func TestMatrixCatchUpRelativeSlotNumbering_FixesStaleNumberingFromBeforeTheFeatureExisted(t *testing.T) {
	ar := newTestAccountRunner(t, &fakeExchange{})
	slot0, staleSlot2 := 0, 2
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT",
			RelativeSlots: true,
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot0, Status: LevelFilled, FilledPrice: 100.0, Qty: "1.0", SizeUSDT: 100.0},
			// L1 already closed (stale data, predates the reactive feature) — L2 was never
			// promoted to L1 when that happened.
			{ID: "level-1", Slot: intPtr(1), Status: LevelSLClosed, FilledPrice: 102.0},
			{ID: "level-2", Slot: &staleSlot2, Status: LevelFilled, FilledPrice: 104.0, Qty: "1.0", SizeUSDT: 100.0},
		},
	}

	sr.matrixCatchUpRelativeSlotNumbering(context.Background())

	if *sr.levels[2].Slot != 1 {
		t.Errorf("level-2's Slot = %d, want 1 — stale numbering from before this feature existed must be caught up", *sr.levels[2].Slot)
	}
}

// TestMatrixCatchUpRelativeSlotNumbering_NotRelativeSlots_NoOp: a plain (non-relative-slots)
// matrix/grid strategy must never be touched by the catch-up pass — proven via a nil
// sr.runner, which would panic if any DB write were attempted.
func TestMatrixCatchUpRelativeSlotNumbering_NotRelativeSlots_NoOp(t *testing.T) {
	slot1 := 1
	sr := &StrategyRunner{
		strategy: Strategy{ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT"},
		cycle:    &Cycle{ID: "cycle-1", CycleNum: 1, StartPrice: 100.0},
		levels:   []GridLevel{{ID: "level-1", Slot: &slot1, Status: LevelFilled, FilledPrice: 102.0}},
		// sr.runner left nil — RelativeSlots is false, so this must return before ever
		// reaching a DB call.
	}
	sr.matrixCatchUpRelativeSlotNumbering(context.Background())
}

// TestMatrixCatchUpRelativeSlotNumbering_NoCycle_NoOp: a strategy with no active cycle has
// nothing to renumber — proven via a nil sr.runner (would panic if touched).
func TestMatrixCatchUpRelativeSlotNumbering_NoCycle_NoOp(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{
			ID: "11111111-2222-3333-4444-555555555555", Direction: DirectionShort, Symbol: "TESTUSDT",
			RelativeSlots: true,
		},
		cycle: nil,
	}
	sr.matrixCatchUpRelativeSlotNumbering(context.Background())
}
