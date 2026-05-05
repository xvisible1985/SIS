import { useEffect, useRef, useState, useCallback } from 'react'

export interface Candle {
  time: number
  open: number
  high: number
  low: number
  close: number
}

const cache = new Map<string, { candles: Candle[]; ts: number }>()
const CACHE_TTL = 60_000

export function useCandles(symbol: string, timeframe: string) {
  const [candles, setCandles] = useState<Candle[]>([])
  const [lastPrice, setLastPrice] = useState<string | null>(null)
  const [priceChange, setPriceChange] = useState(0)
  const wsRef = useRef<WebSocket | null>(null)

  const loadHistory = useCallback(async () => {
    const key = `${symbol}-${timeframe}`
    const cached = cache.get(key)
    if (cached && Date.now() - cached.ts < CACHE_TTL) {
      setCandles(cached.candles)
      const last = cached.candles[cached.candles.length - 1]
      const prev = cached.candles[cached.candles.length - 2]
      if (last && prev) {
        setLastPrice(last.close.toFixed(2))
        setPriceChange(((last.close - prev.close) / prev.close) * 100)
      }
      return
    }
    try {
      const res = await fetch(
        `https://api.bybit.com/v5/market/kline?category=spot&symbol=${symbol}&interval=${timeframe}&limit=200`
      )
      const json = await res.json()
      if (json.result?.list) {
        const cs: Candle[] = (json.result.list as string[][])
          .reverse()
          .map(c => ({
            time: Math.floor(Number(c[0]) / 1000),
            open: parseFloat(c[1]),
            high: parseFloat(c[2]),
            low: parseFloat(c[3]),
            close: parseFloat(c[4]),
          }))
        cache.set(key, { candles: cs, ts: Date.now() })
        setCandles(cs)
        const last = cs[cs.length - 1]
        const prev = cs[cs.length - 2]
        if (last && prev) {
          setLastPrice(last.close.toFixed(2))
          setPriceChange(((last.close - prev.close) / prev.close) * 100)
        }
      }
    } catch { /* ignore */ }
  }, [symbol, timeframe])

  const connectWs = useCallback(() => {
    wsRef.current?.close()
    const ws = new WebSocket('wss://stream.bybit.com/v5/public/spot')
    wsRef.current = ws
    ws.onopen = () => {
      ws.send(JSON.stringify({ op: 'subscribe', args: [`kline.${timeframe}.${symbol}`] }))
    }
    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string)
        if (msg.topic?.startsWith('kline') && msg.data?.[0]) {
          const k = msg.data[0]
          setCandles(prev => {
            const candle: Candle = {
              time: Math.floor(k.start / 1000),
              open: parseFloat(k.open),
              high: parseFloat(k.high),
              low: parseFloat(k.low),
              close: parseFloat(k.close),
            }
            const next = [...prev]
            const idx = next.findIndex(c => c.time === candle.time)
            if (idx !== -1) next[idx] = candle
            else next.push(candle)
            return next
          })
          setLastPrice(parseFloat(k.close).toFixed(2))
        }
      } catch { /* ignore */ }
    }
    ws.onerror = () => ws.close()
  }, [symbol, timeframe])

  useEffect(() => {
    loadHistory()
    connectWs()
    return () => wsRef.current?.close()
  }, [loadHistory, connectWs])

  return { candles, lastPrice, priceChange }
}
