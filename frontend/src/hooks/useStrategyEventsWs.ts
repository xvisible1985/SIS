import { useEffect, useRef, useCallback } from 'react'
import type { StrategyEvent } from '../types'

const MAX_RECONNECT_ATTEMPTS = 10
const BASE_RECONNECT_DELAY = 2000
const MAX_RECONNECT_DELAY = 30000

export function useStrategyEventsWs(
  strategyId: string,
  enabled: boolean,
  since: string | null,
  onEvents: (events: StrategyEvent[]) => void,
) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const attemptsRef = useRef(0)
  const mountedRef = useRef(true)

  const onEventsRef = useRef(onEvents)
  useEffect(() => { onEventsRef.current = onEvents })
  const sinceRef = useRef(since)
  useEffect(() => { sinceRef.current = since }, [since])

  const connect = useCallback(() => {
    if (!mountedRef.current || !enabled || !strategyId) return
    if (wsRef.current) {
      const old = wsRef.current
      old.onopen = null; old.onmessage = null; old.onerror = null; old.onclose = null
      try { old.close() } catch { /* ignore */ }
      wsRef.current = null
    }

    if (attemptsRef.current >= MAX_RECONNECT_ATTEMPTS) {
      // Give up after too many attempts; next enabled change will retry
      return
    }

    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sinceParam = sinceRef.current ? `&since=${encodeURIComponent(sinceRef.current)}` : ''
    const url = `${proto}//${window.location.host}/ws/strategies/${strategyId}/events?token=${encodeURIComponent(token)}${sinceParam}`

    let ws: WebSocket
    try {
      ws = new WebSocket(url)
    } catch (err) {
      // WebSocket creation failed (e.g. invalid URL) — schedule reconnect
      scheduleReconnect()
      return
    }
    wsRef.current = ws

    ws.onopen = () => {
      attemptsRef.current = 0
    }

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
      scheduleReconnect()
    }

    ws.onerror = () => {
      // onclose will fire next and handle reconnect
      try { ws.close() } catch { /* ignore */ }
    }

    function scheduleReconnect() {
      if (!mountedRef.current) return
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      attemptsRef.current += 1
      const delay = Math.min(BASE_RECONNECT_DELAY * Math.pow(2, attemptsRef.current - 1), MAX_RECONNECT_DELAY)
      reconnectRef.current = setTimeout(connect, delay)
    }
  }, [enabled, strategyId])

  useEffect(() => {
    mountedRef.current = true
    attemptsRef.current = 0
    connect()
    return () => {
      mountedRef.current = false
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      if (wsRef.current) {
        const ws = wsRef.current
        ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
        try { ws.close() } catch { /* ignore */ }
        wsRef.current = null
      }
    }
  }, [connect])
}
