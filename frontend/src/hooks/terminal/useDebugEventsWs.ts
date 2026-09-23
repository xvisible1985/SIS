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

// accountId scopes the feed to one exchange account (the terminal's currently selected
// one) — omitting it falls back to the pre-fix "all of the user's accounts" behavior,
// which mixed events across accounts when a user owns more than one (found live
// 2026-09-23: two same-named "Gonchar 2.0" bot pairs, one per account, showed each
// other's events regardless of which account was selected).
export function useDebugEventsWs(open: boolean, accountId: string | null) {
  const [events, setEvents] = useState<DebugEvent[]>([])
  const sinceRef = useRef('')

  useEffect(() => {
    if (!open) return
    sinceRef.current = ''
    setEvents([])

    let destroyed = false
    let ws: WebSocket | null = null
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null

    function connect() {
      if (destroyed) return
      const token = localStorage.getItem('token') ?? ''
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const since = sinceRef.current ? `&since=${encodeURIComponent(sinceRef.current)}` : ''
      const acct = accountId ? `&account_id=${encodeURIComponent(accountId)}` : ''
      ws = new WebSocket(`${proto}//${window.location.host}/ws/debug-events?token=${encodeURIComponent(token)}${since}${acct}`)

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
  }, [open, accountId])

  function clear() {
    setEvents([])
    sinceRef.current = ''
  }

  return { events, clear }
}
