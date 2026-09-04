package binance

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"sis/pkg/trader"
)

// BinanceExchange implements trader.Exchange against Binance USDⓈ-M Futures.
// category parameters accepted by interface methods are always effectively "linear"
// here — Binance separates USDⓈ-M/COIN-M by base URL/endpoint family rather than a
// query param the way Bybit does, and this client only ever targets USDⓈ-M (see the
// design spec's non-goals) — the param exists purely for interface compatibility.
type BinanceExchange struct {
	creds trader.Credentials
}

// NewBinanceExchange builds a BinanceExchange for one account's credentials.
func NewBinanceExchange(creds trader.Credentials) *BinanceExchange {
	return &BinanceExchange{creds: creds}
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

func (e *BinanceExchange) placeOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	orderType := binanceOrderType(req)
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
	if req.TriggerPrice != "" {
		params.Set("stopPrice", req.TriggerPrice)
	}
	// workingType picks which price feed (mark vs last) triggers a conditional order.
	// Binance's default (CONTRACT_PRICE, i.e. last price) already matches our
	// TriggerBy=="LastPrice"/"" default, so only MarkPrice needs an explicit param.
	if req.TriggerPrice != "" && req.TriggerBy == "MarkPrice" {
		params.Set("workingType", "MARK_PRICE")
	}
	if req.ReduceOnly {
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

// PlaceOrder and PlaceOrderREST are identical for Binance — there is no separate
// low-latency WS order-placement channel the way Bybit's TradeStream provides, so both
// Exchange interface methods resolve to the same REST call.
func (e *BinanceExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}

func (e *BinanceExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	return e.placeOrder(ctx, req)
}

func (e *BinanceExchange) cancelOrder(ctx context.Context, req trader.CancelRequest) error {
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set("orderId", req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set("origClientOrderId", truncateClientOrderID(req.OrderLinkId))
	}
	_, err := doSignedDELETE(ctx, e.creds, "/fapi/v1/order", params)
	return err
}

func (e *BinanceExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
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

func (e *BinanceExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	items := make([]binanceBatchOrderItem, 0, len(req.Request))
	for _, it := range req.Request {
		orderReq := trader.OrderRequest{
			Symbol: it.Symbol, Side: it.Side, OrderType: it.OrderType,
			TriggerPrice: it.TriggerPrice, OrderFilter: it.OrderFilter,
			ReduceOnly: it.ReduceOnly, PositionIdx: it.PositionIdx,
		}
		orderType := binanceOrderType(orderReq)
		bi := binanceBatchOrderItem{
			Symbol: it.Symbol, Side: strings.ToUpper(it.Side), Type: orderType,
			PositionSide: binancePositionSide(it.PositionIdx), Quantity: it.Qty, Price: it.Price,
		}
		if it.TimeInForce != "" && orderType == "LIMIT" {
			bi.TimeInForce = it.TimeInForce
		}
		if it.TriggerPrice != "" {
			bi.StopPrice = it.TriggerPrice
		}
		if it.ReduceOnly {
			bi.ReduceOnly = "true"
		}
		if it.OrderLinkId != "" {
			bi.NewClientOrderId = truncateClientOrderID(it.OrderLinkId)
		}
		items = append(items, bi)
	}
	encoded, err := json.Marshal(items)
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
	out := make([]trader.BatchPlaceResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, trader.BatchPlaceResult{
			OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId,
			Code: r.Code, Msg: r.Msg,
		})
	}
	return out, nil
}

// CancelOrderBatch cancels a batch of orders via DELETE /fapi/v1/batchOrders, which is
// scoped to a single symbol per call (the "symbol" param takes exactly one value) — unlike
// Bybit's batch-cancel endpoint, Binance has no way to mix symbols in one request. This
// implementation uses req.Request[0].Symbol for the whole call; if req.Request ever
// contains items for more than one symbol, every orderId is sent against that first
// symbol, which is wrong for items belonging to a different symbol. No current caller
// builds a multi-symbol batch, so this is left as a documented limitation rather than
// implemented as a per-symbol grouping/multiple-requests solution.
func (e *BinanceExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	if len(req.Request) == 0 {
		return nil
	}
	symbol := req.Request[0].Symbol
	ids := make([]string, 0, len(req.Request))
	for _, it := range req.Request {
		ids = append(ids, it.OrderId)
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = doSignedDELETE(ctx, e.creds, "/fapi/v1/batchOrders", url.Values{
		"symbol":      {symbol},
		"orderIdList": {string(encoded)},
	})
	return err
}
