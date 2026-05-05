import { useEffect, useRef, useState, useCallback } from 'react'
import type { Position, ActiveOrder, WsMsg } from '../../types'

export type WsStatus = 'connecting' | 'connected' | 'error' | 'closed'
export type LogEntry = { time: string; message: string; error?: boolean }

function mapPosition(p: any): Position {
  const entry = parseFloat(p.entryPrice || p.avgPrice || '0')
  const mark = parseFloat(p.markPrice ?? '0')
  const size = parseFloat(p.size ?? '0')
  const pnl = parseFloat(p.unrealisedPnl ?? '0')
  const sizeUsdt = size * (mark > 0 ? mark : entry)
  return {
    symbol: p.symbol,
    side: p.side,
    size: p.size,
    sizeUsdt,
    entryPrice: String(entry),
    markPrice: String(mark),
    liqPrice: String(p.liqPrice ?? '0'),
    unrealisedPnl: String(pnl),
    unrealisedPnlPct: entry > 0 && size > 0
      ? ((pnl / (size * entry)) * 100).toFixed(2)
      : '0',
    leverage: p.leverage,
    positionIdx: p.positionIdx ?? 0,
    category: p.category ?? 'linear',
  }
}

function mapOrder(o: any): ActiveOrder {
  return {
    orderId: o.orderId,
    orderLinkId: o.orderLinkId ?? '',
    symbol: o.symbol,
    side: o.side,
    orderType: o.orderType,
    price: o.price ?? '',
    qty: o.qty ?? '',
    cumExecQty: o.cumExecQty ?? '0',
    orderStatus: o.orderStatus,
    triggerPrice: o.triggerPrice ?? '',
    category: o.category ?? 'linear',
    orderFilter: o.orderFilter ?? (o.triggerPrice ? 'StopOrder' : 'Order'),
  }
}

const CLOSED_STATUSES = new Set(['Filled', 'Cancelled', 'Rejected', 'Deactivated'])

export function usePositionsWs(accountId: string | null) {
  const [positions, setPositions] = useState<Position[]>([])
  const [orders, setOrders] = useState<ActiveOrder[]>([])
  const [log, setLog] = useState<LogEntry[]>([])
  const [status, setStatus] = useState<WsStatus>('connecting')
  const [accountName, setAccountName] = useState('')
  const [loading, setLoading] = useState(true)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const addLog = useCallback((message: string, error = false) => {
    const time = new Date().toLocaleTimeString('ru-RU')
    setLog(prev => [{ time, message, error }, ...prev].slice(0, 200))
  }, [])

  const connect = useCallback(() => {
    if (!accountId) return
    const token = localStorage.getItem('token') ?? ''
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${proto}//${window.location.host}/ws/trader/positions?token=${encodeURIComponent(token)}&account_id=${accountId}`

    const ws = new WebSocket(url)
    wsRef.current = ws
    setStatus('connecting')
    setLoading(true)

    const loadingTimeout = setTimeout(() => setLoading(false), 15000)

    ws.onopen = () => setStatus('connected')

    ws.onmessage = (evt) => {
      try {
        const msg = JSON.parse(evt.data as string) as WsMsg
        if (msg.type === 'account') {
          setAccountName(msg.accountName)
          return
        }
        if (msg.type === 'log') {
          addLog(msg.message, msg.error)
          if (msg.error && msg.message?.includes('[REST]')) {
            clearTimeout(loadingTimeout)
            setLoading(false)
          }
          return
        }
        if (msg.type === 'position') {
          if (msg.dataType === 'snapshot') {
            setPositions(msg.data.filter((p: any) => parseFloat(p.size) !== 0).map(mapPosition))
          } else {
            setPositions(prev => {
              const next = [...prev]
              for (const p of msg.data) {
                const idx = next.findIndex(x => x.symbol === p.symbol && x.side === p.side)
                if (parseFloat(p.size) === 0) {
                  if (idx !== -1) next.splice(idx, 1)
                } else {
                  const mapped = mapPosition(p)
                  if (idx !== -1) next[idx] = mapped
                  else next.push(mapped)
                }
              }
              return next
            })
          }
          return
        }
        if (msg.type === 'order') {
          if (msg.dataType === 'snapshot') {
            setOrders(msg.data.filter((o: any) => !CLOSED_STATUSES.has(o.orderStatus)).map(mapOrder))
            clearTimeout(loadingTimeout)
            setLoading(false)
          } else {
            setOrders(prev => {
              const next = [...prev]
              for (const o of msg.data) {
                const idx = next.findIndex(x => x.orderId === o.orderId)
                if (CLOSED_STATUSES.has(o.orderStatus)) {
                  if (idx !== -1) next.splice(idx, 1)
                } else {
                  const mapped = mapOrder(o)
                  if (idx !== -1) next[idx] = mapped
                  else next.push(mapped)
                }
              }
              return next
            })
          }
        }
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      clearTimeout(loadingTimeout)
      setLoading(false)
      setStatus('closed')
      addLog('WebSocket закрыт, переподключение через 5с...', true)
      reconnectRef.current = setTimeout(connect, 5000)
    }

    ws.onerror = () => {
      clearTimeout(loadingTimeout)
      setLoading(false)
      setStatus('error')
      addLog('Ошибка WebSocket', true)
    }
  }, [accountId, addLog])

  useEffect(() => {
    connect()
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      wsRef.current?.close()
    }
  }, [connect])

  const reconnect = useCallback(() => {
    wsRef.current?.close()
    connect()
  }, [connect])

  return { positions, orders, log, status, accountName, loading, reconnect }
}
