package trader

import "time"

type Credentials struct {
	APIKey         string
	SecretKey      string
	AccountID      string   // exchange_accounts.id; используется для whitelist-aware выбора прокси
	WhitelistedIPs []string // exchange_accounts.whitelisted_ips; nil/пусто = ограничений на бирже нет
}

type OrderRequest struct {
	Symbol           string `json:"symbol"`
	Category         string `json:"category"`
	Side             string `json:"side"`
	OrderType        string `json:"orderType"`
	Qty              string `json:"qty"`
	Price            string `json:"price,omitempty"`
	TriggerPrice     string `json:"triggerPrice,omitempty"`
	TriggerBy        string `json:"triggerBy,omitempty"`
	TriggerDirection int    `json:"triggerDirection,omitempty"`
	TimeInForce      string `json:"timeInForce,omitempty"`
	OrderFilter      string `json:"orderFilter,omitempty"`
	ReduceOnly       bool   `json:"reduceOnly"`
	PositionIdx      int    `json:"positionIdx"`
	OrderLinkId      string `json:"orderLinkId,omitempty"`
}

type CancelRequest struct {
	Symbol      string `json:"symbol"`
	Category    string `json:"category"`
	OrderId     string `json:"orderId,omitempty"`
	OrderLinkId string `json:"orderLinkId,omitempty"`
	OrderFilter string `json:"orderFilter,omitempty"`
}

type CancelAllRequest struct {
	Category string `json:"category"`
	Symbol   string `json:"symbol,omitempty"`
}

// TradingStopRequest sets or clears Bybit's native trailing stop on an open position.
// TrailingStop is the callback distance in price units (e.g. "0.0003").
// ActivePrice is the trigger price at which trailing begins (empty = activate immediately).
// Pass TrailingStop="0" to remove an existing trailing stop.
type TradingStopRequest = TrailingStopRequest

// TrailingStopRequest configures (or clears) a trailing stop — see trader.Exchange's
// SetTrailingStop doc comment for why Bybit and Binance need such different fields from
// the same request. Bybit (position attribute) reads Category/Symbol/TrailingStop/
// ActivePrice/PositionIdx. Binance (TRAILING_STOP_MARKET order) additionally needs
// Side/Qty/CallbackRatePct/ClientOrderID.
type TrailingStopRequest struct {
	Category     string `json:"category"`
	Symbol       string `json:"symbol"`
	TrailingStop string `json:"trailingStop"`          // Bybit: price distance; "0" to clear
	ActivePrice  string `json:"activePrice,omitempty"` // activation price; omit to activate immediately
	PositionIdx  int    `json:"positionIdx"`

	// Binance-only fields — ignored by BybitExchange.SetTrailingStop.
	Side            string  // closing side: "Buy" or "Sell" — which way the trailing-stop order itself trades
	Qty             string  // position size to protect; Binance needs an explicit order quantity
	CallbackRatePct float64 // Binance's percentage callback rate (0.1–5.0), vs Bybit's absolute price distance
	ClientOrderID   string  // stable ID so Binance can find and cancel its OWN prior trailing-stop order before replacing it — Binance can't amend one in place the way Bybit's position attribute updates implicitly
}

// BatchOrderItem is a single order in a batch place request.
// Category is omitted — it lives at the top level of BatchPlaceRequest.
type BatchOrderItem struct {
	Symbol           string `json:"symbol"`
	Side             string `json:"side"`
	OrderType        string `json:"orderType"`
	Qty              string `json:"qty"`
	Price            string `json:"price,omitempty"`
	TriggerPrice     string `json:"triggerPrice,omitempty"`
	TriggerDirection int    `json:"triggerDirection,omitempty"`
	OrderFilter      string `json:"orderFilter,omitempty"`
	TimeInForce      string `json:"timeInForce,omitempty"`
	ReduceOnly       bool   `json:"reduceOnly,omitempty"`
	PositionIdx      int    `json:"positionIdx"`
	OrderLinkId      string `json:"orderLinkId,omitempty"`
}

type BatchPlaceRequest struct {
	Category string           `json:"category"`
	Request  []BatchOrderItem `json:"request"`
}

type BatchPlaceResult struct {
	OrderId     string
	OrderLinkId string
	Code        int
	Msg         string
}

type BatchCancelItem struct {
	Symbol      string `json:"symbol"`
	OrderId     string `json:"orderId,omitempty"`
	OrderFilter string `json:"orderFilter,omitempty"`
}

type BatchCancelRequest struct {
	Category string            `json:"category"`
	Request  []BatchCancelItem `json:"request"`
}

type LeverageRequest struct {
	Symbol       string `json:"symbol"`
	Category     string `json:"category"`
	BuyLeverage  string `json:"buyLeverage"`
	SellLeverage string `json:"sellLeverage"`
}

type OrderResult struct {
	OrderId     string `json:"orderId"`
	OrderLinkId string `json:"orderLinkId"`
}

type Position struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Size          string `json:"size"`
	EntryPrice    string `json:"avgPrice"`
	MarkPrice     string `json:"markPrice"`
	LiqPrice      string `json:"liqPrice"`
	UnrealisedPnl string `json:"unrealisedPnl"`
	Leverage      string `json:"leverage"`
	PositionIdx   int    `json:"positionIdx"`
	Category      string `json:"category"`
	PositionIM    string `json:"positionIM"`
}

type Order struct {
	OrderId      string `json:"orderId"`
	OrderLinkId  string `json:"orderLinkId"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`
	OrderType    string `json:"orderType"`
	Price        string `json:"price"`
	Qty          string `json:"qty"`
	AvgPrice     string `json:"avgPrice"`
	CumExecQty   string `json:"cumExecQty"`
	CumExecFee   string `json:"cumExecFee"`
	OrderStatus  string `json:"orderStatus"`
	TriggerPrice string `json:"triggerPrice"`
	Category     string `json:"category"`
	OrderFilter  string `json:"orderFilter"`
	CreatedTime  string `json:"createdTime"`
}

type ClosedPnl struct {
	Symbol        string `json:"symbol"`
	OrderId       string `json:"orderId"`      // closing order ID — for exact match with tpOrderID/slOrderID
	OrderLinkId   string `json:"orderLinkId"`  // our custom link ID
	Side          string `json:"side"`
	Qty           string `json:"qty"`
	AvgEntryPrice string `json:"avgEntryPrice"`
	AvgExitPrice  string `json:"avgExitPrice"`
	CumEntryValue string `json:"cumEntryValue"`
	CumExitValue  string `json:"cumExitValue"`
	ClosedPnl     string `json:"closedPnl"`
	Leverage      string `json:"leverage"`
	CreatedTime   string `json:"createdTime"`
	UpdatedTime   string `json:"updatedTime"`
	Category      string `json:"category"`
	// Fee is the exchange-reported commission for this closing order, when the exchange
	// gives it to us directly (currently: Binance only, from userTrades' own
	// `commission` field — see pkg/trader/binance/exchange.go). Left empty for Bybit,
	// whose own closed-pnl record doesn't include commission as a separate field; the
	// frontend (PnlTable.tsx) falls back to its existing gross-minus-closedPnl estimate
	// whenever Fee is empty, preserving Bybit's exact prior behavior.
	Fee string `json:"fee,omitempty"`
}

type Execution struct {
	ExecId      string    `json:"execId"`
	OrderId     string    `json:"orderId"`
	OrderLinkId string    `json:"orderLinkId"`
	Symbol      string    `json:"symbol"`
	Side        string    `json:"side"`
	ExecType    string    `json:"execType"`
	ExecQty     string    `json:"execQty"`
	ExecPrice   string    `json:"execPrice"`
	ExecValue   string    `json:"execValue"`
	ExecFee     string    `json:"execFee"`
	FeeRate     string    `json:"feeRate"`
	IsMaker     bool      `json:"isMaker"`
	ExecTime    time.Time `json:"-"`
	ExecTimeMs  string    `json:"execTime"`
	Category    string    `json:"category"`
	PositionIdx int       `json:"positionIdx"`
}
