import { apiClient } from './client'
import type { Strategy, StrategyState, StrategyEvent, StrategyFormData, CycleAuditData } from '../types'

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

export async function getStrategyEvents(
  id: string,
  params?: { level?: string; date?: string; limit?: number; offset?: number }
): Promise<{ total: number; events: StrategyEvent[] }> {
  const res = await apiClient.get<{ total: number; events: StrategyEvent[] } | StrategyEvent[]>(`/strategies/${id}/events`, { params })
  const data = res.data
  if (Array.isArray(data)) {
    return { total: data.length, events: data }
  }
  return { total: data.total ?? data.events?.length ?? 0, events: data.events ?? [] }
}

export async function getCycleAudit(id: string): Promise<CycleAuditData> {
  const res = await apiClient.get<CycleAuditData>(`/strategies/${id}/cycle-audit`)
  return res.data
}

export async function restartCycle(id: string): Promise<void> {
  await apiClient.post(`/strategies/${id}/cycle-restart`)
}

export async function dismissManualAlert(id: string): Promise<void> {
  await apiClient.post(`/strategies/${id}/dismiss-alert`)
}

export async function detachFromBot(id: string): Promise<void> {
  await apiClient.post(`/strategies/${id}/detach`)
}

export interface InstrumentConstraints {
  tick_size: number
  qty_step: number
  min_qty: number
  max_leverage: number
  min_notional_value: number
  min_order_usdt: number
}

const instrConstraintsCache = new Map<string, { data: InstrumentConstraints; ts: number }>()

export async function getInstrumentConstraints(symbol: string, category = 'linear'): Promise<InstrumentConstraints> {
  const key = `${category}:${symbol}`
  const cached = instrConstraintsCache.get(key)
  if (cached && Date.now() - cached.ts < 5 * 60_000) return cached.data
  const res = await apiClient.get<InstrumentConstraints>(`/instrument-info?symbol=${symbol}&category=${category}`)
  instrConstraintsCache.set(key, { data: res.data, ts: Date.now() })
  return res.data
}
