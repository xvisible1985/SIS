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
// stop" via OrderFilter="StopOrder" + a nonempty TriggerPrice; Binance has no such
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
	params := url.Values{
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {binanceOrderType(req)},
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
	if req.ReduceOnly {
		params.Set("reduceOnly", "true")
	}
	if req.TimeInForce != "" && binanceOrderType(req) == "LIMIT" {
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
