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

// nextConfigIndex returns the lowest 1-based config-array index (1..n) on this side that
// isn't currently occupied by a live level (Filled, Placed, or Pending), or 0 if all n are
// occupied.
//
// This must be a positional lookup, not a count — "how many are open" and "which specific
// index is free" only coincide when open slots always happen to be exactly {1..count}, and
// that isn't guaranteed: each slot's SL fires independently (its own stop-condition/price),
// so a LOWER-numbered slot can close while a HIGHER-numbered one is still open. Found live
// 2026-09-23 (Gonchar 2.0 hedge, MUBARAKUSDT, pol account): slot 1 closed before slot 2
// did, dropping the open count to 1; the old count+1 formula then returned 2 — the exact
// index the still-open slot 2 already occupied — briefly running two concurrent slot-2
// positions and adding size beyond what the configured n tiers intend, instead of
// reopening the genuinely free slot 1.
func nextConfigIndex(levels []GridLevel, side string, n int) int {
	occupied := make(map[int]bool, n)
	for i := range levels {
		l := &levels[i]
		if l.Slot == nil || *l.Slot == 0 {
			continue
		}
		if side == "below" && *l.Slot > 0 {
			continue
		}
		if side == "above" && *l.Slot < 0 {
			continue
		}
		switch l.Status {
		case LevelFilled, LevelPlaced, LevelPending:
		default:
			continue
		}
		idx := *l.Slot
		if idx < 0 {
			idx = -idx
		}
		occupied[idx] = true
	}
	for idx := 1; idx <= n; idx++ {
		if !occupied[idx] {
			return idx
		}
	}
	return 0
}

// nextSlotPrice = deepest open slot's fill price stepped by stepPct%, or entry stepped
// by stepPct if no slots open. stepPct is the raw configured step (e.g. -3.0 for a
// "below"-configured 3% step); matrixStepMul(dir) inverts it for short so the resulting
// price lands on the in-direction (downward) side of base, mirroring
// calculateMatrixPrices. Without this inversion, short strategies configured with
// "above" (positive-stepPct) accumulation levels would step the target UP instead of
// down — accumulating more short size as price rises against the position.
func nextSlotPrice(levels []GridLevel, entry float64, side string, dir Direction, stepPct float64) float64 {
	open := openAccumLevels(levels, entry, side)
	base := entry
	if len(open) > 0 {
		base = open[len(open)-1].FilledPrice
	}
	return base * (1 + matrixStepMul(dir)*stepPct/100)
}

// matrixRelativeSafeZoneBlocks reports whether `slot` is still cooling down after its most
// recent SL close, mirroring the absolute-mode safe zone in matrixCheckWaitingReentry
// (require price to recover SafeZonePct% past the SL trigger before allowing a new entry).
// matrixAfterSLClose's relative-slots branch skips that absolute-mode gate entirely — a
// closed relative slot's config index simply becomes "free" again the moment
// nextConfigIndex recounts open levels, with nothing stopping it from immediately
// reopening at (near enough) the same price it was just stopped out at. This is the
// relative-slots equivalent, called from matrixRelativeExpand before a freshly-closed
// slot's index is allowed to reopen. Found live 2026-09-21/22 (Gonchar 2.0 hedge,
// poligonorigin33 account, COTIUSDT): with no gate at all, L(4) reopened and stopped out 8
// times in under 5 hours as price ground steadily in one direction.
//
// sr.levels is append-only in chronological order (a re-triggered slot gets a brand new
// GridLevel/DB row, never reuses the old one — see matrixTriggerRelativeVirtualLevel /
// matrixPlaceRelativeSlot), so the last matching sl_closed entry in iteration order is
// always the most recent one; no separate timestamp is needed.
// Must be called with sr.mu held.
func (sr *StrategyRunner) matrixRelativeSafeZoneBlocks(slot int, currentPrice float64) bool {
	if sr.strategy.SafeZonePct <= 0 {
		return false
	}
	var lastSLTrigger float64
	for i := range sr.levels {
		l := &sr.levels[i]
		if l.Slot == nil || *l.Slot != slot || l.Status != LevelSLClosed {
			continue
		}
		trigger := l.SLPrice
		if trigger == 0 {
			trigger = l.FilledPrice
		}
		lastSLTrigger = trigger
	}
	if lastSLTrigger <= 0 {
		return false
	}
	if sr.strategy.Direction == DirectionLong {
		return currentPrice < lastSLTrigger*(1+sr.strategy.SafeZonePct/100)
	}
	return currentPrice > lastSLTrigger*(1-sr.strategy.SafeZonePct/100)
}

// matrixSlotReached reports whether currentPrice has moved far enough to trigger the
// next relative slot at target. The trigger direction follows the SAME sign inversion
// nextSlotPrice applied (matrixStepMul(dir)*stepPct), not the raw config-list name
// ("above"/"below") — for short, an "above"-configured (positive stepPct) level still
// steps DOWN once inverted, so the trigger must wait for price to fall, not rise.
func matrixSlotReached(dir Direction, stepPct, currentPrice, target float64) bool {
	effectiveStep := matrixStepMul(dir) * stepPct
	if effectiveStep <= 0 {
		return currentPrice <= target
	}
	return currentPrice >= target
}
