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
  /** "day" for all periods except "1d"; "hour" for the 1-day period. */
  granularity: 'day' | 'hour'
}

export async function getDashboard(period: '1d' | '7d' | '30d' | '90d' | '1y' | 'all' = '30d'): Promise<DashboardData> {
  const res = await apiClient.get<DashboardData>('/dashboard', { params: { period } })
  return res.data
}
