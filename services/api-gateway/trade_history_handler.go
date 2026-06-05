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
// Rows are grouped by (strategy_id, cycle_num) so each matrix cycle appears
// as one row with summed PnL — intermediate mini-TP/SL entries are rolled up.
// GET /api/trade-history?bot_id=&strategy_id=&from=&to=&result=&limit=&offset=
func (s *Server) GetTradeHistory(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()

	botID := q.Get("bot_id")
	strategyID := q.Get("strategy_id")
	symbol := q.Get("symbol")
	result := q.Get("result") // "tp" | "sl" | "manual" | ""
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

	// Sort — whitelist to prevent SQL injection.
	// All expressions reference the outer "g" alias (post-grouping CTE).
	validCols := map[string]string{
		"symbol":      "g.symbol",
		"direction":   "g.direction",
		"result":      "g.result",
		"bot_name":    "b.name",
		"volume_usdt": "g.volume_usdt",
		"pnl":         "g.net_pnl",
		"pnl_gross":   "g.pnl",
		"fees":        "g.fees",
		"funding":     "g.funding",
		"closed_at":   "g.closed_at",
		"duration":    "(g.closed_at - g.opened_at)",
	}
	sortColExpr := "g.closed_at"
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
			toTime = &t
		}
	}

	// ── Inner WHERE (applied before grouping) ────────────────────────────────
	// result filter is excluded here — it is applied AFTER grouping so that
	// the final cycle result (not intermediate entries) is matched.
	innerWhere := "WHERE th.owner_id = $1"
	args := []any{userID}
	n := 2
	addArg := func(cond string, val any) {
		innerWhere += " AND " + cond + " $" + strconv.Itoa(n)
		args = append(args, val)
		n++
	}
	if botID != "" {
		addArg("th.bot_id =", botID)
	}
	if strategyID != "" {
		addArg("th.strategy_id =", strategyID)
	}
	if symbol != "" {
		addArg("th.symbol =", symbol)
	}
	if fromTime != nil {
		addArg("th.closed_at >=", *fromTime)
	}
	if toTime != nil {
		addArg("th.closed_at <=", *toTime)
	}

	// ── Outer WHERE (applied after grouping, on final cycle result) ───────────
	outerWhere := ""
	outerArgs := append([]any{}, args...)
	if result != "" {
		outerWhere = " WHERE g.result = $" + strconv.Itoa(n)
		outerArgs = append(outerArgs, result)
		n++
	}
	_ = n // may be unused if no result filter

	// ── CTE: group individual trade_history rows into one row per cycle ───────
	// The last result (by closed_at DESC) is used as the cycle outcome.
	// avg_entry and exit_price come from the most recent entry in the cycle.
	// volume_usdt, pnl, fees, funding, net_pnl are summed across all entries.
	groupCTE := `
		WITH raw AS (
			SELECT th.* FROM trade_history th ` + innerWhere + `
		),
		grouped AS (
			SELECT
				COALESCE(th.strategy_id::text, th.bot_id::text, th.account_id::text) || '-' || th.cycle_num::text AS id,
				th.strategy_id,
				th.bot_id,
				th.owner_id,
				th.account_id,
				th.symbol,
				th.category,
				th.direction,
				th.cycle_num,
				(array_agg(th.result      ORDER BY th.closed_at DESC))[1]     AS result,
				(array_agg(th.avg_entry   ORDER BY th.closed_at DESC))[1]     AS avg_entry,
				(array_agg(th.exit_price  ORDER BY th.closed_at DESC))[1]     AS exit_price,
				SUM(COALESCE(th.qty, 0))                                       AS qty,
				SUM(COALESCE(th.volume_usdt, 0))                               AS volume_usdt,
				SUM(COALESCE(th.pnl, 0))                                       AS pnl,
				CASE WHEN SUM(COALESCE(th.volume_usdt, 0)) > 0
				     THEN SUM(COALESCE(th.pnl, 0)) / SUM(COALESCE(th.volume_usdt, 0)) * 100
				     ELSE 0 END                                                AS pnl_pct,
				SUM(th.fees)                                                   AS fees,
				SUM(th.funding)                                                AS funding,
				SUM(th.net_pnl)                                                AS net_pnl,
				MIN(th.opened_at)                                              AS opened_at,
				MAX(th.closed_at)                                              AS closed_at
			FROM raw th
			GROUP BY th.strategy_id, th.cycle_num, th.bot_id, th.owner_id,
			         th.account_id, th.symbol, th.category, th.direction
		)`

	// ── Stats (counts cycles, not individual entries) ─────────────────────────
	statsSQL := groupCTE + `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE g.net_pnl > 0)::int,
			COUNT(*) FILTER (WHERE g.net_pnl < 0)::int,
			COALESCE(SUM(g.pnl), 0),
			COALESCE(SUM(g.net_pnl), 0),
			COALESCE(AVG(g.pnl), 0),
			MAX(g.net_pnl),
			MIN(g.net_pnl),
			COALESCE(SUM(g.fees), 0),
			COALESCE(SUM(g.funding), 0)
		FROM grouped g` + outerWhere

	var stats tradeHistoryStats
	var best, worst *float64
	if err := s.pool.QueryRow(r.Context(), statsSQL, outerArgs...).Scan(
		&stats.Total, &stats.Wins, &stats.Losses,
		&stats.TotalPnL, &stats.TotalNetPnL, &stats.AvgPnL, &best, &worst,
		&stats.TotalFees, &stats.TotalFunding,
	); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if stats.Total > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.Total) * 100
	}
	stats.BestTrade = best
	stats.WorstTrade = worst

	// ── Count for pagination ──────────────────────────────────────────────────
	countSQL := groupCTE + `
		SELECT COUNT(*)::int FROM grouped g` + outerWhere
	var totalRows int
	s.pool.QueryRow(r.Context(), countSQL, outerArgs...).Scan(&totalRows) //nolint:errcheck

	// ── Paginated rows ────────────────────────────────────────────────────────
	rowsSQL := groupCTE + `
		SELECT
			g.id, g.strategy_id, g.bot_id, b.name,
			g.account_id, g.symbol, g.category, g.direction,
			g.cycle_num, g.result,
			g.avg_entry, g.exit_price, g.qty, g.volume_usdt,
			g.pnl, g.pnl_pct, g.fees, g.funding, g.net_pnl,
			g.opened_at, g.closed_at
		FROM grouped g
		LEFT JOIN bots b ON b.id = g.bot_id` +
		outerWhere + `
		ORDER BY ` + sortColExpr + ` ` + sortDir + `, g.id ` + sortDir + `
		LIMIT ` + strconv.Itoa(limit) + ` OFFSET ` + strconv.Itoa(offset)

	rows, err := s.pool.Query(r.Context(), rowsSQL, outerArgs...)
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
			&t.PnL, &t.PnLPct, &t.Fees, &t.Funding, &t.NetPnL,
			&openedAt, &closedAt,
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

// GetTradeHistorySymbols returns distinct symbols that have trade history for the user.
// GET /api/trade-history/symbols
// Response: ["BTCUSDT", "ETHUSDT", ...]
func (s *Server) GetTradeHistorySymbols(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())

	rows, err := s.pool.Query(r.Context(),
		`SELECT DISTINCT symbol FROM trade_history WHERE owner_id = $1 ORDER BY symbol`,
		userID,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(symbols)
}
