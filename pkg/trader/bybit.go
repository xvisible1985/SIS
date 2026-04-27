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
			}
			tsUpdated = time.Now()
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
