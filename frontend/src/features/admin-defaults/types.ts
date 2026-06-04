export interface GridStep {
  price_move_pct: number
  size_pct: number
}

export interface GridDefaults {
  leverage?: number
  grid_size_usdt?: number
  tp_pct?: number
  sl_pct?: number
  trailing_activation_pct?: number
  trailing_callback_pct?: number
  steps?: GridStep[]
}

export interface MatrixDefaults {
  leverage?: number
  grid_size_usdt?: number
  safe_zone_pct?: number
}

export interface AllStrategyDefaults {
  grid?: GridDefaults
  matrix?: MatrixDefaults
}
