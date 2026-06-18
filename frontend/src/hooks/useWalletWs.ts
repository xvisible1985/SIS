import { useEffect, useRef, useState, useCallback } from 'react'

interface WalletState {
  equity: number | null
  available: number | null
}

export function useWalletWs(accountId: string | null): WalletState {
  const [state, setState] = useState<WalletState>({ equity: null, available: null })
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const unmountedRef = useRef(false)

  const connect = useCallback(() => {
    if (!accountId || unmountedRef.current) return

    if (wsRef.current) {
      const old = wsRef.current
      old.onopen = null
      old.onmessage = null
      old.onerror = null
      old.onclose = null
      old.close()
      wsRef.current = null
    }

    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${window.location.host}/ws/trader/positions?token=${encodeURIComponent(token)}&account_id=${accountId}`

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string)
        if (msg.type === 'wallet') {
          setState(prev => ({
            equity:    typeof msg.equity    === 'number' ? msg.equity    : prev.equity,
            available: typeof msg.availableBalance === 'number' ? msg.availableBalance : prev.available,
          }))
        }
      } catch { /* ignore */ }
    }

    ws.onclose = () => {
      if (unmountedRef.current) return
      reconnectRef.current = setTimeout(connect, 5000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [accountId])

  useEffect(() => {
    unmountedRef.current = false
    setState({ equity: null, available: null })
    connect()
    return () => {
      unmountedRef.current = true
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      if (wsRef.current) {
        wsRef.current.onopen = null
        wsRef.current.onmessage = null
        wsRef.current.onerror = null
        wsRef.current.onclose = null
        wsRef.current.close()
        wsRef.current = null
      }
    }
  }, [connect])

  return state
}
