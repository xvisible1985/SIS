// frontend/src/features/log-visualizer/types.ts

export interface LVAccount {
  id: string
  label: string
  ownerUsername: string
}

export interface LVStrategy {
  id:           string
  symbol:       string
  direction:    string   // 'long' | 'short' | 'both'
  strategyType: string   // 'grid' | 'matrix'
  status:       string
  gridLevels:   number
  lastPnl:      number | null
}

export interface LVEvent {
  message: string
  level: 'info' | 'warn' | 'error'
  tsMs: number
}

export interface LVLevel {
  levelIdx:    number
  side:        'Buy' | 'Sell'
  filledPrice: number
  qty:         string    // base asset amount, e.g. "0.001234"
  sizeUsdt:    number    // position size in USDT
  status:      'filled' | 'sl_closed'
  tsMs:        number
}

export interface LVCandle {
  t: number  // unix ms
  o: number
  h: number
  l: number
  c: number
  v: number
}

/** Merged event from strategy_events or strategy_levels, sorted by time. */
export interface MergedEvent {
  tsMs:   number
  kind:   'log' | 'level'
  log?:   LVEvent
  level?: LVLevel
  label:  string   // display text, e.g. "▲ L3 0.4225 · 47 USDT" or "TP placed"
}

export interface LayerSettings {
  showOrderMarkers: boolean   // arrowUp/arrowDown markers for level events
  showLogMarkers:   boolean   // circle markers for log events
  showPriceLines:   boolean   // horizontal dashed price lines at each filled level
  // showInfo/showWarn/showError are only applied when showLogMarkers is true
  showInfo:         boolean   // show log events with level 'info'
  showWarn:         boolean   // show log events with level 'warn'
  showError:        boolean   // show log events with level 'error'
}

export const DEFAULT_LAYER_SETTINGS: LayerSettings = {
  showOrderMarkers: true,
  showLogMarkers:   true,
  showPriceLines:   false,
  showInfo:         true,
  showWarn:         true,
  showError:        true,
}

export const INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D'] as const
export type Interval = typeof INTERVALS[number]
