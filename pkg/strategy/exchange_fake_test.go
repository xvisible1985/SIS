package strategy

import (
	"context"
	"sync"
	"time"

	"sis/pkg/trader"
)

// respQueue holds canned (value, error) pairs for one fakeExchange method, consumed in
// call order. Once exhausted it keeps replaying the last pushed pair, so a test that only
// cares about the first call's response doesn't have to pre-fill one entry per expected
// call. An empty queue (nothing pushed) always returns the zero value and a nil error.
type respQueue[T any] struct {
	items []T
	errs  []error
	next  int
}

func (q *respQueue[T]) push(item T, err error) {
	q.items = append(q.items, item)
	q.errs = append(q.errs, err)
}

func (q *respQueue[T]) take() (T, error) {
	var zero T
	if len(q.items) == 0 {
		return zero, nil
	}
	i := q.next
	if i >= len(q.items) {
		i = len(q.items) - 1
	} else {
		q.next++
	}
	return q.items[i], q.errs[i]
}

type walletResp struct {
	equity    float64
	available float64
}

type openOrdersReq struct {
	Category string
	Symbol   string
}

type closedPnlReq struct {
	Category string
	Symbol   string
	Limit    int
}

type recentClosedPnlReq struct {
	Category string
	Since    time.Time
}

type positionModeReq struct {
	Category string
	Symbol   string
	Mode     int
}

type markPriceReq struct {
	Category string
	Symbol   string
}

type instrumentInfoReq struct {
	Category string
	Symbol   string
}

type orderLookupReq struct {
	Category string
	Symbol   string
	ID       string // orderLinkId or orderId, depending on which method was called
}

type orderLookupResp struct {
	order     trader.Order
	stillOpen bool
}

type marginInfoResp struct {
	equity, available, mmRatePct float64
}

type accountHistoryReq struct {
	Category string
	Since    time.Time
}

// fakeExchange is a hand-written stand-in for trader.Exchange, used only by pkg/strategy
// tests (no live network/WS). Modeled on pkg/trader/exchange_test.go's fakeWSOrderClient,
// but extended with a call LOG (slice, not just the last call) and a response QUEUE (not
// just one canned value) per method — pkg/strategy's flows call PlaceOrder/FetchPositions
// more than once in a single test (cancel-then-place, or a WS-cache staleness fallback to
// FetchPositions), and tests need to assert on the exact fields of each call, in order —
// not just that a call happened at some point with some content.
type fakeExchange struct {
	mu sync.Mutex

	placeOrderCalls []trader.OrderRequest
	placeOrderQ     respQueue[trader.OrderResult]

	placeOrderBatchCalls []trader.BatchPlaceRequest
	placeOrderBatchQ     respQueue[[]trader.BatchPlaceResult]

	cancelOrderCalls []trader.CancelRequest
	cancelOrderQ     respQueue[struct{}]

	cancelOrderBatchCalls []trader.BatchCancelRequest
	cancelOrderBatchQ     respQueue[struct{}]

	placeOrderRESTCalls []trader.OrderRequest
	placeOrderRESTQ     respQueue[trader.OrderResult]

	cancelOrderRESTCalls []trader.CancelRequest
	cancelOrderRESTQ     respQueue[struct{}]

	cancelAllOrdersCalls []trader.CancelAllRequest
	cancelAllOrdersQ     respQueue[struct{}]

	fetchPositionsCalls int
	fetchPositionsQ     respQueue[[]trader.Position]

	fetchOpenOrdersCalls []openOrdersReq
	fetchOpenOrdersQ     respQueue[[]trader.Order]

	fetchClosedPnlCalls []closedPnlReq
	fetchClosedPnlQ     respQueue[[]trader.ClosedPnl]

	fetchRecentClosedPnlCalls []recentClosedPnlReq
	fetchRecentClosedPnlQ     respQueue[[]trader.ClosedPnl]

	getWalletBalanceCalls int
	getWalletBalanceQ     respQueue[walletResp]

	setLeverageCalls []trader.LeverageRequest
	setLeverageQ     respQueue[struct{}]

	switchPositionModeCalls []positionModeReq
	switchPositionModeQ     respQueue[struct{}]

	getMarkPriceCalls []markPriceReq
	getMarkPriceQ     respQueue[float64]

	getInstrumentInfoCalls []instrumentInfoReq
	getInstrumentInfoQ     respQueue[trader.InstrumentInfo]

	fetchOrderByLinkIdCalls []orderLookupReq
	fetchOrderByLinkIdQ     respQueue[orderLookupResp]

	fetchOrderByIdCalls []orderLookupReq
	fetchOrderByIdQ     respQueue[orderLookupResp]

	fetchOpenOrdersAccountCalls int
	fetchOpenOrdersAccountQ     respQueue[[]trader.Order]

	getAccountMarginInfoCalls int
	getAccountMarginInfoQ     respQueue[marginInfoResp]

	setTrailingStopCalls []trader.TrailingStopRequest
	setTrailingStopQ     respQueue[struct{}]

	fetchAccountExecutionsCalls []accountHistoryReq
	fetchAccountExecutionsQ     respQueue[[]trader.Execution]

	fetchAccountOrderHistoryCalls []accountHistoryReq
	fetchAccountOrderHistoryQ     respQueue[[]trader.Order]

	fetchWhitelistedIPsCalls int
	fetchWhitelistedIPsQ     respQueue[[]string]
}

var _ trader.Exchange = (*fakeExchange)(nil)

func (f *fakeExchange) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderCalls = append(f.placeOrderCalls, req)
	return f.placeOrderQ.take()
}

func (f *fakeExchange) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderBatchCalls = append(f.placeOrderBatchCalls, req)
	return f.placeOrderBatchQ.take()
}

func (f *fakeExchange) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderCalls = append(f.cancelOrderCalls, req)
	_, err := f.cancelOrderQ.take()
	return err
}

func (f *fakeExchange) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderBatchCalls = append(f.cancelOrderBatchCalls, req)
	_, err := f.cancelOrderBatchQ.take()
	return err
}

func (f *fakeExchange) PlaceOrderREST(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placeOrderRESTCalls = append(f.placeOrderRESTCalls, req)
	return f.placeOrderRESTQ.take()
}

func (f *fakeExchange) CancelOrderREST(ctx context.Context, req trader.CancelRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelOrderRESTCalls = append(f.cancelOrderRESTCalls, req)
	_, err := f.cancelOrderRESTQ.take()
	return err
}

func (f *fakeExchange) CancelAllOrders(ctx context.Context, req trader.CancelAllRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelAllOrdersCalls = append(f.cancelAllOrdersCalls, req)
	_, err := f.cancelAllOrdersQ.take()
	return err
}

func (f *fakeExchange) FetchPositions(ctx context.Context) ([]trader.Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchPositionsCalls++
	return f.fetchPositionsQ.take()
}

func (f *fakeExchange) FetchOpenOrdersForSymbolAll(ctx context.Context, category, symbol string) ([]trader.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchOpenOrdersCalls = append(f.fetchOpenOrdersCalls, openOrdersReq{Category: category, Symbol: symbol})
	return f.fetchOpenOrdersQ.take()
}

func (f *fakeExchange) FetchClosedPnlForSymbol(ctx context.Context, category, symbol string, limit int) ([]trader.ClosedPnl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchClosedPnlCalls = append(f.fetchClosedPnlCalls, closedPnlReq{Category: category, Symbol: symbol, Limit: limit})
	return f.fetchClosedPnlQ.take()
}

func (f *fakeExchange) FetchRecentClosedPnl(ctx context.Context, category string, since time.Time) ([]trader.ClosedPnl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchRecentClosedPnlCalls = append(f.fetchRecentClosedPnlCalls, recentClosedPnlReq{Category: category, Since: since})
	return f.fetchRecentClosedPnlQ.take()
}

func (f *fakeExchange) GetWalletBalance(ctx context.Context) (float64, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getWalletBalanceCalls++
	resp, err := f.getWalletBalanceQ.take()
	return resp.equity, resp.available, err
}

func (f *fakeExchange) SetLeverage(ctx context.Context, req trader.LeverageRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLeverageCalls = append(f.setLeverageCalls, req)
	_, err := f.setLeverageQ.take()
	return err
}

func (f *fakeExchange) SwitchPositionMode(ctx context.Context, category, symbol string, mode int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switchPositionModeCalls = append(f.switchPositionModeCalls, positionModeReq{Category: category, Symbol: symbol, Mode: mode})
	_, err := f.switchPositionModeQ.take()
	return err
}

func (f *fakeExchange) GetMarkPrice(ctx context.Context, category, symbol string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMarkPriceCalls = append(f.getMarkPriceCalls, markPriceReq{Category: category, Symbol: symbol})
	return f.getMarkPriceQ.take()
}

func (f *fakeExchange) GetInstrumentInfo(ctx context.Context, category, symbol string) (trader.InstrumentInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getInstrumentInfoCalls = append(f.getInstrumentInfoCalls, instrumentInfoReq{Category: category, Symbol: symbol})
	return f.getInstrumentInfoQ.take()
}

func (f *fakeExchange) FetchOrderByLinkId(ctx context.Context, category, symbol, orderLinkId string) (trader.Order, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchOrderByLinkIdCalls = append(f.fetchOrderByLinkIdCalls, orderLookupReq{Category: category, Symbol: symbol, ID: orderLinkId})
	resp, err := f.fetchOrderByLinkIdQ.take()
	return resp.order, resp.stillOpen, err
}

func (f *fakeExchange) FetchOrderById(ctx context.Context, category, symbol, orderId string) (trader.Order, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchOrderByIdCalls = append(f.fetchOrderByIdCalls, orderLookupReq{Category: category, Symbol: symbol, ID: orderId})
	resp, err := f.fetchOrderByIdQ.take()
	return resp.order, resp.stillOpen, err
}

func (f *fakeExchange) FetchOpenOrders(ctx context.Context) ([]trader.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchOpenOrdersAccountCalls++
	return f.fetchOpenOrdersAccountQ.take()
}

func (f *fakeExchange) GetAccountMarginInfo(ctx context.Context) (equity, available, mmRatePct float64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getAccountMarginInfoCalls++
	resp, err := f.getAccountMarginInfoQ.take()
	return resp.equity, resp.available, resp.mmRatePct, err
}

func (f *fakeExchange) SetTrailingStop(ctx context.Context, req trader.TrailingStopRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setTrailingStopCalls = append(f.setTrailingStopCalls, req)
	_, err := f.setTrailingStopQ.take()
	return err
}

func (f *fakeExchange) FetchAccountExecutions(ctx context.Context, category string, since time.Time) ([]trader.Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchAccountExecutionsCalls = append(f.fetchAccountExecutionsCalls, accountHistoryReq{Category: category, Since: since})
	return f.fetchAccountExecutionsQ.take()
}

func (f *fakeExchange) FetchAccountOrderHistory(ctx context.Context, category string, since time.Time) ([]trader.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchAccountOrderHistoryCalls = append(f.fetchAccountOrderHistoryCalls, accountHistoryReq{Category: category, Since: since})
	return f.fetchAccountOrderHistoryQ.take()
}

func (f *fakeExchange) FetchWhitelistedIPs(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchWhitelistedIPsCalls++
	return f.fetchWhitelistedIPsQ.take()
}
