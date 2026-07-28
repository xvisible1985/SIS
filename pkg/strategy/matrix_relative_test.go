package strategy

import (
	"context"
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
	got := nextSlotPrice(levels, 100.0, "below", DirectionLong, -3.0)
	if math.Abs(got-92.15) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 92.15", got)
	}
}

func TestNextSlotPrice_NoOpen_FromEntry(t *testing.T) {
	got := nextSlotPrice(nil, 100.0, "below", DirectionLong, -3.0)
	if math.Abs(got-97.0) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 97.0", got)
	}
}

// Regression for the MAGMAUSDT/TACUSDT live incident (2026-07-15): short strategies with
// relative_slots=true accumulated MORE short size as price ROSE instead of fell, because
// nextSlotPrice applied the "above"-configured (positive) PriceStepPct as-is, without the
// direction-aware sign inversion calculateMatrixPrices already applied for short. Verified
// against the actual production numbers for MAGMAUSDT's HEDGE (short) leg: entry 0.26637,
// "above" config steps +2%/+2%/+3%/+3% — before the fix these produced targets ABOVE entry
// (0.27165, 0.27687, ...); the fix must produce targets BELOW entry instead.
func TestNextSlotPrice_ShortDirection_InvertsPositiveStep(t *testing.T) {
	got := nextSlotPrice(nil, 100.0, "above", DirectionShort, 2.0)
	if got >= 100.0 {
		t.Fatalf("nextSlotPrice (short, +2%% step) = %.6f, want < 100 (in-direction DCA for short is downward)", got)
	}
	if math.Abs(got-98.0) > 1e-9 {
		t.Fatalf("nextSlotPrice = %.6f, want 98.0 (100 * (1 - 2/100))", got)
	}
}

func TestNextSlotPrice_ShortDirection_ChainsFromDeepestOpen(t *testing.T) {
	// Mirrors the real MAGMAUSDT HEDGE leg sequence: entry 0.26637, steps +2%,+2%,+3%,+3%.
	// Each new slot should step DOWN from the previous fill, not up.
	entry := 0.26637
	levels := []GridLevel{
		lvl(1, 0.26632, LevelFilled), // slot 0 anchor-equivalent for this test is entry itself
	}
	got := nextSlotPrice(levels, entry, "above", DirectionShort, 2.0)
	if got >= levels[0].FilledPrice {
		t.Fatalf("nextSlotPrice = %.8f, want below previous fill %.8f", got, levels[0].FilledPrice)
	}
}

func TestNextSlotPrice_LongDirection_KeepsNegativeStepDownward(t *testing.T) {
	// Long's "below" config is already configured with a negative step — direction must
	// NOT flip it a second time (stepMul=+1 for long is a no-op), or long would regress.
	got := nextSlotPrice(nil, 100.0, "below", DirectionLong, -3.0)
	if got >= 100.0 {
		t.Fatalf("nextSlotPrice (long, -3%% step) = %.6f, want < 100", got)
	}
}

func TestMatrixStepMul(t *testing.T) {
	if matrixStepMul(DirectionShort) != -1.0 {
		t.Fatalf("matrixStepMul(short) = %v, want -1", matrixStepMul(DirectionShort))
	}
	if matrixStepMul(DirectionLong) != 1.0 {
		t.Fatalf("matrixStepMul(long) = %v, want 1", matrixStepMul(DirectionLong))
	}
}

// Regression for the second half of the MAGMAUSDT/TACUSDT incident: matrixRelativeExpand
// used to trigger short's "above"-configured slots on currentPrice >= target (price
// rising), which was correct only before the nextSlotPrice sign-inversion fix. Once
// nextSlotPrice correctly places the target BELOW entry for short, the trigger must wait
// for price to FALL to it — using the raw "above" config-list name to pick >= here would
// silently reintroduce the bug even with nextSlotPrice fixed.
func TestMatrixSlotReached_ShortAboveConfig_WaitsForPriceToFall(t *testing.T) {
	target := 98.0 // below entry, as nextSlotPrice now correctly computes for short
	if matrixSlotReached(DirectionShort, 2.0, 99.0, target) {
		t.Fatal("should not be reached yet: price (99) hasn't fallen to target (98)")
	}
	if !matrixSlotReached(DirectionShort, 2.0, 98.0, target) {
		t.Fatal("should be reached: price fell to target")
	}
	if !matrixSlotReached(DirectionShort, 2.0, 97.0, target) {
		t.Fatal("should be reached: price fell past target")
	}
}

func TestMatrixSlotReached_LongBelowConfig_WaitsForPriceToFall(t *testing.T) {
	target := 97.0
	if matrixSlotReached(DirectionLong, -3.0, 98.0, target) {
		t.Fatal("should not be reached yet: price (98) hasn't fallen to target (97)")
	}
	if !matrixSlotReached(DirectionLong, -3.0, 97.0, target) {
		t.Fatal("should be reached: price fell to target")
	}
}

// --- matrixNextRelativeSlot / matrixNextRelativeSlotPreview ---
//
// Regression coverage for the two-sided relative-slots feature (2026-07-15): the engine
// used to only ever expand the direction's accumulation side (matrixAccumSide) — the
// opposite/counter side was never touched at all. These tests pin the shared
// matrixNextRelativeSlot computation (the single source of truth used by both the live
// trigger in matrixRelativeExpand and the read-only chart preview) for both sides.

func shortStrategyWithLevels(filled ...GridLevel) *StrategyRunner {
	return &StrategyRunner{
		strategy: Strategy{
			Direction:     DirectionShort,
			RelativeSlots: true,
			MatrixLevels: []MatrixLevel{
				{Direction: "below", PriceStepPct: -2, SizePct: 10},                     // counter side for short
				{Direction: "above", PriceStepPct: 2, SizePct: 10},                      // accum side for short
				{Direction: "above", PriceStepPct: 3, SizePct: 10, OrderType: "virtual"}, // accum side, explicit virtual
			},
		},
		cycle:  &Cycle{StartPrice: 100.0},
		levels: filled,
	}
}

func TestMatrixNextRelativeSlot_AccumSide_ExchangeByDefault(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	idx, slot, target, cfg, virtual, ok := sr.matrixNextRelativeSlot("above")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if idx != 1 || slot != 1 {
		t.Fatalf("idx/slot = %d/%d, want 1/1", idx, slot)
	}
	if math.Abs(target-98.0) > 1e-9 {
		t.Fatalf("target = %.6f, want 98.0 (100 * (1 - 2/100), short accum steps DOWN)", target)
	}
	if virtual {
		t.Fatal("accum-side level with no order_type config should default to exchange (non-virtual)")
	}
	if cfg.PriceStepPct != 2 {
		t.Fatalf("cfg.PriceStepPct = %v, want 2", cfg.PriceStepPct)
	}
}

func TestMatrixNextRelativeSlot_CounterSide_AlwaysVirtual(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	idx, slot, target, _, virtual, ok := sr.matrixNextRelativeSlot("below")
	if !ok {
		t.Fatal("expected ok=true — counter side must be expandable, not silently skipped")
	}
	if idx != 1 || slot != -1 {
		t.Fatalf("idx/slot = %d/%d, want 1/-1", idx, slot)
	}
	if math.Abs(target-102.0) > 1e-9 {
		t.Fatalf("target = %.6f, want 102.0 (100 * (1 - (-1)*-2/100) = 100*1.02, counter side for short lands ABOVE entry)", target)
	}
	if !virtual {
		t.Fatal("counter/against-direction side must always be virtual — a resting exchange order can't represent it")
	}
}

func TestMatrixNextRelativeSlot_AccumSide_ExplicitVirtualConfig(t *testing.T) {
	// Second accum-side config entry (idx=2) has order_type=virtual explicitly.
	sr := shortStrategyWithLevels(
		lvl(0, 100.0, LevelFilled),
		lvl(1, 98.0, LevelFilled), // first accum slot already filled, so next is idx=2
	)
	_, slot, _, cfg, virtual, ok := sr.matrixNextRelativeSlot("above")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if slot != 2 {
		t.Fatalf("slot = %d, want 2", slot)
	}
	if cfg.OrderType != "virtual" {
		t.Fatalf("cfg.OrderType = %q, want virtual (test setup error)", cfg.OrderType)
	}
	if !virtual {
		t.Fatal("accum-side level explicitly configured order_type=virtual must resolve to virtual=true")
	}
}

func TestMatrixNextRelativeSlot_BlockedWhilePending(t *testing.T) {
	one := 1
	sr := shortStrategyWithLevels(
		lvl(0, 100.0, LevelFilled),
		GridLevel{Slot: &one, Status: LevelPending},
	)
	_, _, _, _, _, ok := sr.matrixNextRelativeSlot("above")
	if ok {
		t.Fatal("expected ok=false — a pending slot on this side must block further expansion")
	}
}

func TestMatrixNextRelativeSlot_CapReached(t *testing.T) {
	// Only 2 "above" configs exist (steps 2 and 3); both filled → cap reached.
	sr := shortStrategyWithLevels(
		lvl(0, 100.0, LevelFilled),
		lvl(1, 98.0, LevelFilled),
		lvl(2, 96.0, LevelFilled),
	)
	_, _, _, _, _, ok := sr.matrixNextRelativeSlot("above")
	if ok {
		t.Fatal("expected ok=false — concurrency cap (2 configured above-levels) reached")
	}
}

func TestMatrixNextRelativeSlotPreview_RequiresRelativeSlots(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.RelativeSlots = false
	if _, _, _, ok := sr.matrixNextRelativeSlotPreview("above"); ok {
		t.Fatal("expected ok=false for a non-relative-slots strategy")
	}
}

func TestMatrixNextRelativeSlotPreview_MatchesLiveComputation(t *testing.T) {
	// The preview must never diverge from what matrixRelativeExpand would actually place —
	// it calls the exact same matrixNextRelativeSlot. Pin that equivalence explicitly so a
	// future edit that special-cases one path but not the other gets caught immediately.
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	_, wantSlot, wantTarget, _, wantVirtual, _ := sr.matrixNextRelativeSlot("above")
	gotSlot, gotPrice, gotVirtual, ok := sr.matrixNextRelativeSlotPreview("above")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if gotSlot != wantSlot || gotPrice != wantTarget || gotVirtual != wantVirtual {
		t.Fatalf("preview (%d, %.4f, %v) != live computation (%d, %.4f, %v)",
			gotSlot, gotPrice, gotVirtual, wantSlot, wantTarget, wantVirtual)
	}
}

// Regression for the MAGMAUSDT live incident (2026-07-15): matrixPriceTick used to return
// immediately after expanding relative slots, before ever reaching matrixApplyStopCondSLs
// (step 3) or the missing-SL retry (step 4). That meant a level's stop_cond_pct→
// stop_replace_pct SL upgrade was only ever evaluated once, at cycle load
// (loadMatrixCycle) — any level that filled while the process kept running (the normal
// case for relative-slots, since expansion happens live) could go permanently
// unprotected. Confirmed against production data: MAGMAUSDT's MAIN leg had SLs on L0/L1
// (placed at a prior load) but none on the newly-filled L2; the HEDGE leg's L0 had no SL
// at all, both filled after the last process restart.
func TestMatrixPriceTick_RelativeSlots_StillAppliesStopCondSLs(t *testing.T) {
	slot := 0
	condPct := 1.5
	replacePct := 0.5
	sr := &StrategyRunner{
		strategy: Strategy{
			Direction:        DirectionLong,
			RelativeSlots:    true,
			MatrixEntryLevel: &MatrixEntryLevel{StopCondPct: &condPct, StopReplacePct: &replacePct},
		},
		cycle: &Cycle{StartPrice: 100.0},
		levels: []GridLevel{
			{ID: "level-0", Slot: &slot, Status: LevelFilled, FilledPrice: 100.0, SLReplaced: false},
		},
	}
	// price (102) is past the 1.5% stop-cond threshold (101.5) → matrixApplyStopCondSLs
	// must attempt to place a replacement SL, which requires sr.runner (nil here) and
	// therefore panics. Before the fix, the early return after matrixRelativeExpand meant
	// this call never reached that code at all, so nothing panicked.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching matrixApplyStopCondSLs — matrixPriceTick still returns early for RelativeSlots")
		}
	}()
	sr.matrixPriceTick(context.Background(), 102.0)
}

// Regression for a live incident (2026-07-17): a relative-slots strategy's counter side
// (always virtual — see TestMatrixNextRelativeSlot_CounterSide_AlwaysVirtual) stopped
// expanding entirely after a single PlaceOrder attempt for a newly-inserted slot didn't
// complete (transient exchange error, or a runner restart between insert and placement).
// matrixNextRelativeSlot's "blocked while a pending/placed slot exists" guard meant the
// stuck slot was never retried — price ran far past the target with nothing happening.
// matrixRetryStuckRelativeSlot must find and retry it instead of matrixRelativeExpand
// silently doing nothing because matrixNextRelativeSlot refuses to offer a new slot.

func TestMatrixRetryStuckRelativeSlot_RetriesPendingVirtualSlot(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "stuck-1", Slot: &negOne, Status: LevelPending, TargetPrice: 102.0, Qty: "1.0",
	})
	// Reaching the retry's PlaceOrder call panics on nil sr.runner — proving it was
	// actually retried, not silently skipped.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching PlaceOrder — stuck slot was not retried")
		}
	}()
	sr.matrixRetryStuckRelativeSlot(context.Background(), "below", 103.0)
}

func TestMatrixRetryStuckRelativeSlot_NoStuckSlot_ReturnsFalse(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	if sr.matrixRetryStuckRelativeSlot(context.Background(), "below", 103.0) {
		t.Fatal("expected false — nothing stuck to retry")
	}
}

func TestMatrixRetryStuckRelativeSlot_SkipsAlreadyPlaced(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "placed-1", Slot: &negOne, Status: LevelPlaced, ExchangeOrderID: "ord-123", TargetPrice: 102.0, Qty: "1.0",
	})
	// Must return false without touching sr.runner (nil here, would panic if reached) —
	// a slot with an exchange order id already succeeded, it isn't stuck.
	if sr.matrixRetryStuckRelativeSlot(context.Background(), "below", 103.0) {
		t.Fatal("expected false — slot already has an exchange order, not stuck")
	}
}

func TestMatrixRelativeExpand_RetriesBeforeCreatingNewSlot(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "stuck-1", Slot: &negOne, Status: LevelPending, TargetPrice: 102.0, Qty: "1.0",
	})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching PlaceOrder via the retry path inside matrixRelativeExpand")
		}
	}()
	sr.matrixRelativeExpand(context.Background(), "below", 103.0)
}
