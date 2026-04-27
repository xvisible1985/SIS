package main

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
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
