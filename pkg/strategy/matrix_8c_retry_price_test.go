package strategy

import "testing"

// TestMatrix8cRetryPrice_RelativeSlotsUsesTargetPrice is the regression for the live
// incident (2026-10-11, JCTUSDT/stepanovsa): block 8c's health-check retried a stuck
// relative-slots pending level with the live market price instead of its TargetPrice,
// which — since matrixRelativeExpand only ever inserts such a level after price has
// already passed TargetPrice — made matrixEntryOrderType choose a resting Limit at the
// stale target instead of a Market order. The order sat unfilled (price had moved on),
// exactly the same bug already fixed for matrixPlaceRelativeSlot/matrixRetryStuckRelativeSlot.
func TestMatrix8cRetryPrice_RelativeSlotsUsesTargetPrice(t *testing.T) {
	slot := 2
	l := &GridLevel{Slot: &slot, TargetPrice: 0.00190600}
	livePrice := 0.00179600 // price has already fallen well past target

	got := matrix8cRetryPrice(l, livePrice)

	if got != l.TargetPrice {
		t.Errorf("matrix8cRetryPrice() = %v, want %v (TargetPrice) — relative-slots levels must retry at TargetPrice so matrixEntryOrderType forces Market, not a stale Limit", got, l.TargetPrice)
	}
}

// TestMatrix8cRetryPrice_AbsoluteModeUsesLivePrice is the negative control: absolute-mode
// matrix levels (l.Slot == nil) never had this bug — matrixEntryOrderType comparing their
// TargetPrice against the live price is the intended, correct behavior for them (resting
// Limit/StopMarket until price arrives) — so the fix must not change their behavior.
func TestMatrix8cRetryPrice_AbsoluteModeUsesLivePrice(t *testing.T) {
	l := &GridLevel{Slot: nil, TargetPrice: 0.00190600}
	livePrice := 0.00179600

	got := matrix8cRetryPrice(l, livePrice)

	if got != livePrice {
		t.Errorf("matrix8cRetryPrice() = %v, want %v (live price) — absolute-mode levels must keep retrying against the live price", got, livePrice)
	}
}
