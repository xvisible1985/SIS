package strategy

import (
	"math"
	"testing"
)

func lvl(slot int, price float64, status LevelStatus) GridLevel {
	s := slot
	return GridLevel{Slot: &s, TargetPrice: price, FilledPrice: price, Status: status}
}

func TestRelativeRanks_BelowSide(t *testing.T) {
	entry := 100.0
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled),
		lvl(-2, 95, LevelFilled),
		lvl(-3, 90, LevelPending),
	}
	got := relativeRanks(levels, entry, "below")
	if got[0] != 1 || got[1] != 2 {
		t.Fatalf("ranks = %v, want [1 2 ...]", got)
	}
	if got[2] != 0 {
		t.Fatalf("pending level should have rank 0, got %d", got[2])
	}
}

func TestNextConfigIndex_RecycleAfterClose(t *testing.T) {
	levels := []GridLevel{
		lvl(-1, 98, LevelSLClosed),
		lvl(-2, 95, LevelFilled),
	}
	if idx := nextConfigIndex(levels, "below", 5); idx != 2 {
		t.Fatalf("nextConfigIndex = %d, want 2", idx)
	}
}

func TestNextConfigIndex_CapAtN(t *testing.T) {
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled), lvl(-2, 95, LevelFilled), lvl(-3, 90, LevelFilled),
	}
	if idx := nextConfigIndex(levels, "below", 3); idx != 0 {
		t.Fatalf("nextConfigIndex at cap = %d, want 0", idx)
	}
}

func TestNextSlotPrice_FromDeepestOpen(t *testing.T) {
	levels := []GridLevel{
		lvl(-1, 98, LevelFilled),
		lvl(-2, 95, LevelFilled),
	}
	got := nextSlotPrice(levels, 100.0, "below", -3.0)
	if math.Abs(got-92.15) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 92.15", got)
	}
}

func TestNextSlotPrice_NoOpen_FromEntry(t *testing.T) {
	got := nextSlotPrice(nil, 100.0, "below", -3.0)
	if math.Abs(got-97.0) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 97.0", got)
	}
}
