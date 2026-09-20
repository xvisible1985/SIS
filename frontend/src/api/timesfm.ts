import { apiClient } from './client'

export type TimesfmPrediction = {
  id: string
  symbol: string
  timeframe: string
  predicted_at: string
  price_at_predict: number
  predicted_pct: number
  predicted_direction: 'buy' | 'sell' | 'neutral'
  target_at: string
  actual_price: number | null
  actual_direction: string | null
  correct: boolean | null
}

export type TimesfmPredictionsResponse = {
  predictions: TimesfmPrediction[]
  total: number
  checked: number
  correct: number
  win_rate: number
}

export async function listTimesfmPredictions(symbol: string): Promise<TimesfmPredictionsResponse> {
  const res = await apiClient.get<TimesfmPredictionsResponse>(
    `/signals/timesfm/predictions?symbol=${encodeURIComponent(symbol)}`
  )
  return res.data
}
