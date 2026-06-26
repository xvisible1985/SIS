// frontend/src/features/whale-parser/types.ts

export type WhaleChain = 'eth' | 'tron'
export type WhaleDirection = 'buy' | 'sell' | 'neutral'

export interface WhaleAddress {
  id: string
  address: string
  chain: WhaleChain
  label: string | null
  isManual: boolean
  volume30d: number
  isActive: boolean
  lastSeenAt: string | null
  createdAt: string
}

export interface WhaleEvent {
  id: string
  address: string
  label: string
  chain: WhaleChain
  symbol: string
  amountUsd: number
  direction: WhaleDirection
  score: number
  txHash: string
  detectedAt: string
}

export interface WhaleStateRow {
  symbol: string
  direction: WhaleDirection
  amountUsd: number
  detectedAt: string
  source: 'redis' | 'db'
}

export interface SimulateBucket {
  label: string
  count: number
}

export interface SimulateResult {
  events_count: number
  by_symbol: { symbol: string; direction: WhaleDirection }[]
  buckets: SimulateBucket[]
}
