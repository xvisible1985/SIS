import { useEffect, useRef, useState } from 'react'

// Subscribes to Bybit public linear tickers for the given symbols and returns
// a map of symbol → latest mark price. Updates on every exchange tick.
export function useTickerPrices(symbols: string[]): Map<string, number> {
  const [prices, setPrices] = useState<Map<string, number>>(new Map())
  const wsRef = useRef<WebSocket | null>(null)
  const key = symbols.slice().sort().join(',')

  useEffect(() => {
    if (!symbols.length) {
      setPrices(new Map())
      return
    }

    wsRef.current?.close()
    const ws = new WebSocket('wss://stream.bybit.com/v5/public/linear')
    wsRef.current = ws

    ws.onopen = () => {
      ws.send(JSON.stringify({ op: 'subscribe', args: symbols.map(s => `tickers.${s}`) }))
    }

    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string)
        if (!msg.topic?.startsWith('tickers.') || !msg.data) return
        const mark = parseFloat(msg.data.markPrice)
        if (mark > 0) {
          const sym: string = msg.topic.slice('tickers.'.length)
          setPrices(prev => {
            const next = new Map(prev)
            next.set(sym, mark)
            return next
          })
        }
      } catch { /* ignore */ }
    }

    ws.onerror = () => ws.close()
    ws.onclose = () => { wsRef.current = null }

    return () => {
      ws.onopen = null; ws.onmessage = null; ws.onerror = null; ws.onclose = null
      ws.close()
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return prices
}
