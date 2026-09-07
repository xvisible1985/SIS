package signal

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"sis/pkg/proxy"
)

// tickerReadTimeout bounds how long a pooled WS connection (Bybit or Binance) waits for
// ANY message before being treated as dead. Without this, a silently-broken network
// path (packets black-holed, no TCP RST/FIN) leaves the reader goroutine blocked in
// ReadMessage() forever — see pkg/trader/ws.go's identical fix (commit 143ffb5) for the
// real incident this class of bug caused on the private position-stream side. Set well
// above both exchanges' normal update cadence (Bybit tickers and Binance's @1s mark
// price stream both update at least once per second when healthy).
var tickerReadTimeout = 45 * time.Second

// binancePublicWS is Binance USDⓈ-M Futures' dynamic-subscribe WS endpoint. Unlike the
// combined-stream /stream endpoint (which wraps every payload as {"stream":...,
// "data":...}), /ws delivers the raw event object directly — matching how
// handleBinanceMessage below parses it.
var binancePublicWS = "wss://fstream.binance.com/ws"

// tickerKey identifies one (exchange, symbol) price feed. Two exchanges can report
// wildly different prices for the identically-spelled symbol string, so the exchange is
// part of the key everywhere — TickerHub never merges prices across exchanges.
type tickerKey struct {
	Exchange string
	Symbol   string
}

// TickerHub maintains WS connections (one pool per exchange) and dispatches markPrice
// updates to registered per-(exchange,symbol) callbacks.
type TickerHub struct {
	ctx context.Context
	mu  sync.RWMutex

	prices map[tickerKey]float64
	cbs    map[tickerKey][]*tickerCb

	pool   []*tickerConn
	poolMu sync.Mutex
}

type tickerConn struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	conn     *websocket.Conn
	exchange string
	topics   []string

	// connected reports whether this pool slot currently holds a live, dialed WS
	// connection. A tickerConn is created once and stays in h.pool for the life of
	// the process (its topics must survive reconnects), so ConnCount() cannot use
	// pool length alone to report live connections — it must distinguish "holds a
	// slot" from "is actually connected right now" (e.g. during the reconnect gap
	// after the read deadline fires).
	connected atomic.Bool
}

type tickerCb struct {
	fn func(float64)
}

// TickerHubMetrics is a live snapshot of TickerHub state.
type TickerHubMetrics struct {
	Symbols     int `json:"symbols"`
	WarmSymbols int `json:"warm_symbols"` // symbols that have received at least one price
	WsConns     int `json:"ws_connections"`
}

// NewTickerHub creates a TickerHub. Subscribe symbols directly or warm via GlobalWarmer.
func NewTickerHub(ctx context.Context) *TickerHub {
	return &TickerHub{
		ctx:    ctx,
		prices: make(map[tickerKey]float64),
		cbs:    make(map[tickerKey][]*tickerCb),
	}
}

// Subscribe ensures a WS subscription for (exchange, symbol) and, if cb is non-nil,
// registers it to be called on every markPrice update. Returns an unsubscribe func.
// exchange is a lowercase exchange id ("bybit" or "binance").
func (h *TickerHub) Subscribe(exchange, symbol string, cb func(markPrice float64)) func() {
	key := tickerKey{Exchange: exchange, Symbol: symbol}
	h.mu.Lock()
	_, exists := h.prices[key]
	if !exists {
		h.prices[key] = 0
	}
	var entry *tickerCb
	if cb != nil {
		entry = &tickerCb{fn: cb}
		h.cbs[key] = append(h.cbs[key], entry)
	}
	h.mu.Unlock()

	if !exists {
		h.assignTopic(exchange, topicFor(exchange, symbol))
	}

	return func() {
		if entry == nil {
			return
		}
		h.mu.Lock()
		cbs := h.cbs[key]
		for i, c := range cbs {
			if c == entry {
				h.cbs[key] = append(cbs[:i], cbs[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
	}
}

// topicFor builds the exchange-specific subscribe topic string for symbol. Binance's
// stream name uses a lowercase symbol; the event payload it later sends back uses
// uppercase (matching this codebase's convention), so no case translation is needed on
// the receiving side — only here, when constructing the subscribe request.
func topicFor(exchange, symbol string) string {
	if exchange == "binance" {
		return strings.ToLower(symbol) + "@markPrice@1s"
	}
	return "tickers." + symbol
}

// LatestPrice returns the most recently received markPrice for (exchange, symbol), or 0.
func (h *TickerHub) LatestPrice(exchange, symbol string) float64 {
	h.mu.RLock()
	p := h.prices[tickerKey{Exchange: exchange, Symbol: symbol}]
	h.mu.RUnlock()
	return p
}

// SetPrice manually sets the cached latest price for (exchange, symbol) — used by tests
// to seed a price without a live WS connection; production code should rely on
// handleMessage's real ticker updates instead.
func (h *TickerHub) SetPrice(exchange, symbol string, price float64) {
	h.mu.Lock()
	h.prices[tickerKey{Exchange: exchange, Symbol: symbol}] = price
	h.mu.Unlock()
}

// ConnCount returns the current number of live (actually dialed) WS connections in the
// pool, across both exchanges. Pool slots that exist but are between reconnect attempts
// (e.g. right after a read-deadline teardown) do not count.
func (h *TickerHub) ConnCount() int {
	h.poolMu.Lock()
	defer h.poolMu.Unlock()
	n := 0
	for _, c := range h.pool {
		if c.connected.Load() {
			n++
		}
	}
	return n
}

// Metrics returns a live snapshot of TickerHub state, aggregated across both exchanges.
func (h *TickerHub) Metrics() TickerHubMetrics {
	h.mu.RLock()
	total := len(h.prices)
	warm := 0
	for _, p := range h.prices {
		if p > 0 {
			warm++
		}
	}
	h.mu.RUnlock()
	return TickerHubMetrics{
		Symbols:     total,
		WarmSymbols: warm,
		WsConns:     h.ConnCount(),
	}
}

// ── WS pool management ─────────────────────────────────────────────────────

func (h *TickerHub) assignTopic(exchange, topic string) {
	h.poolMu.Lock()
	for _, c := range h.pool {
		if c.exchange != exchange {
			continue
		}
		c.mu.Lock()
		if len(c.topics) < maxTopicsPerConn {
			c.topics = append(c.topics, topic)
			c.mu.Unlock()
			h.poolMu.Unlock()
			h.wsSend(c, subscribeMsg(exchange, []string{topic}))
			return
		}
		c.mu.Unlock()
	}
	tc := &tickerConn{exchange: exchange, topics: []string{topic}}
	h.pool = append(h.pool, tc)
	h.poolMu.Unlock()
	go h.runConn(tc)
}

// subscribeMsg builds the exchange-specific subscribe request body.
func subscribeMsg(exchange string, topics []string) map[string]any {
	if exchange == "binance" {
		return map[string]any{"method": "SUBSCRIBE", "params": topics, "id": time.Now().UnixNano()}
	}
	return map[string]any{"op": "subscribe", "args": topics}
}

func wsURLFor(exchange string) string {
	if exchange == "binance" {
		return binancePublicWS
	}
	return bybitPublicWS
}

func (h *TickerHub) runConn(tc *tickerConn) {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, _, err := proxy.WSDialer().DialContext(h.ctx, wsURLFor(tc.exchange), nil)
		if err != nil {
			log.Printf("ticker hub: dial %s: %v; retry in %s", tc.exchange, err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		tc.mu.Lock()
		tc.conn = conn
		topics := make([]string, len(tc.topics))
		copy(topics, tc.topics)
		tc.mu.Unlock()
		tc.connected.Store(true)

		h.wsSend(tc, subscribeMsg(tc.exchange, topics))

		h.readLoop(conn, tc)
		tc.connected.Store(false)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		default:
			log.Printf("ticker hub: %s reconnecting in %s", tc.exchange, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (h *TickerHub) readLoop(conn *websocket.Conn, tc *tickerConn) {
	// Bybit's liveness is an application-level ping/pong (a JSON {"op":"ping"} message
	// we must send, per wsPingInterval below). Binance's is native WS protocol-level
	// ping frames sent BY THE SERVER, which gorilla/websocket answers automatically as
	// long as ReadMessage is running — no app-level ping needs sending to Binance.
	var ping *time.Ticker
	if tc.exchange != "binance" {
		ping = time.NewTicker(wsPingInterval)
		defer ping.Stop()
	}

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	conn.SetReadDeadline(time.Now().Add(tickerReadTimeout)) //nolint:errcheck
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			conn.SetReadDeadline(time.Now().Add(tickerReadTimeout)) //nolint:errcheck
			msgCh <- data
		}
	}()

	var pingCh <-chan time.Time
	if ping != nil {
		pingCh = ping.C
	}

	for {
		select {
		case <-h.ctx.Done():
			return
		case err := <-errCh:
			log.Printf("ticker hub: %s read: %v", tc.exchange, err)
			return
		case <-pingCh:
			h.wsSend(tc, map[string]string{"op": "ping"})
		case data := <-msgCh:
			h.handleMessage(tc.exchange, data)
		}
	}
}

func (h *TickerHub) wsSend(tc *tickerConn, v interface{}) {
	data, _ := json.Marshal(v)
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	tc.mu.Lock()
	conn := tc.conn
	tc.mu.Unlock()
	if conn == nil {
		return
	}
	conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// ── Message parsing ────────────────────────────────────────────────────────

func (h *TickerHub) handleMessage(exchange string, data []byte) {
	if exchange == "binance" {
		h.handleBinanceMessage(data)
		return
	}
	h.handleBybitMessage(data)
}

type wsTickerMsg struct {
	Topic string `json:"topic"`
	Data  struct {
		MarkPrice string `json:"markPrice"`
	} `json:"data"`
}

func (h *TickerHub) handleBybitMessage(data []byte) {
	var msg wsTickerMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Topic == "" {
		return
	}
	if !strings.HasPrefix(msg.Topic, "tickers.") {
		return
	}
	symbol := strings.TrimPrefix(msg.Topic, "tickers.")
	markPrice, err := strconv.ParseFloat(msg.Data.MarkPrice, 64)
	if err != nil || markPrice == 0 {
		return
	}
	h.setPriceAndDispatch(tickerKey{Exchange: "bybit", Symbol: symbol}, markPrice)
}

// binanceMarkPriceMsg is one markPriceUpdate event from Binance's /ws endpoint — the
// raw event object, not wrapped in {"stream":...,"data":...} (that wrapping only
// applies to the /stream combined endpoint, which this package does not use).
//
// Binance always sends this event with sibling keys "E" (event time) and "P"
// (estimated settle price) alongside "e" and "p". encoding/json has no case-sensitive-
// only struct tag mode: when a JSON object key has no exact-tagged field, the decoder
// falls back to a case-insensitive match against any field whose tag differs only in
// case — so without EventTime/EstimatedSettlePrice declared here, "E" silently folds
// onto the "e"-tagged Event field (producing a decode error, since the value is a
// number) and "P" folds onto the "p"-tagged Price field (silently overwriting the real
// mark price with the estimated settle price). Declaring these two fields with their
// exact-case tags gives "E" and "P" their own exact match, so they no longer collide
// with "e"/"p". Their values are otherwise unused.
type binanceMarkPriceMsg struct {
	Event                string `json:"e"`
	EventTime            int64  `json:"E"`
	Symbol               string `json:"s"`
	Price                string `json:"p"`
	EstimatedSettlePrice string `json:"P"`
}

func (h *TickerHub) handleBinanceMessage(data []byte) {
	var msg binanceMarkPriceMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Event != "markPriceUpdate" {
		return
	}
	markPrice, err := strconv.ParseFloat(msg.Price, 64)
	if err != nil || markPrice == 0 {
		return
	}
	h.setPriceAndDispatch(tickerKey{Exchange: "binance", Symbol: msg.Symbol}, markPrice)
}

// setPriceAndDispatch updates the cached price for key and calls every registered
// callback with the new price. Shared by both exchanges' message handlers.
func (h *TickerHub) setPriceAndDispatch(key tickerKey, price float64) {
	h.mu.Lock()
	h.prices[key] = price
	cbs := make([]*tickerCb, len(h.cbs[key]))
	copy(cbs, h.cbs[key])
	h.mu.Unlock()

	for _, c := range cbs {
		c.fn(price)
	}
}
