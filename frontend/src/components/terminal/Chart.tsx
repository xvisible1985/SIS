import { useEffect, useRef, useState } from 'react'
import { createChart, CandlestickSeries, createSeriesMarkers, type IChartApi, type ISeriesApi, ColorType } from 'lightweight-charts'
import type { Candle } from '../../hooks/terminal/useCandles'
import type { Position, ActiveOrder, ChartExecution } from '../../types'

interface Props {
  candles: Candle[]
  candleSymbol: string | null
  positions: Position[]
  orders: ActiveOrder[]
  executions?: ChartExecution[]
  symbol: string
  lastPrice: string | null
  onLoadMore: (before: number) => Promise<void>
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

export function Chart({ candles, candleSymbol, positions, orders, executions, symbol, lastPrice, onLoadMore }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersPluginRef = useRef<any>(null)
  const priceLines = useRef<any[]>([])
  const currentPriceLineRef = useRef<any>(null)
  const loadedSymbolRef = useRef<string | null>(null)
  const prevFirstTimeRef = useRef<number | null>(null)
  const onLoadMoreRef = useRef(onLoadMore)
  const oldestTimeRef = useRef<number | null>(null)
  const loadingMoreRef = useRef(false)
  const lastLoadMoreRef = useRef(0)
  const [afterSetData, setAfterSetData] = useState(0)

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

  // Candles: full setData on symbol change, prepend for history, live update() for ticks
  useEffect(() => {
    if (!seriesRef.current || !candles.length || candleSymbol !== symbol) return

    const isNewSymbol = loadedSymbolRef.current !== symbol
    const isPrepend = !isNewSymbol
      && prevFirstTimeRef.current !== null
      && candles[0].time < prevFirstTimeRef.current

    prevFirstTimeRef.current = candles[0].time

    if (isNewSymbol) {
      seriesRef.current.setData(candles as any)
      chartRef.current?.priceScale('right').applyOptions({ autoScale: true })
      chartRef.current?.timeScale().fitContent()
      loadedSymbolRef.current = symbol
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

  // Price lines — positions and orders
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return

    for (const line of priceLines.current) {
      try { series.removePriceLine(line) } catch {}
    }
    priceLines.current = []

    const currentPrice = lastPrice ? parseFloat(lastPrice) : 0

    for (const pos of positions.filter(p => p.symbol === symbol)) {
      const price = parseFloat(pos.entryPrice)
      if (!price) continue
      const isLong = pos.side === 'Buy'
      const pct = currentPrice > 0 ? ` ${pctFromPrice(price, currentPrice)}` : ''
      const sizeUsdtStr = pos.sizeUsdt > 0 ? ` $${pos.sizeUsdt.toFixed(0)}` : ''
      priceLines.current.push(series.createPriceLine({
        price,
        color: isLong ? '#00DC82' : '#ef4444',
        lineWidth: 2,
        lineStyle: 0,
        axisLabelVisible: true,
        title: `${isLong ? 'Long' : 'Short'}${pct}${sizeUsdtStr}`,
      }))
    }

    for (const ord of orders.filter(o => o.symbol === symbol)) {
      const isLong = ord.side === 'Buy'
      const label = parseOrderLabel(ord.orderLinkId, ord.side)
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
          lineStyle: 4,
          axisLabelVisible: true,
          title: `${prefix}${pct} ${usdt}$`,
        }))
      }
      if (ord.price && parseFloat(ord.price) > 0 && ord.orderType === 'Limit') {
        const p = parseFloat(ord.price)
        const pct = currentPrice > 0 ? ` ${pctFromPrice(p, currentPrice)}` : ''
        const prefix = label || (isLong ? 'Buy' : 'Sell')
        const usdt = (parseFloat(ord.qty) * p).toFixed(0)
        priceLines.current.push(series.createPriceLine({
          price: p,
          color,
          lineWidth: 1,
          lineStyle: 1,
          axisLabelVisible: true,
          title: `${prefix}${pct} ${usdt}$`,
        }))
      }
    }
  }, [positions, orders, symbol, lastPrice, afterSetData])

  // Execution markers
  useEffect(() => {
    const plugin = markersPluginRef.current
    if (!plugin || !candles.length) return
    const relevant = (executions ?? []).filter(e => e.symbol === symbol && e.timeMs > 0)
    if (!relevant.length) { plugin.setMarkers([]); return }

    // Determine current cycle: max cycle number across all executions for this symbol.
    let maxCycle: number | null = null
    for (const e of relevant) {
      const n = extractCycleNum(e.orderLinkId)
      if (n !== null && (maxCycle === null || n > maxCycle)) maxCycle = n
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

      const cycleNum = extractCycleNum(e.orderLinkId)
      const isCurrent = maxCycle === null || cycleNum === maxCycle
      const isTP = isTPLinkId(e.orderLinkId)
      const isBuy = e.side === 'Buy'

      let color: string
      if (isTP) {
        color = isCurrent ? '#5b8cff' : 'rgba(91,140,255,0.3)'
      } else if (isBuy) {
        color = isCurrent ? '#00DC82' : 'rgba(0,220,130,0.3)'
      } else {
        color = isCurrent ? '#ef4444' : 'rgba(239,68,68,0.3)'
      }

      const usdt = (e.price * parseFloat(e.qty || '0')).toFixed(0)
      const levelLabel = parseOrderLabel(e.orderLinkId ?? '', e.side)
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
  }, [executions, candles, symbol])

  return <div ref={containerRef} className="w-full h-full" />
}
