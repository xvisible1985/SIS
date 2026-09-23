import { apiClient } from './client'

export interface DashboardStats {
  total: number
  wins: number
  losses: number
  win_rate: number
  total_pnl: number
  avg_pnl: number
  best_trade: number | null
  worst_trade: number | null
  profit_factor: number
}

export interface DailyPnL {
  day: string
  pnl: number
  trades: number
  wins: number
}

export interface EquityPoint {
  day: string
  equity: number
}

export interface BotStat {
  bot_id: string
  name: string
  status: string
  trades: number
  wins: number
  pnl: number
}

export interface RecentTrade {
  id: string
  symbol: string
  direction: string
  result: string
  bot_name: string | null
  pnl: number | null
  pnl_pct: number | null
  closed_at: string
}

export interface DashboardData {
  stats: DashboardStats
  daily_pnl: DailyPnL[]
  bot_stats: BotStat[]
  recent_trades: RecentTrade[]
  equity_series: EquityPoint[]
  /** "day" for all periods except "1d"; "hour" for the 1-day period. */
  granularity: 'day' | 'hour'
}

export type DashboardPeriod = '1d' | '7d' | '30d' | '90d' | '1y' | 'all'

export interface DashboardRecentTradesPage {
  trades: RecentTrade[]
  has_more: boolean
}

export async function getDashboard(
  period: DashboardPeriod = '30d',
  accountId?: string,
): Promise<DashboardData> {
  // account_id is optional server-side (falls back to aggregating across every account the
  // user owns) — but the dashboard should always show the currently selected account's own
  // stats, not a blend with other accounts' history. Omitting it here is what caused a
  // freshly connected account to display the previous account's stats (2026-08-19): the
  // backend was never told which account to scope to, so it silently aggregated all of them,
  // which — with the new account having no trade history yet — looked identical to the old
  // account's numbers.
  const params: Record<string, string> = { period }
  if (accountId) params.account_id = accountId
  const res = await apiClient.get<DashboardData>('/dashboard', { params })
  return res.data
}

// getDashboardRecentTrades powers the "Все последние сделки" page reached by expanding the
// dashboard's "Последние сделки" widget — same account + period scope as getDashboard (see
// its own comment on why account_id must always be passed when one is selected), paginated
// via limit/offset for infinite scroll instead of the widget's hard LIMIT 10.
export async function getDashboardRecentTrades(
  period: DashboardPeriod,
  accountId: string | undefined,
  limit: number,
  offset: number,
): Promise<DashboardRecentTradesPage> {
  const params: Record<string, string | number> = { period, limit, offset }
  if (accountId) params.account_id = accountId
  const res = await apiClient.get<DashboardRecentTradesPage>('/dashboard/recent-trades', { params })
  return res.data
}
