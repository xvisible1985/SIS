// services/api-gateway/dashboard_handler.go
package main

import (
	"net/http"
	"strconv"
	"time"
)

type dashboardDayPnL struct {
	Day    string  `json:"day"`
	PnL    float64 `json:"pnl"`
	Trades int     `json:"trades"`
	Wins   int     `json:"wins"`
}

// Granularity controls the time resolution of the DailyPnL buckets.
// "day"  – one entry per calendar day  (all periods except "1d")
// "hour" – one entry per hour         (period "1d")

type dashboardBotStat struct {
	BotID  string  `json:"bot_id"`
	Name   string  `json:"name"`
	Status string  `json:"status"`
	Trades int     `json:"trades"`
	Wins   int     `json:"wins"`
	PnL    float64 `json:"pnl"`
}

type dashboardPeriodStats struct {
	Total        int      `json:"total"`
	Wins         int      `json:"wins"`
	Losses       int      `json:"losses"`
	WinRate      float64  `json:"win_rate"`
	TotalPnL     float64  `json:"total_pnl"`
	AvgPnL       float64  `json:"avg_pnl"`
	BestTrade    *float64 `json:"best_trade"`
	WorstTrade   *float64 `json:"worst_trade"`
	ProfitFactor float64  `json:"profit_factor"`
}

type dashboardRecentTrade struct {
	ID        string   `json:"id"`
	Symbol    string   `json:"symbol"`
	Direction string   `json:"direction"`
	Result    string   `json:"result"`
	BotName   *string  `json:"bot_name"`
	PnL       *float64 `json:"pnl"`
	PnLPct    *float64 `json:"pnl_pct"`
	ClosedAt  string   `json:"closed_at"`
}

type dashboardResponse struct {
	Stats        dashboardPeriodStats   `json:"stats"`
	DailyPnL     []dashboardDayPnL      `json:"daily_pnl"`
	BotStats     []dashboardBotStat     `json:"bot_stats"`
	RecentTrades []dashboardRecentTrade `json:"recent_trades"`
	Granularity  string                 `json:"granularity"` // "day" | "hour"
}

// GetDashboard returns aggregated trade stats, daily PnL, bot leaderboard, and recent trades.
// GET /dashboard?period=1d|7d|30d|90d|1y|all&account_id=
func (s *Server) GetDashboard(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	period := q.Get("period")
	accountID := q.Get("account_id")

	granularity := "day"
	if period == "1d" {
		granularity = "hour"
	}

	now := time.Now().UTC()
	var since *time.Time
	switch period {
	case "1d":
		t := now.Add(-24 * time.Hour)
		since = &t
	case "7d":
		t := now.AddDate(0, 0, -7)
		since = &t
	case "30d":
		t := now.AddDate(0, 0, -30)
		since = &t
	case "90d":
		t := now.AddDate(0, 0, -90)
		since = &t
	case "1y":
		t := now.AddDate(-1, 0, 0)
		since = &t
	}

	ctx := r.Context()

	// Build reusable base filter.
	baseWhere := "WHERE th.owner_id = $1"
	baseArgs := []any{userID}
	n := 2
	addFilter := func(cond string, val any) {
		baseWhere += " AND " + cond + " $" + strconv.Itoa(n)
		baseArgs = append(baseArgs, val)
		n++
	}
	if accountID != "" {
		addFilter("th.account_id =", accountID)
	}
	if since != nil {
		addFilter("th.closed_at >=", *since)
	}

	// ── 1. Period stats ───────────────────────────────────────────────────────
	var stats dashboardPeriodStats
	{
		row := s.pool.QueryRow(ctx, `
			SELECT
				COUNT(*)                                                      AS total,
				COUNT(*) FILTER (WHERE net_pnl > 0)                          AS wins,
				COUNT(*) FILTER (WHERE net_pnl <= 0)                         AS losses,
				COALESCE(SUM(net_pnl), 0)                                    AS total_pnl,
				COALESCE(AVG(net_pnl), 0)                                    AS avg_pnl,
				MAX(net_pnl)                                                  AS best_trade,
				MIN(net_pnl)                                                  AS worst_trade,
				COALESCE(SUM(net_pnl) FILTER (WHERE net_pnl > 0), 0)        AS gross_profit,
				COALESCE(ABS(SUM(net_pnl) FILTER (WHERE net_pnl < 0)), 0)   AS gross_loss
			FROM trade_history th `+baseWhere,
			baseArgs...,
		)
		var grossProfit, grossLoss float64
		_ = row.Scan(
			&stats.Total, &stats.Wins, &stats.Losses,
			&stats.TotalPnL, &stats.AvgPnL,
			&stats.BestTrade, &stats.WorstTrade,
			&grossProfit, &grossLoss,
		)
		if stats.Total > 0 {
			stats.WinRate = float64(stats.Wins) / float64(stats.Total) * 100
		}
		switch {
		case grossLoss > 0:
			stats.ProfitFactor = grossProfit / grossLoss
		case grossProfit > 0:
			stats.ProfitFactor = 999
		}
	}

	// ── 2. Daily / hourly PnL ────────────────────────────────────────────────
	dailyPnL := []dashboardDayPnL{}
	{
		trunc := "day"
		if granularity == "hour" {
			trunc = "hour"
		}
		rows, err := s.pool.Query(ctx, `
			SELECT
				DATE_TRUNC('`+trunc+`', th.closed_at) AS bucket,
				COALESCE(SUM(th.net_pnl), 0)          AS pnl,
				COUNT(*)                               AS trades,
				COUNT(*) FILTER (WHERE th.net_pnl > 0) AS wins
			FROM trade_history th `+baseWhere+`
			GROUP BY bucket
			ORDER BY bucket ASC`,
			baseArgs...,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var bucket time.Time
				var entry dashboardDayPnL
				if rows.Scan(&bucket, &entry.PnL, &entry.Trades, &entry.Wins) == nil {
					entry.Day = bucket.Format(time.RFC3339)
					dailyPnL = append(dailyPnL, entry)
				}
			}
		}
	}

	// ── 3. Bot leaderboard ───────────────────────────────────────────────────
	botStats := []dashboardBotStat{}
	{
		// Extend baseArgs with bot_id IS NOT NULL (no extra param needed — it's a constant).
		rows, err := s.pool.Query(ctx, `
			SELECT
				b.id, b.name, b.status,
				COUNT(th.id)                                AS trades,
				COUNT(th.id) FILTER (WHERE th.net_pnl > 0) AS wins,
				COALESCE(SUM(th.net_pnl), 0)               AS pnl
			FROM trade_history th
			JOIN bots b ON b.id = th.bot_id `+baseWhere+`
				AND th.bot_id IS NOT NULL
			GROUP BY b.id, b.name, b.status
			ORDER BY pnl DESC
			LIMIT 10`,
			baseArgs...,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var bs dashboardBotStat
				if rows.Scan(&bs.BotID, &bs.Name, &bs.Status, &bs.Trades, &bs.Wins, &bs.PnL) == nil {
					botStats = append(botStats, bs)
				}
			}
		}
	}

	// ── 4. Recent trades ─────────────────────────────────────────────────────
	recentTrades := []dashboardRecentTrade{}
	{
		rows, err := s.pool.Query(ctx, `
			SELECT
				th.id, th.symbol, th.direction, th.result,
				b.name,
				th.net_pnl,
				th.pnl_pct,
				th.closed_at
			FROM trade_history th
			LEFT JOIN bots b ON b.id = th.bot_id `+baseWhere+`
			ORDER BY th.closed_at DESC
			LIMIT 10`,
			baseArgs...,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var rt dashboardRecentTrade
				var closedAt time.Time
				if rows.Scan(&rt.ID, &rt.Symbol, &rt.Direction, &rt.Result,
					&rt.BotName, &rt.PnL, &rt.PnLPct, &closedAt) == nil {
					rt.ClosedAt = closedAt.Format(time.RFC3339)
					recentTrades = append(recentTrades, rt)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, dashboardResponse{
		Stats:        stats,
		DailyPnL:     dailyPnL,
		BotStats:     botStats,
		RecentTrades: recentTrades,
		Granularity:  granularity,
	})
}
