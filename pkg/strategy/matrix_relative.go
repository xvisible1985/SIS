package strategy

import "sort"

// openAccumLevels returns filled accumulation levels for the given side, sorted by
// distance from entry (closest first). L(0) (Slot==0) is the anchor, excluded.
func openAccumLevels(levels []GridLevel, entry float64, side string) []*GridLevel {
	var out []*GridLevel
	for i := range levels {
		l := &levels[i]
		if l.Slot == nil || *l.Slot == 0 || l.Status != LevelFilled {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return absf(out[i].FilledPrice-entry) < absf(out[j].FilledPrice-entry)
	})
	return out
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// relativeRanks returns the 1-based rank (closest to entry = 1) for each filled
// accumulation level of the side, in input order; 0 for non-open / wrong-side levels.
func relativeRanks(levels []GridLevel, entry float64, side string) []int {
	type entry_ struct {
		idx  int
		dist float64
	}
	var open []entry_
	for i := range levels {
		l := &levels[i]
		if l.Slot == nil || *l.Slot == 0 || l.Status != LevelFilled {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		open = append(open, entry_{idx: i, dist: absf(l.FilledPrice - entry)})
	}
	sort.SliceStable(open, func(i, j int) bool {
		return open[i].dist < open[j].dist
	})
	out := make([]int, len(levels))
	for rank, e := range open {
		out[e.idx] = rank + 1
	}
	return out
}

// nextConfigIndex = (open slots on side) + 1, or 0 if the concurrency cap n is reached.
func nextConfigIndex(levels []GridLevel, side string, n int) int {
	count := len(openAccumLevels(levels, 0, side))
	next := count + 1
	if next > n {
		return 0
	}
	return next
}

// nextSlotPrice = deepest open slot's fill price stepped by stepPct%, or entry stepped
// by stepPct if no slots open. stepPct is signed (e.g. -3.0 for 3% below).
func nextSlotPrice(levels []GridLevel, entry float64, side string, stepPct float64) float64 {
	open := openAccumLevels(levels, entry, side)
	base := entry
	if len(open) > 0 {
		base = open[len(open)-1].FilledPrice
	}
	return base * (1 + stepPct/100)
}
