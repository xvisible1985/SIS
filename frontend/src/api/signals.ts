import { apiClient } from './client'
import type {
  Signal,
  BacktestRequest,
  OptimizeRequest,
  BacktestResult,
  OptimizationResult,
} from '../types'

export async function listSignals(): Promise<Signal[]> {
  const res = await apiClient.get<Signal[]>('/signals')
  return res.data
}

export async function getSignal(id: string): Promise<Signal> {
  const res = await apiClient.get<Signal>(`/signals/${id}`)
  return res.data
}

export async function createSignal(
  data: Omit<Signal, 'id' | 'is_active' | 'created_at'>
): Promise<Signal> {
  const res = await apiClient.post<Signal>('/signals', data)
  return res.data
}

export async function updateSignal(
  id: string,
  data: Partial<Pick<Signal, 'name' | 'description' | 'direction' | 'conditions' | 'is_active'>>
): Promise<Signal> {
  const res = await apiClient.put<Signal>(`/signals/${id}`, data)
  return res.data
}

export async function deleteSignal(id: string): Promise<void> {
  await apiClient.delete(`/signals/${id}`)
}

export async function submitBacktest(
  signalId: string,
  req: BacktestRequest
): Promise<{ job_id: string }> {
  const res = await apiClient.post<{ job_id: string }>(
    `/signals/${signalId}/backtest`,
    req
  )
  return res.data
}

export async function submitOptimize(
  signalId: string,
  req: OptimizeRequest
): Promise<{ job_id: string }> {
  const res = await apiClient.post<{ job_id: string }>(
    `/signals/${signalId}/optimize`,
    req
  )
  return res.data
}

export async function getBacktestResults(signalId: string): Promise<BacktestResult[]> {
  const res = await apiClient.get<BacktestResult[]>(`/signals/${signalId}/backtest-results`)
  return res.data
}

export async function getOptimizationResults(signalId: string): Promise<OptimizationResult[]> {
  const res = await apiClient.get<OptimizationResult[]>(
    `/signals/${signalId}/optimization-results`
  )
  return res.data
}
