import { apiClient } from './client'

export interface PresetBot {
  entry_id: string
  bot_id: string
  role: 'trading' | 'hedging'
  sort_order: number
  name: string
  description: string
  avatar_url: string
}

export interface BotPreset {
  id: string
  name: string
  description: string
  aggressiveness: 'conservative' | 'moderate' | 'aggressive'
  coin_type: 'stable' | 'meme' | 'both'
  trading_style: 'diversified' | 'concentrated' | 'both'
  tags: string[]
  is_active: boolean
  sort_order: number
  bots: PresetBot[]
}

export type CreatePresetInput = Omit<BotPreset, 'id' | 'bots'>
export type PatchPresetInput = Partial<Omit<BotPreset, 'id' | 'bots'>>

export async function listAdminBotPresets(): Promise<BotPreset[]> {
  const res = await apiClient.get<BotPreset[]>('/admin/bot-presets')
  return res.data
}

export async function createBotPreset(data: CreatePresetInput): Promise<BotPreset> {
  const res = await apiClient.post<BotPreset>('/admin/bot-presets', data)
  return res.data
}

export async function patchBotPreset(id: string, data: PatchPresetInput): Promise<BotPreset> {
  const res = await apiClient.patch<BotPreset>(`/admin/bot-presets/${id}`, data)
  return res.data
}

export async function deleteBotPreset(id: string): Promise<void> {
  await apiClient.delete(`/admin/bot-presets/${id}`)
}

export async function addBotToPreset(
  presetId: string,
  data: { bot_id: string; role: 'trading' | 'hedging'; sort_order?: number }
): Promise<PresetBot> {
  const res = await apiClient.post<PresetBot>(`/admin/bot-presets/${presetId}/bots`, data)
  return res.data
}

export async function removeBotFromPreset(presetId: string, entryId: string): Promise<void> {
  await apiClient.delete(`/admin/bot-presets/${presetId}/bots/${entryId}`)
}

export async function listBotPresets(params?: {
  aggressiveness?: string
  coin_type?: string
  trading_style?: string
  tags?: string
}): Promise<BotPreset[]> {
  const res = await apiClient.get<BotPreset[]>('/bot-presets', { params })
  return res.data
}
