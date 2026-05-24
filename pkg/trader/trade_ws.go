package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const bybitTradeWS = "wss://stream.bybit.com/v5/trade"

type tradeResp struct {
	RetCode    int             `json:"retCode"`
	RetMsg     string          `json:"retMsg"`
	ReqId      string          `json:"reqId"`
	Data       json.RawMessage `json:"data"`
	RetExtInfo json.RawMessage `json:"retExtInfo"`
}

// TradeStream maintains a persistent WebSocket to Bybit /v5/trade.
// Each request is individually signed via the "header" field (per-request auth).
// Falls back to REST if not connected.
type TradeStream struct {
	creds        Credentials
	mu           sync.Mutex
	conn         *websocket.Conn // nil when not connected
	seq          int64
	pending      map[string]chan tradeResp
	wsDisabled   bool // set after permanent WS permission error (10005) to skip WS and use REST directly
}

func NewTradeStream(creds Credentials) *TradeStream {
	return &TradeStream{
		creds:   creds,
		pending: make(map[string]chan tradeResp),
	}
}

// Run connects and maintains the trade WS. Blocks until ctx is cancelled.
func (ts *TradeStream) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := ts.runOnce(ctx); err != nil {
			log.Printf("trader trade ws: %v, retry in 5s", err)
		}
		ts.mu.Lock()
		ts.conn = nil
		for id, ch := range ts.pending {
			select {
			case ch <- tradeResp{RetCode: -1, RetMsg: "disconnected"}:
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

func (ts *TradeStream) runOnce(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, bybitTradeWS, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Trade WS uses per-request signing — no connection-level auth op needed.
	ts.mu.Lock()
	ts.conn = conn
	ts.mu.Unlock()
	log.Printf("trader trade ws: connected")

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
			if op == "pong" {
				continue
			}
			reqId, _ := raw["reqId"].(string)
			if reqId == "" {
				continue
			}
			b, _ := json.Marshal(raw)
			var resp tradeResp
			json.Unmarshal(b, &resp) //nolint:errcheck
			ts.mu.Lock()
			ch, ok := ts.pending[reqId]
			if ok {
				delete(ts.pending, reqId)
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

// sendReq signs and sends a trade operation, then waits for the reqId response.
// Caller must not hold ts.mu.
func (ts *TradeStream) sendReq(ctx context.Context, op string, args any) (tradeResp, error) {
	start := time.Now()
	// WS trade per-request signing uses empty string as payload (unlike REST which uses request body).
	timestamp := serverTimestamp()
	sig := sign(timestamp, ts.creds.APIKey, ts.creds.SecretKey, recvWindow, "")

	ts.mu.Lock()
	conn := ts.conn
	if conn == nil {
		ts.mu.Unlock()
		return tradeResp{}, fmt.Errorf("not connected")
	}
	ts.seq++
	reqId := fmt.Sprintf("%d", ts.seq)
	ch := make(chan tradeResp, 1)
	ts.pending[reqId] = ch

	msg, _ := json.Marshal(map[string]any{
		"reqId": reqId,
		"op":    op,
		"args":  args,
		"header": map[string]string{
			"X-BAPI-API-KEY":     ts.creds.APIKey,
			"X-BAPI-TIMESTAMP":   timestamp,
			"X-BAPI-SIGN":        sig,
			"X-BAPI-RECV-WINDOW": recvWindow,
		},
	})
	err := conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		delete(ts.pending, reqId)
		ts.mu.Unlock()
		return tradeResp{}, err
	}
	ts.mu.Unlock()

	select {
	case resp := <-ch:
		if d := time.Since(start); d > 500*time.Millisecond {
			log.Printf("trader trade ws: %s took %v (slow)", op, d)
		}
		return resp, nil
	case <-time.After(10 * time.Second):
		ts.mu.Lock()
		delete(ts.pending, reqId)
		ts.mu.Unlock()
		return tradeResp{}, fmt.Errorf("timeout waiting for %s", op)
	case <-ctx.Done():
		ts.mu.Lock()
		delete(ts.pending, reqId)
		ts.mu.Unlock()
		return tradeResp{}, ctx.Err()
	}
}

// wsPermDenied returns true for retCodes that mean the WS request was rejected due
// to auth/permission issues rather than an actual order error. In this case we
// fallback to REST so trading continues uninterrupted.
func wsPermDenied(code int) bool {
	return code == 10005 || code == 10003 || code == 10004
}

// PlaceOrder places a single order via WS, falling back to REST.
func (ts *TradeStream) PlaceOrder(ctx context.Context, req OrderRequest) (OrderResult, error) {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if !disabled {
		resp, err := ts.sendReq(ctx, "order.create", []any{req})
		if err == nil && !wsPermDenied(resp.RetCode) {
			if resp.RetCode == 0 {
				var result OrderResult
				if err := json.Unmarshal(resp.Data, &result); err != nil {
					return OrderResult{}, fmt.Errorf("parse PlaceOrder response: %w", err)
				}
				return result, nil
			}
			/*
			// Auto-sign trading agreement and retry once on WS.
			if isAgreementError(resp.RetCode) {
				if signErr := trySignAgreement(ctx, ts.creds); signErr == nil {
					resp2, err2 := ts.sendReq(ctx, "order.create", []any{req})
					if err2 == nil && resp2.RetCode == 0 {
						var result OrderResult
						if err := json.Unmarshal(resp2.Data, &result); err != nil {
							return OrderResult{}, fmt.Errorf("parse PlaceOrder response: %w", err)
						}
						return result, nil
					}
					if err2 == nil {
						return OrderResult{}, fmt.Errorf("bybit: retCode=%d: %s", resp2.RetCode, resp2.RetMsg)
					}
					log.Printf("trader trade ws: PlaceOrder retry after agreement failed (%v)", err2)
				} else {
					log.Printf("trader trade ws: PlaceOrder agreement sign failed: %v", signErr)
				}
			}
			*/
			return OrderResult{}, fmt.Errorf("bybit: retCode=%d: %s", resp.RetCode, resp.RetMsg)
		}
		if err != nil {
			log.Printf("trader trade ws: PlaceOrder fallback (%v)", err)
		} else {
			log.Printf("trader trade ws: PlaceOrder WS perm error %d, REST fallback", resp.RetCode)
			ts.mu.Lock()
			ts.wsDisabled = true
			ts.mu.Unlock()
		}
	}
	return PlaceOrder(ctx, ts.creds, req)
}

// PlaceOrderBatch places multiple orders via WS, falling back to REST.
func (ts *TradeStream) PlaceOrderBatch(ctx context.Context, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if !disabled {
		resp, err := ts.sendReq(ctx, "order.create-batch", []any{req})
		if err == nil && !wsPermDenied(resp.RetCode) {
			if resp.RetCode == 0 {
				results, err := parseBatchPlaceWSResp(resp.Data, resp.RetExtInfo)
				if err != nil {
					return nil, err
				}
				/*
				// If any item failed due to a missing trading agreement, sign it
				// and fall back to REST (which handles per-item retries).
				for _, r := range results {
					if isAgreementError(r.Code) {
						signErr := trySignAgreement(ctx, ts.creds)
						if signErr == nil {
							return PlaceOrderBatch(ctx, ts.creds, req)
						}
						log.Printf("trader trade ws: PlaceOrderBatch per-item agreement sign failed: %v", signErr)
						break
					}
				}
				*/
				return results, nil
			}
			/*
			// Auto-sign trading agreement and retry once on WS.
			if isAgreementError(resp.RetCode) {
				if signErr := trySignAgreement(ctx, ts.creds); signErr == nil {
					resp2, err2 := ts.sendReq(ctx, "order.create-batch", []any{req})
					if err2 == nil && resp2.RetCode == 0 {
						return parseBatchPlaceWSResp(resp2.Data, resp2.RetExtInfo)
					}
					if err2 == nil {
						return nil, fmt.Errorf("bybit: retCode=%d: %s", resp2.RetCode, resp2.RetMsg)
					}
					log.Printf("trader trade ws: PlaceOrderBatch retry after agreement failed (%v)", err2)
				} else {
					log.Printf("trader trade ws: PlaceOrderBatch agreement sign failed: %v", signErr)
				}
			}
			*/
			return nil, fmt.Errorf("bybit: retCode=%d: %s", resp.RetCode, resp.RetMsg)
		}
		if err != nil {
			log.Printf("trader trade ws: PlaceOrderBatch fallback (%v)", err)
		} else {
			log.Printf("trader trade ws: PlaceOrderBatch WS perm error %d, REST fallback", resp.RetCode)
			ts.mu.Lock()
			ts.wsDisabled = true
			ts.mu.Unlock()
		}
	}
	return PlaceOrderBatch(ctx, ts.creds, req)
}

// CancelOrder cancels a single order via WS, falling back to REST.
func (ts *TradeStream) CancelOrder(ctx context.Context, req CancelRequest) error {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if !disabled {
		resp, err := ts.sendReq(ctx, "order.cancel", []any{req})
		if err == nil && !wsPermDenied(resp.RetCode) {
			if resp.RetCode != 0 {
				return fmt.Errorf("bybit: retCode=%d: %s", resp.RetCode, resp.RetMsg)
			}
			return nil
		}
		if err != nil {
			log.Printf("trader trade ws: CancelOrder fallback (%v)", err)
		} else {
			log.Printf("trader trade ws: CancelOrder WS perm error %d, REST fallback", resp.RetCode)
			ts.mu.Lock()
			ts.wsDisabled = true
			ts.mu.Unlock()
		}
	}
	return CancelOrder(ctx, ts.creds, req)
}

// CancelOrderBatch cancels specific orders via WS, falling back to REST.
func (ts *TradeStream) CancelOrderBatch(ctx context.Context, req BatchCancelRequest) error {
	ts.mu.Lock()
	disabled := ts.wsDisabled
	ts.mu.Unlock()
	if !disabled {
		resp, err := ts.sendReq(ctx, "order.cancel-batch", []any{req})
		if err == nil && !wsPermDenied(resp.RetCode) {
			if resp.RetCode != 0 {
				return fmt.Errorf("bybit: retCode=%d: %s", resp.RetCode, resp.RetMsg)
			}
			return nil
		}
		if err != nil {
			log.Printf("trader trade ws: CancelOrderBatch fallback (%v)", err)
		} else {
			log.Printf("trader trade ws: CancelOrderBatch WS perm error %d, REST fallback", resp.RetCode)
			ts.mu.Lock()
			ts.wsDisabled = true
			ts.mu.Unlock()
		}
	}
	return CancelOrderBatch(ctx, ts.creds, req)
}

func parseBatchPlaceWSResp(dataJSON, extInfoJSON json.RawMessage) ([]BatchPlaceResult, error) {
	var data struct {
		List []struct {
			OrderId     string `json:"orderId"`
			OrderLinkId string `json:"orderLinkId"`
		} `json:"list"`
	}
	if err := json.Unmarshal(dataJSON, &data); err != nil {
		return nil, fmt.Errorf("parse batch response: %w", err)
	}
	var extInfo struct {
		List []struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		} `json:"list"`
	}
	json.Unmarshal(extInfoJSON, &extInfo) //nolint:errcheck

	out := make([]BatchPlaceResult, len(data.List))
	for i, item := range data.List {
		out[i].OrderId = item.OrderId
		out[i].OrderLinkId = item.OrderLinkId
		if i < len(extInfo.List) {
			out[i].Code = extInfo.List[i].Code
			out[i].Msg = extInfo.List[i].Msg
		}
	}
	return out, nil
}
