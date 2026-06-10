import { useEffect, useRef, useState } from 'react'
import { apiClient } from '../api/client'
import type { SystemHealthSnapshot } from '../types'

/** Polls GET /admin/system-health every 10 s. Returns null until first successful response. */
export function useSystemHealth(): SystemHealthSnapshot | null {
  const [data, setData] = useState<SystemHealthSnapshot | null>(null)
  const cancelled = useRef(false)

  useEffect(() => {
    cancelled.current = false

    async function fetch_() {
      try {
        const res = await apiClient.get<SystemHealthSnapshot>('/admin/system-health')
        if (!cancelled.current) setData(res.data)
      } catch {
        // Silently ignore — chips remain hidden on error
      }
    }

    fetch_()
    const iv = setInterval(fetch_, 10_000)
    return () => {
      cancelled.current = true
      clearInterval(iv)
    }
  }, [])

  return data
}
