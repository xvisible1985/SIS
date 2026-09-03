package trader

import (
	"context"
	"time"
)

// Exchange is every trading operation pkg/strategy and services/api-gateway perform
// against an account, resolved once per account (via account.Exchange) instead of the
// bare free functions in bybit.go being called by name. BybitExchange (this file) is
// the first implementation — a pure reorganization of already-working code, no
// behavior change. A Binance implementation is added in a later plan.
type Exchange interface {
	// WS-backed order placement/cancellation — the live strategy engine's own path
	// (grid/matrix entries, TP/SL, per-level orders). Backed by *TradeStream for Bybit.
	PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req CancelRequest) error
	CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error

	// REST-only order placement/cancellation — used by user-facing/admin handlers
	// (manual order entry, rescue engine, hedge engine's own direct closes) that don't
	// hold a live trade-WS connection for the account.
	PlaceOrderREST(ctx context.Context, req OrderRequest) (OrderResult, error)
	CancelOrderREST(ctx context.Context, req CancelRequest) error
	CancelAllOrders(ctx context.Context, req CancelAllRequest) error

	FetchPositions(ctx context.Context) ([]Position, error)
	FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error)

	FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]ClosedPnl, error)
	FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]ClosedPnl, error)

	GetWalletBalance(ctx context.Context) (equity, available float64, err error)
	SetLeverage(ctx context.Context, req LeverageRequest) error
	SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error

	GetMarkPrice(ctx context.Context, category, symbol string) (float64, error)
}

// wsOrderClient is the subset of *TradeStream's methods BybitExchange needs for the
// WS-backed order operations. Exists only so exchange_test.go can substitute a fake —
// *TradeStream (pkg/trader/trade_ws.go) already has this exact method set and needs no
// changes to satisfy it.
type wsOrderClient interface {
	PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error)
	PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error)
	CancelOrder(ctx context.Context, req CancelRequest) error
	CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error
}

// BybitExchange implements Exchange by delegating to the existing free functions in
// bybit.go (REST) and a wsOrderClient (WS, normally *TradeStream). No new HTTP/WS
// logic — every method here is a direct pass-through to code that already works in
// production.
type BybitExchange struct {
	creds Credentials
	ws    wsOrderClient
}

// NewBybitExchange builds a BybitExchange. ws is normally the account's own
// *TradeStream (see pkg/trader/trade_ws.go), already constructed with the same creds.
func NewBybitExchange(creds Credentials, ws wsOrderClient) *BybitExchange {
	return &BybitExchange{creds: creds, ws: ws}
}

func (e *BybitExchange) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return e.ws.PlaceOrder(ctx, req)
}

func (e *BybitExchange) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	return e.ws.PlaceOrderBatch(ctx, req)
}

func (e *BybitExchange) CancelOrder(ctx context.Context, req CancelRequest) error {
	return e.ws.CancelOrder(ctx, req)
}

func (e *BybitExchange) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	return e.ws.CancelOrderBatch(ctx, req)
}

func (e *BybitExchange) FetchPositions(ctx context.Context) ([]Position, error) {
	return FetchPositions(ctx, e.creds)
}

func (e *BybitExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]Order, error) {
	return FetchOpenOrdersForSymbolAll(ctx, e.creds, category, symbol)
}

func (e *BybitExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]ClosedPnl, error) {
	return FetchClosedPnlForSymbol(ctx, e.creds, category, symbol, limit)
}

func (e *BybitExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]ClosedPnl, error) {
	return FetchRecentClosedPnl(ctx, e.creds, category, since)
}
