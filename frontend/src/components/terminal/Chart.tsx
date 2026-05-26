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
  // Per-level matrix SL orders: SIS_STR-{stratId8}-msl-{slotEncoded}-{seq}
  // Slot encoding: positive/zero → plain digits, negative → "n{abs}" (e.g. slot -1 → "n1")
  if (linkId.includes('-msl-')) {
    const parts = linkId.split('-')
    const raw = parts.length > 3 ? parts[3] : '?'
    const display = raw.startsWith('n') ? `-${raw.slice(1)}` : raw
    return `SL_L(${display})`
  }
  if (/^(SIS_STR|STP)-[a-f0-9]+-tp(-\d+)+$/.test(linkId))
    return side === 'Sell' ? 'TP_LONG' : side === 'Buy' ? 'TP_SHORT' : 'TP'
  if (/^(SIS_STR|STP)-[a-f0-9]+-sl(-\d+)+$/.test(linkId))
    return side === 'Sell' ? 'SL_LONG' : side === 'Buy' ? 'SL_SHORT' : 'SL'
  // Grid virtual: -{repriceGen}-v  Matrix virtual: -v{repriceGen}
  const m = linkId.match(/^(SIS_STR|STR)-[a-f0-9]+-\d+-(\d+)(?:-(?:v\d+|\d+(?:-v)?))?$/)
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
  // Grid virtual: -{repriceGen}-v  Matrix virtual: -v{repriceGen}
  m = linkId.match(/^(?:SIS_STR|STR)-[a-f0-9]+-(\d+)-\d+(?:-(?:v\d+|\d+(?:-v)?))?$/)
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
  const [timeBadge, setTimeBadge] = useState<{ x: number; label: string } | null>(null)

  // Infer direction from live orders or open positions when strategyDir hasn't loaded yet (page refresh).
  const effectiveDir = useMemo(() => {
    if (strategyDir) return strategyDir
    for (const o of orders) {
      if (o.symbol !== symbol) continue  // only current symbol's orders
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

    chart.subscribeCrosshairMove(param => {
      if (!param.point || param.time == null) {
        setTimeBadge(null)
        return
      }
      const d = new Date((param.time as number) * 1000)
      const label = d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
      const w = containerRef.current?.clientWidth ?? 999
      const clampedX = Math.max(36, Math.min(w - 36, param.point.x))
      setTimeBadge({ x: clampedX, label })
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
      const pct = currentPrice > 0 ? ` ${pctFromPrice(price, currentPrice)}` : ''
      priceLines.current.push(series.createPriceLine({
        price,
        color: isLong ? '#059669' : '#dc2626',
        lineWidth: 2,
        lineStyle: 0,
        axisLabelVisible: !stratIdShort,
        title: stratIdShort ? '' : `Вход${pct}`,
      }))
    }

    const virtualLevelIdxs = new Set(
      (strategyLevels ?? [])
        .filter(l => (l.status === 'pending' || l.force_virtual) && l.status !== 'cancelled' && l.status !== 'filled' && l.status !== 'sl_closed')
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

    const drawnTPKeys = new Set<string>()
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
        // Matrix per-level SL (SL_L(N)): closing order — side is opposite to position direction
        if (/^SL_L\(/.test(lbl)) return dirFilter === 'long' ? o.side === 'Sell' : o.side === 'Buy'
        return dirFilter === 'long' ? o.side === 'Buy' : o.side === 'Sell'
      }
      return true
    })) {
      const isLong = ord.side === 'Buy'
      const label = resolveLabel(parseOrderLabel(ord.orderLinkId, ord.side))
      const color = label.startsWith('TP') ? '#5b8cff' : label.startsWith('SL_L') ? '#facc15' : label.startsWith('SL') ? '#f59e0b' : (isLong ? '#34d399' : '#f87171')
      // Deduplicate TP lines: if a duplicate TP order exists on exchange (backend race),
      // only draw the first one so the chart doesn't show two identical TP lines.
      const isTPOrd = label.startsWith('TP')
      if (isTPOrd && drawnTPKeys.has(`${label}-${ord.side}`)) continue
      if (isTPOrd) drawnTPKeys.add(`${label}-${ord.side}`)
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
      for (const lv of (strategyLevels ?? []).filter(l => (l.status === 'pending' || l.force_virtual) && l.status !== 'cancelled' && l.status !== 'filled' && l.status !== 'sl_closed' && l.target_price > 0)) {
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
          if (/^SL_L\(/.test(lbl)) return dirFilter === 'long' ? e.side === 'Sell' : e.side === 'Buy'
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
        // When in strategy view, only count orders from THIS strategy.
        // Other strategies on the same symbol must not leak their cycle numbers here,
        // or their cycle numbers would keep old executions visible after cycle close.
        if (stratIdShort && !o.orderLinkId?.includes(stratIdShort)) continue
        const n = extractCycleNum(o.orderLinkId)
        if (n === null) continue
        if (stratIdShort && currentCycleNum != null && n !== currentCycleNum) continue
        activeCycles.add(n)
      }

      // If a TP order is currently active (placed on exchange), hide TP executions to
      // avoid showing both the [B] placed-order line and a stale execution line simultaneously.
      // This handles the replace-TP race (old TP filled while new one was being placed)
      // and partial-fill cases where both the execution and the remaining order would show.
      const hasActiveTP = orders.some(o => o.symbol === symbol && isTPLinkId(o.orderLinkId))

      // Group executions before drawing: matrix entry levels (L(N)) are merged by slot
      // so multiple re-entries of the same slot appear as one price line.
      type ExecAgg = { totalQty: number; totalValue: number; side: string; isTP: boolean; label: string }
      const execGroups = new Map<string, ExecAgg>()
      for (const e of relevantExecs) {
        if (isOtherStrategyLinkId(e.orderLinkId, stratIdShort)) continue
        const cycleNum = extractCycleNum(e.orderLinkId)
        if (cycleNum === null || !activeCycles.has(cycleNum)) continue
        if (stratIdShort && currentCycleNum != null && e.orderLinkId?.includes(stratIdShort) && cycleNum !== currentCycleNum) continue
        if (!e.price || e.price <= 0) continue
        const isTP = isTPLinkId(e.orderLinkId)
        if (isTP && hasActiveTP) continue
        const isBuy = e.side === 'Buy'
        const label = resolveLabel(parseOrderLabel(e.orderLinkId ?? '', e.side))
        const levelLabel = label || (isBuy ? 'Buy' : 'Sell')
        // Matrix slot entries (L(N) label): merge all re-entries of the same slot into one line
        const isMatrixSlot = /^L\(-?\d+\)$/.test(label)
        const groupKey = isMatrixSlot ? `${label}|${e.side}` : (e.orderLinkId || e.execId)
        const qty = parseFloat(e.qty || '0')
        const existing = execGroups.get(groupKey)
        if (existing) {
          existing.totalQty += qty
          existing.totalValue += e.price * qty
        } else {
          execGroups.set(groupKey, { totalQty: qty, totalValue: e.price * qty, side: e.side, isTP, label: levelLabel })
        }
      }
      for (const [, g] of execGroups) {
        const avgPrice = g.totalQty > 0 ? g.totalValue / g.totalQty : 0
        if (avgPrice <= 0) continue
        const isBuy = g.side === 'Buy'
        const color = g.isTP ? '#5b8cff' : isBuy ? '#059669' : '#dc2626'
        const pct = currentPrice > 0 ? ` ${pctFromPrice(avgPrice, currentPrice)}` : ''
        priceLines.current.push(series.createPriceLine({
          price: avgPrice,
          color,
          lineWidth: 1,
          lineStyle: 0,
          axisLabelVisible: true,
          title: `${g.label} ${g.totalValue.toFixed(0)}$${pct}`,
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
        if (/^SL_L\(/.test(lbl)) return dirFilter === 'long' ? e.side === 'Sell' : e.side === 'Buy'
        return dirFilter === 'long' ? e.side === 'Buy' : e.side === 'Sell'
      }
      return true
    })
    if (!relevant.length) { plugin.setMarkers([]); return }

    // Only show markers for executions whose cycle still has active orders.
    // Manual executions (no cycle number) always show.
    // Build active cycles only from current-cycle orders; exclude all other cycles.
    // When currentCycleNum is null (page just loaded), derive it from the max cycle seen in open orders
    // so that stale markers from old cycles don't appear briefly.
    let effectiveCycleNum: number | null = currentCycleNum ?? null
    if (effectiveCycleNum === null && stratIdShort) {
      for (const o of orders) {
        if (!o.orderLinkId?.includes(stratIdShort)) continue
        const n = extractCycleNum(o.orderLinkId)
        if (n !== null && (effectiveCycleNum === null || n > effectiveCycleNum)) effectiveCycleNum = n
      }
    }
    const activeCycles = new Set<number>()
    for (const o of orders) {
      // Only count THIS strategy's orders — other strategies on the same symbol must not
      // leak their cycle numbers and keep old execution markers visible after cycle close.
      if (stratIdShort && !o.orderLinkId?.includes(stratIdShort)) continue
      const n = extractCycleNum(o.orderLinkId)
      if (n === null) continue
      if (stratIdShort && effectiveCycleNum != null && n !== effectiveCycleNum) continue
      activeCycles.add(n)
    }

    // Strategy closed: no active position and no open orders → cycle finished, clear all markers.
    if (stratIdShort && activeCycles.size === 0 && !positions.some(p => p.symbol === symbol)) {
      plugin.setMarkers([])
      return
    }

    const hasActiveTP = orders.some(o => o.symbol === symbol && isTPLinkId(o.orderLinkId))

    // Group executions by orderLinkId to merge partial fills, or by slot label for matrix
    // re-entries so multiple fills of the same slot appear as one candle marker.
    type M = { time: number; position: string; shape: string; color: string; size: number; text: string }
    type ExecGroup = { execs: typeof relevant; candleTime: number; label: string }
    const groupMap = new Map<string, ExecGroup>()
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
      // In strategy view, skip executions with no cycle number (untracked / manual orders).
      if (cycleNum === null && stratIdShort) continue
      if (cycleNum !== null && !activeCycles.has(cycleNum)) continue
      if (stratIdShort && currentCycleNum != null && e.orderLinkId?.includes(stratIdShort) && cycleNum !== null && cycleNum !== currentCycleNum) continue
      if (isTPLinkId(e.orderLinkId) && hasActiveTP) continue

      const resolvedLabel = resolveMarkerLabel(parseOrderLabel(e.orderLinkId ?? '', e.side))
      const isMatrixSlot = /^L\(-?\d+\)$/.test(resolvedLabel)
      const key = isMatrixSlot ? `${resolvedLabel}|${e.side}` : (e.orderLinkId || e.execId)
      if (!groupMap.has(key)) {
        groupMap.set(key, { execs: [], candleTime, label: resolvedLabel })
      }
      const g = groupMap.get(key)!
      g.execs.push(e)
      if (candleTime > g.candleTime) g.candleTime = candleTime
    }

    const markers: M[] = []
    for (const [, g] of groupMap) {
      const first = g.execs[0]
      const isTP = isTPLinkId(first.orderLinkId)
      const isBuy = first.side === 'Buy'
      const color = isTP ? '#5b8cff' : isBuy ? '#00DC82' : '#ef4444'
      const totalUsdt = g.execs.reduce((sum, e) => sum + e.price * parseFloat(e.qty || '0'), 0)
      // Strip _LONG / _SHORT suffix (direction is already visible from arrow/color).
      const levelLabel = g.label.replace(/_(LONG|SHORT)$/, '')
      const markerText = levelLabel ? `${levelLabel} ${totalUsdt.toFixed(0)}$` : `${totalUsdt.toFixed(0)}$`
      markers.push({
        time: g.candleTime,
        position: isBuy ? 'belowBar' : 'aboveBar',
        shape: isBuy ? 'arrowUp' : 'arrowDown',
        color,
        size: 1,
        text: markerText,
      })
    }
    plugin.setMarkers(markers.sort((a: M, b: M) => a.time - b.time))
  }, [executions, candles, symbol, overlaySettings, effectiveDir, orders, positions, stratIdShort, currentCycleNum, strategyLevels])

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
      {timeBadge && (
        <div
          className="absolute pointer-events-none"
          style={{ bottom: 2, left: timeBadge.x, transform: 'translateX(-50%)', zIndex: 20 }}
        >
          <div
            className="px-2 py-[3px] rounded text-[11px] font-mono font-semibold text-white whitespace-nowrap"
            style={{ background: 'rgba(15,20,35,0.96)', border: '1px solid rgba(91,140,255,0.45)', boxShadow: '0 2px 8px rgba(0,0,0,0.5)' }}
          >
            {timeBadge.label}
          </div>
        </div>
      )}
    </div>
  )
}
