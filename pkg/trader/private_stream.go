package trader

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// OrderEvent carries order data from Bybit private WS "order" topic.
type OrderEvent struct {
	OrderID     string `json:"orderId"`
	OrderLinkID string `json:"orderLinkId"`
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	OrderStatus string `json:"orderStatus"`
	AvgPrice    string `json:"avgPrice"`
	CumExecQty  string `json:"cumExecQty"`
	OrderType   string `json:"orderType"`
	Category    string `json:"category"`
	OrderFilter string `json:"orderFilter"`
}

// PositionEvent carries position data from Bybit private WS "position" topic.
type PositionEvent struct {
	Symbol      string `json:"symbol"`
	Side        string `json:"side"`
	Size        string `json:"size"`
	Category    string `json:"category"`
	PositionIdx int    `json:"positionIdx"`
}

// PrivateStreamHandler receives events from Bybit private WS.
type PrivateStreamHandler interface {
	OnOrderEvent(ev OrderEvent)
	OnPositionEvent(ev PositionEvent)
	OnConnected()
	OnDisconnected(err error)
}

// RunPrivateStream connects to Bybit private WS, subscribes to "order" topic,
// and dispatches events to handler. Blocks until ctx is cancelled. Auto-reconnects.
func RunPrivateStream(ctx context.Context, creds Credentials, handler PrivateStreamHandler) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := runPrivateOnce(ctx, creds, handler); err != nil {
			handler.OnDisconnected(err)
			log.Printf("trader private stream: disconnected (%v), retry in 5s", err)
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func runPrivateOnce(ctx context.Context, creds Credentials, handler PrivateStreamHandler) error {
	ts := serverTimestamp()
	var tsMs int64
	fmt.Sscanf(ts, "%d", &tsMs)
	expires := tsMs + 10000

	sigStr := fmt.Sprintf("GET/realtime%d", expires)
	wsSign := hmacHex(creds.SecretKey, sigStr)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	authMsg, _ := json.Marshal(map[string]any{
		"op":   "auth",
		"args": []any{creds.APIKey, expires, wsSign},
	})
	if err := conn.WriteMessage(websocket.TextMessage, authMsg); err != nil {
		return err
	}

	ping := time.NewTicker(20 * time.Second)
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

	subscribed := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ping.C:
			p, _ := json.Marshal(map[string]string{"op": "ping"})
			conn.WriteMessage(websocket.TextMessage, p) //nolint:errcheck
		case err := <-errCh:
			return err
		case data := <-msgCh:
			var raw map[string]any
			if json.Unmarshal(data, &raw) != nil {
				continue
			}
			op, _ := raw["op"].(string)
			switch op {
			case "auth":
				if ok, _ := raw["success"].(bool); ok && !subscribed {
					sub, _ := json.Marshal(map[string]any{
						"op":   "subscribe",
						"args": []string{"order", "position"},
					})
					conn.WriteMessage(websocket.TextMessage, sub) //nolint:errcheck
					subscribed = true
					handler.OnConnected()
				} else if !ok {
					return fmt.Errorf("auth failed: %v", raw["ret_msg"])
				}
			case "pong":
				// ignore
			default:
				topic, _ := raw["topic"].(string)
				msgType, _ := raw["type"].(string)
				items, ok := raw["data"].([]any)
				if !ok {
					continue
				}
				log.Printf("private stream: topic=%q type=%q items=%d", topic, msgType, len(items))
				switch topic {
				case "order":
					for _, item := range items {
						b, _ := json.Marshal(item)
						var ev OrderEvent
						if json.Unmarshal(b, &ev) == nil {
							handler.OnOrderEvent(ev)
						}
					}
				case "position":
					log.Printf("private stream: position msg type=%q count=%d", msgType, len(items))
					// Skip snapshot — Bybit sends current state on subscribe which may
					// include size=0 for already-closed positions; only react to deltas.
					if msgType == "delta" {
						for _, item := range items {
							b, _ := json.Marshal(item)
							var ev PositionEvent
							if json.Unmarshal(b, &ev) == nil {
								handler.OnPositionEvent(ev)
							}
						}
					}
				}
			}
		}
	}
}

// hmacHex returns hex-encoded HMAC-SHA256 of msg with key.
func hmacHex(key, msg string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}
