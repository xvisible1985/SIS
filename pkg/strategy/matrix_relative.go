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
//
// This is the "Novabot" emergent-renumbering rule from the original design
// (docs/superpowers/specs/2026-07-05-matrix-relative-slots-design.md): a slot's raw config
// index is never explicitly renumbered in the DB — "renumbering toward entry" is purely a
// side effect of counting how many are currently open. When an inner slot closes and a
// deeper one survives, the survivor is emergently "renumbered" to a lower rank, and the
// *next new* slot recycles the deeper config index the survivor still holds — reusing that
// tier's step/size a second time in the same cycle is intended, not a bug, as long as
// concurrently-open count never exceeds n (still enforced by the cap check below).
//
// 2026-09-23/24 history: this was briefly replaced with a "lowest free index" positional
// lookup after an incident (Gonchar 2.0 hedge, MUBARAKUSDT, pol account) where a closed
// inner slot's recycle briefly ran two concurrent same-tier positions. That replacement
// was a misdiagnosis — it matched the design's explicitly *rejected* alternative
// ("explicit renumber", see the design doc) and caused a new, worse symptom: after a slot
// closed, the engine reopened the SAME shallow tier that had just stopped out (near the
// same price) instead of progressing deeper, visible live 2026-09-24 (Gonchar 2.0 hedge,
// MUBARAKUSDT, semera account) as "L(1) reappears after L(2) already filled" — a rank
// regression the emergent count-based rule never produces (next is always
// count(open)+1, which only holds steady or advances, never goes backward). Reverted to
// the original count-based rule; the concurrency-cap check (next > n → 0) already prevents
// open count from ever exceeding n, which is the actual invariant that matters.
//
// Placed/Pending are counted alongside Filled for robustness (matching
// matrixNextRelativeSlot's own separate in-flight guard, which already refuses to call this
// at all while any Placed/Pending exists on the side) even though in practice that guard
// means this function never sees them.
func nextConfigIndex(levels []GridLevel, side string, n int) int {
	count := 0
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
			count++
		}
	}
	next := count + 1
	if next > n {
		return 0
	}
	return next
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
