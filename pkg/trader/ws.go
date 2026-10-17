package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/proxy"
)

var bybitPrivateWS = "wss://stream.bybit.com/v5/private"

// bybitReadTimeout bounds how long RunPositionStream waits for ANY message (data or
// pong) from Bybit's private WS before treating the connection as dead. Without this,
// a silently-broken network path (packets black-holed, no TCP RST/FIN — e.g. a flaky
// VPN/proxy) leaves bwsConn.ReadMessage() blocked forever: the stream never errors,
// never returns, the frontend-facing conn never closes, and the browser never
// reconnects to fetch a fresh REST snapshot. Real incident: sessions observed stuck
// for 10-55+ hours after a Bybit connectivity blip, showing stale/phantom positions
// in the terminal long after they were actually closed on the exchange. Set well above
// the 20s ping interval below to tolerate one slow round trip before declaring dead.
var bybitReadTimeout = 45 * time.Second

// wsWriter serializes every write to the frontend-facing conn. gorilla/websocket
// requires writes to be serialized — at most one goroutine may call WriteMessage at a
// time — but RunPositionStream's main loop and its background REST-snapshot goroutine
// (fetchAndSendSnapshot, launched via `go func()` right after auth succeeds) both write
// to the same conn concurrently. Without this mutex, a WS delta arriving while the
// snapshot goroutine is mid-write can interleave two WriteMessage calls into one
// corrupted frame: the frontend's JSON.parse fails silently (usePositionsWs.ts's
// `catch { /* ignore malformed */ }"), so no error is ever visible — the terminal just
// never gets the next live update until the browser reconnects (e.g. on F5), even
// though the WS connection itself looks perfectly healthy. Found live (2026-10-16): a
// newly-opened Binance position (same unsynchronized-write pattern, see
// pkg/trader/binance/ws.go) didn't appear until a hard refresh.
type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn.WriteMessage(websocket.TextMessage, data) //nolint:errcheck
}

// RunPositionStream opens a Bybit private WebSocket, authenticates, subscribes
// to position and order topics, fetches a REST snapshot, then relays WS deltas
// to the client conn until ctx is cancelled or conn closes.
func RunPositionStream(ctx context.Context, conn *websocket.Conn, creds Credentials, accountName string, broadcastCh <-chan any) {
	w := &wsWriter{conn: conn}
	type msg map[string]any
	logMsg := func(m string, errFlag ...bool) {
		isErr := len(errFlag) > 0 && errFlag[0]
		w.send(msg{"type": "log", "message": m, "error": isErr})
	}

	w.send(msg{"type": "account", "accountName": accountName})

	ts := serverTimestamp()
	var tsMs int64
	fmt.Sscanf(ts, "%d", &tsMs)
	expires := tsMs + 10000
	sigStr := fmt.Sprintf("GET/realtime%d", expires)
	wsSign := hmacHex(creds.SecretKey, sigStr)

	dialer, err := proxy.WSDialerFor(creds.WhitelistedIPs)
	if err != nil {
		logMsg("Ошибка выбора прокси для Bybit WS: "+err.Error(), true)
		return
	}
	bwsConn, _, err := dialer.DialContext(ctx, bybitPrivateWS, nil)
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
	bwsConn.SetReadDeadline(time.Now().Add(bybitReadTimeout)) //nolint:errcheck
	go func() {
		for {
			_, data, err := bwsConn.ReadMessage()
			if err != nil {
				bybitErrCh <- err
				return
			}
			bwsConn.SetReadDeadline(time.Now().Add(bybitReadTimeout)) //nolint:errcheck
			select {
			case bybitCh <- data:
			default:
			}
		}
	}()

	// Bybit private WS сообщения по топикам position/order всегда пересылаем как "delta".
	// Начальный снапшот обеспечивает fetchAndSendSnapshot через REST API.

	for {
		select {
		case <-ctx.Done():
			bwsConn.Close()
			return

		case <-pingTicker.C:
			ping, _ := json.Marshal(map[string]string{"op": "ping"})
			bwsConn.WriteMessage(websocket.TextMessage, ping) //nolint:errcheck

		case err := <-bybitErrCh:
			logMsg("Bybit WS закрыт: "+err.Error(), true)
			return

		case m := <-broadcastCh:
			w.send(m)

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
						"args": []string{"position", "order", "execution", "wallet"},
					})
					bwsConn.WriteMessage(websocket.TextMessage, sub) //nolint:errcheck
					logMsg("Авторизация OK, подписка на position, order, execution и wallet")
					go func() {
						if err := fetchAndSendSnapshot(ctx, w, creds); err != nil {
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
				if topic == "position" || topic == "order" || topic == "execution" {
					w.send(map[string]any{
						"type":     topic,
						"dataType": "delta",
						"data":     raw["data"],
					})
					if items, ok := raw["data"].([]any); ok {
						log.Printf("trader ws: %s/delta count=%d", topic, len(items))
					}
				}
				if topic == "wallet" {
					avail := extractWalletAvailable(raw["data"])
					eq := extractWalletEquity(raw["data"])
					msg := map[string]any{"type": "wallet", "availableBalance": avail}
					if eq >= 0 {
						msg["equity"] = eq
					}
					w.send(msg)
				}
			}
		}
	}
}

func fetchAndSendSnapshot(ctx context.Context, w *wsWriter, creds Credentials) error {
	positions, err := FetchPositions(ctx, creds)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}
	w.send(map[string]any{"type": "position", "dataType": "snapshot", "data": positions})

	orders, err := FetchOpenOrders(ctx, creds)
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	w.send(map[string]any{"type": "order", "dataType": "snapshot", "data": orders})

	eq, available, err := GetWalletBalance(ctx, creds)
	if err == nil {
		msg := map[string]any{"type": "wallet", "availableBalance": available}
		if eq >= 0 {
			msg["equity"] = eq
		}
		w.send(msg)
	}
	return nil
}

// extractWalletAvailable parses totalAvailableBalance from a Bybit wallet WS event data array.
func extractWalletAvailable(data any) float64 {
	return extractWalletField(data, "totalAvailableBalance")
}

// extractWalletEquity parses totalEquity from a Bybit wallet WS event data array.
func extractWalletEquity(data any) float64 {
	return extractWalletField(data, "totalEquity")
}

func extractWalletField(data any, field string) float64 {
	items, ok := data.([]any)
	if !ok || len(items) == 0 {
		return -1
	}
	wallet, ok := items[0].(map[string]any)
	if !ok {
		return -1
	}
	if v, ok := wallet[field].(string); ok {
		var f float64
		fmt.Sscanf(v, "%f", &f)
		return f
	}
	return -1
}
