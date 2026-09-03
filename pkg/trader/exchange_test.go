package trader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeWSOrderClient is a hand-written stand-in for *TradeStream, so the WS-backed
// BybitExchange methods can be tested without a real WS connection (TradeStream has no
// existing test harness — building one is out of scope for this plan, which only
// reorganizes existing, already-working code behind an interface).
type fakeWSOrderClient struct {
	placeOrderReq  OrderRequest
	placeOrderResp OrderResult
	placeOrderErr  error
	placeBatchReq  BatchPlaceRequest
	placeBatchResp []BatchPlaceResult
	placeBatchErr  error
	cancelOrderReq CancelRequest
	cancelOrderErr error
	cancelBatchReq BatchCancelRequest
	cancelBatchErr error
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

func TestBybitExchange_FetchPositions_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("category") {
		case "linear":
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"BTCUSDT","side":"Buy","size":"0.5"}]}}`))
		default:
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[]}}`))
		}
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	found := false
	for _, p := range got {
		if p.Symbol == "BTCUSDT" && p.Category == "linear" {
			found = true
		}
	}
	if !found {
		t.Errorf("FetchPositions = %+v, want a BTCUSDT/linear position", got)
	}
}

func TestBybitExchange_FetchOpenOrdersForSymbolAll_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		filter := r.URL.Query().Get("orderFilter")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"orderId":"ord-` + filter + `","symbol":"BTCUSDT"}],"nextPageCursor":""}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchOpenOrdersForSymbolAll(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("FetchOpenOrdersForSymbolAll: %v", err)
	}
	// bybit.go's FetchOpenOrdersForSymbolAll queries both "Order" and "StopOrder"
	// filters — the mock server echoes the filter into orderId, so two distinct IDs
	// prove both filters were actually requested through this delegation.
	if len(got) != 2 {
		t.Fatalf("FetchOpenOrdersForSymbolAll returned %d orders, want 2 (Order + StopOrder)", len(got))
	}
	seen := map[string]bool{}
	for _, o := range got {
		seen[o.OrderId] = true
	}
	if !seen["ord-Order"] || !seen["ord-StopOrder"] {
		t.Errorf("FetchOpenOrdersForSymbolAll order IDs = %v, want ord-Order and ord-StopOrder", seen)
	}
}

func TestBybitExchange_FetchClosedPnlForSymbol_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v5/position/closed-pnl" {
			// e.g. /v5/market/time — doSignedGET's timestamp sync. Not load-bearing here.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("request symbol = %q, want BTCUSDT", r.URL.Query().Get("symbol"))
		}
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"BTCUSDT","orderId":"ord-1","closedPnl":"5.5"}]}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "ord-1" || got[0].ClosedPnl != "5.5" {
		t.Errorf("FetchClosedPnlForSymbol = %+v, want one ord-1 row with closedPnl=5.5", got)
	}
}

func TestBybitExchange_FetchRecentClosedPnl_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"symbol":"ETHUSDT","orderId":"ord-2","closedPnl":"1.1"}],"nextPageCursor":""}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	got, err := ex.FetchRecentClosedPnl(context.Background(), "linear", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "ord-2" {
		t.Errorf("FetchRecentClosedPnl = %+v, want one ord-2 row", got)
	}
}

func TestBybitExchange_GetWalletBalance_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"list":[{"totalEquity":"1000.5","totalAvailableBalance":"800.25"}]}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	equity, available, err := ex.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if equity != 1000.5 || available != 800.25 {
		t.Errorf("GetWalletBalance = (%v, %v), want (1000.5, 800.25)", equity, available)
	}
}

func TestBybitExchange_SetLeverage_DelegatesToREST(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{}}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	req := LeverageRequest{Symbol: "BTCUSDT", Category: "linear", BuyLeverage: "10", SellLeverage: "10"}
	if err := ex.SetLeverage(context.Background(), req); err != nil {
		t.Fatalf("SetLeverage: %v", err)
	}
	if !bytes.Contains(gotBody, []byte(`"buyLeverage":"10"`)) {
		t.Errorf("SetLeverage request body = %s, want buyLeverage=10", gotBody)
	}
}

func TestBybitExchange_SwitchPositionMode_DelegatesToREST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK"}`))
	}))
	defer srv.Close()
	withMockBybitBase(t, srv)

	ex := NewBybitExchange(Credentials{APIKey: "k", SecretKey: "s"}, &fakeWSOrderClient{})
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
}
