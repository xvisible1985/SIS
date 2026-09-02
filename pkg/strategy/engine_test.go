package strategy

import (
	"context"
	"math"
	"testing"
	"time"

	"sis/pkg/trader"
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

// Regression: matrixPlaceRelativeSlot / matrixRetryStuckRelativeSlot (matrix_relative_engine.go)
// are only ever called AFTER matrixSlotReached has already confirmed price reached/passed
// target — there is no "wait passively" phase to preserve for relative slots. They force
// this Market branch by passing target itself as the currentPrice argument to
// placeMatrixLevel, so this equality shortcut must always resolve to Market regardless of
// direction — pins the exact mechanism that fix relies on.
// Found live (2026-07-21): HEMIUSDT HEDGE (short) leg's L(-1) sat as an unfillable resting
// Limit far above a gapped-down market, because the order was placed at the stale target
// price instead of firing immediately once the trigger condition was already satisfied.
func TestMatrixPlacementDecision_TargetEqualsCurrentForcesMarket(t *testing.T) {
	orderType, trigDir := matrixEntryOrderType("short", 98.0, 98.0)
	if orderType != "Market" {
		t.Errorf("target==current (short): want Market, got %s", orderType)
	}
	if trigDir != 0 {
		t.Errorf("target==current (short): want trigDir=0, got %d", trigDir)
	}

	orderType, trigDir = matrixEntryOrderType("long", 102.0, 102.0)
	if orderType != "Market" {
		t.Errorf("target==current (long): want Market, got %s", orderType)
	}
	if trigDir != 0 {
		t.Errorf("target==current (long): want trigDir=0, got %d", trigDir)
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

// matrixTPIsAdverse decides whether matrixUpdateTP places a plain take-profit LIMIT
// order (adverse=true) or a reduce-only STOP order (adverse=false, price already ran
// past ТВХ in the position's favor — a limit order there would fill instantly).
func TestMatrixTPIsAdverse(t *testing.T) {
	if !matrixTPIsAdverse(DirectionLong, 99.0, 100.0) {
		t.Error("long: price below ТВХ should be adverse (classic recovery TP)")
	}
	if matrixTPIsAdverse(DirectionLong, 101.0, 100.0) {
		t.Error("long: price above ТВХ should not be adverse (pyramided favorably → SL path)")
	}
	if !matrixTPIsAdverse(DirectionShort, 101.0, 100.0) {
		t.Error("short: price above ТВХ should be adverse (mirrored)")
	}
	if matrixTPIsAdverse(DirectionShort, 99.0, 100.0) {
		t.Error("short: price below ТВХ should not be adverse (pyramided favorably → SL path)")
	}
}

// matrixTPCrossed decides LIMIT vs STOP for the TP order matrixUpdateTP actually places —
// distinct from matrixTPIsAdverse (which only tracks ТВХ crossing, used to pick the
// governing level). Regression for the production incident: ALLOUSDT short sat with price
// between ТВХ and tpPrice — already "favorable" per matrixTPIsAdverse, but not yet at the
// TP target — for 2.5 days, during which matrixUpdateTP kept computing a STOP trigger price
// on the wrong side of current price and Bybit rejected it every tick (110092).
func TestMatrixTPCrossed(t *testing.T) {
	if !matrixTPCrossed(DirectionLong, 101.0, 100.0) {
		t.Error("long: price at/above tpPrice should be crossed (STOP path)")
	}
	if matrixTPCrossed(DirectionLong, 99.0, 100.0) {
		t.Error("long: price below tpPrice should not be crossed (LIMIT path)")
	}
	if !matrixTPCrossed(DirectionShort, 99.0, 100.0) {
		t.Error("short: price at/below tpPrice should be crossed (STOP path)")
	}
	if matrixTPCrossed(DirectionShort, 101.0, 100.0) {
		t.Error("short: price above tpPrice should not be crossed (LIMIT path)")
	}

	// The incident gap: short, ТВХ=100 (avgEntryPrice), tpPrice=98 (2% favorable target),
	// price=99 — already past ТВХ into profit (matrixTPIsAdverse says "favorable"), but
	// short of tpPrice itself. matrixTPCrossed must say "not crossed" here so
	// matrixUpdateTP takes the LIMIT path instead of computing an invalid STOP trigger.
	if matrixTPIsAdverse(DirectionShort, 99.0, 100.0) {
		t.Fatal("test setup: price=99 vs ТВХ=100 must read as favorable for this scenario to reproduce the incident")
	}
	if matrixTPCrossed(DirectionShort, 99.0, 98.0) {
		t.Error("short: price=99 has not reached tpPrice=98 yet — must not be crossed, else matrixUpdateTP tries an invalid STOP trigger (production incident: 110092 for 2.5 days)")
	}
}

// matrixMostFavorableFill is the mirror of matrixLatestActiveFill, used once price has
// crossed to the favorable side of ТВХ (see TestMatrixTPIsAdverse).
func TestMatrixMostFavorableFill_Long(t *testing.T) {
	negOne, one, two := -1, 1, 2
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong},
		levels: []GridLevel{
			{Slot: &negOne, Status: LevelFilled, FilledPrice: 98.0},
			{Slot: &one, Status: LevelFilled, FilledPrice: 102.0},
			{Slot: &two, Status: LevelFilled, FilledPrice: 104.0},
		},
	}
	got, ok := sr.matrixMostFavorableFill()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.FilledPrice != 104.0 {
		t.Fatalf("favorable fill = %.2f, want 104.0 (highest price for long)", got.FilledPrice)
	}
}

func TestMatrixMostFavorableFill_Short(t *testing.T) {
	one, negOne, negTwo := 1, -1, -2
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionShort},
		levels: []GridLevel{
			{Slot: &one, Status: LevelFilled, FilledPrice: 102.0},
			{Slot: &negOne, Status: LevelFilled, FilledPrice: 98.0},
			{Slot: &negTwo, Status: LevelFilled, FilledPrice: 96.0},
		},
	}
	got, ok := sr.matrixMostFavorableFill()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.FilledPrice != 96.0 {
		t.Fatalf("favorable fill = %.2f, want 96.0 (lowest price for short)", got.FilledPrice)
	}
}

func TestMatrixMostFavorableFill_IgnoresUnfilledAndEmpty(t *testing.T) {
	one := 1
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong},
		levels: []GridLevel{
			{Slot: &one, Status: LevelPending, FilledPrice: 0},
		},
	}
	if _, ok := sr.matrixMostFavorableFill(); ok {
		t.Fatal("expected ok=false — no filled levels")
	}
}

// TestMatrixMostFavorableFill_PrefersRealDCALevelOverZero is the regression for the bug
// found live 2026-08-21: L(0)'s fill price is always the most extreme "in favor" price
// once a long-that-DCA'd-down (or short-that-DCA'd-up) has any favorable-side comparison
// made against it, because L(0) filled at the best price of the whole set by construction.
// Since matrix_entry_level carries no tp_pct/stop_pct by design, L(0) winning this
// comparison meant TP silently never got placed for MatrixNova strategies with several
// levels filled — matrixUpdateTP: "tp_pct не настроен для слота L(0)" logged every
// reconcile tick for hours despite L(-1)/L(-2) having valid tp_pct configs. A real DCA
// level must always win over L(0) when one exists, regardless of price.
func TestMatrixMostFavorableFill_PrefersRealDCALevelOverZero(t *testing.T) {
	zero, negOne := 0, -1
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong},
		levels: []GridLevel{
			// L(0) filled at the HIGHEST price of the set — by the old (buggy) comparison
			// this alone would win "most favorable for a long", even though L(-1) is the
			// only level with a real tp_pct config in matrix_entry_level's null-tp_pct setup.
			{Slot: &zero, Status: LevelFilled, FilledPrice: 105.0},
			{Slot: &negOne, Status: LevelFilled, FilledPrice: 98.0},
		},
	}
	got, ok := sr.matrixMostFavorableFill()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.Slot == nil || *got.Slot != -1 {
		t.Fatalf("favorable fill slot = %v, want -1 — a real DCA level must govern over L(0) whenever one has filled", got.Slot)
	}
}

// TestMatrixMostFavorableFill_FallsBackToZeroWhenItIsTheOnlyFill pins the other half of
// the same fix: when L(0) genuinely is the only fill (fresh cycle, no DCA yet), it must
// still be returned — matrixUpdateTP's "tp_pct не настроен" branch handles that case as
// expected/normal (see its doc comment), it just must not be preferred over a real level.
func TestMatrixMostFavorableFill_FallsBackToZeroWhenItIsTheOnlyFill(t *testing.T) {
	zero := 0
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong},
		levels: []GridLevel{
			{Slot: &zero, Status: LevelFilled, FilledPrice: 100.0},
		},
	}
	got, ok := sr.matrixMostFavorableFill()
	if !ok {
		t.Fatal("expected ok=true — L(0) is a valid fill, just a low-priority one")
	}
	if got.Slot == nil || *got.Slot != 0 {
		t.Fatalf("favorable fill slot = %v, want 0 (only fill available)", got.Slot)
	}
}

// TestMatrixLatestActiveFill_ExcludesZeroWhenOnlyFavorableLevelsFilled is the regression
// for the bug found live 2026-08-31 (1000NEIROCTOUSDT): a short whose "below" (favorable/
// pyramid) levels filled at progressively BETTER prices than L(0) has no fill actually
// worse than the entry. matrixLatestActiveFill's job is to find the level furthest AGAINST
// the position — before this fix it had no L(0) exclusion at all (unlike
// matrixMostFavorableFill), so L(0) won by construction: its price is the worst of the set
// purely because every other fill was in the position's favor. Since matrix_entry_level
// carries no tp_pct, matrixUpdateTP's adverse branch (which calls this function) then sat
// with zero TP for over a day. A real DCA/pyramid level with a configured tp_pct must win
// over L(0) whenever one exists, exactly like matrixMostFavorableFill already guarantees.
func TestMatrixLatestActiveFill_ExcludesZeroWhenOnlyFavorableLevelsFilled(t *testing.T) {
	zero, one, two := 0, 1, 2
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionShort},
		levels: []GridLevel{
			// L(0): worst (highest) price of the set purely because slots 1/2 both filled
			// favorably (lower, for a short) — none of them is a genuine adverse DCA fill.
			{Slot: &zero, Status: LevelFilled, FilledPrice: 0.08594},
			{Slot: &one, Status: LevelFilled, FilledPrice: 0.08337},
			{Slot: &two, Status: LevelFilled, FilledPrice: 0.08079},
		},
	}
	got, ok := sr.matrixLatestActiveFill()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.Slot == nil || *got.Slot != 1 {
		t.Fatalf("governing slot = %v, want 1 — the worst-priced REAL level must govern over L(0)", got.Slot)
	}
}

// TestMatrixLatestActiveFill_FallsBackToZeroWhenItIsTheOnlyFill mirrors
// TestMatrixMostFavorableFill_FallsBackToZeroWhenItIsTheOnlyFill: a fresh cycle with only
// L(0) filled must still return it — matrixUpdateTP's "tp_pct не настроен" branch handles
// that as the expected "briefly unprotected" state, it just must never be preferred over a
// real level once one fills.
func TestMatrixLatestActiveFill_FallsBackToZeroWhenItIsTheOnlyFill(t *testing.T) {
	zero := 0
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionShort},
		levels: []GridLevel{
			{Slot: &zero, Status: LevelFilled, FilledPrice: 100.0},
		},
	}
	got, ok := sr.matrixLatestActiveFill()
	if !ok {
		t.Fatal("expected ok=true — L(0) is a valid fill, just a low-priority one")
	}
	if got.Slot == nil || *got.Slot != 0 {
		t.Fatalf("governing slot = %v, want 0 (only fill available)", got.Slot)
	}
}

// TestMatrixLatestActiveFill_GenuineAdverseDCAStillWorksUnchanged confirms the exclusion
// doesn't affect the case matrixLatestActiveFill exists for in the first place: a real DCA
// level that filled genuinely worse than L(0) (price kept moving against the position) must
// still be selected — same outcome as before this fix, since that level would have won the
// price comparison over L(0) either way.
func TestMatrixLatestActiveFill_GenuineAdverseDCAStillWorksUnchanged(t *testing.T) {
	zero, negOne := 0, -1
	sr := &StrategyRunner{
		strategy: Strategy{Direction: DirectionLong},
		levels: []GridLevel{
			{Slot: &zero, Status: LevelFilled, FilledPrice: 100.0},
			{Slot: &negOne, Status: LevelFilled, FilledPrice: 95.0}, // genuinely worse for a long
		},
	}
	got, ok := sr.matrixLatestActiveFill()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got.Slot == nil || *got.Slot != -1 {
		t.Fatalf("governing slot = %v, want -1 — the genuinely worse-priced DCA level must govern", got.Slot)
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

func TestMatrixApplyStopCondSLsSkipsAlreadyReplaced(t *testing.T) {
	// SLReplaced=true → must exit before touching sr.runner (nil here).
	slot := 1
	condPct := 1.5
	replacePct := 0.5
	sr := &StrategyRunner{
		strategy: Strategy{
			Direction: DirectionLong,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		levels: []GridLevel{
			{
				ID:          "level-1",
				Slot:        &slot,
				Status:      LevelFilled,
				FilledPrice: 100.0,
				SLReplaced:  true,
			},
		},
	}
	// price (105) > threshold (101.5) — would place SL if guard is missing → panic on nil runner
	sr.matrixApplyStopCondSLs(context.Background(), 105.0)
}

func TestMatrixApplyStopCondSLsSkipsBelowThreshold(t *testing.T) {
	// price < stop_cond threshold → condition not met, no SL placed.
	slot := 1
	condPct := 1.5
	replacePct := 0.5
	sr := &StrategyRunner{
		strategy: Strategy{
			Direction: DirectionLong,
			MatrixLevels: []MatrixLevel{
				{Direction: "above", StopCondPct: &condPct, StopReplacePct: &replacePct},
			},
		},
		levels: []GridLevel{
			{
				ID:          "level-1",
				Slot:        &slot,
				Status:      LevelFilled,
				FilledPrice: 100.0,
				SLReplaced:  false,
				SLOrderID:   "",
			},
		},
	}
	// price 101.0 < threshold 101.5 → condMet=false → no SL → no panic
	sr.matrixApplyStopCondSLs(context.Background(), 101.0)
}

// TestOnPositionEvent_CachesLeverage pins that a WS position event's leverage field is
// cached and retrievable via GetPositionLeverage — the read side of the leverage-drift
// guard (reconcile.go block 10), which compares this cache against confirmedLeverage to
// detect when the exchange has silently reduced leverage outside this system.
func TestOnPositionEvent_CachesLeverage(t *testing.T) {
	ar := &AccountRunner{
		positions:           make(map[string]float64),
		posAvgEntry:         make(map[string]float64),
		posLeverage:         make(map[string]float64),
		discrepancyLoggedAt: make(map[string]time.Time),
		strategies:          make(map[string]*StrategyRunner),
		orderIndex:          make(map[string]orderRef),
	}

	if got := ar.GetPositionLeverage("BTCUSDT", 1); got != 0 {
		t.Fatalf("GetPositionLeverage before any event = %v, want 0 (unknown)", got)
	}

	ar.OnPositionEvent(trader.PositionEvent{
		Symbol: "BTCUSDT", PositionIdx: 1, Size: "10", AvgPrice: "100", Leverage: "25",
	})
	if got := ar.GetPositionLeverage("BTCUSDT", 1); got != 25 {
		t.Errorf("GetPositionLeverage after event = %v, want 25", got)
	}
	// Different positionIdx/symbol must stay unaffected — keyed independently.
	if got := ar.GetPositionLeverage("BTCUSDT", 2); got != 0 {
		t.Errorf("GetPositionLeverage for untouched positionIdx = %v, want 0", got)
	}
	if got := ar.GetPositionLeverage("ETHUSDT", 1); got != 0 {
		t.Errorf("GetPositionLeverage for untouched symbol = %v, want 0", got)
	}

	// A later event with leverage="0" (or missing) must NOT clobber the cached value —
	// Bybit's WS occasionally omits fields on partial updates; 0 means "not reported here",
	// not "leverage is now zero".
	ar.OnPositionEvent(trader.PositionEvent{
		Symbol: "BTCUSDT", PositionIdx: 1, Size: "10", AvgPrice: "100",
	})
	if got := ar.GetPositionLeverage("BTCUSDT", 1); got != 25 {
		t.Errorf("GetPositionLeverage after event without leverage field = %v, want unchanged 25", got)
	}
}
