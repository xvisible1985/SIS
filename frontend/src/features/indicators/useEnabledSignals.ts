import { useEffect, useState } from 'react'
import { apiClient } from '../../api/client'

interface EnabledItem { id: string; panel: string }

// Admin sees the full signal-types list (with disabled ones included, status field
// present); everyone else falls back to the public /signal-types list, which already
// excludes disabled entries — hence the two-request fallback instead of one shared shape.
export function useEnabledSignals() {
  const [ids, setIds] = useState<Set<string> | null>(null)
  useEffect(() => {
    apiClient.get<{ id: string; status: string }[]>('/admin/signal-types')
      .then(res => setIds(new Set(res.data.filter(x => x.status !== 'disabled').map(x => x.id))))
      .catch(() =>
        apiClient.get<EnabledItem[]>('/signal-types')
          .then(res => setIds(new Set(res.data.map(x => x.id))))
          .catch(() => setIds(null))
      )
  }, [])
  return ids
}
