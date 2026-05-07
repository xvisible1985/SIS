package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const bybitPrivateWS = "wss://stream.bybit.com/v5/private"

// safeSend sends JSON to a gorilla WebSocket connection, ignoring write errors.
func safeSend(conn *websocket.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// RunPositionStream opens a Bybit private WebSocket, authenticates, subscribes
// to position and order topics, fetches a REST snapshot, then relays WS deltas
// to the client conn until ctx is cancelled or conn closes.
func RunPositionStream(ctx context.Context, conn *websocket.Conn, creds Credentials, accountName string) {
	type msg map[string]any
	logMsg := func(m string, errFlag ...bool) {
		isErr := len(errFlag) > 0 && errFlag[0]
		safeSend(conn, msg{"type": "log", "message": m, "error": isErr})
	}

	safeSend(conn, msg{"type": "account", "accountName": accountName})

	ts := serverTimestamp()
	var tsMs int64
	fmt.Sscanf(ts, "%d", &tsMs)
	expires := tsMs + 10000
	sigStr := fmt.Sprintf("GET/realtime%d", expires)
	wsSign := hmacHex(creds.SecretKey, sigStr)

	bwsConn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitPrivateWS, nil)
	if err != nil {
		logMsg("Ошибка подключения к Bybit WS: "+err.Error(), true)
		return
	}
	defer bwsConn.Close()

	logMsg("Подключено к Bybit WS, авторизация...")

	authMsg, _ := json.Marshal(map[string]any{
		"op":   "auth",
		"args": []any{creds.APIKey, expires, wsSign},
	})
	bwsConn.WriteMessage(websocket.TextMessage, authMsg) //nolint:errcheck

	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()

	bybitCh := make(chan []byte, 64)
	bybitErrCh := make(chan error, 1)
	go func() {
		for {
			_, data, err := bwsConn.ReadMessage()
			if err != nil {
				bybitErrCh <- err
				return
			}
			bybitCh <- data
		}
	}()

	// Bybit private WS сообщения по топикам position/order всегда пересылаем как "delta".
	// Начальный снапшот обеспечивает fetchAndSendSnapshot через REST API.

	for {
		select {
		case <-ctx.Done():
			return

		case <-pingTicker.C:
			ping, _ := json.Marshal(map[string]string{"op": "ping"})
			bwsConn.WriteMessage(websocket.TextMessage, ping) //nolint:errcheck

		case err := <-bybitErrCh:
			logMsg("Bybit WS закрыт: "+err.Error(), true)
			return

		case data := <-bybitCh:
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				continue
			}
			op, _ := raw["op"].(string)
			switch op {
			case "auth":
				if success, _ := raw["success"].(bool); success {
					sub, _ := json.Marshal(map[string]any{
						"op":   "subscribe",
						"args": []string{"position", "order"},
					})
					bwsConn.WriteMessage(websocket.TextMessage, sub) //nolint:errcheck
					logMsg("Авторизация OK, подписка на position и order")
					go func() {
						if err := fetchAndSendSnapshot(ctx, conn, creds); err != nil {
							logMsg("[REST] Ошибка снапшота: "+err.Error(), true)
						}
					}()
				} else {
					msg, _ := raw["ret_msg"].(string)
					logMsg("Авторизация провалена: "+msg, true)
					return
				}
			case "pong":
				// ignore
			default:
				topic, _ := raw["topic"].(string)
				if topic == "position" || topic == "order" {
					safeSend(conn, map[string]any{
						"type":     topic,
						"dataType": "delta",
						"data":     raw["data"],
					})
					if items, ok := raw["data"].([]any); ok {
						log.Printf("trader ws: %s/delta count=%d", topic, len(items))
					}
				}
			}
		}
	}
}

func fetchAndSendSnapshot(ctx context.Context, conn *websocket.Conn, creds Credentials) error {
	positions, err := FetchPositions(ctx, creds)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}
	safeSend(conn, map[string]any{"type": "position", "dataType": "snapshot", "data": positions})

	orders, err := FetchOpenOrders(ctx, creds)
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	safeSend(conn, map[string]any{"type": "order", "dataType": "snapshot", "data": orders})
	return nil
}
