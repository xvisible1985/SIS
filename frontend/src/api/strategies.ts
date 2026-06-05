import { apiClient } from './client'
import type { Strategy, StrategyState, StrategyEvent, StrategyFormData, CycleAuditData, HedgeSession } from '../types'

export async function listStrategies(asAccountId?: string): Promise<Strategy[]> {
  const params = asAccountId ? { as_account_id: asAccountId } : undefined
  const res = await apiClient.get<Strategy[]>('/strategies', { params })
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

export interface DetachPositionData {
  size: string
  side: string          // "Buy" | "Sell" — exchange side of the hedge position
  entry_price: string   // required for action="adopt"
  position_idx: number  // Bybit positionIdx for reduce-only close
}

export async function detachWithAction(
  id: string,
  action: 'adopt' | 'close' | 'leave',
  opts: {
    addBlacklist?: boolean
    position?: DetachPositionData
  } = {}
): Promise<void> {
  await apiClient.post(`/strategies/${id}/detach`, {
    action,
    add_blacklist: opts.addBlacklist ?? false,
    position: opts.position,
  })
}

export async function addBotBlacklist(botId: string, symbol: string): Promise<void> {
  await apiClient.post(`/bots/${botId}/blacklist-add`, { symbol })
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

export async function getHedgeSession(strategyId: string): Promise<HedgeSession | null> {
  try {
    const res = await apiClient.get<HedgeSession>(`/strategies/${strategyId}/hedge-session`)
    return res.data
  } catch {
    return null
  }
}
