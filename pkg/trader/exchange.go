package trader

import (
	"context"
	"errors"
	"time"
)

// ErrWhitelistNotSupported is returned by Exchange.FetchWhitelistedIPs when the
// exchange's own API has no endpoint exposing its API key's actual IP whitelist —
// currently Binance: GET /sapi/v1/account/apiRestrictions only reports WHETHER IP
// restriction is enabled (ipRestrict bool), never which IPs, so there is nothing to
// persist into exchange_accounts.whitelisted_ips for a Binance account via any API call.
var ErrWhitelistNotSupported = errors.New("trader: exchange does not expose its API key's IP whitelist via its API")

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

	// GetInstrumentInfo returns the exchange-enforced tick/step/minimum-order rules for
	// a symbol — used throughout pkg/strategy (cycle.go) to round every qty/price it
	// sends before placing an order. Added by Plan #4b/#4c: GetInstrumentInfo (the free
	// function below, Bybit-only) used to be called directly by every strategy-engine
	// call site regardless of the account's actual exchange — on a Binance account that
	// meant either an outright failed call (Bybit rejecting a signature made with a
	// Binance key) or, worse, silently falling through to an unrounded qty/price and a
	// live "Precision is over the maximum defined for this asset" order rejection.
	GetInstrumentInfo(ctx context.Context, category, symbol string) (InstrumentInfo, error)

	// --- Added by Plan #4d: a full audit (2026-10-16) found several more strategy-
	// engine/reconcile call sites still calling bybit.go's free functions directly with
	// an account's creds, regardless of its actual exchange — the exact same class of
	// bug GetInstrumentInfo above was added to fix, just not caught by that earlier
	// pass. Every method below closes one of those gaps.

	// FetchOrderByLinkId/FetchOrderById look up a single order by our own clientOrderId
	// or the exchange's orderId — used to idempotently check "did this order we placed
	// actually fill" without re-placing it. Checks active orders first, falls back to
	// history; returns (order, stillOpen, error).
	FetchOrderByLinkId(ctx context.Context, category, symbol, orderLinkId string) (Order, bool, error)
	FetchOrderById(ctx context.Context, category, symbol, orderId string) (Order, bool, error)

	// FetchOpenOrders returns every open order across the whole account (not scoped to
	// one symbol) — reconcile.go's safety sweep uses this once per tick to check every
	// strategy's expected orders against exchange reality in one call.
	FetchOpenOrders(ctx context.Context) ([]Order, error)

	// GetAccountMarginInfo returns account-wide equity/available balance/maintenance-
	// margin-rate — risk.go's risk-gate monitor polls this to detect an account getting
	// close to liquidation across ALL of its positions combined, not just one strategy's.
	GetAccountMarginInfo(ctx context.Context) (equity, available, mmRatePct float64, err error)

	// SetTrailingStop configures (or clears, TrailingStop="0") a trailing stop for the
	// current position. The two exchanges model this completely differently: Bybit
	// exposes it as a position-level attribute (price-distance based, set via
	// /v5/position/set-trading-stop — no order object involved at all); Binance has no
	// such position attribute, only a genuine TRAILING_STOP_MARKET order (percentage-
	// based callback rate) that must be cancelled and re-placed to "update". The request
	// carries both shapes — TrailingStop/ActivePrice for Bybit, CallbackRatePct/Side/Qty/
	// ClientOrderID for Binance — each implementation reads only what it needs.
	SetTrailingStop(ctx context.Context, req TrailingStopRequest) error

	// --- Added 2026-10-17: pkg/trader.Syncer (a background executions/order-history
	// backfill, entirely separate from pkg/strategy/services/api-gateway's Plan #4d
	// audit) was still calling Bybit-only free functions directly for every account
	// regardless of exchange — found live via a Binance account's logs showing
	// "syncer: ... bybit: retCode=10003: You are not authorized...".

	// FetchAccountExecutions returns every execution/fill across the account in
	// [since, now] in one call (pagination/windowing handled internally) — used by
	// pkg/trader.Syncer to backfill trader_executions. category selects Bybit's
	// linear/inverse/spot; Binance (USDⓈ-M futures only) ignores it.
	FetchAccountExecutions(ctx context.Context, category string, since time.Time) ([]Execution, error)

	// FetchAccountOrderHistory is FetchAccountExecutions' counterpart for order history
	// (refreshing trader_orders' status/fill fields for our own orders).
	FetchAccountOrderHistory(ctx context.Context, category string, since time.Time) ([]Order, error)

	// FetchWhitelistedIPs returns the account's exchange-API-key IP whitelist, for
	// persisting into exchange_accounts.whitelisted_ips — or ErrWhitelistNotSupported
	// if the exchange's API has no endpoint exposing it at all (see that error's doc).
	FetchWhitelistedIPs(ctx context.Context) ([]string, error)
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

func (e *BybitExchange) GetWalletBalance(ctx context.Context) (equity, available float64, err error) {
	return GetWalletBalance(ctx, e.creds)
}

func (e *BybitExchange) SetLeverage(ctx context.Context, req LeverageRequest) error {
	return SetLeverage(ctx, e.creds, req)
}

func (e *BybitExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	return SwitchPositionMode(ctx, e.creds, category, symbol, mode)
}

func (e *BybitExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	return FetchMarkPrice(ctx, e.creds, category, symbol)
}

func (e *BybitExchange) GetInstrumentInfo(ctx context.Context, category, symbol string) (InstrumentInfo, error) {
	return GetInstrumentInfo(ctx, e.creds, category, symbol)
}

func (e *BybitExchange) PlaceOrderREST(ctx context.Context, req OrderRequest) (OrderResult, error) {
	return PlaceOrder(ctx, e.creds, req)
}

func (e *BybitExchange) CancelOrderREST(ctx context.Context, req CancelRequest) error {
	return CancelOrder(ctx, e.creds, req)
}

func (e *BybitExchange) CancelAllOrders(ctx context.Context, req CancelAllRequest) error {
	return CancelAllOrders(ctx, e.creds, req)
}

func (e *BybitExchange) FetchOrderByLinkId(ctx context.Context, category, symbol, orderLinkId string) (Order, bool, error) {
	return FetchOrderByLinkId(ctx, e.creds, category, symbol, orderLinkId)
}

func (e *BybitExchange) FetchOrderById(ctx context.Context, category, symbol, orderId string) (Order, bool, error) {
	return FetchOrderById(ctx, e.creds, category, symbol, orderId)
}

func (e *BybitExchange) FetchOpenOrders(ctx context.Context) ([]Order, error) {
	return FetchOpenOrders(ctx, e.creds)
}

func (e *BybitExchange) GetAccountMarginInfo(ctx context.Context) (equity, available, mmRatePct float64, err error) {
	return GetAccountMarginInfo(ctx, e.creds)
}

func (e *BybitExchange) SetTrailingStop(ctx context.Context, req TrailingStopRequest) error {
	return SetTradingStop(ctx, e.creds, req)
}

func (e *BybitExchange) FetchAccountExecutions(ctx context.Context, category string, since time.Time) ([]Execution, error) {
	return FetchAccountExecutionsSince(ctx, e.creds, category, since)
}

func (e *BybitExchange) FetchAccountOrderHistory(ctx context.Context, category string, since time.Time) ([]Order, error) {
	return FetchAccountOrderHistorySince(ctx, e.creds, category, since)
}

func (e *BybitExchange) FetchWhitelistedIPs(ctx context.Context) ([]string, error) {
	return FetchAccountWhitelistedIPs(ctx, e.creds)
}

// var _ Exchange = (*BybitExchange)(nil) fails to compile if BybitExchange ever stops
// satisfying Exchange — the cheapest possible regression guard for this file.
var _ Exchange = (*BybitExchange)(nil)
