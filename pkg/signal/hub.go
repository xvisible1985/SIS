package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"sis/pkg/proxy"
)

const (
	bybitPublicWS     = "wss://stream.bybit.com/v5/public/linear"
	bybitKlineREST    = "https://api.bybit.com/v5/market/kline"
	maxTopicsPerConn  = 40
	candleBufferSize  = 200
	wsPingInterval    = 20 * time.Second
	wsReconnectDelay  = 3 * time.Second
)

// tfToBybit converts frontend TF names to Bybit kline interval codes.
var tfToBybit = map[string]string{
	"1m": "1", "3m": "3", "5m": "5", "15m": "15", "30m": "30",
	"1h": "60", "2h": "120", "4h": "240", "6h": "360", "12h": "720",
	"1D": "D", "1W": "W", "1M": "M",
}

func bybitInterval(tf string) string {
	if v, ok := tfToBybit[tf]; ok {
		return v
	}
	return tf
}

// topicKey builds a canonical map key.
func topicKey(symbol, interval string) string {
	return symbol + ":" + bybitInterval(interval)
}

// ── KlineHub ──────────────────────────────────────────────────────────────

// KlineHub maintains Bybit kline WS connections, rolling candle buffers,
// and notifies listeners on each confirmed candle close.
type KlineHub struct {
	ctx context.Context
	mu  sync.RWMutex

	// candle buffers per (symbol:bybit_interval)
	buffers map[string][]Candle

	// listeners per key; called with a snapshot on each kline close
	listeners map[string][]func([]Candle)

	// WS connection pool
	pool   []*hubConn
	poolMu sync.Mutex

	// prefetchSem limits concurrent REST prefetch goroutines
	prefetchSem chan struct{}
}

type hubConn struct {
	mu      sync.Mutex // protects conn and topics
	writeMu sync.Mutex // serialises all writes to conn (gorilla/websocket is not write-safe)
	conn    *websocket.Conn
	topics  []string // bybit topic strings, e.g. "kline.60.BTCUSDT"
}

// NewKlineHub creates a hub and starts the pool manager.
func NewKlineHub(ctx context.Context) *KlineHub {
	h := &KlineHub{
		ctx:         ctx,
		buffers:     make(map[string][]Candle),
		listeners:   make(map[string][]func([]Candle)),
		prefetchSem: make(chan struct{}, 50),
	}
	return h
}

// Subscribe ensures candle data flows for (symbol, interval) and registers cb.
// cb is called with the current candle slice on every confirmed kline close.
func (h *KlineHub) Subscribe(symbol, interval string, cb func([]Candle)) {
	key := topicKey(symbol, interval)
	bybitIv := bybitInterval(interval)
	topic := fmt.Sprintf("kline.%s.%s", bybitIv, symbol)

	h.mu.Lock()
	_, exists := h.buffers[key]
	if !exists {
		h.buffers[key] = nil // placeholder; filled by prefetch
	}
	if cb != nil {
		h.listeners[key] = append(h.listeners[key], cb)
	}
	h.mu.Unlock()

	if !exists {
		// Fetch historical candles then connect WS for this topic
		go h.prefetchAndConnect(symbol, interval, bybitIv, topic, key)
	}
}

// Snapshot returns a copy of the current candle buffer for a key.
func (h *KlineHub) Snapshot(symbol, interval string) []Candle {
	key := topicKey(symbol, interval)
	h.mu.RLock()
	buf := h.buffers[key]
	out := make([]Candle, len(buf))
	copy(out, buf)
	h.mu.RUnlock()
	return out
}

// ConnCount returns the current number of WS connections in the pool.
func (h *KlineHub) ConnCount() int {
	h.poolMu.Lock()
	n := len(h.pool)
	h.poolMu.Unlock()
	return n
}

// SnapshotOrFetch returns the cached candle buffer if it has ≥2 candles.
// Otherwise it performs a one-shot REST fetch so callers that have no active
// WS subscription can still get indicator values.
func (h *KlineHub) SnapshotOrFetch(symbol, interval string) []Candle {
	snap := h.Snapshot(symbol, interval)
	if len(snap) >= 2 {
		return snap
	}
	candles, err := FetchKlineHistory(symbol, bybitInterval(interval), candleBufferSize)
	if err != nil || len(candles) < 2 {
		return nil
	}
	// Cache so repeated calls don't hit REST every time
	key := topicKey(symbol, interval)
	h.mu.Lock()
	if len(h.buffers[key]) < 2 {
		h.buffers[key] = candles
	}
	h.mu.Unlock()
	return candles
}

// ── prefetch + connect ────────────────────────────────────────────────────

func (h *KlineHub) prefetchAndConnect(symbol, interval, bybitIv, topic, key string) {
	h.prefetchSem <- struct{}{}
	defer func() { <-h.prefetchSem }()

	// 1. Fetch REST history
	candles, err := FetchKlineHistory(symbol, bybitIv, candleBufferSize)
	if err != nil {
		log.Printf("kline hub: REST prefetch %s/%s: %v", symbol, interval, err)
	}
	h.mu.Lock()
	if len(candles) > 0 {
		h.buffers[key] = candles
	}
	h.mu.Unlock()

	// 2. Assign topic to a WS connection and start it
	h.assignTopic(topic, key)
}

func (h *KlineHub) assignTopic(topic, key string) {
	h.poolMu.Lock()
	for _, c := range h.pool {
		c.mu.Lock()
		if len(c.topics) < maxTopicsPerConn {
			c.topics = append(c.topics, topic)
			c.mu.Unlock()
			h.poolMu.Unlock()
			// subscribe on existing live connection
			h.wsSend(c, map[string]interface{}{
				"op":   "subscribe",
				"args": []string{topic},
			})
			return
		}
		c.mu.Unlock()
	}
	// All connections full or none exist — open a new one
	hc := &hubConn{topics: []string{topic}}
	h.pool = append(h.pool, hc)
	h.poolMu.Unlock()

	go h.runConn(hc)
}

// ── WS connection lifecycle ───────────────────────────────────────────────

func (h *KlineHub) runConn(hc *hubConn) {
	for {
		select {
		case <-h.ctx.Done():
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.DialContext(h.ctx, bybitPublicWS, nil)
		if err != nil {
			log.Printf("kline hub: dial: %v; retry in %s", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		hc.mu.Lock()
		hc.conn = conn
		topics := make([]string, len(hc.topics))
		copy(topics, hc.topics)
		hc.mu.Unlock()

		// Subscribe to all topics for this connection
		h.wsSend(hc, map[string]interface{}{
			"op":   "subscribe",
			"args": topics,
		})

		h.readLoop(conn, hc)
		conn.Close()

		select {
		case <-h.ctx.Done():
			return
		default:
			log.Printf("kline hub: reconnecting in %s", wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
		}
	}
}

func (h *KlineHub) readLoop(conn *websocket.Conn, hc *hubConn) {
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
			log.Printf("kline hub: read: %v", err)
			return
		case <-ping.C:
			h.wsSend(hc, map[string]string{"op": "ping"})
		case data := <-msgCh:
			h.handleMessage(data)
		}
	}
}

func (h *KlineHub) wsSend(hc *hubConn, v interface{}) {
	data, _ := json.Marshal(v)
	hc.writeMu.Lock()
	defer hc.writeMu.Unlock()
	hc.mu.Lock()
	conn := hc.conn
	hc.mu.Unlock()
	if conn == nil {
		return
	}
	conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// ── message parsing ───────────────────────────────────────────────────────

type wsKlineMsg struct {
	Topic string `json:"topic"`
	Data  []struct {
		Start    int64  `json:"start"`
		Open     string `json:"open"`
		High     string `json:"high"`
		Low      string `json:"low"`
		Close    string `json:"close"`
		Volume   string `json:"volume"`
		Confirm  bool   `json:"confirm"`
	} `json:"data"`
}

func (h *KlineHub) handleMessage(data []byte) {
	var msg wsKlineMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Topic == "" {
		return
	}
	if !strings.HasPrefix(msg.Topic, "kline.") {
		return
	}
	parts := strings.SplitN(msg.Topic, ".", 3)
	if len(parts) != 3 {
		return
	}
	bybitIv, symbol := parts[1], parts[2]
	key := symbol + ":" + bybitIv

	for _, bar := range msg.Data {
		c := Candle{
			Time:   bar.Start,
			Open:   parseF(bar.Open),
			High:   parseF(bar.High),
			Low:    parseF(bar.Low),
			Close:  parseF(bar.Close),
			Volume: parseF(bar.Volume),
		}
		h.mu.Lock()
		buf := h.buffers[key]
		// Upsert: if last candle has same time → replace, else append
		if len(buf) > 0 && buf[len(buf)-1].Time == c.Time {
			buf[len(buf)-1] = c
		} else {
			buf = append(buf, c)
			if len(buf) > candleBufferSize {
				buf = buf[len(buf)-candleBufferSize:]
			}
		}
		h.buffers[key] = buf

		var snapshot []Candle
		var cbs []func([]Candle)
		if bar.Confirm {
			snapshot = make([]Candle, len(buf))
			copy(snapshot, buf)
			cbs = h.listeners[key]
		}
		h.mu.Unlock()

		if bar.Confirm && len(cbs) > 0 {
			for _, cb := range cbs {
				cb(snapshot)
			}
		}
	}
}

// ── REST history prefetch ─────────────────────────────────────────────────

func FetchKlineHistory(symbol, bybitInterval string, limit int) ([]Candle, error) {
	url := fmt.Sprintf("%s?category=linear&symbol=%s&interval=%s&limit=%d",
		bybitKlineREST, symbol, bybitInterval, limit)

	resp, err := proxy.HTTPClient().Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		RetCode int `json:"retCode"`
		Result  struct {
			List [][]string `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.RetCode != 0 {
		return nil, fmt.Errorf("bybit retCode %d", result.RetCode)
	}

	// List is newest-first; reverse to oldest-first
	list := result.Result.List
	candles := make([]Candle, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		row := list[i]
		if len(row) < 6 {
			continue
		}
		ts, _ := strconv.ParseInt(row[0], 10, 64)
		candles = append(candles, Candle{
			Time:   ts,
			Open:   parseF(row[1]),
			High:   parseF(row[2]),
			Low:    parseF(row[3]),
			Close:  parseF(row[4]),
			Volume: parseF(row[5]),
		})
	}
	return candles, nil
}

func parseF(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
