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

export interface VerifyResult {
  ok: boolean
  message?: string
  read_only?: boolean
  permissions?: Record<string, string[]>
  ips?: string[]
  expires_at?: number
}

export async function verifyAccount(id: string): Promise<VerifyResult> {
  const res = await apiClient.get<VerifyResult>(`/accounts/${id}/verify`)
  return res.data
}

export interface BalanceResult {
  ok: boolean
  message?: string
  equity?: number
  available?: number
}

export async function getAccountBalance(id: string): Promise<BalanceResult> {
  const res = await apiClient.get<BalanceResult>(`/accounts/${id}/balance`)
  return res.data
}

export async function toggleAccountActive(id: string): Promise<ExchangeAccount> {
  const res = await apiClient.patch<ExchangeAccount>(`/accounts/${id}/active`)
  return res.data
}
