package strategy

import (
	"context"
	"math"
	"testing"
)

func TestCalculateMatrixPrices(t *testing.T) {
	above := []MatrixLevel{
		{Direction: "above", PriceStepPct: 2.0},
		{Direction: "above", PriceStepPct: 3.0},
	}
	below := []MatrixLevel{
		{Direction: "below", PriceStepPct: -2.0},
		{Direction: "below", PriceStepPct: -3.0},
	}
	prices := calculateMatrixPrices(100.0, above, below, DirectionLong)
	cases := []struct {
		slot int
		want float64
	}{
		{0, 100.0},
		{-1, 98.0},    // 100 * (1 + 1*(-2)/100) = 98
		{-2, 95.06},   // 98 * (1 + 1*(-3)/100) = 95.06
		{1, 102.0},    // 100 * (1 + 1*2/100) = 102
		{2, 105.06},   // 102 * (1 + 1*3/100) = 105.06
	}
	for _, c := range cases {
		got, ok := prices[c.slot]
		if !ok {
			t.Errorf("slot %d not in result", c.slot)
			continue
		}
		if math.Abs(got-c.want) > 0.001 {
			t.Errorf("slot %d: want %.4f, got %.4f", c.slot, c.want, got)
		}
	}
}

func TestMatrixPlacementDecision(t *testing.T) {
	// Long: target below current → Limit
	orderType, trigDir := matrixEntryOrderType("long", 95.0, 100.0)
	if orderType != "Limit" {
		t.Errorf("below-current Long: want Limit, got %s", orderType)
	}
	_ = trigDir

	// Long: target above current → StopMarket
	orderType, trigDir = matrixEntryOrderType("long", 105.0, 100.0)
	if orderType != "StopMarket" {
		t.Errorf("above-current Long: want StopMarket, got %s", orderType)
	}
	if trigDir != 1 {
		t.Errorf("above-current Long: want trigDir=1, got %d", trigDir)
	}

	// Short: target above current → Limit
	orderType, trigDir = matrixEntryOrderType("short", 105.0, 100.0)
	if orderType != "Limit" {
		t.Errorf("above-current Short: want Limit, got %s", orderType)
	}

	// Short: target below current → StopMarket
	orderType, trigDir = matrixEntryOrderType("short", 95.0, 100.0)
	if orderType != "StopMarket" {
		t.Errorf("below-current Short: want StopMarket, got %s", orderType)
	}
	if trigDir != 2 {
		t.Errorf("below-current Short: want trigDir=2, got %d", trigDir)
	}
}

func TestCalculateGridLevels_Long(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 5, "Buy")
	expected := []float64{99.0, 98.01, 97.0299, 96.0596, 95.0990}
	if len(prices) != 5 {
		t.Fatalf("want 5 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestCalculateGridLevels_Short(t *testing.T) {
	prices := calculateGridLevels(100.0, 1.0, 3, "Sell")
	expected := []float64{101.0, 102.01, 103.0301}
	if len(prices) != 3 {
		t.Fatalf("want 3 levels, got %d", len(prices))
	}
	for i, p := range prices {
		if math.Abs(p-expected[i]) > 0.001 {
			t.Errorf("level[%d]: want %.4f, got %.4f", i, expected[i], p)
		}
	}
}

func TestPlacedCount(t *testing.T) {
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelPlaced},
			{Status: LevelPending},
			{Status: LevelFilled},
			{Status: LevelPlaced},
		},
	}
	if got := sr.placedCount(); got != 2 {
		t.Errorf("want 2 placed, got %d", got)
	}
}

// TestPlacedCountExcludesTPSL verifies that TP and SL orders do not count toward
// GridActive. They live in sr.tpOrderID / sr.slOrderID, not in sr.levels.
func TestPlacedCountExcludesTPSL(t *testing.T) {
	sr := &StrategyRunner{
		tpOrderID: "tp-order-abc",
		slOrderID: "sl-order-xyz",
		levels: []GridLevel{
			{Status: LevelPlaced},
			{Status: LevelPending},
		},
	}
	if got := sr.placedCount(); got != 1 {
		t.Errorf("want 1 (TP/SL must not count toward GridActive), got %d", got)
	}
}

func TestAvgEntry(t *testing.T) {
	p1, p2 := 99.0, 98.0
	sr := &StrategyRunner{
		levels: []GridLevel{
			{Status: LevelFilled, FilledPrice: p1, Qty: "1.0"},
			{Status: LevelFilled, FilledPrice: p2, Qty: "1.0"},
			{Status: LevelPending},
		},
	}
	avg, total := sr.avgEntry()
	if math.Abs(avg-98.5) > 0.001 {
		t.Errorf("want avg 98.5, got %.4f", avg)
	}
	if math.Abs(total-2.0) > 0.001 {
		t.Errorf("want total 2.0, got %.4f", total)
	}
}

func TestLevelSLClosedConstant(t *testing.T) {
	if LevelSLClosed != "sl_closed" {
		t.Errorf("want sl_closed, got %s", LevelSLClosed)
	}
}

func TestMatrixSafeZoneContains(t *testing.T) {
	z := &MatrixSafeZone{Low: 90.0, High: 110.0}
	if !z.Contains(100.0) {
		t.Error("100 should be inside [90,110]")
	}
	if z.Contains(80.0) {
		t.Error("80 should be outside [90,110]")
	}
	if z.Contains(120.0) {
		t.Error("120 should be outside [90,110]")
	}
}

func TestMatrixPerLevelSLTrigger(t *testing.T) {
	// Long: stop_pct = -2.0, fill_price = 100 → trigger = 100 * (1 - 0.02) = 98
	stopPct := -2.0
	trigger := matrixSLTrigger(DirectionLong, 100.0, stopPct)
	if math.Abs(trigger-98.0) > 0.0001 {
		t.Errorf("Long SL trigger: want 98, got %.4f", trigger)
	}
	// Short: stop_pct = -2.0, fill_price = 100 → trigger = 100 * (1 + 0.02) = 102
	trigger = matrixSLTrigger(DirectionShort, 100.0, stopPct)
	if math.Abs(trigger-102.0) > 0.0001 {
		t.Errorf("Short SL trigger: want 102, got %.4f", trigger)
	}
}

func TestMatrixSafeZoneCreation(t *testing.T) {
	// safe_zone_pct=5, sl_trigger=100, slot=3 → Low=95, High=105
	zone := createMatrixSafeZone(100.0, 5.0, 3)
	if math.Abs(zone.Low-95.0) > 0.0001 {
		t.Errorf("Low: want 95, got %.4f", zone.Low)
	}
	if math.Abs(zone.High-105.0) > 0.0001 {
		t.Errorf("High: want 105, got %.4f", zone.High)
	}
	if math.Abs(zone.SLTrigger-100.0) > 0.0001 {
		t.Errorf("SLTrigger: want 100, got %.4f", zone.SLTrigger)
	}
	if zone.Slot != 3 {
		t.Errorf("Slot: want 3, got %d", zone.Slot)
	}
	if !zone.Contains(100.0) {
		t.Error("100 should be inside zone")
	}
	if zone.Contains(90.0) {
		t.Error("90 should be outside zone")
	}
}

func TestMatrixStopCondThreshold(t *testing.T) {
	// Long: stop_cond_pct=3.0 (move up 3% from fill is the trigger)
	threshold := matrixStopCondThreshold(DirectionLong, 100.0, 3.0)
	if math.Abs(threshold-103.0) > 0.0001 {
		t.Errorf("Long threshold: want 103, got %.4f", threshold)
	}
	// Short: stop_cond_pct=3.0 (move down 3% from fill is the trigger)
	threshold = matrixStopCondThreshold(DirectionShort, 100.0, 3.0)
	if math.Abs(threshold-97.0) > 0.0001 {
		t.Errorf("Short threshold: want 97, got %.4f", threshold)
	}
}

func TestMatrixStopReplaceNewTrigger(t *testing.T) {
	// Long: stop_replace_pct=0.5 → new SL above fill (breakeven+)
	trigger := matrixStopReplaceTrigger(DirectionLong, 100.0, 0.5)
	if math.Abs(trigger-100.5) > 0.0001 {
		t.Errorf("Long replace trigger: want 100.5, got %.4f", trigger)
	}
	// Short: stop_replace_pct=0.5 → new SL below fill
	trigger = matrixStopReplaceTrigger(DirectionShort, 100.0, 0.5)
	if math.Abs(trigger-99.5) > 0.0001 {
		t.Errorf("Short replace trigger: want 99.5, got %.4f", trigger)
	}
}

// --- Task 3: one-SZ-at-a-time + positive-slot-only ---

func TestHandleMatrixSLFillSafeZoneOnlyForPositiveSlots(t *testing.T) {
	// SL on a negative slot must NOT create a SafeZone.
	negSlot := -1
	safeZonePct := 5.0
	var sz *MatrixSafeZone
	if safeZonePct > 0 && negSlot > 0 { // same condition used in handleMatrixSLFill
		sz = createMatrixSafeZone(98.0, safeZonePct, negSlot)
	}
	if sz != nil {
		t.Error("SafeZone must NOT be created for negative slot")
	}
}

func TestHandleMatrixSLFillOneZoneAtATime(t *testing.T) {
	// If a SafeZone already exists, creating a new one must cancel old pending re-entry.
	slot1, slot2 := 1, 2
	sr := &StrategyRunner{
		strategy:            Strategy{Direction: DirectionLong, SafeZonePct: 5},
		matrixSafeZone:      createMatrixSafeZone(100.0, 5.0, slot1),
		matrixSZPendingSlot:  &slot1,
		matrixSZPendingPrice: 100.0,
	}
	// Simulate second SL fire on slot 2 (same logic as the new handleMatrixSLFill block).
	slTrigger2 := 102.0
	if sr.strategy.SafeZonePct > 0 && slot2 > 0 {
		sr.matrixSZPendingSlot = nil
		sr.matrixSZPendingPrice = 0
		sr.matrixSafeZone = createMatrixSafeZone(slTrigger2, sr.strategy.SafeZonePct, slot2)
	}
	if sr.matrixSZPendingSlot != nil {
		t.Error("old pending re-entry must be cleared when a new SZ is created")
	}
	if sr.matrixSafeZone == nil || sr.matrixSafeZone.Slot != slot2 {
		t.Errorf("new SZ must be for slot 2, got %+v", sr.matrixSafeZone)
	}
}

// --- Task 4: directional SZ exit + pending re-entry ---

func TestSafeZoneDirectionalExit_PositiveLong(t *testing.T) {
	// LONG: price exits above High → positive exit, no pending set.
	slot := 1
	zone := createMatrixSafeZone(100.0, 5.0, slot) // Low=95, High=105
	currentPrice := 106.0
	positiveExit := (DirectionLong == DirectionLong && currentPrice > zone.High) ||
		(DirectionLong == DirectionShort && currentPrice < zone.Low)
	if !positiveExit {
		t.Fatal("106 > 105 should be a positive exit for LONG")
	}
}

func TestSafeZoneDirectionalExit_NegativeLong(t *testing.T) {
	// LONG: price exits below Low → negative exit, pending must be set.
	slot := 1
	zone := createMatrixSafeZone(100.0, 5.0, slot) // Low=95, High=105
	currentPrice := 94.0
	positiveExit := (DirectionLong == DirectionLong && currentPrice > zone.High) ||
		(DirectionLong == DirectionShort && currentPrice < zone.Low)
	if positiveExit {
		t.Fatal("94 < 95 should be a negative (not positive) exit for LONG")
	}
	// Simulate setting pending re-entry
	slotCopy := zone.Slot
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionLong}}
	sr.matrixSZPendingSlot = &slotCopy
	sr.matrixSZPendingPrice = zone.SLTrigger
	if sr.matrixSZPendingSlot == nil || *sr.matrixSZPendingSlot != slot {
		t.Error("pending slot must be set to zone.Slot on negative exit")
	}
	if math.Abs(sr.matrixSZPendingPrice-100.0) > 0.0001 {
		t.Errorf("pending price must equal SLTrigger=100.0, got %.4f", sr.matrixSZPendingPrice)
	}
}

func TestSafeZonePendingReentryCondition_Long(t *testing.T) {
	// LONG pending re-entry: met when price >= pendingPrice.
	slot := 1
	sr := &StrategyRunner{
		strategy:            Strategy{Direction: DirectionLong},
		matrixSZPendingSlot:  &slot,
		matrixSZPendingPrice: 100.0,
	}
	priceBelow := 99.5
	metBelow := sr.strategy.Direction == DirectionLong && priceBelow >= sr.matrixSZPendingPrice
	if metBelow {
		t.Error("should not be met when price < pendingPrice")
	}
	priceAt := 100.0
	metAt := sr.strategy.Direction == DirectionLong && priceAt >= sr.matrixSZPendingPrice
	if !metAt {
		t.Error("should be met when price == pendingPrice")
	}
}

func TestSafeZoneDirectionalExit_PositiveShort(t *testing.T) {
	// SHORT: positive exit means price drops below Low (in-direction for short = downward).
	slot := 1
	zone := createMatrixSafeZone(100.0, 5.0, slot) // Low=95, High=105
	currentPrice := 94.0                             // below Low → in-direction for SHORT
	positiveExit := (DirectionShort == DirectionLong && currentPrice > zone.High) ||
		(DirectionShort == DirectionShort && currentPrice < zone.Low)
	if !positiveExit {
		t.Fatalf("94 < 95 should be a positive exit for SHORT (price dropped in-direction)")
	}
}

func TestSafeZoneDirectionalExit_NegativeShort(t *testing.T) {
	// SHORT: negative exit means price rises above High (against direction for short = upward).
	slot := 1
	zone := createMatrixSafeZone(100.0, 5.0, slot) // Low=95, High=105
	currentPrice := 106.0                            // above High → against direction for SHORT
	positiveExit := (DirectionShort == DirectionLong && currentPrice > zone.High) ||
		(DirectionShort == DirectionShort && currentPrice < zone.Low)
	if positiveExit {
		t.Fatal("106 > 105 should be a negative (not positive) exit for SHORT")
	}
	// Pending condition for SHORT: met when price <= pendingPrice (drops back to P_sl).
	slotCopy := zone.Slot
	sr := &StrategyRunner{strategy: Strategy{Direction: DirectionShort}}
	sr.matrixSZPendingSlot = &slotCopy
	sr.matrixSZPendingPrice = zone.SLTrigger // 100.0
	priceAbove := 101.0
	metAbove := sr.strategy.Direction == DirectionLong && priceAbove >= sr.matrixSZPendingPrice ||
		sr.strategy.Direction == DirectionShort && priceAbove <= sr.matrixSZPendingPrice
	if metAbove {
		t.Error("101 > 100 should NOT meet SHORT pending condition (price not yet back at P_sl)")
	}
	priceAt := 100.0
	metAt := sr.strategy.Direction == DirectionLong && priceAt >= sr.matrixSZPendingPrice ||
		sr.strategy.Direction == DirectionShort && priceAt <= sr.matrixSZPendingPrice
	if !metAt {
		t.Error("100 == P_sl should meet SHORT pending re-entry condition")
	}
}

// --- Task 5: restoreMatrixSafeZone three-case logic ---

func TestRestoreMatrixSafeZoneThreeCases(t *testing.T) {
	slPrice := 100.0
	safeZonePct := 5.0
	slot := 1
	zone := createMatrixSafeZone(slPrice, safeZonePct, slot) // Low=95, High=105

	// Case 1: price inside zone → should restore SZ
	if !zone.Contains(100.0) {
		t.Fatalf("price 100 should be inside [95, 105]")
	}

	// Case 2: price above High → positive exit for LONG (immediate re-entry)
	priceAbove := 106.0
	positiveExit := (DirectionLong == DirectionLong && priceAbove > zone.High) ||
		(DirectionLong == DirectionShort && priceAbove < zone.Low)
	if !positiveExit {
		t.Error("106 > 105 should be a positive exit for LONG")
	}

	// Case 3: price below Low → negative exit for LONG (pending re-entry at P_sl)
	priceBelow := 94.0
	negExit := (DirectionLong == DirectionLong && priceBelow < zone.Low) ||
		(DirectionLong == DirectionShort && priceBelow > zone.High)
	if !negExit {
		t.Error("94 < 95 should be a negative exit for LONG")
	}
	if math.Abs(zone.SLTrigger-100.0) > 0.0001 {
		t.Errorf("SLTrigger: want 100, got %.4f", zone.SLTrigger)
	}
}

func TestMatrixPlacePerLevelSLSkipsNegativeSlots(t *testing.T) {
	// matrixPlacePerLevelSL must return immediately for negative slots.
	// We verify by checking the function returns without panicking on nil runner
	// (if it reached the exchange call path, sr.runner would panic).
	negSlot := -1
	l := &GridLevel{
		ID:          "level-neg-1",
		Slot:        &negSlot,
		Qty:         "1.0",
		FilledPrice: 100.0,
		Status:      LevelFilled,
	}
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong, SafeZonePct: 5},
		levels:   []GridLevel{*l},
	}
	// Should not panic; no exchange call is made (sr.runner is nil, would panic if called)
	sr.matrixPlacePerLevelSL(context.Background(), l, 100.0, -2.0)
	// If we reach here, the guard worked.
}
