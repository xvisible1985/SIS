import { useEffect, useRef, useState } from 'react'
import { createChart, CandlestickSeries, type IChartApi, type ISeriesApi, ColorType } from 'lightweight-charts'
import type { Candle } from '../../hooks/terminal/useCandles'
import type { Position, ActiveOrder } from '../../types'

interface Props {
  candles: Candle[]
  candleSymbol: string | null
  positions: Position[]
  orders: ActiveOrder[]
  symbol: string
  onLoadMore: (before: number) => Promise<void>
}

export function Chart({ candles, candleSymbol, positions, orders, symbol, onLoadMore }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const priceLines = useRef<any[]>([])
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

  // Price lines
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return

    for (const line of priceLines.current) {
      try { series.removePriceLine(line) } catch {}
    }
    priceLines.current = []

    for (const pos of positions.filter(p => p.symbol === symbol)) {
      const price = parseFloat(pos.entryPrice)
      if (!price) continue
      const isLong = pos.side === 'Buy'
      priceLines.current.push(series.createPriceLine({
        price,
        color: isLong ? '#00DC82' : '#ef4444',
        lineWidth: 2,
        lineStyle: 0,
        axisLabelVisible: true,
        title: `${isLong ? 'Long' : 'Short'} ${pos.size}`,
      }))
    }

    for (const ord of orders.filter(o => o.symbol === symbol)) {
      const isLong = ord.side === 'Buy'
      const color = isLong ? '#34d399' : '#f87171'
      if (ord.triggerPrice && parseFloat(ord.triggerPrice) > 0) {
        const usdt = (parseFloat(ord.qty) * parseFloat(ord.triggerPrice)).toFixed(0)
        priceLines.current.push(series.createPriceLine({
          price: parseFloat(ord.triggerPrice),
          color,
          lineWidth: 1,
          lineStyle: 4,
          axisLabelVisible: true,
          title: `${isLong ? 'Buy' : 'Sell'} ${usdt}$`,
        }))
      }
      if (ord.price && parseFloat(ord.price) > 0 && ord.orderType === 'Limit') {
        priceLines.current.push(series.createPriceLine({
          price: parseFloat(ord.price),
          color,
          lineWidth: 1,
          lineStyle: 1,
          axisLabelVisible: true,
          title: `${isLong ? 'Buy' : 'Sell'} ${ord.qty}`,
        }))
      }
    }
  }, [positions, orders, symbol, afterSetData])

  return <div ref={containerRef} className="w-full h-full" />
}
