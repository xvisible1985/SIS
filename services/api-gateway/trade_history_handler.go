package main

import (
	"encoding/json"
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
	AvgEntry   *float64 `json:"avg_entry"`
	ExitPrice  *float64 `json:"exit_price"`
	Qty        *float64 `json:"qty"`
	VolumeUSDT *float64 `json:"volume_usdt"`
	PnL        *float64 `json:"pnl"`
	PnLPct     *float64 `json:"pnl_pct"`
	OpenedAt   string   `json:"opened_at"`
	ClosedAt   string   `json:"closed_at"`
}

type tradeHistoryStats struct {
	Total      int      `json:"total"`
	Wins       int      `json:"wins"`
	Losses     int      `json:"losses"`
	WinRate    float64  `json:"win_rate"`
	TotalPnL   float64  `json:"total_pnl"`
	AvgPnL     float64  `json:"avg_pnl"`
	BestTrade  *float64 `json:"best_trade"`
	WorstTrade *float64 `json:"worst_trade"`
}

type tradeHistoryResponse struct {
	Stats  tradeHistoryStats `json:"stats"`
	Trades []tradeHistoryRow `json:"trades"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit"`
	Offset int               `json:"offset"`
}

// GetTradeHistory returns paginated trade history with aggregate stats.
// GET /api/trade-history?bot_id=&strategy_id=&from=&to=&result=&limit=&offset=
func (s *Server) GetTradeHistory(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()

	botID := q.Get("bot_id")
	strategyID := q.Get("strategy_id")
	result := q.Get("result") // "tp" | "sl" | ""
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

	// Sort — whitelist to prevent SQL injection
	validCols := map[string]string{
		"symbol":      "th.symbol",
		"direction":   "th.direction",
		"result":      "th.result",
		"bot_name":    "b.name",
		"volume_usdt": "th.volume_usdt",
		"pnl":         "th.pnl",
		"closed_at":   "th.closed_at",
		"duration":    "(th.closed_at - th.opened_at)",
	}
	sortColExpr := "th.closed_at"
	if expr, ok := validCols[q.Get("sort_by")]; ok {
		sortColExpr = expr
	}
	sortDir := "DESC"
	if q.Get("sort_dir") == "asc" {
		sortDir = "ASC"
	}

	var fromTime, toTime *time.Time
	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			fromTime = &t
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			fromTime = fromTime // keep
			toTime = &t
		}
	}

	// Build WHERE conditions
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
	if result != "" {
		addArg("th.result =", result)
	}
	if fromTime != nil {
		addArg("th.closed_at >=", *fromTime)
	}
	if toTime != nil {
		addArg("th.closed_at <=", *toTime)
	}

	// Aggregate stats
	statsSQL := `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE result = 'tp')::int,
			COUNT(*) FILTER (WHERE result = 'sl')::int,
			COALESCE(SUM(pnl), 0),
			COALESCE(AVG(pnl), 0),
			MAX(pnl),
			MIN(pnl)
		FROM trade_history th ` + where
	var stats tradeHistoryStats
	var best, worst *float64
	if err := s.pool.QueryRow(r.Context(), statsSQL, args...).Scan(
		&stats.Total, &stats.Wins, &stats.Losses,
		&stats.TotalPnL, &stats.AvgPnL, &best, &worst,
	); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if stats.Total > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.Total) * 100
	}
	stats.BestTrade = best
	stats.WorstTrade = worst

	// Count for pagination
	var totalRows int
	countSQL := "SELECT COUNT(*)::int FROM trade_history th " + where
	s.pool.QueryRow(r.Context(), countSQL, args...).Scan(&totalRows) //nolint:errcheck

	// Paginated rows
	rowsSQL := `
		SELECT th.id, th.strategy_id, th.bot_id, b.name,
		       th.account_id, th.symbol, th.category, th.direction,
		       th.cycle_num, th.result,
		       th.avg_entry, th.exit_price, th.qty, th.volume_usdt,
		       th.pnl, th.pnl_pct, th.opened_at, th.closed_at
		FROM trade_history th
		LEFT JOIN bots b ON b.id = th.bot_id
		` + where + `
		ORDER BY ` + sortColExpr + ` ` + sortDir + `, th.id ` + sortDir + `
		LIMIT ` + strconv.Itoa(limit) + ` OFFSET ` + strconv.Itoa(offset)
	rows, err := s.pool.Query(r.Context(), rowsSQL, args...)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var trades []tradeHistoryRow
	for rows.Next() {
		var t tradeHistoryRow
		var openedAt, closedAt time.Time
		if err := rows.Scan(
			&t.ID, &t.StrategyID, &t.BotID, &t.BotName,
			&t.AccountID, &t.Symbol, &t.Category, &t.Direction,
			&t.CycleNum, &t.Result,
			&t.AvgEntry, &t.ExitPrice, &t.Qty, &t.VolumeUSDT,
			&t.PnL, &t.PnLPct, &openedAt, &closedAt,
		); err != nil {
			continue
		}
		t.OpenedAt = openedAt.UTC().Format(time.RFC3339)
		t.ClosedAt = closedAt.UTC().Format(time.RFC3339)
		trades = append(trades, t)
	}
	if trades == nil {
		trades = []tradeHistoryRow{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tradeHistoryResponse{
		Stats:  stats,
		Trades: trades,
		Total:  totalRows,
		Limit:  limit,
		Offset: offset,
	})
}
