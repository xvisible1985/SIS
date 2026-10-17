package strategy

import "testing"

// TestMatrixGhostSLCandidates_FlagsOnlyFilledLevelsWithDeadSLOrders is the regression for the
// live incident (2026-10-02, COTIUSDT/pol): L(1)'s per-level matrix SL genuinely fired while
// api-gateway was down for a restart, so the live WS handler (OnOrderEvent → handleMatrixSLFill)
// never ran — the level stayed LevelFilled with its now-dead SLOrderID still recorded, making
// the exchange position quietly smaller than every "filled" level's qty summed together.
// matrixGhostSLCandidates is reconcile's block 8b detector for exactly this: an SL order that
// looks live to our own bookkeeping (level still filled, SLOrderID still set) but is no longer
// in the exchange's current open-order list.
func TestMatrixGhostSLCandidates_FlagsOnlyFilledLevelsWithDeadSLOrders(t *testing.T) {
	slot0, slot1, slot2 := 0, 1, 2
	levels := []GridLevel{
		{ID: "l0", Status: LevelFilled, Slot: &slot0, SLOrderID: "still-live-order"},
		{ID: "l1", Status: LevelFilled, Slot: &slot1, SLOrderID: "dead-order"},        // the ghost
		{ID: "l2", Status: LevelFilled, Slot: &slot2, SLOrderID: ""},                  // never placed — block 8's job, not this one
		{ID: "l3", Status: LevelSLClosed, Slot: &slot1, SLOrderID: "already-closed"}, // not currently filled
		{ID: "l4", Status: LevelFilled, Slot: nil, SLOrderID: "grid-level-no-slot"},  // grid levels have no slot
	}
	live := map[string]bool{"still-live-order": true}

	ghosts := matrixGhostSLCandidates(levels, live)

	if len(ghosts) != 1 {
		t.Fatalf("ghosts = %+v, want exactly 1 (l1)", ghosts)
	}
	if ghosts[0].levelID != "l1" || ghosts[0].slOrderID != "dead-order" {
		t.Errorf("ghosts[0] = %+v, want {levelID: l1, slOrderID: dead-order}", ghosts[0])
	}
}

// TestMatrixGhostSLCandidates_EmptyWhenEverythingMatches documents the silent-when-healthy
// behavior: a level whose SL is genuinely still resting on the exchange must never be flagged.
func TestMatrixGhostSLCandidates_EmptyWhenEverythingMatches(t *testing.T) {
	slot1 := 1
	levels := []GridLevel{
		{ID: "l1", Status: LevelFilled, Slot: &slot1, SLOrderID: "order-1"},
	}
	live := map[string]bool{"order-1": true}

	ghosts := matrixGhostSLCandidates(levels, live)

	if len(ghosts) != 0 {
		t.Errorf("ghosts = %+v, want empty — the SL is genuinely still live", ghosts)
	}
}
