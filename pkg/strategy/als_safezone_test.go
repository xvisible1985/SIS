package strategy

import "testing"

// alsSafeZoneBlocked mirrors matrixCheckWaitingReentry's rule: after a per-level stop at
// anchor, a short hedge waits for price to fall safeZonePct% below it, a long hedge for
// price to rise safeZonePct% above it.
func TestAlsSafeZoneBlocked(t *testing.T) {
	cases := []struct {
		name    string
		dir     Direction
		pct     float64
		anchor  float64
		price   float64
		blocked bool
	}{
		{"short, price still above safe price", DirectionShort, 2, 100, 99, true},
		{"short, price exactly at safe price", DirectionShort, 2, 100, 98, false},
		{"short, price well below safe price", DirectionShort, 2, 100, 90, false},
		{"long, price still below safe price", DirectionLong, 2, 100, 101, true},
		{"long, price at safe price", DirectionLong, 2, 100, 102, false},
		{"no safe zone configured", DirectionShort, 0, 100, 150, false},
		{"no stop pending", DirectionShort, 2, 0, 150, false},
	}
	for _, c := range cases {
		if got := alsSafeZoneBlocked(c.dir, c.pct, c.anchor, c.price); got != c.blocked {
			t.Errorf("%s: blocked = %v, want %v", c.name, got, c.blocked)
		}
	}
}

// A level_idx can appear on several rows once stopped levels are re-armed — the next
// threshold index must come from the highest level_idx, not from len(levels).
func TestAlsMaxLevelIdx(t *testing.T) {
	levels := []GridLevel{{LevelIdx: 1}, {LevelIdx: 1}, {LevelIdx: 2}, {LevelIdx: 2}, {LevelIdx: 2}}
	if got := alsMaxLevelIdx(levels); got != 2 {
		t.Errorf("alsMaxLevelIdx = %d, want 2", got)
	}
	if got := alsMaxLevelIdx(nil); got != 0 {
		t.Errorf("alsMaxLevelIdx(nil) = %d, want 0", got)
	}
}
