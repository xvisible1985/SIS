import { apiClient } from '../../api/client'
import type { AllStrategyDefaults, GridDefaults, MatrixDefaults } from './types'

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
