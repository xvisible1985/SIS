// frontend/src/features/log-visualizer/LogVisualizerChart.tsx

import { useEffect, useRef } from 'react'
import {
  createChart, CandlestickSeries, createSeriesMarkers,
  ColorType, type IChartApi, type ISeriesApi,
} from 'lightweight-charts'
import type { LVCandle, MergedEvent } from './types'

interface Props {
  candles: LVCandle[]      // slice of visible candles (grows during animation)
  events:  MergedEvent[]   // events up to current moment (for markers)
}

export function LogVisualizerChart({ candles, events }: Props) {
  const containerRef   = useRef<HTMLDivElement>(null)
  const chartRef       = useRef<IChartApi | null>(null)
  const seriesRef      = useRef<ISeriesApi<'Candlestick'> | null>(null)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const markersRef     = useRef<any>(null)

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
      chartRef.current  = null
      seriesRef.current = null
      markersRef.current = null
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
    // Auto-scroll to latest candle
    chartRef.current?.timeScale().scrollToRealTime()
  }, [candles])

  // Update event markers when list changes
  useEffect(() => {
    if (!markersRef.current) return
    const markers = events.map(ev => ({
      time: Math.floor(ev.tsMs / 1000) as import('lightweight-charts').Time,
      position: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? 'belowBar' : 'aboveBar')
        : 'inBar' as 'aboveBar' | 'belowBar' | 'inBar',
      color: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? '#34d399' : '#f87171')
        : ev.log?.level === 'error' ? '#f87171'
          : ev.log?.level === 'warn' ? '#fbbf24' : '#94a3b8',
      shape: ev.kind === 'level'
        ? (ev.level?.side === 'Buy' ? 'arrowUp' : 'arrowDown')
        : 'circle' as 'arrowUp' | 'arrowDown' | 'circle',
      text: ev.kind === 'level' ? `L${ev.level?.levelIdx}` : undefined,
      size: 1,
    }))
    markersRef.current.setMarkers(markers)
  }, [events])

  return (
    <div ref={containerRef} className="w-full h-full" />
  )
}
