import { useEffect, useRef, useCallback, useState } from 'react'

export type BotEventCategory = 'tick' | 'strategy' | 'trade' | 'user' | 'system'

export type BotEvent = {
  message: string
  level: 'info' | 'warn' | 'error'
  category: BotEventCategory
  created_at: string
}

const MAX_EVENTS = 200

export function useBotEventsWs(
  botId: string | null,
  enabled: boolean,
  category?: BotEventCategory | null,
  onEvents?: (events: BotEvent[]) => void,
): BotEvent[] {
  const [events, setEvents] = useState<BotEvent[]>([])
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const attemptsRef = useRef(0)
  const mountedRef = useRef(true)
  const sinceRef = useRef<string | null>(null)
  const onEventsRef = useRef(onEvents)
  useEffect(() => { onEventsRef.current = onEvents })

  const connect = useCallback(() => {
    if (!mountedRef.current || !enabled || !botId) return
    if (wsRef.current) {
      const old = wsRef.current
      old.onopen = null; old.onmessage = null; old.onerror = null; old.onclose = null
      try { old.close() } catch { /* ignore */ }
      wsRef.current = null
    }
    if (attemptsRef.current >= 10) return

    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sinceParam = sinceRef.current ? `&since=${encodeURIComponent(sinceRef.current)}` : ''
    const catParam = category ? `&category=${encodeURIComponent(category)}` : ''
    const url = `${proto}//${window.location.host}/ws/bots/${botId}/events?token=${encodeURIComponent(token)}${sinceParam}${catParam}`

    let ws: WebSocket
    try { ws = new WebSocket(url) } catch { scheduleReconnect(); return }
    wsRef.current = ws

    ws.onopen = () => { attemptsRef.current = 0 }

    ws.onmessage = (evt) => {
      try {
        const incoming: BotEvent[] = JSON.parse(evt.data as string)
        if (incoming.length === 0) return
        sinceRef.current = incoming[incoming.length - 1].created_at
        setEvents(prev => {
          const next = [...incoming.reverse(), ...prev]
          return next.length > MAX_EVENTS ? next.slice(0, MAX_EVENTS) : next
        })
        onEventsRef.current?.(incoming)
      } catch { /* ignore */ }
    }

    ws.onclose = () => { scheduleReconnect() }
    ws.onerror = () => { try { ws.close() } catch { /* ignore */ } }

    function scheduleReconnect() {
      if (!mountedRef.current) return
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      attemptsRef.current += 1
      const delay = Math.min(2000 * Math.pow(2, attemptsRef.current - 1), 30000)
      reconnectRef.current = setTimeout(connect, delay)
    }
  }, [enabled, botId, category])

  useEffect(() => {
    mountedRef.current = true
    attemptsRef.current = 0
    sinceRef.current = null
    setEvents([])
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

  return events
}
