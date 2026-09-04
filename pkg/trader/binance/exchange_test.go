package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sis/pkg/trader"
)

func TestBinanceExchange_GetMarkPrice_UsesPublicEndpoint(t *testing.T) {
	var gotAPIKeyHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKeyHeader = r.Header.Get("X-MBX-APIKEY")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","markPrice":"67890.50000000"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.GetMarkPrice(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("GetMarkPrice: %v", err)
	}
	if got != 67890.5 {
		t.Errorf("GetMarkPrice = %v, want 67890.5", got)
	}
	if gotAPIKeyHeader != "" {
		t.Error("GetMarkPrice must use the public (unauthenticated) endpoint")
	}
}

func TestBinanceExchange_FetchPositions_TranslatesPositionRisk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"symbol":"BTCUSDT","positionSide":"LONG","positionAmt":"0.50000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"500.0","liquidationPrice":"40000.0","leverage":"10"},
			{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"0.00000000","entryPrice":"0.0","markPrice":"3000.0","unRealizedProfit":"0.0","liquidationPrice":"0.0","leverage":"20"}
		]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	// Only positions with a nonzero positionAmt are real open positions — Binance
	// returns every symbol's row (mostly zeros) unlike Bybit, which only lists real
	// positions. Filtering zero-size rows keeps behavior compatible with Bybit's
	// FetchPositions (used by pkg/strategy to find actually-open positions).
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1 (the zero-size ETHUSDT row must be filtered)", len(got))
	}
	p := got[0]
	if p.Symbol != "BTCUSDT" || p.Side != "Buy" || p.Size != "0.50000000" || p.EntryPrice != "60000.0" {
		t.Errorf("FetchPositions[0] = %+v, want BTCUSDT Buy 0.5 @ 60000.0", p)
	}
	if p.PositionIdx != 1 {
		t.Errorf("PositionIdx = %d, want 1 (LONG hedge-mode slot, matching Bybit's convention)", p.PositionIdx)
	}
	if p.Category != "linear" {
		t.Errorf("Category = %q, want linear", p.Category)
	}
}

func TestBinanceExchange_FetchPositions_ShortSideFromNegativeAmt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"SHORT","positionAmt":"-0.25000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"-250.0","liquidationPrice":"80000.0","leverage":"10"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1", len(got))
	}
	p := got[0]
	// Bybit's Position.Size is always a positive magnitude with Side telling direction —
	// Binance's positionAmt carries the sign instead. Must translate to Bybit's convention
	// since pkg/strategy reads Size as an unsigned magnitude string.
	if p.Side != "Sell" || p.Size != "0.25000000" {
		t.Errorf("FetchPositions[0] = %+v, want Sell with positive size 0.25 (sign stripped)", p)
	}
	if p.PositionIdx != 2 {
		t.Errorf("PositionIdx = %d, want 2 (SHORT hedge-mode slot)", p.PositionIdx)
	}
}

func TestBinanceExchange_FetchPositions_OneWayModeShortGetsPositionIdxZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"-0.30000000","entryPrice":"60000.0","markPrice":"61000.0","unRealizedProfit":"-300.0","liquidationPrice":"90000.0","leverage":"10"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchPositions(context.Background())
	if err != nil {
		t.Fatalf("FetchPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchPositions returned %d positions, want 1", len(got))
	}
	p := got[0]
	// A one-way-mode account still reports positionSide:"BOTH" even for a short
	// (negative positionAmt) — PositionIdx must stay 0 (one-way slot), not fall
	// through to the hedge-mode short slot 2 just because the sign is negative.
	if p.Side != "Sell" || p.PositionIdx != 0 {
		t.Errorf("FetchPositions[0] = %+v, want Sell with PositionIdx=0 (one-way mode uses slot 0 regardless of side)", p)
	}
	if p.Size != "0.30000000" {
		t.Errorf("Size = %q, want 0.30000000", p.Size)
	}
}

func TestBinanceExchange_GetWalletBalance_TranslatesAccountEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"1000.5","assets":[{"asset":"USDT","availableBalance":"800.25"},{"asset":"BUSD","availableBalance":"5.0"}]}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	equity, available, err := ex.GetWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("GetWalletBalance: %v", err)
	}
	if equity != 1000.5 {
		t.Errorf("equity = %v, want 1000.5 (totalMarginBalance)", equity)
	}
	if available != 800.25 {
		t.Errorf("available = %v, want 800.25 (USDT asset's availableBalance, not BUSD)", available)
	}
}

func TestBinanceExchange_PlaceOrder_TranslatesMarketOrder(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":123456789,"clientOrderId":"SIS_STR-a1b2c3d4-tp-1-1","status":"NEW"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "0.01",
		PositionIdx: 1, OrderLinkId: "SIS_STR-a1b2c3d4-tp-1-1",
	}
	got, err := ex.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderId != "123456789" {
		t.Errorf("OrderId = %q, want %q (numeric orderId stringified)", got.OrderId, "123456789")
	}
	if got.OrderLinkId != "SIS_STR-a1b2c3d4-tp-1-1" {
		t.Errorf("OrderLinkId = %q, want the echoed clientOrderId", got.OrderLinkId)
	}
	if !strings.Contains(gotBody, "side=BUY") {
		t.Errorf("body = %q, want side=BUY (Binance uppercase)", gotBody)
	}
	if !strings.Contains(gotBody, "type=MARKET") {
		t.Errorf("body = %q, want type=MARKET", gotBody)
	}
	if !strings.Contains(gotBody, "positionSide=LONG") {
		t.Errorf("body = %q, want positionSide=LONG (PositionIdx=1 → LONG)", gotBody)
	}
	if !strings.Contains(gotBody, "newClientOrderId=SIS_STR-a1b2c3d4-tp-1-1") {
		t.Errorf("body = %q, want the orderLinkId passed through as newClientOrderId", gotBody)
	}
}

func TestBinanceExchange_PlaceOrder_TranslatesConditionalStopOrder(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":2,"clientOrderId":"x"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000",
		OrderFilter: "StopOrder", ReduceOnly: true, PositionIdx: 1,
	}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !strings.Contains(gotBody, "type=STOP_MARKET") {
		t.Errorf("body = %q, want type=STOP_MARKET for a conditional stop order", gotBody)
	}
	if !strings.Contains(gotBody, "stopPrice=58000") {
		t.Errorf("body = %q, want stopPrice=58000", gotBody)
	}
	if !strings.Contains(gotBody, "reduceOnly=true") {
		t.Errorf("body = %q, want reduceOnly=true", gotBody)
	}
}

func TestBinanceExchange_PlaceOrder_TruncatesLongOrderLinkId(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":3,"clientOrderId":"whatever"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	longID := "SIS_STR-a1b2c3d4-999999-88888888-777-v99"
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "1", OrderLinkId: longID}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if strings.Contains(gotBody, "newClientOrderId="+longID) {
		t.Errorf("body = %q, must NOT send the untruncated >36-char orderLinkId — Binance would reject it", gotBody)
	}
	want := truncateClientOrderID(longID)
	if !strings.Contains(gotBody, "newClientOrderId="+want) {
		t.Errorf("body = %q, want the truncated id %q", gotBody, want)
	}
}

func TestBinanceExchange_PlaceOrder_MapsTriggerByMarkPriceToWorkingType(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":8,"clientOrderId":"z"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000",
		TriggerBy: "MarkPrice", OrderFilter: "StopOrder", PositionIdx: 1,
	}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !strings.Contains(gotBody, "workingType=MARK_PRICE") {
		t.Errorf("body = %q, want workingType=MARK_PRICE when TriggerBy=MarkPrice", gotBody)
	}
}

func TestBinanceExchange_PlaceOrderREST_SameBehaviorAsPlaceOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":4,"clientOrderId":"y"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Market", Qty: "1"}
	got, err := ex.PlaceOrderREST(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderREST: %v", err)
	}
	if got.OrderId != "4" {
		t.Errorf("OrderId = %q, want 4", got.OrderId)
	}
}

func TestBinanceExchange_CancelOrder_UsesOrderIdWhenPresent(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":5,"status":"CANCELED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelOrder(context.Background(), trader.CancelRequest{Symbol: "BTCUSDT", OrderId: "5"}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if gotQuery.Get("orderId") != "5" {
		t.Errorf("query orderId = %q, want 5", gotQuery.Get("orderId"))
	}
}

func TestBinanceExchange_CancelOrder_UsesOrigClientOrderIdWhenNoOrderId(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":7,"status":"CANCELED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.CancelRequest{Symbol: "BTCUSDT", OrderLinkId: "SIS_STR-a1b2c3d4-tp-1-1"}
	if err := ex.CancelOrder(context.Background(), req); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if gotQuery.Get("orderId") != "" {
		t.Errorf("query orderId = %q, want empty (no OrderId given, should use origClientOrderId instead)", gotQuery.Get("orderId"))
	}
	if gotQuery.Get("origClientOrderId") != "SIS_STR-a1b2c3d4-tp-1-1" {
		t.Errorf("query origClientOrderId = %q, want SIS_STR-a1b2c3d4-tp-1-1", gotQuery.Get("origClientOrderId"))
	}
}

func TestBinanceExchange_CancelOrderREST_SameBehaviorAsCancelOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":6,"status":"CANCELED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelOrderREST(context.Background(), trader.CancelRequest{Symbol: "BTCUSDT", OrderId: "6"}); err != nil {
		t.Fatalf("CancelOrderREST: %v", err)
	}
}

func TestBinanceExchange_CancelAllOrders_DelegatesCorrectly(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"The operation of cancel all open order is done."}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.CancelAllOrders(context.Background(), trader.CancelAllRequest{Symbol: "BTCUSDT"}); err != nil {
		t.Fatalf("CancelAllOrders: %v", err)
	}
	if gotQuery.Get("symbol") != "BTCUSDT" {
		t.Errorf("query symbol = %q, want BTCUSDT", gotQuery.Get("symbol"))
	}
}

func TestBinanceExchange_PlaceOrderBatch_SendsJSONArrayParam(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":10,"clientOrderId":"a"},{"orderId":11,"clientOrderId":"b"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "50000", PositionIdx: 1},
		{Symbol: "ETHUSDT", Side: "Sell", OrderType: "Market", Qty: "1", PositionIdx: 2},
	}}
	got, err := ex.PlaceOrderBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 || got[0].OrderId != "10" || got[1].OrderId != "11" {
		t.Errorf("PlaceOrderBatch = %+v, want orderIds 10 and 11", got)
	}
	if !strings.Contains(gotBody, "batchOrders=") {
		t.Errorf("body = %q, want a batchOrders param", gotBody)
	}
	if !strings.Contains(gotBody, "BTCUSDT") || !strings.Contains(gotBody, "ETHUSDT") {
		t.Errorf("body = %q, want both symbols present in the encoded batch", gotBody)
	}
}

func TestBinanceExchange_CancelOrderBatch_SendsOrderIdListParam(t *testing.T) {
	// Unlike PlaceOrderBatch (POST, body-encoded), CancelOrderBatch goes through
	// doSignedDELETE which — like every other signed DELETE call in this package
	// (CancelOrder, CancelAllOrders) — sends params via the query string, not the
	// request body. So this asserts on r.URL.Query()/RawQuery, matching the
	// TestBinanceExchange_CancelOrder_* and TestBinanceExchange_CancelAllOrders_*
	// tests above, rather than reading an (always-empty, for a query-based DELETE) body.
	var gotQuery url.Values
	var gotRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":20,"status":"CANCELED"},{"orderId":21,"status":"CANCELED"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchCancelRequest{Request: []trader.BatchCancelItem{
		{Symbol: "BTCUSDT", OrderId: "20"},
		{Symbol: "BTCUSDT", OrderId: "21"},
	}}
	if err := ex.CancelOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	if gotQuery.Get("orderIdList") == "" {
		t.Errorf("query = %q, want a nonempty orderIdList param", gotRawQuery)
	}
	if !strings.Contains(gotRawQuery, "20") || !strings.Contains(gotRawQuery, "21") {
		t.Errorf("query = %q, want both order IDs present", gotRawQuery)
	}
}
