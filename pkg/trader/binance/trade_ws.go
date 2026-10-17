package binance

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strconv"
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
