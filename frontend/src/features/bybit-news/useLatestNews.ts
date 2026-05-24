import { useState, useEffect, useCallback } from 'react'
import { apiClient } from '../../api/client'
import type { BybitAnnouncement } from './types'

export function useLatestBybitNews() {
  const [items, setItems] = useState<BybitAnnouncement[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const fetch_ = useCallback(async () => {
    try {
      const res = await apiClient.get<BybitAnnouncement[]>('/admin/bybit-news/latest')
      setItems(res.data)
      setError(null)
    } catch (e: any) {
      setError(e?.response?.data?.error || 'Ошибка')
    } finally {
      setLoading(false)
    }
  }, [])

  const forceRefresh = useCallback(async () => {
    setRefreshing(true)
    try {
      await apiClient.post('/admin/bybit-news/refresh')
      // wait a bit for fetch to complete then reload
      await new Promise(r => setTimeout(r, 3000))
      await fetch_()
    } catch (e: any) {
      setError(e?.response?.data?.error || 'Ошибка обновления')
    } finally {
      setRefreshing(false)
    }
  }, [fetch_])

  useEffect(() => {
    fetch_()
    const iv = setInterval(fetch_, 60000) // refresh every minute
    return () => clearInterval(iv)
  }, [fetch_])

  return { items, loading, refreshing, error, refresh: fetch_, forceRefresh }
}
