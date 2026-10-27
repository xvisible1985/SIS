package trader

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

// FundsFlow is one external movement of funds into (Amount > 0) or out of (Amount < 0) the
// trading account — a deposit/transfer-in or a withdrawal/transfer-out, in USDT/USDC terms.
// Trading PnL, fees and funding are NOT flows.
type FundsFlow struct {
	Time   time.Time
	Amount float64
}

// FundsFlowFetcher is an optional capability of an Exchange (kept out of the Exchange
// interface so existing fakes/implementations are unaffected): the dashboard's "Депозит"
// widget type-asserts for it to split a day's equity change into trading vs deposits/
// withdrawals.
type FundsFlowFetcher interface {
	// FetchFundsFlows returns every flow in [since, until]; windowing/pagination is internal.
	FetchFundsFlows(ctx context.Context, since, until time.Time) ([]FundsFlow, error)
}

var _ FundsFlowFetcher = (*BybitExchange)(nil)

// bybitFlowWindow is Bybit's transaction-log cap on endTime-startTime.
const bybitFlowWindow = 7 * 24 * time.Hour

// FetchFundsFlows reads the UNIFIED account's transaction log for TRANSFER_IN/TRANSFER_OUT
// (the log's own signed `change`), 7-day windows, cursor-paginated. Only USDT/USDC rows
// count (1:1 to USD); other coins are skipped.
func (e *BybitExchange) FetchFundsFlows(ctx context.Context, since, until time.Time) ([]FundsFlow, error) {
	var out []FundsFlow
	for _, typ := range []string{"TRANSFER_IN", "TRANSFER_OUT"} {
		for ws := since; ws.Before(until); ws = ws.Add(bybitFlowWindow) {
			we := ws.Add(bybitFlowWindow)
			if we.After(until) {
				we = until
			}
			cursor := ""
			for page := 0; page < 20; page++ {
				q := "accountType=UNIFIED&type=" + typ +
					"&startTime=" + strconv.FormatInt(ws.UnixMilli(), 10) +
					"&endTime=" + strconv.FormatInt(we.UnixMilli(), 10) + "&limit=50"
				if cursor != "" {
					q += "&cursor=" + cursor
				}
				data, err := doSignedGET(ctx, e.creds, "/v5/account/transaction-log", q)
				if err != nil {
					return nil, err
				}
				if err := checkRetCode(data); err != nil {
					return nil, err
				}
				var r struct {
					Result struct {
						NextPageCursor string `json:"nextPageCursor"`
						List           []struct {
							Currency        string `json:"currency"`
							Change          string `json:"change"`
							TransactionTime string `json:"transactionTime"`
						} `json:"list"`
					} `json:"result"`
				}
				if err := json.Unmarshal(data, &r); err != nil {
					return nil, err
				}
				for _, it := range r.Result.List {
					if it.Currency != "USDT" && it.Currency != "USDC" {
						continue
					}
					amt, err1 := strconv.ParseFloat(it.Change, 64)
					ms, err2 := strconv.ParseInt(it.TransactionTime, 10, 64)
					if err1 != nil || err2 != nil || amt == 0 {
						continue
					}
					out = append(out, FundsFlow{Time: time.UnixMilli(ms).UTC(), Amount: amt})
				}
				cursor = r.Result.NextPageCursor
				if cursor == "" || len(r.Result.List) == 0 {
					break
				}
			}
		}
	}
	return out, nil
}
