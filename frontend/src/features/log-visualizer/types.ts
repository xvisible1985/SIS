// frontend/src/features/log-visualizer/types.ts

export interface LVAccount {
  id: string
  label: string
  ownerUsername: string
}

export interface LVStrategy {
  id: string
  symbol: string
  direction: string   // 'long' | 'short' | 'both'
  strategyType: string // 'grid' | 'matrix'
  status: string
}

export interface LVEvent {
  message: string
  level: 'info' | 'warn' | 'error'
  tsMs: number
}

export interface LVLevel {
  levelIdx: number
  side: 'Buy' | 'Sell'
  filledPrice: number
  qty: string
  status: 'filled' | 'sl_closed'
  tsMs: number
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
  label:  string   // display text, e.g. "L3 filled 0.4225" or "TP placed"
}

export const INTERVALS = ['1m', '5m', '15m', '30m', '1h', '4h', '1D'] as const
export type Interval = typeof INTERVALS[number]
