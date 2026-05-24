// frontend/src/api/account.ts
import { apiClient } from './client'

export interface AccountProfile {
  email: string
  username: string | null
  plan: string
  telegram_username: string | null
  novabot_balance: number
}

export interface NotificationSettings {
  on_trade: boolean
  on_signal: boolean
  on_balance: boolean
}

export interface ReferralSignup {
  date: string
  email_masked: string
  active: boolean
}

export interface ReferralInfo {
  code: string
  link: string
  count: number
  total_rewards: number
  signups: ReferralSignup[]
}

export async function getProfile(): Promise<AccountProfile> {
  const res = await apiClient.get<AccountProfile>('/account/profile')
  return res.data
}

export async function updateProfile(username: string): Promise<AccountProfile> {
  const res = await apiClient.patch<AccountProfile>('/account/profile', { username })
  return res.data
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  await apiClient.post('/account/change-password', {
    current_password: currentPassword,
    new_password: newPassword,
  })
}

export async function getTelegramLink(): Promise<{ url: string }> {
  const res = await apiClient.get<{ url: string }>('/account/telegram-link')
  return res.data
}

export async function disconnectTelegram(): Promise<void> {
  await apiClient.delete('/account/telegram')
}

export async function getNotifications(): Promise<NotificationSettings> {
  const res = await apiClient.get<NotificationSettings>('/account/notifications')
  return res.data
}

export async function updateNotifications(settings: Partial<NotificationSettings>): Promise<void> {
  await apiClient.patch('/account/notifications', settings)
}

export async function getReferral(): Promise<ReferralInfo> {
  const res = await apiClient.get<ReferralInfo>('/account/referral')
  return res.data
}
