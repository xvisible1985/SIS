package strategy

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"

	"sis/pkg/trader"
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

// TestNextConfigIndex_RecycleAfterClose_AdvancesByCount is the count-based (Novabot
// "emergent renumbering") behavior from the original design: slot -1 is closed, slot -2
// survives as the sole open slot (count=1), so the next new slot recycles config index 2 —
// the deeper tier the survivor still holds — not the freed index 1. See nextConfigIndex's
// doc comment for why a "lowest free index" reading (which would return 1 here) is wrong.
func TestNextConfigIndex_RecycleAfterClose_AdvancesByCount(t *testing.T) {
	levels := []GridLevel{
		lvl(-1, 98, LevelSLClosed),
		lvl(-2, 95, LevelFilled),
	}
	if idx := nextConfigIndex(levels, "below", 5); idx != 2 {
		t.Fatalf("nextConfigIndex = %d, want 2 (count-based: 1 open -> recycle tier 2)", idx)
	}
}

// TestNextConfigIndex_LowerSlotClosedWhileHigherStillOpen_RecyclesDeeperTier is the
// corrected version of this test (previously named "...NeverDuplicatesTheOpenSlot" and
// asserting idx=1 — see nextConfigIndex's doc comment for why that reading was a
// misdiagnosis of the 2026-09-23 incident, and caused a worse regression live on
// 2026-09-24 semera/MUBARAKUSDT: a closed slot's rank going *backward* to a shallower tier
// instead of progressing). Slot 1 closed before slot 2 did; count-based correctly returns
// 2 — recycling the deeper tier the still-open slot 2 already holds, exactly as the
// original design's worked example describes.
func TestNextConfigIndex_LowerSlotClosedWhileHigherStillOpen_RecyclesDeeperTier(t *testing.T) {
	levels := []GridLevel{
		lvl(1, 0.0569, LevelSLClosed), // slot 1: closed
		lvl(2, 0.0532, LevelFilled),   // slot 2: still open
	}
	got := nextConfigIndex(levels, "above", 4)
	if got != 2 {
		t.Errorf("nextConfigIndex = %d, want 2 (count-based: 1 open -> recycle tier 2)", got)
	}
}

// TestNextConfigIndex_PlacedAndPendingAlsoCountAsOccupied pins that the occupancy check
// isn't limited to Filled — a resting (Placed) or not-yet-placed (Pending) live order must
// also block that index from being handed out again, same as matrixNextRelativeSlot's own
// separate in-flight guard already assumes elsewhere.
func TestNextConfigIndex_PlacedAndPendingAlsoCountAsOccupied(t *testing.T) {
	levels := []GridLevel{lvl(1, 0.0569, LevelPlaced)}
	if idx := nextConfigIndex(levels, "above", 3); idx != 2 {
		t.Errorf("nextConfigIndex = %d, want 2 — a Placed (resting, unfilled) level must still occupy its slot", idx)
	}
	levels = []GridLevel{lvl(1, 0.0569, LevelPending)}
	if idx := nextConfigIndex(levels, "above", 3); idx != 2 {
		t.Errorf("nextConfigIndex = %d, want 2 — a Pending level must still occupy its slot", idx)
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

// --- matrixRelativeSafeZoneBlocks ---
//
// Regression for the incident found live 2026-09-21/22 (Gonchar 2.0 hedge, poligonorigin33
// account, COTIUSDT): matrixAfterSLClose's relative-slots branch skips the absolute-mode
// safe zone entirely, so a just-closed slot's config index became immediately eligible to
// reopen the moment nextConfigIndex recounted open levels — L(4) reopened and stopped out
// 8 times in under 5 hours. matrixRelativeSafeZoneBlocks is the gate that closes this hole.

func TestMatrixRelativeSafeZoneBlocks_NoSafeZoneConfigured_NeverBlocks(t *testing.T) {
	sr := shortStrategyWithLevels()
	sr.strategy.SafeZonePct = 0
	two := 2
	sr.levels = append(sr.levels, GridLevel{Slot: &two, Status: LevelSLClosed, SLPrice: 96.04})
	if sr.matrixRelativeSafeZoneBlocks(2, 95.0) {
		t.Error("SafeZonePct=0 must never block")
	}
}

func TestMatrixRelativeSafeZoneBlocks_NoPriorSLOnThisSlot_NeverBlocks(t *testing.T) {
	sr := shortStrategyWithLevels()
	sr.strategy.SafeZonePct = 1.5
	// Slot 2 was never SL-closed (only slot 1 was) — nothing to cool down from.
	one := 1
	sr.levels = append(sr.levels, GridLevel{Slot: &one, Status: LevelSLClosed, SLPrice: 96.04})
	if sr.matrixRelativeSafeZoneBlocks(2, 95.0) {
		t.Error("must not block a slot with no prior SL close of its own")
	}
}

func TestMatrixRelativeSafeZoneBlocks_Short_BlocksUntilPriceFallsBelowThreshold(t *testing.T) {
	sr := shortStrategyWithLevels()
	sr.strategy.SafeZonePct = 1.5
	two := 2
	sr.levels = append(sr.levels, GridLevel{Slot: &two, Status: LevelSLClosed, SLPrice: 96.04})
	// threshold = 96.04 * (1 - 1.5/100) = 94.5994
	if !sr.matrixRelativeSafeZoneBlocks(2, 95.0) {
		t.Error("price 95.0 has not recovered past the safe-zone threshold (~94.60) — must block")
	}
	if sr.matrixRelativeSafeZoneBlocks(2, 94.0) {
		t.Error("price 94.0 has recovered past the safe-zone threshold (~94.60) — must not block")
	}
}

func TestMatrixRelativeSafeZoneBlocks_Long_BlocksUntilPriceRisesAboveThreshold(t *testing.T) {
	sr := shortStrategyWithLevels()
	sr.strategy.Direction = DirectionLong
	sr.strategy.SafeZonePct = 1.5
	negOne := -1
	sr.levels = append(sr.levels, GridLevel{Slot: &negOne, Status: LevelSLClosed, SLPrice: 96.04})
	// threshold = 96.04 * (1 + 1.5/100) = 97.4806
	if !sr.matrixRelativeSafeZoneBlocks(-1, 97.0) {
		t.Error("price 97.0 has not recovered past the safe-zone threshold (~97.48) — must block")
	}
	if sr.matrixRelativeSafeZoneBlocks(-1, 98.0) {
		t.Error("price 98.0 has recovered past the safe-zone threshold (~97.48) — must not block")
	}
}

func TestMatrixRelativeSafeZoneBlocks_UsesMostRecentSLOnRepeatedCloses(t *testing.T) {
	sr := shortStrategyWithLevels()
	sr.strategy.SafeZonePct = 1.5
	two := 2
	// Slot 2 was stopped out twice; the SECOND (most recent, lower) trigger must govern.
	sr.levels = append(sr.levels,
		GridLevel{Slot: &two, Status: LevelSLClosed, SLPrice: 96.04},
		GridLevel{Slot: &two, Status: LevelSLClosed, SLPrice: 94.0},
	)
	// Against the stale first trigger (96.04) this price would already be clear (>94.6034
	// threshold with old trigger 96.04, no — recompute: 94.0*(1-0.015)=92.59). Use a price
	// between the two thresholds to prove which trigger actually governs.
	if !sr.matrixRelativeSafeZoneBlocks(2, 93.0) {
		t.Error("must use the most recent SL trigger (94.0, threshold ~92.59) — price 93.0 must still be blocked")
	}
	if sr.matrixRelativeSafeZoneBlocks(2, 92.0) {
		t.Error("price 92.0 has recovered past the most recent trigger's threshold (~92.59) — must not block")
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

// TestMatrixRelativeSlotSizing_PricesQtyAtCurrentPriceNotStaleTarget is the regression for
// the infinite cancel/retry loop found live 2026-08-18 (BEATUSDT L(-4)): a relative slot's
// qty must clear the exchange's minimum notional value at the price it will ACTUALLY fill
// at (currentPrice — matrixRelativeExpand only calls this once matrixSlotReached confirms
// price already reached/passed target, so a virtual slot fires as an immediate Market
// order at currentPrice, not at the stale target). Sizing off target instead — as the code
// used to — computed a qty that only cleared the minimum back when target was still near
// currentPrice; once price ran far past target, the same qty fell far short of the minimum
// at the real fill price, causing an unrecoverable cancel/reinsert loop that never adapts.
func TestMatrixRelativeSlotSizing_PricesQtyAtCurrentPriceNotStaleTarget(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong, GridSizeUSDT: 20},
		instr:    trader.InstrumentInfo{QtyStep: 1, MinQty: 1, MinNotionalValue: 5},
	}
	cfg := MatrixLevel{SizePct: 50} // 50% of $20 deposit = $10 nominal budget

	// target=1.8486 is where the slot was originally priced (stale); currentPrice=0.24 is
	// where it will actually fill, after price ran far past target (BEATUSDT's ~90% crash).
	_, qty := sr.matrixRelativeSlotSizing(cfg, 1.8486, 0.24)

	qf, err := strconv.ParseFloat(qty, 64)
	if err != nil {
		t.Fatalf("qty %q not a valid float: %v", qty, err)
	}
	notionalAtFill := qf * 0.24
	if notionalAtFill < sr.instr.MinNotionalValue {
		t.Errorf("qty=%s → notional at currentPrice(0.24) = %.4f, want >= MinNotionalValue=%.2f — "+
			"a real fill at this qty would be rejected by the exchange exactly like the production bug",
			qty, notionalAtFill, sr.instr.MinNotionalValue)
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

// TestMatrixPlaceRelativeVirtualOrder_BlockedByRiskGate_DoesNotReachExchange is the
// regression for the bypass found live 2026-08-20: virtual/relative-slot orders went
// straight to tradeStream.PlaceOrder without ever consulting riskGate, unlike
// placeMatrixLevel's absolute-mode entries — a relative-slots strategy could keep
// pyramiding past the configured per-symbol notional cap while its non-virtual sibling
// levels stayed correctly blocked (observed live: BIOUSDT hedge leg ran three full cycles
// entirely through this path on an account at ~$11 equity / $2.75 cap). Proves the gate
// now runs BEFORE any exchange call by asserting the level never advances past Pending —
// if the gate were skipped, this would panic on the nil sr.runner.tradeStream instead.
func TestMatrixPlaceRelativeVirtualOrder_BlockedByRiskGate_DoesNotReachExchange(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.Symbol = "BTCUSDT"
	sr.runner = &AccountRunner{
		positions:   map[string]float64{},
		posAvgEntry: map[string]float64{},
	}
	sr.runner.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25

	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "virt-1", Slot: &negOne, Status: LevelPending, TargetPrice: 102.0, Qty: "1.0", Side: "Sell",
	})
	placed := &sr.levels[len(sr.levels)-1]

	// Pre-seed riskBlockReason to the exact reason the gate will report, so the dedup
	// check (`if sr.riskBlockReason != reason`) skips the sr.warn(...) call — which would
	// otherwise hit a nil pool.Exec in this pure unit-test setup. sr.runner.pool
	// intentionally stays nil: the point of this test is proving the gate returns BEFORE
	// any PlaceOrder/logging work happens, not exercising the logging plumbing itself.
	sr.riskBlockReason = "лимит notional на символ"

	// qty=1 @ currentPrice=1000 → $1000 notional, far past cap=25 with zero existing
	// exposure → must be blocked.
	sr.matrixPlaceRelativeVirtualOrder(context.Background(), placed, 1000.0)

	if placed.Status != LevelPending {
		t.Errorf("level status = %v, want still Pending — a risk-blocked entry must not advance to Placed", placed.Status)
	}
	if placed.ExchangeOrderID != "" {
		t.Error("ExchangeOrderID must stay empty — riskGate must prevent ever reaching PlaceOrder")
	}
}

// TestMatrixPlaceRelativeVirtualOrder_AllowedByRiskGate_ReachesExchange is the inverse of
// the above: within the cap, the order must proceed to PlaceOrder as before — pins that
// the new gate doesn't accidentally block legitimate entries. Reaching the nil
// sr.runner.tradeStream panics, proving the gate let it through.
func TestMatrixPlaceRelativeVirtualOrder_AllowedByRiskGate_ReachesExchange(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.Symbol = "BTCUSDT"
	sr.runner = &AccountRunner{
		positions:   map[string]float64{},
		posAvgEntry: map[string]float64{},
	}
	sr.runner.risk = accountRiskState{equity: 100, notionalPct: 25, paused: false, updatedAt: time.Now()} // cap = 25

	negOne := -1
	sr.levels = append(sr.levels, GridLevel{
		ID: "virt-1", Slot: &negOne, Status: LevelPending, TargetPrice: 102.0, Qty: "0.01", Side: "Sell",
	})
	placed := &sr.levels[len(sr.levels)-1]

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic reaching PlaceOrder — an within-cap entry was blocked instead")
		}
	}()
	// qty=0.01 @ currentPrice=1000 → $10 notional, within cap=25 → gate must allow it
	// through to PlaceOrder.
	sr.matrixPlaceRelativeVirtualOrder(context.Background(), placed, 1000.0)
}

// --- matrixNextRelativeSlotPreview: Safe Zone clamping ---
//
// Regression for the incident found live 2026-09-23/24 (Gonchar 2.0 hedge,
// poligonorigin33 account, MUBARAKUSDT): the preview showed the raw config-step target
// even when matrixRelativeSafeZoneBlocks was silently holding the real expansion back for
// a much larger price move — the chart's "next order" line sat at a price already crossed
// by the market, with nothing explaining why no order had fired there.

func TestMatrixNextRelativeSlotPreview_SafeZoneMoreRestrictive_ClampsToThreshold(t *testing.T) {
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.SafeZonePct = 1.5
	one := 1
	// Slot 1 (config step 2%, target would be 100*0.98=98.0) closed by SL at 96.04.
	// Threshold = 96.04*(1-1.5/100) = 94.5994 — well past (lower than) the raw target 98.0.
	sr.levels = append(sr.levels, GridLevel{Slot: &one, Status: LevelSLClosed, SLPrice: 96.04})

	slot, price, _, ok := sr.matrixNextRelativeSlotPreview("above")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if slot != 1 {
		t.Fatalf("slot = %d, want 1 (the freed slot)", slot)
	}
	wantThreshold := 96.04 * (1 - 1.5/100)
	if math.Abs(price-wantThreshold) > 1e-6 {
		t.Errorf("price = %.6f, want %.6f (the Safe Zone threshold, not the raw target 98.0) — the chart's next-order line must sit right past the safe zone, not at an already-crossed target", price, wantThreshold)
	}
}

func TestMatrixNextRelativeSlotPreview_NoSafeZone_UsesRawTarget(t *testing.T) {
	// No SafeZonePct configured — matches TestMatrixNextRelativeSlotPreview_MatchesLiveComputation,
	// pinned again here specifically as the "clamping had no effect" counterpart to the test above.
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	_, _, wantTarget, _, _, _ := sr.matrixNextRelativeSlot("above")
	_, price, _, ok := sr.matrixNextRelativeSlotPreview("above")
	if !ok || price != wantTarget {
		t.Errorf("price = %v (ok=%v), want %v unclamped", price, ok, wantTarget)
	}
}

func TestMatrixNextRelativeSlotPreview_TargetMoreRestrictiveThanSafeZone_UsesTarget(t *testing.T) {
	// The reverse case: the SL trigger sat close to entry (99.0) and SafeZonePct is small, so
	// the safe zone clears (98.505) before the raw config target (98.0) does — the config
	// target is the actually-binding constraint here. The preview must show 98.0, not clamp
	// down to the less-restrictive, already-cleared safe zone threshold.
	sr := shortStrategyWithLevels(lvl(0, 100.0, LevelFilled))
	sr.strategy.SafeZonePct = 0.5 // threshold = 99.0*(1-0.5/100) = 98.505
	one := 1
	sr.levels = append(sr.levels, GridLevel{Slot: &one, Status: LevelSLClosed, SLPrice: 99.0})

	_, price, _, ok := sr.matrixNextRelativeSlotPreview("above")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if math.Abs(price-98.0) > 1e-6 {
		t.Errorf("price = %.6f, want 98.0 (the raw target is more restrictive than the already-cleared safe zone) — must not clamp to a less restrictive safe zone", price)
	}
}

// --- GetMatrixSafeZone: relative-slots branch ---

func newTestEngineWithStrategy(strategyID string, sr *StrategyRunner) *Engine {
	ar := &AccountRunner{strategies: map[string]*StrategyRunner{strategyID: sr}}
	return &Engine{runners: map[string]*AccountRunner{"acc1": ar}}
}

// TestGetMatrixSafeZone_RelativeSlots_ReturnsZoneForMostRecentSLClose is the regression for
// the visualization gap found live 2026-09-23/24: relative-slots strategies never populated
// matrixWaitingSlots (matrixAfterSLClose's relative-slots branch skips that bookkeeping), so
// GetMatrixSafeZone always returned nil for them even once matrixRelativeSafeZoneBlocks was
// actively enforcing a cooldown — the protection existed with nothing to show for it on the
// chart.
func TestGetMatrixSafeZone_RelativeSlots_ReturnsZoneForMostRecentSLClose(t *testing.T) {
	two := 2
	sr := &StrategyRunner{
		strategy: Strategy{ID: "strat-1", Direction: DirectionShort, RelativeSlots: true, SafeZonePct: 1.5},
		levels:   []GridLevel{{Slot: &two, Status: LevelSLClosed, SLPrice: 0.052680}},
	}
	e := newTestEngineWithStrategy("strat-1", sr)

	sz := e.GetMatrixSafeZone("strat-1")
	if sz == nil {
		t.Fatal("expected non-nil SafeZone for a relative-slots strategy with a recent SL close")
	}
	wantHigh := 0.052680
	wantLow := 0.052680 * (1 - 1.5/100)
	if math.Abs(sz.High-wantHigh) > 1e-9 || math.Abs(sz.Low-wantLow) > 1e-9 {
		t.Errorf("SafeZone = {Low:%.6f High:%.6f}, want {Low:%.6f High:%.6f}", sz.Low, sz.High, wantLow, wantHigh)
	}
}

func TestGetMatrixSafeZone_RelativeSlots_Long_LowIsTriggerHighIsThreshold(t *testing.T) {
	// Long mirrors short with low/high swapped: the SL trigger is the lower bound, the
	// recovery threshold (price must rise past it) is the upper bound.
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{ID: "strat-2", Direction: DirectionLong, RelativeSlots: true, SafeZonePct: 2.0},
		levels:   []GridLevel{{Slot: &one, Status: LevelSLClosed, SLPrice: 100.0}},
	}
	e := newTestEngineWithStrategy("strat-2", sr)

	sz := e.GetMatrixSafeZone("strat-2")
	if sz == nil {
		t.Fatal("expected non-nil SafeZone")
	}
	if sz.Low != 100.0 {
		t.Errorf("Low = %v, want 100.0 (the SL trigger)", sz.Low)
	}
	wantHigh := 100.0 * 1.02
	if math.Abs(sz.High-wantHigh) > 1e-9 {
		t.Errorf("High = %.6f, want %.6f (trigger + SafeZonePct)", sz.High, wantHigh)
	}
}

func TestGetMatrixSafeZone_RelativeSlots_NoSLCloseYet_ReturnsNil(t *testing.T) {
	sr := &StrategyRunner{
		strategy: Strategy{ID: "strat-3", Direction: DirectionShort, RelativeSlots: true, SafeZonePct: 1.5},
		levels:   []GridLevel{{Slot: intPtr(0), Status: LevelFilled, FilledPrice: 0.05}},
	}
	e := newTestEngineWithStrategy("strat-3", sr)

	if sz := e.GetMatrixSafeZone("strat-3"); sz != nil {
		t.Errorf("SafeZone = %+v, want nil — nothing has ever SL-closed on this strategy yet", sz)
	}
}

func intPtr(v int) *int { return &v }
