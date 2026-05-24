import { useState, useEffect, useCallback } from 'react'
import { apiClient } from '../../api/client'
import type { BybitAnnouncement } from './types'

export function useBybitNews(listingsOnly = false, delistingsOnly = false) {
  const [items, setItems] = useState<BybitAnnouncement[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetch_ = useCallback(async () => {
    try {
      const params = new URLSearchParams()
      if (listingsOnly) params.set('listings', '1')
      if (delistingsOnly) params.set('delistings', '1')
      const res = await apiClient.get<BybitAnnouncement[]>(`/admin/bybit-news?${params.toString()}`)
      setItems(res.data)
      setError(null)
    } catch (e: any) {
      setError(e?.response?.data?.error || 'Ошибка загрузки')
    } finally {
      setLoading(false)
    }
  }, [listingsOnly, delistingsOnly])

  useEffect(() => {
    fetch_()
    const iv = setInterval(fetch_, 30000) // refresh every 30s
    return () => clearInterval(iv)
  }, [fetch_])

  return { items, loading, error, refresh: fetch_ }
}
