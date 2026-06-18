import { useState, useEffect, useRef } from 'react'

export type DebugEvent = {
  source: 'strategy' | 'bot'
  source_id: string
  symbol: string
  direction: string
  bot_name: string
  category: string
  message: string
  level: 'info' | 'warn' | 'error'
  created_at: string
}

const MAX_EVENTS = 500

export function useDebugEventsWs(open: boolean) {
  const [events, setEvents] = useState<DebugEvent[]>([])
  const sinceRef = useRef('')

  useEffect(() => {
    if (!open) return
    sinceRef.current = ''

    let destroyed = false
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null

    function connect() {
      if (destroyed) return
      const token = localStorage.getItem('token') ?? ''
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const since = sinceRef.current ? `&since=${encodeURIComponent(sinceRef.current)}` : ''
      ws = new WebSocket(`${proto}//${window.location.host}/ws/debug-events?token=${encodeURIComponent(token)}${since}`)

      ws.onmessage = (evt) => {
        try {
          const batch: DebugEvent[] = JSON.parse(evt.data as string)
          if (batch.length > 0) {
            sinceRef.current = batch[batch.length - 1].created_at
            setEvents(prev => {
              const next = [...prev, ...batch]
              return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
            })
          }
        } catch { /* ignore */ }
      }

      ws.onclose = () => {
        if (!destroyed) reconnectTimer = setTimeout(connect, 5000)
      }
      ws.onerror = () => ws?.close()
    }

    connect()

    return () => {
      destroyed = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (ws) { ws.onclose = null; ws.close() }
    }
  }, [open])

  function clear() {
    setEvents([])
    sinceRef.current = ''
  }

  return { events, clear }
}
