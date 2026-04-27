// Auth
export interface AuthResponse {
  token: string
  user_id: string
}

// Signals
export interface Signal {
  id: string
  name: string
  description: string
  exchange: string
  symbol: string
  market: string
  timeframe: string
  direction: string
  conditions: ConditionNode
  is_active: boolean
  created_at: string
}

export type ConditionNode = GroupNode | ConditionLeaf | SignalRefNode

export interface GroupNode {
  type: 'AND' | 'OR'
  children: ConditionNode[]
}

export interface ConditionLeaf {
  type: 'condition'
  indicator: string
  params: Record<string, number>
  operator: string
  value?: number
  compare_to?: { indicator: string; params: Record<string, number> }
}

export interface SignalRefNode {
  type: 'signal_ref'
  signal_id: string
}

// Backtest
export interface BacktestRequest {
  period_from: string
  period_to: string
  take_profit: number
  stop_loss: number
}

export interface BacktestResult {
  id: string
  signal_id: string
  symbol: string
  timeframe: string
  period_from: string
  period_to: string
  mode: string
  total_signals: number
  win_count: number
  loss_count: number
  win_rate: number
  avg_gain: number
  max_drawdown: number
  profit_factor: number
  patterns: unknown
  created_at: string
}

// Optimizer
export interface OptimizeRequest {
  period_from: string
  period_to: string
  mode: 'fast' | 'walk_forward'
  score_by: string
  top_n: number
  take_profits: number[]
  stop_losses: number[]
  param_space: Record<string, number[]>
  wf_folds: number
}

export interface RankedResult {
  params: Record<string, number>
  take_profit: number
  stop_loss: number
  score: number
  win_rate: number
  avg_gain: number
  profit_factor: number
  total_signals: number
}

export interface OptimizationResult {
  id: string
  signal_id: string
  mode: string
  top_combinations: RankedResult[]
  best_params: Record<string, number>
  created_at: string
}

// Webhooks
export interface Webhook {
  id: string
  signal_id: string
  url: string
  platform: string
  is_active: boolean
  created_at: string
}

// Job progress (WebSocket frame)
export interface ProgressMessage {
  pct: number
  status: string
  updated_at: number
}

// Exchange Accounts
export interface ExchangeAccount {
  id: string
  exchange: string
  label: string
  is_active: boolean
  created_at: string
}

// Trader — positions & orders (live, from WS)
export interface Position {
  symbol: string
  side: 'Buy' | 'Sell'
  size: string
  sizeUsdt: number
  entryPrice: string
  markPrice: string
  liqPrice: string
  unrealisedPnl: string
  unrealisedPnlPct: string
  leverage: string
  positionIdx: number
  category: string
}

export interface ActiveOrder {
  orderId: string
  orderLinkId: string
  symbol: string
  side: 'Buy' | 'Sell'
  orderType: string
  price: string
  qty: string
  cumExecQty: string
  orderStatus: string
  triggerPrice: string
  category: string
  orderFilter: string
}

// Trader — history (from REST)
export interface TraderOrder {
  id: string
  account_id: string
  order_link_id: string
  order_id: string | null
  exchange: string
  symbol: string
  category: string
  side: string
  order_type: string
  qty: string
  price: string | null
  trigger_price: string | null
  status: string
  cum_exec_qty: string
  cum_exec_fee: string
  created_at: string
}

export interface TraderExecution {
  id: string
  exec_id: string
  order_id: string | null
  order_link_id: string | null
  symbol: string
  category: string
  side: string | null
  exec_type: string
  qty: string | null
  price: string | null
  exec_value: string | null
  exec_fee: string | null
  fee_rate: string | null
  is_maker: boolean | null
  exec_time: string
}

export interface TraderStats {
  total_fee: number
  total_funding: number
  trade_count: number
}

export interface PaginatedResponse<T> {
  orders?: T[]
  executions?: T[]
  total: number
  page: number
}

// WS position stream messages
export type WsMsg =
  | { type: 'account'; accountName: string }
  | { type: 'log'; message: string; error?: boolean }
  | { type: 'position'; dataType: 'snapshot' | 'delta'; data: any[] }
  | { type: 'order'; dataType: 'snapshot' | 'delta'; data: any[] }
