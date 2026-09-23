// services/api-gateway/dashboard_handler.go
package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

type dashboardEquityPoint struct {
	Day    string  `json:"day"`
	Equity float64 `json:"equity"`
}

type dashboardResponse struct {
	Stats        dashboardPeriodStats   `json:"stats"`
	DailyPnL     []dashboardDayPnL      `json:"daily_pnl"`
	BotStats     []dashboardBotStat     `json:"bot_stats"`
	RecentTrades []dashboardRecentTrade `json:"recent_trades"`
	EquitySeries []dashboardEquityPoint `json:"equity_series"`
	Granularity  string                 `json:"granularity"` // "day" | "hour"
}

// dashboardPeriodSince maps a dashboard period preset to its start time ("all"/"" → nil,
// meaning no lower bound). Shared by GetDashboard and GetDashboardRecentTrades so the two
// endpoints always agree on what counts as "in the current period".
func dashboardPeriodSince(period string, now time.Time) *time.Time {
	switch period {
	case "1d":
		t := now.Add(-24 * time.Hour)
		return &t
	case "7d":
		t := now.AddDate(0, 0, -7)
		return &t
	case "30d":
		t := now.AddDate(0, 0, -30)
		return &t
	case "90d":
		t := now.AddDate(0, 0, -90)
		return &t
	case "1y":
		t := now.AddDate(-1, 0, 0)
		return &t
	default:
		return nil
	}
}

// buildDashboardBaseFilter builds the WHERE clause + args shared by every dashboard query:
// scoped to the owner, optionally to one account, and optionally to trades closed on/after
// since. When accountID is set, it also folds in that account's "Очистить статистику" marker
// (exchange_accounts.stats_cleared_at) — a non-destructive per-account cutoff, not a DELETE:
// trade_history rows before it stay intact for every other consumer (accounting,
// hedge_sessions accumulation, ...), only dashboard-scoped queries hide them. Returns the
// (possibly raised) since alongside the filter, since callers with their own since-dependent
// logic (equity series bucketing) need the final value too.
func buildDashboardBaseFilter(ctx context.Context, pool *pgxpool.Pool, userID, accountID string, since *time.Time) (where string, args []any, effectiveSince *time.Time) {
	if accountID != "" {
		var clearedAt *time.Time
		pool.QueryRow(ctx, //nolint:errcheck
			`SELECT stats_cleared_at FROM exchange_accounts WHERE id=$1 AND owner_id=$2`,
			accountID, userID,
		).Scan(&clearedAt)
		if clearedAt != nil && (since == nil || clearedAt.After(*since)) {
			since = clearedAt
		}
	}

	where = "WHERE th.owner_id = $1"
	args = []any{userID}
	n := 2
	addFilter := func(cond string, val any) {
		where += " AND " + cond + " $" + strconv.Itoa(n)
		args = append(args, val)
		n++
	}
	if accountID != "" {
		addFilter("th.account_id =", accountID)
	}
	if since != nil {
		addFilter("th.closed_at >=", *since)
	}
	return where, args, since
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
	since := dashboardPeriodSince(period, now)

	ctx := r.Context()

	baseWhere, baseArgs, since := buildDashboardBaseFilter(ctx, s.pool, userID, accountID, since)

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
				COUNT(*)                                AS trades,
				COUNT(*) FILTER (WHERE th.net_pnl > 0) AS wins,
				COALESCE(SUM(th.net_pnl), 0)           AS pnl
			FROM `+botPnlUnionSQL+` th
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

	// ── 5. Equity series (for the hero deposit/drawdown chart) ─────────────────
	// Only meaningful for a single selected account — balance_snapshots is written
	// per-account on GET /accounts/:id/balance, so there is no cross-account series to
	// bucket when accountID is empty (same "all accounts" limitation daily_pnl doesn't
	// have but the Equity widget already does).
	equitySeries := []dashboardEquityPoint{}
	if accountID != "" {
		trunc := "day"
		if granularity == "hour" {
			trunc = "hour"
		}
		args := []any{accountID, userID}
		sinceSQL := ""
		if since != nil {
			sinceSQL = " AND bs.created_at >= $3"
			args = append(args, *since)
		}
		rows, err := s.pool.Query(ctx, `
			SELECT bucket, equity FROM (
				SELECT DATE_TRUNC('`+trunc+`', bs.created_at) AS bucket, bs.equity,
					   ROW_NUMBER() OVER (
						   PARTITION BY DATE_TRUNC('`+trunc+`', bs.created_at)
						   ORDER BY bs.created_at DESC
					   ) AS rn
				FROM balance_snapshots bs
				JOIN exchange_accounts ea ON ea.id = bs.account_id
				WHERE bs.account_id = $1 AND ea.owner_id = $2`+sinceSQL+`
			) s WHERE rn = 1
			ORDER BY bucket ASC`,
			args...,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var bucket time.Time
				var entry dashboardEquityPoint
				if rows.Scan(&bucket, &entry.Equity) == nil {
					entry.Day = bucket.Format(time.RFC3339)
					equitySeries = append(equitySeries, entry)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, dashboardResponse{
		Stats:        stats,
		DailyPnL:     dailyPnL,
		BotStats:     botStats,
		RecentTrades: recentTrades,
		EquitySeries: equitySeries,
		Granularity:  granularity,
	})
}

type dashboardRecentTradesResponse struct {
	Trades  []dashboardRecentTrade `json:"trades"`
	HasMore bool                   `json:"has_more"`
}

// GetDashboardRecentTrades powers the "Все последние сделки" page reached by expanding the
// dashboard's "Последние сделки" widget: the same trades that widget shows (same account +
// period scope, via dashboardPeriodSince/buildDashboardBaseFilter — the exact filter GetDashboard
// applies to its own LIMIT-10 query), but paginated via limit/offset for infinite scroll instead
// of hard-capped at 10.
// GET /dashboard/recent-trades?period=1d|7d|30d|90d|1y|all&account_id=&limit=&offset=
func (s *Server) GetDashboardRecentTrades(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromCtx(r.Context())
	q := r.URL.Query()
	period := q.Get("period")
	accountID := q.Get("account_id")

	limit := 30
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}

	ctx := r.Context()
	now := time.Now().UTC()
	since := dashboardPeriodSince(period, now)
	baseWhere, baseArgs, _ := buildDashboardBaseFilter(ctx, s.pool, userID, accountID, since)

	// Fetch one extra row to know whether another page exists without a separate COUNT(*).
	args := append(append([]any{}, baseArgs...), limit+1, offset)
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
		LIMIT $`+strconv.Itoa(len(baseArgs)+1)+` OFFSET $`+strconv.Itoa(len(baseArgs)+2),
		args...,
	)
	trades := []dashboardRecentTrade{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var rt dashboardRecentTrade
			var closedAt time.Time
			if rows.Scan(&rt.ID, &rt.Symbol, &rt.Direction, &rt.Result,
				&rt.BotName, &rt.PnL, &rt.PnLPct, &closedAt) == nil {
				rt.ClosedAt = closedAt.Format(time.RFC3339)
				trades = append(trades, rt)
			}
		}
	}

	hasMore := len(trades) > limit
	if hasMore {
		trades = trades[:limit]
	}

	writeJSON(w, http.StatusOK, dashboardRecentTradesResponse{
		Trades:  trades,
		HasMore: hasMore,
	})
}
