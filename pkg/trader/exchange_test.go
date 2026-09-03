package trader

import (
	"context"
	"errors"
	"testing"
)

// fakeWSOrderClient is a hand-written stand-in for *TradeStream, so the WS-backed
// BybitExchange methods can be tested without a real WS connection (TradeStream has no
// existing test harness — building one is out of scope for this plan, which only
// reorganizes existing, already-working code behind an interface).
type fakeWSOrderClient struct {
	placeOrderReq   OrderRequest
	placeOrderResp  OrderResult
	placeOrderErr   error
	placeBatchReq   BatchPlaceRequest
	placeBatchResp  []BatchPlaceResult
	placeBatchErr   error
	cancelOrderReq  CancelRequest
	cancelOrderErr  error
	cancelBatchReq  BatchCancelRequest
	cancelBatchErr  error
}

func (f *fakeWSOrderClient) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	f.placeOrderReq = req
	return f.placeOrderResp, f.placeOrderErr
}

func (f *fakeWSOrderClient) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	f.placeBatchReq = req
	return f.placeBatchResp, f.placeBatchErr
}

func (f *fakeWSOrderClient) CancelOrder(ctx context.Context, req CancelRequest) error {
	f.cancelOrderReq = req
	return f.cancelOrderErr
}

func (f *fakeWSOrderClient) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	f.cancelBatchReq = req
	return f.cancelBatchErr
}

func TestBybitExchange_PlaceOrder_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{placeOrderResp: OrderResult{OrderId: "ord-1", OrderLinkId: "link-1"}}
	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, fake)

	req := OrderRequest{Symbol: "BTCUSDT", Category: "linear", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "50000"}
	got, err := ex.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderId != "ord-1" || got.OrderLinkId != "link-1" {
		t.Errorf("PlaceOrder result = %+v, want the fake's canned OrderResult", got)
	}
	if fake.placeOrderReq.Symbol != "BTCUSDT" || fake.placeOrderReq.Price != "50000" {
		t.Errorf("fake received req = %+v, want the exact req passed to PlaceOrder", fake.placeOrderReq)
	}
}

func TestBybitExchange_PlaceOrder_PropagatesError(t *testing.T) {
	wantErr := errors.New("bybit: retCode=110017: current position is zero")
	fake := &fakeWSOrderClient{placeOrderErr: wantErr}
	ex := NewBybitExchange(Credentials{}, fake)

	_, err := ex.PlaceOrder(context.Background(), OrderRequest{Symbol: "BTCUSDT"})
	if !errors.Is(err, wantErr) {
		t.Errorf("PlaceOrder error = %v, want %v", err, wantErr)
	}
}

func TestBybitExchange_PlaceOrderBatch_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{placeBatchResp: []BatchPlaceResult{{OrderId: "b1"}, {OrderId: "b2"}}}
	ex := NewBybitExchange(Credentials{}, fake)

	req := BatchPlaceRequest{Category: "linear", Request: []BatchOrderItem{{Symbol: "ETHUSDT"}}}
	got, err := ex.PlaceOrderBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 || got[0].OrderId != "b1" || got[1].OrderId != "b2" {
		t.Errorf("PlaceOrderBatch result = %+v, want the fake's canned results", got)
	}
	if fake.placeBatchReq.Category != "linear" {
		t.Errorf("fake received req.Category = %q, want %q", fake.placeBatchReq.Category, "linear")
	}
}

func TestBybitExchange_CancelOrder_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{}
	ex := NewBybitExchange(Credentials{}, fake)

	req := CancelRequest{Symbol: "BTCUSDT", Category: "linear", OrderId: "ord-1"}
	if err := ex.CancelOrder(context.Background(), req); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if fake.cancelOrderReq.OrderId != "ord-1" {
		t.Errorf("fake received req.OrderId = %q, want %q", fake.cancelOrderReq.OrderId, "ord-1")
	}
}

func TestBybitExchange_CancelOrderBatch_DelegatesToWSClient(t *testing.T) {
	fake := &fakeWSOrderClient{}
	ex := NewBybitExchange(Credentials{}, fake)

	req := BatchCancelRequest{Category: "linear", Request: []BatchCancelItem{{Symbol: "BTCUSDT", OrderId: "ord-1"}}}
	if err := ex.CancelOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	if fake.cancelBatchReq.Category != "linear" {
		t.Errorf("fake received req.Category = %q, want %q", fake.cancelBatchReq.Category, "linear")
	}
}
