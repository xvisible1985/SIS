import { apiClient } from '../../api/client'
import type { AllStrategyDefaults, GridDefaults, MatrixDefaults, HedgeBotDefaults, MatrixBotDefaults, CoinFilterSettings } from './types'

let _cache: AllStrategyDefaults | null = null

export async function getStrategyDefaults(): Promise<AllStrategyDefaults> {
  if (_cache) return _cache
  const res = await apiClient.get<AllStrategyDefaults>('/strategy-defaults')
  _cache = res.data ?? {}
  return _cache
}

export function invalidateStrategyDefaultsCache(): void {
  _cache = null
}

export async function updateStrategyDefaults(
  type: 'grid' | 'matrix' | 'bot_hedge' | 'bot_matrix',
  config: GridDefaults | MatrixDefaults | HedgeBotDefaults | MatrixBotDefaults
): Promise<void> {
  await apiClient.put(`/admin/strategy-defaults/${type}`, config)
  invalidateStrategyDefaultsCache()
}

let _coinFilterCache: CoinFilterSettings | null = null

export async function getCoinFilter(): Promise<CoinFilterSettings> {
  if (_coinFilterCache) return _coinFilterCache
  const res = await apiClient.get<CoinFilterSettings>('/coin-filter')
  const d = res.data ?? { min_turnover_usdt: 500000, blacklist: [] }
  _coinFilterCache = { ...d, blacklist: d.blacklist ?? [], min_publish_days: d.min_publish_days ?? 15 }
  return _coinFilterCache
}

export function invalidateCoinFilterCache(): void {
  _coinFilterCache = null
}

export async function updateCoinFilter(settings: CoinFilterSettings): Promise<void> {
  await apiClient.put('/admin/coin-filter', settings)
  invalidateCoinFilterCache()
}
