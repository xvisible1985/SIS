// frontend/src/api/payments.ts
import { apiClient } from './client'

export interface TronDeposit {
  id: string
  amount_usdt: number
  amount_exact: number
  address: string
  status: 'pending' | 'confirmed' | 'expired'
  expires_at: string
  confirmed_at?: string
  tx_hash?: string
}

export async function createTronDeposit(amount: number): Promise<TronDeposit> {
  const res = await apiClient.post<TronDeposit>('/payments/tron/deposit', { amount })
  return res.data
}

export async function getTronDeposit(id: string): Promise<TronDeposit> {
  const res = await apiClient.get<TronDeposit>(`/payments/tron/deposit/${id}`)
  return res.data
}

export async function listTronDeposits(): Promise<TronDeposit[]> {
  const res = await apiClient.get<TronDeposit[]>('/payments/tron/deposits')
  return res.data
}
