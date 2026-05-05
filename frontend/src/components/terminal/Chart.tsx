import { useEffect, useRef, useCallback } from 'react'
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
  const loadedSymbolRef = useRef<string | null>(null)

  // Refs so applyPriceLines always sees fresh values without being a dependency
  const positionsRef = useRef(positions)
  const ordersRef = useRef(orders)
  const symbolRef = useRef(symbol)
  useEffect(() => { positionsRef.current = positions })
  useEffect(() => { ordersRef.current = orders })
  useEffect(() => { symbolRef.current = symbol })

  const applyPriceLines = useCallback(() => {
    const series = seriesRef.current
    if (!series) return
    for (const line of priceLines.current) {
      try { series.removePriceLine(line) } catch {}
    }
    priceLines.current = []

    const sym = symbolRef.current

    for (const pos of positionsRef.current.filter(p => p.symbol === sym)) {
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

    for (const ord of ordersRef.current.filter(o => o.symbol === sym)) {
      const isLong = ord.side === 'Buy'
      const color = isLong ? '#34d399' : '#f87171'
      if (ord.triggerPrice && parseFloat(ord.triggerPrice) > 0) {
        priceLines.current.push(series.createPriceLine({
          price: parseFloat(ord.triggerPrice),
          color,
          lineWidth: 1,
          lineStyle: 4,
          axisLabelVisible: true,
          title: `${isLong ? 'Buy' : 'Sell'} Stop`,
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
  }, [])

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
    }
  }, [])

  // Candles: setData only on symbol change, update() on live ticks
  useEffect(() => {
    if (!seriesRef.current || !candles.length) return
    if (loadedSymbolRef.current !== symbol) {
      seriesRef.current.setData(candles as any)
      chartRef.current?.timeScale().fitContent()
      loadedSymbolRef.current = symbol
      applyPriceLines()
    } else {
      const last = candles[candles.length - 1]
      if (last) seriesRef.current.update(last as any)
    }
  }, [candles, symbol, applyPriceLines])

  // Price lines: re-apply when positions/orders/symbol change
  useEffect(() => {
    applyPriceLines()
  }, [positions, orders, symbol, applyPriceLines])

  return <div ref={containerRef} className="w-full h-full" />
}
