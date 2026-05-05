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
    }
  }, [])

  useEffect(() => {
    if (!seriesRef.current || !candles.length) return
    seriesRef.current.setData(candles as any)
    chartRef.current?.timeScale().fitContent()
  }, [candles])

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
