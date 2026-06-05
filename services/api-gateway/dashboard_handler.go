package main

import (
	"encoding/json"
	"net/http"
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
// GET /dashboard?period=1d|7d|30d|90d|1y|all
func (s *Server) GetDashboard(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())

	period := r.URL.Query().Get("period")
	now := time.Now().UTC()
	var cutoff time.Time
	switch period {
	case "1d":
		cutoff = now.AddDate(0, 0, -1)
	case "7d":
		cutoff = now.AddDate(0, 0, -7)
	case "90d":
		cutoff = now.AddDate(0, 0, -90)
	case "1y":
		cutoff = now.AddDate(-1, 0, 0)
	case "all":
		cutoff = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	default: // "30d"
		cutoff = now.AddDate(0, 0, -30)
	}

	// When period is "1d" we use hourly buckets; otherwise daily.
	granularity := "day"
	if period == "1d" {
		granularity = "hour"
	}

	// ─── Period stats ─────────────────────────────────────────────────────────
	var stats dashboardPeriodStats
	var best, worst *float64
	var grossProfit, grossLoss float64
	err := s.pool.QueryRow(r.Context(), `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE result = 'tp')::int,
			COUNT(*) FILTER (WHERE result = 'sl')::int,
			COALESCE(SUM(pnl), 0),
			COALESCE(AVG(pnl), 0),
			MAX(pnl),
			MIN(pnl),
			COALESCE(SUM(pnl) FILTER (WHERE pnl > 0), 0),
			COALESCE(ABS(SUM(pnl) FILTER (WHERE pnl < 0)), 0)
		FROM trade_history
		WHERE owner_id = $1 AND closed_at >= $2`,
		userID, cutoff,
	).Scan(
		&stats.Total, &stats.Wins, &stats.Losses,
		&stats.TotalPnL, &stats.AvgPnL, &best, &worst,
		&grossProfit, &grossLoss,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	stats.BestTrade = best
	stats.WorstTrade = worst
	if stats.Total > 0 {
		stats.WinRate = float64(stats.Wins) / float64(stats.Total) * 100
	}
	if grossLoss > 0 {
		stats.ProfitFactor = grossProfit / grossLoss
	} else if grossProfit > 0 {
		stats.ProfitFactor = 999 // infinite — no losing trades
	}

	// ─── Periodic PnL buckets (hourly for "1d", daily for all other periods) ────
	var bucketSQL, dayFmt string
	if granularity == "hour" {
		bucketSQL = `DATE_TRUNC('hour', closed_at AT TIME ZONE 'UTC')`
		dayFmt = "2006-01-02T15" // e.g. "2026-06-03T14" — frontend appends ":00"
	} else {
		bucketSQL = `DATE_TRUNC('day', closed_at AT TIME ZONE 'UTC')`
		dayFmt = "2006-01-02"
	}
	dailyRows, err := s.pool.Query(r.Context(), `
		SELECT
			`+bucketSQL+` AS bucket,
			COALESCE(SUM(pnl), 0),
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE result = 'tp')::int
		FROM trade_history
		WHERE owner_id = $1 AND closed_at >= $2
		GROUP BY 1
		ORDER BY 1`,
		userID, cutoff,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer dailyRows.Close()

	var dailyPnL []dashboardDayPnL
	for dailyRows.Next() {
		var d dashboardDayPnL
		var bucket time.Time
		if err := dailyRows.Scan(&bucket, &d.PnL, &d.Trades, &d.Wins); err != nil {
			continue
		}
		d.Day = bucket.UTC().Format(dayFmt)
		dailyPnL = append(dailyPnL, d)
	}
	if dailyPnL == nil {
		dailyPnL = []dashboardDayPnL{}
	}

	// ─── Bot stats (same period as selected filter) ───────────────────────────
	botRows, err := s.pool.Query(r.Context(), `
		SELECT b.id, b.name, b.status,
			COUNT(th.id)::int,
			COUNT(th.id) FILTER (WHERE th.result = 'tp')::int,
			COALESCE(SUM(th.pnl), 0)
		FROM bots b
		LEFT JOIN trade_history th
			ON th.bot_id = b.id AND th.owner_id = $1 AND th.closed_at >= $2
		WHERE b.owner_id = $1
		GROUP BY b.id, b.name, b.status
		ORDER BY COALESCE(SUM(th.pnl), 0) DESC
		LIMIT 10`,
		userID, cutoff,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer botRows.Close()

	var botStats []dashboardBotStat
	for botRows.Next() {
		var b dashboardBotStat
		if err := botRows.Scan(&b.BotID, &b.Name, &b.Status, &b.Trades, &b.Wins, &b.PnL); err != nil {
			continue
		}
		botStats = append(botStats, b)
	}
	if botStats == nil {
		botStats = []dashboardBotStat{}
	}

	// ─── Recent trades (last 10 within the selected period) ──────────────────
	recentRows, err := s.pool.Query(r.Context(), `
		SELECT th.id, th.symbol, th.direction, th.result,
			b.name, th.pnl, th.pnl_pct, th.closed_at
		FROM trade_history th
		LEFT JOIN bots b ON b.id = th.bot_id
		WHERE th.owner_id = $1 AND th.closed_at >= $2
		ORDER BY th.closed_at DESC
		LIMIT 10`,
		userID, cutoff,
	)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()

	var recentTrades []dashboardRecentTrade
	for recentRows.Next() {
		var t dashboardRecentTrade
		var closedAt time.Time
		if err := recentRows.Scan(
			&t.ID, &t.Symbol, &t.Direction, &t.Result,
			&t.BotName, &t.PnL, &t.PnLPct, &closedAt,
		); err != nil {
			continue
		}
		t.ClosedAt = closedAt.UTC().Format(time.RFC3339)
		recentTrades = append(recentTrades, t)
	}
	if recentTrades == nil {
		recentTrades = []dashboardRecentTrade{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dashboardResponse{
		Stats:        stats,
		DailyPnL:     dailyPnL,
		BotStats:     botStats,
		RecentTrades: recentTrades,
		Granularity:  granularity,
	})
}
