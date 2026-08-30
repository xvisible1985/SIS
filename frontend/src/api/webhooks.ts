import { apiClient } from './client'
import type { Webhook, WebhookLog } from '../types'

export async function listWebhooks(): Promise<Webhook[]> {
  const res = await apiClient.get<Webhook[]>('/webhooks')
  return res.data
}

export async function createWebhook(data: {
  catalog_signal_id?: string
  custom_signal_id?: string
  symbol: string
  timeframe: string
  params?: Record<string, unknown>
  platform: string
}): Promise<Webhook> {
  const res = await apiClient.post<Webhook>('/webhooks', data)
  return res.data
}

export async function updateWebhook(
  id: string,
  data: Partial<Pick<Webhook, 'platform' | 'is_active'>>
): Promise<Webhook> {
  const res = await apiClient.put<Webhook>(`/webhooks/${id}`, data)
  return res.data
}

export async function deleteWebhook(id: string): Promise<void> {
  await apiClient.delete(`/webhooks/${id}`)
}

export async function listWebhookLogs(id: string): Promise<WebhookLog[]> {
  const res = await apiClient.get<WebhookLog[]>(`/webhooks/${id}/logs`)
  return res.data
}
