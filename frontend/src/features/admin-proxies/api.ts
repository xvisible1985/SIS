import { useState, useEffect, useCallback } from 'react'
import { apiClient } from '../../api/client'
import type { Proxy, ProxyMetrics, CreateProxyBody } from './types'

export function useProxies() {
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [metrics, setMetrics] = useState<ProxyMetrics[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchAll = useCallback(async () => {
    try {
      const [proxiesRes, metricsRes] = await Promise.all([
        apiClient.get<Proxy[]>('/admin/proxies'),
        apiClient.get<ProxyMetrics[]>('/admin/proxy-metrics'),
      ])
      setProxies(proxiesRes.data)
      setMetrics(metricsRes.data)
      setError(null)
    } catch (e: any) {
      setError(e?.response?.data?.error || 'Ошибка загрузки прокси')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchAll()
    const iv = setInterval(fetchAll, 5000)
    return () => clearInterval(iv)
  }, [fetchAll])

  return { proxies, metrics, loading, error, refresh: fetchAll }
}

export async function createProxy(body: CreateProxyBody) {
  const res = await apiClient.post<{ id: number }>('/admin/proxies', body)
  return res.data
}

export async function updateProxy(id: number, body: Partial<CreateProxyBody & { is_active: boolean }>) {
  await apiClient.patch(`/admin/proxies/${id}`, body)
}

export async function deleteProxy(id: number) {
  await apiClient.delete(`/admin/proxies/${id}`)
}
