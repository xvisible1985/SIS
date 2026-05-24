import { useEffect, useMemo, useRef, useState } from 'react'
import { createChart, CandlestickSeries, createSeriesMarkers, type IChartApi, type ISeriesApi, ColorType } from 'lightweight-charts'
import type { Candle } from '../../hooks/terminal/useCandles'
import type { Position, ActiveOrder, ChartExecution, StrategyLevel } from '../../types'

export interface ChartOverlaySettings {
  showPositions: boolean
  showPlacedOrders: boolean
  showTakenOrders: boolean
  showTakeProfit: boolean
  showStopLoss: boolean
  bothDirections: boolean
}

interface Props {
  candles: Candle[]
  candleSymbol: string | null
  positions: Position[]
  orders: ActiveOrder[]
  executions?: ChartExecution[]
  symbol: string
  lastPrice: string | null
  onLoadMore: (before: number) => Promise<void>
  overlaySettings?: ChartOverlaySettings
  strategyDir?: 'long' | 'short' | null
  stratIdShort?: string | null
  currentCycleNum?: number | null
  strategyLevels?: StrategyLevel[]
  tickerPrices?: Map<string, number>
}

function parseOrderLabel(linkId: string, side?: string): string {
  if (!linkId) return ''
  if (/^(SIS_STR|STP)-[a-f0-9]+-tp(-\d+)+$/.test(linkId))
    return side === 'Sell' ? 'TP_LONG' : side === 'Buy' ? 'TP_SHORT' : 'TP'
  if (/^(SIS_STR|STP)-[a-f0-9]+-sl(-\d+)+$/.test(linkId))
    return side === 'Sell' ? 'SL_LONG' : side === 'Buy' ? 'SL_SHORT' : 'SL'
  const m = linkId.match(/^(SIS_STR|STR)-[a-f0-9]+-\d+-(\d+)(?:-\d+)?$/)
  return m ? `L${m[2]}` : ''
}

function isTPLinkId(linkId?: string): boolean {
  return !!linkId && /^(SIS_STR|STP)-[a-f0-9]+-tp(-\d+)+$/.test(linkId)
}

// Extracts cycle number from SIS order link IDs. Returns null for unknown formats.
function extractCycleNum(linkId?: string): number | null {
  if (!linkId) return null
  // SIS_STR-{id}-tp-{cycle}[-{seq}] or SIS_STR-{id}-sl-{cycle}[-{seq}]
  let m = linkId.match(/^(?:SIS_STR|STP)-[a-f0-9]+-(?:tp|sl)-(\d+)/)
  if (m) return parseInt(m[1])
  // SIS_STR-{id}-{cycle}-{level}[-{repriceGen}]
  m = linkId.match(/^(?:SIS_STR|STR)-[a-f0-9]+-(\d+)-\d+(?:-\d+)?$/)
  if (m) return parseInt(m[1])
  return null
}

function pctFromPrice(orderPrice: number, currentPrice: number): string {
  const pct = (orderPrice - currentPrice) / currentPrice * 100
  return `${pct >= 0 ? '+' : ''}${pct.toFixed(2)}%`
}

function autoPrecision(price: number): { precision: number; minMove: number } {
  if (price >= 1000) return { precision: 2, minMove: 0.01 }
  if (price >= 10)   return { precision: 3, minMove: 0.001 }
  if (price >= 1)    return { precision: 4, minMove: 0.0001 }
  if (price >= 0.1)  return { precision: 5, minMove: 0.00001 }
  if (price >= 0.01) return { precision: 6, minMove: 0.000001 }
  if (price >= 0.001)return { precision: 7, minMove: 0.0000001 }
  return { precision: 8, minMove: 0.00000001 }
}

// Returns true when a link ID belongs to a different SIS strategy (not the current one).
// Used to hide ghost orders/executions from deleted strategies that share the same symbol.
function isOtherStrategyLinkId(linkId: string | undefined, stratIdShort: string | null | undefined): boolean {
  if (!linkId || !stratIdShort) return false
  return /^(?:SIS_STR|STP|STR)-[a-f0-9]/.test(linkId) && !linkId.includes(stratIdShort)
}

export function Chart({ candles, candleSymbol, positions, orders, executions, symbol, lastPrice, onLoadMore, overlaySettings, strategyDir, stratIdShort, currentCycleNum, strategyLevels, tickerPrices }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const overlayRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersPluginRef = useRef<any>(null)
  const priceLines = useRef<any[]>([])
  const currentPriceLineRef = useRef<any>(null)
  const loadedSymbolRef = useRef<string | null>(null)
  const prevFirstTimeRef = useRef<number | null>(null)
  const tickerRef = useRef<Map<string, number> | undefined>(tickerPrices)
  useEffect(() => { tickerRef.current = tickerPrices }, [tickerPrices])
  const onLoadMoreRef = useRef(onLoadMore)
  const oldestTimeRef = useRef<number | null>(null)
  const loadingMoreRef = useRef(false)
  const lastLoadMoreRef = useRef(0)
  const lastPrecisionRef = useRef(-1)
  const [afterSetData, setAfterSetData] = useState(0)

  // Infer direction from live orders or open positions when strategyDir hasn't loaded yet (page refresh).
  const effectiveDir = useMemo(() => {
    if (strategyDir) return strategyDir
    for (const o of orders) {
      const lbl = parseOrderLabel(o.orderLinkId, o.side)
      if (lbl?.includes('_LONG')) return 'long'
      if (lbl?.includes('_SHORT')) return 'short'
    }
    for (const p of positions) {
      if (p.symbol !== symbol) continue
      if (p.side === 'Buy') return 'long'
      if (p.side === 'Sell') return 'short'
    }
    return null
  }, [strategyDir, orders, positions, symbol])

  useEffect(() => { onLoadMoreRef.current = onLoadMore }, [onLoadMore])

  useEffect(() => {
    oldestTimeRef.current = candles[0]?.time ?? null
  }, [candles])

  // Init chart once
  useEffect(() => {
    if (!containerRef.current) return

    const themeColors = (dark: boolean) => ({
      layout: {
        background: { type: ColorType.Solid, color: dark ? '#0a0a0f' : '#ffffff' },
        textColor: dark ? '#9ca3af' : '#374151',
      },
      grid: {
        vertLines: { color: dark ? '#1a1a2e' : '#e5e7eb' },
        horzLines: { color: dark ? '#1a1a2e' : '#e5e7eb' },
      },
      rightPriceScale: { borderColor: dark ? '#2d2d3d' : '#d1d5db' },
      timeScale: { borderColor: dark ? '#2d2d3d' : '#d1d5db', timeVisible: true },
    })

    const dark = document.documentElement.classList.contains('dark')
    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: containerRef.current.clientHeight,
      ...themeColors(dark),
      crosshair: { mode: 1 },
      localization: {
        locale: 'ru-RU',
        timeFormatter: (time: number) => {
          const d = new Date(time * 1000)
          return d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
        },
      },
    })
    chartRef.current = chart

    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#00DC82', downColor: '#ef4444',
      borderUpColor: '#00DC82', borderDownColor: '#ef4444',
      wickUpColor: '#00DC82', wickDownColor: '#ef4444',
      lastValueVisible: false,
      priceLineVisible: false,
    })
    seriesRef.current = series
    markersPluginRef.current = createSeriesMarkers(series)

    // Subscribe to visible range changes to load historical data
    chart.timeScale().subscribeVisibleLogicalRangeChange((range) => {
      if (!range || loadingMoreRef.current) return
      if (Date.now() - lastLoadMoreRef.current < 1500) return
      if (range.from < 10 && oldestTimeRef.current) {
        lastLoadMoreRef.current = Date.now()
        loadingMoreRef.current = true
        onLoadMoreRef.current(oldestTimeRef.current).finally(() => {
          loadingMoreRef.current = false
        })
      }
    })

    const themeObserver = new MutationObserver(() => {
      chart.applyOptions(themeColors(document.documentElement.classList.contains('dark')))
    })
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

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
      themeObserver.disconnect()
      markersPluginRef.current = null
      chart.remove()
      loadedSymbolRef.current = null
      prevFirstTimeRef.current = null
    }
  }, [])

  // Clear stale candles immediately when symbol changes (before new candles arrive)
  useEffect(() => {
    if (!seriesRef.current) return
    if (loadedSymbolRef.current && loadedSymbolRef.current.split(':')[0] !== symbol) {
      seriesRef.current.setData([])
      loadedSymbolRef.current = null
      prevFirstTimeRef.current = null
    }
  }, [symbol])

  // Candles: full setData on symbol change, prepend for history, live update() for ticks
  useEffect(() => {
    if (!seriesRef.current || !candles.length || !candleSymbol || candleSymbol.split(':')[0] !== symbol) return

    const isNewSymbol = loadedSymbolRef.current !== candleSymbol
    const isPrepend = !isNewSymbol
      && prevFirstTimeRef.current !== null
      && candles[0].time < prevFirstTimeRef.current

    prevFirstTimeRef.current = candles[0].time

    if (isNewSymbol) {
      seriesRef.current.setData(candles as any)
      chartRef.current?.priceScale('right').applyOptions({ autoScale: true })
      chartRef.current?.timeScale().fitContent()
      chartRef.current?.timeScale().applyOptions({ rightOffset: Math.round(candles.length * 0.2) })
      loadedSymbolRef.current = candleSymbol
      lastLoadMoreRef.current = Date.now() + 2000  // suppress auto-load right after initial load
      setAfterSetData(n => n + 1)
    } else if (isPrepend) {
      // Preserve viewport when prepending historical candles
      const visibleRange = chartRef.current?.timeScale().getVisibleRange()
      seriesRef.current.setData(candles as any)
      setAfterSetData(n => n + 1)
      if (visibleRange) {
        requestAnimationFrame(() => {
          chartRef.current?.timeScale().setVisibleRange(visibleRange as any)
        })
      }
    } else {
      // Live tick
      const last = candles[candles.length - 1]
      if (last) seriesRef.current.update(last as any)
    }
  }, [candles, candleSymbol, symbol])

  // Current price line — updates on every tick
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return
    if (currentPriceLineRef.current) {
      try { series.removePriceLine(currentPriceLineRef.current) } catch {}
      currentPriceLineRef.current = null
    }
    const price = lastPrice ? parseFloat(lastPrice) : 0
    if (price > 0) {
      currentPriceLineRef.current = series.createPriceLine({
        price,
        color: '#64748b',
        lineWidth: 1,
        lineStyle: 2,
        axisLabelVisible: true,
        title: '',
      })
    }
  }, [lastPrice, afterSetData])

  // Auto price precision — updates series priceFormat when the price magnitude changes
  useEffect(() => {
    const series = seriesRef.current
    if (!series || !lastPrice) return
    const price = parseFloat(lastPrice)
    if (!price || price <= 0) return
    const { precision, minMove } = autoPrecision(price)
    if (precision === lastPrecisionRef.current) return
    lastPrecisionRef.current = precision
    series.applyOptions({ priceFormat: { type: 'price', precision, minMove } })
  }, [lastPrice])

  // Price lines — positions and orders
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return

    for (const line of priceLines.current) {
      try { series.removePriceLine(line) } catch {}
    }
    priceLines.current = []

    const currentPrice = lastPrice ? parseFloat(lastPrice) : 0
    const dirFilter = overlaySettings && !overlaySettings.bothDirections && effectiveDir ? effectiveDir : null

    for (const pos of positions.filter(p => {
      if (p.symbol !== symbol) return false
      if (overlaySettings && !overlaySettings.showPositions) return false
      if (dirFilter) return dirFilter === 'long' ? p.side === 'Buy' : p.side === 'Sell'
      return true
    })) {
      const price = parseFloat(pos.entryPrice)
      if (!price) continue
      const isLong = pos.side === 'Buy'
      priceLines.current.push(series.createPriceLine({
        price,
        color: isLong ? '#059669' : '#dc2626',
        lineWidth: 2,
        lineStyle: 0,
        axisLabelVisible: true,
        title: '',
      }))
    }

    const virtualLevelIdxs = new Set(
      (strategyLevels ?? [])
        .filter(l => l.status === 'pending' || l.force_virtual)
        .map(l => l.level_idx)
    )
    const levelSlotMap = new Map<number, number>(
      (strategyLevels ?? [])
        .filter(l => l.slot != null)
        .map(l => [l.level_idx, l.slot as number])
    )
    const resolveLabel = (lbl: string): string => {
      const m = lbl.match(/^L(\d+)$/)
      if (!m) return lbl
      const slot = levelSlotMap.get(parseInt(m[1]))
      return slot != null ? `L(${slot})` : lbl
    }

    for (const ord of orders.filter(o => {
      if (o.symbol !== symbol) return false
      // Hide orders belonging to a different strategy (ghost orders from deleted strategies).
      if (isOtherStrategyLinkId(o.orderLinkId, stratIdShort)) return false
      // Show only orders from the current cycle — hide any other cycle (old or phantom).
      if (stratIdShort && currentCycleNum != null && o.orderLinkId?.includes(stratIdShort)) {
        const ordCycle = extractCycleNum(o.orderLinkId)
        if (ordCycle !== null && ordCycle !== currentCycleNum) return false
      }
      const lbl = parseOrderLabel(o.orderLinkId, o.side)
      // Skip exchange orders whose level is tracked as virtual in strategyLevels
      const lvMatch = lbl.match(/^L(\d+)/)
      if (lvMatch && virtualLevelIdxs.has(parseInt(lvMatch[1]))) return false
      const isTP = lbl.startsWith('TP')
      const isSL = lbl.startsWith('SL')
      if (overlaySettings) {
        if (isTP && !overlaySettings.showTakeProfit) return false
        if (isSL && !overlaySettings.showStopLoss) return false
        if (!isTP && !isSL && !overlaySettings.showPlacedOrders) return false
      }
      if (dirFilter) {
        if (lbl.includes('_LONG')) return dirFilter === 'long'
        if (lbl.includes('_SHORT')) return dirFilter === 'short'
        return dirFilter === 'long' ? o.side === 'Buy' : o.side === 'Sell'
      }
      return true
    })) {
      const isLong = ord.side === 'Buy'
      const label = resolveLabel(parseOrderLabel(ord.orderLinkId, ord.side))
      const color = label.startsWith('TP') ? '#5b8cff' : label.startsWith('SL') ? '#f59e0b' : (isLong ? '#34d399' : '#f87171')
      if (ord.triggerPrice && parseFloat(ord.triggerPrice) > 0) {
        const p = parseFloat(ord.triggerPrice)
        const usdt = (parseFloat(ord.qty) * p).toFixed(0)
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice)}` : ''
        const prefix = label || (isLong ? 'Buy' : 'Sell')
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 1,
          axisLabelVisible: true,
          title: `${prefix}${pct} ${usdt}$ [B]`,
        }))
      }
      if (ord.price && parseFloat(ord.price) > 0) {
        const p = parseFloat(ord.price)
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice)}` : ''
        const prefix = label || (isLong ? 'Buy' : 'Sell')
        const usdt = (parseFloat(ord.qty) * p).toFixed(0)
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 2,
          axisLabelVisible: true,
          title: `${prefix}${pct} ${usdt}$ [B]`,
        }))
      }
    }
    // Virtual levels — pending (matrix) or force_virtual (regular strategies)
    if (!overlaySettings || overlaySettings.showPlacedOrders) {
      for (const lv of (strategyLevels ?? []).filter(l => (l.status === 'pending' || l.force_virtual) && l.target_price > 0)) {
        if (dirFilter) {
          const lvLong = lv.side === 'Buy'
          if (dirFilter === 'long' && !lvLong) continue
          if (dirFilter === 'short' && lvLong) continue
        }
        const p = lv.target_price
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice)}` : ''
        const usdt = lv.size_usdt > 0 ? ` ${lv.size_usdt.toFixed(0)}$` : ''
        const isLong = lv.side === 'Buy'
        const color = isLong ? '#6ee7b7' : '#fca5a5'
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 3,
          axisLabelVisible: true,
          title: `${lv.slot != null ? `L(${lv.slot})` : `L${lv.level_idx}`}${pct}${usdt} [V]`,
        }))
      }
    }

    // Execution price lines — thin solid lines for filled orders
    if (!overlaySettings || overlaySettings.showTakenOrders) {
      const relevantExecs = (executions ?? []).filter(e => {
        if (e.symbol !== symbol || e.timeMs <= 0) return false
        const isTP = isTPLinkId(e.orderLinkId)
        if (overlaySettings && isTP && !overlaySettings.showTakeProfit) return false
        if (dirFilter) {
          const lbl = parseOrderLabel(e.orderLinkId ?? '', e.side)
          if (lbl.includes('_LONG')) return dirFilter === 'long'
          if (lbl.includes('_SHORT')) return dirFilter === 'short'
          return dirFilter === 'long' ? e.side === 'Buy' : e.side === 'Sell'
        }
        return true
      })

      // Only show executions whose cycle still has active orders on the exchange.
      // When a cycle finishes its orders are cancelled/removed, so old executions
      // disappear automatically. Executions with an unrecognised linkId (e.g. empty
      // orderLinkId from Bybit) are treated as stale and hidden.
      // Only current-cycle orders form the active set; all other cycles are excluded.
      const activeCycles = new Set<number>()
      for (const o of orders) {
        const n = extractCycleNum(o.orderLinkId)
        if (n === null) continue
        if (stratIdShort && currentCycleNum != null && o.orderLinkId?.includes(stratIdShort) && n !== currentCycleNum) continue
        activeCycles.add(n)
      }

      for (const e of relevantExecs) {
        if (isOtherStrategyLinkId(e.orderLinkId, stratIdShort)) continue
        const cycleNum = extractCycleNum(e.orderLinkId)
        if (cycleNum === null || !activeCycles.has(cycleNum)) continue
        if (stratIdShort && currentCycleNum != null && e.orderLinkId?.includes(stratIdShort) && cycleNum !== currentCycleNum) continue
        if (!e.price || e.price <= 0) continue
        const isTP = isTPLinkId(e.orderLinkId)
        const isBuy = e.side === 'Buy'
        const label = resolveLabel(parseOrderLabel(e.orderLinkId ?? '', e.side))
        const color = isTP ? '#5b8cff' : isBuy ? '#059669' : '#dc2626'
        const usdt = (e.price * parseFloat(e.qty || '0')).toFixed(0)
        const levelLabel = label || (isBuy ? 'Buy' : 'Sell')
        const pct = currentPrice > 0 ? ` ${pctFromPrice(e.price, currentPrice)}` : ''
        priceLines.current.push(series.createPriceLine({
          price: e.price,
          color,
          lineWidth: 1,
          lineStyle: 0,
          axisLabelVisible: true,
          title: `${levelLabel} ${usdt}$${pct}`,
        }))
      }
    }
  }, [positions, orders, executions, symbol, lastPrice, afterSetData, overlaySettings, effectiveDir, stratIdShort, currentCycleNum, strategyLevels])

  // Execution markers
  useEffect(() => {
    const plugin = markersPluginRef.current
    if (!plugin || !candles.length) return
    const markerSlotMap = new Map<number, number>(
      (strategyLevels ?? [])
        .filter(l => l.slot != null)
        .map(l => [l.level_idx, l.slot as number])
    )
    const resolveMarkerLabel = (lbl: string): string => {
      const m = lbl.match(/^L(\d+)$/)
      if (!m) return lbl
      const slot = markerSlotMap.get(parseInt(m[1]))
      return slot != null ? `L(${slot})` : lbl
    }
    const dirFilter = overlaySettings && !overlaySettings.bothDirections && effectiveDir ? effectiveDir : null
    const relevant = (executions ?? []).filter(e => {
      if (e.symbol !== symbol || e.timeMs <= 0) return false
      if (overlaySettings && !overlaySettings.showTakenOrders) return false
      const isTP = isTPLinkId(e.orderLinkId)
      if (overlaySettings && isTP && !overlaySettings.showTakeProfit) return false
      if (dirFilter) {
        const lbl = parseOrderLabel(e.orderLinkId ?? '', e.side)
        if (lbl.includes('_LONG')) return dirFilter === 'long'
        if (lbl.includes('_SHORT')) return dirFilter === 'short'
        return dirFilter === 'long' ? e.side === 'Buy' : e.side === 'Sell'
      }
      return true
    })
    if (!relevant.length) { plugin.setMarkers([]); return }

    // Only show markers for executions whose cycle still has active orders.
    // Manual executions (no cycle number) always show.
    // Build active cycles only from current-cycle orders; exclude all other cycles.
    const activeCycles = new Set<number>()
    for (const o of orders) {
      const n = extractCycleNum(o.orderLinkId)
      if (n === null) continue
      if (stratIdShort && currentCycleNum != null && o.orderLinkId?.includes(stratIdShort) && n !== currentCycleNum) continue
      activeCycles.add(n)
    }

    // Snap each execution to the nearest candle time (binary search)
    type M = { time: number; position: string; shape: string; color: string; size: number; text: string }
    const markers: M[] = []
    for (const e of relevant) {
      const execSec = Math.floor(e.timeMs / 1000)
      let lo = 0, hi = candles.length - 1, candleTime = -1
      while (lo <= hi) {
        const mid = (lo + hi) >> 1
        if ((candles[mid].time as number) <= execSec) { candleTime = candles[mid].time as number; lo = mid + 1 }
        else hi = mid - 1
      }
      if (candleTime === -1) continue

      if (isOtherStrategyLinkId(e.orderLinkId, stratIdShort)) continue
      const cycleNum = extractCycleNum(e.orderLinkId)
      // Skip executions from completed (inactive) cycles; keep only current cycle and manual trades
      if (cycleNum !== null && !activeCycles.has(cycleNum)) continue
      if (stratIdShort && currentCycleNum != null && e.orderLinkId?.includes(stratIdShort) && cycleNum !== null && cycleNum !== currentCycleNum) continue
      const isTP = isTPLinkId(e.orderLinkId)
      const isBuy = e.side === 'Buy'
      const color = isTP ? '#5b8cff' : isBuy ? '#00DC82' : '#ef4444'

      const usdt = (e.price * parseFloat(e.qty || '0')).toFixed(0)
      const levelLabel = resolveMarkerLabel(parseOrderLabel(e.orderLinkId ?? '', e.side))
      const markerText = levelLabel ? `${levelLabel}:${usdt}$` : `${usdt}$`
      markers.push({
        time: candleTime,
        position: isBuy ? 'belowBar' : 'aboveBar',
        shape: isBuy ? 'arrowUp' : 'arrowDown',
        color,
        size: 1,
        text: markerText,
      })
    }
    plugin.setMarkers(markers.sort((a: M, b: M) => a.time - b.time))
  }, [executions, candles, symbol, overlaySettings, effectiveDir, orders, stratIdShort, currentCycleNum, strategyLevels])

  // Position overlays (Bybit-style rectangles with P&L)
  useEffect(() => {
    const container = overlayRef.current
    const series = seriesRef.current
    if (!container || !series) return

    container.innerHTML = ''

    const dirFilter = overlaySettings && !overlaySettings.bothDirections && effectiveDir ? effectiveDir : null
    const filtered = positions.filter(p => {
      if (p.symbol !== symbol) return false
      if (overlaySettings && !overlaySettings.showPositions) return false
      if (dirFilter) return dirFilter === 'long' ? p.side === 'Buy' : p.side === 'Sell'
      return true
    })

    if (filtered.length === 0) return

    // Determine current cycle to know if a position consists of a single fill.
    let currentCycle: number | null = null
    for (const o of orders) {
      const n = extractCycleNum(o.orderLinkId)
      if (n !== null && (currentCycle === null || n > currentCycle)) currentCycle = n
    }
    const execCountBySide: Record<string, number> = {}
    if (currentCycle !== null) {
      for (const e of (executions ?? [])) {
        if (e.symbol !== symbol) continue
        const cycleNum = extractCycleNum(e.orderLinkId)
        if (cycleNum === currentCycle) {
          execCountBySide[e.side] = (execCountBySide[e.side] || 0) + 1
        }
      }
    }

    const els: HTMLDivElement[] = []
    const pnlSpans: HTMLSpanElement[] = []
    const pctSpans: HTMLSpanElement[] = []

    const dark = document.documentElement.classList.contains('dark')
    const bgColor = dark ? 'rgba(10, 10, 15, 0.92)' : 'rgba(255, 255, 255, 0.92)'

    for (const pos of filtered) {
      const el = document.createElement('div')
      const isLong = pos.side === 'Buy'
      const dirColor = isLong ? '#00DC82' : '#ef4444'
      el.className = 'absolute flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-mono font-bold pointer-events-auto'
      el.style.backgroundColor = bgColor
      el.style.color = '#e5e7eb'
      el.style.border = `1px solid ${dirColor}`
      el.style.transition = 'top 0.05s linear'
      el.style.zIndex = '11'
      el.style.whiteSpace = 'nowrap'
      el.style.boxShadow = `0 2px 8px ${dirColor}25`
      el.style.left = '50%'
      el.style.transform = 'translate(-50%, -50%)'

      const pnlSpan = document.createElement('span')
      const pctSpan = document.createElement('span')
      pctSpan.style.opacity = '0.75'
      const sepSpan = document.createElement('span')
      sepSpan.style.opacity = '0.4'
      sepSpan.textContent = '|'
      const sizeSpan = document.createElement('span')
      sizeSpan.style.opacity = '0.75'
      sizeSpan.textContent = `${pos.sizeUsdt.toFixed(0)}$`
      el.appendChild(pnlSpan)
      el.appendChild(pctSpan)
      el.appendChild(sepSpan)
      el.appendChild(sizeSpan)
      container.appendChild(el)
      els.push(el)
      pnlSpans.push(pnlSpan)
      pctSpans.push(pctSpan)
    }

    const update = () => {
      for (let i = 0; i < filtered.length; i++) {
        const pos = filtered[i]
        const price = parseFloat(pos.entryPrice)
        if (isNaN(price)) {
          els[i].style.display = 'none'
          continue
        }
        const y = series.priceToCoordinate(price)
        if (y === null) {
          els[i].style.display = 'none'
        } else {
          els[i].style.display = 'flex'
          els[i].style.top = `${y}px`
        }

        const entry = parseFloat(pos.entryPrice)
        const size = parseFloat(pos.size)
        const livePrice = tickerRef.current?.get(pos.symbol)
        const mark = livePrice ?? parseFloat(pos.markPrice)
        const pnl = livePrice != null
          ? (pos.side === 'Buy' ? (mark - entry) : (entry - mark)) * size
          : parseFloat(pos.unrealisedPnl)
        const pnlPct = entry > 0 && size > 0 ? (pnl / (size * entry)) * 100 : parseFloat(pos.unrealisedPnlPct)
        const pnlColor = pnl >= 0 ? '#00DC82' : '#ef4444'
        pnlSpans[i].style.color = pnlColor
        pnlSpans[i].textContent = `${pnl >= 0 ? '+' : ''}${pnl.toFixed(2)}$`
        pctSpans[i].textContent = `(${pnlPct >= 0 ? '+' : ''}${pnlPct.toFixed(2)}%)`
      }
    }

    update()

    let rafId = requestAnimationFrame(function loop() {
      update()
      rafId = requestAnimationFrame(loop)
    })

    return () => {
      cancelAnimationFrame(rafId)
      container.innerHTML = ''
    }
  }, [positions, orders, executions, symbol, overlaySettings, effectiveDir])

  return (
    <div className="relative w-full h-full">
      <div ref={containerRef} className="absolute inset-0" style={{ zIndex: 1 }} />
      <div ref={overlayRef} className="absolute inset-0 overflow-hidden pointer-events-none" style={{ zIndex: 10 }} />
    </div>
  )
}
