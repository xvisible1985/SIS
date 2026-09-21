//go:build integration

package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"sis/pkg/trader"
)

// fakeReconcileExchange is a minimal trader.Exchange stand-in for fullReconcile tests —
// only FetchRecentClosedPnl does anything; every other method is an unused stub. Records
// the `since` it was called with so tests can assert fullReconcile passes its own wide
// lookback rather than reusing syncAccount's narrow rolling watermark.
type fakeReconcileExchange struct {
	pnls     map[string][]trader.ClosedPnl // keyed by category
	gotSince map[string]time.Time
}

func (f *fakeReconcileExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	if f.gotSince == nil {
		f.gotSince = map[string]time.Time{}
	}
	f.gotSince[category] = since
	return f.pnls[category], nil
}

func (f *fakeReconcileExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return trader.OrderResult{}, nil
}
func (f *fakeReconcileExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	return nil, nil
}
func (f *fakeReconcileExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	return nil
}
func (f *fakeReconcileExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	return nil
}
func (f *fakeReconcileExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return trader.OrderResult{}, nil
}
func (f *fakeReconcileExchange) CancelOrderREST(ctx context.Context, req trader.CancelRequest) error {
	return nil
}
func (f *fakeReconcileExchange) CancelAllOrders(ctx context.Context, req trader.CancelAllRequest) error {
	return nil
}
func (f *fakeReconcileExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	return nil, nil
}
func (f *fakeReconcileExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	return nil, nil
}
func (f *fakeReconcileExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]trader.ClosedPnl, error) {
	return nil, nil
}
func (f *fakeReconcileExchange) GetWalletBalance(ctx context.Context) (float64, float64, error) {
	return 0, 0, nil
}
func (f *fakeReconcileExchange) SetLeverage(ctx context.Context, req trader.LeverageRequest) error {
	return nil
}
func (f *fakeReconcileExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	return nil
}
func (f *fakeReconcileExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	return 0, nil
}

var _ trader.Exchange = (*fakeReconcileExchange)(nil)

// TestClosedPnlSyncer_FullReconcile_RecordsOldMissedManualClose: an order that closed 10
// days ago, has no owning strategy in our DB and was never written to trade_history (the
// exact "syncAccount's watermark already moved past it" scenario this task fixes) must be
// picked up by fullReconcile and recorded as a manual trade_history row.
func TestClosedPnlSyncer_FullReconcile_RecordsOldMissedManualClose(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "fullrec")
	accID := createTestAccount(t, s, userID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", accID) })

	closeTime := time.Now().Add(-10 * 24 * time.Hour)
	fake := &fakeReconcileExchange{pnls: map[string][]trader.ClosedPnl{
		"linear": {{
			Symbol: "FULLRECUSDT", OrderId: "old-missed-order-1", OrderLinkId: "",
			Side: "Sell", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "1.1",
			ClosedPnl: "1.0", CreatedTime: fmtMs(closeTime), Category: "linear",
		}},
	}}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	acc := closedPnlAccount{id: accID, ownerID: userID}
	syncer.fullReconcile(ctx, acc, trader.Credentials{}, fake)

	var count int
	var source string
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`,
		"old-missed-order-1",
	).Scan(&count); err != nil {
		t.Fatalf("query trade_history: %v", err)
	}
	if count != 1 {
		t.Fatalf("trade_history rows for the missed order = %d, want 1", count)
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT source FROM trade_history WHERE bybit_close_order_id=$1`, "old-missed-order-1",
	).Scan(&source); err != nil {
		t.Fatalf("query source: %v", err)
	}
	if source != "manual" {
		t.Errorf("source = %q, want %q (no owning strategy exists for this order)", source, "manual")
	}

	// fullReconcile must pass its own wide lookback (~14 days), not a narrow window —
	// the whole point is reaching further back than syncAccount's rolling watermark ever
	// would.
	gotSince := fake.gotSince["linear"]
	wantSince := time.Now().Add(-fullReconcileLookback)
	if gotSince.After(wantSince.Add(time.Minute)) || gotSince.Before(wantSince.Add(-time.Minute)) {
		t.Errorf("fullReconcile called FetchRecentClosedPnl with since=%v, want ~%v (fullReconcileLookback)", gotSince, wantSince)
	}
}

// TestClosedPnlSyncer_FullReconcile_SkipsAlreadyRecorded: an order already present in
// trade_history (recorded by the live path or a prior reconcile pass) must not produce a
// duplicate row when fullReconcile sees it again — processClosedPnl's own order-id
// idempotency check must hold across repeated wide-window sweeps.
func TestClosedPnlSyncer_FullReconcile_SkipsAlreadyRecorded(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createWHUser(t, s, "fullrecdup")
	accID := createTestAccount(t, s, userID)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM trade_history WHERE account_id=$1", accID) })

	closeTime := time.Now().Add(-3 * 24 * time.Hour)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO trade_history (account_id, owner_id, symbol, category, direction,
			cycle_num, result, source, opened_at, closed_at, net_pnl, bybit_close_order_id)
		VALUES ($1,$2,'DUPUSDT','linear','long',0,'manual','manual',$3,$3,1.0,'already-recorded-1')`,
		accID, userID, closeTime); err != nil {
		t.Fatalf("seed existing trade_history row: %v", err)
	}

	fake := &fakeReconcileExchange{pnls: map[string][]trader.ClosedPnl{
		"linear": {{
			Symbol: "DUPUSDT", OrderId: "already-recorded-1", OrderLinkId: "",
			Side: "Sell", Qty: "10", AvgEntryPrice: "1.0", AvgExitPrice: "1.1",
			ClosedPnl: "1.0", CreatedTime: fmtMs(closeTime), Category: "linear",
		}},
	}}

	syncer := NewClosedPnlSyncer(s.pool, "test-enc-key")
	acc := closedPnlAccount{id: accID, ownerID: userID}
	syncer.fullReconcile(ctx, acc, trader.Credentials{}, fake)

	var count int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM trade_history WHERE bybit_close_order_id=$1`,
		"already-recorded-1",
	).Scan(&count); err != nil {
		t.Fatalf("query trade_history: %v", err)
	}
	if count != 1 {
		t.Errorf("trade_history rows for the already-recorded order = %d, want 1 (no duplicate)", count)
	}
}

func fmtMs(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}
