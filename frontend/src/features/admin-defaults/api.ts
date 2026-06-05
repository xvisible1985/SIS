import { apiClient } from '../../api/client'
import type { AllStrategyDefaults, GridDefaults, MatrixDefaults, CoinFilterSettings } from './types'

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
  type: 'grid' | 'matrix',
  config: GridDefaults | MatrixDefaults
): Promise<void> {
  await apiClient.put(`/admin/strategy-defaults/${type}`, config)
  invalidateStrategyDefaultsCache()
}

let _coinFilterCache: CoinFilterSettings | null = null

export async function getCoinFilter(): Promise<CoinFilterSettings> {
  if (_coinFilterCache) return _coinFilterCache
  const res = await apiClient.get<CoinFilterSettings>('/coin-filter')
  _coinFilterCache = res.data ?? { min_turnover_usdt: 500000, blacklist: [] }
  return _coinFilterCache
}

export function invalidateCoinFilterCache(): void {
  _coinFilterCache = null
}

export async function updateCoinFilter(settings: CoinFilterSettings): Promise<void> {
  await apiClient.put('/admin/coin-filter', settings)
  invalidateCoinFilterCache()
}
