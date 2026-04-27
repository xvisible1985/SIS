import { apiClient } from './client'
import type { ExchangeAccount } from '../types'

export async function listAccounts(): Promise<ExchangeAccount[]> {
  const res = await apiClient.get<ExchangeAccount[]>('/accounts')
  return res.data
}

export async function createAccount(data: {
  exchange: string
  label: string
  api_key: string
  secret: string
}): Promise<ExchangeAccount> {
  const res = await apiClient.post<ExchangeAccount>('/accounts', data)
  return res.data
}

export async function deleteAccount(id: string): Promise<void> {
  await apiClient.delete(`/accounts/${id}`)
}

export async function verifyAccount(id: string): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.get<{ ok: boolean; message?: string }>(`/accounts/${id}/verify`)
  return res.data
}
