// frontend/src/features/whale-parser/api.ts

import { apiClient } from '../../api/client'
import type { WhaleAddress, WhaleEvent, WhaleStateRow, SimulateResult } from './types'

export async function listWhaleAddresses(): Promise<WhaleAddress[]> {
  const res = await apiClient.get<WhaleAddress[]>('/admin/whale/addresses')
  return res.data
}

export async function createWhaleAddress(data: {
  address: string
  chain: string
  label?: string
}): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/admin/whale/addresses', data)
  return res.data
}

export async function patchWhaleAddress(id: string, data: {
  label?: string
  isActive?: boolean
}): Promise<void> {
  await apiClient.patch(`/admin/whale/addresses/${id}`, data)
}

export async function deleteWhaleAddress(id: string): Promise<void> {
  await apiClient.delete(`/admin/whale/addresses/${id}`)
}

export async function listWhaleEvents(limit = 50): Promise<WhaleEvent[]> {
  const res = await apiClient.get<WhaleEvent[]>(`/admin/whale/events?limit=${limit}`)
  return res.data
}

export async function getWhaleState(): Promise<WhaleStateRow[]> {
  const res = await apiClient.get<WhaleStateRow[]>('/admin/whale/state')
  return res.data
}

export async function simulateWhale(params: {
  threshold_usdt: number
  window_hours: number
}): Promise<SimulateResult> {
  const res = await apiClient.post<SimulateResult>('/admin/whale/simulate', params)
  return res.data
}

export async function getWhaleExchangeSymbols(): Promise<Record<string, string[]>> {
  const res = await apiClient.get<Record<string, string[]>>('/admin/whale/exchange-symbols')
  return res.data
}
