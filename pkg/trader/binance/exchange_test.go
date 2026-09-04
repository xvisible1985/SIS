package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	// PositionIdx: 0 (one-way mode) here — reduceOnly is only valid alongside
	// positionSide=BOTH; see TestBinanceExchange_PlaceOrder_OmitsReduceOnlyInHedgeMode
	// for the PositionIdx 1/2 (hedge mode) case, which must NOT send reduceOnly.
	req := trader.OrderRequest{
		Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", TriggerPrice: "58000",
		OrderFilter: "StopOrder", ReduceOnly: true, PositionIdx: 0,
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
	// Binance's orderIdList is integer[] (e.g. [20,21]), validated server-side by a
	// strict character regex before JSON parsing even runs — a quoted-string array
	// like ["20","21"] fails that validation with -1100 "Illegal characters found".
	// Assert the exact unquoted wire format, not just substring presence of "20"/"21"
	// (which can't distinguish [20,21] from ["20","21"]).
	if got := gotQuery.Get("orderIdList"); got != "[20,21]" {
		t.Errorf("orderIdList = %q, want unquoted integers [20,21], not quoted strings", got)
	}
}

func TestBinanceExchange_FetchOpenOrdersForSymbolAll_TranslatesOpenOrders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":30,"clientOrderId":"link-1","symbol":"BTCUSDT","side":"BUY","type":"LIMIT","price":"50000","origQty":"1","executedQty":"0","status":"NEW"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchOpenOrdersForSymbolAll(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("FetchOpenOrdersForSymbolAll: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "30" || got[0].OrderLinkId != "link-1" {
		t.Errorf("FetchOpenOrdersForSymbolAll = %+v, want one order id=30 linkId=link-1", got)
	}
	if got[0].Side != "Buy" {
		t.Errorf("Side = %q, want Buy", got[0].Side)
	}
	if got[0].OrderStatus != "New" {
		t.Errorf("OrderStatus = %q, want New", got[0].OrderStatus)
	}
	if got[0].OrderFilter != "Order" {
		t.Errorf("OrderFilter = %q, want Order (plain LIMIT order)", got[0].OrderFilter)
	}
}

func TestBinanceExchange_FetchOpenOrdersForSymbolAll_NormalizesConditionalStopOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":31,"clientOrderId":"link-2","symbol":"BTCUSDT","side":"SELL","type":"STOP_MARKET","price":"0","stopPrice":"58000","origQty":"1","executedQty":"0","status":"NEW"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchOpenOrdersForSymbolAll(context.Background(), "linear", "BTCUSDT")
	if err != nil {
		t.Fatalf("FetchOpenOrdersForSymbolAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchOpenOrdersForSymbolAll returned %d orders, want 1", len(got))
	}
	o := got[0]
	if o.Side != "Sell" {
		t.Errorf("Side = %q, want Sell (normalized from SELL)", o.Side)
	}
	if o.OrderStatus != "New" {
		t.Errorf("OrderStatus = %q, want New (normalized from NEW)", o.OrderStatus)
	}
	if o.OrderFilter != "StopOrder" {
		t.Errorf("OrderFilter = %q, want StopOrder (conditional order type)", o.OrderFilter)
	}
	if o.TriggerPrice != "58000" {
		t.Errorf("TriggerPrice = %q, want 58000", o.TriggerPrice)
	}
}

func TestBinanceExchange_SetLeverage_SendsSymbolAndLeverage(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"leverage":10,"maxNotionalValue":"1000000","symbol":"BTCUSDT"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.LeverageRequest{Symbol: "BTCUSDT", BuyLeverage: "10", SellLeverage: "10"}
	if err := ex.SetLeverage(context.Background(), req); err != nil {
		t.Fatalf("SetLeverage: %v", err)
	}
	if !strings.Contains(gotBody, "leverage=10") {
		t.Errorf("body = %q, want leverage=10 (Binance has one leverage per symbol, not separate buy/sell — BuyLeverage is used as the single value)", gotBody)
	}
}

func TestBinanceExchange_SwitchPositionMode_SendsDualSidePositionString(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
	if !strings.Contains(gotBody, "dualSidePosition=true") {
		t.Errorf("body = %q, want dualSidePosition=true for mode=3 (hedge)", gotBody)
	}
}

func TestBinanceExchange_SwitchPositionMode_OneWayForModeZero(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 0); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
	if !strings.Contains(gotBody, "dualSidePosition=false") {
		t.Errorf("body = %q, want dualSidePosition=false for mode=0 (one-way)", gotBody)
	}
}

func TestBinanceExchange_FetchClosedPnlForSymbol_GroupsClosingFillsByOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			// Order 100 is a SELL that opens/adds to a SHORT position (positionSide
			// SHORT), not a close — isClosingFill requires a BUY to reduce a SHORT, so
			// combined with its zero realizedPnl it stays excluded. Order 200's fills
			// are SELLs reducing a LONG position (positionSide LONG), so they're
			// classified as closing fills directly via isClosingFill.
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","positionSide":"SHORT","price":"61000","qty":"0.3","realizedPnl":"0","time":1000},
				{"id":2,"orderId":200,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.3","realizedPnl":"180.0","time":2000},
				{"id":3,"orderId":200,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61050","qty":"0.2","realizedPnl":"130.0","time":2100}
			]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":200,"clientOrderId":"SIS_STR-a1b2c3d4-tp-3-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchClosedPnlForSymbol returned %d rows, want 1 (order 100's zero-pnl opening fill must be excluded; order 200's two fills must merge into one row)", len(got))
	}
	row := got[0]
	if row.OrderId != "200" {
		t.Errorf("OrderId = %q, want 200", row.OrderId)
	}
	if row.OrderLinkId != "SIS_STR-a1b2c3d4-tp-3-1" {
		t.Errorf("OrderLinkId = %q, want the looked-up clientOrderId", row.OrderLinkId)
	}
	if row.ClosedPnl != "310" && row.ClosedPnl != "310.0" {
		t.Errorf("ClosedPnl = %q, want 310 (180.0 + 130.0 summed across both fills)", row.ClosedPnl)
	}
	if row.Qty != "0.5" {
		t.Errorf("Qty = %q, want 0.5 (0.3 + 0.2 summed)", row.Qty)
	}
}

func TestBinanceExchange_FetchRecentClosedPnl_WindowsRequestsAt7Days(t *testing.T) {
	var gotStartTimes, gotEndTimes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0.5","entryPrice":"60000","markPrice":"61000","unRealizedProfit":"500","liquidationPrice":"40000","leverage":"10"}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			gotStartTimes = append(gotStartTimes, r.URL.Query().Get("startTime"))
			gotEndTimes = append(gotEndTimes, r.URL.Query().Get("endTime"))
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	since := time.Now().Add(-16 * 24 * time.Hour)
	if _, err := ex.FetchRecentClosedPnl(context.Background(), "linear", since); err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(gotStartTimes) < 3 {
		t.Errorf("FetchRecentClosedPnl issued %d userTrades calls for a 16-day window, want at least 3 (7-day cap per call)", len(gotStartTimes))
	}
	if len(gotEndTimes) != len(gotStartTimes) {
		t.Errorf("got %d endTime params but %d startTime params, want equal counts", len(gotEndTimes), len(gotStartTimes))
	}
}

func TestBinanceExchange_FetchClosedPnlForSymbol_ReturnsPartialResultsOnLookupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.3","realizedPnl":"50.0","time":1000},
				{"id":2,"orderId":200,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.3","realizedPnl":"80.0","time":2000}
			]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order") && r.URL.Query().Get("orderId") == "100":
			_, _ = w.Write([]byte(`{"orderId":100,"clientOrderId":"SIS_STR-a1b2c3d4-tp-1-1"}`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order") && r.URL.Query().Get("orderId") == "200":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-2013,"msg":"Order does not exist."}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err == nil {
		t.Fatal("expected a non-nil error since order 200's lookup fails")
	}
	if len(got) != 1 || got[0].OrderId != "100" {
		t.Errorf("FetchClosedPnlForSymbol = %+v, want the successful order 100's row to still be returned despite order 200's lookup failure", got)
	}
}

func TestBinanceExchange_FetchClosedPnlForSymbol_IncludesBreakevenHedgeModeClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[{"id":1,"orderId":300,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"60000","qty":"0.4","realizedPnl":"0","time":1000}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":300,"clientOrderId":"SIS_STR-a1b2c3d4-sl-1-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchClosedPnlForSymbol returned %d rows, want 1 (a real breakeven close in hedge mode must not be dropped)", len(got))
	}
	if got[0].OrderId != "300" || got[0].ClosedPnl != "0" {
		t.Errorf("got = %+v, want OrderId=300 ClosedPnl=0", got[0])
	}
}

func TestBinanceExchange_PlaceOrder_OmitsReduceOnlyInHedgeMode(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":9,"clientOrderId":"z"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", Qty: "1", ReduceOnly: true, PositionIdx: 1}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if strings.Contains(gotBody, "reduceOnly=") {
		t.Errorf("body = %q, must NOT send reduceOnly in hedge mode (PositionIdx=1) — Binance rejects it with -1106", gotBody)
	}
}

func TestBinanceExchange_PlaceOrder_SendsReduceOnlyInOneWayMode(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":10,"clientOrderId":"z2"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.OrderRequest{Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", Qty: "1", ReduceOnly: true, PositionIdx: 0}
	if _, err := ex.PlaceOrder(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !strings.Contains(gotBody, "reduceOnly=true") {
		t.Errorf("body = %q, want reduceOnly=true in one-way mode (PositionIdx=0)", gotBody)
	}
}

func TestBinanceExchange_PlaceOrderBatch_OmitsReduceOnlyInHedgeMode(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"orderId":20,"clientOrderId":"c"}]`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", Qty: "1", ReduceOnly: true, PositionIdx: 1},
	}}
	if _, err := ex.PlaceOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if strings.Contains(gotBody, "reduceOnly") {
		t.Errorf("body = %q, must NOT include reduceOnly in a hedge-mode (PositionIdx=1) batch item — Binance rejects it with -1106", gotBody)
	}
}
