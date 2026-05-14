import { useEffect, useRef, useState } from 'react'
import { createChart, CandlestickSeries, LineSeries, createSeriesMarkers, ColorType } from 'lightweight-charts'
import type { IChartApi } from 'lightweight-charts'
import { EMA, SMA } from 'technicalindicators'
import { toBybitInterval } from '../../hooks/useMarketIndicators'
import type { IndicatorMarker } from '../../hooks/useMarketIndicators'

interface Props {
  symbol: string
  interval: string
  title: string
  badgeColor: 'green' | 'red' | 'neutral'
  badgeLabel: string
  markers: IndicatorMarker[]
  showMA?: boolean
  onClose: () => void
}

function getChartColors(dark: boolean) {
  return {
    layout: {
      background: { type: ColorType.Solid, color: dark ? '#0a0a0f' : '#ffffff' },
      textColor: dark ? '#9ca3af' : '#374151',
    },
    grid: {
      vertLines: { color: dark ? '#1a1a2e' : '#f3f4f6' },
      horzLines: { color: dark ? '#1a1a2e' : '#f3f4f6' },
    },
    rightPriceScale: { borderColor: dark ? '#2d2d3d' : '#e5e7eb' },
    timeScale: { borderColor: dark ? '#2d2d3d' : '#e5e7eb', timeVisible: true },
  }
}

export function SignalChartModal({ symbol, interval, title, badgeColor, badgeLabel, markers, showMA, onClose }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const [chartLoading, setChartLoading] = useState(true)
  const [visibleMarkerCount, setVisibleMarkerCount] = useState(0)
  const isDark = document.documentElement.classList.contains('dark')

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    let cancelled = false
    setChartLoading(true)

    async function init() {
      try {
        const bybitIv = toBybitInterval(interval)
        const res = await fetch(`https://api.bybit.com/v5/market/kline?category=linear&symbol=${symbol}&interval=${bybitIv}&limit=1000`)
        const data = await res.json()
        if (cancelled || !el) return

        const list: string[][] = (data?.result?.list ?? []).slice().reverse()
        const candles = list.map(k => ({
          time: Math.floor(Number(k[0]) / 1000) as unknown as number,
          open: parseFloat(k[1]!),
          high: parseFloat(k[2]!),
          low: parseFloat(k[3]!),
          close: parseFloat(k[4]!),
        }))

        chartRef.current?.remove()
        const chart = createChart(el, {
          width: el.clientWidth,
          height: el.clientHeight,
          ...getChartColors(isDark),
          crosshair: { mode: 1 },
        })
        chartRef.current = chart

        const series = chart.addSeries(CandlestickSeries, {
          upColor: '#00DC82', downColor: '#ef4444',
          borderUpColor: '#00DC82', borderDownColor: '#ef4444',
          wickUpColor: '#00DC82', wickDownColor: '#ef4444',
        })
        series.setData(candles as any)

        if (showMA) {
          const closes = list.map(k => parseFloat(k[4]!))
          const times = candles.map(c => c.time)
          const addMA = (values: number[], color: string, lineWidth: number, maTitle: string) => {
            const offset = closes.length - values.length
            const maData = values.map((v, i) => ({ time: times[i + offset]!, value: v }))
            const maSeries = chart.addSeries(LineSeries, { color, lineWidth: lineWidth as 1 | 2 | 3 | 4, priceLineVisible: false, lastValueVisible: true, title: maTitle })
            maSeries.setData(maData as any)
          }
          addMA(EMA.calculate({ values: closes, period: 20 }), '#3b82f6', 1, 'EMA20')
          addMA(EMA.calculate({ values: closes, period: 50 }), '#f59e0b', 1, 'EMA50')
          addMA(SMA.calculate({ values: closes, period: 200 }), '#8b5cf6', 2, 'SMA200')
        }

        if (markers.length) {
          const firstTime = candles[0]?.time ?? 0
          const lastTime = candles[candles.length - 1]?.time ?? Infinity
          const sorted = [...markers]
            .map(m => ({ ...m, timeSec: Math.floor(m.time / 1000) }))
            .filter(m => m.timeSec >= (firstTime as number) && m.timeSec <= (lastTime as number))
            .sort((a, b) => a.timeSec - b.timeSec)
            .map(m => ({
              time: m.timeSec as unknown as number,
              position: m.direction === 'bullish' ? 'belowBar' as const : 'aboveBar' as const,
              color: m.direction === 'bullish' ? '#00DC82' : '#ef4444',
              shape: m.direction === 'bullish' ? 'arrowUp' as const : 'arrowDown' as const,
              text: m.label,
              size: 1,
            }))
          if (!cancelled) setVisibleMarkerCount(sorted.length)
          if (sorted.length) createSeriesMarkers(series, sorted as any)
        }

        chart.timeScale().fitContent()
      } finally {
        if (!cancelled) setChartLoading(false)
      }
    }

    init()

    function onResize() {
      if (el && chartRef.current) {
        chartRef.current.applyOptions({ width: el.clientWidth, height: el.clientHeight })
      }
    }
    window.addEventListener('resize', onResize)

    return () => {
      cancelled = true
      window.removeEventListener('resize', onResize)
      chartRef.current?.remove()
      chartRef.current = null
    }
  }, [symbol, interval]) // eslint-disable-line react-hooks/exhaustive-deps

  const badgeCls = badgeColor === 'green'
    ? 'bg-green-500/20 text-green-400'
    : badgeColor === 'red'
    ? 'bg-red-500/20 text-red-400'
    : 'bg-gray-500/20 text-gray-400'

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/65 backdrop-blur-sm"
      onClick={e => { if (e.target === e.currentTarget) onClose() }}
    >
      <div className="flex flex-col overflow-hidden rounded-2xl border border-gray-700 bg-gray-900 shadow-2xl"
        style={{ width: '80vw', height: '80vh' }}>
        {/* Header */}
        <div className="flex flex-shrink-0 items-center justify-between border-b border-gray-700 px-4 py-3">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-semibold">{title}</span>
            <span className="text-sm text-gray-400">— {symbol} · {interval}</span>
            <span className={`text-[10px] px-1.5 py-0.5 rounded font-medium ${badgeCls}`}>{badgeLabel}</span>
            <span className="text-[10px] px-1.5 py-0.5 rounded bg-gray-700 text-gray-300">{visibleMarkerCount} маркеров</span>
            {showMA && (
              <span className="flex items-center gap-1 text-xs ml-1">
                <span className="inline-block w-4 h-px bg-blue-500 rounded" />EMA20
                <span className="inline-block w-4 h-px bg-amber-400 rounded ml-1" />EMA50
                <span className="inline-block w-4 h-0.5 bg-violet-500 rounded ml-1" />SMA200
              </span>
            )}
          </div>
          <button onClick={onClose} className="text-gray-400 hover:text-white transition-colors ml-2">✕</button>
        </div>
        {/* Chart */}
        <div className="relative flex-1 min-h-0">
          {chartLoading && (
            <div className="absolute inset-0 flex items-center justify-center">
              <span className="animate-pulse text-sm text-gray-400">Загрузка графика...</span>
            </div>
          )}
          <div ref={containerRef} className="w-full h-full" />
        </div>
      </div>
    </div>
  )
}
