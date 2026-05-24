import { useEffect, useRef, useState, useCallback } from 'react'
import type { Position, ActiveOrder, WsMsg, ChartExecution } from '../../types'

export type WsStatus = 'connecting' | 'connected' | 'error' | 'closed'
export type LogEntry = { time: string; message: string; error?: boolean }

// Модульный кэш — живёт между навигациями, сбрасывается при смене аккаунта
let _cacheAccountId: string | null = null
let _cachePositions: Position[] = []
let _cacheOrders: ActiveOrder[] = []
let _cacheAccountName = ''

const CACHE_TTL_MS = 5 * 60 * 1000

function lsKey(accountId: string) { return `sis_trader_${accountId}` }

function readLs(accountId: string): { positions: Position[]; orders: ActiveOrder[]; name: string; ts: number } | null {
  try {
    const raw = localStorage.getItem(lsKey(accountId))
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (!parsed || Date.now() - (parsed.ts || 0) > CACHE_TTL_MS) return null
    return parsed
  } catch { return null }
}

function writeLs(accountId: string, positions: Position[], orders: ActiveOrder[], name: string) {
  try {
    localStorage.setItem(lsKey(accountId), JSON.stringify({ positions, orders, name, ts: Date.now() }))
  } catch { /* ignore quota errors */ }
}

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
    orderId: o.orderId || o.orderLinkId || '',
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
  const ls = accountId ? readLs(accountId) : null
  const fromCache = accountId !== null && accountId === _cacheAccountId
  const initialPositions = fromCache ? _cachePositions : (ls?.positions ?? [])
  const initialOrders    = fromCache ? _cacheOrders    : (ls?.orders ?? [])
  const initialName      = fromCache ? _cacheAccountName : (ls?.name ?? '')

  const [positions, setPositions] = useState<Position[]>(initialPositions)
  const [orders, setOrders] = useState<ActiveOrder[]>(initialOrders)
  const [executions, setExecutions] = useState<ChartExecution[]>([])
  const [log, setLog] = useState<LogEntry[]>([])
  const [status, setStatus] = useState<WsStatus>('connecting')
  const [accountName, setAccountName] = useState(initialName)
  const [loading, setLoading] = useState(!fromCache && !ls)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const rawPositions = useRef(new Map<string, any>())
  const rawOrders = useRef(new Map<string, any>())

  const addLog = useCallback((message: string, error = false) => {
    const time = new Date().toLocaleTimeString('ru-RU')
    setLog(prev => [{ time, message, error }, ...prev].slice(0, 200))
  }, [])

  const connect = useCallback(() => {
    if (!accountId) return

    // Safely close existing WS without triggering its onclose reconnect logic
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
    setStatus('connecting')
    const hasCache = accountId === _cacheAccountId && (_cachePositions.length > 0 || _cacheOrders.length > 0)
    if (!hasCache) setLoading(true)

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
          // Key by positionIdx for hedge mode (1=long, 2=short), symbol-side for one-way (positionIdx=0)
          const posKey = (p: any) => p.positionIdx ? `${p.symbol}-${p.positionIdx}` : `${p.symbol}-${p.side}`
          const posSize = (s: any) => { const n = parseFloat(s); return isNaN(n) ? 0 : n }

          if (msg.dataType === 'snapshot') {
            rawPositions.current.clear()
            for (const p of msg.data) rawPositions.current.set(posKey(p), p)
            setPositions(msg.data.filter((p: any) => posSize(p.size) !== 0).map(mapPosition))
          } else {
            setPositions(prev => {
              const next = [...prev]
              for (const p of msg.data) {
                const key = posKey(p)
                const merged = { ...(rawPositions.current.get(key) ?? {}), ...p }
                rawPositions.current.set(key, merged)
                const idx = p.positionIdx
                  ? next.findIndex(x => x.symbol === p.symbol && x.positionIdx === p.positionIdx)
                  : next.findIndex(x => x.symbol === p.symbol && x.side === p.side)
                if (posSize(merged.size) === 0) {
                  rawPositions.current.delete(key)
                  if (idx !== -1) next.splice(idx, 1)
                } else {
                  const mapped = mapPosition(merged)
                  if (idx !== -1) next[idx] = mapped
                  else next.push(mapped)
                }
              }
              return next
            })
          }
          return
        }
        if (msg.type === 'execution') {
          const items: ChartExecution[] = ((msg as any).data ?? [])
            .filter((e: any) => e.execType === 'Trade')
            .map((e: any) => ({
              execId: e.execId as string,
              symbol: e.symbol as string,
              side: e.side as 'Buy' | 'Sell',
              price: parseFloat(e.execPrice ?? '0'),
              qty: (e.execQty ?? '') as string,
              timeMs: parseInt(e.execTime ?? '0', 10),
              orderLinkId: (e.orderLinkId as string) || undefined,
            }))
          if (items.length > 0) {
            setExecutions(prev => {
              const seen = new Set(prev.map(e => e.execId))
              const fresh = items.filter(e => !seen.has(e.execId))
              return fresh.length ? [...prev, ...fresh].slice(-500) : prev
            })
          }
          return
        }
        if (msg.type === 'order') {
          if (msg.dataType === 'snapshot') {
            rawOrders.current.clear()
            for (const o of msg.data) {
              const key = o.orderId || o.orderLinkId
              if (key) rawOrders.current.set(key, o)
            }
            setOrders(msg.data.filter((o: any) => !CLOSED_STATUSES.has(o.orderStatus)).map(mapOrder))
            clearTimeout(loadingTimeout)
            setLoading(false)
          } else {
            setOrders(prev => {
              const next = [...prev]
              for (const o of msg.data) {
                const key = o.orderId || o.orderLinkId
                if (!key) continue
                const merged = { ...(rawOrders.current.get(key) ?? {}), ...o }
                rawOrders.current.set(key, merged)
                const idx = next.findIndex(x => x.orderId === key || x.orderLinkId === key)
                if (CLOSED_STATUSES.has(merged.orderStatus)) {
                  rawOrders.current.delete(key)
                  if (idx !== -1) next.splice(idx, 1)
                } else {
                  const mapped = mapOrder(merged)
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

  // Когда accountId становится известен — подгружаем кэш (module или localStorage)
  useEffect(() => {
    if (!accountId) return
    if (accountId === _cacheAccountId && (_cachePositions.length > 0 || _cacheOrders.length > 0)) {
      setPositions(_cachePositions)
      setOrders(_cacheOrders)
      setAccountName(_cacheAccountName)
      setLoading(false)
      return
    }
    const ls = readLs(accountId)
    if (ls) {
      setPositions(ls.positions)
      setOrders(ls.orders)
      setAccountName(ls.name)
      setLoading(false)
    }
  }, [accountId])

  useEffect(() => {
    connect()
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      if (wsRef.current) {
        wsRef.current.onopen = null
        wsRef.current.onmessage = null
        wsRef.current.onerror = null
        wsRef.current.onclose = null
        wsRef.current.close()
      }
    }
  }, [connect])

  // Синхронизируем данные в кэш и localStorage после каждого обновления
  useEffect(() => {
    if (!accountId) return
    _cacheAccountId = accountId
    _cachePositions = positions
    _cacheOrders = orders
    writeLs(accountId, positions, orders, accountName)
  }, [accountId, positions, orders, accountName])

  useEffect(() => {
    if (accountId && accountName) _cacheAccountName = accountName
  }, [accountId, accountName])

  const reconnect = useCallback(() => {
    if (reconnectRef.current) clearTimeout(reconnectRef.current)
    connect()
  }, [connect])

  const removeOrder = useCallback((orderId: string) => {
    rawOrders.current.delete(orderId)
    setOrders(prev => prev.filter(o => o.orderId !== orderId && o.orderLinkId !== orderId))
  }, [])

  return { positions, orders, executions, log, status, accountName, loading, reconnect, removeOrder }
}
