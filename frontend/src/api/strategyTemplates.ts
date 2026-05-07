import { apiClient } from './client'
import type { StrategyTemplate, StrategyFormData } from '../types'

export async function listTemplates(): Promise<StrategyTemplate[]> {
  const res = await apiClient.get<StrategyTemplate[]>('/strategy-templates')
  return res.data
}

export async function createTemplate(name: string, config: StrategyFormData): Promise<{ id: string }> {
  const res = await apiClient.post<{ id: string }>('/strategy-templates', { name, config })
  return res.data
}

export async function deleteTemplate(id: string): Promise<void> {
  await apiClient.delete(`/strategy-templates/${id}`)
}
