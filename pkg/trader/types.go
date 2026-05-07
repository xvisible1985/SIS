package trader

import "time"

type Credentials struct {
	APIKey    string
	SecretKey string
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
}

type Order struct {
	OrderId      string `json:"orderId"`
	OrderLinkId  string `json:"orderLinkId"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`
	OrderType    string `json:"orderType"`
	Price        string `json:"price"`
	Qty          string `json:"qty"`
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
}
