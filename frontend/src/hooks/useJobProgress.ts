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
