package trader

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sis/pkg/proxy"
)

// ErrPositionModeUnsupported is returned by SwitchPositionMode when the symbol
// does not support position mode switching (Bybit retCode=10001).
// Callers should treat this as a permanent condition and skip the switch.
var ErrPositionModeUnsupported = errors.New("symbol does not support position mode switch")

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
		resp, err := proxy.HTTPClient().Get(bybitBase + "/v5/market/time")
		if err == nil {
			defer resp.Body.Close()
			var r struct {
				Time int64 `json:"time"`
			}
			if json.NewDecoder(resp.Body).Decode(&r) == nil && r.Time > 0 {
				tsOffset = r.Time - time.Now().UnixMilli()
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
	resp, err := proxy.HTTPClient().Do(req)
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
	resp, err := proxy.HTTPClient().Do(req)
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

/*
// extractRetCode unmarshals retCode/retMsg without allocating an error.
func extractRetCode(data []byte) (int, string) {
	var r struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
	}
	if json.Unmarshal(data, &r) != nil {
		return -1, ""
	}
	return r.RetCode, r.RetMsg
}

// isAgreementError returns true for retCodes that mean the user must sign
// a trading agreement before placing orders on this contract.
func isAgreementError(code int) bool {
	return code == 110123 || code == 110125 || code == 110126
}

// SignAgreement calls POST /v5/user/agreement.
// If categoryV2 is nil, the request is sent without category fields.
func SignAgreement(ctx context.Context, creds Credentials, categoryV2 *int) error {
	body := map[string]any{"agree": true}
	if categoryV2 != nil {
		body["categoryV2"] = *categoryV2
	}
	data, err := doSignedPOST(ctx, creds, "/v5/user/agreement", body)
	if err != nil {
		return err
	}
	return checkRetCode(data)
}

var agreementSignedCache sync.Map // key: apiKey, value: struct{}

// ptrVal returns the dereferenced value or "nil".
func ptrVal(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}

// trySignAgreement attempts to sign the trading agreement with a set of
// likely categoryV2 values. It returns nil as soon as one succeeds.
func trySignAgreement(ctx context.Context, creds Credentials) error {
	if _, ok := agreementSignedCache.Load(creds.APIKey); ok {
		return nil
	}

	// Known trad-fi mappings (from Bybit SDK docs):
	//   categoryV2=1 → metals, 2 → crude oil.
	//   category=2   → metals (legacy), 3 → crude oil (legacy).
	// For crypto derivatives the correct value is undocumented;
	// we try a wide range of values.
	candidates := []struct {
		v2  *int
		cat *int
	}{
		{v2: nil, cat: nil},        // no category at all
		{v2: intPtr(0), cat: nil},  // generic / crypto hypothesis
		{v2: intPtr(1), cat: nil},  // metals
		{v2: intPtr(2), cat: nil},  // crude oil
		{v2: intPtr(3), cat: nil},
		{v2: intPtr(4), cat: nil},
		{v2: intPtr(5), cat: nil},
		{v2: nil, cat: intPtr(1)},
		{v2: nil, cat: intPtr(2)},  // metals (legacy)
		{v2: nil, cat: intPtr(3)},  // crude oil (legacy)
		{v2: nil, cat: intPtr(4)},
		{v2: nil, cat: intPtr(5)},
	}
	for _, c := range candidates {
		body := map[string]any{"agree": true}
		if c.v2 != nil {
			body["categoryV2"] = *c.v2
		}
		if c.cat != nil {
			body["category"] = *c.cat
		}
		data, err := doSignedPOST(ctx, creds, "/v5/user/agreement", body)
		if err != nil {
			log.Printf("bybit: agreement request error (categoryV2=%v category=%v): %v", ptrVal(c.v2), ptrVal(c.cat), err)
			continue
		}
		code, msg := extractRetCode(data)
		log.Printf("bybit: agreement attempt (categoryV2=%v category=%v) → retCode=%d msg=%s", ptrVal(c.v2), ptrVal(c.cat), code, msg)
		if code == 0 {
			log.Printf("bybit: agreement signed (categoryV2=%v category=%v)", ptrVal(c.v2), ptrVal(c.cat))
			agreementSignedCache.Store(creds.APIKey, struct{}{})
			return nil
		}
		// retCode=10005 means the API key lacks permission for /v5/user/agreement.
		// No point trying other category combinations — abort immediately.
		if code == 10005 {
			return fmt.Errorf("bybit: API key lacks permission for POST /v5/user/agreement (retCode=10005). " +
				"Enable 'Account Transfer' or master-key permissions in Bybit API settings.")
		}
	}
	return fmt.Errorf("bybit: unable to sign trading agreement (tried %d combinations)", len(candidates))
}

func intPtr(v int) *int { return &v }
*/
func PlaceOrder(ctx context.Context, creds Credentials, req OrderRequest) (OrderResult, error) {
	data, err := doSignedPOST(ctx, creds, "/v5/order/create", req)
	if err != nil {
		return OrderResult{}, err
	}
	/*
	code, _ := extractRetCode(data)
	if isAgreementError(code) {
		if signErr := trySignAgreement(ctx, creds); signErr == nil {
			data, err = doSignedPOST(ctx, creds, "/v5/order/create", req)
			if err != nil {
				return OrderResult{}, err
			}
		} else {
			log.Printf("bybit: PlaceOrder agreement sign failed: %v", signErr)
		}
	}
	*/
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
	/*
	code, _ := extractRetCode(data)
	if isAgreementError(code) {
		if signErr := trySignAgreement(ctx, creds); signErr == nil {
			data, err = doSignedPOST(ctx, creds, "/v5/order/create-batch", req)
			if err != nil {
				return nil, err
			}
		} else {
			log.Printf("bybit: PlaceOrderBatch agreement sign failed: %v", signErr)
		}
	}
	*/
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
	/*
	// If any item failed due to a missing trading agreement, sign it and retry once.
	needsAgreement := false
	for _, r := range out {
		if isAgreementError(r.Code) {
			needsAgreement = true
			break
		}
	}
	if needsAgreement {
		if signErr := trySignAgreement(ctx, creds); signErr == nil {
			data, err = doSignedPOST(ctx, creds, "/v5/order/create-batch", req)
			if err != nil {
				return nil, err
			}
			if err := checkRetCode(data); err != nil {
				return nil, err
			}
			var resp2 struct {
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
			if err := json.Unmarshal(data, &resp2); err != nil {
				return nil, err
			}
			out2 := make([]BatchPlaceResult, len(resp2.Result.List))
			for i, item := range resp2.Result.List {
				out2[i].OrderId = item.OrderId
				out2[i].OrderLinkId = item.OrderLinkId
				if i < len(resp2.RetExtInfo.List) {
					out2[i].Code = resp2.RetExtInfo.List[i].Code
					out2[i].Msg = resp2.RetExtInfo.List[i].Msg
				}
			}
			return out2, nil
		} else {
			log.Printf("bybit: PlaceOrderBatch per-item agreement sign failed: %v", signErr)
		}
	}
	*/
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

// SetTradingStop sets or clears Bybit's native trailing stop on an open position.
// To remove an existing trailing stop, pass TrailingStop="0".
func SetTradingStop(ctx context.Context, creds Credentials, req TradingStopRequest) error {
	data, err := doSignedPOST(ctx, creds, "/v5/position/set-trading-stop", req)
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
	var errs []string
	for _, r := range reqs {
		q := "category=" + r.category + "&limit=200"
		if r.extra != "" {
			q += "&" + r.extra
		}
		data, err := doSignedGET(ctx, creds, "/v5/position/list", q)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s(%s): %v", r.category, r.extra, err))
			continue
		}
		if err := checkRetCode(data); err != nil {
			errs = append(errs, fmt.Sprintf("%s(%s): %v", r.category, r.extra, err))
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
	// Return error only when ALL three categories failed (partial failures are normal
	// for accounts that don't trade inverse or USDC, where Bybit may return auth errors).
	if len(errs) == len(reqs) {
		return nil, fmt.Errorf("FetchPositions: all categories failed: %s", strings.Join(errs, "; "))
	}
	return all, nil
}

// FetchPositionBySymbol fetches positions for a specific symbol from the exchange.
// It makes a single targeted API call rather than fetching all positions.
// Useful when only one symbol's position is needed (e.g. for TP base price).
func FetchPositionBySymbol(ctx context.Context, creds Credentials, category, symbol string) ([]Position, error) {
	q := "category=" + category + "&symbol=" + symbol
	data, err := doSignedGET(ctx, creds, "/v5/position/list", q)
	if err != nil {
		return nil, err
	}
	if err := checkRetCode(data); err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			List []Position `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Result.List {
		resp.Result.List[i].Category = category
	}
	return resp.Result.List, nil
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

// FetchOrderById looks up a single order by orderId.
// Checks active orders first; falls back to history if not found.
// Returns the order, whether it is still open (active), and any error.
func FetchOrderById(ctx context.Context, creds Credentials, category, symbol, orderId string) (Order, bool, error) {
	q := "category=" + category + "&symbol=" + symbol + "&orderId=" + orderId
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
	return Order{}, false, fmt.Errorf("order not found for orderId %s", orderId)
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
		base := "category=" + r.category + "&orderFilter=" + r.orderFilter + "&limit=50"
		if r.extra != "" {
			base += "&" + r.extra
		}
		cursor := ""
		for {
			q := base
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}
			data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
			if err != nil {
				break
			}
			var resp struct {
				Result struct {
					List           []Order `json:"list"`
					NextPageCursor string  `json:"nextPageCursor"`
				} `json:"result"`
			}
			if json.Unmarshal(data, &resp) != nil {
				break
			}
			for _, o := range resp.Result.List {
				o.Category = r.category
				o.OrderFilter = r.orderFilter
				all = append(all, o)
			}
			if resp.Result.NextPageCursor == "" {
				break
			}
			cursor = resp.Result.NextPageCursor
		}
	}
	return all, nil
}

// FetchOpenOrdersForSymbol fetches active (non-conditional) orders for a specific symbol.
// Used to find stale orders that were lost from our tracking (e.g. tpOrderID cleared from DB
// but the order still exists on the exchange).
func FetchOpenOrdersForSymbol(ctx context.Context, creds Credentials, category, symbol string) ([]Order, error) {
	q := "category=" + category + "&symbol=" + symbol + "&orderFilter=Order&limit=50"
	data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			List []Order `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Result.List {
		resp.Result.List[i].Category = category
		resp.Result.List[i].OrderFilter = "Order"
	}
	return resp.Result.List, nil
}

// FetchOpenOrdersForSymbolAll fetches active orders (Order + StopOrder) for a specific symbol.
// Used for safety sweeps to find any remaining orders after a cycle closes.
func FetchOpenOrdersForSymbolAll(ctx context.Context, creds Credentials, category, symbol string) ([]Order, error) {
	var all []Order
	for _, filter := range []string{"Order", "StopOrder"} {
		base := "category=" + category + "&symbol=" + symbol + "&orderFilter=" + filter + "&limit=50"
		cursor := ""
		for {
			q := base
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}
			data, err := doSignedGET(ctx, creds, "/v5/order/realtime", q)
			if err != nil {
				return nil, err
			}
			var resp struct {
				Result struct {
					List           []Order `json:"list"`
					NextPageCursor string  `json:"nextPageCursor"`
				} `json:"result"`
			}
			if err := json.Unmarshal(data, &resp); err != nil {
				return nil, err
			}
			for i := range resp.Result.List {
				resp.Result.List[i].Category = category
				resp.Result.List[i].OrderFilter = filter
			}
			all = append(all, resp.Result.List...)
			if resp.Result.NextPageCursor == "" {
				break
			}
			cursor = resp.Result.NextPageCursor
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

// FetchRecentClosedPnl fetches all closed PnL records since `since` for a given category.
// Pages through cursor until items become older than `since`.
func FetchRecentClosedPnl(ctx context.Context, creds Credentials, category string, since time.Time) ([]ClosedPnl, error) {
	startMs := strconv.FormatInt(since.UnixMilli(), 10)
	var all []ClosedPnl
	cursor := ""
	for {
		q := "category=" + category + "&limit=50&startTime=" + startMs
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		data, err := doSignedGET(ctx, creds, "/v5/position/closed-pnl", q)
		if err != nil {
			return all, err
		}
		if err := checkRetCode(data); err != nil {
			return all, err
		}
		var resp struct {
			Result struct {
				List           []ClosedPnl `json:"list"`
				NextPageCursor string      `json:"nextPageCursor"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return all, err
		}
		for i := range resp.Result.List {
			resp.Result.List[i].Category = category
		}
		all = append(all, resp.Result.List...)
		if resp.Result.NextPageCursor == "" {
			break
		}
		cursor = resp.Result.NextPageCursor
	}
	return all, nil
}

// FetchClosedPnlForSymbol fetches the most recent closed PnL records for a specific symbol.
// Used by the trade recorder to find the Bybit-authoritative PnL right after a cycle closes.
func FetchClosedPnlForSymbol(ctx context.Context, creds Credentials, category, symbol string, limit int) ([]ClosedPnl, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	q := "category=" + category + "&symbol=" + symbol + "&limit=" + strconv.Itoa(limit)
	data, err := doSignedGET(ctx, creds, "/v5/position/closed-pnl", q)
	if err != nil {
		return nil, err
	}
	if err := checkRetCode(data); err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			List []ClosedPnl `json:"list"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Result.List {
		resp.Result.List[i].Category = category
	}
	return resp.Result.List, nil
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
	case 10001: // symbol does not support position mode switch (e.g. dated futures)
		return ErrPositionModeUnsupported
	case 110024:
		return fmt.Errorf("нельзя переключить режим при открытой позиции — сначала закройте все позиции и ордера по %s", symbol)
	default:
		return fmt.Errorf("bybit: retCode=%d: %s", r.RetCode, r.RetMsg)
	}
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
