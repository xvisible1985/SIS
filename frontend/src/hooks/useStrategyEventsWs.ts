import { useEffect, useRef, useCallback } from 'react'
import type { StrategyEvent } from '../types'

export function useStrategyEventsWs(
  strategyId: string,
  enabled: boolean,
  since: string | null,
  onEvents: (events: StrategyEvent[]) => void,
) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Keep latest callback and since without recreating the WS
  const onEventsRef = useRef(onEvents)
  useEffect(() => { onEventsRef.current = onEvents })
  const sinceRef = useRef(since)
  useEffect(() => { sinceRef.current = since }, [since])

  const connect = useCallback(() => {
    if (!enabled || !strategyId) return
    if (wsRef.current) {
      const old = wsRef.current
      old.onopen = null; old.onmessage = null; old.onerror = null; old.onclose = null
      old.close()
      wsRef.current = null
    }

    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sinceParam = sinceRef.current ? `&since=${encodeURIComponent(sinceRef.current)}` : ''
    const url = `${proto}//${window.location.host}/ws/strategies/${strategyId}/events?token=${encodeURIComponent(token)}${sinceParam}`

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onmessage = (evt) => {
      try {
        const events: StrategyEvent[] = JSON.parse(evt.data as string)
        if (events.length > 0) {
          sinceRef.current = events[events.length - 1].created_at
          onEventsRef.current(events)
        }
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      reconnectRef.current = setTimeout(connect, 5000)
    }

    ws.onerror = () => { ws.close() }
  }, [enabled, strategyId]) // onEvents intentionally excluded — tracked via ref

  useEffect(() => {
    connect()
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      if (wsRef.current) {
        const ws = wsRef.current
        ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
        ws.close()
        wsRef.current = null
      }
    }
  }, [connect])
}
