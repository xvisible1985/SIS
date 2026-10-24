package strategy

import (
	"context"
	"testing"
	"time"

	"sis/pkg/trader"
)

// Regression for the live bug (2026-10-24, account "Bybit"/N2, bot "Гончар 2.0",
// PTBUSDT grid): a settings-mismatch on resume after a process restart triggered
// restartGridCycle while L3's order had ALREADY filled on the exchange, but the fill
// event was lost in the restart so both DB and in-memory state still said "placed".
// restartGridCycle's cancelPlacedLevels is a no-op against an already-filled order,
// but the code right after it unconditionally reset every in-memory LevelPlaced level
// back to "pending" and cleared its ExchangeOrderID — discarding the fact that it had
// already filled for real. repriceRemainingFromFills then repriced "L3" from the
// wrong (earlier) base price, placeNextLevels placed a brand new order for it, the
// price had already moved past the new target so levelTriggerAlreadyCrossed fired a
// market-order fallback, and that second order filled too — doubling the real
// position for that level while strategy_levels only ever recorded one fill.
//
// Fix: before discarding a LevelPlaced level's ExchangeOrderID, confirm against the
// exchange (FetchOrderById) that the order hasn't already filled — mirroring the
// check reconcile.go already does for "placed order missing from exchange".

func newRestartGridCycleFixture(fake *fakeExchange, ar *AccountRunner) *StrategyRunner {
	return &StrategyRunner{
		strategy: Strategy{
			ID:           "11111111-2222-3333-4444-555555555555",
			Symbol:       "PTBUSDT",
			Category:     "linear",
			Direction:    DirectionLong,
			Status:       StatusActive,
			GridSizeUSDT: 100,
			Steps: []GridStep{
				{PriceMovePct: 1, SizePct: 50},
				{PriceMovePct: 1, SizePct: 50},
			},
		},
		runner: ar,
		cycle:  &Cycle{ID: "cycle-1", CycleNum: 2},
		levels: []GridLevel{
			{ID: "l1", LevelIdx: 1, Side: "Buy", Status: LevelFilled, FilledPrice: 0.0003, Qty: "100"},
			{ID: "l3", LevelIdx: 3, Side: "Buy", Status: LevelPlaced, ExchangeOrderID: "order-gen2", TargetPrice: 0.0003, Qty: "410570"},
		},
	}
}

func TestRestartGridCycle_PlacedLevelAlreadyFilledOnExchange_RecordsFillInsteadOfResettingToPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	sr := newRestartGridCycleFixture(fake, ar)

	// The exchange confirms the order genuinely filled before the cancel could take effect.
	fake.fetchOrderByIdQ.push(orderLookupResp{order: trader.Order{
		OrderStatus: "Filled",
		AvgPrice:    "0.0002",
		CumExecQty:  "410570",
	}}, nil)

	sr.restartGridCycle(ctx)

	var l3 *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == "l3" {
			l3 = &sr.levels[i]
		}
	}
	if l3 == nil {
		t.Fatal("level l3 disappeared from sr.levels")
	}
	if l3.Status != LevelFilled {
		t.Errorf("l3.Status = %v, want LevelFilled — an order already filled on the exchange must be recorded as a fill, not reset to pending", l3.Status)
	}
	if l3.FilledPrice != 0.0002 {
		t.Errorf("l3.FilledPrice = %v, want 0.0002 (from the exchange's AvgPrice)", l3.FilledPrice)
	}
	if l3.ExchangeOrderID != "order-gen2" {
		t.Errorf("l3.ExchangeOrderID = %q, want unchanged \"order-gen2\" — a confirmed fill must keep its order id, matching handleLevelFill's own convention", l3.ExchangeOrderID)
	}
	if n := len(fake.placeOrderCalls) + len(fake.placeOrderBatchCalls); n != 0 {
		t.Errorf("PlaceOrder(+Batch) called %d times, want 0 — a level already filled for real must never be re-placed", n)
	}
}

func TestRestartGridCycle_PlacedLevelGenuinelyNotFilled_StillResetsToPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fake := &fakeExchange{}
	ar := newTestAccountRunner(t, fake)
	sr := newRestartGridCycleFixture(fake, ar)

	// The exchange confirms the order is genuinely gone (cancelled), not filled.
	fake.fetchOrderByIdQ.push(orderLookupResp{order: trader.Order{
		OrderStatus: "Cancelled",
	}}, nil)

	sr.restartGridCycle(ctx)

	var l3 *GridLevel
	for i := range sr.levels {
		if sr.levels[i].ID == "l3" {
			l3 = &sr.levels[i]
		}
	}
	if l3 == nil {
		t.Fatal("level l3 disappeared from sr.levels")
	}
	if l3.Status != LevelPending {
		t.Errorf("l3.Status = %v, want LevelPending — a genuinely cancelled order must still reset to pending as before this fix", l3.Status)
	}
	if l3.ExchangeOrderID != "" {
		t.Errorf("l3.ExchangeOrderID = %q, want empty — a genuinely cancelled order's id must still be cleared", l3.ExchangeOrderID)
	}
}
