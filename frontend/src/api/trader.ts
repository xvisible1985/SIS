import { apiClient } from './client'
import type { TraderOrder, TraderExecution, TraderStats, PaginatedResponse } from '../types'

export interface PlaceOrderParams {
  account_id: string
  symbol: string
  category: string
  side: 'Buy' | 'Sell'
  order_type: 'Limit' | 'Market' | 'Conditional'
  qty: string
  price?: string
  trigger_price?: string
  trigger_by?: string
  trigger_direction?: 1 | 2
  time_in_force?: string
  order_filter?: string
  reduce_only?: boolean
  position_idx?: number
}

export async function placeOrder(params: PlaceOrderParams): Promise<{ ok: boolean; message?: string; order_id?: string; order_link_id?: string }> {
  const res = await apiClient.post('/trader/order', params)
  return res.data
}

export async function cancelOrder(params: {
  account_id: string
  symbol: string
  category: string
  order_id: string
  order_filter?: string
}): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.delete('/trader/order', { data: params })
  return res.data
}

export async function setLeverage(params: {
  account_id: string
  symbol: string
  category: string
  leverage: string
}): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.post('/trader/leverage', params)
  return res.data
}

export async function listOrders(params: {
  account_id?: string
  status?: 'open' | 'closed' | 'all'
  symbol?: string
  page?: number
  limit?: number
}): Promise<PaginatedResponse<TraderOrder> & { orders: TraderOrder[] }> {
  const res = await apiClient.get('/trader/orders', { params })
  return res.data
}

export async function listExecutions(params: {
  account_id?: string
  type?: string
  symbol?: string
  from?: string
  to?: string
  page?: number
  limit?: number
}): Promise<PaginatedResponse<TraderExecution> & { executions: TraderExecution[] }> {
  const res = await apiClient.get('/trader/executions', { params })
  return res.data
}

export async function getStats(params: {
  account_id?: string
  from?: string
  to?: string
}): Promise<TraderStats> {
  const res = await apiClient.get<TraderStats>('/trader/stats', { params })
  return res.data
}
