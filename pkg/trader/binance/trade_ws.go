package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/proxy"
	"sis/pkg/trader"
)

// binanceWSIntParams is the fixed set of WS request fields Binance expects as JSON
// integers rather than strings — confirmed via Binance's own documented example
// payloads (INT-typed params like timestamp/recvWindow are unquoted; DECIMAL/STRING
// params like quantity/price/symbol stay quoted, exactly as REST's form-encoded body
// already treats them as strings for signing purposes).
var binanceWSIntParams = map[string]bool{
	"timestamp":  true,
	"recvWindow": true,
}

// binanceWSBoolParams is the (currently singleton) set of WS request fields Binance
// expects as a JSON boolean rather than a string — REST sends "true" as a literal
// string in the form-encoded body; the WS JSON envelope expects a real `true`.
var binanceWSBoolParams = map[string]bool{
	"reduceOnly": true,
}

// wsParamsToJSON converts a signed url.Values (built and signed exactly like a REST
// request — see signParams in client.go) into the map[string]any the WS JSON envelope's
// "params" object needs, converting only the fields Binance expects as non-string JSON
// types. The signature itself is unaffected: it's computed over params.Encode() (the
// all-strings query form) BEFORE this conversion ever runs, so signing math never needs
// to know about WS-specific JSON typing at all.
func wsParamsToJSON(params url.Values) map[string]any {
	out := make(map[string]any, len(params))
	for k := range params {
		v := params.Get(k)
		switch {
		case binanceWSIntParams[k]:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				out[k] = n
				continue
			}
		case binanceWSBoolParams[k]:
			if b, err := strconv.ParseBool(v); err == nil {
				out[k] = b
				continue
			}
		}
		out[k] = v
	}
	return out
}

// binanceTradeWS is the Binance USDⓈ-M Futures WebSocket API endpoint for order
// placement/cancellation — a separate service from both the market-data streams
// (pkg/signal) and the account/user-data stream (ws.go's binanceWsBase); Binance
// requires these on independent connections.
const binanceTradeWS = "wss://ws-fapi.binance.com/ws-fapi/v1"

// ErrWSUnavailable is returned by BinanceTradeStream's order methods whenever the
// failure is about the CONNECTION (not connected, a permission/auth-shaped rejection, or
// a timeout) rather than a genuine order outcome — callers (BinanceExchange) treat this
// as "retry on REST", and must NEVER retry any other error (a real order
// acceptance/rejection) via a different transport, to avoid a duplicate submission.
var ErrWSUnavailable = errors.New("binance trade ws: unavailable")

// binanceWSResp is one WS API response envelope — see
// developers.binance.com/docs/derivatives/usds-margined-futures/websocket-api-general-info
// for the documented shape: {id, status, result} on success, {id, status, error:{code,msg}}
// on failure.
type binanceWSResp struct {
	Id     string          `json:"id"`
	Status int             `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	} `json:"error"`
}

// BinanceTradeStream maintains a persistent WebSocket to Binance's order-placement WS
// API. Each request is individually signed (HMAC-SHA256, same math as REST — see
// sendReq) and correlated to its response via a client-chosen "id". Deliberately has NO
// REST fallback logic of its own (unlike Bybit's *TradeStream) — see
// ErrWSUnavailable's doc comment; BinanceExchange owns that decision instead.
type BinanceTradeStream struct {
	creds trader.Credentials
	mu    sync.Mutex
	conn  *websocket.Conn // nil when not connected
	seq   int64

	pending    map[string]chan binanceWSResp
	wsDisabled bool // set after a permanent auth/permission error — see binanceWSPermanentlyDenied

	// dialURL overrides binanceTradeWS — test-only (set directly by trade_ws_test.go);
	// the zero value (empty string) means "use the real endpoint".
	dialURL string
}

// NewBinanceTradeStream builds a BinanceTradeStream for one account's credentials. The
// connection isn't established until Run is called.
func NewBinanceTradeStream(creds trader.Credentials) *BinanceTradeStream {
	return &BinanceTradeStream{
		creds:   creds,
		pending: make(map[string]chan binanceWSResp),
	}
}

// Run connects and maintains the trade WS, reconnecting every 5s on failure. Blocks
// until ctx is cancelled — call as `go safeLoop(ctx, "...", ts.Run)`, mirroring
// pkg/trader/trade_ws.go's TradeStream.Run exactly.
func (ts *BinanceTradeStream) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := ts.runOnce(ctx); err != nil {
			log.Printf("binance trade ws: %v, retry in 5s", err)
		}
		ts.mu.Lock()
		ts.conn = nil
		for id, ch := range ts.pending {
			select {
			case ch <- binanceWSResp{Id: id, Status: 0}: // synthetic "disconnected" — sendReq's caller sees a zero-value response and ts.conn==nil decides ErrWSUnavailable
			default:
			}
			delete(ts.pending, id)
		}
		ts.mu.Unlock()
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (ts *BinanceTradeStream) runOnce(ctx context.Context) error {
	dialer, err := proxy.WSDialerFor(ts.creds.WhitelistedIPs)
	if err != nil {
		return err
	}
	target := binanceTradeWS
	if ts.dialURL != "" {
		target = ts.dialURL
	}
	conn, _, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	ts.mu.Lock()
	ts.conn = conn
	ts.mu.Unlock()
	log.Printf("binance trade ws: connected")

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
	// Same ping-deadline handling already proven for the account-data stream (ws.go) —
	// Binance's server-side ping every ~3 minutes must refresh our read deadline, or a
	// perfectly healthy but quiet connection gets torn down for no reason.
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
			select {
			case msgCh <- data:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			conn.Close()
			return nil
		case err := <-errCh:
			return err
		case data := <-msgCh:
			var resp binanceWSResp
			if json.Unmarshal(data, &resp) != nil || resp.Id == "" {
				continue
			}
			ts.mu.Lock()
			ch, ok := ts.pending[resp.Id]
			if ok {
				delete(ts.pending, resp.Id)
			}
			ts.mu.Unlock()
			if ok {
				select {
				case ch <- resp:
				default:
				}
			}
		}
	}
}

// sendReq signs params (identical math to REST's signParams — see client.go), converts
// them to the WS envelope's JSON types (wsParamsToJSON), and sends {id, method, params}
// over the connection, then blocks for the matching response (by id) or a 10s timeout.
// Caller must not hold ts.mu. Returns ErrWSUnavailable (never a transport error value)
// whenever the problem is connection-level rather than a genuine response.
func (ts *BinanceTradeStream) sendReq(ctx context.Context, method string, params url.Values) (binanceWSResp, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("apiKey", ts.creds.APIKey)
	signed := signParams(ts.creds, params)

	ts.mu.Lock()
	conn := ts.conn
	if conn == nil {
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	}
	ts.seq++
	id := strconv.FormatInt(ts.seq, 10)
	ch := make(chan binanceWSResp, 1)
	ts.pending[id] = ch

	msg, _ := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": wsParamsToJSON(signed),
	})
	err := conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	}
	ts.mu.Unlock()

	select {
	case resp := <-ch:
		if resp.Status == 0 {
			// Synthetic disconnect response from Run's cleanup (see Run's comment).
			return binanceWSResp{}, ErrWSUnavailable
		}
		return resp, nil
	case <-time.After(10 * time.Second):
		ts.mu.Lock()
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ErrWSUnavailable
	case <-ctx.Done():
		ts.mu.Lock()
		delete(ts.pending, id)
		ts.mu.Unlock()
		return binanceWSResp{}, ctx.Err()
	}
}

// binanceWSConnDenied reports whether code means the WS request never reached real
// order validation at all — connection/auth/signature/clock-shaped, not a genuine order
// outcome — so falling back to REST for THIS call is safe: nothing was placed or
// rejected on Binance's side yet, so there is no duplicate-submission risk. -2015 is the
// exact code from the 2026-10-16 incident ("Invalid API-key, IP, or permissions");
// -1002 is "not authorized for this endpoint"; -1022/-1021 are a bad signature or a
// timestamp outside recvWindow — Binance's equivalent of pkg/trader/trade_ws.go's
// wsPermDenied (Bybit), just split further below for the permanent-disable decision.
func binanceWSConnDenied(code int) bool {
	switch code {
	case -2015, -1002, -1022, -1021:
		return true
	}
	return false
}

// binanceWSPermanentlyDenied is the narrower subset of binanceWSConnDenied that
// indicates a real, persistent account/key misconfiguration (wrong IP, wrong key, a
// permission the key genuinely lacks) rather than something that could be a one-off
// blip — only -2015/-1002 set wsDisabled permanently. -1022 (bad signature) and -1021
// (timestamp/recvWindow) do NOT: both fall back to REST for this one call via
// binanceWSConnDenied, but get a fresh, un-disabled chance at WS on the very next
// call — a transient clock-sync hiccup must not permanently strand the account on REST
// for the rest of its runtime.
func binanceWSPermanentlyDenied(code int) bool {
	switch code {
	case -2015, -1002:
		return true
	}
	return false
}

func (ts *BinanceTradeStream) PlaceOrder(ctx context.Context, req trader.OrderRequest) (trader.OrderResult, error) {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if disabled {
		return trader.OrderResult{}, ErrWSUnavailable
	}

	orderType := binanceOrderType(req)
	method := "order.place"
	params := url.Values{
		"symbol":       {req.Symbol},
		"side":         {strings.ToUpper(req.Side)},
		"type":         {orderType},
		"positionSide": {binancePositionSide(req.PositionIdx)},
	}
	if isAlgoOrderType(orderType) {
		method = "algoOrder.place"
		params.Set("algoType", "CONDITIONAL")
		if req.TriggerPrice != "" {
			params.Set("triggerPrice", req.TriggerPrice)
		}
		if req.TriggerPrice != "" && req.TriggerBy == "MarkPrice" {
			params.Set("workingType", "MARK_PRICE")
		}
	}
	if req.Qty != "" {
		params.Set("quantity", req.Qty)
	}
	if req.Price != "" {
		params.Set("price", req.Price)
	}
	if req.ReduceOnly && req.PositionIdx == 0 {
		params.Set("reduceOnly", "true")
	}
	if req.TimeInForce != "" && orderType == "LIMIT" {
		params.Set("timeInForce", req.TimeInForce)
	}
	if req.OrderLinkId != "" {
		idField := "newClientOrderId"
		if method == "algoOrder.place" {
			idField = "clientAlgoId"
		}
		params.Set(idField, truncateClientOrderID(req.OrderLinkId))
	}

	resp, err := ts.sendReq(ctx, method, params)
	if err != nil {
		return trader.OrderResult{}, err
	}
	if resp.Error != nil {
		if binanceWSConnDenied(resp.Error.Code) {
			if binanceWSPermanentlyDenied(resp.Error.Code) {
				ts.mu.Lock()
				ts.wsDisabled = true
				ts.mu.Unlock()
			}
			return trader.OrderResult{}, ErrWSUnavailable
		}
		return trader.OrderResult{}, fmt.Errorf("binance: code=%d: %s", resp.Error.Code, resp.Error.Msg)
	}
	if method == "algoOrder.place" {
		var r struct {
			AlgoId       int64  `json:"algoId"`
			ClientAlgoId string `json:"clientAlgoId"`
		}
		if err := json.Unmarshal(resp.Result, &r); err != nil {
			return trader.OrderResult{}, fmt.Errorf("parse algoOrder.place response: %w", err)
		}
		return trader.OrderResult{OrderId: strconv.FormatInt(r.AlgoId, 10), OrderLinkId: r.ClientAlgoId}, nil
	}
	var r struct {
		OrderId       int64  `json:"orderId"`
		ClientOrderId string `json:"clientOrderId"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		return trader.OrderResult{}, fmt.Errorf("parse order.place response: %w", err)
	}
	return trader.OrderResult{OrderId: strconv.FormatInt(r.OrderId, 10), OrderLinkId: r.ClientOrderId}, nil
}

func (ts *BinanceTradeStream) CancelOrder(ctx context.Context, req trader.CancelRequest) error {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if disabled {
		return ErrWSUnavailable
	}

	method := "order.cancel"
	idField, origIdField := "orderId", "origClientOrderId"
	if req.OrderFilter == "StopOrder" {
		method = "algoOrder.cancel"
		idField, origIdField = "algoId", "clientAlgoId"
	}
	params := url.Values{"symbol": {req.Symbol}}
	if req.OrderId != "" {
		params.Set(idField, req.OrderId)
	} else if req.OrderLinkId != "" {
		params.Set(origIdField, truncateClientOrderID(req.OrderLinkId))
	}

	resp, err := ts.sendReq(ctx, method, params)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		if binanceWSConnDenied(resp.Error.Code) {
			if binanceWSPermanentlyDenied(resp.Error.Code) {
				ts.mu.Lock()
				ts.wsDisabled = true
				ts.mu.Unlock()
			}
			return ErrWSUnavailable
		}
		return fmt.Errorf("binance: code=%d: %s", resp.Error.Code, resp.Error.Msg)
	}
	return nil
}

func (ts *BinanceTradeStream) PlaceOrderBatch(ctx context.Context, req trader.BatchPlaceRequest) ([]trader.BatchPlaceResult, error) {
	out := make([]trader.BatchPlaceResult, len(req.Request))
	var wg sync.WaitGroup
	for i, it := range req.Request {
		wg.Add(1)
		go func(i int, it trader.BatchOrderItem) {
			defer wg.Done()
			res, err := ts.PlaceOrder(ctx, trader.OrderRequest{
				Symbol: it.Symbol, Side: it.Side, OrderType: it.OrderType, Qty: it.Qty,
				Price: it.Price, TriggerPrice: it.TriggerPrice, TimeInForce: it.TimeInForce,
				OrderFilter: it.OrderFilter, ReduceOnly: it.ReduceOnly,
				PositionIdx: it.PositionIdx, OrderLinkId: it.OrderLinkId,
			})
			if err != nil {
				out[i] = trader.BatchPlaceResult{Code: -1, Msg: err.Error()}
				return
			}
			out[i] = trader.BatchPlaceResult{OrderId: res.OrderId, OrderLinkId: res.OrderLinkId}
		}(i, it)
	}
	wg.Wait()
	return out, nil
}

func (ts *BinanceTradeStream) CancelOrderBatch(ctx context.Context, req trader.BatchCancelRequest) error {
	var wg sync.WaitGroup
	errs := make([]error, len(req.Request))
	for i, it := range req.Request {
		wg.Add(1)
		go func(i int, it trader.BatchCancelItem) {
			defer wg.Done()
			errs[i] = ts.CancelOrder(ctx, trader.CancelRequest{
				Symbol: it.Symbol, OrderId: it.OrderId, OrderFilter: it.OrderFilter,
			})
		}(i, it)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err // first error wins, matching the existing REST CancelOrderBatch's reporting granularity
		}
	}
	return nil
}
