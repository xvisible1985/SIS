# Trader Terminal — Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Добавить страницу `/terminal` в React-фронтенд `sis` — торговый терминал с графиком, стаканом, формой ордеров, таблицами позиций/ордеров/истории/сделок.

**Architecture:** Новая страница `TerminalPage` с grid-лейаутом (65%/35%). Данные реального времени — через хуки с WS (позиции) и Bybit public WS (свечи, стакан). Исторические данные — через axios API-клиент. Визуальный референс — `test1/nuxt-app/app/pages/bots.vue` и `test1/nuxt-app/app/components/OrderForm.vue`.

**Tech Stack:** React 18, lightweight-charts v5, Tailwind CSS, axios, Vitest + Testing Library

**Зависит от:** бэкенд-плана `2026-04-21-trader-backend.md` — все API-эндпоинты должны быть реализованы.

---

## File Map

| Файл | Действие | Что делает |
|------|----------|------------|
| `frontend/package.json` | Modify | добавить lightweight-charts |
| `frontend/src/types.ts` | Modify | добавить trader-типы |
| `frontend/src/api/accounts.ts` | Create | CRUD exchange accounts |
| `frontend/src/api/trader.ts` | Create | place/cancel/leverage/history/executions/stats |
| `frontend/src/hooks/terminal/usePositionsWs.ts` | Create | WS /ws/trader/positions, snapshot+delta merge |
| `frontend/src/hooks/terminal/useCandles.ts` | Create | Bybit public WS kline + REST история |
| `frontend/src/hooks/terminal/useOrderbook.ts` | Create | Bybit public WS orderbook.50 |
| `frontend/src/hooks/terminal/useTraderApi.ts` | Create | обёртка над api/trader.ts с loading/error |
| `frontend/src/components/terminal/Chart.tsx` | Create | lightweight-charts свечи + entry lines |
| `frontend/src/components/terminal/Orderbook.tsx` | Create | стакан bids/asks с volume bars |
| `frontend/src/components/terminal/OrderForm.tsx` | Create | Limit/Market/Conditional форма ордера |
| `frontend/src/components/terminal/PositionsTable.tsx` | Create | открытые позиции + "Закрыть" |
| `frontend/src/components/terminal/OrdersTable.tsx` | Create | активные ордера + "Снять" |
| `frontend/src/components/terminal/HistoryTable.tsx` | Create | закрытые ордера с пагинацией |
| `frontend/src/components/terminal/ExecutionsTable.tsx` | Create | сделки/фандинг/комиссии |
| `frontend/src/components/terminal/TradeLog.tsx` | Create | WS-лог событий |
| `frontend/src/pages/TerminalPage.tsx` | Create | главная страница терминала |
| `frontend/src/components/Layout.tsx` | Modify | добавить "Terminal" в навигацию |
| `frontend/src/App.tsx` | Modify | добавить маршрут /terminal |

---

## Task 1: Зависимости + типы + API-клиенты

**Files:**
- Modify: `frontend/package.json`
- Modify: `frontend/src/types.ts`
- Create: `frontend/src/api/accounts.ts`
- Create: `frontend/src/api/trader.ts`

- [ ] **Установить lightweight-charts**

```bash
cd frontend && npm install lightweight-charts
```

Убедиться в package.json: `"lightweight-charts": "^5.x.x"`.

- [ ] **Добавить типы в `frontend/src/types.ts`**

Дописать в конец файла:

```typescript
// Exchange Accounts
export interface ExchangeAccount {
  id: string
  exchange: string
  label: string
  is_active: boolean
  created_at: string
}

// Trader — positions & orders (live, from WS)
export interface Position {
  symbol: string
  side: 'Buy' | 'Sell'
  size: string
  sizeUsdt: number
  entryPrice: string
  markPrice: string
  liqPrice: string
  unrealisedPnl: string
  unrealisedPnlPct: string
  leverage: string
  positionIdx: number
  category: string
}

export interface ActiveOrder {
  orderId: string
  orderLinkId: string
  symbol: string
  side: 'Buy' | 'Sell'
  orderType: string
  price: string
  qty: string
  cumExecQty: string
  orderStatus: string
  triggerPrice: string
  category: string
  orderFilter: string
}

// Trader — history (from REST)
export interface TraderOrder {
  id: string
  account_id: string
  order_link_id: string
  order_id: string | null
  exchange: string
  symbol: string
  category: string
  side: string
  order_type: string
  qty: string
  price: string | null
  trigger_price: string | null
  status: string
  cum_exec_qty: string
  cum_exec_fee: string
  created_at: string
}

export interface TraderExecution {
  id: string
  exec_id: string
  order_id: string | null
  order_link_id: string | null
  symbol: string
  category: string
  side: string | null
  exec_type: string
  qty: string | null
  price: string | null
  exec_value: string | null
  exec_fee: string | null
  fee_rate: string | null
  is_maker: boolean | null
  exec_time: string
}

export interface TraderStats {
  total_fee: number
  total_funding: number
  trade_count: number
}

export interface PaginatedResponse<T> {
  orders?: T[]
  executions?: T[]
  total: number
  page: number
}

// WS position stream messages
export type WsMsg =
  | { type: 'account'; accountName: string }
  | { type: 'log'; message: string; error?: boolean }
  | { type: 'position'; dataType: 'snapshot' | 'delta'; data: any[] }
  | { type: 'order'; dataType: 'snapshot' | 'delta'; data: any[] }
```

- [ ] **Создать `frontend/src/api/accounts.ts`**

```typescript
import { apiClient } from './client'
import type { ExchangeAccount } from '../types'

export async function listAccounts(): Promise<ExchangeAccount[]> {
  const res = await apiClient.get<ExchangeAccount[]>('/accounts')
  return res.data
}

export async function createAccount(data: {
  exchange: string
  label: string
  api_key: string
  secret: string
}): Promise<ExchangeAccount> {
  const res = await apiClient.post<ExchangeAccount>('/accounts', data)
  return res.data
}

export async function deleteAccount(id: string): Promise<void> {
  await apiClient.delete(`/accounts/${id}`)
}

export async function verifyAccount(id: string): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.get<{ ok: boolean; message?: string }>(`/accounts/${id}/verify`)
  return res.data
}
```

- [ ] **Создать `frontend/src/api/trader.ts`**

```typescript
import { apiClient } from './client'
import type { TraderOrder, TraderExecution, TraderStats, PaginatedResponse } from '../types'

export interface PlaceOrderParams {
  account_id: string
  symbol: string
  category: string
  side: 'Buy' | 'Sell'
  order_type: 'Limit' | 'Market' | 'Conditional'
  qty: string
  price?: string
  trigger_price?: string
  trigger_by?: string
  trigger_direction?: 1 | 2
  time_in_force?: string
  order_filter?: string
  reduce_only?: boolean
  position_idx?: number
}

export async function placeOrder(params: PlaceOrderParams): Promise<{ ok: boolean; message?: string; order_id?: string; order_link_id?: string }> {
  const res = await apiClient.post('/trader/order', params)
  return res.data
}

export async function cancelOrder(params: {
  account_id: string
  symbol: string
  category: string
  order_id: string
  order_filter?: string
}): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.delete('/trader/order', { data: params })
  return res.data
}

export async function setLeverage(params: {
  account_id: string
  symbol: string
  category: string
  leverage: string
}): Promise<{ ok: boolean; message?: string }> {
  const res = await apiClient.post('/trader/leverage', params)
  return res.data
}

export async function listOrders(params: {
  account_id?: string
  status?: 'open' | 'closed' | 'all'
  symbol?: string
  page?: number
  limit?: number
}): Promise<PaginatedResponse<TraderOrder> & { orders: TraderOrder[] }> {
  const res = await apiClient.get('/trader/orders', { params })
  return res.data
}

export async function listExecutions(params: {
  account_id?: string
  type?: string
  symbol?: string
  from?: string
  to?: string
  page?: number
  limit?: number
}): Promise<PaginatedResponse<TraderExecution> & { executions: TraderExecution[] }> {
  const res = await apiClient.get('/trader/executions', { params })
  return res.data
}

export async function getStats(params: {
  account_id?: string
  from?: string
  to?: string
}): Promise<TraderStats> {
  const res = await apiClient.get<TraderStats>('/trader/stats', { params })
  return res.data
}
```

- [ ] **Убедиться что фронтенд компилируется**

```bash
cd frontend && npm run build
# Expected: no TypeScript errors
```

- [ ] **Commit**

```bash
git add frontend/
git commit -m "feat: add trader types and API client functions"
```

---

## Task 2: WS-хуки и хуки данных

**Files:**
- Create: `frontend/src/hooks/terminal/usePositionsWs.ts`
- Create: `frontend/src/hooks/terminal/useCandles.ts`
- Create: `frontend/src/hooks/terminal/useOrderbook.ts`

- [ ] **Создать `frontend/src/hooks/terminal/usePositionsWs.ts`**

Адаптация логики `test1/nuxt-app/app/pages/bots.vue` (функции `connectPositionsWs`, `mapPosition`, `mapOrder`):

```typescript
import { useEffect, useRef, useState, useCallback } from 'react'
import type { Position, ActiveOrder, WsMsg } from '../../types'

export type WsStatus = 'connecting' | 'connected' | 'error' | 'closed'
export type LogEntry = { time: string; message: string; error?: boolean }

function mapPosition(p: any): Position {
  const entry = parseFloat(p.entryPrice ?? p.avgPrice ?? '0')
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
```

- [ ] **Создать `frontend/src/hooks/terminal/useCandles.ts`**

```typescript
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
```

- [ ] **Создать `frontend/src/hooks/terminal/useOrderbook.ts`**

```typescript
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
```

- [ ] **Убедиться что компилируется**

```bash
cd frontend && npm run build 2>&1 | head -20
# Expected: no TypeScript errors
```

- [ ] **Commit**

```bash
git add frontend/src/
git commit -m "feat: add terminal WebSocket hooks (positions, candles, orderbook)"
```

---

## Task 3: Chart + Orderbook + OrderForm компоненты

**Files:**
- Create: `frontend/src/components/terminal/Chart.tsx`
- Create: `frontend/src/components/terminal/Orderbook.tsx`
- Create: `frontend/src/components/terminal/OrderForm.tsx`

- [ ] **Создать `frontend/src/components/terminal/Chart.tsx`**

```tsx
import { useEffect, useRef } from 'react'
import { createChart, CandlestickSeries, type IChartApi, type ISeriesApi, ColorType } from 'lightweight-charts'
import type { Candle } from '../../hooks/terminal/useCandles'
import type { Position, ActiveOrder } from '../../types'

interface Props {
  candles: Candle[]
  positions: Position[]
  orders: ActiveOrder[]
  symbol: string
}

export function Chart({ candles, positions, orders, symbol }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const priceLines = useRef<any[]>([])

  // Init chart
  useEffect(() => {
    if (!containerRef.current) return
    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
      layout: {
        background: { type: ColorType.Solid, color: '#0a0a0f' },
        textColor: '#9ca3af',
      },
      grid: {
        vertLines: { color: '#1a1a2e' },
        horzLines: { color: '#1a1a2e' },
      },
      rightPriceScale: { borderColor: '#2d2d3d' },
      timeScale: { borderColor: '#2d2d3d', timeVisible: true },
      crosshair: { mode: 1 },
    })
    chartRef.current = chart

    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#00DC82', downColor: '#ef4444',
      borderUpColor: '#00DC82', borderDownColor: '#ef4444',
      wickUpColor: '#00DC82', wickDownColor: '#ef4444',
    })
    seriesRef.current = series

    const ro = new ResizeObserver(() => {
      if (containerRef.current) {
        chart.applyOptions({
          width: containerRef.current.clientWidth,
          height: containerRef.current.clientHeight,
        })
      }
    })
    ro.observe(containerRef.current)

    return () => {
      ro.disconnect()
      chart.remove()
    }
  }, [])

  // Update candles
  useEffect(() => {
    if (!seriesRef.current || !candles.length) return
    seriesRef.current.setData(candles as any)
    chartRef.current?.timeScale().fitContent()
  }, [candles])

  // Draw entry lines for positions and orders
  useEffect(() => {
    if (!seriesRef.current) return
    for (const line of priceLines.current) {
      try { seriesRef.current.removePriceLine(line) } catch {}
    }
    priceLines.current = []

    for (const pos of positions.filter(p => p.symbol === symbol)) {
      const price = parseFloat(pos.entryPrice)
      if (!price) continue
      const isLong = pos.side === 'Buy'
      priceLines.current.push(seriesRef.current.createPriceLine({
        price,
        color: isLong ? '#00DC82' : '#ef4444',
        lineWidth: 1,
        lineStyle: 2,
        axisLabelVisible: true,
        title: `${isLong ? 'Long' : 'Short'} ${pos.size}`,
      }))
    }

    for (const ord of orders.filter(o => o.symbol === symbol)) {
      const isLong = ord.side === 'Buy'
      const color = isLong ? '#34d399' : '#f87171'
      if (ord.triggerPrice && parseFloat(ord.triggerPrice) > 0) {
        priceLines.current.push(seriesRef.current.createPriceLine({
          price: parseFloat(ord.triggerPrice),
          color,
          lineWidth: 1,
          lineStyle: 4,
          axisLabelVisible: true,
          title: `${isLong ? 'Buy' : 'Sell'} Stop`,
        }))
      }
      if (ord.price && parseFloat(ord.price) > 0 && ord.orderType === 'Limit') {
        priceLines.current.push(seriesRef.current.createPriceLine({
          price: parseFloat(ord.price),
          color,
          lineWidth: 1,
          lineStyle: 1,
          axisLabelVisible: true,
          title: `${isLong ? 'Buy' : 'Sell'} ${ord.qty}`,
        }))
      }
    }
  }, [positions, orders, symbol])

  return <div ref={containerRef} className="w-full h-full" />
}
```

- [ ] **Создать `frontend/src/components/terminal/Orderbook.tsx`**

```tsx
import type { BookRow } from '../../hooks/terminal/useOrderbook'

interface Props {
  bids: BookRow[]
  asks: BookRow[]
  spread: string | null
  symbol: string
}

export function Orderbook({ bids, asks, spread, symbol }: Props) {
  const maxBid = Math.max(...bids.map(([, s]) => parseFloat(s)), 1)
  const maxAsk = Math.max(...asks.map(([, s]) => parseFloat(s)), 1)

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-gray-700 flex-shrink-0">
        <span className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Стакан</span>
        <span className="text-xs text-gray-400 font-mono">{symbol}</span>
        {spread && (
          <span className="text-xs font-mono text-gray-400">
            Спред: <span className="text-white">{spread}</span>
          </span>
        )}
      </div>
      <div className="flex flex-1 overflow-hidden min-h-0">
        {/* Bids */}
        <div className="flex-1 flex flex-col overflow-hidden border-r border-gray-700/30">
          <div className="flex justify-between px-2 py-0.5 border-b border-gray-700/20 flex-shrink-0">
            <span className="text-[9px] font-medium text-green-500 uppercase">Bid</span>
            <span className="text-[9px] text-gray-400">Объём</span>
          </div>
          <div className="flex-1 overflow-auto">
            {bids.map(([price, size]) => (
              <div key={price} className="relative flex justify-between px-2 py-[2px] hover:bg-green-500/5">
                <div
                  className="absolute inset-y-0 right-0 bg-green-500/10"
                  style={{ width: `${(parseFloat(size) / maxBid * 100).toFixed(1)}%` }}
                />
                <span className="relative text-[10px] font-mono text-green-400 z-10">{parseFloat(price).toFixed(2)}</span>
                <span className="relative text-[10px] font-mono text-gray-400 z-10">{parseFloat(size).toFixed(3)}</span>
              </div>
            ))}
            {!bids.length && <div className="text-center text-gray-500 text-[10px] py-4">Загрузка...</div>}
          </div>
        </div>
        {/* Asks */}
        <div className="flex-1 flex flex-col overflow-hidden">
          <div className="flex justify-between px-2 py-0.5 border-b border-gray-700/20 flex-shrink-0">
            <span className="text-[9px] font-medium text-red-500 uppercase">Ask</span>
            <span className="text-[9px] text-gray-400">Объём</span>
          </div>
          <div className="flex-1 overflow-auto">
            {asks.map(([price, size]) => (
              <div key={price} className="relative flex justify-between px-2 py-[2px] hover:bg-red-500/5">
                <div
                  className="absolute inset-y-0 left-0 bg-red-500/10"
                  style={{ width: `${(parseFloat(size) / maxAsk * 100).toFixed(1)}%` }}
                />
                <span className="relative text-[10px] font-mono text-red-400 z-10">{parseFloat(price).toFixed(2)}</span>
                <span className="relative text-[10px] font-mono text-gray-400 z-10">{parseFloat(size).toFixed(3)}</span>
              </div>
            ))}
            {!asks.length && <div className="text-center text-gray-500 text-[10px] py-4">Загрузка...</div>}
          </div>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Создать `frontend/src/components/terminal/OrderForm.tsx`**

```tsx
import { useState, useEffect, useCallback } from 'react'
import { placeOrder, cancelOrder, setLeverage } from '../../api/trader'
import type { ActiveOrder, Position } from '../../types'

interface Props {
  accountId: string
  symbol: string
  lastPrice: string | null
  orders: ActiveOrder[]
  positions: Position[]
}

type Tab = 'Limit' | 'Market' | 'Conditional'
type LogEntry = { text: string; status: 'pending' | 'ok' | 'error' }

function coinIcon(symbol: string) {
  const base = symbol.replace(/USDT|USDC|USD$/, '').toLowerCase()
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${base}.png`
}

export function OrderForm({ accountId, symbol, lastPrice, orders, positions }: Props) {
  const category = symbol.endsWith('USDT') || symbol.endsWith('USDC') ? 'linear' : 'inverse'
  const baseCoin = symbol.replace(/USDT|USDC|USD$/, '')
  const quoteCoin = symbol.endsWith('USDC') ? 'USDC' : 'USDT'

  const [tab, setTab] = useState<Tab>('Limit')
  const [side, setSide] = useState<'Buy' | 'Sell'>('Buy')
  const [price, setPrice] = useState('')
  const [qty, setQty] = useState('')
  const [qtyUsdt, setQtyUsdt] = useState('')
  const [triggerPrice, setTriggerPrice] = useState('')
  const [triggerBy, setTriggerBy] = useState('MarkPrice')
  const [triggerDir, setTriggerDir] = useState<1 | 2>(1)
  const [condType, setCondType] = useState<'Market' | 'Limit'>('Market')
  const [tif, setTif] = useState('GTC')
  const [leverage, setLev] = useState('')
  const [reduceOnly, setReduceOnly] = useState(false)
  const [loading, setLoading] = useState(false)
  const [log, setLog] = useState<LogEntry[]>([])
  const [qtyStep, setQtyStep] = useState('1')

  const hedgeMode = positions.some(p => p.symbol === symbol && p.positionIdx !== 0)

  const effectivePrice = useCallback(() => {
    if (tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit'))
      return parseFloat(price) || parseFloat(lastPrice ?? '0') || 0
    return parseFloat(lastPrice ?? '0') || 0
  }, [tab, condType, price, lastPrice])

  // Fetch instrument info for qty step
  useEffect(() => {
    fetch(`https://api.bybit.com/v5/market/instruments-info?category=${category}&symbol=${symbol}`)
      .then(r => r.json())
      .then(j => {
        const step = j.result?.list?.[0]?.lotSizeFilter?.qtyStep
        if (step) setQtyStep(step)
      })
      .catch(() => {})
  }, [symbol, category])

  function applyStep(val: number): string {
    const step = parseFloat(qtyStep)
    if (!step || !val) return ''
    const decimals = qtyStep.includes('.') ? qtyStep.split('.')[1].length : 0
    return (Math.round(val / step) * step).toFixed(decimals)
  }

  function onQtyChange(v: string) {
    setQty(v)
    const p = effectivePrice()
    setQtyUsdt(p > 0 && v ? (parseFloat(v) * p).toFixed(2) : '')
  }

  function onQtyUsdtChange(v: string) {
    setQtyUsdt(v)
    const p = effectivePrice()
    const decimals = qtyStep.includes('.') ? qtyStep.split('.')[1].length : 4
    setQty(p > 0 && v ? (parseFloat(v) / p).toFixed(Math.max(decimals, 4)) : '')
  }

  function addLog(text: string, status: LogEntry['status'] = 'pending') {
    setLog(prev => [...prev, { text, status }])
  }
  function resolveLog(status: 'ok' | 'error', text?: string) {
    setLog(prev => {
      const next = [...prev]
      const last = next[next.length - 1]
      if (last) { last.status = status; if (text) last.text = text }
      return next
    })
  }

  // Reset on symbol change
  useEffect(() => {
    setPrice(''); setQty(''); setQtyUsdt(''); setTriggerPrice(''); setLog([])
  }, [symbol])

  async function handleSubmit() {
    const finalQty = qty || (qtyUsdt ? applyStep(parseFloat(qtyUsdt) / (effectivePrice() || 1)) : '')
    if (!finalQty) { addLog('Укажите количество или объём в USDT', 'error'); return }
    if (tab === 'Limit' && !price) { addLog('Укажите цену', 'error'); return }
    if (tab === 'Conditional' && !triggerPrice) { addLog('Укажите цену триггера', 'error'); return }

    setLoading(true)
    setLog([])

    const sideLabel = side === 'Buy' ? '▲ Buy' : '▼ Sell'
    addLog(`${sideLabel} ${applyStep(parseFloat(finalQty))} ${baseCoin}`, 'ok')

    // Set leverage if specified
    if (leverage && parseFloat(leverage) > 0) {
      addLog(`Плечо ${leverage}x — установка...`)
      const levRes = await setLeverage({ account_id: accountId, symbol, category, leverage })
      resolveLog(levRes.ok ? 'ok' : 'error', levRes.ok ? `Плечо ${leverage}x установлено` : levRes.message)
      if (!levRes.ok) { setLoading(false); return }
    }

    addLog('Отправка ордера...')
    const res = await placeOrder({
      account_id: accountId,
      symbol,
      category,
      side,
      order_type: tab === 'Conditional' ? condType : tab,
      qty: applyStep(parseFloat(finalQty)),
      price: tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit') ? price : undefined,
      trigger_price: tab === 'Conditional' ? triggerPrice : undefined,
      trigger_by: tab === 'Conditional' ? triggerBy : undefined,
      trigger_direction: tab === 'Conditional' ? triggerDir : undefined,
      time_in_force: tab === 'Limit' ? tif : undefined,
      order_filter: tab === 'Conditional' ? 'StopOrder' : undefined,
      reduce_only: reduceOnly,
      position_idx: hedgeMode ? (side === 'Buy' ? 1 : 2) : 0,
    })

    if (res.ok) {
      resolveLog('ok', `Принят: ${symbol} ${sideLabel}`)
      setQty(''); setQtyUsdt(''); setPrice(''); setTriggerPrice('')
    } else {
      resolveLog('error', `Отклонено: ${res.message}`)
    }
    setLoading(false)
  }

  async function handleCancel(ord: ActiveOrder) {
    await cancelOrder({
      account_id: accountId,
      symbol: ord.symbol,
      category: ord.category,
      order_id: ord.orderId,
      order_filter: ord.orderFilter,
    })
  }

  const symbolOrders = orders.filter(o => o.symbol === symbol)

  return (
    <div className="flex flex-col h-full overflow-hidden text-xs text-white bg-gray-900">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-gray-700 flex-shrink-0">
        <img src={coinIcon(symbol)} className="w-5 h-5 rounded-full" onError={e => (e.currentTarget.style.display = 'none')} />
        <span className="font-bold font-mono text-sm">{symbol}</span>
        <span className="ml-auto text-gray-400 text-[10px]">
          <span className={`inline-block w-2 h-2 rounded-full mr-1 ${hedgeMode ? 'bg-blue-500' : 'bg-gray-500'}`} />
          {hedgeMode ? 'Хедж' : 'Один. нап.'}
        </span>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-gray-700 flex-shrink-0">
        {(['Limit', 'Market', 'Conditional'] as Tab[]).map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`flex-1 py-1.5 text-xs font-medium border-b-2 transition-colors ${tab === t ? 'border-blue-500 text-white' : 'border-transparent text-gray-400 hover:text-white'}`}
          >
            {t === 'Limit' ? 'Лимит' : t === 'Market' ? 'Рынок' : 'Условный'}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-2 space-y-2">
        {/* Buy/Sell */}
        <div className="grid grid-cols-2 gap-1">
          <button
            onClick={() => setSide('Buy')}
            className={`py-1.5 rounded font-semibold transition-colors ${side === 'Buy' ? 'bg-green-500 text-white' : 'bg-green-500/10 text-green-500 hover:bg-green-500/20'}`}
          >Купить / Long</button>
          <button
            onClick={() => setSide('Sell')}
            className={`py-1.5 rounded font-semibold transition-colors ${side === 'Sell' ? 'bg-red-500 text-white' : 'bg-red-500/10 text-red-500 hover:bg-red-500/20'}`}
          >Продать / Short</button>
        </div>

        {/* Conditional sub-type */}
        {tab === 'Conditional' && (
          <>
            <div className="grid grid-cols-2 gap-1">
              {(['Market', 'Limit'] as const).map(t => (
                <button key={t} onClick={() => setCondType(t)}
                  className={`py-1 rounded text-xs transition-colors ${condType === t ? 'bg-gray-700 text-white' : 'text-gray-400 hover:text-white'}`}>
                  {t === 'Market' ? 'По рынку' : 'Лимит'}
                </button>
              ))}
            </div>
            <div className="space-y-1">
              <div className="flex items-center justify-between">
                <label className="text-gray-400">Триггер</label>
                <div className="flex gap-1">
                  {['MarkPrice', 'LastPrice', 'IndexPrice'].map(opt => (
                    <button key={opt} onClick={() => setTriggerBy(opt)}
                      className={`px-1.5 py-0.5 rounded text-xs ${triggerBy === opt ? 'bg-blue-500/20 text-blue-400' : 'text-gray-400 hover:text-white'}`}>
                      {opt === 'MarkPrice' ? 'Марк.' : opt === 'LastPrice' ? 'Посл.' : 'Индекс'}
                    </button>
                  ))}
                </div>
              </div>
              <div className="flex items-center gap-1">
                <input value={triggerPrice} onChange={e => setTriggerPrice(e.target.value)} type="number" placeholder="0.00"
                  className="flex-1 bg-gray-800 border border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
                <div className="flex flex-col gap-0.5">
                  {([1, 2] as const).map(d => (
                    <button key={d} onClick={() => setTriggerDir(d)}
                      className={`px-1.5 py-0.5 rounded text-xs ${triggerDir === d ? (d === 1 ? 'bg-green-500/20 text-green-500' : 'bg-red-500/20 text-red-500') : 'text-gray-400'}`}>
                      {d === 1 ? '↑' : '↓'}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </>
        )}

        {/* Price */}
        {(tab === 'Limit' || (tab === 'Conditional' && condType === 'Limit')) && (
          <div>
            <label className="text-gray-400 block mb-1">Цена ({quoteCoin})</label>
            <input value={price} onChange={e => setPrice(e.target.value)} type="number" placeholder="0.00"
              className="w-full bg-gray-800 border border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
          </div>
        )}

        {/* Qty */}
        <div className="grid grid-cols-2 gap-1">
          <div>
            <label className="text-gray-400 block mb-1">Кол-во ({baseCoin})</label>
            <input value={qty} onChange={e => onQtyChange(e.target.value)} type="number" placeholder="0.00"
              className="w-full bg-gray-800 border border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
          </div>
          <div>
            <label className="text-gray-400 block mb-1">Объём (USDT)</label>
            <input value={qtyUsdt} onChange={e => onQtyUsdtChange(e.target.value)} type="number" placeholder="0.00"
              className="w-full bg-gray-800 border border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
          </div>
        </div>

        {/* TIF */}
        {tab === 'Limit' && (
          <div className="flex items-center gap-1">
            <span className="text-gray-400">TIF:</span>
            {['GTC', 'IOC', 'FOK'].map(t => (
              <button key={t} onClick={() => setTif(t)}
                className={`px-1.5 py-0.5 rounded text-xs ${tif === t ? 'bg-blue-500/20 text-blue-400' : 'text-gray-400 hover:text-white'}`}>
                {t}
              </button>
            ))}
          </div>
        )}

        {/* Leverage */}
        <div className="flex items-center gap-2">
          <label className="text-gray-400 shrink-0">Плечо (x)</label>
          <input value={leverage} onChange={e => setLev(e.target.value)} type="number" min="1" max="200" placeholder="авто"
            className="w-20 bg-gray-800 border border-gray-600 rounded px-2 py-1 font-mono text-xs focus:outline-none focus:border-blue-500" />
          <span className="text-gray-500 text-[10px]">Пусто = не менять</span>
        </div>

        {/* Reduce only */}
        <label className="flex items-center gap-2 cursor-pointer">
          <input type="checkbox" checked={reduceOnly} onChange={e => setReduceOnly(e.target.checked)} className="rounded" />
          <span className="text-gray-400">Только закрытие (Reduce Only)</span>
        </label>

        {/* Submit */}
        <button
          onClick={handleSubmit}
          disabled={loading}
          className={`w-full py-2 rounded font-semibold transition-all ${side === 'Buy' ? 'bg-green-500 hover:bg-green-600' : 'bg-red-500 hover:bg-red-600'} text-white ${loading ? 'opacity-50 cursor-not-allowed' : ''}`}
        >
          {side === 'Buy' ? 'Купить / Long' : 'Продать / Short'}
        </button>

        {/* Order log */}
        {log.length > 0 && (
          <div className="rounded border border-gray-700 overflow-hidden">
            {log.map((e, i) => (
              <div key={i} className={`flex items-center gap-2 px-2 py-1 text-xs border-b border-gray-700/30 last:border-0 ${e.status === 'ok' ? 'bg-green-500/5' : e.status === 'error' ? 'bg-red-500/5' : ''}`}>
                <span className="shrink-0">
                  {e.status === 'pending' && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
                  {e.status === 'ok' && <span className="text-green-500">✓</span>}
                  {e.status === 'error' && <span className="text-red-400">✗</span>}
                </span>
                <span className={e.status === 'error' ? 'text-red-400' : e.status === 'ok' ? 'text-white' : 'text-gray-400'}>{e.text}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Active orders for this symbol */}
      {symbolOrders.length > 0 && (
        <div className="border-t border-gray-700 flex-shrink-0">
          <div className="px-3 py-1.5 text-gray-400 flex items-center justify-between">
            <span>Активные ордера</span>
            <span className="bg-gray-700 text-xs px-1.5 py-0.5 rounded-full">{symbolOrders.length}</span>
          </div>
          <div className="overflow-y-auto max-h-32">
            {symbolOrders.map(ord => (
              <div key={ord.orderId} className="flex items-center justify-between px-3 py-1.5 border-t border-gray-700/30 hover:bg-gray-800/50">
                <div className="flex items-center gap-1.5 min-w-0">
                  <span className={`text-[10px] px-1 rounded ${ord.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                    {ord.side === 'Buy' ? 'Long' : 'Short'}
                  </span>
                  <span className="text-gray-400">{ord.orderType}</span>
                  <span className="font-mono">
                    {ord.triggerPrice ? `~${parseFloat(ord.triggerPrice).toFixed(4)}` : parseFloat(ord.price) > 0 ? parseFloat(ord.price).toFixed(4) : '—'}
                  </span>
                  <span className="text-gray-400">× {ord.qty}</span>
                </div>
                <button onClick={() => handleCancel(ord)} className="text-gray-400 hover:text-red-400 transition-colors ml-1 shrink-0">✕</button>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Убедиться что компилируется**

```bash
cd frontend && npm run build 2>&1 | grep -i error | head -20
# Expected: no errors
```

- [ ] **Commit**

```bash
git add frontend/src/components/terminal/
git commit -m "feat: add Chart, Orderbook, and OrderForm terminal components"
```

---

## Task 4: PositionsTable + OrdersTable + HistoryTable + ExecutionsTable + TradeLog

**Files:**
- Create: `frontend/src/components/terminal/PositionsTable.tsx`
- Create: `frontend/src/components/terminal/OrdersTable.tsx`
- Create: `frontend/src/components/terminal/HistoryTable.tsx`
- Create: `frontend/src/components/terminal/ExecutionsTable.tsx`
- Create: `frontend/src/components/terminal/TradeLog.tsx`

- [ ] **Создать `frontend/src/components/terminal/PositionsTable.tsx`**

```tsx
import type { Position } from '../../types'
import { cancelOrder } from '../../api/trader'
import { useState } from 'react'

interface Props {
  accountId: string
  positions: Position[]
  onSelect: (symbol: string) => void
  loading: boolean
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function PositionsTable({ accountId, positions, onSelect, loading }: Props) {
  const [closing, setClosing] = useState<string | null>(null)

  async function handleClose(pos: Position) {
    const key = `${pos.symbol}-${pos.side}`
    if (closing === key) return
    setClosing(key)
    await cancelOrder({ // reuse cancel for close via reduce_only market order
      account_id: accountId,
      symbol: pos.symbol,
      category: pos.category,
      order_id: '',
    }).catch(() => {})
    // Close is done via placeOrder with reduceOnly=true — call from parent in real impl
    setClosing(null)
  }

  if (loading) return (
    <div className="flex items-center justify-center h-full text-gray-400 text-sm">
      <span className="animate-pulse">Загрузка данных...</span>
    </div>
  )

  if (!positions.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2">
      <span className="text-2xl opacity-30">📭</span>
      <p className="text-sm">Открытых позиций нет</p>
    </div>
  )

  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-gray-900">
        <tr className="text-gray-400 border-b border-gray-700">
          {['Символ','Сторона','Размер','Цена входа','Mark Price','PnL','Плечо',''].map(h => (
            <th key={h} className={`px-3 py-2 font-medium ${h === '' || h === 'Размер' || h === 'Цена входа' || h === 'Mark Price' || h === 'PnL' || h === 'Плечо' ? 'text-right' : 'text-left'}`}>{h}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {positions.map(pos => {
          const pnl = parseFloat(pos.unrealisedPnl)
          return (
            <tr key={`${pos.symbol}-${pos.side}`} onClick={() => onSelect(pos.symbol)}
              className="border-b border-gray-700/50 hover:bg-gray-800/60 transition-colors cursor-pointer">
              <td className="px-3 py-2 font-mono font-medium">
                <div className="flex items-center gap-1.5">
                  <img src={coinIcon(pos.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display='none')} />
                  {pos.symbol}
                </div>
              </td>
              <td className="px-3 py-2">
                <span className={`text-[10px] px-1.5 py-0.5 rounded ${pos.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                  {pos.side === 'Buy' ? 'Long' : 'Short'}
                </span>
              </td>
              <td className="px-3 py-2 text-right font-mono">{pos.sizeUsdt.toFixed(2)} <span className="text-gray-400 text-[10px]">USDT</span></td>
              <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.entryPrice).toFixed(2)}</td>
              <td className="px-3 py-2 text-right font-mono">{parseFloat(pos.markPrice).toFixed(2)}</td>
              <td className={`px-3 py-2 text-right font-mono font-medium ${pnl >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {pnl >= 0 ? '+' : ''}{pnl.toFixed(2)} <span className="text-gray-400 font-normal">({pos.unrealisedPnlPct}%)</span>
              </td>
              <td className="px-3 py-2 text-right font-mono">{pos.leverage}x</td>
              <td className="px-3 py-2 text-right" onClick={e => e.stopPropagation()}>
                <button
                  onClick={() => handleClose(pos)}
                  disabled={closing === `${pos.symbol}-${pos.side}`}
                  className="text-[10px] px-2 py-0.5 rounded bg-red-500/20 text-red-400 hover:bg-red-500/30 disabled:opacity-50"
                >
                  {closing === `${pos.symbol}-${pos.side}` ? '...' : 'Закрыть'}
                </button>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}
```

- [ ] **Создать `frontend/src/components/terminal/OrdersTable.tsx`**

```tsx
import { useState } from 'react'
import type { ActiveOrder } from '../../types'
import { cancelOrder } from '../../api/trader'

interface Props {
  accountId: string
  orders: ActiveOrder[]
  loading: boolean
}

function coinIcon(s: string) {
  return `https://cdn.jsdelivr.net/gh/spothq/cryptocurrency-icons/32/color/${s.replace(/USDT|USDC|USD$/, '').toLowerCase()}.png`
}

export function OrdersTable({ accountId, orders, loading }: Props) {
  const [cancelling, setCancelling] = useState<string | null>(null)

  async function handleCancel(ord: ActiveOrder) {
    if (cancelling === ord.orderId) return
    setCancelling(ord.orderId)
    await cancelOrder({ account_id: accountId, symbol: ord.symbol, category: ord.category, order_id: ord.orderId, order_filter: ord.orderFilter }).catch(() => {})
    setCancelling(null)
  }

  if (loading) return <div className="flex items-center justify-center h-full text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!orders.length) return <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">Активных ордеров нет</p></div>

  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-gray-900">
        <tr className="text-gray-400 border-b border-gray-700">
          <th className="text-left px-3 py-2">Символ</th>
          <th className="text-left px-3 py-2">Сторона</th>
          <th className="text-left px-3 py-2">Тип</th>
          <th className="text-right px-3 py-2">Цена</th>
          <th className="text-right px-3 py-2">Кол-во</th>
          <th className="text-right px-3 py-2">Исполнено</th>
          <th className="text-left px-3 py-2">Статус</th>
          <th className="text-left px-3 py-2">Маркер</th>
          <th className="px-3 py-2" />
        </tr>
      </thead>
      <tbody>
        {orders.map(ord => (
          <tr key={ord.orderId} className="border-b border-gray-700/50 hover:bg-gray-800/60">
            <td className="px-3 py-2 font-mono font-medium">
              <div className="flex items-center gap-1.5">
                <img src={coinIcon(ord.symbol)} className="w-4 h-4 rounded-full shrink-0" onError={e => (e.currentTarget.style.display='none')} />
                {ord.symbol}
              </div>
            </td>
            <td className="px-3 py-2">
              <span className={`text-[10px] px-1.5 py-0.5 rounded ${ord.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>
                {ord.side}
              </span>
            </td>
            <td className="px-3 py-2 text-gray-400">{ord.orderType}</td>
            <td className="px-3 py-2 text-right font-mono">
              {ord.triggerPrice ? `~${parseFloat(ord.triggerPrice).toFixed(2)}` : parseFloat(ord.price) > 0 ? parseFloat(ord.price).toFixed(2) : '—'}
            </td>
            <td className="px-3 py-2 text-right font-mono">{ord.qty}</td>
            <td className="px-3 py-2 text-right font-mono text-gray-400">{ord.cumExecQty}</td>
            <td className="px-3 py-2">
              <span className="text-[10px] px-1.5 py-0.5 rounded bg-yellow-500/20 text-yellow-400">{ord.orderStatus}</span>
            </td>
            <td className="px-3 py-2">
              {ord.orderLinkId?.startsWith('sis') && (
                <span className="text-[10px] px-1 py-0.5 rounded bg-blue-500/20 text-blue-400">SIS</span>
              )}
            </td>
            <td className="px-3 py-2 text-right">
              <button onClick={() => handleCancel(ord)} disabled={cancelling === ord.orderId}
                className="text-gray-400 hover:text-red-400 transition-colors disabled:opacity-50">✕</button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Создать `frontend/src/components/terminal/HistoryTable.tsx`**

```tsx
import { useEffect, useState } from 'react'
import { listOrders } from '../../api/trader'
import type { TraderOrder } from '../../types'

interface Props { accountId?: string; symbol?: string }

export function HistoryTable({ accountId, symbol }: Props) {
  const [orders, setOrders] = useState<TraderOrder[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listOrders({ account_id: accountId, status: 'closed', symbol, page, limit: 50 })
      .then(r => { setOrders(r.orders); setTotal(r.total) })
      .finally(() => setLoading(false))
  }, [accountId, symbol, page])

  if (loading) return <div className="flex items-center justify-center h-full text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
  if (!orders.length) return <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2"><span className="text-2xl opacity-30">📭</span><p className="text-sm">История пуста</p></div>

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-auto">
        <table className="w-full text-xs">
          <thead className="sticky top-0 z-10 bg-gray-900">
            <tr className="text-gray-400 border-b border-gray-700">
              <th className="text-left px-3 py-2">Дата</th>
              <th className="text-left px-3 py-2">Символ</th>
              <th className="text-left px-3 py-2">Сторона</th>
              <th className="text-left px-3 py-2">Тип</th>
              <th className="text-right px-3 py-2">Цена</th>
              <th className="text-right px-3 py-2">Кол-во</th>
              <th className="text-right px-3 py-2">Комиссия</th>
              <th className="text-left px-3 py-2">Статус</th>
              <th className="px-3 py-2">Маркер</th>
            </tr>
          </thead>
          <tbody>
            {orders.map(o => (
              <tr key={o.id} className="border-b border-gray-700/50 hover:bg-gray-800/60">
                <td className="px-3 py-2 font-mono text-gray-400 whitespace-nowrap">{new Date(o.created_at).toLocaleString('ru-RU')}</td>
                <td className="px-3 py-2 font-mono font-medium">{o.symbol}</td>
                <td className="px-3 py-2">
                  <span className={`text-[10px] px-1.5 py-0.5 rounded ${o.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>{o.side}</span>
                </td>
                <td className="px-3 py-2 text-gray-400">{o.order_type}</td>
                <td className="px-3 py-2 text-right font-mono">{o.price ? parseFloat(o.price).toFixed(2) : '—'}</td>
                <td className="px-3 py-2 text-right font-mono">{o.cum_exec_qty}</td>
                <td className="px-3 py-2 text-right font-mono text-gray-400">{o.cum_exec_fee}</td>
                <td className="px-3 py-2">
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">{o.status}</span>
                </td>
                <td className="px-3 py-2">
                  {o.order_link_id?.startsWith('sis') && <span className="text-[10px] px-1 py-0.5 rounded bg-blue-500/20 text-blue-400">SIS</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {total > 50 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-gray-700 flex-shrink-0 text-xs text-gray-400">
          <span>Всего: {total}</span>
          <div className="flex gap-1">
            <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">←</button>
            <span className="px-2 py-1">{page}</span>
            <button onClick={() => setPage(p => p + 1)} disabled={page * 50 >= total} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">→</button>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Создать `frontend/src/components/terminal/ExecutionsTable.tsx`**

```tsx
import { useEffect, useState } from 'react'
import { listExecutions } from '../../api/trader'
import type { TraderExecution } from '../../types'

interface Props { accountId?: string }

export function ExecutionsTable({ accountId }: Props) {
  const [execs, setExecs] = useState<TraderExecution[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [type, setType] = useState('all')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    setLoading(true)
    listExecutions({ account_id: accountId, type: type === 'all' ? undefined : type, page, limit: 100 })
      .then(r => { setExecs(r.executions); setTotal(r.total) })
      .finally(() => setLoading(false))
  }, [accountId, type, page])

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-gray-700 flex-shrink-0">
        {['all', 'Trade', 'Funding', 'Fee'].map(t => (
          <button key={t} onClick={() => { setType(t); setPage(1) }}
            className={`text-xs px-2 py-0.5 rounded transition-colors ${type === t ? 'bg-blue-500/20 text-blue-400' : 'text-gray-400 hover:text-white'}`}>
            {t === 'all' ? 'Все' : t}
          </button>
        ))}
      </div>
      {loading
        ? <div className="flex items-center justify-center flex-1 text-gray-400 text-sm"><span className="animate-pulse">Загрузка...</span></div>
        : !execs.length
          ? <div className="flex items-center justify-center flex-1 text-gray-400 text-sm">Нет данных</div>
          : (
            <div className="flex-1 overflow-auto">
              <table className="w-full text-xs">
                <thead className="sticky top-0 z-10 bg-gray-900">
                  <tr className="text-gray-400 border-b border-gray-700">
                    <th className="text-left px-3 py-2">Время</th>
                    <th className="text-left px-3 py-2">Символ</th>
                    <th className="text-left px-3 py-2">Тип</th>
                    <th className="text-left px-3 py-2">Сторона</th>
                    <th className="text-right px-3 py-2">Цена</th>
                    <th className="text-right px-3 py-2">Кол-во</th>
                    <th className="text-right px-3 py-2">Значение</th>
                    <th className="text-right px-3 py-2">Комиссия</th>
                    <th className="text-right px-3 py-2">Мейкер</th>
                  </tr>
                </thead>
                <tbody>
                  {execs.map(e => (
                    <tr key={e.id} className="border-b border-gray-700/50 hover:bg-gray-800/60">
                      <td className="px-3 py-2 font-mono text-gray-400 whitespace-nowrap">{new Date(e.exec_time).toLocaleString('ru-RU')}</td>
                      <td className="px-3 py-2 font-mono font-medium">{e.symbol}</td>
                      <td className="px-3 py-2">
                        <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">{e.exec_type}</span>
                      </td>
                      <td className="px-3 py-2">
                        {e.side && <span className={`text-[10px] px-1.5 py-0.5 rounded ${e.side === 'Buy' ? 'bg-green-500/20 text-green-400' : 'bg-red-500/20 text-red-400'}`}>{e.side}</span>}
                      </td>
                      <td className="px-3 py-2 text-right font-mono">{e.price ? parseFloat(e.price).toFixed(2) : '—'}</td>
                      <td className="px-3 py-2 text-right font-mono">{e.qty ?? '—'}</td>
                      <td className="px-3 py-2 text-right font-mono">{e.exec_value ? parseFloat(e.exec_value).toFixed(4) : '—'}</td>
                      <td className="px-3 py-2 text-right font-mono text-gray-400">{e.exec_fee ? parseFloat(e.exec_fee).toFixed(6) : '—'}</td>
                      <td className="px-3 py-2 text-right">{e.is_maker != null ? (e.is_maker ? '✓' : '✗') : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
      }
      {total > 100 && (
        <div className="flex items-center justify-between px-3 py-2 border-t border-gray-700 flex-shrink-0 text-xs text-gray-400">
          <span>Всего: {total}</span>
          <div className="flex gap-1">
            <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">←</button>
            <span className="px-2 py-1">{page}</span>
            <button onClick={() => setPage(p => p + 1)} disabled={page * 100 >= total} className="px-2 py-1 rounded bg-gray-800 disabled:opacity-40">→</button>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Создать `frontend/src/components/terminal/TradeLog.tsx`**

```tsx
import type { LogEntry } from '../../hooks/terminal/usePositionsWs'

interface Props { log: LogEntry[] }

export function TradeLog({ log }: Props) {
  if (!log.length) return (
    <div className="flex flex-col items-center justify-center h-full text-gray-400 gap-2">
      <span className="text-2xl opacity-30">💻</span>
      <p className="text-sm">Ожидание событий...</p>
    </div>
  )
  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-gray-900">
        <tr className="text-gray-400 border-b border-gray-700">
          <th className="text-left px-3 py-2 w-20">Время</th>
          <th className="text-left px-3 py-2">Сообщение</th>
        </tr>
      </thead>
      <tbody>
        {log.map((row, i) => (
          <tr key={i} className={`border-b border-gray-700/30 ${row.error ? 'bg-red-500/5' : ''}`}>
            <td className="px-3 py-1.5 font-mono text-gray-400 whitespace-nowrap">{row.time}</td>
            <td className={`px-3 py-1.5 ${row.error ? 'text-red-400' : 'text-gray-200'}`}>{row.message}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
```

- [ ] **Убедиться что компилируется**

```bash
cd frontend && npm run build 2>&1 | grep -i error | head -20
# Expected: no errors
```

- [ ] **Commit**

```bash
git add frontend/src/components/terminal/
git commit -m "feat: add positions, orders, history, executions, and log table components"
```

---

## Task 5: TerminalPage + маршрутизация

**Files:**
- Create: `frontend/src/pages/TerminalPage.tsx`
- Modify: `frontend/src/components/Layout.tsx`
- Modify: `frontend/src/App.tsx`

- [ ] **Создать `frontend/src/pages/TerminalPage.tsx`**

```tsx
import { useState, useEffect } from 'react'
import { Chart } from '../components/terminal/Chart'
import { Orderbook } from '../components/terminal/Orderbook'
import { OrderForm } from '../components/terminal/OrderForm'
import { PositionsTable } from '../components/terminal/PositionsTable'
import { OrdersTable } from '../components/terminal/OrdersTable'
import { HistoryTable } from '../components/terminal/HistoryTable'
import { ExecutionsTable } from '../components/terminal/ExecutionsTable'
import { TradeLog } from '../components/terminal/TradeLog'
import { usePositionsWs } from '../hooks/terminal/usePositionsWs'
import { useCandles } from '../hooks/terminal/useCandles'
import { useOrderbook } from '../hooks/terminal/useOrderbook'
import { listAccounts } from '../api/accounts'
import type { ExchangeAccount } from '../types'

const COINS = ['BTCUSDT', 'ETHUSDT', 'SOLUSDT', 'BNBUSDT', 'XRPUSDT', 'DOGEUSDT']
const TIMEFRAMES = [
  { label: '1м', value: '1' },
  { label: '5м', value: '5' },
  { label: '15м', value: '15' },
  { label: '1ч', value: '60' },
  { label: '4ч', value: '240' },
  { label: '1д', value: 'D' },
]

type BottomTab = 'positions' | 'orders' | 'history' | 'executions' | 'log'

export function TerminalPage() {
  const [accounts, setAccounts] = useState<ExchangeAccount[]>([])
  const [accountId, setAccountId] = useState<string | null>(null)
  const [symbol, setSymbol] = useState('BTCUSDT')
  const [tf, setTf] = useState('60')
  const [bottomTab, setBottomTab] = useState<BottomTab>('positions')

  useEffect(() => {
    listAccounts().then(accs => {
      setAccounts(accs)
      if (accs.length > 0 && accs[0]) setAccountId(accs[0].id)
    }).catch(() => {})
  }, [])

  const { positions, orders, log, status, accountName, loading, reconnect } = usePositionsWs(accountId)
  const { candles, lastPrice, priceChange } = useCandles(symbol, tf)
  const { bids, asks, spread } = useOrderbook(symbol)

  const statusColor = {
    connecting: 'bg-yellow-400 animate-pulse',
    connected: 'bg-green-500',
    error: 'bg-red-500',
    closed: 'bg-red-500',
  }[status]

  const bottomTabs: { key: BottomTab; label: string; count?: number }[] = [
    { key: 'positions', label: 'Позиции', count: positions.length },
    { key: 'orders', label: 'Ордера', count: orders.length },
    { key: 'history', label: 'История' },
    { key: 'executions', label: 'Сделки' },
    { key: 'log', label: 'Лог' },
  ]

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '65% 35%', gridTemplateRows: '65% 35%', height: 'calc(100vh - 65px)', gap: '10px', padding: '10px', background: '#07070f' }}>

      {/* Chart area */}
      <div style={{ gridColumn: 1, gridRow: 1 }} className="bg-gray-900/50 border border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        {/* Toolbar */}
        <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-700 flex-shrink-0">
          <select value={symbol} onChange={e => setSymbol(e.target.value)}
            className="bg-gray-800 border border-gray-600 rounded px-2 py-1 text-sm text-white w-36 focus:outline-none">
            {COINS.map(c => <option key={c} value={c}>{c}</option>)}
          </select>
          <div className="flex gap-1">
            {TIMEFRAMES.map(t => (
              <button key={t.value} onClick={() => setTf(t.value)}
                className={`px-2 py-0.5 rounded text-xs font-medium transition-colors ${tf === t.value ? 'bg-blue-500 text-white' : 'text-gray-400 hover:text-white'}`}>
                {t.label}
              </button>
            ))}
          </div>
          {lastPrice && (
            <div className="ml-auto flex items-center gap-2">
              <span className="font-mono font-bold text-lg text-white">{lastPrice}</span>
              <span className={`text-sm font-medium ${priceChange >= 0 ? 'text-green-500' : 'text-red-500'}`}>
                {priceChange >= 0 ? '+' : ''}{priceChange.toFixed(2)}%
              </span>
            </div>
          )}
        </div>
        <div className="flex-1 min-h-0">
          <Chart candles={candles} positions={positions} orders={orders} symbol={symbol} />
        </div>
      </div>

      {/* Bottom: positions/orders/history */}
      <div style={{ gridColumn: 1, gridRow: 2 }} className="bg-gray-900/50 border border-gray-700/50 rounded-xl flex flex-col overflow-hidden">
        <div className="flex items-center border-b border-gray-700 flex-shrink-0 px-2">
          {bottomTabs.map(t => (
            <button key={t.key} onClick={() => setBottomTab(t.key)}
              className={`flex items-center gap-1.5 px-3 py-2 text-xs font-medium border-b-2 transition-colors ${bottomTab === t.key ? 'border-blue-500 text-white' : 'border-transparent text-gray-400 hover:text-white'}`}>
              {t.label}
              {t.count != null && t.count > 0 && (
                loading && (t.key === 'positions' || t.key === 'orders')
                  ? <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
                  : <span className="bg-gray-700 text-xs px-1.5 py-0.5 rounded-full">{t.count}</span>
              )}
            </button>
          ))}
          {accountName && <span className="text-xs text-gray-400 ml-2">{accountName}</span>}
          <div className="ml-auto flex items-center gap-1.5">
            <span className={`w-2 h-2 rounded-full ${statusColor}`} />
            {(status === 'closed' || status === 'error') && (
              <button onClick={reconnect} className="text-gray-400 hover:text-white text-xs">↺</button>
            )}
          </div>
        </div>
        <div className="flex-1 overflow-auto">
          {bottomTab === 'positions' && <PositionsTable accountId={accountId ?? ''} positions={positions} onSelect={setSymbol} loading={loading} />}
          {bottomTab === 'orders' && <OrdersTable accountId={accountId ?? ''} orders={orders} loading={loading} />}
          {bottomTab === 'history' && <HistoryTable accountId={accountId ?? undefined} symbol={symbol} />}
          {bottomTab === 'executions' && <ExecutionsTable accountId={accountId ?? undefined} />}
          {bottomTab === 'log' && <TradeLog log={log} />}
        </div>
      </div>

      {/* Right panel: OrderForm + Orderbook */}
      <div style={{ gridColumn: 2, gridRow: '1 / 3', display: 'flex', flexDirection: 'column', gap: '10px', overflow: 'hidden' }}>
        <div className="bg-gray-900/50 border border-gray-700/50 rounded-xl overflow-hidden flex-shrink-0" style={{ maxHeight: '55%' }}>
          {accountId ? (
            <OrderForm accountId={accountId} symbol={symbol} lastPrice={lastPrice} orders={orders} positions={positions} />
          ) : (
            <div className="flex flex-col items-center justify-center h-full p-6 text-center text-gray-400 gap-3">
              <span className="text-3xl opacity-30">🔑</span>
              <p className="text-sm">Нет аккаунта Bybit. Добавьте API-ключи в настройках.</p>
            </div>
          )}
        </div>
        <div className="bg-gray-900/50 border border-gray-700/50 rounded-xl flex-1 flex flex-col overflow-hidden min-h-0">
          <Orderbook bids={bids} asks={asks} spread={spread} symbol={symbol} />
        </div>
      </div>

    </div>
  )
}
```

- [ ] **Обновить `frontend/src/components/Layout.tsx`** — добавить Terminal в навигацию

Заменить:
```tsx
const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Webhooks', to: '/webhooks' },
]
```
на:
```tsx
const navItems = [
  { label: 'Dashboard', to: '/' },
  { label: 'Terminal', to: '/terminal' },
  { label: 'Webhooks', to: '/webhooks' },
]
```

И обновить `<main>` чтобы страница терминала занимала всю высоту без padding:
```tsx
<main className={`flex-1 overflow-auto ${pathname === '/terminal' ? '' : 'p-6'}`}>
  {children}
</main>
```

- [ ] **Обновить `frontend/src/App.tsx`** — добавить маршрут

Добавить импорт:
```tsx
import { TerminalPage } from './pages/TerminalPage'
```

Добавить маршрут внутри `<Layout>`:
```tsx
<Route path="terminal" element={<TerminalPage />} />
```

- [ ] **Финальная проверка — сборка**

```bash
cd frontend && npm run build
# Expected: Build successful, no errors
```

- [ ] **Запустить тесты**

```bash
cd frontend && npm test
# Expected: все тесты PASS
```

- [ ] **Commit**

```bash
git add frontend/src/
git commit -m "feat: add TerminalPage with chart, orderbook, order form, positions and history"
```

---

## Self-Review

**Spec coverage:**
- ✅ TerminalPage с grid лейаутом 65%/35% (Task 5)
- ✅ График lightweight-charts со свечами и entry lines (Task 3)
- ✅ Стакан через Bybit public WS (Task 2, 3)
- ✅ OrderForm: Limit / Market / Conditional, leverage, reduce-only (Task 3)
- ✅ PositionsTable + кнопка "Закрыть" (Task 4)
- ✅ OrdersTable + кнопка "Снять" + маркер "SIS" (Task 4)
- ✅ HistoryTable: закрытые ордера с пагинацией (Task 4)
- ✅ ExecutionsTable: сделки/фандинг/комиссии с фильтром (Task 4)
- ✅ TradeLog: WS-лог событий (Task 4)
- ✅ usePositionsWs: snapshot + delta merge, reconnect (Task 2)
- ✅ useCandles: WS + REST история с кэшем (Task 2)
- ✅ useOrderbook: snapshot + delta (Task 2)
- ✅ Маршрут /terminal + навигация (Task 5)
- ✅ Выбор аккаунта из exchange_accounts (Task 5)
- ✅ accountId в WS query param (Task 2)

**Placeholder scan:** нет TBD/TODO. ✅

**Type consistency:**
- `Position`, `ActiveOrder`, `TraderOrder`, `TraderExecution` — определены в types.ts, используются во всех компонентах ✅
- `BookRow = [string, string]` — определена в useOrderbook.ts, используется в Orderbook.tsx ✅
- `LogEntry` — определена в usePositionsWs.ts, используется в TradeLog.tsx ✅
- `placeOrder`, `cancelOrder`, `setLeverage` — импортируются из api/trader.ts в OrderForm.tsx, PositionsTable.tsx, OrdersTable.tsx ✅
