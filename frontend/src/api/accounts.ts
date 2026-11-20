import { apiClient } from './client'
import type { ExchangeAccount, Position } from '../types'

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
  proxy_host?: string   // прокси, через который прошёл последний запрос к бирже
}

export async function verifyAccount(id: string): Promise<VerifyResult> {
  const res = await apiClient.get<VerifyResult>(`/accounts/${id}/verify`)
  return res.data
}

export interface BalanceResult {
  ok: boolean
  message?: string
  /** "server_unavailable": our proxy layer cannot reach the exchange — show that, not zeros */
  code?: string
  equity?: number
  available?: number
  equity_24h_ago?: number
  equity_change_usd?: number
  equity_change_percent?: number
}

/** True when the balance could not be read because the server side (proxies) is down. */
export function isServerUnavailable(res: { ok: boolean; code?: string } | null | undefined): boolean {
  return !!res && !res.ok && res.code === 'server_unavailable'
}

export async function getAccountBalance(id: string): Promise<BalanceResult> {
  const res = await apiClient.get<BalanceResult>(`/accounts/${id}/balance`)
  return res.data
}

export async function getAccountPositions(id: string): Promise<{ ok: boolean; message?: string; positions?: Position[] }> {
  const res = await apiClient.get<{ ok: boolean; message?: string; positions?: Position[] }>(`/accounts/${id}/positions`)
  return res.data
}

export async function toggleAccountActive(id: string): Promise<ExchangeAccount> {
  const res = await apiClient.patch<ExchangeAccount>(`/accounts/${id}/active`)
  return res.data
}

export interface RiskSettingsInput {
  margin_warn_pct: number
  margin_pause_pct: number
  max_symbol_notional_pct: number
  risk_guard_enabled: boolean
}

export async function patchAccountRiskSettings(id: string, data: RiskSettingsInput): Promise<void> {
  await apiClient.patch(`/accounts/${id}/risk-settings`, data)
}

// Non-destructive: sets a "hide everything before now" marker for the Dashboard
// (exchange_accounts.stats_cleared_at). trade_history rows themselves are never touched.
export async function clearAccountStats(id: string): Promise<void> {
  await apiClient.patch(`/accounts/${id}/clear-stats`)
}
