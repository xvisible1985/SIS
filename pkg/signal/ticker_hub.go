package signal

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TickerHub maintains Bybit tickers WS connections and dispatches markPrice
// updates to registered per-symbol callbacks.
type TickerHub struct {
	ctx context.Context
	mu  sync.RWMutex

	prices map[string]float64  // latest markPrice per symbol
	cbs    map[string][]*tickerCb

	pool   []*tickerConn
	poolMu sync.Mutex
}

type tickerConn struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *websocket.Conn
	topics  []string
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
		prices: make(map[string]float64),
		cbs:    make(map[string][]*tickerCb),
	}
}

// Subscribe ensures a tickers WS subscription for symbol and, if cb is non-nil,
// registers it to be called on every markPrice update. Returns an unsubscribe func.
func (h *TickerHub) Subscribe(symbol string, cb func(markPrice float64)) func() {
	h.mu.Lock()
	_, exists := h.prices[symbol]
	if !exists {
		h.prices[symbol] = 0
	}
	var entry *tickerCb
	if cb != nil {
		entry = &tickerCb{fn: cb}
		h.cbs[symbol] = append(h.cbs[symbol], entry)
	}
	h.mu.Unlock()

	if !exists {
		h.assignTopic("tickers." + symbol)
	}

	return func() {
		if entry == nil {
			return
		}
		h.mu.Lock()
		cbs := h.cbs[symbol]
		for i, c := range cbs {
			if c == entry {
				h.cbs[symbol] = append(cbs[:i], cbs[i+1:]...)
				break
			}
		}
		h.mu.Unlock()
	}
}

// LatestPrice returns the most recently received markPrice for symbol, or 0.
func (h *TickerHub) LatestPrice(symbol string) float64 {
	h.mu.RLock()
	p := h.prices[symbol]
	h.mu.RUnlock()
	return p
}

// ConnCount returns the current number of WS connections in the pool.
func (h *TickerHub) ConnCount() int {
	h.poolMu.Lock()
	n := len(h.pool)
	h.poolMu.Unlock()
	return n
}

// Metrics returns a live snapshot of TickerHub state.
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

func (h *TickerHub) assignTopic(topic string) {
	h.poolMu.Lock()
	for _, c := range h.pool {
		c.mu.Lock()
		if len(c.topics) < maxTopicsPerConn {
			c.topics = append(c.topics, topic)
			c.mu.Unlock()
			h.poolMu.Unlock()
			h.wsSend(c, map[string]interface{}{
				"op":   "subscribe",
				"args": []string{topic},
			})
			return
		}
		c.mu.Unlock()
	}
	tc := &tickerConn{topics: []string{topic}}
	h.pool = append(h.pool, tc)
	h.poolMu.Unlock()
	go h.runConn(tc)
}

func (h *TickerHub) runConn(tc *tickerConn) {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		// bybitPublicWS is declared in hub.go (same package)
		conn, _, err := websocket.DefaultDialer.DialContext(h.ctx, bybitPublicWS, nil)
		if err != nil {
			log.Printf("ticker hub: dial: %v; retry in %s", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		tc.mu.Lock()
		tc.conn = conn
		topics := make([]string, len(tc.topics))
		copy(topics, tc.topics)
		tc.mu.Unlock()

		h.wsSend(tc, map[string]interface{}{
			"op":   "subscribe",
			"args": topics,
		})

		h.readLoop(conn, tc)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		default:
			log.Printf("ticker hub: reconnecting in %s", wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (h *TickerHub) readLoop(conn *websocket.Conn, tc *tickerConn) {
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	msgCh := make(chan []byte, 64)
	errCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- data
		}
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		case err := <-errCh:
			log.Printf("ticker hub: read: %v", err)
			return
		case <-ping.C:
			h.wsSend(tc, map[string]string{"op": "ping"})
		case data := <-msgCh:
			h.handleMessage(data)
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

type wsTickerMsg struct {
	Topic string `json:"topic"`
	Data  struct {
		MarkPrice string `json:"markPrice"`
	} `json:"data"`
}

func (h *TickerHub) handleMessage(data []byte) {
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

	h.mu.Lock()
	h.prices[symbol] = markPrice
	cbs := make([]*tickerCb, len(h.cbs[symbol]))
	copy(cbs, h.cbs[symbol])
	h.mu.Unlock()

	for _, c := range cbs {
		c.fn(markPrice)
	}
}
