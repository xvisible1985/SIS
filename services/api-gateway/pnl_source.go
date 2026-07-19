// services/api-gateway/pnl_source.go
package main

// botPnlUnionSQL is a derived-table subquery normalizing every realized-PnL source into
// one (bot_id, owner_id, account_id, net_pnl, closed_at) row shape: full-cycle closes
// (trade_history), per-level matrix SL fills (strategy_levels.realized_pnl), and global
// matrix-TP re-arm profits (matrix_tp_profits). A matrix bot's profit lives mostly in the
// last two sources — reading trade_history alone makes such a bot vanish from stats
// entirely (its trade_history bot_id rows can be zero even though it's actively
// profitable). Keep this the ONLY place that defines "total bot PnL" so the dashboard
// leaderboard, bot-card stats, and the per-strategy "Накоплено" counters
// (GetHedgeSession, GetStrategyCumulativePnl) can't drift apart again.
//
// Usage: embed as a derived table, e.g. `FROM ` + botPnlUnionSQL + ` th JOIN bots b ON
// b.id = th.bot_id ...` — aliasing it `th` lets existing `WHERE th.owner_id = $1`-style
// filter strings keep working unchanged, since the union's projected columns
// (bot_id, owner_id, account_id, net_pnl, closed_at) match trade_history's own column
// names.
const botPnlUnionSQL = `(
	SELECT th.bot_id, th.owner_id, th.account_id, th.net_pnl, th.closed_at
	FROM trade_history th
	UNION ALL
	SELECT st.bot_id, st.owner_id, st.account_id, sl.realized_pnl AS net_pnl, sl.sl_closed_at AS closed_at
	FROM strategy_levels sl
	JOIN strategies st ON st.id = sl.strategy_id
	WHERE sl.realized_pnl IS NOT NULL
	UNION ALL
	SELECT mtp.bot_id, st.owner_id, mtp.account_id, mtp.net_pnl, mtp.closed_at
	FROM matrix_tp_profits mtp
	JOIN strategies st ON st.id = mtp.strategy_id
)`
