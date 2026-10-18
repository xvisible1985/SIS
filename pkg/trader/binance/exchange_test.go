package binance

import (
	"context"
	"errors"
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

// Regression (2026-10-18): a live order on a TradFi-Perps symbol (Binance's newer
// stock/commodity-linked perpetual product, e.g. XAUUSDT/XAGUSDT) failed with
// "-4411: Please sign TradFi-Perps agreement contract fapi." — the account hadn't yet
// signed that one-time agreement. This pins that placeOrder signs it automatically
// (POST /fapi/v1/stock/contract) and retries the SAME order exactly once, succeeding
// without the caller ever seeing the -4411 at all.
func TestBinanceExchange_PlaceOrder_SignsTradFiPerpsAgreementAndRetriesOnce(t *testing.T) {
	var orderAttempts, agreementCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/stock/contract":
			agreementCalls++
			_, _ = w.Write([]byte(`{}`))
		case "/fapi/v1/order":
			orderAttempts++
			if orderAttempts == 1 {
				_, _ = w.Write([]byte(`{"code":-4411,"msg":"Please sign TradFi-Perps agreement contract fapi."}`))
				return
			}
			_, _ = w.Write([]byte(`{"orderId":1,"clientOrderId":"SIS_TRM-x"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	// A unique APIKey (not testCreds()'s shared "test-key") + cleanup keeps this test's
	// use of the package-level binanceAgreementSignedCache isolated from every other
	// test in this file — without this, whichever test signs testCreds()'s key FIRST
	// would make every later test's mock server see zero /fapi/v1/stock/contract calls.
	creds := testCreds()
	creds.APIKey = "tradfi-retry-" + t.Name()
	t.Cleanup(func() { binanceAgreementSignedCache.Delete(creds.APIKey) })

	ex := NewBinanceExchange(creds)
	got, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "XAUUSDT", Side: "Buy", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderId != "1" {
		t.Errorf("got %+v, want OrderId=1 (the retried order succeeding)", got)
	}
	if agreementCalls != 1 {
		t.Errorf("agreement signed %d times, want 1", agreementCalls)
	}
	if orderAttempts != 2 {
		t.Errorf("order attempted %d times, want 2 (fail then retry)", orderAttempts)
	}
}

// Second call on the same account must NOT re-sign — binanceAgreementSignedCache
// avoids paying the extra REST round-trip once an account is known-signed.
func TestBinanceExchange_PlaceOrder_DoesNotReSignAgreementOnSubsequentOrders(t *testing.T) {
	var agreementCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/stock/contract":
			agreementCalls++
			_, _ = w.Write([]byte(`{}`))
		case "/fapi/v1/order":
			_, _ = w.Write([]byte(`{"orderId":1,"clientOrderId":"x"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	creds := testCreds()
	creds.APIKey = "already-signed-test-key-" + t.Name()
	binanceAgreementSignedCache.Store(creds.APIKey, struct{}{})
	t.Cleanup(func() { binanceAgreementSignedCache.Delete(creds.APIKey) })

	ex := NewBinanceExchange(creds)
	if _, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "XAUUSDT", Side: "Buy", OrderType: "Market", Qty: "1"}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if agreementCalls != 0 {
		t.Errorf("agreement signed %d times, want 0 (already cached as signed)", agreementCalls)
	}
}

func TestBinanceExchange_PlaceOrderBatch_SignsTradFiPerpsAgreementAndRetriesOnce(t *testing.T) {
	var batchAttempts, agreementCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/stock/contract":
			agreementCalls++
			_, _ = w.Write([]byte(`{}`))
		case "/fapi/v1/batchOrders":
			batchAttempts++
			if batchAttempts == 1 {
				_, _ = w.Write([]byte(`[{"code":-4411,"msg":"Please sign TradFi-Perps agreement contract fapi."}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"orderId":1,"clientOrderId":"x","code":0}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	creds := testCreds()
	creds.APIKey = "tradfi-retry-" + t.Name()
	t.Cleanup(func() { binanceAgreementSignedCache.Delete(creds.APIKey) })

	ex := NewBinanceExchange(creds)
	req := trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "XAUUSDT", Side: "Buy", OrderType: "Market", Qty: "1"},
	}}
	got, err := ex.placeOrderBatchREST(context.Background(), req)
	if err != nil {
		t.Fatalf("placeOrderBatchREST: %v", err)
	}
	if len(got) != 1 || got[0].OrderId != "1" || got[0].Code != 0 {
		t.Fatalf("got %+v, want one succeeding row after the agreement retry", got)
	}
	if agreementCalls != 1 {
		t.Errorf("agreement signed %d times, want 1", agreementCalls)
	}
	if batchAttempts != 2 {
		t.Errorf("batch attempted %d times, want 2 (fail then retry)", batchAttempts)
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

// TestBinanceExchange_PlaceOrder_TranslatesConditionalStopOrder also locks in the
// 2025-12-09 Algo Order migration (Plan #4d, found live 2026-10-16 as a batch-placement
// "-4120 Order type not supported for this endpoint" error): a conditional order must
// go to POST /fapi/v1/algoOrder with algoType=CONDITIONAL and triggerPrice (not the old
// /fapi/v1/order with stopPrice), or Binance rejects it outright.
func TestBinanceExchange_PlaceOrder_TranslatesConditionalStopOrder(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"algoId":2,"clientAlgoId":"x"}`))
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
	got, err := ex.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if gotPath != "/fapi/v1/algoOrder" {
		t.Errorf("path = %q, want /fapi/v1/algoOrder", gotPath)
	}
	if !strings.Contains(gotBody, "algoType=CONDITIONAL") {
		t.Errorf("body = %q, want algoType=CONDITIONAL", gotBody)
	}
	if !strings.Contains(gotBody, "type=STOP_MARKET") {
		t.Errorf("body = %q, want type=STOP_MARKET for a conditional stop order", gotBody)
	}
	if !strings.Contains(gotBody, "triggerPrice=58000") {
		t.Errorf("body = %q, want triggerPrice=58000 (NOT stopPrice — the Algo Order API's own field name)", gotBody)
	}
	if !strings.Contains(gotBody, "reduceOnly=true") {
		t.Errorf("body = %q, want reduceOnly=true", gotBody)
	}
	if got.OrderId != "2" || got.OrderLinkId != "x" {
		t.Errorf("got %+v, want OrderId=2 (from algoId) OrderLinkId=x (from clientAlgoId)", got)
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

// TestBinanceExchange_PlaceOrderBatch_MixedConditionalAndRegularItems is the regression
// test for the live bug (2026-10-16): a grid/matrix level batch containing a
// stop_market entry (a conditional order) alongside plain limit/market levels was sent
// entirely through /fapi/v1/batchOrders, which Binance's 2025-12-09 Algo Order
// migration rejects outright for ANY conditional item with "-4120 Order type not
// supported for this endpoint" — failing the WHOLE batch, not just that one item (the
// live symptom: "Batch L3: [-4120] ..."). The conditional item must be split out and
// placed individually via /fapi/v1/algoOrder, with its result still landing at its
// ORIGINAL index in the returned slice — callers (cycle.go) index results[j] against
// their own req.Request[j].
func TestBinanceExchange_PlaceOrderBatch_MixedConditionalAndRegularItems(t *testing.T) {
	var batchBody string
	var algoCalls int
	var algoBody url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/algoOrder":
			algoCalls++
			_ = r.ParseForm()
			algoBody = r.Form
			_, _ = w.Write([]byte(`{"algoId":99,"clientAlgoId":"stop-leg"}`))
		case "/fapi/v1/batchOrders":
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			batchBody = string(buf)
			_, _ = w.Write([]byte(`[{"orderId":10,"clientOrderId":"limit-leg"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchPlaceRequest{Request: []trader.BatchOrderItem{
		{Symbol: "BTCUSDT", Side: "Buy", OrderType: "Limit", Qty: "1", Price: "50000", PositionIdx: 1, OrderLinkId: "limit-leg"},
		{Symbol: "BTCUSDT", Side: "Sell", OrderType: "Market", Qty: "1", TriggerPrice: "48000", OrderFilter: "StopOrder", PositionIdx: 1, OrderLinkId: "stop-leg"}, // "L3"
	}}
	got, err := ex.PlaceOrderBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("PlaceOrderBatch returned %d results, want 2 (one per request item)", len(got))
	}
	// Index 0 (the LIMIT item) must come from the regular batch endpoint.
	if got[0].OrderId != "10" || got[0].OrderLinkId != "limit-leg" {
		t.Errorf("results[0] = %+v, want the LIMIT item's batchOrders result (id=10)", got[0])
	}
	// Index 1 (the STOP_MARKET item, "L3") must come from the individual algoOrder call —
	// landing at ITS OWN original index, not appended after/instead of index 0's result.
	if got[1].OrderId != "99" || got[1].OrderLinkId != "stop-leg" {
		t.Errorf("results[1] = %+v, want the STOP_MARKET item's algoOrder result (id=99) at its own index", got[1])
	}
	if algoCalls != 1 {
		t.Errorf("algoOrder calls = %d, want exactly 1 (one per conditional item — no batch algo endpoint exists)", algoCalls)
	}
	if algoBody.Get("algoType") != "CONDITIONAL" || algoBody.Get("type") != "STOP_MARKET" {
		t.Errorf("algo call body = %v, want algoType=CONDITIONAL type=STOP_MARKET", algoBody)
	}
	if strings.Contains(batchBody, "STOP_MARKET") {
		t.Errorf("batchOrders body = %q, must NOT contain the conditional item at all", batchBody)
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

// TestBinanceExchange_CancelOrderBatch_MixedConditionalAndRegularItems is
// CancelOrderBatch's counterpart to PlaceOrderBatch's mixed-batch regression test
// above: a conditional item (OrderFilter=="StopOrder") must be cancelled individually
// via DELETE /fapi/v1/algoOrder (algoId, not orderId), not swept into the regular
// /fapi/v1/batchOrders DELETE call, which Binance's Algo Order migration doesn't
// recognize algoIds through at all.
func TestBinanceExchange_CancelOrderBatch_MixedConditionalAndRegularItems(t *testing.T) {
	var batchOrderIdList string
	var algoCalls int
	var algoQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/algoOrder":
			algoCalls++
			algoQuery = r.URL.Query()
			_, _ = w.Write([]byte(`{"algoId":99}`))
		case "/fapi/v1/batchOrders":
			batchOrderIdList = r.URL.Query().Get("orderIdList")
			_, _ = w.Write([]byte(`[{"orderId":20,"status":"CANCELED"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	req := trader.BatchCancelRequest{Request: []trader.BatchCancelItem{
		{Symbol: "BTCUSDT", OrderId: "20"},
		{Symbol: "BTCUSDT", OrderId: "99", OrderFilter: "StopOrder"},
	}}
	if err := ex.CancelOrderBatch(context.Background(), req); err != nil {
		t.Fatalf("CancelOrderBatch: %v", err)
	}
	if batchOrderIdList != "[20]" {
		t.Errorf("batchOrders orderIdList = %q, want [20] (the conditional item's 99 must NOT be in it)", batchOrderIdList)
	}
	if algoCalls != 1 {
		t.Fatalf("algoOrder DELETE calls = %d, want exactly 1", algoCalls)
	}
	if algoQuery.Get("algoId") != "99" {
		t.Errorf("algoOrder DELETE algoId = %q, want 99", algoQuery.Get("algoId"))
	}
}

func TestBinanceExchange_FetchOpenOrdersForSymbolAll_TranslatesOpenOrders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/openAlgoOrders":
			_, _ = w.Write([]byte(`[]`)) // no open conditional orders for this test
		default:
			_, _ = w.Write([]byte(`[{"orderId":30,"clientOrderId":"link-1","symbol":"BTCUSDT","side":"BUY","type":"LIMIT","price":"50000","origQty":"1","executedQty":"0","status":"NEW"}]`))
		}
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

// TestBinanceExchange_FetchOpenOrdersForSymbolAll_NormalizesConditionalStopOrder also
// locks in the 2025-12-09 Algo Order migration: a conditional order now comes from
// GET /fapi/v1/openAlgoOrders (a completely separate order book), never from
// /fapi/v1/openOrders — mocked here returning empty, to catch a regression that went
// back to expecting it there.
func TestBinanceExchange_FetchOpenOrdersForSymbolAll_NormalizesConditionalStopOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/openAlgoOrders":
			_, _ = w.Write([]byte(`[{"algoId":31,"clientAlgoId":"link-2","symbol":"BTCUSDT","side":"SELL","orderType":"STOP_MARKET","price":"0","triggerPrice":"58000","quantity":"1"}]`))
		default:
			_, _ = w.Write([]byte(`[]`)) // conditional orders never appear here post-migration
		}
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
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			// Reports the opposite of what's requested below, so the switch is a real
			// change and the POST this test actually cares about still happens — see
			// TestBinanceExchange_SwitchPositionMode_SkipsPOSTWhenAlreadyInRequestedMode
			// for the GET-already-matches case this test is not about.
			_, _ = w.Write([]byte(`{"dualSidePosition":false}`))
		case http.MethodPost:
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			gotBody = string(buf)
			_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		}
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
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			// Opposite of what's requested below — see the comment in
			// TestBinanceExchange_SwitchPositionMode_SendsDualSidePositionString above.
			_, _ = w.Write([]byte(`{"dualSidePosition":true}`))
		case http.MethodPost:
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			gotBody = string(buf)
			_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		}
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

// TestBinanceExchange_SwitchPositionMode_AlreadyInRequestedModeIsSuccess is the
// regression test for the live bug (2026-10-16): Binance rejects a no-op
// dualSidePosition switch with code -4059 "No need to change position side" — unlike
// Bybit, whose own SwitchPositionMode (pkg/trader/bybit.go) explicitly treats the
// equivalent "already in this mode" response as success. Without the same treatment
// here, pkg/strategy's ensurePositionMode (called at the start of every cycle) stopped
// every single Binance strategy the moment its account's position mode was already
// correctly configured — the common case for a setting meant to stay fixed once set.
func TestBinanceExchange_SwitchPositionMode_AlreadyInRequestedModeIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-4059,"msg":"No need to change position side."}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Errorf("SwitchPositionMode = %v, want nil (-4059 means the account is already in the requested mode)", err)
	}
}

// TestBinanceExchange_SwitchPositionMode_OtherErrorsStillPropagate guards against the
// -4059 special-case above swallowing every error indiscriminately — a genuinely
// different failure (e.g. an open position blocking the mode switch) must still
// surface to the caller.
func TestBinanceExchange_SwitchPositionMode_OtherErrorsStillPropagate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-4068,"msg":"Position side cannot be changed if there exists position."}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err == nil {
		t.Error("SwitchPositionMode = nil, want an error for a genuine failure (open position blocking the switch)")
	}
}

// TestBinanceExchange_SwitchPositionMode_SkipsPOSTWhenAlreadyInRequestedMode is the
// regression test for the live bug (2026-10-18, strategy "1000TURBOUSDT", acc N2
// Binance): Binance's POST /fapi/v1/positionSide/dual rejects with -4067 "Position side
// cannot be changed if there exists open orders" whenever the account has ANY open
// order on ANY symbol — Binance checks this before even checking whether the requested
// value is a no-op, so it fires even when the account is ALREADY in the requested mode
// (the -4059 case above only fires when there are no open orders to begin with). On an
// actively-trading account (the normal case — some other strategy almost always has an
// order open), ensurePositionMode's unconditional POST on every new strategy's first
// cycle (and after every process restart, which clears the in-memory verified-cache)
// made every such Binance strategy fail to start. Fixed by checking the account's
// CURRENT mode via GET first and only POSTing when a real change is needed.
func TestBinanceExchange_SwitchPositionMode_SkipsPOSTWhenAlreadyInRequestedMode(t *testing.T) {
	var gotPOST bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"dualSidePosition":true}`))
		case http.MethodPost:
			gotPOST = true
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-4067,"msg":"Position side cannot be changed if there exists open orders."}`))
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	// Requesting hedge (mode=3) while the account is already reported as hedge
	// (dualSidePosition=true) must succeed without ever POSTing — if it did POST, the
	// mocked -4067 response proves the account's open orders would have blocked it.
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Errorf("SwitchPositionMode = %v, want nil (GET already reports the requested mode)", err)
	}
	if gotPOST {
		t.Error("POST /fapi/v1/positionSide/dual was called despite the account already being in the requested mode — must check GET first")
	}
}

// TestBinanceExchange_SwitchPositionMode_POSTsWhenModeActuallyDiffers is the positive
// counterpart: when the GET genuinely reports a different mode, the POST must still
// happen (so a real, intentional switch — e.g. first-time setup with no open orders
// yet — keeps working).
func TestBinanceExchange_SwitchPositionMode_POSTsWhenModeActuallyDiffers(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"dualSidePosition":false}`))
		case http.MethodPost:
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			gotBody = string(buf)
			_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Fatalf("SwitchPositionMode: %v", err)
	}
	if !strings.Contains(gotBody, "dualSidePosition=true") {
		t.Errorf("body = %q, want dualSidePosition=true — GET reported one-way, so the POST switch must still happen", gotBody)
	}
}

// TestBinanceExchange_SwitchPositionMode_FallsBackToPOSTWhenGETFails ensures a GET
// failure (network error, unparseable body, whatever) doesn't block the switch outright
// — it must fall through to the POST exactly as before this fix, not newly introduce a
// failure mode of its own.
func TestBinanceExchange_SwitchPositionMode_FallsBackToPOSTWhenGETFails(t *testing.T) {
	var gotPOST bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":-1000,"msg":"An unknown error occurred."}`))
		case http.MethodPost:
			gotPOST = true
			_, _ = w.Write([]byte(`{"code":200,"msg":"success"}`))
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if err := ex.SwitchPositionMode(context.Background(), "linear", "BTCUSDT", 3); err != nil {
		t.Errorf("SwitchPositionMode = %v, want nil (GET failure must fall back to POST, not fail outright)", err)
	}
	if !gotPOST {
		t.Error("POST was never attempted after the GET failed — GET failure must fall back to POST")
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

// TestBinanceExchange_FetchClosedPnlForSymbol_ReconstructsEntryPriceFromOpeningFills is
// the regression test for the live bug (2026-10-16): AvgEntryPrice/CumEntryValue/
// CumExitValue used to always be left at zero for every Binance-sourced row, so the
// terminal's PnL tab showed no volume, no entry price, and a nonsensical fee figure
// (derived from entry/exit prices) for every Binance trade. The two opening fills here
// (two different prices, VWAP-ing the entry) are in the SAME response as the closing
// fill — closingFillsToClosedPnl must walk them to reconstruct the position's cost
// basis before the close, not just report the closing fill's own price.
func TestBinanceExchange_FetchClosedPnlForSymbol_ReconstructsEntryPriceFromOpeningFills(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":10,"symbol":"BTCUSDT","side":"BUY","positionSide":"LONG","price":"60000","qty":"0.3","realizedPnl":"0","time":1000},
				{"id":2,"orderId":11,"symbol":"BTCUSDT","side":"BUY","positionSide":"LONG","price":"61000","qty":"0.2","realizedPnl":"0","time":1500},
				{"id":3,"orderId":20,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"62000","qty":"0.5","realizedPnl":"800.0","time":2000}
			]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":20,"clientOrderId":"SIS_TRM-1"}`))
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
		t.Fatalf("FetchClosedPnlForSymbol returned %d rows, want 1 (the two opening fills must not become rows of their own)", len(got))
	}
	row := got[0]
	// VWAP of the two opening fills: (60000*0.3 + 61000*0.2) / 0.5 = 60400.
	if row.AvgEntryPrice != "60400" {
		t.Errorf("AvgEntryPrice = %q, want 60400 (qty-weighted VWAP of the 0.3@60000 and 0.2@61000 opening fills)", row.AvgEntryPrice)
	}
	if row.AvgExitPrice != "62000" {
		t.Errorf("AvgExitPrice = %q, want 62000 (the single closing fill's price)", row.AvgExitPrice)
	}
	if row.CumEntryValue != "30200" {
		t.Errorf("CumEntryValue = %q, want 30200 (60400 × 0.5)", row.CumEntryValue)
	}
	if row.CumExitValue != "31000" {
		t.Errorf("CumExitValue = %q, want 31000 (62000 × 0.5)", row.CumExitValue)
	}
}

// TestBinanceExchange_FetchClosedPnlForSymbol_SumsRealCommissionAcrossFills is the
// regression test for the live bug (2026-10-16): the terminal's PnL tab derived "fee"
// from gross(entry/exit prices) − closedPnl — sound for Bybit (whose entry price is
// exchange-exact) but wildly wrong for Binance whenever the reconstructed entry price
// was imprecise (a real SUPERUSDT close showed ~94 USDT "fee" on a ~719 USDT trade).
// Fee must instead be the exchange's own userTrades `commission` field, summed across
// every fill of the closing order — negative-signed commission values (real Binance
// convention on some endpoints) must not cancel out when a multi-fill close sums them.
func TestBinanceExchange_FetchClosedPnlForSymbol_SumsRealCommissionAcrossFills(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":30,"symbol":"SUPERUSDT","side":"BUY","positionSide":"SHORT","price":"0.1300","qty":"2000","realizedPnl":"20.0","commission":"-0.1200","time":1000},
				{"id":2,"orderId":30,"symbol":"SUPERUSDT","side":"BUY","positionSide":"SHORT","price":"0.1310","qty":"1000","realizedPnl":"10.0","commission":"-0.0600","time":1100}
			]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":30,"clientOrderId":"SIS_TRM-2"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "SUPERUSDT", 10)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchClosedPnlForSymbol returned %d rows, want 1", len(got))
	}
	// 0.1200 + 0.0600 = 0.18 — both negative-signed commissions must add up as costs,
	// not cancel toward zero.
	if got[0].Fee != "0.18" {
		t.Errorf("Fee = %q, want 0.18 (real commission summed across both fills, sign-normalized)", got[0].Fee)
	}
}

func TestBinanceExchange_FetchRecentClosedPnl_WindowsRequestsAt7Days(t *testing.T) {
	var gotStartTimes, gotEndTimes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0.5","entryPrice":"60000","markPrice":"61000","unRealizedProfit":"500","liquidationPrice":"40000","leverage":"10"}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/income"):
			_, _ = w.Write([]byte(`[]`))
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

// TestBinanceExchange_FetchRecentClosedPnl_FindsSymbolWithNoOpenPosition is the
// regression test for the live bug (2026-10-16): a symbol that closed fully to flat —
// completely absent from FetchPositions, which only ever lists symbols still open — was
// never queried for userTrades at all, so a completed Binance trade could never be
// found and never appeared in the P&L table, no matter how long the syncer kept
// retrying. The account has ZERO open positions here; the only way DOGEUSDT's closing
// fill is found at all is via /fapi/v1/income's account-wide REALIZED_PNL discovery.
func TestBinanceExchange_FetchRecentClosedPnl_FindsSymbolWithNoOpenPosition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[]`)) // no open positions at all
		case strings.Contains(r.URL.Path, "/fapi/v1/income"):
			_, _ = w.Write([]byte(`[{"symbol":"DOGEUSDT","incomeType":"REALIZED_PNL","income":"12.5","time":1500}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			if r.URL.Query().Get("symbol") != "DOGEUSDT" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"orderId":900,"symbol":"DOGEUSDT","side":"SELL","positionSide":"LONG","price":"0.12","qty":"100","realizedPnl":"12.5","time":1500}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":900,"clientOrderId":"SIS_TRM-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	since := time.Now().Add(-time.Hour)
	got, err := ex.FetchRecentClosedPnl(context.Background(), "linear", since)
	if err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FetchRecentClosedPnl returned %d rows, want 1 (DOGEUSDT's close, discovered via /fapi/v1/income despite no open position)", len(got))
	}
	if got[0].Symbol != "DOGEUSDT" || got[0].OrderId != "900" {
		t.Errorf("got %+v, want DOGEUSDT order 900", got[0])
	}
}

// Regression (2026-10-17): FetchRecentClosedPnl used to append rows in window-processing
// order (oldest window first) with no final sort, so the P&L tab showed oldest-first —
// the opposite of Bybit's own convention, which the frontend relies on verbatim (no
// client-side sort). This fixture spans two symbols with very different close times to
// also catch Go's randomized map-iteration order (which symbol gets fetched "first"
// within a window) accidentally determining the result instead of actual close time.
func TestBinanceExchange_FetchRecentClosedPnl_ReturnsNewestFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/income"):
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","incomeType":"REALIZED_PNL","income":"5","time":1000},{"symbol":"ETHUSDT","incomeType":"REALIZED_PNL","income":"5","time":9000}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			switch r.URL.Query().Get("symbol") {
			case "BTCUSDT":
				// Closes at time=1000 — the OLDER of the two.
				_, _ = w.Write([]byte(`[{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.1","realizedPnl":"5","time":1000}]`))
			case "ETHUSDT":
				// Closes at time=9000 — the NEWER of the two.
				_, _ = w.Write([]byte(`[{"id":2,"orderId":200,"symbol":"ETHUSDT","side":"SELL","positionSide":"LONG","price":"3000","qty":"1","realizedPnl":"5","time":9000}]`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			_, _ = w.Write([]byte(`{"orderId":1,"clientOrderId":"x"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchRecentClosedPnl(context.Background(), "linear", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FetchRecentClosedPnl: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].Symbol != "ETHUSDT" || got[1].Symbol != "BTCUSDT" {
		t.Errorf("order = [%s, %s], want [ETHUSDT, BTCUSDT] (newest close first)", got[0].Symbol, got[1].Symbol)
	}
}

// Regression (2026-10-17): closingFillsToClosedPnl always paid one REST round-trip per
// distinct closing order to resolve clientOrderId, even when the caller (the P&L tab
// handler) already has the same mapping cheaply available from our own DB. This test
// pins that a resolver returning found=true is used instead of hitting /fapi/v1/order at
// all, and that a resolver miss (found=false) still falls back to REST — never silently
// drops the row or returns a wrong/empty link id.
func TestBinanceExchange_FetchClosedPnlForSymbol_UsesOrderLinkIDResolverBeforeREST(t *testing.T) {
	var hitOrderEndpoint int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.1","realizedPnl":"5","time":1000}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			hitOrderEndpoint++
			_, _ = w.Write([]byte(`{"orderId":100,"clientOrderId":"REST-FALLBACK"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.SetOrderLinkIDResolver(func(ctx context.Context, symbol string, orderId int64) (string, bool) {
		if orderId == 100 {
			return "FROM-DB", true
		}
		return "", false
	})
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 50)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 || got[0].OrderLinkId != "FROM-DB" {
		t.Fatalf("got %+v, want one row with OrderLinkId=FROM-DB", got)
	}
	if hitOrderEndpoint != 0 {
		t.Errorf("hit /fapi/v1/order %d times, want 0 — a resolver hit must skip REST entirely", hitOrderEndpoint)
	}
}

func TestBinanceExchange_FetchClosedPnlForSymbol_FallsBackToRESTWhenResolverMisses(t *testing.T) {
	var hitOrderEndpoint int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			_, _ = w.Write([]byte(`[{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"SELL","positionSide":"LONG","price":"61000","qty":"0.1","realizedPnl":"5","time":1000}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/order"):
			hitOrderEndpoint++
			_, _ = w.Write([]byte(`{"orderId":100,"clientOrderId":"REST-FALLBACK"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.SetOrderLinkIDResolver(func(ctx context.Context, symbol string, orderId int64) (string, bool) {
		return "", false // never found — e.g. an order outside the DB's preloaded window
	})
	got, err := ex.FetchClosedPnlForSymbol(context.Background(), "linear", "BTCUSDT", 50)
	if err != nil {
		t.Fatalf("FetchClosedPnlForSymbol: %v", err)
	}
	if len(got) != 1 || got[0].OrderLinkId != "REST-FALLBACK" {
		t.Fatalf("got %+v, want one row with OrderLinkId=REST-FALLBACK (resolver miss must still fall back to REST)", got)
	}
	if hitOrderEndpoint != 1 {
		t.Errorf("hit /fapi/v1/order %d times, want 1", hitOrderEndpoint)
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

func TestBinanceExchange_FetchOrderByLinkId_FindsOrderByOrigClientOrderId(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":555,"clientOrderId":"SIS_STR-abc12345-tp-1-1","symbol":"BTCUSDT","side":"SELL","type":"LIMIT","price":"61000","avgPrice":"61000","origQty":"0.5","executedQty":"0.5","status":"FILLED"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	order, stillOpen, err := ex.FetchOrderByLinkId(context.Background(), "linear", "BTCUSDT", "SIS_STR-abc12345-tp-1-1")
	if err != nil {
		t.Fatalf("FetchOrderByLinkId: %v", err)
	}
	if gotQuery.Get("origClientOrderId") != "SIS_STR-abc12345-tp-1-1" {
		t.Errorf("query origClientOrderId = %q, want our linkId", gotQuery.Get("origClientOrderId"))
	}
	if order.OrderId != "555" || order.OrderStatus != "Filled" {
		t.Errorf("order = %+v, want OrderId=555 OrderStatus=Filled", order)
	}
	if stillOpen {
		t.Error("stillOpen = true, want false for a FILLED order")
	}
}

func TestBinanceExchange_FetchOrderById_StillOpenForNewStatus(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":777,"clientOrderId":"c","symbol":"BTCUSDT","side":"BUY","type":"LIMIT","price":"60000","origQty":"0.5","executedQty":"0","status":"NEW"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	order, stillOpen, err := ex.FetchOrderById(context.Background(), "linear", "BTCUSDT", "777")
	if err != nil {
		t.Fatalf("FetchOrderById: %v", err)
	}
	if gotQuery.Get("orderId") != "777" {
		t.Errorf("query orderId = %q, want 777", gotQuery.Get("orderId"))
	}
	if !stillOpen {
		t.Error("stillOpen = false, want true for a NEW order")
	}
	if order.OrderStatus != "New" {
		t.Errorf("OrderStatus = %q, want New", order.OrderStatus)
	}
}

func TestBinanceExchange_FetchOrderById_NotFoundReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":-2013,"msg":"Order does not exist."}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	if _, _, err := ex.FetchOrderById(context.Background(), "linear", "BTCUSDT", "999"); err == nil {
		t.Fatal("expected an error for a nonexistent order, got nil")
	}
}

func TestBinanceExchange_FetchOpenOrders_NoSymbolFilter(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/fapi/v1/openAlgoOrders":
			_, _ = w.Write([]byte(`[]`)) // no open conditional orders for this test
		default:
			gotQuery = r.URL.Query()
			_, _ = w.Write([]byte(`[{"orderId":1,"clientOrderId":"a","symbol":"BTCUSDT","side":"BUY","type":"LIMIT","price":"1","origQty":"1","executedQty":"0","status":"NEW"},{"orderId":2,"clientOrderId":"b","symbol":"ETHUSDT","side":"SELL","type":"LIMIT","price":"2","origQty":"1","executedQty":"0","status":"NEW"}]`))
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	orders, err := ex.FetchOpenOrders(context.Background())
	if err != nil {
		t.Fatalf("FetchOpenOrders: %v", err)
	}
	if gotQuery.Get("symbol") != "" {
		t.Errorf("query symbol = %q, want empty (account-wide, no symbol filter)", gotQuery.Get("symbol"))
	}
	if len(orders) != 2 {
		t.Fatalf("got %d orders, want 2 (across both symbols)", len(orders))
	}
}

func TestBinanceExchange_GetAccountMarginInfo_ComputesMaintMarginRatePct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalMarginBalance":"1000","totalMaintMargin":"25","assets":[{"asset":"USDT","availableBalance":"800"}]}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	equity, available, mmRatePct, err := ex.GetAccountMarginInfo(context.Background())
	if err != nil {
		t.Fatalf("GetAccountMarginInfo: %v", err)
	}
	if equity != 1000 {
		t.Errorf("equity = %v, want 1000", equity)
	}
	if available != 800 {
		t.Errorf("available = %v, want 800", available)
	}
	// 25 / 1000 * 100 = 2.5%
	if mmRatePct != 2.5 {
		t.Errorf("mmRatePct = %v, want 2.5 (25 maint margin / 1000 equity × 100)", mmRatePct)
	}
}

// TestBinanceExchange_SetTrailingStop_CancelsPriorThenPlacesNewOrder also locks in the
// 2025-12-09 Algo Order migration: TRAILING_STOP_MARKET is itself a conditional type,
// so both the cancel and the place must go through /fapi/v1/algoOrder with its own
// field names (clientAlgoId, activatePrice — NOT origClientOrderId/activationPrice).
func TestBinanceExchange_SetTrailingStop_CancelsPriorThenPlacesNewOrder(t *testing.T) {
	var gotDeletePath, gotPostPath string
	var gotDeleteQuery, gotPostBody url.Values
	var sawDelete, sawPost bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodDelete:
			sawDelete = true
			gotDeletePath = r.URL.Path
			gotDeleteQuery = r.URL.Query()
			_, _ = w.Write([]byte(`{"algoId":1,"clientAlgoId":"old"}`))
		case http.MethodPost:
			sawPost = true
			gotPostPath = r.URL.Path
			_ = r.ParseForm()
			gotPostBody = r.Form
			_, _ = w.Write([]byte(`{"algoId":2,"clientAlgoId":"new"}`))
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	err := ex.SetTrailingStop(context.Background(), trader.TrailingStopRequest{
		Category: "linear", Symbol: "BTCUSDT", PositionIdx: 0,
		Side: "Sell", Qty: "0.5", TrailingStop: "500", ActivePrice: "61000",
		CallbackRatePct: 1.5, ClientOrderID: "SIS_STR-abc12345-trail-1",
	})
	if err != nil {
		t.Fatalf("SetTrailingStop: %v", err)
	}
	if !sawDelete {
		t.Error("expected a DELETE call to cancel the prior trailing-stop order")
	}
	if gotDeletePath != "/fapi/v1/algoOrder" {
		t.Errorf("DELETE path = %q, want /fapi/v1/algoOrder", gotDeletePath)
	}
	if gotDeleteQuery.Get("clientAlgoId") != "SIS_STR-abc12345-trail-1" {
		t.Errorf("DELETE clientAlgoId = %q, want the stable ClientOrderID", gotDeleteQuery.Get("clientAlgoId"))
	}
	if !sawPost {
		t.Fatal("expected a POST call to place the new trailing-stop order")
	}
	if gotPostPath != "/fapi/v1/algoOrder" {
		t.Errorf("POST path = %q, want /fapi/v1/algoOrder", gotPostPath)
	}
	if gotPostBody.Get("algoType") != "CONDITIONAL" {
		t.Errorf("algoType = %q, want CONDITIONAL", gotPostBody.Get("algoType"))
	}
	if gotPostBody.Get("type") != "TRAILING_STOP_MARKET" {
		t.Errorf("type = %q, want TRAILING_STOP_MARKET", gotPostBody.Get("type"))
	}
	if gotPostBody.Get("callbackRate") != "1.5" {
		t.Errorf("callbackRate = %q, want 1.5", gotPostBody.Get("callbackRate"))
	}
	if gotPostBody.Get("activatePrice") != "61000" {
		t.Errorf("activatePrice = %q, want 61000 (NOT activationPrice — the Algo Order API's own field name)", gotPostBody.Get("activatePrice"))
	}
	if gotPostBody.Get("reduceOnly") != "true" {
		t.Errorf("reduceOnly = %q, want true (one-way mode, PositionIdx=0)", gotPostBody.Get("reduceOnly"))
	}
	if gotPostBody.Get("clientAlgoId") != "SIS_STR-abc12345-trail-1" {
		t.Errorf("clientAlgoId = %q, want the stable ClientOrderID", gotPostBody.Get("clientAlgoId"))
	}
}

func TestBinanceExchange_SetTrailingStop_ClearOnlyCancelsWithoutPlacingNew(t *testing.T) {
	var sawPost bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			sawPost = true
		}
		_, _ = w.Write([]byte(`{"orderId":1,"clientOrderId":"old"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	err := ex.SetTrailingStop(context.Background(), trader.TrailingStopRequest{
		Category: "linear", Symbol: "BTCUSDT", PositionIdx: 0,
		TrailingStop: "0", ClientOrderID: "SIS_STR-abc12345-trail-1",
	})
	if err != nil {
		t.Fatalf("SetTrailingStop: %v", err)
	}
	if sawPost {
		t.Error("a clear request (TrailingStop=\"0\") must only cancel, not place a new order")
	}
}

// Regression (2026-10-17): pkg/trader.Syncer used to call Bybit-only free functions
// directly for every account regardless of exchange ("syncer: fetch executions
// account=... bybit: retCode=10003: You are not authorized" on a Binance account).
// FetchAccountExecutions/FetchAccountOrderHistory/FetchWhitelistedIPs close that gap for
// Binance via the same trader.Exchange interface used everywhere else.

func TestBinanceExchange_FetchAccountExecutions_ReturnsRawFillsNotAggregated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0.2","entryPrice":"60000","markPrice":"61000","unRealizedProfit":"200","liquidationPrice":"40000","leverage":"10"}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/income"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/userTrades"):
			// One opening fill, one closing fill on the same order — FetchClosedPnlForSymbol
			// would aggregate these into a single row; FetchAccountExecutions must NOT.
			_, _ = w.Write([]byte(`[
				{"id":1,"orderId":100,"symbol":"BTCUSDT","side":"BUY","positionSide":"BOTH","price":"60000","qty":"0.5","realizedPnl":"0","commission":"1.2","maker":true,"time":1000},
				{"id":2,"orderId":200,"symbol":"BTCUSDT","side":"SELL","positionSide":"BOTH","price":"61000","qty":"0.3","realizedPnl":"300","commission":"0.8","maker":false,"time":2000}
			]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchAccountExecutions(context.Background(), "linear", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FetchAccountExecutions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d executions, want 2 raw fills (opening + closing), not aggregated into 1", len(got))
	}

	byID := map[string]trader.Execution{}
	for _, e := range got {
		byID[e.ExecId] = e
	}
	open, ok := byID["1"]
	if !ok {
		t.Fatalf("missing opening fill (id=1): %+v", got)
	}
	if open.OrderId != "100" || open.Side != "Buy" || open.ExecFee != "1.2" || !open.IsMaker || open.PositionIdx != 0 {
		t.Errorf("opening fill = %+v, want OrderId=100 Side=Buy ExecFee=1.2 IsMaker=true PositionIdx=0", open)
	}
	close, ok := byID["2"]
	if !ok {
		t.Fatalf("missing closing fill (id=2): %+v", got)
	}
	if close.OrderId != "200" || close.Side != "Sell" || close.ExecFee != "0.8" || close.IsMaker {
		t.Errorf("closing fill = %+v, want OrderId=200 Side=Sell ExecFee=0.8 IsMaker=false", close)
	}
	if open.ExecType != "Trade" || close.ExecType != "Trade" {
		t.Errorf("ExecType = %q/%q, want both \"Trade\"", open.ExecType, close.ExecType)
	}
}

func TestBinanceExchange_FetchAccountOrderHistory_ParsesAllOrdersRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/fapi/v3/positionRisk"):
			_, _ = w.Write([]byte(`[{"symbol":"ETHUSDT","positionSide":"BOTH","positionAmt":"1.0","entryPrice":"3000","markPrice":"3100","unRealizedProfit":"100","liquidationPrice":"2000","leverage":"5"}]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/income"):
			_, _ = w.Write([]byte(`[]`))
		case strings.Contains(r.URL.Path, "/fapi/v1/allOrders"):
			if r.URL.Query().Get("symbol") != "ETHUSDT" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`[
				{"orderId":500,"clientOrderId":"SIS_STR-abc12345-tp-1-1","symbol":"ETHUSDT","side":"SELL","type":"LIMIT","price":"3200","origQty":"1.0","executedQty":"1.0","status":"FILLED","stopPrice":"0"},
				{"orderId":501,"clientOrderId":"not-ours","symbol":"ETHUSDT","side":"BUY","type":"LIMIT","price":"2900","origQty":"1.0","executedQty":"0","status":"CANCELED","stopPrice":"0"}
			]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	got, err := ex.FetchAccountOrderHistory(context.Background(), "linear", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("FetchAccountOrderHistory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d orders, want 2 (both rows — filtering by order_link_id prefix is syncer.go's job, not this method's)", len(got))
	}
	byID := map[string]trader.Order{}
	for _, o := range got {
		byID[o.OrderId] = o
	}
	filled, ok := byID["500"]
	if !ok {
		t.Fatalf("missing order 500: %+v", got)
	}
	if filled.OrderLinkId != "SIS_STR-abc12345-tp-1-1" || filled.OrderStatus != "Filled" || filled.Side != "Sell" {
		t.Errorf("order 500 = %+v, want OrderLinkId=SIS_STR-abc12345-tp-1-1 OrderStatus=Filled Side=Sell", filled)
	}
	cancelled, ok := byID["501"]
	if !ok {
		t.Fatalf("missing order 501: %+v", got)
	}
	if cancelled.OrderStatus != "Cancelled" {
		t.Errorf("order 501 OrderStatus = %q, want %q (Binance CANCELED mapped to Bybit's double-L spelling)", cancelled.OrderStatus, "Cancelled")
	}
}

// FetchWhitelistedIPs: Binance has no endpoint exposing the actual IP list (only whether
// restriction is enabled) — must return ErrWhitelistNotSupported, never attempt a call.
func TestBinanceExchange_FetchWhitelistedIPs_ReturnsErrNotSupported(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ips, err := ex.FetchWhitelistedIPs(context.Background())
	if !errors.Is(err, trader.ErrWhitelistNotSupported) {
		t.Errorf("err = %v, want trader.ErrWhitelistNotSupported", err)
	}
	if ips != nil {
		t.Errorf("ips = %v, want nil", ips)
	}
	if hit {
		t.Error("FetchWhitelistedIPs must not make any HTTP call — Binance has no endpoint for this")
	}
}

// fakeBinanceWsOrderClient is a hand-written stand-in for binanceWsOrderClient — lets
// this file assert BinanceExchange's WS-then-REST fallback decision without a real
// network dial, mirroring pkg/trader/exchange_test.go's fakeWSOrderClient for Bybit.
type fakeBinanceWsOrderClient struct {
	placeOrderResp trader.OrderResult
	placeOrderErr  error
	cancelOrderErr error
}

func (f *fakeBinanceWsOrderClient) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return f.placeOrderResp, f.placeOrderErr
}
func (f *fakeBinanceWsOrderClient) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	return nil, f.placeOrderErr
}
func (f *fakeBinanceWsOrderClient) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	return f.cancelOrderErr
}
func (f *fakeBinanceWsOrderClient) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	return f.cancelOrderErr
}

func TestBinanceExchange_PlaceOrder_UsesWSResultWhenAvailable(t *testing.T) {
	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderResp: trader.OrderResult{OrderId: "1", OrderLinkId: "ws-order"}})

	got, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderLinkId != "ws-order" {
		t.Errorf("got %+v, want the WS result, not a REST call", got)
	}
}

func TestBinanceExchange_PlaceOrder_FallsBackToRESTOnErrWSUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":2,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderErr: ErrWSUnavailable})

	got, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if got.OrderLinkId != "rest-order" {
		t.Errorf("got %+v, want the REST fallback's result", got)
	}
}

func TestBinanceExchange_PlaceOrder_DoesNotFallBackOnGenuineRejection(t *testing.T) {
	var hitREST bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitREST = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":3,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	wantErr := errors.New("binance: code=-2019: Margin is insufficient.")
	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderErr: wantErr})

	_, err := ex.PlaceOrder(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err == nil || err.Error() != wantErr.Error() {
		t.Errorf("err = %v, want %v (no REST retry)", err, wantErr)
	}
	if hitREST {
		t.Error("a genuine order rejection must not fall back to REST — duplicate-submission risk")
	}
}

func TestBinanceExchange_HasTradeStream_FalseUntilSetTradeStream(t *testing.T) {
	ex := NewBinanceExchange(testCreds())
	if ex.HasTradeStream() {
		t.Error("HasTradeStream() = true before SetTradeStream was ever called")
	}
	ex.SetTradeStream(NewBinanceTradeStream(testCreds()))
	if !ex.HasTradeStream() {
		t.Error("HasTradeStream() = false after SetTradeStream")
	}
}

func TestBinanceExchange_PlaceOrderREST_NeverTouchesWS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderId":4,"clientOrderId":"rest-order"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	ex.setWsOrderClientForTest(&fakeBinanceWsOrderClient{placeOrderResp: trader.OrderResult{OrderLinkId: "WOULD-BE-WRONG-IF-USED"}})

	got, err := ex.PlaceOrderREST(context.Background(), trader.OrderRequest{Symbol: "BTCUSDT", OrderType: "Market", Qty: "1"})
	if err != nil {
		t.Fatalf("PlaceOrderREST: %v", err)
	}
	if got.OrderLinkId != "rest-order" {
		t.Errorf("got %+v, want PlaceOrderREST to always use REST, ignoring any wired ws client", got)
	}
}

// TestBinanceConditionalOrderType_PicksCorrectTypeByTriggerDirection is the regression
// test for the live bug (2026-10-18, strategy NEARUSDT, acc N2 Binance): a grid DCA
// entry level buying further below the current price (Buy, TriggerDirection=2 — "price
// falls to trigger", see pkg/strategy/cycle.go's placeLevelsBatch) was unconditionally
// sent as STOP_MARKET, which Binance only accepts for a BUY whose trigger fires on a
// price RISE — exactly backwards, and rejected outright with -2021 "Order would
// immediately trigger" rather than a mere inefficiency. See binanceConditionalOrderType's
// doc comment for the full STOP_MARKET vs TAKE_PROFIT_MARKET truth table.
func TestBinanceConditionalOrderType_PicksCorrectTypeByTriggerDirection(t *testing.T) {
	cases := []struct {
		name             string
		side             string
		triggerDirection int
		want             string
	}{
		{"buy rising (breakout entry / short's SL)", "Buy", 1, "STOP_MARKET"},
		{"buy falling (grid DCA entry — the live bug)", "Buy", 2, "TAKE_PROFIT_MARKET"},
		{"sell falling (long's SL)", "Sell", 2, "STOP_MARKET"},
		{"sell rising (grid sell-level / long's TP-as-trigger)", "Sell", 1, "TAKE_PROFIT_MARKET"},
		{"unset trigger direction defaults to the prior behavior", "Buy", 0, "STOP_MARKET"},
		{"side is case-insensitive", "buy", 2, "TAKE_PROFIT_MARKET"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := binanceConditionalOrderType(c.side, c.triggerDirection)
			if got != c.want {
				t.Errorf("binanceConditionalOrderType(%q, %d) = %q, want %q", c.side, c.triggerDirection, got, c.want)
			}
		})
	}
}

// TestBinanceOrderType_NonConditionalUnaffected guards that binanceOrderType's existing
// LIMIT/MARKET dispatch for plain (non-conditional) orders is untouched by routing
// conditional orders through binanceConditionalOrderType.
func TestBinanceOrderType_NonConditionalUnaffected(t *testing.T) {
	if got := binanceOrderType(trader.OrderRequest{OrderType: "Limit"}); got != "LIMIT" {
		t.Errorf("plain Limit: got %q, want LIMIT", got)
	}
	if got := binanceOrderType(trader.OrderRequest{OrderType: "Market"}); got != "MARKET" {
		t.Errorf("plain Market: got %q, want MARKET", got)
	}
}

// TestBinanceExchange_PlaceOrderBatchREST_SendsTakeProfitMarketForFallingBuy reproduces
// the live bug end-to-end through the REST batch path (placeOrderBatchREST) — guards
// against the OTHER half of the bug: even with binanceConditionalOrderType fixed, the
// batch item → trader.OrderRequest conversion in placeOrderBatchREST dropped
// TriggerDirection entirely, so the picked type would silently fall back to STOP_MARKET
// regardless.
func TestBinanceExchange_PlaceOrderBatchREST_SendsTakeProfitMarketForFallingBuy(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"algoId":1,"clientAlgoId":"L2"}`))
	}))
	defer srv.Close()
	withMockBinanceBase(t, srv)

	ex := NewBinanceExchange(testCreds())
	results, err := ex.PlaceOrderBatch(context.Background(), trader.BatchPlaceRequest{
		Category: "linear",
		Request: []trader.BatchOrderItem{
			{
				Symbol: "NEARUSDT", Side: "Buy", OrderType: "Market",
				Qty: "10", TriggerPrice: "4.81", TriggerDirection: 2, // falls to trigger — grid DCA entry
				OrderFilter: "StopOrder", OrderLinkId: "L3",
			},
		},
	})
	if err != nil {
		t.Fatalf("PlaceOrderBatch: %v", err)
	}
	if len(results) != 1 || results[0].Code == -1 {
		t.Fatalf("results = %+v, want a successful algo placement", results)
	}
	if gotPath != "/fapi/v1/algoOrder" {
		t.Errorf("request path = %q, want /fapi/v1/algoOrder (a Buy+falls-to-trigger item must be routed as a conditional/algo order, not the plain batch endpoint)", gotPath)
	}
	if !strings.Contains(gotBody, "type=TAKE_PROFIT_MARKET") {
		t.Errorf("body = %q, want type=TAKE_PROFIT_MARKET — TriggerDirection=2 on a Buy must survive the BatchOrderItem conversion, not be dropped and silently default to STOP_MARKET", gotBody)
	}
}
