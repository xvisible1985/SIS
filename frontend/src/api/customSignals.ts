import { apiClient } from './client'
import type { CustomSignal } from '../types'

export interface ComboComponentInput {
  signal_id: string
  params: Record<string, unknown>
}

export async function listCustomSignals(): Promise<CustomSignal[]> {
  const res = await apiClient.get<CustomSignal[]>('/custom-signals')
  return res.data
}

export async function createCustomSignal(name: string, components: ComboComponentInput[]): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/custom-signals', { name, components })
  return res.data
}

export async function deleteCustomSignal(id: string): Promise<void> {
  await apiClient.delete(`/custom-signals/${id}`)
}

export interface ComboPreviewEvent {
  time: number
  state: 'buy' | 'sell' | 'neutral'
  price: number
}

export async function comboPreview(
  symbol: string, interval: string, components: ComboComponentInput[], limit = 500
): Promise<ComboPreviewEvent[]> {
  const res = await apiClient.post<{ events: ComboPreviewEvent[] }>('/signals/combo-preview', {
    symbol, interval, limit, components,
  })
  return res.data.events ?? []
}
