// frontend/src/features/log-visualizer/api.ts

import { apiClient } from '../../api/client'
import type { LVAccount, LVStrategy, LVEvent, LVLevel, LVCandle, Interval } from './types'

export async function lvGetAccounts(): Promise<LVAccount[]> {
  const res = await apiClient.get<LVAccount[]>('/admin/log-visualizer/accounts')
  return res.data
}

export async function lvGetStrategies(accountId: string): Promise<LVStrategy[]> {
  const res = await apiClient.get<LVStrategy[]>('/admin/log-visualizer/strategies', {
    params: { account_id: accountId },
  })
  return res.data
}

export async function lvGetEvents(
  strategyId: string, fromMs: number, toMs: number,
): Promise<LVEvent[]> {
  const res = await apiClient.get<LVEvent[]>('/admin/log-visualizer/events', {
    params: { strategy_id: strategyId, from: fromMs, to: toMs },
  })
  return res.data
}

export async function lvGetLevels(
  strategyId: string, fromMs: number, toMs: number,
): Promise<LVLevel[]> {
  const res = await apiClient.get<LVLevel[]>('/admin/log-visualizer/levels', {
    params: { strategy_id: strategyId, from: fromMs, to: toMs },
  })
  return res.data
}

export async function lvGetKlines(
  symbol: string, interval: Interval, fromMs: number, toMs: number,
): Promise<LVCandle[]> {
  const res = await apiClient.get<LVCandle[]>('/admin/log-visualizer/klines', {
    params: { symbol, interval, from: fromMs, to: toMs },
  })
  return res.data
}
