package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	traderPkg "sis/pkg/trader"
	"sis/pkg/trader/binance"
)

type traderOrderRow struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"account_id"`
	OrderLinkID  string    `json:"order_link_id"`
	OrderID      *string   `json:"order_id"`
	Exchange     string    `json:"exchange"`
	Symbol       string    `json:"symbol"`
	Category     string    `json:"category"`
	Side         string    `json:"side"`
	OrderType    string    `json:"order_type"`
	Qty          string    `json:"qty"`
	Price        *string   `json:"price"`
	TriggerPrice *string   `json:"trigger_price"`
	Status       string    `json:"status"`
	CumExecQty   string    `json:"cum_exec_qty"`
	CumExecFee   string    `json:"cum_exec_fee"`
	CreatedAt    time.Time `json:"created_at"`
}

type execRow struct {
	ID          string    `json:"id"`
	ExecID      string    `json:"exec_id"`
	OrderID     *string   `json:"order_id"`
	OrderLinkID *string   `json:"order_link_id"`
	Symbol      string    `json:"symbol"`
	Category    string    `json:"category"`
	Side        *string   `json:"side"`
	ExecType    string    `json:"exec_type"`
	Qty         *string   `json:"qty"`
	Price       *string   `json:"price"`
	ExecValue   *string   `json:"exec_value"`
	ExecFee     *string   `json:"exec_fee"`
	FeeRate     *string   `json:"fee_rate"`
	IsMaker     *bool     `json:"is_maker"`
	ExecTime    time.Time `json:"exec_time"`
}

// ListTraderOrders returns paginated orders for the authenticated user.
// GET /trader/orders?account_id=&status=open|closed|all&symbol=&page=1&limit=50
func (s *Server) ListTraderOrders(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	status := q.Get("status")
	symbol := q.Get("symbol")
	accountID := q.Get("account_id")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if accountID != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, accountID)
		i++
	}
	if symbol != "" {
		where += fmt.Sprintf(" AND symbol=$%d", i)
		args = append(args, symbol)
		i++
	}
	switch status {
	case "open":
		where += " AND status NOT IN ('Filled','Cancelled','Rejected','Deactivated')"
	case "closed":
		where += " AND status IN ('Filled','Cancelled','Rejected','Deactivated')"
	}

	var total int
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM trader_orders WHERE "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.pool.Query(r.Context(),
		fmt.Sprintf(`SELECT id, account_id, order_link_id, order_id, exchange, symbol, category, side, order_type,
			qty::text, price::text, trigger_price::text, status, cum_exec_qty::text, cum_exec_fee::text, created_at
			FROM trader_orders WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, i, i+1),
		args...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]traderOrderRow, 0)
	for rows.Next() {
		var o traderOrderRow
		if err := rows.Scan(&o.ID, &o.AccountID, &o.OrderLinkID, &o.OrderID, &o.Exchange,
			&o.Symbol, &o.Category, &o.Side, &o.OrderType,
			&o.Qty, &o.Price, &o.TriggerPrice, &o.Status,
			&o.CumExecQty, &o.CumExecFee, &o.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, o)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "row error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orders": result, "total": total, "page": page})
}

// ListTraderExecutions returns paginated executions for the authenticated user.
// GET /trader/executions?account_id=&type=Trade|Funding|Fee|all&symbol=&from=&to=&page=1&limit=100
func (s *Server) ListTraderExecutions(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	execType := q.Get("type")
	symbol := q.Get("symbol")
	accountID := q.Get("account_id")
	fromStr := q.Get("from")
	toStr := q.Get("to")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset := (page - 1) * limit

	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if accountID != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, accountID)
		i++
	}
	if execType != "" && execType != "all" {
		where += fmt.Sprintf(" AND exec_type=$%d", i)
		args = append(args, execType)
		i++
	}
	if symbol != "" {
		where += fmt.Sprintf(" AND symbol=$%d", i)
		args = append(args, symbol)
		i++
	}
	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			where += fmt.Sprintf(" AND exec_time>=$%d", i)
			args = append(args, t)
			i++
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			where += fmt.Sprintf(" AND exec_time<=$%d", i)
			args = append(args, t)
			i++
		}
	}

	var total int
	s.pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM trader_executions WHERE "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.pool.Query(r.Context(),
		fmt.Sprintf(`SELECT id, exec_id, order_id, order_link_id, symbol, category, side, exec_type,
			qty::text, price::text, exec_value::text, exec_fee::text, fee_rate::text, is_maker, exec_time
			FROM trader_executions WHERE %s ORDER BY exec_time DESC LIMIT $%d OFFSET $%d`, where, i, i+1),
		args...,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	result := make([]execRow, 0)
	for rows.Next() {
		var e execRow
		if err := rows.Scan(&e.ID, &e.ExecID, &e.OrderID, &e.OrderLinkID, &e.Symbol, &e.Category,
			&e.Side, &e.ExecType, &e.Qty, &e.Price, &e.ExecValue, &e.ExecFee, &e.FeeRate,
			&e.IsMaker, &e.ExecTime); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error")
			return
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "row error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": result, "total": total, "page": page})
}

// GetClosedPnl fetches closed position P&L from Bybit.
// GET /trader/pnl?account_id=&category=linear&cursor=
// closedPnlTabLookback bounds the Binance branch below — Binance has no Bybit-style
// cursor-paginated closed-pnl endpoint to mirror, so there's no natural "page size" to
// ask for; a fixed lookback keeps a single request bounded instead of walking back
// indefinitely.
//
// 7 days (not 30, as it originally was) — found live (2026-10-17): a 30-day lookback
// meant BinanceExchange.FetchRecentClosedPnl's forEachWindowSince walked ~5 separate
// ≤7-day REST windows every single tab load (Binance caps userTrades'/income's own
// startTime..endTime span at 7 days, so a wider lookback can't be done in fewer windows
// — see forEachWindowSince's doc comment), each with its own /fapi/v1/income call plus
// one /fapi/v1/userTrades call per discovered symbol. At 7 days the window loop always
// runs exactly once, cutting that fan-out roughly 5x on top of the concurrency and
// caching fixes below.
const closedPnlTabLookback = 7 * 24 * time.Hour

// closedPnlCacheTTL: see closedPnlCache's doc comment.
const closedPnlCacheTTL = 30 * time.Second

type closedPnlCacheEntry struct {
	list      []traderPkg.ClosedPnl
	expiresAt time.Time
}

// closedPnlCache is a short-lived, process-local cache for GetClosedPnl's response,
// keyed by "accountID|category|cursor". Added alongside the lookback/concurrency/
// resolver fixes above — found live (2026-10-17) alongside them: the P&L tab fully
// unmounts and remounts on every tab switch (TerminalPage.tsx's conditional render),
// with no cache anywhere (unlike positions/orders, which usePositionsWs.ts caches via
// WS + localStorage), so even after the Binance fan-out above is cut down, toggling the
// tab back and forth within a few seconds still re-pays the whole REST round-trip (or,
// for Bybit, at least one) every time for no reason. 30s is short enough that genuinely
// new data (a trade closing) shows up quickly on the NEXT tab open, long enough to
// absorb "switched tabs and switched right back" within the same session.
var (
	closedPnlCacheMu sync.Mutex
	closedPnlCache   = map[string]closedPnlCacheEntry{}
)

func closedPnlCacheKey(accountID, category, cursor string) string {
	return accountID + "|" + category + "|" + cursor
}

func getClosedPnlCache(key string) ([]traderPkg.ClosedPnl, bool) {
	closedPnlCacheMu.Lock()
	defer closedPnlCacheMu.Unlock()
	e, ok := closedPnlCache[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.list, true
}

func setClosedPnlCache(key string, list []traderPkg.ClosedPnl) {
	closedPnlCacheMu.Lock()
	defer closedPnlCacheMu.Unlock()
	closedPnlCache[key] = closedPnlCacheEntry{list: list, expiresAt: time.Now().Add(closedPnlCacheTTL)}
}

// loadOrderLinkIDMap bulk-preloads this account's own order_id -> order_link_id mapping
// from trader_orders (one query, not one per closing order) for orders created at or
// after `since` — see BinanceExchange.SetOrderLinkIDResolver's doc comment for why. Rows
// with a non-numeric order_id (shouldn't happen for Binance, defensive only) are
// skipped, not treated as an error.
func (s *Server) loadOrderLinkIDMap(ctx context.Context, accountID string, since time.Time) (map[int64]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT order_id, order_link_id FROM trader_orders
		 WHERE account_id=$1 AND order_id IS NOT NULL AND created_at >= $2`,
		accountID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]string)
	for rows.Next() {
		var orderIDStr, linkID string
		if err := rows.Scan(&orderIDStr, &linkID); err != nil {
			return nil, err
		}
		if id, err := strconv.ParseInt(orderIDStr, 10, 64); err == nil {
			out[id] = linkID
		}
	}
	return out, rows.Err()
}

func (s *Server) GetClosedPnl(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	accountID := r.URL.Query().Get("account_id")
	category := r.URL.Query().Get("category")
	cursor := r.URL.Query().Get("cursor")
	if category == "" {
		category = "linear"
	}

	var exchangeName string
	if err := s.pool.QueryRow(r.Context(),
		`SELECT exchange FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
		accountID, userID,
	).Scan(&exchangeName); err != nil {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}

	// Plan #4b/#4c: this used to always call traderPkg.FetchClosedPnl (Bybit's
	// /v5/position/closed-pnl, cursor-paginated), regardless of the account's actual
	// exchange — on a Binance account that meant sending a Binance key to Bybit's own
	// signed endpoint, which Bybit rejects outright (surfaced to the user as a flat
	// "Ошибка загрузки", no further detail). Binance has no equivalent cursor-paginated,
	// account-wide, symbol-agnostic endpoint to call instead, so this branch uses
	// trader.Exchange's own FetchRecentClosedPnl (already fixed to discover fully-closed
	// symbols via /fapi/v1/income, not just currently-open positions) over a fixed
	// lookback window, and always returns an empty nextCursor — the frontend's "Load
	// more" button simply never appears for a Binance account, a known, deliberate
	// simplification rather than building out Binance-side pagination to match a model
	// its own API doesn't share.
	cacheKey := closedPnlCacheKey(accountID, category, cursor)
	if cached, ok := getClosedPnlCache(cacheKey); ok {
		writeJSON(w, http.StatusOK, map[string]any{"list": cached, "nextCursor": ""})
		return
	}

	if exchangeName == "binance" {
		ex, err := s.loadExchange(r, accountID, userID)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		since := time.Now().Add(-closedPnlTabLookback)
		// See BinanceExchange.SetOrderLinkIDResolver's doc comment: trades a single
		// bulk DB query for the up-to-dozens of per-closing-order REST calls
		// FetchRecentClosedPnl would otherwise make. Best-effort — a failed preload
		// just means every row falls back to the REST lookup, same as before this
		// fix, not a request failure.
		if be, ok := ex.(*binance.BinanceExchange); ok {
			if linkIDs, err := s.loadOrderLinkIDMap(r.Context(), accountID, since); err != nil {
				log.Printf("trader: preload order_link_id map account=%s: %v", accountID, err)
			} else {
				be.SetOrderLinkIDResolver(func(_ context.Context, _ string, orderId int64) (string, bool) {
					id, ok := linkIDs[orderId]
					return id, ok
				})
			}
		}
		list, err := ex.FetchRecentClosedPnl(r.Context(), category, since)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		setClosedPnlCache(cacheKey, list)
		writeJSON(w, http.StatusOK, map[string]any{"list": list, "nextCursor": ""})
		return
	}

	creds, err := s.loadCreds(r, accountID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	list, nextCursor, err := traderPkg.FetchClosedPnl(r.Context(), creds, category, cursor)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	setClosedPnlCache(cacheKey, list)
	writeJSON(w, http.StatusOK, map[string]any{"list": list, "nextCursor": nextCursor})
}

// GetTraderStats returns aggregated fee/funding stats.
// GET /trader/stats?account_id=&from=&to=
func (s *Server) GetTraderStats(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	where := "owner_id=$1"
	args := []any{userID}
	i := 2
	if acc := q.Get("account_id"); acc != "" {
		where += fmt.Sprintf(" AND account_id=$%d", i)
		args = append(args, acc)
		i++
	}
	if f := q.Get("from"); f != "" {
		if t, err := time.Parse(time.RFC3339, f); err == nil {
			where += fmt.Sprintf(" AND exec_time>=$%d", i)
			args = append(args, t)
			i++
		}
	}
	if t := q.Get("to"); t != "" {
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			where += fmt.Sprintf(" AND exec_time<=$%d", i)
			args = append(args, ts)
		}
	}

	var totalFee, totalFunding float64
	var tradeCount int
	if err := s.pool.QueryRow(r.Context(),
		fmt.Sprintf(`SELECT
			COALESCE(SUM(exec_fee) FILTER (WHERE exec_type='Trade'),0),
			COALESCE(SUM(exec_fee) FILTER (WHERE exec_type='Funding'),0),
			COUNT(*) FILTER (WHERE exec_type='Trade')
			FROM trader_executions WHERE %s`, where),
		args...,
	).Scan(&totalFee, &totalFunding, &tradeCount); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_fee":     totalFee,
		"total_funding": totalFunding,
		"trade_count":   tradeCount,
	})
}
