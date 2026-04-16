import { useEffect, useState } from 'react'
import type { ProgressMessage } from '../types'

export function useJobProgress(
  jobId: string | null,
  type: 'backtest' | 'optimize'
): ProgressMessage {
  const [progress, setProgress] = useState<ProgressMessage>({
    pct: 0,
    status: '',
    updated_at: 0,
  })

  useEffect(() => {
    if (!jobId) return

    const token = localStorage.getItem('token') ?? ''
    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    // NOTE: The token is passed as a query param because the backend (ws_handler.go) authenticates
    // via r.URL.Query().Get("token") before upgrading the connection. Browsers cannot set custom
    // headers on WebSocket connections, so this is a known limitation. Move to first-message auth
    // only once the backend supports reading the token from the first WebSocket message instead.
    const url = `${wsProtocol}//${window.location.host}/ws/jobs/${jobId}/progress?type=${type}&token=${encodeURIComponent(token)}`

    const ws = new WebSocket(url)

    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string) as ProgressMessage
        setProgress(msg)
      } catch {
        // ignore malformed frames
      }
    }

    ws.onerror = () => ws.close()

    return () => ws.close()
  }, [jobId, type])

  return progress
}
