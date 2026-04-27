import { useEffect, useRef, useState, useCallback } from 'react'

export type BookRow = [string, string] // [price, size]

export function useOrderbook(symbol: string) {
  const [bids, setBids] = useState<BookRow[]>([])
  const [asks, setAsks] = useState<BookRow[]>([])
  const wsRef = useRef<WebSocket | null>(null)
  const bidMap = useRef(new Map<string, string>())
  const askMap = useRef(new Map<string, string>())

  const flush = useCallback(() => {
    setBids([...bidMap.current.entries()].sort((a, b) => parseFloat(b[0]) - parseFloat(a[0])).slice(0, 20))
    setAsks([...askMap.current.entries()].sort((a, b) => parseFloat(a[0]) - parseFloat(b[0])).slice(0, 20))
  }, [])

  useEffect(() => {
    bidMap.current.clear()
    askMap.current.clear()
    setBids([])
    setAsks([])

    const ws = new WebSocket('wss://stream.bybit.com/v5/public/spot')
    wsRef.current = ws

    ws.onopen = () => {
      ws.send(JSON.stringify({ op: 'subscribe', args: [`orderbook.50.${symbol}`] }))
    }
    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string)
        if (!msg.data?.b && !msg.data?.a) return
        if (msg.type === 'snapshot') {
          bidMap.current = new Map(msg.data.b as BookRow[])
          askMap.current = new Map(msg.data.a as BookRow[])
        } else if (msg.type === 'delta') {
          for (const [p, s] of (msg.data.b ?? []) as BookRow[]) {
            if (s === '0') bidMap.current.delete(p); else bidMap.current.set(p, s)
          }
          for (const [p, s] of (msg.data.a ?? []) as BookRow[]) {
            if (s === '0') askMap.current.delete(p); else askMap.current.set(p, s)
          }
        }
        flush()
      } catch { /* ignore */ }
    }
    ws.onerror = () => ws.close()
    return () => ws.close()
  }, [symbol, flush])

  const spread = bids[0] && asks[0]
    ? (parseFloat(asks[0][0]) - parseFloat(bids[0][0])).toFixed(2)
    : null

  return { bids, asks, spread }
}
