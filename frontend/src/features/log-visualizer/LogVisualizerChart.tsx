// frontend/src/features/log-visualizer/LogVisualizerChart.tsx

import { useEffect, useRef } from 'react'
import {
  createChart, CandlestickSeries, createSeriesMarkers,
  ColorType, type IChartApi, type ISeriesApi, type IPriceLine,
} from 'lightweight-charts'
import type { LVCandle, MergedEvent, LayerSettings } from './types'
import { filterEvents } from './utils'

interface Props {
  candles:       LVCandle[]
  events:        MergedEvent[]
  layerSettings: LayerSettings
}

export function LogVisualizerChart({ candles, events, layerSettings }: Props) {
  const containerRef  = useRef<HTMLDivElement>(null)
  const chartRef      = useRef<IChartApi | null>(null)
  const seriesRef     = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersRef    = useRef<any>(null)
  const priceLinesRef = useRef<IPriceLine[]>([])

  // Initialize chart once on mount
  useEffect(() => {
    if (!containerRef.current) return

    const chart = createChart(containerRef.current, {
      width:  containerRef.current.clientWidth,
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
      timeScale: {
        borderColor: '#2d2d3d',
        timeVisible: true,
      },
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
    markersRef.current = createSeriesMarkers(series)

    // Resize observer
    const ro = new ResizeObserver(() => {
      if (containerRef.current) {
        chart.applyOptions({
          width:  containerRef.current.clientWidth,
          height: containerRef.current.clientHeight,
        })
      }
    })
    ro.observe(containerRef.current)

    return () => {
      ro.disconnect()
      chart.remove()
      chartRef.current      = null
      seriesRef.current     = null
      markersRef.current    = null
      priceLinesRef.current = []
    }
  }, [])

  // Update candle data when slice changes
  useEffect(() => {
    if (!seriesRef.current || candles.length === 0) return
    const data = candles.map(c => ({
      time:  Math.floor(c.t / 1000) as import('lightweight-charts').Time,
      open:  c.o, high: c.h, low: c.l, close: c.c,
    }))
    seriesRef.current.setData(data)
  }, [candles])

  // Update event markers when list or layer settings change
  useEffect(() => {
    if (!markersRef.current) return
    const visible = filterEvents(events, layerSettings)
    const markers = visible.map(ev => {
      type MarkerPos   = 'aboveBar' | 'belowBar' | 'inBar'
      type MarkerShape = 'arrowUp' | 'arrowDown' | 'circle'

      let position: MarkerPos
      let shape: MarkerShape
      let color: string
      let text: string | undefined

      if (ev.kind === 'level' && ev.level) {
        const isBuy = ev.level.side === 'Buy'
        position = isBuy ? 'belowBar' : 'aboveBar'
        shape    = isBuy ? 'arrowUp'  : 'arrowDown'
        color    = isBuy ? '#34d399'  : '#f87171'
        text     = `L${ev.level.levelIdx}`
      } else {
        position = 'inBar'
        shape    = 'circle'
        color    = ev.log?.level === 'error' ? '#f87171'
                 : ev.log?.level === 'warn'  ? '#fbbf24' : '#94a3b8'
        text     = undefined
      }

      return {
        time: Math.floor(ev.tsMs / 1000) as import('lightweight-charts').Time,
        position,
        color,
        shape,
        text,
        size: 1,
      }
    })
    markersRef.current.setMarkers(markers)
  }, [events, layerSettings])

  // Rebuild price lines when events change or showPriceLines is toggled.
  // Only depends on layerSettings.showPriceLines (not the full object) because
  // price lines are only controlled by that flag — other layer settings don't affect them.
  useEffect(() => {
    const series = seriesRef.current
    if (!series) return

    // Remove all existing price lines
    priceLinesRef.current.forEach(pl => series.removePriceLine(pl))
    priceLinesRef.current = []

    if (!layerSettings.showPriceLines) return

    priceLinesRef.current = events
      .filter(ev => ev.kind === 'level' && ev.level)
      .map(ev =>
        series.createPriceLine({
          price:            ev.level!.filledPrice,
          color:            ev.level!.side === 'Buy' ? '#34d399' : '#f87171',
          lineWidth:        1,
          lineStyle:        2,    // LineStyle.Dashed
          axisLabelVisible: true,
          title:            `L${ev.level!.levelIdx}`,
        })
      )
  }, [events, layerSettings.showPriceLines])

  return (
    <div ref={containerRef} className="w-full h-full" />
  )
}
