package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"sis/pkg/proxy"
	"sis/pkg/trader"
)

// binanceWsBase is a var (not const), same reason as binanceBase in client.go — tests
// point it at an httptest-backed server.
//
// Root cause (confirmed 2026-10-17): this used to be the pre-migration legacy endpoint
// "wss://fstream.binance.com/ws/" (listenKey appended as a path suffix). Binance's
// "Important WebSocket Change Notice" split the old combined gateway into /public,
// /market, and /private endpoints, with legacy URLs fully decommissioned as of
// 2026-04-23 — NOT rejected outright, but silently degraded: "any connections not
// migrated will ONLY be able to receive data from the /public endpoint. Channels under
// /market and /private will stop pushing data." That's exactly what every account's
// position/order push had been doing for ~6 months: the WS connected fine (so
// RunPositionStream never saw an error and never logged one), the REST-driven initial
// snapshot worked fine (doesn't touch this WS at all), but ACCOUNT_UPDATE/
// ORDER_TRADE_UPDATE — both /private channels — never arrived, ever. Confirmed via a
// live reproduction: placed a real order while watching the raw WS frames, got the
// initial REST snapshot but zero delta for the new order, before this fix.
//
// New format per Binance's docs: listenKey + the specific events wanted are both query
// parameters on /private/ws, not a path suffix — see dialURLFor below.
var binanceWsBase = "wss://fstream.binance.com/private/ws"

// binanceUserDataWsURL builds the /private/ws connection URL — listenKey and the
// specific event types wanted are both query parameters (not a path suffix, unlike the
// pre-migration legacy format binanceWsBase used to be). events lists exactly the two
// event types this file's read loop actually switches on (ACCOUNT_UPDATE,
// ORDER_TRADE_UPDATE) — matching Binance's own documented example syntax
// ("events=ORDER_TRADE_UPDATE/ACCOUNT_UPDATE", slash-separated, not URL-encoded).
func binanceUserDataWsURL(listenKey string) string {
	return binanceWsBase + "?listenKey=" + listenKey + "&events=ORDER_TRADE_UPDATE/ACCOUNT_UPDATE"
}

// binanceReadTimeout mirrors pkg/trader/ws.go's bybitReadTimeout (same regression this
// guards against — see ws_test.go): Binance's user data stream sends a server ping
// every ~3 minutes and gorilla/websocket answers it automatically, but a silently-dead
// network path (no TCP RST/FIN) would leave ReadMessage() blocked forever without this.
var binanceReadTimeout = 4 * time.Minute

// listenKeyRefreshInterval: Binance expires a listenKey after 60 minutes of inactivity
// and recommends a keep-alive PUT every 30.
var listenKeyRefreshInterval = 30 * time.Minute

// wsWriter serializes every write to the frontend-facing conn — see pkg/trader/ws.go's
// identical type (Bybit) for the full rationale: gorilla/websocket requires writes to
// be serialized, but RunPositionStream's main loop and its background REST-snapshot
// goroutine (fetchAndSendBinanceSnapshot) both write to conn concurrently. An
// unsynchronized interleaving corrupts a WS frame, which the frontend's JSON.parse
// drops silently — the terminal then shows no further live updates on that connection
// until it reconnects (e.g. on F5). Found live (2026-10-16): a newly-opened position
// didn't appear until a hard refresh.
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

func getListenKey(ctx context.Context, creds trader.Credentials) (string, error) {
	data, err := doRequestToBase(ctx, binanceBase, "POST", "/fapi/v1/listenKey", url.Values{}, creds, creds.APIKey, true)
	if err != nil {
		return "", err
	}
	var r struct {
		ListenKey string `json:"listenKey"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	if r.ListenKey == "" {
		return "", fmt.Errorf("binance: empty listenKey in response: %s", data)
	}
	return r.ListenKey, nil
}

func keepAliveListenKey(ctx context.Context, creds trader.Credentials) error {
	_, err := doRequestToBase(ctx, binanceBase, "PUT", "/fapi/v1/listenKey", url.Values{}, creds, creds.APIKey, true)
	return err
}

// binanceAccountUpdateEvent is Binance's ACCOUNT_UPDATE user-data-stream event —
// carries both updated positions and updated asset balances in one payload.
// https://developers.binance.com/docs/derivatives/usds-margined-futures/user-data-streams
type binanceAccountUpdateEvent struct {
	EventType string `json:"e"`
	// EventTime exists purely so Go's json decoder has an EXACT match for the
	// top-level "E" key (event time, a number) — without it, "E" falls back to a
	// case-insensitive match against EventType's "e" tag and fails to unmarshal a
	// number into a string field, discarding the WHOLE event (see this file's
	// regression test for the live payload this broke on, 2026-10-17).
	EventTime int64 `json:"E"`
	A         struct {
		Positions []struct {
			Symbol        string `json:"s"`
			PositionAmt   string `json:"pa"`
			EntryPrice    string `json:"ep"`
			UnrealizedPnl string `json:"up"`
			PositionSide  string `json:"ps"` // "BOTH", "LONG", "SHORT"
		} `json:"P"`
	} `json:"a"`
}

// binanceOrderUpdateEvent is Binance's ORDER_TRADE_UPDATE user-data-stream event — one
// order's status/fill change. x=="TRADE" marks an actual fill (vs. a pure status change
// like a new/cancelled order), reported alongside as an execution.
type binanceOrderUpdateEvent struct {
	EventType string `json:"e"`
	// EventTime: same reason as binanceAccountUpdateEvent's own EventTime field —
	// an exact match for top-level "E" so it can't collide case-insensitively with
	// EventType's "e" tag.
	EventTime int64 `json:"E"`
	O         struct {
		Symbol          string `json:"s"`
		ClientOrderId   string `json:"c"`
		Side            string `json:"S"`
		OrderType       string `json:"o"`
		OrigQty         string `json:"q"`
		Price           string `json:"p"`
		StopPrice       string `json:"sp"`
		ExecutionType   string `json:"x"`
		OrderStatus     string `json:"X"`
		OrderId         int64  `json:"i"`
		LastFilledQty   string `json:"l"`
		FilledQty       string `json:"z"`
		LastFilledPrice string `json:"L"`
		TradeTime       int64  `json:"T"`
		TradeId         int64  `json:"t"`
	} `json:"o"`
}

// translateAccountUpdate turns one ACCOUNT_UPDATE event into position deltas in the same
// shape the frontend already understands from Bybit's raw WS payload (see
// frontend/src/hooks/terminal/usePositionsWs.ts's mapPosition — field names are Bybit's
// own convention: symbol/side/size/entryPrice/positionIdx/category/unrealisedPnl).
//
// markPrice is deliberately left out: usePositionsWs merges a delta INTO the last known
// full object by key rather than replacing it (`{...previous, ...delta}`), so omitting a
// field here means "leave whatever mark price we last had", not "mark price is zero" —
// Binance's ACCOUNT_UPDATE doesn't carry mark price at all, and sending "0" would corrupt
// the notional/PNL% the frontend derives from it.
func translateAccountUpdate(ev binanceAccountUpdateEvent) []map[string]any {
	out := make([]map[string]any, 0, len(ev.A.Positions))
	for _, p := range ev.A.Positions {
		amt, err := parseFloatSafe(p.PositionAmt)
		if err != nil {
			continue
		}
		side := "Buy"
		positionIdx := 1
		size := p.PositionAmt
		if len(size) > 0 && size[0] == '-' {
			size = size[1:]
		}
		if amt < 0 {
			side = "Sell"
		}
		if p.PositionSide == "BOTH" {
			positionIdx = 0
		} else if amt < 0 {
			positionIdx = 2
		}
		out = append(out, map[string]any{
			"symbol":        p.Symbol,
			"side":          side,
			"size":          size,
			"entryPrice":    p.EntryPrice,
			"unrealisedPnl": p.UnrealizedPnl,
			"positionIdx":   positionIdx,
			"category":      "linear",
		})
	}
	return out
}

// translateOrderUpdate turns one ORDER_TRADE_UPDATE event into an order delta (always)
// and an execution delta (only when x=="TRADE", an actual fill) — same Bybit-shaped
// field names as the REST snapshot (binanceOrderStatus/binanceSide already exist in
// exchange.go for exactly this translation).
func translateOrderUpdate(ev binanceOrderUpdateEvent) (order map[string]any, execution map[string]any, isFill bool) {
	o := ev.O
	order = map[string]any{
		"orderId":      fmt.Sprintf("%d", o.OrderId),
		"orderLinkId":  o.ClientOrderId,
		"symbol":       o.Symbol,
		"side":         binanceSide(o.Side),
		"orderType":    o.OrderType,
		"price":        o.Price,
		"qty":          o.OrigQty,
		"cumExecQty":   o.FilledQty,
		"orderStatus":  binanceOrderStatus(o.OrderStatus),
		"triggerPrice": o.StopPrice,
		"category":     "linear",
		"orderFilter":  binanceOrderFilter(o.OrderType),
	}
	if o.ExecutionType != "TRADE" {
		return order, nil, false
	}
	execution = map[string]any{
		"execId":      fmt.Sprintf("%d", o.TradeId),
		"symbol":      o.Symbol,
		"side":        binanceSide(o.Side),
		"execType":    "Trade",
		"execPrice":   o.LastFilledPrice,
		"execQty":     o.LastFilledQty,
		"execTime":    fmt.Sprintf("%d", o.TradeTime),
		"orderLinkId": o.ClientOrderId,
	}
	return order, execution, true
}

func parseFloatSafe(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// truncateForLog caps a raw WS payload at 500 bytes before it goes into a log line —
// long enough to see the full shape of a position/order event, short enough that a
// burst of events (or a much larger, unrelated message type) can't flood the log.
func truncateForLog(data []byte) string {
	const max = 500
	if len(data) <= max {
		return string(data)
	}
	return string(data[:max]) + "...(truncated)"
}

// RunPositionStream opens a Binance USDⓈ-M Futures user data stream (listenKey +
// binanceUserDataWsURL), fetches a REST snapshot, then relays WS deltas to the client
// conn until ctx is cancelled or conn closes — the Binance counterpart of
// pkg/trader.RunPositionStream (Bybit), matching its exact wire contract to the
// frontend so usePositionsWs.ts needs no exchange-specific branching.
func RunPositionStream(ctx context.Context, conn *websocket.Conn, creds trader.Credentials, accountName string, broadcastCh <-chan any) {
	// Mirror connection-lifecycle events to the server console, not just logMsg's
	// frontend-only WS log panel (w.send) — this exact gap (listenKey/dial
	// failure/connect-success being invisible server-side) is why the legacy-URL bug
	// (see binanceWsBase's doc comment) went unnoticed for ~6 months: the connection
	// always "succeeded" from the frontend's point of view, so there was nothing in the
	// one place anyone was actually looking (server logs) to suggest anything was wrong.
	log.Printf("trader ws (binance): RunPositionStream ENTERED account=%q", accountName)
	w := &wsWriter{conn: conn}
	type msg map[string]any
	logMsg := func(m string, errFlag ...bool) {
		isErr := len(errFlag) > 0 && errFlag[0]
		w.send(msg{"type": "log", "message": m, "error": isErr})
	}

	w.send(msg{"type": "account", "accountName": accountName})

	listenKey, err := getListenKey(ctx, creds)
	if err != nil {
		log.Printf("trader ws (binance): getListenKey FAILED account=%q: %v", accountName, err)
		logMsg("Ошибка получения listenKey Binance: "+err.Error(), true)
		return
	}
	log.Printf("trader ws (binance): listenKey obtained account=%q", accountName)

	dialer, err := proxy.WSDialerFor(creds.WhitelistedIPs)
	if err != nil {
		log.Printf("trader ws (binance): WSDialerFor FAILED account=%q: %v", accountName, err)
		logMsg("Ошибка выбора прокси для Binance WS: "+err.Error(), true)
		return
	}
	bConn, _, err := dialer.DialContext(ctx, binanceUserDataWsURL(listenKey), nil)
	if err != nil {
		log.Printf("trader ws (binance): DialContext FAILED account=%q: %v", accountName, err)
		logMsg("Ошибка подключения к Binance WS: "+err.Error(), true)
		return
	}
	defer bConn.Close()

	log.Printf("trader ws (binance): CONNECTED account=%q", accountName)
	logMsg("Подключено к Binance WS")

	e := NewBinanceExchange(creds)
	go func() {
		if err := fetchAndSendBinanceSnapshot(ctx, w, e); err != nil {
			logMsg("[REST] Ошибка снапшота: "+err.Error(), true)
		}
	}()

	keepAliveTicker := time.NewTicker(listenKeyRefreshInterval)
	defer keepAliveTicker.Stop()

	binCh := make(chan []byte, 64)
	binErrCh := make(chan error, 1)
	bConn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
	// Binance's WS server sends a protocol-level ping frame every ~3 minutes; gorilla's
	// default ping handler auto-replies with a pong but — critically — does NOT surface
	// the ping to ReadMessage()'s caller, so without this override our own read deadline
	// above never gets refreshed by it. On any account quiet for binanceReadTimeout (no
	// ACCOUNT_UPDATE/ORDER_TRADE_UPDATE — not unusual between trades), ReadMessage would
	// time out and RunPositionStream would tear down a perfectly healthy connection,
	// forcing a reconnect (and losing live updates until the next REST snapshot) for no
	// real reason. Refreshing the deadline here means a timeout now only fires when the
	// connection is genuinely dead (no data AND no protocol ping for binanceReadTimeout) —
	// matching Bybit's equivalent safety margin (bybitReadTimeout), which stays alive via
	// its own app-level ping/pong exchange instead.
	bConn.SetPingHandler(func(appData string) error {
		bConn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
		return bConn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	go func() {
		for {
			_, data, err := bConn.ReadMessage()
			if err != nil {
				binErrCh <- err
				return
			}
			bConn.SetReadDeadline(time.Now().Add(binanceReadTimeout)) //nolint:errcheck
			select {
			case binCh <- data:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case <-keepAliveTicker.C:
			if err := keepAliveListenKey(ctx, creds); err != nil {
				logMsg("Ошибка продления listenKey: "+err.Error(), true)
			}

		case err := <-binErrCh:
			logMsg("Binance WS закрыт: "+err.Error(), true)
			return

		case m := <-broadcastCh:
			w.send(m)

		case data := <-binCh:
			var head struct {
				EventType string `json:"e"`
				// EventTime: same reason as binanceAccountUpdateEvent/
				// binanceOrderUpdateEvent's own EventTime field below — without an
				// exact match for top-level "E", it collides case-insensitively with
				// "e" and json.Unmarshal fails on the type mismatch (number into
				// string), discarding the event entirely before head.EventType is
				// even looked at by the switch below.
				EventTime int64 `json:"E"`
			}
			if err := json.Unmarshal(data, &head); err != nil {
				log.Printf("trader ws (binance): unmarshal event head failed: %v raw=%s", err, truncateForLog(data))
				continue
			}
			// TEMP DIAGNOSTIC (2026-10-16): every event type that arrives, logged
			// unconditionally — not just the ones that end up producing a non-empty
			// delta. Added to find out whether ACCOUNT_UPDATE is arriving at all for
			// a live account where it's never once produced a logged delta across
			// several real position opens/closes — removes the guesswork between
			// "Binance never sends it" and "we parse/filter it into nothing".
			log.Printf("trader ws (binance): event type=%q", head.EventType)
			switch head.EventType {
			case "ACCOUNT_UPDATE":
				var ev binanceAccountUpdateEvent
				if err := json.Unmarshal(data, &ev); err != nil {
					log.Printf("trader ws (binance): unmarshal ACCOUNT_UPDATE failed: %v raw=%s", err, truncateForLog(data))
					continue
				}
				deltas := translateAccountUpdate(ev)
				log.Printf("trader ws (binance): ACCOUNT_UPDATE positions=%d deltas=%d raw=%s", len(ev.A.Positions), len(deltas), truncateForLog(data))
				if len(deltas) > 0 {
					w.send(msg{"type": "position", "dataType": "delta", "data": deltas})
					log.Printf("trader ws (binance): position/delta count=%d", len(deltas))
				}
			case "ORDER_TRADE_UPDATE":
				var ev binanceOrderUpdateEvent
				if err := json.Unmarshal(data, &ev); err != nil {
					log.Printf("trader ws (binance): unmarshal ORDER_TRADE_UPDATE failed: %v raw=%s", err, truncateForLog(data))
					continue
				}
				order, execution, isFill := translateOrderUpdate(ev)
				w.send(msg{"type": "order", "dataType": "delta", "data": []map[string]any{order}})
				if isFill {
					w.send(msg{"type": "execution", "data": []map[string]any{execution}})
				}
			}
		}
	}
}

func fetchAndSendBinanceSnapshot(ctx context.Context, w *wsWriter, e *BinanceExchange) error {
	positions, err := e.FetchPositions(ctx)
	if err != nil {
		return fmt.Errorf("positions: %w", err)
	}
	w.send(map[string]any{"type": "position", "dataType": "snapshot", "data": positions})

	orders, err := e.FetchAllOpenOrders(ctx)
	if err != nil {
		return fmt.Errorf("orders: %w", err)
	}
	w.send(map[string]any{"type": "order", "dataType": "snapshot", "data": orders})

	eq, available, err := e.GetWalletBalance(ctx)
	if err == nil {
		w.send(map[string]any{"type": "wallet", "availableBalance": available, "equity": eq})
	}
	return nil
}
