import { apiClient } from './client'
import type { AuthResponse } from '../types'

export async function login(email: string, password: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/login', { email, password })
  return res.data
}

export async function register(email: string, password: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/register', { email, password })
  return res.data
}

export async function telegramCallback(token: string): Promise<AuthResponse> {
  const res = await apiClient.post<AuthResponse>('/auth/telegram-callback', { token })
  return res.data
}
