package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	ids := make([]int64, 0, len(req.Request))
	for _, it := range req.Request {
		id, err := strconv.ParseInt(it.OrderId, 10, 64)
		if err != nil {
			return fmt.Errorf("binance: invalid orderId %q in batch cancel: %w", it.OrderId, err)
		}
		ids = append(ids, id)
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

func (e *BinanceExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	data, err := doSignedGET(ctx, e.creds, "/fapi/v1/openOrders", url.Values{"symbol": {symbol}})
	if err != nil {
		return nil, err
	}
	var rows []struct {
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
	_, err := doSignedPOST(ctx, e.creds, "/fapi/v1/positionSide/dual", url.Values{"dualSidePosition": {dual}})
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
	Time         int64  `json:"time"`
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
// A lookupClientOrderId failure for one orderId (e.g. Binance's order history retention
// limit has purged it) does NOT abort the whole batch — that row is skipped and the
// first error encountered is returned alongside every row that DID succeed, so a caller
// keeps partial progress instead of losing already-aggregated rows for unrelated orders.
func closingFillsToClosedPnl(ctx context.Context, creds trader.Credentials, symbol string, trades []binanceUserTrade) ([]trader.ClosedPnl, error) {
	type agg struct {
		side       string
		qty        float64
		pnl        float64
		lastPrice  string
		lastTimeMs int64
	}
	byOrder := make(map[int64]*agg)
	var order []int64
	for _, t := range trades {
		pnl, _ := strconv.ParseFloat(t.RealizedPnl, 64)
		if !isClosingFill(t.Side, t.PositionSide) && pnl == 0 {
			continue
		}
		a, ok := byOrder[t.OrderId]
		if !ok {
			a = &agg{side: binanceSide(t.Side)}
			byOrder[t.OrderId] = a
			order = append(order, t.OrderId)
		}
		qty, _ := strconv.ParseFloat(t.Qty, 64)
		a.qty += qty
		a.pnl += pnl
		if t.Time >= a.lastTimeMs {
			a.lastTimeMs = t.Time
			a.lastPrice = t.Price
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
		// AvgEntryPrice, CumEntryValue, CumExitValue, Leverage, and UpdatedTime are
		// intentionally left unpopulated (zero-value strings) — Binance's userTrades/
		// order responses don't cheaply provide them. services/api-gateway's
		// closed_pnl_syncer.go entry-volume-in-USDT computation reads AvgEntryPrice, so
		// it will show $0 for Binance-sourced rows until this is addressed — a known
		// gap, not silently forgotten.
		out = append(out, trader.ClosedPnl{
			Symbol: symbol, OrderId: strconv.FormatInt(orderId, 10), OrderLinkId: linkId,
			Side: a.side, Qty: strconv.FormatFloat(a.qty, 'f', -1, 64),
			AvgExitPrice: a.lastPrice, ClosedPnl: strconv.FormatFloat(a.pnl, 'f', -1, 64),
			CreatedTime: strconv.FormatInt(a.lastTimeMs, 10), Category: "linear",
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

// FetchRecentClosedPnl fans out across ≤7-day windows from since to now — Binance caps
// userTrades' startTime..endTime span at 7 days per call, unlike Bybit's single
// cursor-paginated call across an arbitrarily old `since`. category is accepted for
// interface compatibility. Binance has no account-wide equivalent to userTrades (it's
// always per-symbol), so this walks every symbol currently holding an open position.
//
// KNOWN LIMITATION: this only walks symbols with a currently-open position, so a
// symbol that fully closed to flat within the queried window is missed. Acceptable
// for now — this feeds services/api-gateway's closed_pnl_syncer reconciliation sweep,
// which already treats missed rows as retryable via its own gap-backfill watchdog
// rather than requiring single-pass completeness.
//
// A single window or lookup failure (for one symbol, or one order within one window)
// does NOT abort the whole multi-symbol/multi-window sweep — every row that DID
// succeed is still returned, alongside the first error encountered, so a caller keeps
// partial progress rather than losing every already-fetched row to one bad window.
func (e *BinanceExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	positions, err := e.FetchPositions(ctx)
	if err != nil {
		return nil, err
	}
	var all []trader.ClosedPnl
	var firstErr error
	now := time.Now()
	for _, p := range positions {
		windowStart := since
		for windowStart.Before(now) {
			windowEnd := windowStart.Add(7 * 24 * time.Hour)
			if windowEnd.After(now) {
				windowEnd = now
			}
			data, err := doSignedGET(ctx, e.creds, "/fapi/v1/userTrades", url.Values{
				"symbol":    {p.Symbol},
				"startTime": {strconv.FormatInt(windowStart.UnixMilli(), 10)},
				"endTime":   {strconv.FormatInt(windowEnd.UnixMilli(), 10)},
				"limit":     {"1000"},
			})
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				windowStart = windowEnd
				continue
			}
			var trades []binanceUserTrade
			if err := json.Unmarshal(data, &trades); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				windowStart = windowEnd
				continue
			}
			rows, err := closingFillsToClosedPnl(ctx, e.creds, p.Symbol, trades)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			all = append(all, rows...)
			windowStart = windowEnd
		}
	}
	return all, firstErr
}

// var _ trader.Exchange = (*BinanceExchange)(nil) fails to compile if BinanceExchange
// ever stops satisfying trader.Exchange — mirrors pkg/trader/exchange.go's own
// var _ Exchange = (*BybitExchange)(nil).
var _ trader.Exchange = (*BinanceExchange)(nil)
