package strategy

import "testing"

// TestAlsGhostSLCandidates_FlagsOnlyFilledLevelsWithDeadSLOrders mirrors
// matrixGhostSLCandidates's own regression test — same ghost-fill detection, but ALS
// levels never set Slot (that's a matrix-only concept), so this must not require it.
func TestAlsGhostSLCandidates_FlagsOnlyFilledLevelsWithDeadSLOrders(t *testing.T) {
	levels := []GridLevel{
		{ID: "l0", Status: LevelFilled, SLOrderID: "still-live-order"},
		{ID: "l1", Status: LevelFilled, SLOrderID: "dead-order"},        // the ghost
		{ID: "l2", Status: LevelFilled, SLOrderID: ""},                  // never placed — a different health-check's job
		{ID: "l3", Status: LevelSLClosed, SLOrderID: "already-closed"}, // not currently filled
	}
	live := map[string]bool{"still-live-order": true}

	ghosts := alsGhostSLCandidates(levels, live)

	if len(ghosts) != 1 {
		t.Fatalf("ghosts = %+v, want exactly 1 (l1)", ghosts)
	}
	if ghosts[0].levelID != "l1" || ghosts[0].slOrderID != "dead-order" {
		t.Errorf("ghosts[0] = %+v, want {levelID: l1, slOrderID: dead-order}", ghosts[0])
	}
}

// TestAlsGhostSLCandidates_EmptyWhenEverythingMatches documents the silent-when-healthy
// behavior: a level whose SL is genuinely still resting on the exchange must never be flagged.
func TestAlsGhostSLCandidates_EmptyWhenEverythingMatches(t *testing.T) {
	levels := []GridLevel{
		{ID: "l1", Status: LevelFilled, SLOrderID: "order-1"},
	}
	live := map[string]bool{"order-1": true}

	ghosts := alsGhostSLCandidates(levels, live)

	if len(ghosts) != 0 {
		t.Errorf("ghosts = %+v, want empty — the SL is genuinely still live", ghosts)
	}
}
