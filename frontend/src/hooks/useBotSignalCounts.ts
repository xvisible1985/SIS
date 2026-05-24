import { useEffect, useRef, useCallback, useState } from 'react'

export type BotSignalCount = {
  signalCount: number
  totalCount: number
}

const MAX_RECONNECT_ATTEMPTS = 10
const BASE_RECONNECT_DELAY = 2000
const MAX_RECONNECT_DELAY = 30000

export function useBotSignalCounts(enabled: boolean): Map<string, BotSignalCount> {
  const [counts, setCounts] = useState<Map<string, BotSignalCount>>(new Map())
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const attemptsRef = useRef(0)
  const mountedRef = useRef(true)

  const connect = useCallback(() => {
    if (!mountedRef.current || !enabled) return
    if (wsRef.current) {
      const old = wsRef.current
      old.onopen = null; old.onmessage = null; old.onerror = null; old.onclose = null
      try { old.close() } catch { /* ignore */ }
      wsRef.current = null
    }

    if (attemptsRef.current >= MAX_RECONNECT_ATTEMPTS) return

    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${window.location.host}/ws/bots/updates?token=${encodeURIComponent(token)}`

    let ws: WebSocket
    try {
      ws = new WebSocket(url)
    } catch {
      scheduleReconnect()
      return
    }
    wsRef.current = ws

    ws.onopen = () => {
      attemptsRef.current = 0
    }

    ws.onmessage = (evt) => {
      try {
        const updates: { id: string; signalCount: number; totalCount: number }[] =
          JSON.parse(evt.data as string)
        if (!Array.isArray(updates)) return
        setCounts(prev => {
          const next = new Map(prev)
          for (const u of updates) {
            next.set(u.id, { signalCount: u.signalCount, totalCount: u.totalCount })
          }
          return next
        })
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      scheduleReconnect()
    }

    ws.onerror = () => {
      try { ws.close() } catch { /* ignore */ }
    }

    function scheduleReconnect() {
      if (!mountedRef.current) return
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      attemptsRef.current += 1
      const delay = Math.min(
        BASE_RECONNECT_DELAY * Math.pow(2, attemptsRef.current - 1),
        MAX_RECONNECT_DELAY,
      )
      reconnectRef.current = setTimeout(connect, delay)
    }
  }, [enabled])

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

  return counts
}
