import type { MatrixLevel, MatrixEntryLevel } from '../bots/types'
export type { MatrixLevel, MatrixEntryLevel }

export interface GridStep {
  price_move_pct: number
  size_pct: number
}

export interface GridDefaults {
  leverage?: number
  grid_size_usdt?: number
  direction?: 'long' | 'short' | 'both'
  entry_order_type?: 'limit' | 'stop_market'
  margin_type?: 'isolated' | 'cross'
  hedge_mode?: boolean
  grid_active?: number
  max_stop_active?: number
  after_stop_mode?: 'restart' | 'delete'
  max_cycles?: number
  tp_pct?: number
  sl_pct?: number
  sl_type?: 'conditional' | 'programmatic'
  trailing_activation_pct?: number
  trailing_callback_pct?: number
  steps?: GridStep[]
}

export interface MatrixDefaults {
  leverage?: number
  grid_size_usdt?: number
  direction?: 'long' | 'short' | 'both'
  entry_order_type?: 'limit' | 'stop_market'
  margin_type?: 'isolated' | 'cross'
  hedge_mode?: boolean
  grid_active?: number
  max_stop_active?: number
  after_stop_mode?: 'restart' | 'delete'
  max_cycles?: number
  safe_zone_pct?: number
  protected_build?: boolean
  matrix_rebuild_on_sl?: boolean
  matrix_rebuild_from_entry?: boolean
  matrix_levels?: MatrixLevel[]
  matrix_entry_level?: MatrixEntryLevel
}

export interface HedgeBotDefaults {
  leverage?: number
  grid_size_usdt?: number
  entry_order_type?: 'limit' | 'stop_market'
  margin_type?: 'isolated' | 'cross'
  grid_active?: number
  max_stop_active?: number
  after_stop_mode?: 'restart' | 'delete'
  max_cycles?: number
  safe_zone_pct?: number
  protected_build?: boolean
  matrix_rebuild_on_sl?: boolean
  matrix_rebuild_from_entry?: boolean
  matrix_levels?: MatrixLevel[]
  matrix_entry_level?: MatrixEntryLevel
  // Hedge activation/deactivation
  hedge_act_type?: number
  hedge_act_value?: number
  hedge_force_activation?: boolean
  hedge_close_type?: number
  hedge_close_value?: number
  hedge_deact_close_type?: number
  hedge_deact_close_value?: number
  hedge_profit_lazy?: boolean
  hedge_profit_lazy_pct?: number
  hedge_deact_type?: number
  hedge_deact_value?: number
}

export interface MatrixBotDefaults {
  leverage?: number
  grid_size_usdt?: number
  entry_order_type?: 'limit' | 'stop_market'
  margin_type?: 'isolated' | 'cross'
  hedge_mode?: boolean
  grid_active?: number
  max_stop_active?: number
  after_stop_mode?: 'restart' | 'delete'
  max_cycles?: number
  safe_zone_pct?: number
  protected_build?: boolean
  matrix_rebuild_on_sl?: boolean
  matrix_rebuild_from_entry?: boolean
  matrix_levels?: MatrixLevel[]
  matrix_entry_level?: MatrixEntryLevel
  hedge_deact_close_type?: number
  hedge_deact_close_value?: number
  hedge_profit_lazy?: boolean
  hedge_profit_lazy_pct?: number
}

export interface AllStrategyDefaults {
  grid?: GridDefaults
  matrix?: MatrixDefaults
  bot_hedge?: HedgeBotDefaults
  bot_matrix?: MatrixBotDefaults
}

export interface CoinFilterSettings {
  min_turnover_usdt: number
  blacklist: string[]
  min_publish_days: number
}
