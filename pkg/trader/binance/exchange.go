package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"sis/pkg/trader"
)

// binanceWsOrderClient is the subset of *BinanceTradeStream's methods BinanceExchange
// needs for the WS-backed order operations — exists only so exchange_test.go can
// substitute a fake, mirroring pkg/trader/exchange.go's wsOrderClient for Bybit.
type binanceWsOrderClient interface {
	PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req trader.CancelRequest) error
	CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error
}

// BinanceExchange implements trader.Exchange against Binance USDⓈ-M Futures.
// category parameters accepted by interface methods are always effectively "linear"
// here — Binance separates USDⓈ-M/COIN-M by base URL/endpoint family rather than a
// query param the way Bybit does, and this client only ever targets USDⓈ-M (see the
// design spec's non-goals) — the param exists purely for interface compatibility.
type BinanceExchange struct {
	creds trader.Credentials
	ws    binanceWsOrderClient // nil until SetTradeStream is called — PlaceOrder/CancelOrder/batch fall straight through to REST
}

// NewBinanceExchange builds a BinanceExchange for one account's credentials. ws stays
// nil (pure REST, today's exact behavior) until SetTradeStream is called — most
// ResolveExchange call sites (closed_pnl_syncer.go, bot_engine.go, trader_handler.go)
// deliberately never call it, since those are short-lived/read-mostly resolutions that
// must not each open a persistent WS connection. Only AccountRunner's own long-lived
// per-account lifecycle (pkg/strategy/engine.go) calls SetTradeStream.
func NewBinanceExchange(creds trader.Credentials) *BinanceExchange {
	return &BinanceExchange{creds: creds}
}

// SetTradeStream wires a live *BinanceTradeStream into this exchange — see
// NewBinanceExchange's doc comment for who should (and shouldn't) call this.
func (e *BinanceExchange) SetTradeStream(ws *BinanceTradeStream) {
	e.ws = ws
}

// setWsOrderClientForTest is test-only — lets exchange_test.go substitute a
// fakeBinanceWsOrderClient without needing a real *BinanceTradeStream (which would
// require a real or test WS connection). SetTradeStream (above) is the real, exported
// entry point every non-test caller uses.
func (e *BinanceExchange) setWsOrderClientForTest(ws binanceWsOrderClient) {
	e.ws = ws
}

// HasTradeStream reports whether a trade WS has been wired in — used by
// pkg/strategy/engine.go's wireBinanceTradeStream test to confirm SetTradeStream was
// actually reached, without exposing the (unexported) ws field itself.
func (e *BinanceExchange) HasTradeStream() bool {
	return e.ws != nil
}

func (e *BinanceExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	data, err := doPublicGET(ctx, e.creds, "/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}})
	if err != nil {
		return 0, err
	}
	var r struct {
		MarkPrice string `json:"markPrice"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(r.MarkPrice, 64)
}

// binancePositionRow is one row of GET /fapi/v3/positionRisk.
type binancePositionRow struct {
	Symbol           string `json:"symbol"`
	PositionSide     string `json:"positionSide"` // "BOTH", "LONG", "SHORT"
	PositionAmt      string `json:"positionAmt"`  // signed: negative = short
	EntryPrice       string `json:"entryPrice"`
	MarkPrice        string `json:"markPrice"`
	UnRealizedProfit string `json:"unRealizedProfit"`
	LiquidationPrice string `json:"liquidationPrice"`
	Leverage         string `json:"leverage"`
}

func (e *BinanceExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v3/positionRisk", nil)
	if err != nil {
		return nil, err
	}
	var rows []binancePositionRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	var out []trader.Position
	for _, r := range rows {
		amt, err := strconv.ParseFloat(r.PositionAmt, 64)
		if err != nil || amt == 0 {
			continue // Binance lists every symbol; only nonzero positionAmt is a real open position
		}
		side := "Buy"
		positionIdx := 1
		// Strip the sign from the original string (rather than reformatting the
		// parsed float) so precision/trailing zeros from the exchange are preserved
		// exactly — e.g. "-0.25000000" becomes "0.25000000", not "0.25".
		size := strings.TrimPrefix(r.PositionAmt, "-")
		if amt < 0 {
			side = "Sell"
		}
		// PositionSide=="BOTH" (one-way mode) always maps to slot 0, regardless of
		// sign — a one-way-mode short still reports positionSide:"BOTH" with a
		// negative positionAmt, so this must not be an `else if` chained after the
		// sign check above (that would wrongly give it the hedge-mode short slot 2).
		if r.PositionSide == "BOTH" {
			positionIdx = 0
		} else if amt < 0 {
			positionIdx = 2
		}
		out = append(out, trader.Position{
			Symbol:        r.Symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    r.EntryPrice,
			MarkPrice:     r.MarkPrice,
			LiqPrice:      r.LiquidationPrice,
			UnrealisedPnl: r.UnRealizedProfit,
			Leverage:      r.Leverage,
			PositionIdx:   positionIdx,
			Category:      "linear",
		})
	}
	return out, nil
}

func (e *BinanceExchange) GetWalletBalance(ctx context.Context) (equity, available float64, err error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v2/account", nil)
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		TotalMarginBalance string `json:"totalMarginBalance"`
		Assets             []struct {
			Asset            string `json:"asset"`
			AvailableBalance string `json:"availableBalance"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, 0, err
	}
	equity, err = strconv.ParseFloat(r.TotalMarginBalance, 64)
	if err != nil {
		return 0, 0, err
	}
	for _, a := range r.Assets {
		if a.Asset == "USDT" {
			available, _ = strconv.ParseFloat(a.AvailableBalance, 64)
			break
		}
	}
	return equity, available, nil
}

// binanceOrderType maps our OrderType/OrderFilter/TriggerPrice combination onto
// Binance's dedicated conditional-order types. Bybit expresses "this is a conditional
// stop" via OrderFilter="StopOrder" or a nonempty TriggerPrice; Binance has no such
// generic flag — it's baked into the order `type` itself.
func binanceOrderType(req trader.OrderRequest) string {
	if req.OrderFilter == "StopOrder" || req.TriggerPrice != "" {
		return "STOP_MARKET"
	}
	switch req.OrderType {
	case "Limit":
		return "LIMIT"
	default:
		return "MARKET"
	}
}

// isAlgoOrderType reports whether orderType is one of Binance's conditional types —
// since 2025-12-09, Binance REJECTS every one of these on the regular /fapi/v1/order
// and /fapi/v1/batchOrders endpoints with code -4120 ("Order type not supported for
// this endpoint. Please use the Algo Order API endpoints instead."). They must go
// through the separate /fapi/v1/algoOrder family instead (placeAlgoOrder/
// cancelAlgoOrder/parseAlgoOpenOrderRows, below). binanceOrderType above only ever
// returns STOP_MARKET for us today (TP/SL and stop_market grid/matrix entries), but
// this checks the full conditional set for when that changes.
func isAlgoOrderType(orderType string) bool {
	switch orderType {
	case "STOP", "STOP_MARKET", "TAKE_PROFIT", "TAKE_PROFIT_MARKET", "TRAILING_STOP_MARKET":
		return true
	}
	return false
}

// binancePositionSide maps our PositionIdx (0=one-way, 1=long hedge slot, 2=short
// hedge slot — Bybit's convention, reused as our internal canonical one) onto
// Binance's positionSide enum.
func binancePositionSide(positionIdx int) string {
	switch positionIdx {
	case 1:
		return "LONG"
	case 2:
		return "SHORT"
	default:
		return "BOTH"
	}
}

// binancePositionSideToIdx is binancePositionSide's inverse — used by
// binanceUserTradeToExecution, where a fill's own positionSide is given directly (unlike
// FetchPositions, which has to reconstruct it from a signed positionAmt).
func binancePositionSideToIdx(positionSide string) int {
	switch positionSide {
	case "LONG":
		return 1
	case "SHORT":
		return 2
	default: // "BOTH" (one-way mode)
		return 0
	}
}

// binanceSide maps Binance's "BUY"/"SELL" to Bybit's "Buy"/"Sell" convention, matching
// FetchPositions' own translation — pkg/strategy hardcodes the Bybit-style values
// (confirmed via grep of pkg/strategy/cycle.go: side comparisons/assignments use
// exactly "Buy"/"Sell").
func binanceSide(side string) string {
	if side == "SELL" {
		return "Sell"
	}
	return "Buy"
}

// binanceOrderStatus maps Binance's order status enum to Bybit's convention — confirmed
// via grep of pkg/strategy/{cycle,engine,reconcile}.go, which compares OrderStatus
// against exactly "Filled" and "Cancelled" (double L, Bybit's own spelling).
func binanceOrderStatus(status string) string {
	switch status {
	case "NEW":
		return "New"
	case "PARTIALLY_FILLED":
		return "PartiallyFilled"
	case "FILLED":
		return "Filled"
	case "CANCELED":
		return "Cancelled"
	case "REJECTED":
		return "Rejected"
	case "EXPIRED":
		return "Expired"
	default:
		return status
	}
}

// binanceOrderFilter tags a resting order "StopOrder" if it's a conditional
// (trigger-based) order type, "Order" otherwise — matching Bybit's OrderFilter
// convention (see pkg/trader/bybit.go) that pkg/strategy reads to decide cancel
// semantics (e.g. whether a cancel needs OrderFilter:"StopOrder").
func binanceOrderFilter(orderType string) string {
	switch orderType {
	case "STOP", "STOP_MARKET", "TAKE_PROFIT", "TAKE_PROFIT_MARKET", "TRAILING_STOP_MARKET":
		return "StopOrder"
	default:
		return "Order"
	}
}

// placeAlgoOrder places a conditional order (STOP_MARKET/TAKE_PROFIT_MARKET/STOP/
// TAKE_PROFIT/TRAILING_STOP_MARKET) via POST /fapi/v1/algoOrder — the endpoint Binance
// made mandatory for every conditional order type on 2025-12-09 (see isAlgoOrderType's
// doc comment). Mirrors placeOrder's own param-building, but with the Algo Order API's
// renamed fields: triggerPrice (not stopPrice), clientAlgoId (not newClientOrderId),
// and a mandatory algoType=CONDITIONAL. The response uses algoId/clientAlgoId instead
// of orderId/clientOrderId — translated into the same trader.OrderResult shape so
// every caller stays unaware of the distinction; the algoId is what must be passed
// back into cancelAlgoOrder later (NOT a regular orderId — the two ID spaces are
// unrelated, and Binance's cancel endpoint for one never finds an ID from the other).
func (e *BinanceExchange) placeAlgoOrder(ctx context.Context, req trader.OrderRequest, orderType string) (trader.OrderResult, error) {
	params := url.Values{
		"algoType":     {"CONDITIONAL"},
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {orderType},
		"positionSide": {binancePositionSide(req.PositionIdx)},
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	if req.Price != "" {
		params.Set("price", req.Price)
	}
	if req.TriggerPrice != "" {
		params.Set("triggerPrice", req.TriggerPrice)
	}
	if req.TriggerPrice != "" && req.TriggerBy == "MarkPrice" {
		params.Set("workingType", "MARK_PRICE")
	}
	if req.ReduceOnly && req.PositionIdx == 0 {
		params.Set("reduceOnly", "true")
	}
	if req.OrderLinkId != "" {
		params.Set("clientAlgoId", truncateClientOrderID(req.OrderLinkId))
	}

	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/algoOrder", params)
	if err != nil {
		return trader.OrderResult{}, err
	}
	var r struct {
		AlgoId       int64  `json:"algoId"`
		ClientAlgoId string `json:"clientAlgoId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return trader.OrderResult{}, err
	}
	return trader.OrderResult{
		OrderId:     strconv.FormatInt(r.AlgoId, 10),
		OrderLinkId: r.ClientAlgoId,
	}, nil
}

func (e *BinanceExchange) placeOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	orderType := binanceOrderType(req)
	if isAlgoOrderType(orderType) {
		return e.placeAlgoOrder(ctx, req, orderType)
	}
	params := url.Values{
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {orderType},
		"positionSide": {binancePositionSide(req.PositionIdx)},
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	if req.Price != "" {
		params.Set("price", req.Price)
	}
	// req.TriggerPrice is never set here — isAlgoOrderType's branch above already
	// returned for any request with a trigger price (binanceOrderType maps a nonempty
	// TriggerPrice to STOP_MARKET unconditionally), so this path is LIMIT/MARKET only.
	// reduceOnly is invalid in Binance hedge mode (PositionIdx 1/2, positionSide LONG/
	// SHORT) — Binance rejects it with -1106. side+positionSide alone already
	// unambiguously reduce the correct leg in hedge mode, so the flag is only needed
	// (and only valid) in one-way mode.
	if req.ReduceOnly && req.PositionIdx == 0 {
		params.Set("reduceOnly", "true")
	}
	if req.TimeInForce != "" && orderType == "LIMIT" {
		params.Set("timeInForce", req.TimeInForce)
	}
	if req.OrderLinkId != "" {
		params.Set("newClientOrderId", truncateClientOrderID(req.OrderLinkId))
	}

	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/order", params)
	if err != nil {
		return trader.OrderResult{}, err
	}
	var r struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return trader.OrderResult{}, err
	}
	return trader.OrderResult{
		OrderId:     strconv.FormatInt(r.OrderId, 10),
		OrderLinkId: r.ClientOrderId,
	}, nil
}

// PlaceOrder tries the WS trade stream first (if wired via SetTradeStream), falling
// back to REST only when the WS failure is connection-shaped (ErrWSUnavailable) — a
// genuine order outcome (acceptance or rejection) is returned as-is, never retried via a
// different transport, to avoid a duplicate submission.
func (e *BinanceExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	if e.ws != nil {
		result, err := e.ws.PlaceOrder(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return result, err
		}
		log.Printf("binance trade ws: PlaceOrder fallback to REST (%v)", err)
	}
	return e.placeOrder(ctx, req)
}

// PlaceOrderREST always uses REST, regardless of whether a trade WS is wired in — used
// by callers (admin/rescue-engine handlers) that explicitly want a one-off REST call.
func (e *BinanceExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}

// cancelAlgoOrder cancels a conditional order via DELETE /fapi/v1/algoOrder — the
// Algo Order API's own cancel endpoint, with algoId/clientAlgoId instead of the
// regular endpoint's orderId/origClientOrderId. Required for every TP/SL and
// stop_market-entry cancel since Binance's 2025-12-09 migration (see
// isAlgoOrderType's doc comment); /fapi/v1/order's DELETE never finds an algoId at
// all, the two ID spaces are unrelated.
func (e *BinanceExchange) cancelAlgoOrder(ctx context.Context, req trader.CancelRequest) error {
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set("algoId", req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set("clientAlgoId", truncateClientOrderID(req.OrderLinkId))
	}
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/algoOrder", params)
	return err
}

func (e *BinanceExchange) cancelOrder(ctx context.Context, req trader.CancelRequest) error {
	if req.OrderFilter == "StopOrder" {
		return e.cancelAlgoOrder(ctx, req)
	}
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set("orderId", req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set("origClientOrderId", truncateClientOrderID(req.OrderLinkId))
	}
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/order", params)
	return err
}

// CancelOrder follows PlaceOrder's exact WS-then-REST fallback rule — see its doc
// comment.
func (e *BinanceExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	if e.ws != nil {
		err := e.ws.CancelOrder(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return err
		}
		log.Printf("binance trade ws: CancelOrder fallback to REST (%v)", err)
	}
	return e.cancelOrder(ctx, req)
}

func (e *BinanceExchange) CancelOrderREST(ctx context.Context, req trader.CancelRequest) error {
	return e.cancelOrder(ctx, req)
}

func (e *BinanceExchange) CancelAllOrders(ctx context.Context, req trader.CancelAllRequest) error {
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/allOpenOrders", url.Values{"symbol": {req.Symbol}})
	return err
}

// binanceBatchOrderItem is one entry of the JSON array sent as POST
// /fapi/v1/batchOrders' "batchOrders" param (itself a JSON-encoded string, not a
// native array — this is a real Binance quirk: the array is serialized to JSON text
// and that text becomes the value of one form field).
type binanceBatchOrderItem struct {
	Symbol           string `json:"symbol"`
	Side             string `json:"side"`
	Type             string `json:"type"`
	PositionSide     string `json:"positionSide"`
	Quantity         string `json:"quantity,omitempty"`
	Price            string `json:"price,omitempty"`
	StopPrice        string `json:"stopPrice,omitempty"`
	ReduceOnly       string `json:"reduceOnly,omitempty"`
	TimeInForce      string `json:"timeInForce,omitempty"`
	NewClientOrderId string `json:"newClientOrderId,omitempty"`
}

// PlaceOrderBatch splits req.Request into non-conditional items (sent together via
// POST /fapi/v1/batchOrders, as before) and conditional items — STOP_MARKET/TP/SL/
// stop_market entries — each placed with its OWN individual placeAlgoOrder call:
// Binance's 2025-12-09 Algo Order migration provides no batch endpoint for conditional
// orders at all (see isAlgoOrderType's doc comment), so there is no way to place more
// than one per HTTP call. Results are written back into `out` at each item's ORIGINAL
// index — callers (pkg/strategy/cycle.go) index results[j] against their own
// req.Request[j], so preserving that 1:1 positional correspondence regardless of which
// of the two paths actually placed a given item is required, not cosmetic.
// PlaceOrderBatch follows PlaceOrder's exact WS-then-REST fallback rule — see its doc
// comment.
func (e *BinanceExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	if e.ws != nil {
		results, err := e.ws.PlaceOrderBatch(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return results, err
		}
		log.Printf("binance trade ws: PlaceOrderBatch fallback to REST (%v)", err)
	}
	return e.placeOrderBatchREST(ctx, req)
}

func (e *BinanceExchange) placeOrderBatchREST(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	out := make([]trader.BatchPlaceResult, len(req.Request))
	var regularIdx []int
	var regularItems []binanceBatchOrderItem

	for i, it := range req.Request {
		orderReq := trader.OrderRequest{
			Symbol: it.Symbol, Side: it.Side, OrderType: it.OrderType,
			TriggerPrice: it.TriggerPrice, OrderFilter: it.OrderFilter,
			ReduceOnly: it.ReduceOnly, PositionIdx: it.PositionIdx,
			OrderLinkId: it.OrderLinkId,
		}
		orderType := binanceOrderType(orderReq)

		if isAlgoOrderType(orderType) {
			res, err := e.placeAlgoOrder(ctx, orderReq, orderType)
			if err != nil {
				// -1 is never a real Binance error code (always negative, but this
				// exact value isn't used by Binance) and never collides with the
				// Bybit retCodes cycle.go's batch-result handling checks for
				// (110072/110094) — safe to use as "this item failed locally/in
				// transit, not via a parsed exchange response".
				out[i] = trader.BatchPlaceResult{Code: -1, Msg: err.Error()}
				continue
			}
			out[i] = trader.BatchPlaceResult{OrderId: res.OrderId, OrderLinkId: res.OrderLinkId}
			continue
		}

		bi := binanceBatchOrderItem{
			Symbol: it.Symbol, Side: strings.ToUpper(it.Side), Type: orderType,
			PositionSide: binancePositionSide(it.PositionIdx), Quantity: it.Qty, Price: it.Price,
		}
		if it.TimeInForce != "" && orderType == "LIMIT" {
			bi.TimeInForce = it.TimeInForce
		}
		// it.TriggerPrice is never set here — isAlgoOrderType above already handled
		// any item with one (binanceOrderType maps it to STOP_MARKET unconditionally).
		// Same hedge-mode restriction as placeOrder above: reduceOnly is invalid
		// (Binance -1106) when positionSide is LONG/SHORT (PositionIdx 1/2).
		if it.ReduceOnly && it.PositionIdx == 0 {
			bi.ReduceOnly = "true"
		}
		if it.OrderLinkId != "" {
			bi.NewClientOrderId = truncateClientOrderID(it.OrderLinkId)
		}
		regularIdx = append(regularIdx, i)
		regularItems = append(regularItems, bi)
	}

	if len(regularItems) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(regularItems)
	if err != nil {
		return nil, err
	}
	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/batchOrders", url.Values{"batchOrders": {string(encoded)}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
		Code          int    `json:"code"`
		Msg           string `json:"msg"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	for j, r := range rows {
		if j >= len(regularIdx) {
			break
		}
		out[regularIdx[j]] = trader.BatchPlaceResult{
			OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
			Code: r.Code, Msg: r.Msg,
		}
	}
	return out, nil
}

// CancelOrderBatch cancels a batch of orders via DELETE /fapi/v1/batchOrders, which is
// scoped to a single symbol per call (the "symbol" param takes exactly one value) — unlike
// Bybit's batch-cancel endpoint, Binance has no way to mix symbols in one request. This
// implementation uses req.Request[0].Symbol for the whole (non-conditional) batch call;
// if req.Request ever contains non-conditional items for more than one symbol, every
// orderId is sent against that first symbol, which is wrong for items belonging to a
// different symbol. No current caller builds a multi-symbol batch, so this is left as a
// documented limitation rather than implemented as a per-symbol grouping/multiple-
// requests solution.
//
// Conditional items (OrderFilter=="StopOrder" — TP/SL/stop_market entries) are split
// out and cancelled individually via cancelAlgoOrder: Binance's 2025-12-09 Algo Order
// migration provides no batch cancel for them either (see isAlgoOrderType's doc
// comment), unlike regular orders' own /fapi/v1/batchOrders DELETE. All per-item
// errors are collected and the FIRST one is returned (matching this method's existing
// error-reporting granularity — one error for the whole call, not per-item), so one
// bad conditional cancel doesn't stop the rest of the batch from being attempted.
// CancelOrderBatch follows PlaceOrder's exact WS-then-REST fallback rule — see its doc
// comment.
func (e *BinanceExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	if e.ws != nil {
		err := e.ws.CancelOrderBatch(ctx, req)
		if err == nil || !errors.Is(err, ErrWSUnavailable) {
			return err
		}
		log.Printf("binance trade ws: CancelOrderBatch fallback to REST (%v)", err)
	}
	return e.cancelOrderBatchREST(ctx, req)
}

func (e *BinanceExchange) cancelOrderBatchREST(ctx context.Context, req trader.BatchCancelRequest) error {
	if len(req.Request) == 0 {
		return nil
	}
	var symbol string
	var ids []int64
	var firstErr error
	for _, it := range req.Request {
		if it.OrderFilter == "StopOrder" {
			if err := e.cancelAlgoOrder(ctx, trader.CancelRequest{Symbol: it.Symbol, OrderId: it.OrderId}); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		symbol = it.Symbol
		id, err := strconv.ParseInt(it.OrderId, 10, 64)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("binance: invalid orderId %q in batch cancel: %w", it.OrderId, err)
			}
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return firstErr
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if _, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/batchOrders", url.Values{
		"symbol":      {symbol},
		"orderIdList": {string(encoded)},
	}); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// binanceOpenOrderRow is one row of GET /fapi/v1/openOrders, with or without a symbol
// filter — shared by FetchOpenOrdersForSymbolAll and FetchAllOpenOrders.
type binanceOpenOrderRow struct {
	OrderId       int64  `json:"orderId"`
	ClientOrderId string `json:"clientOrderId"`
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Type          string `json:"type"`
	Price         string `json:"price"`
	OrigQty       string `json:"origQty"`
	ExecutedQty   string `json:"executedQty"`
	Status        string `json:"status"`
	StopPrice     string `json:"stopPrice"`
}

func parseOpenOrderRows(data []byte) ([]trader.Order, error) {
	var rows []binanceOpenOrderRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]trader.Order, 0, len(rows))
	for _, r := range rows {
		out = append(out, trader.Order{
			OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
			Symbol: r.Symbol, Side: binanceSide(r.Side), OrderType: r.Type, Price: r.Price,
			Qty: r.OrigQty, CumExecQty: r.ExecutedQty, OrderStatus: binanceOrderStatus(r.Status),
			TriggerPrice: r.StopPrice, Category: "linear", OrderFilter: binanceOrderFilter(r.Type),
		})
	}
	return out, nil
}

// binanceAlgoOpenOrderRow is one row of GET /fapi/v1/openAlgoOrders — conditional
// orders (TP/SL, stop_market entries, trailing stops) live in a completely separate
// order book from GET /fapi/v1/openOrders since Binance's 2025-12-09 Algo Order
// migration (see isAlgoOrderType's doc comment); they never appear in the regular
// endpoint's response at all, however open they are. Every row returned by this
// endpoint is by construction still open, so OrderStatus is always "New" below —
// there's no need to interpret an algoStatus enum for this particular call.
type binanceAlgoOpenOrderRow struct {
	AlgoId       int64  `json:"algoId"`
	ClientAlgoId string `json:"clientAlgoId"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`
	OrderType    string `json:"orderType"` // note: "orderType", not "type" — a genuine field-name difference from the regular order endpoints
	Price        string `json:"price"`
	TriggerPrice string `json:"triggerPrice"`
	Quantity     string `json:"quantity"`
}

func parseAlgoOpenOrderRows(data []byte) ([]trader.Order, error) {
	var rows []binanceAlgoOpenOrderRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make([]trader.Order, 0, len(rows))
	for _, r := range rows {
		out = append(out, trader.Order{
			OrderId: strconv.FormatInt(r.AlgoId, 10), OrderLinkId: r.ClientAlgoId,
			Symbol: r.Symbol, Side: binanceSide(r.Side), OrderType: r.OrderType, Price: r.Price,
			Qty: r.Quantity, OrderStatus: "New",
			TriggerPrice: r.TriggerPrice, Category: "linear", OrderFilter: "StopOrder",
		})
	}
	return out, nil
}

// fetchOpenAlgoOrders is shared by FetchOpenOrdersForSymbolAll/FetchAllOpenOrders —
// params carries an optional "symbol" filter, matching GET /fapi/v1/openOrders' own
// optional-symbol convention (assumed symmetric for /fapi/v1/openAlgoOrders; not
// independently confirmed, but REST-conventional and the only sane design for an
// account-wide snapshot).
func (e *BinanceExchange) fetchOpenAlgoOrders(ctx context.Context, params url.Values) ([]trader.Order, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/openAlgoOrders", params)
	if err != nil {
		// Conditional orders (TP/SL/stop-market entries) no longer appear in
		// /fapi/v1/openOrders at all since the Algo Order migration — a failure here
		// must not be silently swallowed into "no conditional orders found", since
		// reconcile.go's entire safety net (missing-TP/SL detection, stale-order
		// cleanup) depends on actually seeing them.
		return nil, fmt.Errorf("binance: fetch open algo orders: %w", err)
	}
	return parseAlgoOpenOrderRows(data)
}

func (e *BinanceExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/openOrders", url.Values{"symbol": {symbol}})
	if err != nil {
		return nil, err
	}
	regular, err := parseOpenOrderRows(data)
	if err != nil {
		return nil, err
	}
	algo, err := e.fetchOpenAlgoOrders(ctx, url.Values{"symbol": {symbol}})
	if err != nil {
		return nil, err
	}
	return append(regular, algo...), nil
}

// FetchAllOpenOrders returns open orders across every symbol — GET /fapi/v1/openOrders
// without a symbol filter, merged with every open conditional order from
// /fapi/v1/openAlgoOrders (also unfiltered). Used by ws.go to build the initial
// snapshot for the account-wide positions/orders stream (FetchOpenOrdersForSymbolAll
// needs one symbol at a time, which doesn't fit a single account-level snapshot).
func (e *BinanceExchange) FetchAllOpenOrders(ctx context.Context) ([]trader.Order, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/openOrders", nil)
	if err != nil {
		return nil, err
	}
	regular, err := parseOpenOrderRows(data)
	if err != nil {
		return nil, err
	}
	algo, err := e.fetchOpenAlgoOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	return append(regular, algo...), nil
}

// FetchOpenOrders implements trader.Exchange's account-wide open-orders method (Plan
// #4d) — just FetchAllOpenOrders under its interface-mandated name; kept as a thin
// wrapper rather than renaming FetchAllOpenOrders, since ws.go's existing call site and
// this file's own doc comments already refer to it by that name.
func (e *BinanceExchange) FetchOpenOrders(ctx context.Context) ([]trader.Order, error) {
	return e.FetchAllOpenOrders(ctx)
}

// SetLeverage sets one symbol's leverage. Binance has a single leverage value per
// symbol (not separate long/short values like Bybit's hedge-mode BuyLeverage/
// SellLeverage) — BuyLeverage is used as that single value; callers in pkg/strategy
// already always set both fields identically for grid/matrix, so this is a no-op
// simplification in practice, not a behavior loss.
func (e *BinanceExchange) SetLeverage(ctx context.Context, req trader.LeverageRequest) error {
	_, err := doSignedPOST(ctx, e.creds, "/fapi/v1/leverage", url.Values{
		"symbol":   {req.Symbol},
		"leverage": {req.BuyLeverage},
	})
	return err
}

// SwitchPositionMode sets Hedge Mode (dualSidePosition=true) or One-way Mode (false)
// for the WHOLE ACCOUNT — Binance has no per-symbol position mode, unlike Bybit. The
// category/symbol params are accepted only for Exchange interface compatibility and
// are unused. Binance also errors if any position/order is currently open when this
// is called — same class of constraint as Bybit's own "can't switch with an open
// position" error; that error surfaces to the caller unchanged via doSignedPOST.
func (e *BinanceExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	dual := "false"
	if mode != 0 {
		dual = "true"
	}
	data, err := doSignedPOST(ctx, e.creds, "/fapi/v1/positionSide/dual", url.Values{"dualSidePosition": {dual}})
	if err == nil {
		return nil
	}
	// -4059 "No need to change position side": Binance rejects a no-op switch request
	// outright when the account is already in the requested dualSidePosition — unlike
	// Bybit, whose own SwitchPositionMode (bybit.go) explicitly treats the equivalent
	// "already in this mode" response (retCode 110025) as success. Binance needs the
	// identical treatment here, or pkg/strategy's ensurePositionMode (cycle.go, called
	// at the start of every cycle) stops the strategy outright on an account that's
	// already correctly configured — the COMMON case for a setting meant to stay fixed
	// once set, not a rare edge case. Found live (2026-10-16): every single Binance
	// strategy cycle start failed with "Не удалось переключить position mode —
	// стратегия остановлена" the moment the account's position mode was already correct.
	var r struct {
		Code int `json:"code"`
	}
	if jsonErr := json.Unmarshal(data, &r); jsonErr == nil && r.Code == -4059 {
		return nil
	}
	return err
}

// binanceOrderDetail is GET /fapi/v1/order's response shape — a single order looked up
// by orderId or origClientOrderId. Unlike Bybit's two-endpoint (realtime + history)
// fallback chain, Binance returns ANY order (open or historical, within its retention
// window) from this one endpoint regardless of status.
type binanceOrderDetail struct {
	OrderId       int64  `json:"orderId"`
	ClientOrderId string `json:"clientOrderId"`
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Type          string `json:"type"`
	Price         string `json:"price"`
	AvgPrice      string `json:"avgPrice"`
	OrigQty       string `json:"origQty"`
	ExecutedQty   string `json:"executedQty"`
	Status        string `json:"status"`
	StopPrice     string `json:"stopPrice"`
}

func (r binanceOrderDetail) toOrder() trader.Order {
	return trader.Order{
		OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
		Symbol: r.Symbol, Side: binanceSide(r.Side), OrderType: r.Type,
		Price: r.Price, AvgPrice: r.AvgPrice, Qty: r.OrigQty, CumExecQty: r.ExecutedQty,
		OrderStatus: binanceOrderStatus(r.Status), TriggerPrice: r.StopPrice,
		Category: "linear", OrderFilter: binanceOrderFilter(r.Type),
	}
}

// binanceOrderIsOpen reports whether a Binance order status still counts as "active" —
// NEW or PARTIALLY_FILLED. Matches the bool FetchOrderByLinkId/FetchOrderById return:
// "is this order still open" (Bybit: whether it came from /v5/order/realtime vs
// /v5/order/history).
func binanceOrderIsOpen(status string) bool {
	return status == "NEW" || status == "PARTIALLY_FILLED"
}

// binanceAlgoOrderDetail is GET /fapi/v1/algoOrder's single-order-lookup response
// shape — assumed to live at the same path as the POST/DELETE algoOrder calls
// (confirmed), following ordinary REST CRUD conventions; not independently confirmed
// for the GET verb specifically.
type binanceAlgoOrderDetail struct {
	AlgoId       int64  `json:"algoId"`
	ClientAlgoId string `json:"clientAlgoId"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`
	OrderType    string `json:"orderType"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	TriggerPrice string `json:"triggerPrice"`
	AlgoStatus   string `json:"algoStatus"`
}

func (r binanceAlgoOrderDetail) toOrder() trader.Order {
	return trader.Order{
		OrderId: strconv.FormatInt(r.AlgoId, 10), OrderLinkId: r.ClientAlgoId,
		Symbol: r.Symbol, Side: binanceSide(r.Side), OrderType: r.OrderType,
		Price: r.Price, Qty: r.Quantity, TriggerPrice: r.TriggerPrice,
		OrderStatus: binanceAlgoOrderStatus(r.AlgoStatus),
		Category:    "linear", OrderFilter: "StopOrder",
	}
}

// binanceAlgoOrderStatus maps algoStatus onto the same Bybit-style vocabulary
// binanceOrderStatus uses for regular orders. The exact algoStatus enum isn't
// independently confirmed beyond "NEW" (seen in Binance's own documented example
// response) — this defensively covers both American and British CANCELED/CANCELLED
// spellings since Binance's API surface isn't always internally consistent, and falls
// through to the raw value for anything unrecognized rather than silently mangling it.
func binanceAlgoOrderStatus(status string) string {
	switch status {
	case "NEW", "WORKING":
		return "New"
	case "CANCELED", "CANCELLED":
		return "Cancelled"
	case "EXECUTED", "FILLED", "TRIGGERED":
		return "Filled"
	case "EXPIRED":
		return "Expired"
	case "REJECTED":
		return "Rejected"
	default:
		return status
	}
}

func binanceAlgoOrderIsOpen(status string) bool {
	return status == "NEW" || status == "WORKING"
}

// fetchOneOrder is the shared implementation for FetchOrderByLinkId/FetchOrderById.
// Tries the regular order endpoint first, then falls back to the algo-order lookup —
// since Binance's 2025-12-09 Algo Order migration, a linkId/orderId belonging to a
// conditional order (TP/SL, stop_market entry) is NEVER found via /fapi/v1/order at
// all, only via /fapi/v1/algoOrder (see isAlgoOrderType's doc comment). Trying regular
// first costs one extra round trip for every conditional-order lookup, but needs no
// caller-supplied hint about which kind of order it's looking for — FetchOrderByLinkId/
// FetchOrderById's own signatures (shared with trader.Exchange, implemented
// identically by Bybit) have no room to add one.
func (e *BinanceExchange) fetchOneOrder(ctx context.Context, symbol string, params url.Values) (trader.Order, bool, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/order", params)
	if err == nil {
		var r binanceOrderDetail
		if jsonErr := json.Unmarshal(data, &r); jsonErr == nil && r.OrderId != 0 {
			return r.toOrder(), binanceOrderIsOpen(r.Status), nil
		}
	}

	algoParams := url.Values{"symbol": {symbol}}
	if v := params.Get("origClientOrderId"); v != "" {
		algoParams.Set("clientAlgoId", v)
	}
	if v := params.Get("orderId"); v != "" {
		algoParams.Set("algoId", v)
	}
	algoData, algoErr := doSignedGET(ctx, e.creds, "/fapi/v1/algoOrder", algoParams)
	if algoErr != nil {
		return trader.Order{}, false, fmt.Errorf("binance: order not found (symbol=%s): %w", symbol, algoErr)
	}
	var ar binanceAlgoOrderDetail
	if jsonErr := json.Unmarshal(algoData, &ar); jsonErr != nil || ar.AlgoId == 0 {
		return trader.Order{}, false, fmt.Errorf("binance: order not found (symbol=%s)", symbol)
	}
	return ar.toOrder(), binanceAlgoOrderIsOpen(ar.AlgoStatus), nil
}

// FetchOrderByLinkId looks up a single order by our own clientOrderId.
func (e *BinanceExchange) FetchOrderByLinkId(ctx context.Context, category, symbol, orderLinkId string) (trader.Order, bool, error) {
	return e.fetchOneOrder(ctx, symbol, url.Values{"symbol": {symbol}, "origClientOrderId": {orderLinkId}})
}

// FetchOrderById looks up a single order by the exchange's own orderId.
func (e *BinanceExchange) FetchOrderById(ctx context.Context, category, symbol, orderId string) (trader.Order, bool, error) {
	return e.fetchOneOrder(ctx, symbol, url.Values{"symbol": {symbol}, "orderId": {orderId}})
}

// GetAccountMarginInfo returns account-wide equity/available-balance/maintenance-margin-
// rate from the same /fapi/v2/account endpoint GetWalletBalance already uses, now also
// reading totalMaintMargin to derive a Bybit-style accountMMRate-as-percentage figure.
func (e *BinanceExchange) GetAccountMarginInfo(ctx context.Context) (equity, available, mmRatePct float64, err error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v2/account", nil)
	if err != nil {
		return 0, 0, 0, err
	}
	var r struct {
		TotalMarginBalance string `json:"totalMarginBalance"`
		TotalMaintMargin   string `json:"totalMaintMargin"`
		Assets             []struct {
			Asset            string `json:"asset"`
			AvailableBalance string `json:"availableBalance"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, 0, 0, err
	}
	equity, err = strconv.ParseFloat(r.TotalMarginBalance, 64)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, a := range r.Assets {
		if a.Asset == "USDT" {
			available, _ = strconv.ParseFloat(a.AvailableBalance, 64)
			break
		}
	}
	maintMargin, _ := strconv.ParseFloat(r.TotalMaintMargin, 64)
	if equity > 0 {
		mmRatePct = maintMargin / equity * 100
	}
	return equity, available, mmRatePct, nil
}

// SetTrailingStop implements a Binance TRAILING_STOP_MARKET order in place of Bybit's
// position-level trailing-stop attribute — see trader.Exchange's SetTrailingStop doc
// comment for why the two exchanges need such different request fields. Binance can't
// amend a trailing stop in place, so this cancels any existing order under
// req.ClientOrderID first (a "not found" cancel error is expected and ignored — there's
// nothing to cancel the very first time this runs for a position) and places a fresh
// one, unless req.TrailingStop=="0" (clear request), in which case cancelling is all
// that's needed.
// SetTrailingStop places a TRAILING_STOP_MARKET order via POST /fapi/v1/algoOrder —
// TRAILING_STOP_MARKET is itself one of the conditional types Binance's 2025-12-09
// Algo Order migration moved off /fapi/v1/order entirely (see isAlgoOrderType's doc
// comment), so this never used the plain order endpoint correctly to begin with past
// that cutover date. Uses the Algo Order API's own field names, which differ from both
// the regular order endpoint AND from this package's other params in places:
// activatePrice (not activationPrice — note the missing "ion"), clientAlgoId (not
// newClientOrderId), algoId-based cancel (not origClientOrderId-based).
func (e *BinanceExchange) SetTrailingStop(ctx context.Context, req trader.TrailingStopRequest) error {
	if req.ClientOrderID != "" {
		_ = e.cancelAlgoOrder(ctx, trader.CancelRequest{Symbol: req.Symbol, OrderLinkId: req.ClientOrderID})
	}
	if req.TrailingStop == "0" || req.TrailingStop == "" {
		return nil // clear-only request — the cancel above already did the work
	}
	if req.CallbackRatePct <= 0 {
		return fmt.Errorf("binance: SetTrailingStop requires CallbackRatePct > 0 (got %v)", req.CallbackRatePct)
	}
	params := url.Values{
		"algoType":     {"CONDITIONAL"},
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {"TRAILING_STOP_MARKET"},
		"positionSide": {binancePositionSide(req.PositionIdx)},
		"callbackRate": {strconv.FormatFloat(req.CallbackRatePct, 'f', -1, 64)},
	}
	if req.ActivePrice != "" {
		params.Set("activatePrice", req.ActivePrice)
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	// reduceOnly is invalid in Binance hedge mode (positionSide LONG/SHORT) — same
	// restriction placeOrder already works around for every other order type.
	if req.PositionIdx == 0 {
		params.Set("reduceOnly", "true")
	}
	if req.ClientOrderID != "" {
		params.Set("clientAlgoId", truncateClientOrderID(req.ClientOrderID))
	}
	_, err := doSignedPOST(ctx, e.creds, "/fapi/v1/algoOrder", params)
	return err
}

type binanceUserTrade struct {
	Id           int64  `json:"id"`
	OrderId      int64  `json:"orderId"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`
	PositionSide string `json:"positionSide"`
	Price        string `json:"price"`
	Qty          string `json:"qty"`
	RealizedPnl  string `json:"realizedPnl"`
	Commission   string `json:"commission"`
	Maker        bool   `json:"maker"`
	Time         int64  `json:"time"`
}

// binanceUserTradeToExecution maps one raw userTrades fill to trader.Execution — used by
// FetchAccountExecutions (every fill, opening AND closing) as opposed to
// closingFillsToClosedPnl (which aggregates only the closing ones into VWAP'd rows).
//
// OrderLinkId is deliberately left empty: resolving every single fill's clientOrderId
// here (via lookupClientOrderId) would mean one extra REST round-trip per distinct
// orderId across the whole sync window, unlike closingFillsToClosedPnl which only needs
// that lookup for the handful of CLOSING orders in a batch. trader_executions' real
// consumers (pkg/strategy/trade_recorder.go's InsertMatrixTPProfit and
// AccumulateMatrixLevelSLPnl) key their fee lookups off order_id, not order_link_id, so
// this is a deliberate, documented gap rather than an oversight.
func binanceUserTradeToExecution(symbol string, t binanceUserTrade) trader.Execution {
	qty, _ := strconv.ParseFloat(t.Qty, 64)
	price, _ := strconv.ParseFloat(t.Price, 64)
	return trader.Execution{
		ExecId:      strconv.FormatInt(t.Id, 10),
		OrderId:     strconv.FormatInt(t.OrderId, 10),
		Symbol:      symbol,
		Side:        binanceSide(t.Side),
		ExecType:    "Trade", // userTrades only ever reports fills — funding is a separate /fapi/v1/income row, never a trade
		ExecQty:     t.Qty,
		ExecPrice:   t.Price,
		ExecValue:   strconv.FormatFloat(qty*price, 'f', -1, 64),
		ExecFee:     t.Commission,
		IsMaker:     t.Maker,
		ExecTimeMs:  strconv.FormatInt(t.Time, 10),
		Category:    "linear",
		PositionIdx: binancePositionSideToIdx(t.PositionSide),
	}
}

// isClosingFill reports whether a fill reduces a position, using side+positionSide —
// independent of realizedPnl, so a genuine breakeven close (realizedPnl=="0" despite
// being a real close, since Binance's realizedPnl excludes commission) isn't confused
// with an opening/adding fill. In hedge mode (positionSide LONG/SHORT) this is
// unambiguous: a LONG position is reduced by a SELL, a SHORT position by a BUY. In
// one-way mode (positionSide=="BOTH") side alone can't distinguish "opening" from
// "reducing" without tracking a running position — that case still falls back to the
// realizedPnl!=0 heuristic in closingFillsToClosedPnl (a known, narrower limitation
// than before, only affecting one-way-mode breakeven closes).
func isClosingFill(side, positionSide string) bool {
	switch positionSide {
	case "LONG":
		return side == "SELL"
	case "SHORT":
		return side == "BUY"
	default:
		return false
	}
}

// closingFillsToClosedPnl groups closing/reducing fills (see isClosingFill; fills with
// nonzero realizedPnl are also included as a one-way-mode fallback) by orderId, summing
// PnL and quantity and taking the latest fill's price/time as the row's exit/close
// time — the same one-row-per-closing-order shape Bybit's own /v5/position/closed-pnl
// already returns natively.
//
// Entry price is reconstructed by walking EVERY fill in `trades` (not just closing
// ones) in chronological order, maintaining a running qty-weighted cost basis per
// positionSide bucket (independent books for hedge mode's LONG/SHORT; one shared book
// for one-way mode's BOTH) — an opening/adding fill updates the book, a closing fill
// reads the book's current VWAP as that fill's entry price before reducing it. This is
// only as accurate as `trades` is complete: if the position was opened before the
// earliest fill in `trades` (the caller's fetch window started after the position was
// already open), the book starts "empty" and the computed entry price for that first
// close is wrong — a known, caller-dependent limitation, strictly better than the
// previous behavior of always leaving entry price at zero for every Binance trade.
//
// A lookupClientOrderId failure for one orderId (e.g. Binance's order history retention
// limit has purged it) does NOT abort the whole batch — that row is skipped and the
// first error encountered is returned alongside every row that DID succeed, so a caller
// keeps partial progress instead of losing already-aggregated rows for unrelated orders.
func closingFillsToClosedPnl(ctx context.Context, creds trader.Credentials, symbol string, trades []binanceUserTrade) ([]trader.ClosedPnl, error) {
	sorted := make([]binanceUserTrade, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Time != sorted[j].Time {
			return sorted[i].Time < sorted[j].Time
		}
		return sorted[i].Id < sorted[j].Id
	})

	type agg struct {
		side         string
		qty          float64
		pnl          float64
		lastPrice    string
		lastTimeMs   int64
		entryCostSum float64 // Σ(avgEntryAtFillTime × fillQty), for this order's qty-weighted AvgEntryPrice
		exitValueSum float64 // Σ(fillPrice × fillQty), for CumExitValue
		feeSum       float64 // Σ|commission| across this order's closing fills — real exchange-reported cost
	}
	type book struct{ qty, cost float64 } // cost = Σ(openPrice × openQty); VWAP = cost/qty
	books := make(map[string]*book, 2)    // keyed by positionSide: "LONG"/"SHORT"/"BOTH"

	byOrder := make(map[int64]*agg)
	var order []int64
	for _, t := range sorted {
		qty, _ := strconv.ParseFloat(t.Qty, 64)
		price, _ := strconv.ParseFloat(t.Price, 64)
		pnl, _ := strconv.ParseFloat(t.RealizedPnl, 64)

		b, ok := books[t.PositionSide]
		if !ok {
			b = &book{}
			books[t.PositionSide] = b
		}

		closing := isClosingFill(t.Side, t.PositionSide) || pnl != 0
		if !closing {
			// Opening/adding fill: extend the running position and its cost basis.
			b.qty += qty
			b.cost += price * qty
			continue
		}

		avgEntry := 0.0
		if b.qty > 0 {
			avgEntry = b.cost / b.qty
		}
		a, ok := byOrder[t.OrderId]
		if !ok {
			a = &agg{side: binanceSide(t.Side)}
			byOrder[t.OrderId] = a
			order = append(order, t.OrderId)
		}
		commission, _ := strconv.ParseFloat(t.Commission, 64)
		a.qty += qty
		a.pnl += pnl
		a.entryCostSum += avgEntry * qty
		a.exitValueSum += price * qty
		a.feeSum += math.Abs(commission)
		if t.Time >= a.lastTimeMs {
			a.lastTimeMs = t.Time
			a.lastPrice = t.Price
		}

		// Reduce the book by this closing fill, clamped at zero — a closing fill
		// larger than the tracked book (the exact "window started mid-position" case
		// the doc comment above describes) would otherwise drive qty/cost negative.
		b.qty -= qty
		b.cost -= avgEntry * qty
		if b.qty <= 0 {
			b.qty, b.cost = 0, 0
		}
	}

	out := make([]trader.ClosedPnl, 0, len(order))
	var firstErr error
	for _, orderId := range order {
		a := byOrder[orderId]
		linkId, err := lookupClientOrderId(ctx, creds, symbol, orderId)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue // skip this row, keep processing the rest
		}
		// AvgEntryPrice/CumEntryValue/CumExitValue are reconstructed from the running
		// position book above — accurate whenever `trades` covers the position's full
		// open-to-close lifetime, degraded (but no longer simply zero) otherwise; see
		// this function's doc comment. Leverage is still left unpopulated — Binance's
		// userTrades/order responses don't provide it at all, cheaply or otherwise.
		// UpdatedTime IS populated (same as CreatedTime — Binance's userTrades only gives
		// one timestamp per fill, there's no separate "last updated" moment the way
		// Bybit's closed-pnl rows have): the terminal's PnL tab (PnlTable.tsx) displays
		// updatedTime, not createdTime, for its "Время" column.
		//
		// Fee is real commission straight from userTrades, NOT derived — the frontend's
		// own fallback (gross-from-entry/exit-prices minus closedPnl) silently assumed
		// AvgEntryPrice was always exact, which it reliably is for Bybit (the exchange's
		// own closed-pnl record) but is only an RECONSTRUCTED estimate for Binance
		// (above) — for a position built up across many fills/windows, any imprecision
		// there got amplified into a wildly wrong "fee" once multiplied by qty and
		// subtracted from the real closedPnl. Found live (2026-10-16): a SUPERUSDT close
		// showed a derived "fee" of ~94 USDT on a ~719 USDT trade (13%!), nowhere near
		// Binance's real ~0.04-0.05% futures taker rate.
		avgEntry := 0.0
		if a.qty > 0 {
			avgEntry = a.entryCostSum / a.qty
		}
		avgExit := 0.0
		if a.qty > 0 {
			avgExit = a.exitValueSum / a.qty
		}
		out = append(out, trader.ClosedPnl{
			Symbol: symbol, OrderId: strconv.FormatInt(orderId, 10), OrderLinkId: linkId,
			Side: a.side, Qty: strconv.FormatFloat(a.qty, 'f', -1, 64),
			AvgEntryPrice: strconv.FormatFloat(avgEntry, 'f', -1, 64),
			AvgExitPrice:  strconv.FormatFloat(avgExit, 'f', -1, 64),
			CumEntryValue: strconv.FormatFloat(a.entryCostSum, 'f', -1, 64),
			CumExitValue:  strconv.FormatFloat(a.exitValueSum, 'f', -1, 64),
			ClosedPnl:     strconv.FormatFloat(a.pnl, 'f', -1, 64),
			Fee:           strconv.FormatFloat(a.feeSum, 'f', -1, 64),
			CreatedTime:   strconv.FormatInt(a.lastTimeMs, 10),
			UpdatedTime:   strconv.FormatInt(a.lastTimeMs, 10),
			Category:      "linear",
		})
	}
	return out, firstErr
}

// lookupClientOrderId fetches one order's clientOrderId by orderId — userTrades doesn't
// carry it, but pkg/strategy's attribution (ParseStrategyLinkID) needs it. One call per
// distinct closing order in the queried window, not per fill.
func lookupClientOrderId(ctx context.Context, creds trader.Credentials, symbol string, orderId int64) (string, error) {
	data, err := doSignedGET(ctx, creds, "/fapi/v1/order", url.Values{
		"symbol": {symbol}, "orderId": {strconv.FormatInt(orderId, 10)},
	})
	if err != nil {
		return "", err
	}
	var r struct {
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.ClientOrderId, nil
}

// limit bounds the number of raw fills fetched from userTrades, not the number of
// closing-pnl rows returned — unlike Bybit's native endpoint, since multiple fills can
// merge into one row or be filtered out as opening fills. A caller asking for
// limit=50 can get fewer (or zero) real closing rows back.
func (e *BinanceExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]trader.ClosedPnl, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
		"symbol": {symbol}, "limit": {strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, err
	}
	var trades []binanceUserTrade
	if err := json.Unmarshal(data, &trades); err != nil {
		return nil, err
	}
	return closingFillsToClosedPnl(ctx, e.creds, symbol, trades)
}

// binanceIncomeRow is one row of GET /fapi/v1/income.
type binanceIncomeRow struct {
	Symbol     string `json:"symbol"`
	IncomeType string `json:"incomeType"`
	Time       int64  `json:"time"`
}

// fetchRealizedPnlSymbols returns the distinct set of symbols with at least one
// REALIZED_PNL income event in [start, end] — /fapi/v1/income is account-wide (unlike
// userTrades, which is always per-symbol and needs a symbol to query), so this is how
// FetchRecentClosedPnl discovers which symbols to fetch userTrades for WITHOUT already
// knowing the symbol — the one thing a fully-closed position's symbol never is: still
// present in FetchPositions.
func fetchRealizedPnlSymbols(ctx context.Context, creds trader.Credentials, start, end time.Time) (map[string]bool, error) {
	data, err := doSignedGET(ctx, creds, "/fapi/v1/income", url.Values{
		"incomeType": {"REALIZED_PNL"},
		"startTime":  {strconv.FormatInt(start.UnixMilli(), 10)},
		"endTime":    {strconv.FormatInt(end.UnixMilli(), 10)},
		"limit":      {"1000"},
	})
	if err != nil {
		return nil, err
	}
	var rows []binanceIncomeRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.Symbol != "" {
			out[r.Symbol] = true
		}
	}
	return out, nil
}

// FetchRecentClosedPnl fans out across ≤7-day windows from since to now — Binance caps
// userTrades' startTime..endTime span at 7 days per call, unlike Bybit's single
// cursor-paginated call across an arbitrarily old `since`. category is accepted for
// interface compatibility.
//
// Per window, the symbols queried are the union of currently-open positions (catches a
// partial close on a position that's still open) and fetchRealizedPnlSymbols' account-
// wide income-history discovery (catches a position that closed fully to flat within
// the window — FIXED 2026-10-16: this used to derive the symbol list from
// FetchPositions ALONE, which by construction never includes a symbol whose position
// has already fully closed — so a completed Binance trade could never be found at all,
// and never appeared in the P&L table, regardless of how long this syncer kept retrying).
//
// A single window or lookup failure (for one symbol, or one order within one window)
// does NOT abort the whole multi-symbol/multi-window sweep — every row that DID
// succeed is still returned, alongside the first error encountered, so a caller keeps
// partial progress rather than losing every already-fetched row to one bad window.
// forEachWindowSince is the shared core of every Binance account-wide, time-bounded
// sweep (FetchRecentClosedPnl, FetchAccountExecutions, FetchAccountOrderHistory): it
// fans out from `since` to now in ≤7-day windows (Binance caps userTrades'/allOrders'
// startTime..endTime span at 7 days) and, per window, resolves the symbol set to query —
// currently-open positions ∪ fetchRealizedPnlSymbols' account-wide income-history
// discovery for that window (catches a position that closed fully to flat within the
// window — see FetchRecentClosedPnl's original 2026-10-16 fix note, now applying to all
// three callers instead of just one copy of this logic).
//
// onErr is called (possibly multiple times) for every sub-step failure; it does NOT
// abort the sweep — one window or one symbol failing doesn't lose progress already made
// on others. fn is called once per window with that window's resolved symbol set; the
// caller decides what REST call to make per symbol and what to do with the result.
func (e *BinanceExchange) forEachWindowSince(ctx context.Context, since time.Time, onErr func(error), fn func(windowStart, windowEnd time.Time, symbols map[string]bool)) {
	positions, err := e.FetchPositions(ctx)
	if err != nil {
		onErr(err)
		return
	}
	openSymbols := make(map[string]bool, len(positions))
	for _, p := range positions {
		openSymbols[p.Symbol] = true
	}

	now := time.Now()
	windowStart := since
	for windowStart.Before(now) {
		windowEnd := windowStart.Add(7 * 24 * time.Hour)
		if windowEnd.After(now) {
			windowEnd = now
		}

		symbols := make(map[string]bool, len(openSymbols)+4)
		for sym := range openSymbols {
			symbols[sym] = true
		}
		if incomeSymbols, ierr := fetchRealizedPnlSymbols(ctx, e.creds, windowStart, windowEnd); ierr != nil {
			onErr(ierr)
		} else {
			for sym := range incomeSymbols {
				symbols[sym] = true
			}
		}

		fn(windowStart, windowEnd, symbols)
		windowStart = windowEnd
	}
}

func (e *BinanceExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	var all []trader.ClosedPnl
	var firstErr error
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	e.forEachWindowSince(ctx, since, setErr, func(windowStart, windowEnd time.Time, symbols map[string]bool) {
		for sym := range symbols {
			data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
				"symbol":    {sym},
				"startTime": {strconv.FormatInt(windowStart.UnixMilli(), 10)},
				"endTime":   {strconv.FormatInt(windowEnd.UnixMilli(), 10)},
				"limit":     {"1000"},
			})
			if err != nil {
				setErr(err)
				continue
			}
			var trades []binanceUserTrade
			if err := json.Unmarshal(data, &trades); err != nil {
				setErr(err)
				continue
			}
			rows, err := closingFillsToClosedPnl(ctx, e.creds, sym, trades)
			setErr(err)
			all = append(all, rows...)
		}
	})
	return all, firstErr
}

// FetchAccountExecutions implements trader.Exchange for Binance by reusing
// forEachWindowSince, returning raw per-fill Execution rows (every fill, opening and
// closing) instead of FetchRecentClosedPnl's VWAP-aggregated ClosedPnl rows. category is
// accepted for interface compatibility and ignored — Binance USDⓈ-M futures only, unlike
// Bybit's linear/inverse/spot split.
//
// Added 2026-10-17: pkg/trader.Syncer used to call Bybit-only free functions directly
// for every account regardless of exchange — found live via a Binance account's logs
// showing "syncer: fetch executions ... bybit: retCode=10003: You are not authorized".
func (e *BinanceExchange) FetchAccountExecutions(ctx context.Context, category string, since time.Time) ([]trader.Execution, error) {
	var all []trader.Execution
	var firstErr error
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	e.forEachWindowSince(ctx, since, setErr, func(windowStart, windowEnd time.Time, symbols map[string]bool) {
		for sym := range symbols {
			data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
				"symbol":    {sym},
				"startTime": {strconv.FormatInt(windowStart.UnixMilli(), 10)},
				"endTime":   {strconv.FormatInt(windowEnd.UnixMilli(), 10)},
				"limit":     {"1000"},
			})
			if err != nil {
				setErr(err)
				continue
			}
			var trades []binanceUserTrade
			if err := json.Unmarshal(data, &trades); err != nil {
				setErr(err)
				continue
			}
			for _, t := range trades {
				all = append(all, binanceUserTradeToExecution(sym, t))
			}
		}
	})
	return all, firstErr
}

// FetchAccountOrderHistory is FetchAccountExecutions' counterpart for order history
// (refreshing trader_orders' status/fill fields for our own "sis"-prefixed orders — see
// pkg/trader.Syncer.syncOrderHistory). Reuses parseOpenOrderRows: GET /fapi/v1/allOrders
// returns the exact same row shape as GET /fapi/v1/openOrders (orderId, clientOrderId,
// symbol, side, type, price, origQty, executedQty, status, stopPrice — confirmed via
// developers.binance.com's docs), just over a different (closed-inclusive) time range.
//
// Known limitation: like FetchAccountExecutions, the symbol set comes from
// forEachWindowSince's open-positions ∪ realized-pnl discovery, which can miss a pure
// cancel (an order that never filled) on a symbol with no other current/historical
// exposure — a narrower gap than Bybit's own unbounded order-history walk, but avoids
// giving this method a DB dependency just to ask "which symbols has this account ever
// placed a 'sis' order for" instead.
func (e *BinanceExchange) FetchAccountOrderHistory(ctx context.Context, category string, since time.Time) ([]trader.Order, error) {
	var all []trader.Order
	var firstErr error
	setErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	e.forEachWindowSince(ctx, since, setErr, func(windowStart, windowEnd time.Time, symbols map[string]bool) {
		for sym := range symbols {
			data, err := doSignedGET(ctx, e.creds, "/fapi/v1/allOrders", url.Values{
				"symbol":    {sym},
				"startTime": {strconv.FormatInt(windowStart.UnixMilli(), 10)},
				"endTime":   {strconv.FormatInt(windowEnd.UnixMilli(), 10)},
				"limit":     {"1000"},
			})
			if err != nil {
				setErr(err)
				continue
			}
			orders, err := parseOpenOrderRows(data)
			if err != nil {
				setErr(err)
				continue
			}
			all = append(all, orders...)
		}
	})
	return all, firstErr
}

// FetchWhitelistedIPs: Binance has no endpoint equivalent to Bybit's query-api — its
// closest analogue, GET /sapi/v1/account/apiRestrictions, only reports WHETHER IP
// restriction is enabled (an ipRestrict bool), never the actual IP list (confirmed via
// developers.binance.com/docs/wallet/account/api-key-permission's full response field
// list) — so there is nothing to persist into exchange_accounts.whitelisted_ips for a
// Binance account via any API call.
func (e *BinanceExchange) FetchWhitelistedIPs(ctx context.Context) ([]string, error) {
	return nil, trader.ErrWhitelistNotSupported
}

// var _ trader.Exchange = (*BinanceExchange)(nil) fails to compile if BinanceExchange
// ever stops satisfying trader.Exchange — mirrors pkg/trader/exchange.go's own
// var _ Exchange = (*BybitExchange)(nil).
var _ trader.Exchange = (*BinanceExchange)(nil)
