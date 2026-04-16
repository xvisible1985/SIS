import { apiClient } from './client'
import type { Webhook } from '../types'

export async function listWebhooks(): Promise<Webhook[]> {
  const res = await apiClient.get<Webhook[]>('/webhooks')
  return res.data
}

export async function createWebhook(data: {
  signal_id: string
  url: string
  platform: string
}): Promise<Webhook> {
  const res = await apiClient.post<Webhook>('/webhooks', data)
  return res.data
}

export async function updateWebhook(
  id: string,
  data: Partial<Pick<Webhook, 'url' | 'platform' | 'is_active'>>
): Promise<Webhook> {
  const res = await apiClient.put<Webhook>(`/webhooks/${id}`, data)
  return res.data
}

export async function deleteWebhook(id: string): Promise<void> {
  await apiClient.delete(`/webhooks/${id}`)
}
