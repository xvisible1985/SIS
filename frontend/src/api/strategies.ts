import { apiClient } from './client'
import type { Strategy, StrategyState, StrategyFormData } from '../types'

export async function listStrategies(): Promise<Strategy[]> {
  const res = await apiClient.get<Strategy[]>('/strategies')
  return res.data
}

export async function createStrategy(data: StrategyFormData): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/strategies', data)
  return res.data
}

export async function updateStrategy(id: string, data: StrategyFormData): Promise<void> {
  await apiClient.put(`/strategies/${id}`, data)
}

export async function setStrategyStatus(
  id: string,
  status: 'active' | 'finishing' | 'stopped'
): Promise<void> {
  await apiClient.post(`/strategies/${id}/status`, { status })
}

export async function deleteStrategy(id: string): Promise<void> {
  await apiClient.delete(`/strategies/${id}`)
}

export async function getStrategyState(id: string): Promise<StrategyState> {
  const res = await apiClient.get<StrategyState>(`/strategies/${id}/state`)
  return res.data
}
