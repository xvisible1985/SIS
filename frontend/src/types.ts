// Auth
export interface AuthResponse {
  token: string
  user_id: string
  email: string
  is_admin?: boolean
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

// Exchange Accounts
export interface ExchangeAccount {
  id: string
  exchange: string
  label: string
  is_active: boolean
  created_at: string
  expires_at?: string
}

// Job progress (WebSocket frame)
export interface ProgressMessage {
  pct: number
  status: string
  updated_at: number
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
  positionIM?: number
  // Trailing stop fields (populated from Bybit WS when trailing stop is active)
  trailingStop?: string  // callback distance in price units; "0" or "" = not set
  activePrice?: string   // price at which trailing activates; "0" or "" = not set
  stopLoss?: string      // current trailing stop level once activated; "0" or "" = not set
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
  createdTime?: string
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

export interface ChartExecution {
  execId: string
  symbol: string
  side: 'Buy' | 'Sell'
  price: number
  qty: string
  timeMs: number
  orderLinkId?: string
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
  | { type: 'execution'; dataType: 'delta'; data: any[] }
  | { type: 'wallet'; availableBalance: number; equity?: number }

// Strategies
export interface SignalConfig {
  name: string
  params: Record<string, unknown>
}

export interface GridStep {
  price_move_pct: number
  size_pct: number
  order_type?: 'exchange' | 'virtual'
  use_signal?: boolean
  signal_key?: string
}

export interface Strategy {
  id: string
  account_id: string
  symbol: string
  category: string
  direction: 'long' | 'short' | 'both'
  status: 'active' | 'finishing' | 'stopped'
  grid_levels: number
  grid_active: number
  max_stop_active: number
  grid_step_pct: number
  grid_size_usdt: number
  tp_mode: 'total' | 'per_level'
  tp_pct: number | null
  sl_type: 'conditional' | 'programmatic'
  sl_pct: number | null
  signal_filter: boolean
  leverage: number
  margin_type: 'isolated' | 'cross'
  hedge_mode: boolean
  strategy_type: 'grid' | 'matrix' | 'manual'
  entry_order_type: 'limit' | 'stop_market'
  signal_configs: SignalConfig[]
  steps: GridStep[] | null
  matrix_levels?: MatrixLevel[] | null
  safe_zone_pct?: number | null
  protected_build?: boolean | null
  matrix_rebuild_on_sl?: boolean | null
  matrix_rebuild_from_entry?: boolean | null
  size_as_main?: boolean | null
  hedged_strategy_id?: string | null
  matrix_entry_level?: MatrixEntryLevel | null
  trailing_stop_enabled: boolean
  trailing_activation_pct: number | null
  trailing_callback_pct: number | null
  tp_signal_configs?: SignalConfig[] | null
  tp_signal_dir?: 'buy' | 'sell' | null
  sl_signal_configs?: SignalConfig[] | null
  sl_signal_dir?: 'buy' | 'sell' | null
  created_at: string
  updated_at: string
  // Computed by server
  volume_usdt: number
  active_levels: number
  last_pnl: number
  last_filled_price?: number
  manual_alert?: string
  bot_id?: string | null
  bot_name?: string | null
  bot_kind?: string | null
  current_cycle_num?: number
}

export interface StrategyLevel {
  level_idx: number
  side: string
  target_price: number
  size_usdt: number
  status: 'pending' | 'placed' | 'filled' | 'cancelled' | 'sl_closed'
  filled_price: number
  exchange_order_id: string
  slot?: number | null
  sl_order_id?: string
  sl_price?: number
  sl_replaced?: boolean
  force_virtual?: boolean
}

export interface StrategyEvent {
  message: string
  level: 'info' | 'warn' | 'error'
  created_at: string
}

export interface StrategyState {
  cycle_num: number
  start_price: number
  tp_order_id: string
  sl_order_id: string
  started_at: string
  levels: StrategyLevel[]
  volume_usdt: number
  avg_entry: number
  signal_state?: 'buy' | 'sell' | 'neutral' | ''
  signal_values?: Record<string, number>
  safe_zone?: { low: number; high: number } | null
  tp_halted?: boolean
  /** Non-empty when order placement is suppressed: Bybit instrument status (e.g. "Closed")
   *  or "circuit_breaker" if the TP cancel streak reached the limit. */
  trading_halt_reason?: string
}

export interface HedgeSession {
  id:                   string
  bot_id:               string
  main_strategy_id:     string | null
  hedge_strategy_id:    string
  main_entry_at_start:  number | null
  hedge_entry_at_start: number | null
  gap_at_start:         number | null
  started_at:           string
  ended_at:             string | null
  cumulative_hedge_pnl: number
}

export interface StrategyTemplate {
  id: string
  name: string
  config: Partial<StrategyFormData>
  created_at: string
}

export interface MatrixLevel {
  direction: 'above' | 'below'
  price_step_pct: number
  size_pct: number
  stop_pct: number | null
  stop_cond_pct: number | null
  stop_replace_pct: number | null
  tp_pct: number | null
  order_type?: 'exchange' | 'virtual'
  use_signal?: boolean
}

export interface MatrixEntryLevel {
  price_step_pct?: number | null
  size_pct: number
  stop_pct: number | null
  stop_cond_pct: number | null
  stop_replace_pct: number | null
  tp_pct: number | null
  order_type?: 'exchange' | 'virtual'
  use_signal?: boolean
}

export interface StrategyFormData {
  account_id: string
  symbol: string
  category: string
  direction: 'long' | 'short'
  strategy_type: 'grid' | 'matrix' | 'manual'
  robot_enabled: boolean
  virtual_orders: boolean
  entry_order_type: 'limit' | 'stop_market'
  leverage: number
  grid_size_usdt: number
  margin_type: 'isolated' | 'cross'
  hedge_mode: boolean
  steps: GridStep[]
  grid_active: number
  max_stop_active: number
  signal_configs: SignalConfig[]
  tp_pct: number | null
  tp_mode: 'total' | 'per_level'
  sl_pct: number | null
  sl_type: 'conditional' | 'programmatic'
  trailing_stop_enabled: boolean
  trailing_activation_pct: number
  trailing_callback_pct: number
  tp_signal_configs?: SignalConfig[] | null
  tp_signal_dir?: 'buy' | 'sell' | null
  sl_signal_configs?: SignalConfig[] | null
  sl_signal_dir?: 'buy' | 'sell' | null
  matrix_levels: MatrixLevel[]
  safe_zone_pct: number
  protected_build: boolean
  matrix_rebuild_on_sl: boolean
  matrix_rebuild_from_entry: boolean
  size_as_main: boolean
  matrix_entry_level: MatrixEntryLevel
  adopt_position_data?: { size: string; entry_price: string } | null
}

// Cycle Audit
export interface CycleAuditLevel {
  idx: number
  slot?: number | null
  side: string
  target_price: number
  size_usdt: number
  qty: string
  db_status: string
  filled_price: number
  exchange_order_id: string
  sl_order_id?: string
  sl_price?: number
  sl_replaced?: boolean
  live_on_exchange: boolean
  sl_live_on_exchange?: boolean
  in_order_index: boolean
  sl_in_order_index?: boolean
  flag: 'ok' | 'pending' | 'missing_fill' | 'cancelled' | 'orphan'
}

export interface CycleAuditTPSL {
  order_id: string
  live_on_exchange: boolean
  in_order_index: boolean
  flag: 'ok' | 'not_placed' | 'missing' | 'orphan'
}

export interface CycleAuditData {
  no_active_cycle?: boolean
  strategy_type?: string
  cycle_num?: number
  started_at?: string
  position?: { size: string; avg_entry: string; side: string; unrealised_pnl: string } | null
  expected_qty_from_fills?: string
  qty_discrepancy?: string
  levels?: CycleAuditLevel[]
  tp?: CycleAuditTPSL | null
  sl?: CycleAuditTPSL | null
}

export interface SystemHealthSnapshot {
  cpu_pct: number
  ram_used_mb: number
  ram_total_mb: number
  ram_pct: number
  disk_used_gb: number
  disk_total_gb: number
  disk_pct: number
  db_ok: boolean
  db_size_mb: number
  db_growth_mb_per_day: number
}
