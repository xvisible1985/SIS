package trader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	bybitBase  = "https://api.bybit.com"
	recvWindow = "10000"
)

func sign(timestamp, apiKey, secret, recvWin, payload string) string {
	msg := timestamp + apiKey + recvWin + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

var (
	tsMu      sync.Mutex
	tsOffset  int64
	tsUpdated time.Time
)

func serverTimestamp() string {
	tsMu.Lock()
	defer tsMu.Unlock()
	if time.Since(tsUpdated) > 5*time.Minute {
		resp, err := http.Get(bybitBase + "/v5/market/time")
		if err == nil {
			defer resp.Body.Close()
			var r struct {
				Time string `json:"time"`
			}
			if json.NewDecoder(resp.Body).Decode(&r) == nil && r.Time != "" {
				var serverMs int64
				fmt.Sscanf(r.Time, "%d", &serverMs)
				tsOffset = serverMs - time.Now().UnixMilli()
				tsUpdated = time.Now()
			}
		}
	}
	return fmt.Sprintf("%d", time.Now().UnixMilli()+tsOffset)
}

func authHeaders(creds Credentials, payload string) map[string]string {
	ts := serverTimestamp()
	sig := sign(ts, creds.APIKey, creds.SecretKey, recvWindow, payload)
	return map[string]string{
		"X-BAPI-API-KEY":     creds.APIKey,
		"X-BAPI-SIGN":        sig,
		"X-BAPI-TIMESTAMP":   ts,
		"X-BAPI-RECV-WINDOW": recvWindow,
	}
}

func doSignedGET(ctx context.Context, creds Credentials, path, query string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bybitBase+path+"?"+query, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range authHeaders(creds, query) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func doSignedPOST(ctx context.Context, creds Credentials, path string, body any) ([]byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bybitBase+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range authHeaders(creds, string(b)) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func checkRetCode(data []byte) error {
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("bybit: parse response: %w", err)
	}
	if r.RetCode != 0 {
		return fmt.Errorf("bybit: retCode=%d: %s", r.RetCode, r.RetMsg)
	}
	return nil
}

func PlaceOrder(ctx context.Context, creds Credentials, req OrderRequest) (OrderResult, error) {
	data, err := doSignedPOST(ctx, creds, "/v5/order/create", req)
	if err != nil {
		return OrderResult{}, err
	}
	if err := checkRetCode(data); err != nil {
		return OrderResult{}, err
	}
	var r struct {
		Result OrderResult `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return OrderResult{}, err
	}
	return r.Result, nil
}

func CancelOrder(ctx context.Context, creds Credentials, req CancelRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/order/cancel", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

// PlaceOrderBatch places up to 20 orders in a single REST call.
// Results are returned in the same order as req.Request.
// Per-item errors have Code != 0 and do not fail the whole batch.
func PlaceOrderBatch(ctx context.Context, creds Credentials, req BatchPlaceRequest) ([]BatchPlaceResult, error) {
	data, err := doSignedPOST(ctx, creds, "/v5/order/create-batch", req)
	if err != nil {
		return nil, err
	}
	if err := checkRetCode(data); err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			List []struct {
				OrderId     string `json:"orderId"`
				OrderLinkId string `json:"orderLinkId"`
			} `json:"list"`
		} `json:"result"`
		RetExtInfo struct {
			List []struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			} `json:"list"`
		} `json:"retExtInfo"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	out := make([]BatchPlaceResult, len(resp.Result.List))
	for i, item := range resp.Result.List {
		out[i].OrderId = item.OrderId
		out[i].OrderLinkId = item.OrderLinkId
		if i < len(resp.RetExtInfo.List) {
			out[i].Code = resp.RetExtInfo.List[i].Code
			out[i].Msg = resp.RetExtInfo.List[i].Msg
		}
	}
	return out, nil
}

// CancelOrderBatch cancels up to 20 specific orders in one REST call.
// Per-item errors do not fail the whole batch — check retExtInfo if needed.
func CancelOrderBatch(ctx context.Context, creds Credentials, req BatchCancelRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/order/cancel-batch", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

func CancelAllOrders(ctx context.Context, creds Credentials, req CancelAllRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/order/cancel-all", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

func SetLeverage(ctx context.Context, creds Credentials, req LeverageRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/position/set-leverage", req)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

func FetchPositions(ctx context.Context, creds Credentials) ([]Position, error) {
	type req struct {
		category string
		extra    string
	}
	reqs := []req{
		{"linear", "settleCoin=USDT"},
		{"linear", "settleCoin=USDC"},
		{"inverse", ""},
	}
	var all []Position
	for _, r := range reqs {
		q := "category=" + r.category + "&limit=200"
		if r.extra != "" {
			q += "&" + r.extra
		}
		data, err := doSignedGET(ctx, creds, "/v5/position/list", q)
		if err != nil {
			continue
		}
		var resp struct {
			Result struct {
				List []Position `json:"list"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &resp) == nil {
			for _, p := range resp.Result.List {
				p.Category = r.category
				all = append(all, p)
			}
		}
	}
	return all, nil
}

// FetchOrderByLinkId looks up a single order by orderLinkId.
// Checks active orders first; falls back to history if not found.
// Returns the order, whether it is still open (active), and any error.
func FetchOrderByLinkId(ctx context.Context, creds Credentials, category, symbol, orderLinkId string) (Order, bool, error) {
	q := "category=" + category + "&symbol=" + symbol + "&orderLinkId=" + orderLinkId
	for _, endpoint := range []string{"/v5/order/realtime", "/v5/order/history"} {
		data, err := doSignedGET(ctx, creds, endpoint, q)
		if err != nil {
			continue
		}
		var resp struct {
			Result struct {
				List []Order `json:"list"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &resp) == nil && len(resp.Result.List) > 0 {
			o := resp.Result.List[0]
			o.Category = category
			return o, endpoint == "/v5/order/realtime", nil
		}
	}
	return Order{}, false, fmt.Errorf("order not found for linkId %s", orderLinkId)
}

func FetchOpenOrders(ctx context.Context, creds Credentials) ([]Order, error) {
	type req struct {
		category    string
		orderFilter string
		extra       string
	}
	reqs := []req{
		{"linear", "Order", "settleCoin=USDT"},
		{"linear", "StopOrder", "settleCoin=USDT"},
		{"inverse", "Order", ""},
		{"inverse", "StopOrder", ""},
		{"spot", "Order", ""},
		{"spot", "StopOrder", ""},
	}
	var all []Order
	for _, r := range reqs {
		q := "category=" + r.category + "&orderFilter=" + r.orderFilter + "&limit=50"
		if r.extra != "" {
			q += "&" + r.extra
		}
		data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
		if err != nil {
			continue
		}
		var resp struct {
			Result struct {
				List []Order `json:"list"`
			} `json:"result"`
		}
		if json.Unmarshal(data, &resp) == nil {
			for _, o := range resp.Result.List {
				o.Category = r.category
				o.OrderFilter = r.orderFilter
				all = append(all, o)
			}
		}
	}
	return all, nil
}

// FetchOpenOrdersForCategory fetches open orders for a single exchange category
// (Order + StopOrder filters) using two parallel HTTP requests instead of six sequential ones.
func FetchOpenOrdersForCategory(ctx context.Context, creds Credentials, category string) ([]Order, error) {
	type result struct {
		orders []Order
		err    error
	}
	fetch := func(ch chan<- result, orderFilter string) {
		q := "category=" + category + "&orderFilter=" + orderFilter + "&limit=50"
		if category == "linear" {
			q += "&settleCoin=USDT"
		}
		data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
		if err != nil {
			ch <- result{err: err}
			return
		}
		var resp struct {
			Result struct {
				List []Order `json:"list"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			ch <- result{err: err}
			return
		}
		orders := resp.Result.List
		for i := range orders {
			orders[i].Category = category
			orders[i].OrderFilter = orderFilter
		}
		ch <- result{orders: orders}
	}
	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)
	go fetch(ch1, "Order")
	go fetch(ch2, "StopOrder")
	r1, r2 := <-ch1, <-ch2
	if r1.err != nil && r2.err != nil {
		return nil, r1.err
	}
	return append(r1.orders, r2.orders...), nil
}

func FetchOrderHistory(ctx context.Context, creds Credentials, category, cursor string) ([]Order, string, error) {
	q := "category=" + category + "&limit=50"
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	data, err := doSignedGET(ctx, creds, "/v5/order/history", q)
	if err != nil {
		return nil, "", err
	}
	if err := checkRetCode(data); err != nil {
		return nil, "", err
	}
	var resp struct {
		Result struct {
			List           []Order `json:"list"`
			NextPageCursor string  `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	return resp.Result.List, resp.Result.NextPageCursor, nil
}

func FetchExecutions(ctx context.Context, creds Credentials, category, cursor string) ([]Execution, string, error) {
	q := "category=" + category + "&limit=100"
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	data, err := doSignedGET(ctx, creds, "/v5/execution/list", q)
	if err != nil {
		return nil, "", err
	}
	if err := checkRetCode(data); err != nil {
		return nil, "", err
	}
	var resp struct {
		Result struct {
			List           []Execution `json:"list"`
			NextPageCursor string      `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	return resp.Result.List, resp.Result.NextPageCursor, nil
}

func FetchClosedPnl(ctx context.Context, creds Credentials, category, cursor string) ([]ClosedPnl, string, error) {
	q := "category=" + category + "&limit=50"
	if cursor != "" {
		q += "&cursor=" + cursor
	}
	data, err := doSignedGET(ctx, creds, "/v5/position/closed-pnl", q)
	if err != nil {
		return nil, "", err
	}
	if err := checkRetCode(data); err != nil {
		return nil, "", err
	}
	var resp struct {
		Result struct {
			List           []ClosedPnl `json:"list"`
			NextPageCursor string      `json:"nextPageCursor"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, "", err
	}
	for i := range resp.Result.List {
		resp.Result.List[i].Category = category
	}
	return resp.Result.List, resp.Result.NextPageCursor, nil
}

// GetWalletBalance returns total equity and available balance in USDT.
// Tries UNIFIED account first, then CONTRACT (classic accounts).
func GetWalletBalance(ctx context.Context, creds Credentials) (equity, available float64, err error) {
	for _, accType := range []string{"UNIFIED", "CONTRACT"} {
		data, e := doSignedGET(ctx, creds, "/v5/account/wallet-balance", "accountType="+accType)
		if e != nil {
			continue
		}
		if e := checkRetCode(data); e != nil {
			continue
		}
		var r struct {
			Result struct {
				List []struct {
					TotalEquity           string `json:"totalEquity"`
					TotalAvailableBalance string `json:"totalAvailableBalance"`
				} `json:"list"`
			} `json:"result"`
		}
		if e := json.Unmarshal(data, &r); e != nil || len(r.Result.List) == 0 {
			continue
		}
		fmt.Sscanf(r.Result.List[0].TotalEquity, "%f", &equity)
		fmt.Sscanf(r.Result.List[0].TotalAvailableBalance, "%f", &available)
		if equity > 0 {
			return equity, available, nil
		}
	}
	return equity, available, nil
}

// SwitchPositionMode switches between one-way (mode=0) and hedge (mode=3) for a symbol.
func SwitchPositionMode(ctx context.Context, creds Credentials, category, symbol string, mode int) error {
	body := map[string]any{
		"category": category,
		"symbol":   symbol,
		"mode":     mode,
	}
	data, err := doSignedPOST(ctx, creds, "/v5/position/switch-mode", body)
	if err != nil {
		return err
	}
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("bybit: parse response: %w", err)
	}
	switch r.RetCode {
	case 0, 110025: // 110025 = already in this mode
		return nil
	case 110024:
		return fmt.Errorf("нельзя переключить режим при открытой позиции — сначала закройте все позиции и ордера по %s", symbol)
	default:
		return fmt.Errorf("bybit: retCode=%d: %s", r.RetCode, r.RetMsg)
	}
	return nil
}

// QueryAPI calls /v5/user/query-api and returns the raw result JSON.
func QueryAPI(ctx context.Context, creds Credentials) (json.RawMessage, error) {
	data, err := doSignedGET(ctx, creds, "/v5/user/query-api", "")
	if err != nil {
		return nil, err
	}
	if err := checkRetCode(data); err != nil {
		return nil, err
	}
	var r struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Result, nil
}

// FetchMarkPrice returns the current mark price for a symbol from Bybit REST.
func FetchMarkPrice(ctx context.Context, creds Credentials, category, symbol string) (float64, error) {
	q := "category=" + category + "&symbol=" + symbol
	data, err := doSignedGET(ctx, creds, "/v5/market/tickers", q)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			List []struct {
				MarkPrice string `json:"markPrice"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, err
	}
	if len(resp.Result.List) == 0 {
		return 0, fmt.Errorf("no ticker for %s/%s", category, symbol)
	}
	price, err := strconv.ParseFloat(resp.Result.List[0].MarkPrice, 64)
	if err != nil {
		return 0, fmt.Errorf("parse mark price: %w", err)
	}
	return price, nil
}
