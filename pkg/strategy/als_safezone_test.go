package strategy

import "testing"

// alsSafeZoneBlocked keeps the hedge out only while price is inside the Safe Zone band
// around the stop trigger; once price is outside the band on either side it no longer blocks.
func TestAlsSafeZoneBlocked(t *testing.T) {
	cases := []struct {
		name    string
		dir     Direction
		pct     float64
		anchor  float64
		price   float64
		blocked bool
	}{
		{"short, inside band", DirectionShort, 2, 100, 99, true},
		{"short, at band edges", DirectionShort, 2, 100, 98, true},
		{"short, below band", DirectionShort, 2, 100, 97, false},
		{"short, above the stop trigger (live DNAUSDT case)", DirectionShort, 1.5, 0.18137, 0.188, false},
		{"long, inside band", DirectionLong, 2, 100, 101, true},
		{"long, above band", DirectionLong, 2, 100, 103, false},
		{"long, below the stop trigger", DirectionLong, 2, 100, 99, false},
		{"no safe zone configured", DirectionShort, 0, 100, 99, false},
		{"no stop pending", DirectionShort, 2, 0, 99, false},
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

func TestAlsSafeZoneBounds(t *testing.T) {
	if lo, hi, ok := alsSafeZoneBounds(DirectionShort, 2, 100); !ok || lo != 98 || hi != 100 {
		t.Errorf("short: (%v,%v,%v), want (98,100,true)", lo, hi, ok)
	}
	if lo, hi, ok := alsSafeZoneBounds(DirectionLong, 2, 100); !ok || lo != 100 || hi != 102 {
		t.Errorf("long: (%v,%v,%v), want (100,102,true)", lo, hi, ok)
	}
	if _, _, ok := alsSafeZoneBounds(DirectionShort, 0, 100); ok {
		t.Error("no SafeZonePct configured: want no zone")
	}
	if _, _, ok := alsSafeZoneBounds(DirectionShort, 2, 0); ok {
		t.Error("no stop pending: want no zone")
	}
}
