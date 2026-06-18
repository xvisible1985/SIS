// services/api-gateway/trade_history_handler.go
package main

import (
	"net/http"
	"strconv"
	"time"
)

type tradeHistoryRow struct {
	ID         string   `json:"id"`
	StrategyID *string  `json:"strategy_id"`
	BotID      *string  `json:"bot_id"`
	BotName    *string  `json:"bot_name"`
	AccountID  string   `json:"account_id"`
	Symbol     string   `json:"symbol"`
	Category   string   `json:"category"`
	Direction  string   `json:"direction"`
	CycleNum   int      `json:"cycle_num"`
	Result     string   `json:"result"`
	Source     string   `json:"source"` // "strategy" | "manual"
	AvgEntry   *float64 `json:"avg_entry"`
	ExitPrice  *float64 `json:"exit_price"`
	Qty        *float64 `json:"qty"`
	VolumeUSDT *float64 `json:"volume_usdt"`
	PnL        *float64 `json:"pnl"`
	PnLPct     *float64 `json:"pnl_pct"`
	Fees       float64  `json:"fees"`
	Funding    float64  `json:"funding"`
	NetPnL     float64  `json:"net_pnl"`
	OpenedAt   string   `json:"opened_at"`
	ClosedAt   string   `json:"closed_at"`
}

type tradeHistoryStats struct {
	Total        int      `json:"total"`
	Wins         int      `json:"wins"`
	Losses       int      `json:"losses"`
	WinRate      float64  `json:"win_rate"`
	TotalPnL     float64  `json:"total_pnl"`
	TotalNetPnL  float64  `json:"total_net_pnl"`
	AvgPnL       float64  `json:"avg_pnl"`
	BestTrade    *float64 `json:"best_trade"`
	WorstTrade   *float64 `json:"worst_trade"`
	TotalFees    float64  `json:"total_fees"`
	TotalFunding float64  `json:"total_funding"`
}

type tradeHistoryResponse struct {
	Stats  tradeHistoryStats `json:"stats"`
	Trades []tradeHistoryRow `json:"trades"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// GetTradeHistory returns paginated trade history with aggregate stats.
// One row per closed cycle (strategy) or per closed position (manual).
// GET /trade-history?bot_id=&strategy_id=&symbol=&source=&result=&from=&to=&limit=&offset=&sort_by=&sort_dir=
func (s *Server) GetTradeHistory(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()

	botID := q.Get("bot_id")
	strategyID := q.Get("strategy_id")
	accountID := q.Get("account_id")
	symbol := q.Get("symbol")
	source := q.Get("source")       // "strategy" | "manual" | ""
	result := q.Get("result")       // "tp" | "sl" | "manual" | ""
	fromStr := q.Get("from")
	toStr := q.Get("to")

	limit := 50
	offset := 0
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}

	validSorts := map[string]string{
		"symbol":    "th.symbol",
		"direction": "th.direction",
		"result":    "th.result",
		"bot_name":  "b.name",
		"volume":    "th.volume_usdt",
		"pnl":       "th.net_pnl",
		"pnl_gross": "th.pnl",
		"fees":      "th.fees",
		"funding":   "th.funding",
		"closed_at": "th.closed_at",
		"duration":  "(th.closed_at - th.opened_at)",
	}
	sortExpr := "th.closed_at"
	if expr, ok := validSorts[q.Get("sort_by")]; ok {
		sortExpr = expr
	}
	sortDir := "DESC"
	if q.Get("sort_dir") == "asc" {
		sortDir = "ASC"
	}

	var fromTime, toTime *time.Time
	if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
		fromTime = &t
	}
	if t, err := time.Parse(time.RFC3339, toStr); err == nil {
		toTime = &t
	}

	// Build WHERE clause.
	where := "WHERE th.owner_id = $1"
	args := []any{userID}
	n := 2
	addArg := func(cond string, val any) {
		where += " AND " + cond + " $" + strconv.Itoa(n)
		args = append(args, val)
		n++
	}
	if botID != "" {
		addArg("th.bot_id =", botID)
	}
	if strategyID != "" {
		addArg("th.strategy_id =", strategyID)
	}
	if accountID != "" {
		addArg("th.account_id =", accountID)
	}
	if symbol != "" {
		addArg("th.symbol =", symbol)
	}
	if source != "" {
		addArg("th.source =", source)
	}
	if result != "" {
		addArg("th.result =", result)
	}
	if fromTime != nil {
		addArg("th.closed_at >=", *fromTime)
	}
	if toTime != nil {
		addArg("th.closed_at <=", *toTime)
	}

	// Stats.
	statsSQL := `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE th.net_pnl > 0)::int,
			COUNT(*) FILTER (WHERE th.net_pnl < 0)::int,
			COALESCE(SUM(th.pnl), 0),
			COALESCE(SUM(th.net_pnl), 0),
			COALESCE(AVG(th.pnl), 0),
			MAX(th.net_pnl),
			MIN(th.net_pnl),
			COALESCE(SUM(th.fees), 0),
			COALESCE(SUM(th.funding), 0)
		FROM trade_history th ` + where

	var stats tradeHistoryStats
	var best, worst *float64
	if err := s.pool.QueryRow(r.Context(), statsSQL, args...).Scan(
		&stats.Total, &stats.Wins, &stats.Losses,
		&stats.TotalPnL, &stats.TotalNetPnL, &stats.AvgPnL,
		&best, &worst,
		&stats.TotalFees, &stats.TotalFunding,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if stats.Total > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.Total) * 100
	}
	stats.BestTrade = best
	stats.WorstTrade = worst

	// Total count for pagination.
	var totalRows int
	s.pool.QueryRow(r.Context(), //nolint:errcheck
		`SELECT COUNT(*)::int FROM trade_history th `+where, args...,
	).Scan(&totalRows)

	// Paginated rows.
	limitN := strconv.Itoa(limit)
	offsetN := strconv.Itoa(offset)
	rowsSQL := `
		SELECT
			th.id, th.strategy_id, th.bot_id, b.name,
			th.account_id, th.symbol, th.category, th.direction,
			th.cycle_num, th.result, COALESCE(th.source, 'strategy'),
			th.avg_entry, th.exit_price, th.qty, th.volume_usdt,
			th.pnl, th.pnl_pct, th.fees, th.funding, th.net_pnl,
			th.opened_at, th.closed_at
		FROM trade_history th
		LEFT JOIN bots b ON b.id = th.bot_id ` +
		where + ` ORDER BY ` + sortExpr + ` ` + sortDir +
		` LIMIT ` + limitN + ` OFFSET ` + offsetN

	dbRows, err := s.pool.Query(r.Context(), rowsSQL, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer dbRows.Close()

	trades := []tradeHistoryRow{}
	for dbRows.Next() {
		var t tradeHistoryRow
		var openedAt, closedAt time.Time
		if err := dbRows.Scan(
			&t.ID, &t.StrategyID, &t.BotID, &t.BotName,
			&t.AccountID, &t.Symbol, &t.Category, &t.Direction,
			&t.CycleNum, &t.Result, &t.Source,
			&t.AvgEntry, &t.ExitPrice, &t.Qty, &t.VolumeUSDT,
			&t.PnL, &t.PnLPct, &t.Fees, &t.Funding, &t.NetPnL,
			&openedAt, &closedAt,
		); err != nil {
			continue
		}
		t.OpenedAt = openedAt.UTC().Format(time.RFC3339)
		t.ClosedAt = closedAt.UTC().Format(time.RFC3339)
		trades = append(trades, t)
	}

	writeJSON(w, http.StatusOK, tradeHistoryResponse{
		Stats:  stats,
		Trades: trades,
		Total:  totalRows,
		Limit:  limit,
		Offset: offset,
	})
}

// GetTradeHistorySymbols returns distinct symbols that have trade history for the user.
// GET /trade-history/symbols
func (s *Server) GetTradeHistorySymbols(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	rows, err := s.pool.Query(r.Context(),
		`SELECT DISTINCT symbol FROM trade_history WHERE owner_id = $1 ORDER BY symbol`,
		userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	defer rows.Close()
	symbols := []string{}
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err == nil {
			symbols = append(symbols, sym)
		}
	}
	writeJSON(w, http.StatusOK, symbols)
}
