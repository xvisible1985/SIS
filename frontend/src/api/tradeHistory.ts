import { apiClient } from './client'

export interface TradeHistoryRow {
  id: string
  strategy_id: string | null
  bot_id: string | null
  bot_name: string | null
  account_id: string
  symbol: string
  category: string
  direction: 'long' | 'short'
  cycle_num: number
  result: 'tp' | 'sl' | 'manual' | string
  avg_entry: number | null
  exit_price: number | null
  qty: number | null
  volume_usdt: number | null
  pnl: number | null
  pnl_pct: number | null
  fees: number
  funding: number
  net_pnl: number
  opened_at: string
  closed_at: string
}

export interface TradeHistoryStats {
  total: number
  wins: number
  losses: number
  win_rate: number
  total_pnl: number
  total_net_pnl: number
  avg_pnl: number
  best_trade: number | null
  worst_trade: number | null
  total_fees: number
  total_funding: number
}

export interface TradeHistoryResponse {
  stats: TradeHistoryStats
  trades: TradeHistoryRow[]
  total: number
  limit: number
  offset: number
}

export interface TradeHistoryParams {
  bot_id?: string
  strategy_id?: string
  result?: 'tp' | 'sl' | 'manual' | ''
  from?: string
  to?: string
  limit?: number
  offset?: number
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function getTradeHistory(params: TradeHistoryParams = {}): Promise<TradeHistoryResponse> {
  const q = new URLSearchParams()
  if (params.bot_id)      q.set('bot_id',      params.bot_id)
  if (params.strategy_id) q.set('strategy_id', params.strategy_id)
  if (params.result)      q.set('result',      params.result)
  if (params.from)        q.set('from',        params.from)
  if (params.to)          q.set('to',          params.to)
  if (params.limit    != null) q.set('limit',    String(params.limit))
  if (params.offset   != null) q.set('offset',   String(params.offset))
  if (params.sort_by)          q.set('sort_by',  params.sort_by)
  if (params.sort_dir)         q.set('sort_dir', params.sort_dir)
  const res = await apiClient.get<TradeHistoryResponse>(`/trade-history?${q}`)
  return res.data
}
